package cancel_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
)

func TestSoftBreakJoinsBeforeReusableOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		entered, release := make(chan struct{}), make(chan struct{})
		acknowledgement := errors.New("native cancellation acknowledgement")
		var disposed atomic.Int32
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{Query: true, QueryGrace: 5 * time.Second}, sessioncancel.Hooks{
			Dispose:  func() error { disposed.Add(1); return nil },
			Break:    func() error { close(entered); <-release; return nil },
			Reusable: func(err error, requested bool) bool { return requested && errors.Is(err, acknowledgement) },
		})
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			outcome sessioncancel.Outcome
			err     error
		}
		done := make(chan result, 1)
		go func() { <-entered; outcome, err := watch.Finish(acknowledgement); done <- result{outcome, err} }()
		time.Sleep(2 * time.Second)
		select {
		case <-done:
			t.Fatal("Finish released session before break joined")
		default:
		}
		close(release)
		synctest.Wait()
		got := <-done
		if got.outcome.Health != sessioncancel.Reusable || !got.outcome.Evidence.BreakJoined || !errors.Is(got.err, acknowledgement) || !errors.Is(got.err, context.DeadlineExceeded) || disposed.Load() != 0 {
			t.Fatalf("outcome=%+v err=%v disposed=%d", got.outcome, got.err, disposed.Load())
		}
		_, _ = watch.Finish(nil)
		time.Sleep(10 * time.Second)
		if disposed.Load() != 0 {
			t.Fatal("old watcher disposed later operation")
		}
	})
}

func TestBlockedSoftBreakDisposesEvenAfterOperationReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		entered, disposed := make(chan struct{}), make(chan struct{})
		breakFailure := errors.New("break write interrupted")
		var proofs atomic.Int32
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{Query: true, QueryGrace: time.Second}, sessioncancel.Hooks{
			Break:    func() error { close(entered); <-disposed; return breakFailure },
			Dispose:  func() error { close(disposed); return nil },
			Reusable: func(error, bool) bool { proofs.Add(1); return true },
		})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		outcome, err := watch.Finish(nil)
		if outcome.Health != sessioncancel.Unusable || !outcome.Evidence.BreakJoined || !errors.Is(err, breakFailure) || !errors.Is(err, context.DeadlineExceeded) || proofs.Load() != 0 {
			t.Fatalf("outcome=%+v err=%v proofs=%d", outcome, err, proofs.Load())
		}
	})
}

func TestParentCancellationShortensSoftBreakGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		ctx, stop := context.WithTimeout(parent, time.Second)
		defer stop()
		entered, disposed := make(chan struct{}), make(chan struct{})
		watch, err := sessioncancel.Start(ctx, parent, sessioncancel.Policy{Query: true, QueryGrace: time.Hour, ParentGrace: 100 * time.Millisecond}, sessioncancel.Hooks{
			Break: func() error { close(entered); <-disposed; return nil }, Dispose: func() error { close(disposed); return nil }, Reusable: func(error, bool) bool { return true },
		})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		at := time.Now()
		stage := errors.New("late stage interruption")
		cancel(errors.Join(context.Canceled, stage))
		outcome, err := watch.Finish(nil)
		if time.Since(at) > 100*time.Millisecond || outcome.Cause != "parent" || outcome.Health != sessioncancel.Unusable || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, context.Canceled) || !errors.Is(err, stage) {
			t.Fatalf("elapsed=%v outcome=%+v err=%v", time.Since(at), outcome, err)
		}
	})
}

func TestUnprovenAndPanickingSoftHooksRetire(t *testing.T) {
	for _, which := range []string{"unknown", "break-panic", "proof-panic"} {
		t.Run(which, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				entered := make(chan struct{})
				var disposed atomic.Int32
				watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{Query: true, QueryGrace: time.Second}, sessioncancel.Hooks{
					Break: func() error {
						close(entered)
						if which == "break-panic" {
							panic("private panic value")
						}
						return nil
					},
					Reusable: func(error, bool) bool {
						if which == "proof-panic" {
							panic("private panic value")
						}
						return false
					}, Dispose: func() error { disposed.Add(1); return nil },
				})
				if err != nil {
					t.Fatal(err)
				}
				<-entered
				outcome, err := watch.Finish(nil)
				if outcome.Health != sessioncancel.Unusable || disposed.Load() != 1 || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(outcome, err, disposed.Load())
				}
				if which != "unknown" && !errors.Is(err, sessioncancel.ErrHookPanic) {
					t.Fatal("hook panic was lost")
				}
			})
		})
	}
}

func TestOwnedCleanupPreservesActiveBreakGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		entered, finished := make(chan struct{}), make(chan struct{})
		acknowledgement := errors.New("native acknowledgement")
		var disposed atomic.Int32
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{Query: true, QueryGrace: 5 * time.Second}, sessioncancel.Hooks{
			Break:    func() error { close(entered); time.Sleep(2 * time.Second); close(finished); return nil },
			Dispose:  func() error { disposed.Add(1); return nil },
			Reusable: func(err error, requested bool) bool { return requested && errors.Is(err, acknowledgement) },
		})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		if err := watch.Cleanup(100*time.Millisecond, func(context.Context) error { <-finished; return nil }); err != nil {
			t.Fatal("secondary close timer shortened native grace", err)
		}
		outcome, err := watch.Finish(acknowledgement)
		if outcome.Health != sessioncancel.Reusable || disposed.Load() != 0 || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(outcome, err, disposed.Load())
		}
	})
}

func TestOwnedCleanupBoundsHealthyCloseAndJoinsDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		disposed, closed := make(chan struct{}), make(chan struct{})
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: func() error { close(disposed); time.Sleep(time.Second); close(closed); return nil }})
		if err != nil {
			t.Fatal(err)
		}
		err = watch.Cleanup(100*time.Millisecond, func(context.Context) error { <-disposed; return nil })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("unbounded healthy close", err)
		}
		select {
		case <-closed:
		default:
			t.Fatal("cleanup returned before physical disposal joined")
		}
		outcome, err := watch.Finish(err)
		if outcome.Health != sessioncancel.Unusable || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(outcome, err)
		}
	})
}

func TestJoinedStageCauseIsNotCollapsedToContextSentinel(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	stage := errors.New("stage interrupted")
	cancel(errors.Join(context.Canceled, stage))
	_, err = watch.Finish(nil)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, stage) {
		t.Fatal("custom joined stage cause lost", err)
	}
}

func TestCleanupSignalsCooperativeExitBeforeDisposalJoinsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		nativeExited := make(chan struct{})
		operationContext := make(chan context.Context, 1)
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: func() error {
			operation := <-operationContext
			select {
			case <-operation.Done():
			default:
				t.Error("cooperative stop was not signaled before disposal")
			}
			<-nativeExited
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		err = watch.Cleanup(100*time.Millisecond, func(operation context.Context) error {
			operationContext <- operation
			<-operation.Done()
			close(nativeExited)
			return operation.Err()
		})
		outcome, err := watch.Finish(err)
		if !errors.Is(err, context.DeadlineExceeded) || outcome.Health != sessioncancel.Unusable {
			t.Fatal(outcome, err)
		}
	})
}

func TestCooperativeCleanupWithoutTransportHookStillStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		watch, err := sessioncancel.Start(ctx, context.Background(), sessioncancel.Policy{}, sessioncancel.Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		err = watch.Cleanup(time.Second, func(operation context.Context) error { <-operation.Done(); return operation.Err() })
		outcome, err := watch.Finish(err)
		if !errors.Is(err, context.DeadlineExceeded) || outcome.Evidence.TransportDiscarded {
			t.Fatal("cooperative stop falsely claimed physical disposal", outcome, err)
		}
	})
}
