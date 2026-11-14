package cancel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Policy distinguishes a query's grace budget from parent/stage interruption.
// Zero QueryGrace requests immediate disposal at the query deadline.
type Policy struct {
	Query                   bool
	QueryGrace, ParentGrace time.Duration
}

// ErrHookPanic identifies a panic in a native break or reuse-proof callback.
// The panic value is not formatted; owned workers join and the transport is
// discarded before this error is returned.
var ErrHookPanic = errors.New("native cancellation hook panicked")

// Hooks supplies per-physical-session interruption and health operations.
// Dispose must close owned transport I/O and return after closure. Break is
// optional, must be safe alongside the active command, and must return when
// Dispose interrupts its I/O. The watch owns and joins its break worker.
//
// Reusable is optional and requires Break. It runs synchronously after the
// operation, rows, cleanup and break worker join. It must return promptly without
// I/O. True must attest protocol settlement and usable transaction state; when
// breakRequested is true it must also verify the native cancellation
// acknowledgement and reject unexpected joined causes in operationErr. A generic
// Ping or natural return does not supply that proof. Missing/false proof retires.
// Panics in Break or Reusable become ErrHookPanic after disposal and joining.
type Hooks struct {
	Dispose  func() error
	Break    func() error
	Reusable func(operationErr error, breakRequested bool) bool
}

// Outcome keeps cancellation provenance and disposal separate from the error.
type Outcome struct {
	Cause    string
	Health   Health
	Evidence Evidence
}

// Watch owns one AfterFunc callback and joins it when Finish returns.
// Finish is called only after the operation and its cursor cleanup have joined.
type Watch struct {
	ctx, parent            context.Context
	policy                 Policy
	hooks                  Hooks
	completed, watcherDone chan struct{}
	stop                   func() bool
	mu                     sync.Mutex
	finished               bool
	once                   sync.Once
	disposed               atomic.Bool
	disposeOnce            sync.Once
	disposalContext        context.Context
	cancelDisposal         context.CancelFunc
	disposeErr             error
	breakRequested         bool  // read only after watcherDone
	breakErr               error // read only after watcherDone
	outcome                Outcome
	returnedErr            error
}

// Start arms one operation watcher; parent is the enclosing collection lifetime.
// Break and Dispose belong to this watcher; callers must not arm a competing
// native cancellation controller for the same operation.
func Start(ctx, parent context.Context, policy Policy, hooks Hooks) (*Watch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if policy.QueryGrace < 0 || policy.ParentGrace < 0 {
		return nil, errors.New("cancellation grace must not be negative")
	}
	if hooks.Reusable != nil && hooks.Break == nil {
		return nil, errors.New("reuse proof requires an owned native break")
	}
	if parent == nil {
		parent = ctx
	}
	disposalContext, cancelDisposal := context.WithCancel(context.Background())
	watch := &Watch{ctx: ctx, parent: parent, policy: policy, hooks: hooks, completed: make(chan struct{}), watcherDone: make(chan struct{}), disposalContext: disposalContext, cancelDisposal: cancelDisposal}
	watch.stop = context.AfterFunc(ctx, watch.interrupt)
	return watch, nil
}

func (watch *Watch) interrupt() {
	defer close(watch.watcherDone)
	watch.mu.Lock()
	finished := watch.finished
	watch.mu.Unlock()
	if finished {
		return
	}
	var breakDone <-chan error
	if watch.hooks.Break != nil {
		watch.breakRequested = true
		done := make(chan error, 1)
		breakDone = done
		go func() { done <- callBreak(watch.hooks.Break) }()
	}
	grace := watch.policy.ParentGrace
	if watch.queryExpired() {
		grace = watch.policy.QueryGrace
	}
	cutoff := time.Now().Add(grace)
	if watch.queryExpired() {
		if deadline, ok := watch.ctx.Deadline(); ok {
			cutoff = Cutoff(deadline, time.Time{}, grace, watch.policy.ParentGrace)
		}
	}
	timer := time.NewTimer(time.Until(cutoff))
	defer timer.Stop()
	parentDone, operationDone := watch.parent.Done(), (<-chan struct{})(watch.completed)
	for operationDone != nil || breakDone != nil {
		select {
		case <-operationDone:
			operationDone = nil
		case err := <-breakDone:
			watch.breakErr = err
			breakDone = nil
		case <-parentDone:
			parentDone = nil
			parentCutoff := time.Now().Add(watch.policy.ParentGrace)
			if parentCutoff.Before(cutoff) {
				cutoff = parentCutoff
				timer.Reset(time.Until(cutoff))
			}
		case <-timer.C:
			// Operation return cannot release a still-blocked break worker. Closing
			// the physical transport must unblock both before Finish can publish reuse.
			watch.dispose()
			if breakDone != nil {
				watch.breakErr = <-breakDone
			}
			return
		}
	}
}

// Cleanup joins one owned close under an independent cleanup budget. Once the
// original operation is canceled, its existing break/grace policy owns disposal;
// a secondary close timer must not shorten that grace. Call before Finish and
// only for resources belonging to this watch. The callback context preserves
// values and is canceled when this owner commits to transport disposal; it has no
// independent deadline that could start a competing native break. The callback
// must return on that signal or when Dispose unblocks its I/O.
func (watch *Watch) Cleanup(budget time.Duration, closeOwned func(context.Context) error) error {
	if budget <= 0 || closeOwned == nil {
		return errors.New("invalid owned cleanup budget/function")
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(watch.ctx), budget)
	defer cancel()
	var expired atomic.Bool
	done := make(chan struct{})
	stop := context.AfterFunc(bounded, func() {
		defer close(done)
		if watch.ctx.Err() == nil {
			expired.Store(true)
			watch.dispose()
		}
	})
	var join sync.Once
	joinCleanup := func() {
		join.Do(func() {
			if !stop() {
				<-done
			}
		})
	}
	defer joinCleanup()
	operationSignal, cancelOperation := context.WithCancel(watch.disposalContext)
	defer cancelOperation()
	operation := cleanupContext{Context: operationSignal, values: context.WithoutCancel(watch.ctx)}
	err := closeOwned(operation)
	joinCleanup()
	if expired.Load() {
		err = errors.Join(err, context.DeadlineExceeded, watch.disposeErr)
	}
	return err
}

func (watch *Watch) queryExpired() bool {
	return watch.policy.Query && watch.parent.Err() == nil && errors.Is(watch.ctx.Err(), context.DeadlineExceeded)
}

func (watch *Watch) dispose() {
	watch.disposeOnce.Do(func() {
		watch.cancelDisposal()
		if watch.hooks.Dispose != nil {
			watch.disposed.Store(true)
			watch.disposeErr = watch.hooks.Dispose()
		}
	})
}

func callBreak(breakCall func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrHookPanic
		}
	}()
	return breakCall()
}

func callProof(proof func(error, bool) bool, operationErr error, requested bool) (reusable bool, err error) {
	defer func() {
		if recover() != nil {
			reusable = false
			err = ErrHookPanic
		}
	}()
	return proof(operationErr, requested), nil
}

// Finish joins the watcher and native break before releasing physical-session
// state. Cancellation remains an error even when affirmative native health
// permits the same session's next command. Missing/failed proof disposes the
// transport, including when the operation beat the grace timer. No worker detaches.
func (watch *Watch) Finish(operationErr error) (Outcome, error) {
	watch.once.Do(func() {
		watch.mu.Lock()
		watch.finished = true
		watch.mu.Unlock()
		close(watch.completed)
		if !watch.stop() {
			<-watch.watcherDone
		}
		evidence := Evidence{OperationJoined: true, RowsJoined: true, CleanupJoined: true}
		cause := "none"
		var proofErr error
		if watch.ctx.Err() != nil {
			cause = "parent"
			if watch.queryExpired() {
				cause = "query"
			}
			evidence.BreakJoined = watch.hooks.Break != nil
			if !watch.disposed.Load() && watch.breakErr == nil && watch.hooks.Reusable != nil {
				var reusable bool
				reusable, proofErr = callProof(watch.hooks.Reusable, operationErr, watch.breakRequested)
				evidence.ProtocolSettled = reusable
				evidence.TransactionUsable = reusable
			}
			if Classify(evidence) != Reusable {
				watch.dispose()
			}
			evidence.TransportDiscarded = watch.disposed.Load() && watch.disposeErr == nil
		} else if operationErr == nil {
			evidence.BreakJoined = true
			evidence.ProtocolSettled = true
			evidence.TransactionUsable = true
		}
		evidence.TransportDiscarded = watch.disposed.Load() && watch.disposeErr == nil
		watch.outcome = Outcome{Cause: cause, Health: Classify(evidence), Evidence: evidence}
		watch.returnedErr = errors.Join(operationErr, watch.ctx.Err(), watch.parent.Err(), watch.breakErr, proofErr, watch.disposeErr)
		for _, scope := range []context.Context{watch.ctx, watch.parent} {
			if nativeCause := context.Cause(scope); nativeCause != nil && nativeCause != scope.Err() {
				watch.returnedErr = errors.Join(watch.returnedErr, nativeCause)
			}
		}
	})
	return watch.outcome, watch.returnedErr
}

// cleanupContext takes cancellation only from the owner's terminal signal. The
// original context supplies values without exposing its canceled internal state.
type cleanupContext struct {
	context.Context
	values context.Context
}

func (c cleanupContext) Value(key any) any { return c.values.Value(key) }
