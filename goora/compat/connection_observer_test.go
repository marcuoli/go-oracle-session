package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
	"time"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
)

func TestConnectObservedBeforeFailureDoesNotDial(t *testing.T) {
	dialer := &failingDiagnosticDialer{}
	c, err := NewConnector("oracle://reader:secret@fixture.invalid:1521/service", Options{Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	writeFailure := errors.New("start record failed")
	conn, err := c.ConnectObserved(t.Context(), ConnectionObserver{
		Before: func(mode Mode) error {
			if mode != V2 {
				t.Fatalf("default backend = %v", mode)
			}
			return writeFailure
		},
		After: func(ConnectionAttempt, error) error {
			t.Fatal("reported an attempt without calling the driver")
			return nil
		},
	})
	if conn != nil || !errors.Is(err, writeFailure) || dialer.calls != 0 {
		t.Fatalf("connection=%v error=%v dial count=%d", conn, err, dialer.calls)
	}
}

func TestConnectObservedControlsNegotiatedFallback(t *testing.T) {
	writeFailure := errors.New("completion record failed")
	for _, tc := range []struct {
		name   string
		reject bool
		cancel bool
	}{
		{name: "allowed"},
		{name: "observer rejects fallback", reject: true},
		{name: "context canceled between attempts", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events []string
			physical := &fixtureConnection{mode: V2}
			c := &Connector{
				mode: Negotiated,
				modern: testBackend{func(got context.Context) (driver.Conn, error) {
					if got != ctx {
						t.Fatal("modern context changed")
					}
					events = append(events, "connect V3")
					return nil, modern.ErrLegacyServer
				}},
				legacy: testBackend{func(got context.Context) (driver.Conn, error) {
					if got != ctx {
						t.Fatal("legacy context changed")
					}
					events = append(events, "connect V2")
					return physical, nil
				}},
			}
			conn, err := c.ConnectObserved(ctx, ConnectionObserver{
				Before: func(mode Mode) error {
					if mode == V3 {
						events = append(events, "before V3")
					} else {
						events = append(events, "before V2")
					}
					return nil
				},
				After: func(attempt ConnectionAttempt, cause error) error {
					if attempt.Mode == V3 {
						events = append(events, "after V3")
						if attempt.Connected || cause != modern.ErrLegacyServer {
							t.Fatalf("unexpected modern outcome: %+v %v", attempt, cause)
						}
						if tc.reject {
							return writeFailure
						}
						if tc.cancel {
							cancel()
						}
					} else {
						events = append(events, "after V2")
						if !attempt.Connected || cause != nil || attempt.Handshake != nil {
							t.Fatalf("unexpected legacy outcome: %+v %v", attempt, cause)
						}
					}
					return nil
				},
			})
			want := []string{"before V3", "connect V3", "after V3"}
			switch {
			case tc.reject:
				if conn != nil || !errors.Is(err, writeFailure) || !errors.Is(err, modern.ErrLegacyServer) {
					t.Fatalf("observer or legacy cause lost: %v %v", conn, err)
				}
			case tc.cancel:
				if conn != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled admission: %v %v", conn, err)
				}
			default:
				want = append(want, "before V2", "connect V2", "after V2")
				if conn != physical || err != nil {
					t.Fatalf("fallback result: %v %v", conn, err)
				}
			}
			if !slices.Equal(events, want) {
				t.Fatalf("attempt events %v, want %v", events, want)
			}
		})
	}
}

type failingObservationClose struct {
	fixtureConnection
	cause error
}

func (c *failingObservationClose) Close() error {
	c.closed++
	return c.cause
}

func TestConnectObservedClosesEstablishedConnectionOnRecordFailure(t *testing.T) {
	writeFailure := errors.New("completion record failed")
	closeFailure := errors.New("connection cleanup failed")
	physical := &failingObservationClose{cause: closeFailure}
	c := &Connector{legacy: testBackend{func(context.Context) (driver.Conn, error) { return physical, nil }}}
	conn, err := c.ConnectObserved(t.Context(), ConnectionObserver{
		After: func(attempt ConnectionAttempt, cause error) error {
			if !attempt.Connected || cause != nil || physical.closed != 0 {
				t.Fatalf("record did not see the successful attempt: %+v %v", attempt, cause)
			}
			return writeFailure
		},
	})
	if conn != nil || physical.closed != 1 || !errors.Is(err, writeFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("connection=%v close count=%d error=%v", conn, physical.closed, err)
	}
}

func TestConnectObservedDoesNotAllowDatabaseSQLToRetryObserverFailure(t *testing.T) {
	calls := 0
	c := &Connector{legacy: testBackend{func(context.Context) (driver.Conn, error) {
		calls++
		return nil, driver.ErrBadConn
	}}}
	writeFailure := errors.New("completion record failed")
	db := sql.OpenDB(observedTestConnector{Connector: c, observer: ConnectionObserver{
		After: func(ConnectionAttempt, error) error { return writeFailure },
	}})
	defer db.Close()
	if err := db.PingContext(t.Context()); !errors.Is(err, writeFailure) || errors.Is(err, driver.ErrBadConn) || calls != 1 {
		t.Fatalf("error=%v backend attempts=%d", err, calls)
	}
}

type observedTestConnector struct {
	*Connector
	observer ConnectionObserver
}

func (c observedTestConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.ConnectObserved(ctx, c.observer)
}

func TestConnectObservedExpiredContextSkipsHooks(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	conn, err := (&Connector{}).ConnectObserved(ctx, ConnectionObserver{
		Before: func(Mode) error {
			t.Fatal("observer called for an expired request")
			return nil
		},
	})
	if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired context admitted work: %v %v", conn, err)
	}
}

func TestConnectObservedCancellationDuringCompletionClosesConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closeFailure := errors.New("connection cleanup failed")
	physical := &failingObservationClose{cause: closeFailure}
	c := &Connector{legacy: testBackend{func(context.Context) (driver.Conn, error) { return physical, nil }}}
	result, err := c.ConnectObserved(ctx, ConnectionObserver{
		After: func(ConnectionAttempt, error) error {
			cancel()
			return nil
		},
	})
	if result != nil || physical.closed != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, closeFailure) {
		t.Fatalf("connection=%v close count=%d error=%v", result, physical.closed, err)
	}
}

func TestConnectObservedPanicClosesUnreturnedConnection(t *testing.T) {
	physical := &fixtureConnection{}
	c := &Connector{legacy: testBackend{func(context.Context) (driver.Conn, error) { return physical, nil }}}
	defer func() {
		value := recover()
		if value != "observer failure" || physical.closed != 1 {
			t.Fatalf("panic=%v close count=%d", value, physical.closed)
		}
	}()
	_, _ = c.ConnectObserved(t.Context(), ConnectionObserver{
		After: func(ConnectionAttempt, error) error { panic("observer failure") },
	})
}
