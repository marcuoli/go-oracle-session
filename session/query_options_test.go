package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

type queryLimitsClient interface {
	Query(context.Context, session.Statement) (session.Result, error)
	QueryWithOptions(context.Context, session.Statement, session.QueryOptions) (session.Result, error)
	Stream(context.Context, session.Statement, session.StreamOptions) (*session.Stream, error)
}

func queryLimitsTarget(t *testing.T, kind string, c *connection, defaults session.QueryOptions, codec session.Codec) queryLimitsClient {
	t.Helper()
	native := &executionConn{connection: c}
	opts := options(c)
	opts.MaxRows = defaults.MaxRows
	opts.MaxBytes = defaults.MaxBytes
	opts.Codec = codec
	if kind == "pool" {
		pool, err := session.NewPool(t.Context(), session.PoolOptions{Size: 1, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
			return session.Open(ctx, executionConnector{native}, opts)
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := pool.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		return pool
	}
	client, err := session.Open(t.Context(), executionConnector{native}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if kind == "transaction" {
		tx, err := client.BeginTx(context.Background(), session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Error(err)
			}
		})
		return tx
	}
	return client
}

func TestQueryWithOptionsIndependentZeroAndExactBoundaries(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		for _, test := range []struct {
			name           string
			values         []driver.Value
			defaults, call session.QueryOptions
			capacity       bool
		}{
			{"zero inherits", []driver.Value{"one", "two"}, session.QueryOptions{MaxRows: 2, MaxBytes: 6}, session.QueryOptions{}, false},
			{"zero rows inherits", []driver.Value{"a", "b", "c"}, session.QueryOptions{MaxRows: 2, MaxBytes: 8}, session.QueryOptions{MaxBytes: 100}, true},
			{"zero bytes inherits", []driver.Value{"123456789"}, session.QueryOptions{MaxRows: 2, MaxBytes: 8}, session.QueryOptions{MaxRows: 100}, true},
			{"raise rows", []driver.Value{"a", "b", "c"}, session.QueryOptions{MaxRows: 2, MaxBytes: 8}, session.QueryOptions{MaxRows: 3}, false},
			{"lower rows", []driver.Value{"a", "b"}, session.QueryOptions{MaxRows: 3, MaxBytes: 8}, session.QueryOptions{MaxRows: 1}, true},
			{"raise bytes", []driver.Value{"123456789"}, session.QueryOptions{MaxRows: 2, MaxBytes: 8}, session.QueryOptions{MaxBytes: 9}, false},
			{"multibyte exact", []driver.Value{"é", "€"}, session.QueryOptions{MaxRows: 5, MaxBytes: 100}, session.QueryOptions{MaxRows: 2, MaxBytes: 5}, false},
			{"multibyte overflow", []driver.Value{"é", "€"}, session.QueryOptions{MaxRows: 5, MaxBytes: 100}, session.QueryOptions{MaxRows: 2, MaxBytes: 4}, true},
			{"NULL empty exact", []driver.Value{nil, "", []byte("€")}, session.QueryOptions{MaxRows: 5, MaxBytes: 100}, session.QueryOptions{MaxRows: 3, MaxBytes: 3}, false},
			{"NULL counts row", []driver.Value{nil, "", []byte("€")}, session.QueryOptions{MaxRows: 5, MaxBytes: 100}, session.QueryOptions{MaxRows: 2, MaxBytes: 3}, true},
			{"empty inventory", nil, session.QueryOptions{MaxRows: 5, MaxBytes: 100}, session.QueryOptions{MaxRows: 1, MaxBytes: 1}, false},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				c := &connection{values: test.values, disposed: make(chan struct{})}
				target := queryLimitsTarget(t, kind, c, test.defaults, nil)
				result, err := target.QueryWithOptions(t.Context(), statement(), test.call)
				if test.capacity {
					if !errors.Is(err, session.ErrCapacity) || !reflect.DeepEqual(result, session.Result{}) {
						t.Fatalf("capacity prefix: %+v %v", result, err)
					}
				} else {
					if err != nil || len(result.Rows) != len(test.values) || !reflect.DeepEqual(result.Columns, []string{"VALUE"}) {
						t.Fatalf("complete result: %+v %v", result, err)
					}
					for i, value := range test.values {
						want, e := session.ExactValue(value)
						if e != nil || result.Rows[i][0] != want {
							t.Fatalf("raw value changed at%d: %+v want%+v", i, result.Rows[i][0], want)
						}
					}
				}
				if c.calls.Load() != 1 || !c.cursorClosed.Load() {
					t.Fatal("cursor ownership or no replay changed")
				}
			})
		}
	}
}

func TestQueryWithOptionsLeavesLaterDefaultsUnchanged(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		for _, raise := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "/raise", false: "/lower"}[raise], func(t *testing.T) {
				c := &connection{values: []driver.Value{"a", "b", "c"}, disposed: make(chan struct{})}
				defaults := session.QueryOptions{MaxRows: 3, MaxBytes: 3}
				call := session.QueryOptions{MaxRows: 1, MaxBytes: 1}
				if raise {
					defaults = session.QueryOptions{MaxRows: 1, MaxBytes: 1}
					call = session.QueryOptions{MaxRows: 3, MaxBytes: 3}
				}
				target := queryLimitsTarget(t, kind, c, defaults, nil)
				first, err := target.QueryWithOptions(t.Context(), statement(), call)
				if raise {
					if err != nil || len(first.Rows) != 3 {
						t.Fatal("raised call failed", err)
					}
				} else if !errors.Is(err, session.ErrCapacity) {
					t.Fatal("lowered call succeeded", err)
				}
				for _, original := range []bool{true, false} {
					var result session.Result
					if original {
						result, err = target.Query(t.Context(), statement())
					} else {
						result, err = target.QueryWithOptions(t.Context(), statement(), session.QueryOptions{})
					}
					if raise {
						if !errors.Is(err, session.ErrCapacity) || !reflect.DeepEqual(result, session.Result{}) {
							t.Fatal("raised limits changed defaults", result, err)
						}
					} else if err != nil || len(result.Rows) != 3 {
						t.Fatal("lowered limits changed defaults", result, err)
					}
				}
				if c.calls.Load() != 3 {
					t.Fatal("queries replayed or skipped", c.calls.Load())
				}
			})
		}
	}
}

func TestQueryWithOptionsConcurrentCallsKeepTheirOwnLimits(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		t.Run(kind, func(t *testing.T) {
			c := &connection{values: []driver.Value{"a", "b", "c"}, disposed: make(chan struct{})}
			target := queryLimitsTarget(t, kind, c, session.QueryOptions{MaxRows: 2, MaxBytes: 2}, nil)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for index := range 30 {
				workers.Go(func() {
					<-start
					wide := index%3 == 0
					call := session.QueryOptions{}
					if wide {
						call = session.QueryOptions{MaxRows: 3, MaxBytes: 3}
					} else if index%3 == 1 {
						call = session.QueryOptions{MaxRows: 1, MaxBytes: 1}
					}
					result, err := target.QueryWithOptions(t.Context(), statement(), call)
					if wide {
						if err != nil || len(result.Rows) != 3 {
							t.Errorf("wide caller inherited another budget: %+v %v", result, err)
						}
					} else if !errors.Is(err, session.ErrCapacity) || !reflect.DeepEqual(result, session.Result{}) {
						t.Errorf("bounded caller inherited another budget: %+v %v", result, err)
					}
				})
			}
			close(start)
			workers.Wait()
			result, err := target.Query(t.Context(), statement())
			if !errors.Is(err, session.ErrCapacity) || !reflect.DeepEqual(result, session.Result{}) || c.calls.Load() != 31 {
				t.Fatal("later default/no replay changed", result, err, c.calls.Load())
			}
		})
	}
}

func TestQueryWithOptionsNegativeBoundsDoNotWaitForLease(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		for _, call := range []session.QueryOptions{{MaxRows: -1}, {MaxBytes: -1}, {MaxRows: -1, MaxBytes: -1}} {
			t.Run(kind+map[bool]string{true: "/rows", false: "/bytes"}[call.MaxRows < 0], func(t *testing.T) {
				c := &connection{values: []driver.Value{"one"}, disposed: make(chan struct{})}
				target := queryLimitsTarget(t, kind, c, session.QueryOptions{MaxRows: 3, MaxBytes: 20}, nil)
				cursor, err := target.Stream(t.Context(), statement(), session.StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				result, err := target.QueryWithOptions(ctx, statement(), call)
				if !errors.Is(err, session.ErrInvalidQueryOptions) || !reflect.DeepEqual(result, session.Result{}) || ctx.Err() != nil || c.calls.Load() != 1 {
					t.Fatalf("invalid request waited/queried: %+v %v calls%d", result, err, c.calls.Load())
				}
				if !cursor.Next() || cursor.Next() || !cursor.Complete() || cursor.Err() != nil {
					t.Fatal("invalid waiter affected the active cursor", cursor.Err())
				}
				if err = cursor.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err = target.Query(t.Context(), statement()); err != nil {
					t.Fatal("invalid request affected later defaults", err)
				}
			})
		}
	}
}

func TestQueryWithOptionsRetainsCodecAndEveryFailure(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		for _, failureMode := range []string{"terminal-close", "codec", "cancel"} {
			t.Run(kind+"/"+failureMode, func(t *testing.T) {
				terminal, closeFailure, codecFailure := errors.New("private fetch"), errors.New("private close"), errors.New("private codec")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var converted atomic.Int32
				c := &connection{values: []driver.Value{"one", "two"}, disposed: make(chan struct{})}
				if failureMode == "terminal-close" {
					c.rowErr = terminal
					c.closeRowsErr = closeFailure
				}
				codec := func(value any) (session.Value, error) {
					converted.Add(1)
					if value == "two" {
						switch failureMode {
						case "codec":
							return session.Value{}, codecFailure
						case "cancel":
							cancel()
						}
					}
					return session.ExactValue(value)
				}
				target := queryLimitsTarget(t, kind, c, session.QueryOptions{MaxRows: 1, MaxBytes: 1}, codec)
				result, err := target.QueryWithOptions(ctx, statement(), session.QueryOptions{MaxRows: 3, MaxBytes: 20})
				if err == nil || !reflect.DeepEqual(result, session.Result{}) || c.calls.Load() != 1 || !c.cursorClosed.Load() || converted.Load() != 2 || strings.Contains(err.Error(), "private") {
					t.Fatalf("failed prefix/codec/lifecycle: %+v %v", result, err)
				}
				switch failureMode {
				case "terminal-close":
					if !errors.Is(err, terminal) || !errors.Is(err, closeFailure) {
						t.Fatal("terminal/close cause lost", err)
					}
				case "codec":
					if !errors.Is(err, codecFailure) {
						t.Fatal(err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestQueryWithOptionsBudgetsCodecOutputAndPreservesExactValues(t *testing.T) {
	c := &connection{values: []driver.Value{"9007199254740993", nil, ""}, disposed: make(chan struct{})}
	codec := func(value any) (session.Value, error) {
		result, err := session.ExactValue(value)
		if !result.Null {
			result.Text = "prefix:" + result.Text
		}
		return result, err
	}
	target := queryLimitsTarget(t, "session", c, session.QueryOptions{MaxRows: 1, MaxBytes: 1}, codec)
	result, err := target.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 3, MaxBytes: 30})
	if err != nil || result.Rows[0][0].Text != "prefix:9007199254740993" || !result.Rows[1][0].Null || result.Rows[2][0].Text != "prefix:" {
		t.Fatal("codec/exact/NULL semantics changed", result, err)
	}
	result, err = target.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 3, MaxBytes: 29})
	if !errors.Is(err, session.ErrCapacity) || !reflect.DeepEqual(result, session.Result{}) {
		t.Fatal("byte bound ignored converted text", result, err)
	}
}

func TestQueryWithOptionsPreparedFinalizerFailureAndNoReplay(t *testing.T) {
	failure := errors.New("private prepared close")
	c := &preparedConn{execution: executionFixture(), statementCloseErr: failure}
	opts := options(c.execution.connection)
	opts.PreparedContext = true
	opts.MaxRows = 1
	opts.MaxBytes = 1
	client, err := session.Open(t.Context(), preparedConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	result, err := client.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 2, MaxBytes: 20})
	if !errors.Is(err, failure) || !reflect.DeepEqual(result, session.Result{}) || c.prepared.Load() != 1 || c.closed.Load() != 1 || strings.Contains(err.Error(), "private") {
		t.Fatal("prepared finalization lost", result, err)
	}
	if _, err = client.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 2, MaxBytes: 20}); !errors.Is(err, session.ErrUnavailable) || c.prepared.Load() != 1 {
		t.Fatal("failed prepared session replayed", err)
	}
}

func TestQueryWithOptionsBadConnectionNeverReplays(t *testing.T) {
	for _, kind := range []string{"session", "pool", "transaction"} {
		t.Run(kind, func(t *testing.T) {
			c := &connection{queryErr: driver.ErrBadConn, disposed: make(chan struct{})}
			target := queryLimitsTarget(t, kind, c, session.QueryOptions{MaxRows: 1, MaxBytes: 1}, nil)
			result, err := target.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 100, MaxBytes: 100})
			if !errors.Is(err, driver.ErrBadConn) || !reflect.DeepEqual(result, session.Result{}) || c.calls.Load() != 1 {
				t.Fatal("failed query replayed", result, err, c.calls.Load())
			}
		})
	}
}

func TestQueryWithOptionsLargeInventoryAndDefaultStreamRemainIndependent(t *testing.T) {
	values := make([]driver.Value, 8001)
	for i := range values {
		values[i] = "unchanged row"
	}
	values[0] = "first"
	values[len(values)-1] = "last"
	c := &connection{values: values, disposed: make(chan struct{})}
	target := queryLimitsTarget(t, "session", c, session.QueryOptions{MaxRows: 2000, MaxBytes: 8 << 20}, nil)
	result, err := target.QueryWithOptions(t.Context(), statement(), session.QueryOptions{MaxRows: 10000, MaxBytes: 16 << 20})
	if err != nil || len(result.Rows) != 8001 || result.Rows[0][0].Text != "first" || result.Rows[8000][0].Text != "last" {
		t.Fatal("large inventory was truncated", len(result.Rows), err)
	}
	cursor, err := target.Stream(t.Context(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for cursor.Next() {
		var value string
		if err = cursor.Scan(&value); err != nil {
			t.Fatal(err)
		}
		retained++
	}
	if retained != 2000 || !errors.Is(cursor.Err(), session.ErrCapacity) || cursor.Complete() {
		t.Fatal("query override changed streaming defaults", retained, cursor.Err())
	}
	if err = cursor.Close(); !errors.Is(err, session.ErrCapacity) {
		t.Fatal("stream failure lost", err)
	}
}
