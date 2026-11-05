package cancel_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
)

func TestReuseNeedsCompleteAffirmativeEvidence(t *testing.T) {
	if sessioncancel.Classify(sessioncancel.Evidence{OperationJoined: true}) != sessioncancel.Unknown {
		t.Fatal("natural return proved reuse")
	}
	if sessioncancel.Classify(sessioncancel.Evidence{TransportDiscarded: true}) != sessioncancel.Unusable {
		t.Fatal("discarded connection reused")
	}
	complete := sessioncancel.Evidence{OperationJoined: true, RowsJoined: true, BreakJoined: true, CleanupJoined: true, ProtocolSettled: true, TransactionUsable: true}
	if sessioncancel.Classify(complete) != sessioncancel.Reusable {
		t.Fatal("complete reuse evidence rejected")
	}
}

func TestWatcherDisposesAndJoinsTimedOutTransport(t *testing.T) {
	parent := context.Background()
	ctx, cancel := context.WithTimeout(parent, 10*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	closed := make(chan struct{})
	watch, err := sessioncancel.Start(ctx, parent, sessioncancel.Policy{Query: true, ParentGrace: time.Millisecond}, sessioncancel.Hooks{Dispose: func() error { calls.Add(1); close(closed); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("transport never disposed")
	}
	outcome, err := watch.Finish(nil)
	if !errors.Is(err, context.DeadlineExceeded) || outcome.Health != sessioncancel.Unusable || outcome.Cause != "query" || calls.Load() != 1 {
		t.Fatalf("outcome=%+v err=%v calls=%d", outcome, err, calls.Load())
	}
	_, _ = watch.Finish(nil)
	if calls.Load() != 1 {
		t.Fatal("double disposal")
	}
}

func TestCancellationRacingWithReturnStillDisposes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	watch, err := sessioncancel.Start(ctx, ctx, sessioncancel.Policy{ParentGrace: time.Hour}, sessioncancel.Hooks{Dispose: func() error { calls.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	outcome, err := watch.Finish(nil)
	if !errors.Is(err, context.Canceled) || outcome.Health != sessioncancel.Unusable || calls.Load() != 1 {
		t.Fatal("cancelled transport escaped disposal")
	}
}
