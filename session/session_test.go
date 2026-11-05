package session_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-sqltemplate/reference"
)

type connector struct{ connection *connection }

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.connection, nil }
func (c connector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unexpected driver Open") }

type connection struct {
	values                         []driver.Value
	queryErr, rowErr, closeRowsErr error
	block                          bool
	disposed                       chan struct{}
	once                           sync.Once
	calls                          atomic.Int32
	cursorClosed                   atomic.Bool
}

func (*connection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (*connection) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (*connection) Close() error                        { return nil }
func (*connection) Ping(context.Context) error          { return nil }
func (c *connection) dispose() error                    { c.once.Do(func() { close(c.disposed) }); return nil }
func (c *connection) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	c.calls.Add(1)
	if !strings.Contains(text, "example:readiness:fixture:1") {
		return nil, errors.New("missing command reference")
	}
	if c.block {
		<-c.disposed
		return nil, errors.New("transport closed")
	}
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &rows{connection: c}, nil
}

type rows struct {
	connection *connection
	index      int
}

func (*rows) Columns() []string { return []string{"VALUE"} }
func (r *rows) Close() error    { r.connection.cursorClosed.Store(true); return r.connection.closeRowsErr }
func (r *rows) Next(values []driver.Value) error {
	if r.index < len(r.connection.values) {
		values[0] = r.connection.values[r.index]
		r.index++
		return nil
	}
	if r.connection.rowErr != nil {
		return r.connection.rowErr
	}
	return io.EOF
}
func statement() session.Statement {
	ref, _ := reference.NewReference("example", "readiness", "fixture", 1)
	return session.Statement{Reference: ref, Text: "SELECT value FROM example_table"}
}
func options(c *connection) session.Options {
	return session.Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second, MaxRows: 10, MaxBytes: 1024, Dispose: c.dispose}
}
func open(t *testing.T, c *connection, opts session.Options) *session.Session {
	t.Helper()
	client, err := session.Open(context.Background(), connector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return client
}
func TestExactNullAndCursorOwnership(t *testing.T) {
	c := &connection{values: []driver.Value{"9007199254740993", nil, []byte("1234567890123456.789")}, disposed: make(chan struct{})}
	client := open(t, c, options(c))
	result, err := client.Query(context.Background(), statement())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 || result.Rows[0][0].Text != "9007199254740993" || !result.Rows[1][0].Null || result.Rows[2][0].Text != "1234567890123456.789" || !c.cursorClosed.Load() {
		t.Fatal("exact values/NULL/cursor ownership changed")
	}
}
func TestIncompleteOrAlteredValuesNeverPublishPrefix(t *testing.T) {
	for _, test := range []struct {
		name              string
		values            []driver.Value
		rowErr, closeErr  error
		maxRows, maxBytes int
	}{
		{"row limit", []driver.Value{"one", "two"}, nil, nil, 1, 1024},
		{"byte limit", []driver.Value{"oversized"}, nil, nil, 10, 1},
		{"terminal row failure", []driver.Value{"prefix"}, errors.New("fetch failed"), nil, 10, 1024},
		{"cursor close failure", []driver.Value{"prefix"}, nil, errors.New("close failed"), 10, 1024},
		{"float refused", []driver.Value{float64(9007199254740993)}, nil, nil, 10, 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &connection{values: test.values, rowErr: test.rowErr, closeRowsErr: test.closeErr, disposed: make(chan struct{})}
			opts := options(c)
			opts.MaxRows = test.maxRows
			opts.MaxBytes = test.maxBytes
			client := open(t, c, opts)
			result, err := client.Query(context.Background(), statement())
			if err == nil || len(result.Rows) != 0 || !c.cursorClosed.Load() {
				t.Fatal("partial/altered result escaped")
			}
		})
	}
}
func TestLeasedQueryDoesNotReplayBadConnection(t *testing.T) {
	c := &connection{queryErr: driver.ErrBadConn, disposed: make(chan struct{})}
	client := open(t, c, options(c))
	_, err := client.Query(context.Background(), statement())
	if !errors.Is(err, driver.ErrBadConn) || c.calls.Load() != 1 {
		t.Fatalf("query replayed: calls=%d err=%v", c.calls.Load(), err)
	}
	if _, err := client.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) || c.calls.Load() != 1 {
		t.Fatal("discarded physical session was reused")
	}
}
func TestCancellationClosesOwnedIOAndPreventsReuse(t *testing.T) {
	c := &connection{block: true, disposed: make(chan struct{})}
	opts := options(c)
	opts.QueryTimeout = 10 * time.Millisecond
	client := open(t, c, opts)
	_, err := client.Query(context.Background(), statement())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := client.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) || c.calls.Load() != 1 {
		t.Fatal("cancelled physical session reused")
	}
}
func TestErrorProjectionDoesNotEchoCredentials(t *testing.T) {
	c := &connection{queryErr: errors.New("oracle://private-user:private-password@private-host/service ORA-01031"), disposed: make(chan struct{})}
	client := open(t, c, options(c))
	_, err := client.Query(context.Background(), statement())
	if err == nil || !strings.Contains(err.Error(), "ORA-01031") || strings.Contains(err.Error(), "private-") {
		t.Fatal("unsafe error projection")
	}
}
