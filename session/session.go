// Package session owns one leased connection and bounded, non-replayed queries.
// Callers provide an official QueryerContext driver connector, reviewed SQL and value codec.
package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
	"github.com/marcuoli/go-sqltemplate/reference"
)

// ErrUnavailable identifies a closed or interrupted physical session.
var ErrUnavailable = errors.New("session is unavailable")

// ErrCapacity prevents a retained prefix from becoming a complete result.
var ErrCapacity = errors.New("query result exceeds its declared capacity")

// Value preserves exact transport text and explicit SQL NULL.
type Value struct {
	Text string
	Null bool
}

// Codec admits driver-owned values without float conversion or shared buffers.
type Codec func(any) (Value, error)

// Options bounds every operation. Codec must return promptly. Dispose is required,
// must be idempotent and must unblock/join all owned connector/driver I/O;
// callers qualify that hook and their official driver on actual native targets.
type Options struct {
	ConnectTimeout, QueryTimeout, CleanupTimeout time.Duration
	MaxRows, MaxBytes                            int
	Dispose                                      func() error
	Codec                                        Codec
}

// Statement is one catalog-owned query with a stable namespaced reference.
// Values belong in Args; this package does not authorize arbitrary SQL as safe.
type Statement struct {
	Reference reference.Reference
	Text      string
	Args      []any
}

// Result is published only after every row, terminal error and close succeeds.
type Result struct {
	Columns  []string
	Rows     [][]Value
	Duration time.Duration
}

// Error exposes safe operation/code facts; Error never formats a DSN or binds.
// Unwrap retains programmatic causes, which callers must not log indiscriminately.
type Error struct {
	Operation, Code, Reference string
	cause                      error
}

// Error returns only operation and safe database-code facts.
func (err *Error) Error() string {
	if err.Code != "" {
		return err.Operation + " failed (" + err.Code + ")"
	}
	return err.Operation + " failed"
}

// Unwrap preserves the original cause for bounded programmatic inspection.
func (err *Error) Unwrap() error { return err.cause }

var oracleCode = regexp.MustCompile(`ORA-[0-9]{5}`)

func safeError(operation string, ref reference.Reference, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Operation: operation, Code: oracleCode.FindString(err.Error()), Reference: ref.String(), cause: err}
}

// Session serializes operations on exactly one leased connection. It does not
// retry SQL or pick recovery policy; interrupted sessions cannot be queried again.
type Session struct {
	gate             chan struct{}
	closeOnce        sync.Once
	closeErr         error
	closing          atomic.Bool
	cursorErrors     *cursorErrors
	db               *sql.DB
	connection       *sql.Conn
	options          Options
	closed, unusable bool
	streamMu         sync.Mutex
	activeStream     *Stream
	streamFinalizers sync.WaitGroup
}

// Open authenticates one connector under the explicit connection budget.
// Failed setup closes the leased connection and pool before returning.
func Open(ctx context.Context, connector driver.Connector, options Options) (*Session, error) {
	if connector == nil || options.ConnectTimeout <= 0 || options.QueryTimeout <= 0 || options.CleanupTimeout <= 0 || options.MaxRows < 1 || options.MaxBytes < 1 || options.Dispose == nil {
		return nil, errors.New("invalid session connector/budgets")
	}
	if options.Codec == nil {
		options.Codec = ExactValue
	}
	operation, cancel := context.WithTimeout(ctx, options.ConnectTimeout)
	defer cancel()
	watch, err := sessioncancel.Start(operation, ctx, sessioncancel.Policy{ParentGrace: 100 * time.Millisecond}, sessioncancel.Hooks{Dispose: options.Dispose})
	if err != nil {
		return nil, err
	}
	cursorErrors := &cursorErrors{}
	pool := sql.OpenDB(trackedConnector{Connector: connector, errors: cursorErrors})
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	connection, err := pool.Conn(operation)
	if err == nil {
		err = connection.PingContext(operation)
	}
	_, err = watch.Finish(err)
	if err != nil {
		cleanupErr := cleanup(ctx, options, func() error {
			var connectionErr error
			if connection != nil {
				connectionErr = connection.Close()
				if errors.Is(connectionErr, sql.ErrConnDone) {
					connectionErr = nil
				}
			}
			return errors.Join(connectionErr, pool.Close())
		})
		err = errors.Join(err, cleanupErr, cursorErrors.take())
		return nil, safeError("connect", reference.Reference{}, err)
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Session{db: pool, connection: connection, options: options, gate: gate, cursorErrors: cursorErrors}, nil
}

// Query runs one leased-connection command, never database/sql's retry helper.
// Cursor cleanup and the cancellation watcher join before any result is returned.
func (session *Session) Query(ctx context.Context, statement Statement) (result Result, returnedErr error) {
	operation, cancel := context.WithTimeout(ctx, session.options.QueryTimeout)
	defer cancel()
	// Queueing is part of this query's budget. An expired waiter never disposes
	// the unrelated operation that currently owns the physical connection.
	if err := session.acquire(operation); err != nil {
		return Result{}, safeError("query", statement.Reference, err)
	}
	defer session.release()
	if session.closing.Load() || session.closed || session.unusable {
		return Result{}, ErrUnavailable
	}
	if err := statement.Reference.Validate(); err != nil {
		return Result{}, err
	}
	if statement.Text == "" || len(statement.Text) > 1<<20 {
		return Result{}, errors.New("invalid catalog statement")
	}
	annotated, err := statement.Reference.Annotate(statement.Text)
	if err != nil {
		return Result{}, err
	}
	watch, err := sessioncancel.Start(operation, ctx, sessioncancel.Policy{Query: true, QueryGrace: 0, ParentGrace: 100 * time.Millisecond}, sessioncancel.Hooks{Dispose: session.options.Dispose})
	if err != nil {
		return Result{}, err
	}
	started := time.Now()
	defer func() {
		returnedErr = joinCauses(returnedErr, session.cursorErrors.take())
		outcome, err := watch.Finish(returnedErr)
		if outcome.Cause != "none" || errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
			session.unusable = true
		}
		returnedErr = safeError("query", statement.Reference, err)
		if returnedErr != nil {
			result = Result{}
		} else {
			result.Duration = time.Since(started)
		}
	}()
	rows, err := session.connection.QueryContext(operation, annotated, statement.Args...)
	if err != nil {
		return Result{}, err
	}
	defer func() { returnedErr = errors.Join(returnedErr, rows.Close()) }()
	columns, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	if len(columns) == 0 || len(columns) > 16384 {
		return Result{}, ErrCapacity
	}
	result.Columns = append([]string(nil), columns...)
	result.Rows = [][]Value{}
	retained := 0
	for rows.Next() {
		if err := operation.Err(); err != nil {
			return Result{}, err
		}
		if len(result.Rows) >= session.options.MaxRows {
			return Result{}, ErrCapacity
		}
		raw := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range raw {
			destinations[i] = &raw[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return Result{}, err
		}
		row := make([]Value, len(columns))
		for i, value := range raw {
			admitted, err := session.options.Codec(value)
			if err != nil {
				return Result{}, err
			}
			if admitted.Null && admitted.Text != "" {
				return Result{}, errors.New("NULL transport value contains text")
			}
			if len(admitted.Text) > session.options.MaxBytes-retained {
				return Result{}, ErrCapacity
			}
			retained += len(admitted.Text)
			row[i] = admitted
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	return result, nil
}

// Close uses a bounded cleanup lifetime even after collection cancellation.
// Dispose joins transport closure; no new query or recovery is attempted.
func (session *Session) Close(ctx context.Context) error {
	return session.close(ctx, true)
}

func (session *Session) close(ctx context.Context, joinFinalizers bool) error {
	if session == nil {
		return nil
	}
	session.closeOnce.Do(func() {
		session.closing.Store(true)
		session.cancelActiveStream()
		// Arm disposal before waiting for admission: active I/O cannot extend the
		// cleanup lifetime indefinitely when the qualified hook unblocks it.
		session.closeErr = cleanup(ctx, session.options, func() error {
			<-session.gate
			defer session.release()
			session.closed = true
			connectionErr := session.connection.Close()
			// database/sql already released a lease invalidated by ErrBadConn.
			if errors.Is(connectionErr, sql.ErrConnDone) {
				connectionErr = nil
			}
			return errors.Join(connectionErr, session.db.Close())
		})
	})
	// The gate joins physical I/O, while a stream may still be returning its
	// lease and publishing terminal state. No new finalizer can register after
	// closing is set and this cleanup has acquired/released that gate.
	if joinFinalizers {
		session.streamFinalizers.Wait()
	}
	return safeError("close", reference.Reference{}, session.closeErr)
}

func (session *Session) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-session.gate:
	}
	if err := ctx.Err(); err != nil {
		session.release()
		return err
	}
	return nil
}
func (session *Session) release() { session.gate <- struct{}{} }

func cleanup(ctx context.Context, options Options, closeOwned func() error) error {
	parent := context.WithoutCancel(ctx)
	bounded, cancel := context.WithTimeout(parent, options.CleanupTimeout)
	defer cancel()
	watch, err := sessioncancel.Start(bounded, parent, sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: options.Dispose})
	if err != nil {
		return err
	}
	returnedErr := closeOwned()
	_, returnedErr = watch.Finish(returnedErr)
	return errors.Join(returnedErr, options.Dispose())
}

// ExactValue accepts explicit text, integral values, NULL and exact driver codecs.
// Floating-point values are refused; numeric catalog queries should use TO_CHAR.
func ExactValue(value any) (Value, error) {
	switch value := value.(type) {
	case nil:
		return Value{Null: true}, nil
	case string:
		return Value{Text: value}, nil
	case []byte:
		return Value{Text: string(value)}, nil
	case int64:
		return Value{Text: strconv.FormatInt(value, 10)}, nil
	case int32:
		return Value{Text: strconv.FormatInt(int64(value), 10)}, nil
	case int:
		return Value{Text: strconv.Itoa(value)}, nil
	case uint64:
		return Value{Text: strconv.FormatUint(value, 10)}, nil
	case bool:
		return Value{Text: strconv.FormatBool(value)}, nil
	case time.Time:
		return Value{Text: value.UTC().Format(time.RFC3339Nano)}, nil
	case interface{ String() (string, error) }:
		text, err := value.String()
		return Value{Text: text}, err
	default:
		return Value{}, fmt.Errorf("unsupported exact transport value type %T", value)
	}
}
