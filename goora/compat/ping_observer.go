package compat

import (
	"context"
	"errors"
	"time"
)

// PingObserver brackets a caller-owned ping synchronously. Either callback
// may be nil. After receives the elapsed time through the operation and its
// original error, which may contain private data. The caller owns redaction.
// Callbacks shared across concurrent calls must support concurrent use.
type PingObserver struct {
	Before func() error
	After  func(elapsed time.Duration, cause error) error
}

// PingObserved calls ping once with ctx. Timing starts before Before. An error
// from Before is returned directly and skips both ping and After. An error from
// After is joined with the operation error. Otherwise the original operation
// result is returned unchanged. The ping callback must be non-nil and owns
// connection handling, including cancellation and cleanup. An empty observer
// preserves the callback's behavior.
func PingObserved(ctx context.Context, ping func(context.Context) error, observer PingObserver) error {
	started := time.Now()
	if observer.Before != nil {
		if err := observer.Before(); err != nil {
			return err
		}
	}
	cause := ping(ctx)
	if observer.After != nil {
		if err := observer.After(time.Since(started), cause); err != nil {
			return errors.Join(cause, err)
		}
	}
	return cause
}
