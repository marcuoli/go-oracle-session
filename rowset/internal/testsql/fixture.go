// Package testsql provides deterministic database/sql fixtures for unit tests.
// Production packages must not import it. It opens no network/database target
// and is never evidence of native Oracle compatibility.
package testsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// Step describes one deterministic query and cursor outcome, without network I/O.
type Step struct {
	SQL      string
	Columns  []string
	Values   [][]driver.Value
	QueryErr error
	NextErr  error
	CloseErr error
	OnNext   func(int)
}

// Call retains SQL and bound arguments for private fixture assertions.
type Call struct {
	SQL  string
	Args []driver.NamedValue
}

// State synchronizes fixture call and cursor-close observations.
type State struct {
	mu     sync.Mutex
	steps  []Step
	calls  []Call
	closed int
}

// New opens a native database/sql fixture handle and registers its test cleanup.
func New(t testing.TB, steps ...Step) (*sql.DB, *State) {
	t.Helper()
	native, state := NewConnector(steps...)
	db := sql.OpenDB(native)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("fixture handle cleanup: %v", err)
		}
	})
	return db, state
}

// Calls returns an independent snapshot of executed fixture queries.
func (s *State) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Closed reports how many native cursors completed their first Close call.
func (s *State) Closed() int { s.mu.Lock(); defer s.mu.Unlock(); return s.closed }

type connector struct{ s *State }

func (c connector) Connect(context.Context) (driver.Conn, error) { return conn{c.s}, nil }
func (c connector) Driver() driver.Driver                        { return fixtureDriver{} }

type fixtureDriver struct{}

func (fixtureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fixture requires its connector")
}

type conn struct{ s *State }

func (conn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fixture does not prepare") }
func (conn) Close() error                        { return nil }
func (conn) Begin() (driver.Tx, error)           { return nil, errors.New("fixture does not transact") }
func (c conn) QueryContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	index := len(c.s.calls)
	c.s.calls = append(c.s.calls, Call{SQL: sql, Args: append([]driver.NamedValue(nil), args...)})
	if index >= len(c.s.steps) {
		c.s.mu.Unlock()
		return nil, errors.New("unexpected fixture query")
	}
	step := c.s.steps[index]
	c.s.mu.Unlock()
	if step.SQL != "" && step.SQL != sql {
		return nil, errors.New("fixture SQL differs")
	}
	if step.QueryErr != nil {
		return nil, step.QueryErr
	}
	return &rows{s: c.s, step: step}, nil
}

type rows struct {
	s     *State
	step  Step
	index int
	once  sync.Once
}

func (r *rows) Columns() []string { return append([]string(nil), r.step.Columns...) }
func (r *rows) Close() error {
	r.once.Do(func() { r.s.mu.Lock(); r.s.closed++; r.s.mu.Unlock() })
	return r.step.CloseErr
}
func (r *rows) Next(dest []driver.Value) error {
	if r.step.OnNext != nil {
		r.step.OnNext(r.index)
	}
	if r.index >= len(r.step.Values) {
		if r.step.NextErr != nil {
			return r.step.NextErr
		}
		return io.EOF
	}
	copy(dest, r.step.Values[r.index])
	r.index++
	return nil
}

// NewConnector supplies an isolated connector for tests of caller-owned sessions.
// It never dials a network or registers a process-global driver.
func NewConnector(steps ...Step) (driver.Connector, *State) {
	state := &State{steps: append([]Step(nil), steps...)}
	return connector{state}, state
}
