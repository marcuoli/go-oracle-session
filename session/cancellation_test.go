package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

var nativeAcknowledgement = errors.New("typed native cancellation acknowledgement")

type cancellationValueKey struct{}
type softConnection struct {
	*executionConn
	kind                       string
	slow                       atomic.Bool
	settled                    atomic.Bool
	invalidContext             atomic.Bool
	operations                 atomic.Int32
	entered, broken, breakDone chan struct{}
	breakEntered, breakRelease chan struct{}
	breakDelay                 time.Duration
	natural, unsettled         bool
	cleanupErr                 error
	cleanupReturned            atomic.Bool
	expectedValue              string
	rollbackEntered            chan struct{}
}

func newSoftConnection(kind string) *softConnection {
	c := &softConnection{executionConn: executionFixture(), kind: kind, entered: make(chan struct{}), broken: make(chan struct{}), breakDone: make(chan struct{}), breakEntered: make(chan struct{})}
	c.slow.Store(true)
	return c
}
func (c *softConnection) inspect(ctx context.Context) {
	_, deadline := ctx.Deadline()
	if ctx.Done() != nil || ctx.Err() != nil || deadline || c.expectedValue != "" && ctx.Value(cancellationValueKey{}) != c.expectedValue {
		c.invalidContext.Store(true)
	}
}
func (c *softConnection) wait() error {
	if c.slow.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.broken
		if !c.natural {
			return nativeAcknowledgement
		}
	}
	return nil
}
func (c *softConnection) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	c.inspect(ctx)
	c.operations.Add(1)
	if err := c.wait(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}
func (c *softConnection) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	c.inspect(ctx)
	c.operations.Add(1)
	if c.kind == "fetch" && c.slow.Load() {
		return &softRows{owner: c}, nil
	}
	if err := c.wait(); err != nil {
		return nil, err
	}
	return &rows{connection: c.connection}, nil
}
func (c *softConnection) PrepareContext(ctx context.Context, _ string) (driver.Stmt, error) {
	c.inspect(ctx)
	return &softStatement{owner: c}, nil
}
func (c *softConnection) hooks() *session.Cancellation {
	return &session.Cancellation{QueryGrace: 5 * time.Second,
		Break: func() error {
			close(c.breakEntered)
			close(c.broken)
			if c.breakRelease != nil {
				<-c.breakRelease
			}
			if c.breakDelay > 0 {
				time.Sleep(c.breakDelay)
			}
			c.settled.Store(!c.unsettled)
			close(c.breakDone)
			return nil
		},
		Reusable: func(err error, requested bool) bool {
			return c.settled.Load() && (!requested || errors.Is(err, nativeAcknowledgement)) && expectedNativeCauses(err)
		},
	}
}
func expectedNativeCauses(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !expectedNativeCauses(child) {
				return false
			}
		}
		return true
	}
	if inner := errors.Unwrap(err); inner != nil {
		return expectedNativeCauses(inner)
	}
	return err == nativeAcknowledgement || err == context.Canceled || err == context.DeadlineExceeded
}

type softConnector struct {
	c        *softConnection
	connects *atomic.Int32
}

func (c softConnector) Connect(context.Context) (driver.Conn, error) {
	if c.connects != nil {
		c.connects.Add(1)
	}
	return c.c, nil
}
func (c softConnector) Driver() driver.Driver { return fakeDriver{} }

type softRows struct{ owner *softConnection }

func (*softRows) Columns() []string { return []string{"VALUE"} }
func (r *softRows) Next([]driver.Value) error {
	if r.owner.slow.Load() {
		return r.owner.wait()
	}
	return io.EOF
}
func (r *softRows) Close() error { <-r.owner.breakDone; return r.owner.cleanupErr }

type softStatement struct{ owner *softConnection }

func (*softStatement) NumInput() int { return -1 }
func (*softStatement) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("legacy Exec must not run")
}
func (*softStatement) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("legacy Query must not run")
}
func (s *softStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.owner.ExecContext(ctx, "", args)
}
func (s *softStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.owner.QueryContext(ctx, "", args)
}
func (s *softStatement) Close() error {
	<-s.owner.breakDone
	if !s.owner.cleanupReturned.Swap(true) {
		return s.owner.cleanupErr
	}
	return nil
}

func softOptions(c *softConnection, prepared bool) session.Options {
	o := options(c.connection)
	o.PreparedContext = prepared
	o.QueryTimeout = time.Second
	o.CleanupTimeout = 100 * time.Millisecond
	o.Cancellation = c.hooks()
	return o
}

func TestSettledCancellationRetainsSamePhysicalSession(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"query", "exec", "fetch"} {
			name := kind
			if prepared {
				name += "-prepared"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					c := newSoftConnection(kind)
					c.expectedValue = "trace-value"
					command := context.WithValue(context.Background(), cancellationValueKey{}, c.expectedValue)
					c.breakDelay = time.Second
					if kind == "fetch" {
						c.breakDelay = 0
						c.breakRelease = make(chan struct{})
					}
					var connects atomic.Int32
					s, err := session.Open(context.Background(), softConnector{c, &connects}, softOptions(c, prepared))
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close(context.Background())
					switch kind {
					case "query":
						r, e := s.Query(command, statement())
						if len(r.Rows) != 0 {
							t.Fatal("canceled query published data")
						}
						err = e
					case "exec":
						r, e := s.Exec(command, statement(), session.ExecOptions{})
						if r != nil {
							t.Fatal("canceled Exec published result")
						}
						err = e
					case "fetch":
						cursor, e := s.Stream(command, statement(), session.StreamOptions{})
						if e != nil {
							t.Fatal(e)
						}
						advanced := make(chan bool, 1)
						go func() { advanced <- cursor.Next() }()
						<-c.breakEntered
						select {
						case <-advanced:
							t.Fatal("cursor returned before native break joined")
						default:
						}
						close(c.breakRelease)
						if <-advanced || cursor.Complete() {
							t.Fatal("canceled cursor claimed completion")
						}
						err = cursor.Err()
						if !errors.Is(cursor.Close(), context.DeadlineExceeded) {
							t.Fatal("terminal cursor error was not stable")
						}
					}
					if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, nativeAcknowledgement) {
						t.Fatal("cancellation causes lost", err)
					}
					if c.invalidContext.Load() {
						t.Fatal("driver received a competing cancellation context")
					}
					select {
					case <-c.disposed:
						t.Fatal("qualified canceled session discarded")
					default:
					}
					next, err := s.Query(command, statement())
					if err != nil || len(next.Rows) != 1 || next.Rows[0][0].Text != "one" {
						t.Fatal("same session could not continue", next, err)
					}
					if connects.Load() != 1 || c.operations.Load() != 2 {
						t.Fatal("session replaced or SQL replayed", connects.Load(), c.operations.Load())
					}
					time.Sleep(10 * time.Second)
					select {
					case <-c.disposed:
						t.Fatal("old watcher affected later command")
					default:
					}
				})
			})
		}
	}
}

func TestUnprovenCancellationAndCleanupFailureRetire(t *testing.T) {
	for _, which := range []string{"natural", "unsettled", "cleanup"} {
		t.Run(which, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := newSoftConnection("exec")
				c.natural = which == "natural"
				c.unsettled = which == "unsettled"
				if which == "cleanup" {
					c.cleanupErr = errors.New("unexpected native close error")
				}
				s, err := session.Open(context.Background(), softConnector{c, nil}, softOptions(c, which == "cleanup"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				_, err = s.Exec(context.Background(), statement(), session.ExecOptions{})
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				if c.cleanupErr != nil && !errors.Is(err, c.cleanupErr) {
					t.Fatal("independent cleanup cause lost")
				}
				if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) || c.operations.Load() != 1 {
					t.Fatal("unproven session reused", err)
				}
				select {
				case <-c.disposed:
				default:
					t.Fatal("unproven transport not discarded")
				}
			})
		})
	}
}

func TestPoolQueryDeadlineUsesQueryGraceAndRetainsCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("query")
		c.breakDelay = time.Second
		o := softOptions(c, false)
		o.QueryTimeout = time.Hour
		var connects atomic.Int32
		p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 1, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
			return session.Open(ctx, softConnector{c, &connects}, o)
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close(context.Background())
		_, err = p.Query(context.Background(), statement())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		r, err := p.Query(context.Background(), statement())
		if err != nil || len(r.Rows) != 1 || connects.Load() != 1 {
			t.Fatal("pool capacity retired after qualified query break", r, err)
		}
	})
}

func TestSettledTransactionCommandDoesNotCancelTransactionLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("exec")
		s, err := session.Open(context.Background(), softConnector{c, nil}, softOptions(c, false))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		tx, err := s.BeginTx(context.Background(), session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if c.beginContext.Done() == nil {
			t.Fatal("transaction lifetime lost its cancellation authority")
		}
		_, err = tx.Exec(context.Background(), statement(), session.ExecOptions{})
		if !errors.Is(err, context.DeadlineExceeded) || c.beginContext.Err() != nil {
			t.Fatal("query timeout canceled transaction lifetime", err)
		}
		if err := tx.Commit(context.Background()); err != nil || tx.Outcome() != session.TxCommitted || c.commits.Load() != 1 {
			t.Fatal("qualified transaction could not commit", err, tx.Outcome())
		}
	})
}

func TestStageCauseSurvivesSettledNativeCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("exec")
		s, err := session.Open(context.Background(), softConnector{c, nil}, softOptions(c, false))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		stageCause := errors.New("collection stage ended")
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		go func() { <-c.entered; cancel(stageCause) }()
		_, err = s.Exec(ctx, statement(), session.ExecOptions{})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, stageCause) || !errors.Is(err, nativeAcknowledgement) {
			t.Fatal("stage or native cancellation provenance lost", err)
		}
		if _, err := s.Query(context.Background(), statement()); err != nil {
			t.Fatal("settled stage-canceled physical session unavailable", err)
		}
	})
}

func (c *softConnection) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	c.beginContext = ctx
	return softTransaction{executionTransaction{c.executionConn}, c}, nil
}

type softTransaction struct {
	executionTransaction
	owner *softConnection
}

func (tx softTransaction) Rollback() error {
	if tx.owner.rollbackEntered != nil {
		close(tx.owner.rollbackEntered)
	}
	return tx.executionTransaction.Rollback()
}

func TestParentCancellationJoinsBreakBeforeNativeRollback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("exec")
		c.breakRelease = make(chan struct{})
		c.rollbackEntered = make(chan struct{})
		s, err := session.Open(context.Background(), softConnector{c, nil}, softOptions(c, false))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		tx, err := s.BeginTx(parent, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := tx.Exec(context.Background(), statement(), session.ExecOptions{}); done <- err }()
		<-c.entered
		cancel()
		<-c.breakEntered
		synctest.Wait()
		select {
		case <-c.rollbackEntered:
			t.Error("native rollback raced an unjoined break worker")
		default:
		}
		close(c.breakRelease)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error("parent cancellation cause lost", err)
		}
		_ = tx.Rollback(context.Background())
		select {
		case <-c.rollbackEntered:
		default:
			t.Fatal("native rollback did not run after break joined")
		}
		if c.rollbacks.Load() != 1 {
			t.Fatal("rollback replayed", c.rollbacks.Load())
		}
	})
}

func TestSampleFinalizerSharesNativeCancellationOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("sample")
		c.slow.Store(false)
		c.breakRelease = make(chan struct{})
		opts := softOptions(c, false)
		opts.CleanupTimeout = 3 * time.Second
		s, err := session.Open(context.Background(), softConnector{c, nil}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !cursor.Next() {
			t.Fatal("sample lost first row")
		}
		type result struct {
			outcome session.SampleFinalization
			err     error
		}
		done := make(chan result, 1)
		go func() {
			outcome, err := cursor.FinalizeSample(func(ctx context.Context, _ any) (session.SampleHealth, error) {
				if ctx.Err() != nil {
					c.invalidContext.Store(true)
				}
				<-c.broken
				return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nativeAcknowledgement
			})
			done <- result{outcome, err}
		}()
		<-c.breakEntered
		close(c.breakRelease)
		got := <-done
		if !errors.Is(got.err, context.DeadlineExceeded) || !errors.Is(got.err, nativeAcknowledgement) || !got.outcome.Evidence.ProtocolSettled || !got.outcome.Evidence.TransactionUsable || cursor.Complete() {
			t.Error("sample cancellation did not retain qualified joined health", got.outcome, got.err)
		}
		if c.invalidContext.Load() {
			t.Error("sample finalizer received a competing cancellation context")
		}
		select {
		case <-c.disposed:
			t.Error("secondary sample watcher disposed during qualified break")
		default:
		}
		if _, err := s.Query(context.Background(), statement()); err != nil {
			t.Error("sample's settled physical session unavailable", err)
		}
	})
}

func TestCooperativeSampleFinalizerStopsAfterOwnedCleanupDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSoftConnection("sample")
		c.slow.Store(false)
		opts := softOptions(c, false)
		opts.QueryTimeout = time.Hour
		s, err := session.Open(context.Background(), softConnector{c, nil}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !cursor.Next() {
			t.Fatal("sample lost first row")
		}
		outcome, err := cursor.FinalizeSample(func(ctx context.Context, _ any) (session.SampleHealth, error) {
			<-ctx.Done()
			return session.SampleHealth{}, ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || !outcome.Evidence.TransportDiscarded || cursor.Complete() {
			t.Fatal("cooperative cleanup did not stop on its owned budget", outcome, err)
		}
		if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
			t.Fatal("failed sample transport reused", err)
		}
	})
}

func TestFatalStreamCauseCannotBeOverriddenByReuseProof(t *testing.T) {
	for _, fatal := range []error{driver.ErrBadConn, sql.ErrConnDone} {
		t.Run(fatal.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := newSoftConnection("fetch")
				c.cleanupErr = fatal
				opts := softOptions(c, false)
				opts.Cancellation.Reusable = func(error, bool) bool { return true }
				s, err := session.Open(context.Background(), softConnector{c, nil}, opts)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if cursor.Next() || !errors.Is(cursor.Err(), fatal) || cursor.Complete() {
					t.Fatal("fatal native stream cause lost", cursor.Err())
				}
				if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
					t.Fatal("fatal sql.Conn state reused", err)
				}
			})
		})
	}
}

func TestAcknowledgedCancellationFromPreparedCleanupCanRetainSession(t *testing.T) {
	for _, kind := range []string{"query", "exec"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := newSoftConnection(kind)
				c.cleanupErr = nativeAcknowledgement
				s, err := session.Open(context.Background(), softConnector{c, nil}, softOptions(c, true))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				if kind == "query" {
					_, err = s.Query(context.Background(), statement())
				} else {
					_, err = s.Exec(context.Background(), statement(), session.ExecOptions{})
				}
				if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, nativeAcknowledgement) {
					t.Fatal("native cancellation lost", err)
				}
				if _, err := s.Query(context.Background(), statement()); err != nil {
					t.Fatal("fully acknowledged joined cleanup forced retirement", err)
				}
			})
		})
	}
}
