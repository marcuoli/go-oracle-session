package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBusyCommitNeverArmsTransactionCancellation(t *testing.T) {
	canceled := make(chan struct{})
	tx := &Tx{parent: &Session{options: Options{CleanupTimeout: 10 * time.Millisecond}}, view: &Session{activeStream: &Stream{}}, cancel: func() { close(canceled) }}
	tx.view.streamMu.Lock()
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() { close(started); finished <- tx.Commit(context.Background()) }()
	<-started
	select {
	case <-canceled:
	case <-time.After(40 * time.Millisecond):
	}
	tx.view.streamMu.Unlock()
	if err := <-finished; !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	select {
	case <-canceled:
		t.Fatal("busy commit canceled its transaction")
	default:
	}
}

func TestTransactionOutcomeWaitsForErrorPublication(t *testing.T) {
	marker := errors.New("terminal marker")
	tx := &Tx{callbackDone: make(chan struct{})}
	tx.outcome.Store(uint32(TxUnknown))
	tx.err = marker
	if tx.Outcome() != TxActive || tx.Err() != nil {
		t.Fatal("terminal state published before finalization joined")
	}
	close(tx.callbackDone)
	if tx.Outcome() != TxUnknown || !errors.Is(tx.Err(), marker) {
		t.Fatal("joined terminal evidence lost")
	}
}
