package session

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
)

// StreamOptions controls an explicitly streaming operation. Zero MaxRows uses
// the session's positive row bound; a positive value overrides it. Negative
// values are invalid. NoQueryTimeout disables only the query deadline, retaining
// parent cancellation and the session's connection and cursor-cleanup budgets.
type StreamOptions struct {
	NoQueryTimeout bool
	MaxRows        int
}

// Stream owns one cursor and its session lease. Next and Scan belong to one
// goroutine, including FinalizeSample. Close, Err and Complete may be called
// concurrently. Scan preserves
// database/sql conversions and driver-owned value lifetimes, including lazy LOBs.
// Buffered Query's Codec/MaxBytes do not apply to native Scan; callers own value
// consumption and memory limits. Completion never certifies that consumption.
type Stream struct {
	mu                sync.Mutex
	session           *Session
	statement         Statement
	ctx               context.Context
	cancel            context.CancelFunc
	watch             *sessioncancel.Watch
	rows              *sql.Rows
	columns           []string
	maxRows, read     int
	current, complete bool
	sample            SampleFinalization
	err               error
	finished          atomic.Bool
	stopCancel        func() bool
	cancelDone        chan struct{}
	onFinish          func() error
}

// Stream starts a non-replayed native cursor. The session remains unavailable to
// other operations until EOF/error/Close cleanup finishes. Early Close reports
// cancellation and makes the physical session unavailable; Close after successful
// EOF is idempotently successful. A stream must be closed when not exhausted.
func (session *Session) Stream(ctx context.Context, statement Statement, options StreamOptions) (*Stream, error) {
	return session.startStream(ctx, ctx, statement, options, nil)
}

func (session *Session) startStream(parent, watchParent context.Context, statement Statement, options StreamOptions, onFinish func() error) (cursor *Stream, returnedErr error) {
	var operation context.Context
	var cancel context.CancelFunc
	if options.NoQueryTimeout {
		operation, cancel = context.WithCancel(parent)
	} else {
		operation, cancel = context.WithTimeout(parent, session.options.QueryTimeout)
	}
	acquired, transferred := false, false
	defer func() {
		if !transferred {
			cancel()
			if acquired {
				session.release()
			}
			if onFinish != nil {
				returnedErr = errors.Join(returnedErr, onFinish())
			}
			returnedErr = safeError("stream", statement.Reference, returnedErr)
		}
	}()
	if options.MaxRows < 0 {
		return nil, errors.New("negative stream row bound")
	}
	text, err := session.statementText(statement)
	if err != nil {
		return nil, err
	}
	if err := session.acquire(operation); err != nil {
		return nil, err
	}
	acquired = true
	if session.closing.Load() || session.closed || session.unusable {
		return nil, ErrUnavailable
	}
	watch, err := sessioncancel.Start(operation, watchParent, sessioncancel.Policy{Query: true, ParentGrace: min(100*time.Millisecond, session.options.CleanupTimeout)}, sessioncancel.Hooks{Dispose: session.options.Dispose})
	if err != nil {
		return nil, err
	}
	maxRows := options.MaxRows
	if maxRows == 0 {
		maxRows = session.options.MaxRows
	}
	cursor = &Stream{session: session, statement: statement, ctx: operation, cancel: cancel, watch: watch, maxRows: maxRows, cancelDone: make(chan struct{}), onFinish: onFinish}
	cursor.mu.Lock()
	// Registration occurs under the session lease, before shutdown can pass
	// that gate and begin waiting for finalizers (including late registration).
	session.streamFinalizers.Add(1)
	active := cursor
	cursor.stopCancel = context.AfterFunc(operation, func() {
		defer active.endCancellation()
		active.mu.Lock()
		active.finishLocked(operation.Err(), false)
		active.mu.Unlock()
	})
	transferred = true
	// database/sql closes a driver cursor inside Next at EOF. Arm the cleanup
	// budget at that driver boundary, including when query deadlines are disabled.
	session.cursorErrors.setCloseHook(cursor.closeDriverRows)
	session.streamMu.Lock()
	session.activeStream = cursor
	if session.closing.Load() {
		cancel()
	}
	session.streamMu.Unlock()
	cursor.rows, err = session.runner().QueryContext(operation, text, statement.Args...)
	if err == nil {
		cursor.columns, err = cursor.rows.Columns()
		if err == nil && (len(cursor.columns) == 0 || len(cursor.columns) > 16384) {
			err = ErrCapacity
		}
	}
	if err != nil {
		finishHere := cursor.finishLocked(err, false)
		cursor.mu.Unlock()
		if finishHere {
			cursor.endCancellation()
		}
		cursor.joinCancellation()
		return nil, cursor.Err()
	}
	cursor.mu.Unlock()
	return cursor, nil
}

func (session *Session) cancelActiveStream() {
	session.streamMu.Lock()
	defer session.streamMu.Unlock()
	if session.activeTx != nil {
		session.activeTx.cancel()
	}
	if session.activeStream != nil {
		session.activeStream.cancel()
	}
}

func (session *Session) available() bool {
	<-session.gate
	defer session.release()
	return !session.closing.Load() && !session.closed && !session.unusable
}

func (cursor *Stream) closeDriverRows(closeRows func() error) error {
	parent := context.WithoutCancel(cursor.ctx)
	bounded, cancel := context.WithTimeout(parent, cursor.session.options.CleanupTimeout)
	defer cancel()
	watch, err := sessioncancel.Start(bounded, parent, sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: cursor.session.options.Dispose})
	if err != nil {
		return err
	}
	_, err = watch.Finish(closeRows())
	return err
}

func (cursor *Stream) finishLocked(cause error, exhausted bool) bool {
	finishHere, _ := cursor.finishWithSampleLocked(cause, exhausted, nil)
	return finishHere
}

func (cursor *Stream) finishWithSampleLocked(cause error, exhausted bool, finalizer SampleFinalizer) (bool, any) {
	if cursor.finished.Swap(true) {
		return false, nil
	}
	// Stop/join ownership is split deliberately: a callback cannot join itself,
	// and a caller joins it only after releasing the mutex needed by that callback.
	finishHere := cursor.stopCancel()
	if cursor.rows != nil {
		cause = joinCauses(cause, cursor.rows.Close())
		cause = joinCauses(cause, cursor.rows.Err())
	}
	cause = joinCauses(cause, cursor.session.cursorErrors.take())
	var qualified bool
	var recovered any
	if finalizer != nil && cause == nil {
		var health SampleHealth
		health, cause, recovered = cursor.probeSample(finalizer)
		qualified = health.ProtocolSettled && health.TransactionUsable && cause == nil
	}
	outcome, cause := cursor.watch.Finish(cause)
	cursor.sample.Evidence = outcome.Evidence
	cursor.complete = exhausted && cause == nil
	if cause != nil || (!cursor.complete && !qualified) {
		cursor.session.unusable = true
		disposeErr := cursor.session.options.Dispose()
		cursor.sample.Evidence.TransportDiscarded = cursor.sample.Evidence.TransportDiscarded || disposeErr == nil
		cause = joinCauses(cause, disposeErr)
	}
	cursor.session.cursorErrors.setCloseHook(nil)
	cursor.session.streamMu.Lock()
	if cursor.session.activeStream == cursor {
		cursor.session.activeStream = nil
	}
	cursor.session.streamMu.Unlock()
	cursor.session.release()
	if cursor.onFinish != nil {
		cause = joinCauses(cause, cursor.onFinish())
	}
	if cause != nil {
		cursor.complete = false
		cursor.sample.Evidence.ProtocolSettled = false
		cursor.sample.Evidence.TransactionUsable = false
	}
	cursor.sample.EOFObserved = cursor.complete
	cursor.err = safeError("stream", cursor.statement.Reference, cause)
	cursor.cancel()
	return finishHere, recovered
}

func (cursor *Stream) joinCancellation() { <-cursor.cancelDone }

func (cursor *Stream) endCancellation() {
	close(cursor.cancelDone)
	cursor.session.streamFinalizers.Done()
}

// Next advances the native cursor. False finalizes and joins cleanup; inspect
// Err and Complete afterward. Exactly MaxRows followed by EOF is complete;
// observing another source row fails with ErrCapacity and an incomplete stream.
func (cursor *Stream) Next() bool {
	cursor.mu.Lock()
	if cursor.finished.Load() {
		cursor.mu.Unlock()
		cursor.joinCancellation()
		return false
	}
	cursor.current = false
	var finishHere bool
	if err := cursor.ctx.Err(); err != nil {
		finishHere = cursor.finishLocked(err, false)
	} else if !cursor.rows.Next() {
		err := cursor.rows.Err()
		finishHere = cursor.finishLocked(err, err == nil)
	} else if cursor.read >= cursor.maxRows {
		finishHere = cursor.finishLocked(ErrCapacity, false)
	} else {
		cursor.read++
		cursor.current = true
		cursor.mu.Unlock()
		return true
	}
	cursor.mu.Unlock()
	if finishHere {
		cursor.endCancellation()
	}
	cursor.joinCancellation()
	return false
}

// Scan delegates conversion to database/sql for the current row. A conversion
// failure closes the cursor and prevents Complete from becoming true. Borrowed
// bytes and lazy driver values must be consumed before advancing/closing.
func (cursor *Stream) Scan(destinations ...any) error {
	cursor.mu.Lock()
	if cursor.finished.Load() || !cursor.current {
		cursor.mu.Unlock()
		return safeError("stream-scan", cursor.statement.Reference, errors.New("no current stream row"))
	}
	err := cursor.rows.Scan(destinations...)
	var finishHere bool
	if err != nil {
		finishHere = cursor.finishLocked(err, false)
		err = cursor.err
	}
	cursor.mu.Unlock()
	if finishHere {
		cursor.endCancellation()
	}
	if err != nil {
		cursor.joinCancellation()
	}
	return err
}

// Columns returns an independent copy of the source projection, including after
// completion. The error result permits the conventional database/sql call shape.
func (cursor *Stream) Columns() ([]string, error) {
	return append([]string(nil), cursor.columns...), nil
}

// Err returns the terminal, safely formatted operation/cleanup error. Underlying
// causes remain available through errors.Is/As; do not log raw error chains.
func (cursor *Stream) Err() error { cursor.mu.Lock(); defer cursor.mu.Unlock(); return cursor.err }

// Complete reports EOF plus successful Scan calls and cursor/cancellation cleanup.
// It is false while active, after early Close, or after any operation failure;
// it does not establish application processing or lazy LOB consumption.
func (cursor *Stream) Complete() bool {
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	return cursor.complete
}

// Close cancels an unexhausted stream and joins its cleanup. It can interrupt a
// concurrent Next via the qualified disposal hook. Early cancellation retires the
// session and is returned as an error; after successful EOF it returns nil.
func (cursor *Stream) Close() error {
	cursor.cancel()
	cursor.mu.Lock()
	var finishHere bool
	if !cursor.finished.Load() {
		finishHere = cursor.finishLocked(cursor.ctx.Err(), false)
	}
	cursor.mu.Unlock()
	if finishHere {
		cursor.endCancellation()
	}
	cursor.joinCancellation()
	return cursor.Err()
}
