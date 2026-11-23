package compat_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/goora/compat"
)

func TestPingObservedSpansTheGuardedOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var order []string
	var insideBefore time.Time
	var minimumElapsed time.Duration
	err := compat.PingObserved(ctx, func(got context.Context) error {
		order = append(order, "ping")
		if got != ctx || !errors.Is(got.Err(), context.Canceled) {
			t.Fatalf("ping context = %v, error = %v", got, got.Err())
		}
		minimumElapsed = time.Since(insideBefore)
		return driver.ErrBadConn
	}, compat.PingObserver{
		Before: func() error {
			order = append(order, "before")
			insideBefore = time.Now()
			time.Sleep(5 * time.Millisecond)
			cancel()
			return nil
		},
		After: func(elapsed time.Duration, cause error) error {
			order = append(order, "after")
			if elapsed < minimumElapsed || cause != driver.ErrBadConn {
				t.Fatalf("after elapsed = %v, want at least %v, cause = %v", elapsed, minimumElapsed, cause)
			}
			return nil
		},
	})
	if err != driver.ErrBadConn || !slices.Equal(order, []string{"before", "ping", "after"}) {
		t.Fatalf("callbacks = %v, error = %v", order, err)
	}
}

func TestPingObservedStopsOnBeforeError(t *testing.T) {
	refused := errors.New("start recorder failed")
	ping := func(context.Context) error {
		t.Fatal("ping ran after start recording failed")
		return nil
	}
	observer := compat.PingObserver{
		Before: func() error { return refused },
		After: func(time.Duration, error) error {
			t.Fatal("after ran after start recording failed")
			return nil
		},
	}
	if err := compat.PingObserved(t.Context(), ping, observer); err != refused {
		t.Fatalf("error = %v, want original start error", err)
	}
}

func TestPingObservedPreservesCompletionCauses(t *testing.T) {
	recordErr := errors.New("completion recorder failed")
	for _, test := range []struct {
		name string
		err  error
	}{
		{"successful ping", nil},
		{"failed ping", driver.ErrBadConn},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := compat.PingObserved(t.Context(), func(context.Context) error {
				calls++
				return test.err
			}, compat.PingObserver{
				After: func(_ time.Duration, cause error) error {
					if calls != 1 || cause != test.err {
						t.Fatalf("calls = %d, cause = %v", calls, cause)
					}
					return recordErr
				},
			})
			if calls != 1 || !errors.Is(err, recordErr) || (test.err != nil && !errors.Is(err, test.err)) {
				t.Fatalf("calls = %d, error = %v", calls, err)
			}
		})
	}
}

func TestPingObservedWithoutCallbacks(t *testing.T) {
	ctx := t.Context()
	calls := 0
	err := compat.PingObserved(ctx, func(got context.Context) error {
		calls++
		if got != ctx {
			t.Fatalf("ping received a different context: %v", got)
		}
		return nil
	}, compat.PingObserver{})
	if calls != 1 || err != nil {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}
