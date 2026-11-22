package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/marcuoli/go-oracle-session/session"
)

type borrowedSampleConnection struct {
	connection
	nexts, closes, opens atomic.Int32
	text                 string
	args                 []driver.NamedValue
	onQuery              func()
}

func (c *borrowedSampleConnection) QueryContext(_ context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	c.calls.Add(1)
	c.text, c.args = text, slices.Clone(args)
	if c.onQuery != nil {
		c.onQuery()
	}
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &borrowedSampleRows{sampleRows{Rows: &rows{connection: &c.connection}, nexts: &c.nexts}}, nil
}

func (c *borrowedSampleConnection) Close() error {
	c.closes.Add(1)
	return nil
}

type borrowedSampleRows struct{ sampleRows }

func (*borrowedSampleRows) ColumnTypeDatabaseTypeName(int) string { return "VARCHAR2" }

type borrowedSampleConnector struct{ conn *borrowedSampleConnection }

func (c borrowedSampleConnector) Connect(context.Context) (driver.Conn, error) {
	c.conn.opens.Add(1)
	return c.conn, nil
}

func (borrowedSampleConnector) Driver() driver.Driver { return fakeDriver{} }

func borrowSample(t *testing.T, native *borrowedSampleConnection) *sql.Conn {
	t.Helper()
	pool := sql.OpenDB(borrowedSampleConnector{native})
	t.Cleanup(func() { _ = pool.Close() })
	conn, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func healthyBorrowedSample(context.Context, any) (session.SampleHealth, error) {
	return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nil
}

func TestSampleBorrowedPreservesNativeCursorAndLease(t *testing.T) {
	native := &borrowedSampleConnection{connection: connection{values: []driver.Value{[]byte("first"), []byte("unread")}}}
	conn := borrowSample(t, native)
	stmt := statement()
	stmt.Text = "SELECT value FROM example_table WHERE id = :id\n"
	stmt.Args = []any{sql.Named("id", int64(9007199254740993))}
	ctx := t.Context()
	probes := 0
	err := session.SampleBorrowed(ctx, conn, stmt, func(rows *sql.Rows) error {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "VALUE" || columns[0].DatabaseTypeName() != "VARCHAR2" {
			t.Fatalf("native metadata was lost: %v, %v", columns, err)
		}
		if !rows.Next() {
			t.Fatal("sample lost the first row:", rows.Err())
		}
		var value sql.RawBytes
		if err := rows.Scan(&value); err != nil || string(value) != "first" {
			t.Fatalf("native scan = %q, %v", value, err)
		}
		return nil
	}, func(probeCtx context.Context, raw any) (session.SampleHealth, error) {
		probes++
		if probeCtx != ctx || raw != native || !native.cursorClosed.Load() || native.nexts.Load() != 1 {
			t.Error("probe ran on a different context or connection, or after draining")
		}
		return healthyBorrowedSample(probeCtx, raw)
	})
	if err != nil || probes != 1 || native.closes.Load() != 0 {
		t.Fatalf("sampling changed lease ownership: error=%v, probes=%d, closes=%d", err, probes, native.closes.Load())
	}
	if native.text != stmt.Text || !slices.Equal(native.args, []driver.NamedValue{{Name: "id", Ordinal: 1, Value: int64(9007199254740993)}}) {
		t.Fatal("sample altered native SQL or binds")
	}
	var next string
	if err := conn.QueryRowContext(ctx, "SELECT value FROM example_table").Scan(&next); err != nil || next != "first" || native.opens.Load() != 1 || native.calls.Load() != 2 {
		t.Fatalf("same lease did not continue: value=%q, error=%v, opens=%d, queries=%d", next, err, native.opens.Load(), native.calls.Load())
	}
}

func TestSampleBorrowedDiscardsUnprovenLease(t *testing.T) {
	closeErr := errors.New("close failed")
	fetchErr := errors.New("fetch failed")
	readErr := errors.New("consumer failed")
	probeErr := errors.New("settlement failed")
	for _, test := range []struct {
		name               string
		readErr, closeErr  error
		fetchErr, probeErr error
		weakHealth         session.SampleHealth
		cancelDuring       string
		wantProbes         int
		want               []error
	}{
		{name: "read and close", readErr: readErr, closeErr: closeErr, want: []error{readErr, closeErr}},
		{name: "fetch", fetchErr: fetchErr, want: []error{fetchErr}},
		{name: "reader cancellation with RawBytes", cancelDuring: "read", want: []error{context.Canceled}},
		{name: "probe failure", probeErr: probeErr, wantProbes: 1, want: []error{probeErr}},
		{name: "protocol proof missing", weakHealth: session.SampleHealth{TransactionUsable: true}, wantProbes: 1},
		{name: "transaction proof missing", weakHealth: session.SampleHealth{ProtocolSettled: true}, wantProbes: 1},
		{name: "finalizer cancellation", cancelDuring: "finalize", wantProbes: 1, want: []error{context.Canceled}},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := &borrowedSampleConnection{connection: connection{values: []driver.Value{[]byte("first")}, rowErr: test.fetchErr, closeRowsErr: test.closeErr}}
			conn := borrowSample(t, native)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probes := 0
			err := session.SampleBorrowed(ctx, conn, statement(), func(rows *sql.Rows) error {
				if !rows.Next() {
					t.Fatal("missing first row")
				}
				var value sql.RawBytes
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				if test.fetchErr != nil && rows.Next() {
					t.Fatal("cursor should have failed after its first row")
				}
				if test.cancelDuring == "read" {
					cancel()
				}
				return test.readErr
			}, func(probeCtx context.Context, raw any) (session.SampleHealth, error) {
				probes++
				if test.cancelDuring == "finalize" {
					cancel()
					return healthyBorrowedSample(probeCtx, raw)
				}
				return test.weakHealth, test.probeErr
			})
			var stage *session.Error
			if !errors.As(err, &stage) || stage.Operation != "sample" || !errors.Is(err, session.ErrUnavailable) || probes != test.wantProbes {
				t.Fatalf("sample returned %v with %d probes", err, probes)
			}
			for _, cause := range test.want {
				if !errors.Is(err, cause) {
					t.Errorf("missing cause %v in %v", cause, err)
				}
			}
			if !native.cursorClosed.Load() || native.closes.Load() != 1 || native.opens.Load() != 1 || native.calls.Load() != 1 {
				t.Fatal("terminal sample did not close exactly once or replayed the query")
			}
			if err := conn.Raw(func(any) error { return nil }); !errors.Is(err, sql.ErrConnDone) {
				t.Fatalf("unproven lease still available: %v", err)
			}
		})
	}
}

func TestSampleBorrowedSeparatesAdmissionErrors(t *testing.T) {
	rejection := errors.New("ORA-00942")
	for _, test := range []struct {
		name, cancel string
		queryErr     error
		want         error
		wrongLease   bool
		wantCalls    int32
	}{
		{name: "ordinary rejection", queryErr: rejection, want: rejection, wantCalls: 1},
		{name: "pre-canceled", cancel: "before", want: context.Canceled},
		{name: "canceled dispatch", cancel: "during", queryErr: rejection, want: context.Canceled, wrongLease: true, wantCalls: 1},
		{name: "bad driver connection", queryErr: driver.ErrBadConn, want: driver.ErrBadConn, wrongLease: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := &borrowedSampleConnection{connection: connection{queryErr: test.queryErr}}
			conn := borrowSample(t, native)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancel == "before" {
				cancel()
			}
			if test.cancel == "during" {
				native.onQuery = cancel
			}
			err := session.SampleBorrowed(ctx, conn, statement(), func(*sql.Rows) error {
				t.Fatal("rejected query reached reader")
				return nil
			}, func(context.Context, any) (session.SampleHealth, error) {
				t.Fatal("rejected query reached finalizer")
				return session.SampleHealth{}, nil
			})
			var stage *session.Error
			if !errors.As(err, &stage) || stage.Operation != "query" || !errors.Is(err, test.want) || native.calls.Load() != test.wantCalls || native.opens.Load() != 1 {
				t.Fatalf("admission returned %v after %d queries", err, native.calls.Load())
			}
			rawErr := conn.Raw(func(any) error { return nil })
			if errors.Is(rawErr, sql.ErrConnDone) != test.wrongLease {
				t.Fatalf("unexpected original lease state: %v", rawErr)
			}
		})
	}
}

func TestSampleBorrowedCleansUpCallbackPanics(t *testing.T) {
	for _, stage := range []string{"read", "finalize"} {
		t.Run(stage, func(t *testing.T) {
			native := &borrowedSampleConnection{}
			conn := borrowSample(t, native)
			marker := errors.New("callback panic")
			func() {
				defer func() {
					if recovered := recover(); recovered != marker {
						t.Errorf("panic = %v, want original marker", recovered)
					}
				}()
				_ = session.SampleBorrowed(t.Context(), conn, statement(), func(*sql.Rows) error {
					if stage == "read" {
						panic(marker)
					}
					return nil
				}, func(context.Context, any) (session.SampleHealth, error) {
					panic(marker)
				})
			}()
			if !native.cursorClosed.Load() || native.closes.Load() != 1 || native.opens.Load() != 1 || native.calls.Load() != 1 {
				t.Fatal("panic escaped before cursor and lease cleanup")
			}
		})
	}
}

func TestSampleBorrowedSkipsReadWhenAdmissionReturnsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	native := &borrowedSampleConnection{onQuery: cancel}
	conn := borrowSample(t, native)
	err := session.SampleBorrowed(ctx, conn, statement(), func(*sql.Rows) error {
		t.Error("canceled cursor reached reader")
		return nil
	}, func(context.Context, any) (session.SampleHealth, error) {
		t.Error("canceled cursor reached finalizer")
		return session.SampleHealth{}, nil
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, session.ErrUnavailable) ||
		!native.cursorClosed.Load() || native.closes.Load() != 1 || native.nexts.Load() != 0 {
		t.Fatalf("canceled cursor was not closed and discarded: %v", err)
	}
}
