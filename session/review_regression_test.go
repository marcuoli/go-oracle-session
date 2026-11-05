package session_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-sqltemplate/reference"
	"io"
	"sync"
	"testing"
	"time"
)

type reviewConnector struct{ c *reviewConn }

func (c reviewConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c reviewConnector) Driver() driver.Driver                        { return reviewDriver{} }

type reviewDriver struct{}

func (reviewDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unexpected open") }

type reviewConn struct {
	entered             chan struct{}
	release             chan struct{}
	once                sync.Once
	releaseOnce         sync.Once
	rowErr, rowCloseErr error
}

func (c *reviewConn) dispose() error {
	if c.release != nil {
		c.releaseOnce.Do(func() { close(c.release) })
	}
	return nil
}
func (*reviewConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (*reviewConn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (*reviewConn) Close() error                        { return nil }
func (*reviewConn) Ping(context.Context) error          { return nil }
func (c *reviewConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
		<-c.release
	}
	return &reviewRows{c: c}, nil
}

type reviewRows struct{ c *reviewConn }

func (*reviewRows) Columns() []string { return []string{"VALUE"} }
func (r *reviewRows) Close() error    { return r.c.rowCloseErr }
func (r *reviewRows) Next([]driver.Value) error {
	if r.c.rowErr != nil {
		return r.c.rowErr
	}
	return io.EOF
}
func reviewStatement() session.Statement {
	ref, _ := reference.NewReference("review", "test", "query", 1)
	return session.Statement{Reference: ref, Text: "SELECT 1 FROM dual"}
}
func reviewOpen(t *testing.T, c *reviewConn) *session.Session {
	t.Helper()
	s, err := session.Open(context.Background(), reviewConnector{c}, session.Options{ConnectTimeout: time.Second, QueryTimeout: 2 * time.Second, CleanupTimeout: 20 * time.Millisecond, MaxRows: 2, MaxBytes: 128, Dispose: c.dispose})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestReviewQueuedQueryHonorsCancellation(t *testing.T) {
	c := &reviewConn{entered: make(chan struct{}), release: make(chan struct{})}
	s := reviewOpen(t, c)
	first := make(chan error, 1)
	go func() { _, err := s.Query(context.Background(), reviewStatement()); first <- err }()
	<-c.entered
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	started := time.Now()
	go func() { _, err := s.Query(ctx, reviewStatement()); second <- err }()
	var blocked bool
	select {
	case <-second:
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	c.dispose()
	<-first
	if blocked {
		<-second
		t.Errorf("queued query still blocked after %s despite 15ms caller deadline", time.Since(started))
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestReviewCloseBudgetIncludesQueuedWait(t *testing.T) {
	c := &reviewConn{entered: make(chan struct{}), release: make(chan struct{})}
	s := reviewOpen(t, c)
	first := make(chan error, 1)
	go func() { _, err := s.Query(context.Background(), reviewStatement()); first <- err }()
	<-c.entered
	closed := make(chan error, 1)
	started := time.Now()
	go func() { closed <- s.Close(context.Background()) }()
	var blocked bool
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	c.dispose()
	<-first
	if blocked {
		<-closed
		t.Errorf("Close still blocked after %s despite 20ms CleanupTimeout", time.Since(started))
	}
}
func TestReviewJoinsFetchAndCloseFailures(t *testing.T) {
	fetchErr, closeErr := errors.New("fetch marker"), errors.New("close marker")
	s := reviewOpen(t, &reviewConn{rowErr: fetchErr, rowCloseErr: closeErr})
	_, err := s.Query(context.Background(), reviewStatement())
	if !errors.Is(err, fetchErr) || !errors.Is(err, closeErr) {
		t.Errorf("joined failure lost a cause: fetch=%v cleanup=%v error=%v", errors.Is(err, fetchErr), errors.Is(err, closeErr), err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
