package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	legacyNegotiation "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/advanced_nego"
	legacyNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
)

func TestAutomaticSelectsOnlyOnCleanInitialCryptoRejection(t *testing.T) {
	signal := legacyNegotiation.ErrClientCryptoVersion
	closeFailure := errors.New("close failed")
	for _, tc := range []struct {
		name          string
		mode          Mode
		first         error
		wantAlternate bool
	}{
		{"confirmed", Automatic, signal, true},
		{"wrapped confirmed", Automatic, fmt.Errorf("first response: %w", signal), true},
		{"typed code from unknown phase", Automatic, legacyNetwork.NewOracleError(12269), false},
		{"same text", Automatic, errors.New(signal.Error()), false},
		{"joined close failure", Automatic, errors.Join(signal, closeFailure), false},
		{"legacy signal in wrong direction", Automatic, modern.ErrLegacyServer, false},
		{"authentication failure", Automatic, legacyNetwork.NewOracleError(1017), false},
		{"EOF", Automatic, io.EOF, false},
		{"default still V2 only", V2, signal, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			var calls []Mode
			physical := &fixtureConnection{mode: V3}
			c := &Connector{mode: tc.mode,
				legacy: testBackend{func(got context.Context) (driver.Conn, error) {
					calls = append(calls, V2)
					if got != ctx {
						t.Fatal("original context lost")
					}
					return nil, tc.first
				}},
				modern: testBackend{func(got context.Context) (driver.Conn, error) {
					calls = append(calls, V3)
					if got != ctx {
						t.Fatal("original context lost")
					}
					return physical, nil
				}},
			}
			var attempts []Mode
			conn, err := c.ConnectObserved(ctx, ConnectionObserver{
				After: func(attempt ConnectionAttempt, _ error) error {
					attempts = append(attempts, attempt.Mode)
					return nil
				},
			})
			want := []Mode{V2}
			if tc.wantAlternate {
				want = append(want, V3)
				if err != nil || conn != physical {
					t.Fatalf("alternate connection=%v, err=%v", conn, err)
				}
			} else if conn != nil || !errors.Is(err, tc.first) {
				t.Fatalf("terminal connection=%v, err=%v", conn, err)
			}
			if !slices.Equal(calls, want) || !slices.Equal(attempts, want) {
				t.Fatalf("connects=%v observed=%v want=%v", calls, attempts, want)
			}
		})
	}
}

func TestAutomaticSelectionStopsAfterAlternateFailure(t *testing.T) {
	signal := legacyNegotiation.ErrClientCryptoVersion
	nativeCause := legacyNetwork.NewOracleError(3113)
	first, alternate := 0, 0
	c := &Connector{mode: Automatic,
		legacy: testBackend{func(context.Context) (driver.Conn, error) {
			first++
			return nil, signal
		}},
		modern: testBackend{func(context.Context) (driver.Conn, error) {
			alternate++
			return nil, errors.Join(nativeCause, modern.ErrLegacyServer, driver.ErrBadConn)
		}},
	}
	pool := sql.OpenDB(c)
	t.Cleanup(func() { _ = pool.Close() })
	err := pool.PingContext(t.Context())
	var typed *legacyNetwork.OracleError
	if !errors.Is(err, signal) || !errors.Is(err, modern.ErrLegacyServer) ||
		!errors.As(err, &typed) || typed != nativeCause || errors.Is(err, driver.ErrBadConn) ||
		first != 1 || alternate != 1 {
		t.Fatalf("terminal cause=%v attempts=%d,%d", err, first, alternate)
	}
}

func TestAutomaticSelectionStopsOnCancellationOrObserverFailure(t *testing.T) {
	for _, after := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		signal := legacyNegotiation.ErrClientCryptoVersion
		recordFailure := errors.New("recording failed")
		c := &Connector{mode: Automatic,
			legacy: testBackend{func(context.Context) (driver.Conn, error) {
				if !after {
					cancel()
				}
				return nil, signal
			}},
			modern: testBackend{func(context.Context) (driver.Conn, error) {
				t.Fatal("alternate started after cancellation or observer failure")
				return nil, nil
			}},
		}
		want := error(context.Canceled)
		observer := ConnectionObserver{}
		if after {
			want = recordFailure
			observer.After = func(ConnectionAttempt, error) error { return recordFailure }
		}
		_, err := c.ConnectObserved(ctx, observer)
		cancel()
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
	}
}

func TestNegotiatedSelectionDoesNotRestartAdmissionFromPool(t *testing.T) {
	first, alternate := 0, 0
	c := &Connector{mode: Negotiated,
		modern: testBackend{func(context.Context) (driver.Conn, error) {
			first++
			return nil, modern.ErrLegacyServer
		}},
		legacy: testBackend{func(context.Context) (driver.Conn, error) {
			alternate++
			return nil, driver.ErrBadConn
		}},
	}
	pool := sql.OpenDB(c)
	t.Cleanup(func() { _ = pool.Close() })
	err := pool.PingContext(t.Context())
	if err == nil || errors.Is(err, driver.ErrBadConn) || first != 1 || alternate != 1 {
		t.Fatalf("terminal error=%v attempts=%d,%d", err, first, alternate)
	}
}
