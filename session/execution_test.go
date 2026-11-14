package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

type executionConn struct {
	*connection
	execErr, commitErr, rollbackErr error
	execCalls, commits, rollbacks   atomic.Int32
	beginContext                    context.Context
}

func (c *executionConn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (c *executionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.execCalls.Add(1)
	for _, arg := range args {
		if out, ok := arg.Value.(sql.Out); ok {
			*out.Dest.(*int) = 99
		}
	}
	if c.block {
		<-c.disposed
		return nil, ctx.Err()
	}
	if c.execErr != nil {
		return nil, c.execErr
	}
	return driver.RowsAffected(1), nil
}
func (c *executionConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.beginContext = ctx
	return executionTransaction{c}, nil
}

type executionTransaction struct{ c *executionConn }

func (tx executionTransaction) Commit() error   { tx.c.commits.Add(1); return tx.c.commitErr }
func (tx executionTransaction) Rollback() error { tx.c.rollbacks.Add(1); return tx.c.rollbackErr }

type executionConnector struct{ c *executionConn }

func (c executionConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c executionConnector) Driver() driver.Driver                        { return fakeDriver{} }
func executionOpen(t *testing.T, c *executionConn) *session.Session {
	t.Helper()
	s, err := session.Open(context.Background(), executionConnector{c}, options(c.connection))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
func executionFixture() *executionConn {
	return &executionConn{connection: &connection{values: []driver.Value{"one"}, disposed: make(chan struct{})}}
}

func TestExecForwardsOutputAndDoesNotPublishFailure(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	value := 0
	stmt := statement()
	stmt.Args = []any{sql.Named("answer", sql.Out{Dest: &value})}
	result, err := s.Exec(context.Background(), stmt, session.ExecOptions{})
	if err != nil || value != 99 {
		t.Fatalf("output/result: %v %d", err, value)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	c.execErr = errors.New("private-password ORA-01031")
	result, err = s.Exec(context.Background(), stmt, session.ExecOptions{})
	if result != nil || err == nil || err.Error() != "exec failed (ORA-01031)" {
		t.Fatalf("failed exec escaped: %v %v", result, err)
	}
}
func TestExecUnsupportedAndNoReplay(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	s := open(t, c, options(c))
	if _, err := s.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, session.ErrUnsupported) {
		t.Fatal(err)
	}
	e := executionFixture()
	e.execErr = driver.ErrBadConn
	client := executionOpen(t, e)
	if _, err := client.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, driver.ErrBadConn) || e.execCalls.Load() != 1 {
		t.Fatal(err, e.execCalls.Load())
	}
	if _, err := client.Exec(context.Background(), statement(), session.ExecOptions{}); !errors.Is(err, session.ErrUnavailable) || e.execCalls.Load() != 1 {
		t.Fatal("discarded session reused", err)
	}
}
func TestTransactionRetainsPoolLeaseAndCommitsOnce(t *testing.T) {
	c := executionFixture()
	p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 1, QueryTimeout: 20 * time.Millisecond, Open: func(ctx context.Context) (*session.Session, error) {
		return session.Open(ctx, executionConnector{c}, options(c.connection))
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	tx, err := p.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.beginContext.Err() != nil {
		t.Fatal("begin canceled transaction lifetime")
	}
	if _, err := p.Query(context.Background(), statement()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("transaction lost lease", err)
	}
	if _, err := tx.Query(context.Background(), statement()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), statement(), session.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil || tx.Outcome() != session.TxCommitted {
		t.Fatal(err, tx.Outcome())
	}
	if err := tx.Commit(context.Background()); !errors.Is(err, sql.ErrTxDone) || c.commits.Load() != 1 {
		t.Fatal("commit replay", err)
	}
	if _, err := p.Query(context.Background(), statement()); err != nil {
		t.Fatal("session not returned", err)
	}
}
func TestTransactionCommitRejectsCursorAndRollbackJoins(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := tx.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); !errors.Is(err, session.ErrBusy) || c.commits.Load() != 0 {
		t.Fatal("commit raced cursor", err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cursor.Complete() || !c.cursorClosed.Load() || c.rollbacks.Load() != 1 {
		t.Fatal("rollback did not join cursor")
	}
}
func TestTransactionTerminalFailureIsUnknownAndRetires(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			c := executionFixture()
			cause := errors.New("private terminal marker")
			if commit {
				c.commitErr = cause
			} else {
				c.rollbackErr = cause
			}
			s := executionOpen(t, c)
			tx, err := s.BeginTx(context.Background(), session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				err = tx.Commit(context.Background())
			} else {
				err = tx.Rollback(context.Background())
			}
			if !errors.Is(err, cause) || !errors.Is(err, session.ErrOutcomeUnknown) || tx.Outcome() != session.TxUnknown {
				t.Fatal(err, tx.Outcome())
			}
			if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
				t.Fatal("failed transaction reused", err)
			}
		})
	}
}
