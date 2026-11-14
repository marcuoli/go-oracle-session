package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

type preparedConnector struct{ c *preparedConn }

func (c preparedConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c preparedConnector) Driver() driver.Driver                        { return fakeDriver{} }

type preparedConn struct {
	execution                *executionConn
	statementCloseErr        error
	prepared, closed, legacy atomic.Int32
	seen                     []driver.NamedValue
}

func (c *preparedConn) Prepare(string) (driver.Stmt, error) {
	c.legacy.Add(1)
	return nil, errors.New("legacy prepare")
}
func (c *preparedConn) Close() error { return nil }
func (c *preparedConn) Begin() (driver.Tx, error) {
	c.legacy.Add(1)
	return nil, errors.New("legacy begin")
}
func (c *preparedConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.execution.BeginTx(ctx, o)
}
func (c *preparedConn) CheckNamedValue(*driver.NamedValue) error { return driver.ErrSkip }
func (c *preparedConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	c.prepared.Add(1)
	return &preparedStatement{c}, nil
}

type preparedStatement struct{ c *preparedConn }

func (s *preparedStatement) NumInput() int { return -1 }
func (s *preparedStatement) Close() error  { s.c.closed.Add(1); return s.c.statementCloseErr }
func (s *preparedStatement) Exec([]driver.Value) (driver.Result, error) {
	s.c.legacy.Add(1)
	return nil, errors.New("legacy exec")
}
func (s *preparedStatement) Query([]driver.Value) (driver.Rows, error) {
	s.c.legacy.Add(1)
	return nil, errors.New("legacy query")
}
func (s *preparedStatement) CheckNamedValue(v *driver.NamedValue) error {
	if _, ok := v.Value.(nativeOption); ok {
		return driver.ErrRemoveArgument
	}
	return nil
}
func (s *preparedStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.c.seen = append([]driver.NamedValue(nil), args...)
	for _, arg := range args {
		if out, ok := arg.Value.(nativeOutput); ok {
			*out.dest = 77
		}
	}
	return s.c.execution.ExecContext(ctx, "", args)
}
func (s *preparedStatement) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return &rows{connection: s.c.execution.connection}, nil
}

type nativeOutput struct {
	dest *int
	size int
	in   bool
}
type nativeOption struct{}

func TestPreparedContextPreservesStatementBindConversion(t *testing.T) {
	c := &preparedConn{execution: executionFixture()}
	opts := options(c.execution.connection)
	if _, err := session.Open(context.Background(), preparedConnector{c}, opts); !errors.Is(err, session.ErrUnsupported) {
		t.Fatal("default changed", err)
	}
	c = &preparedConn{execution: executionFixture()}
	opts = options(c.execution.connection)
	opts.PreparedContext = true
	s, err := session.Open(context.Background(), preparedConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if !s.Capabilities().PreparedContext || s.Capabilities().DirectQuery {
		t.Fatal("capabilities do not match driver")
	}
	stdValue, nativeValue := 0, 0
	stmt := statement()
	stmt.Args = []any{sql.Named("std", sql.Out{Dest: &stdValue, In: true}), nativeOption{}, sql.Named("native", nativeOutput{&nativeValue, 42, true})}
	if _, err := s.Exec(context.Background(), stmt, session.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if stdValue != 99 || nativeValue != 77 || len(c.seen) != 2 || c.seen[0].Name != "std" || c.seen[1].Name != "native" || c.seen[1].Value.(nativeOutput).size != 42 {
		t.Fatalf("binds changed: %d %d %+v", stdValue, nativeValue, c.seen)
	}
	if c.legacy.Load() != 0 || c.prepared.Load() != 1 || c.closed.Load() != 1 {
		t.Fatal("prepared route/release", c.legacy.Load(), c.prepared.Load(), c.closed.Load())
	}
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), statement(), session.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPreparedCloseFailureInvalidatesExecution(t *testing.T) {
	cause := errors.New("statement-close marker")
	c := &preparedConn{execution: executionFixture(), statementCloseErr: cause}
	opts := options(c.execution.connection)
	opts.PreparedContext = true
	s, err := session.Open(context.Background(), preparedConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	result, err := s.Exec(context.Background(), statement(), session.ExecOptions{})
	if result != nil || !errors.Is(err, cause) {
		t.Fatal("close failure lost", result, err)
	}
	if _, err := s.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, session.ErrUnavailable) || c.execution.execCalls.Load() != 1 {
		t.Fatal("failed cleanup reused", err)
	}
}
func TestPreparedExecutionDoesNotReplayBadConnection(t *testing.T) {
	c := &preparedConn{execution: executionFixture()}
	c.execution.execErr = driver.ErrBadConn
	opts := options(c.execution.connection)
	opts.PreparedContext = true
	s, err := session.Open(context.Background(), preparedConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, driver.ErrBadConn) || c.execution.execCalls.Load() != 1 {
		t.Fatal("execution replayed", err, c.execution.execCalls.Load())
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestExecCancellationRetiresAndOptOutRetainsParent(t *testing.T) {
	c := executionFixture()
	c.block = true
	opts := options(c.connection)
	opts.QueryTimeout = 10 * time.Millisecond
	s, err := session.Open(context.Background(), executionConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if _, err := s.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if c.execCalls.Load() != 1 {
		t.Fatal("replay")
	}
	c = executionFixture()
	c.block = true
	opts = options(c.connection)
	opts.QueryTimeout = time.Nanosecond
	s2, err := session.Open(context.Background(), executionConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s2.Exec(ctx, statement(), session.ExecOptions{NoQueryTimeout: true}); done <- err }()
	deadline := time.After(time.Second)
	for c.execCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("exec not started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestTransactionCancellationJoinsAndRetires(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "stream"}[streaming], func(t *testing.T) {
			c := executionFixture()
			s := executionOpen(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			tx, err := s.BeginTx(ctx, session.TxOptions{NoQueryTimeout: true})
			if err != nil {
				t.Fatal(err)
			}
			if streaming {
				if _, err := tx.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true}); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tx.Outcome() != session.TxRolledBack || !errors.Is(tx.Err(), context.Canceled) || c.rollbacks.Load() != 1 {
				t.Fatal("cancel cleanup", tx.Outcome(), tx.Err(), c.rollbacks.Load())
			}
		})
	}
}
func TestConcurrentTransactionTerminalCallsAreSingle(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			err := tx.Commit(context.Background())
			if err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if c.commits.Load() != 1 || c.rollbacks.Load() != 0 || tx.Outcome() != session.TxCommitted {
		t.Fatal("terminal replay", c.commits.Load(), c.rollbacks.Load(), tx.Outcome())
	}
}

func TestCanceledCommitNeverDispatches(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Commit(ctx); !errors.Is(err, context.Canceled) || c.commits.Load() != 0 {
		t.Fatalf("canceled commit dispatched: %v commits=%d", err, c.commits.Load())
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type anyConnector struct{ conn driver.Conn }

func (c anyConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c anyConnector) Driver() driver.Driver                        { return fakeDriver{} }

type blockedTerminalConn struct {
	*executionConn
	entered chan struct{}
}

func (c *blockedTerminalConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	c.beginContext = ctx
	return blockedTerminal{c}, nil
}

type blockedTerminal struct{ c *blockedTerminalConn }

func (tx blockedTerminal) Commit() error {
	tx.c.commits.Add(1)
	close(tx.c.entered)
	<-tx.c.disposed
	return errors.New("terminal transport outcome unknown")
}
func (tx blockedTerminal) Rollback() error { tx.c.rollbacks.Add(1); return nil }
func TestInFlightCommitCancellationIsUnknownAndJoined(t *testing.T) {
	c := &blockedTerminalConn{executionConn: executionFixture(), entered: make(chan struct{})}
	opts := options(c.connection)
	opts.CleanupTimeout = 30 * time.Millisecond
	s, err := session.Open(context.Background(), anyConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- tx.Commit(ctx) }()
	<-c.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, session.ErrOutcomeUnknown) || tx.Outcome() != session.TxUnknown {
			t.Fatal(err, tx.Outcome())
		}
	case <-time.After(time.Second):
		t.Fatal("commit cancellation did not join")
	}
	if c.commits.Load() != 1 || c.rollbacks.Load() != 0 {
		t.Fatal("terminal operation replayed")
	}
	if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("unknown commit reused", err)
	}
}
func TestBeginCapabilitiesAndConfirmedRollbackReuse(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	s := open(t, c, options(c))
	if _, err := s.BeginTx(context.Background(), session.TxOptions{}); !errors.Is(err, session.ErrUnsupported) {
		t.Fatal("legacy transaction admitted", err)
	}
	e := executionFixture()
	client := executionOpen(t, e)
	tx, err := client.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil || tx.Outcome() != session.TxRolledBack {
		t.Fatal(err, tx.Outcome())
	}
	if _, err := client.Query(context.Background(), statement()); err != nil {
		t.Fatal("confirmed rollback not reusable", err)
	}
}

type canceledBeginConn struct {
	*executionConn
	cancel context.CancelFunc
}

func (c *canceledBeginConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	c.beginContext = ctx
	c.cancel()
	return executionTransaction{c.executionConn}, nil
}
func TestCancellationDuringBeginRegistration(t *testing.T) {
	for range 1000 {
		c := &canceledBeginConn{executionConn: executionFixture()}
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		s, err := session.Open(context.Background(), anyConnector{c}, options(c.connection))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.BeginTx(ctx, session.TxOptions{})
		if err == nil || tx != nil {
			t.Fatal("canceled startup succeeded", tx, err)
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c.commits.Load() != 0 || c.rollbacks.Load() != 1 {
			t.Fatal("startup cleanup", c.commits.Load(), c.rollbacks.Load())
		}
		cancel()
	}
}

func TestAutomaticRollbackFailurePreservesCause(t *testing.T) {
	for range 100 {
		c := executionFixture()
		marker := errors.New("rollback-cause marker")
		c.rollbackErr = marker
		s, err := session.Open(context.Background(), executionConnector{c}, options(c.connection))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		tx, err := s.BeginTx(ctx, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(tx.Err(), marker) || !errors.Is(tx.Err(), session.ErrOutcomeUnknown) || tx.Outcome() != session.TxUnknown {
			t.Fatalf("automatic rollback cause lost: %v outcome=%v", tx.Err(), tx.Outcome())
		}
	}
}

func TestDriverMetadataRetainsPhysicalOwnership(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	if err := s.WithDriver(context.Background(), func(ctx context.Context, native any) error {
		if native != c {
			t.Fatal("driver wrapper escaped", native)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("metadata context unbounded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	called := false
	err = s.WithDriver(ctx, func(context.Context, any) error { called = true; return nil })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatal("metadata bypassed transaction owner", err, called)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	c2 := &preparedConn{execution: executionFixture()}
	opts := options(c2.execution.connection)
	opts.PreparedContext = true
	prepared, err := session.Open(context.Background(), preparedConnector{c2}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close(context.Background())
	if err := prepared.WithDriver(context.Background(), func(_ context.Context, native any) error {
		if native != c2 {
			t.Error("prepared wrapper escaped")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if c2.prepared.Load() != 0 {
		t.Fatal("metadata emitted magic SQL")
	}
}

func TestDriverMetadataBadConnectionAndPanicRetire(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "bad connection", true: "panic"}[panics], func(t *testing.T) {
			c := executionFixture()
			s := executionOpen(t, c)
			if panics {
				marker := "metadata panic marker"
				func() {
					defer func() {
						if recovered := recover(); recovered != marker {
							t.Error("panic changed", recovered)
						}
					}()
					_ = s.WithDriver(context.Background(), func(context.Context, any) error { panic(marker) })
				}()
			} else {
				if err := s.WithDriver(context.Background(), func(context.Context, any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
					t.Fatal(err)
				}
			}
			if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
				t.Fatal("metadata failure reused", err)
			}
		})
	}
}

type nilResultConn struct{ *executionConn }

func (c nilResultConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, nil
}
func TestNilDriverExecutionResultIsRejected(t *testing.T) {
	c := nilResultConn{executionFixture()}
	s, err := session.Open(context.Background(), anyConnector{c}, options(c.connection))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	result, err := s.Exec(context.Background(), statement(), session.ExecOptions{})
	if err == nil || result != nil {
		t.Fatal("invalid driver result escaped", result, err)
	}
}
