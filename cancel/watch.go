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

// Hooks supplies physical transport disposal. It must unblock owned I/O and
// return only after closure. Nil hooks cannot bound an uncooperative driver.
type Hooks struct{ Dispose func() error }

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
	disposeErr             error
	outcome                Outcome
	returnedErr            error
}

// Start arms one operation watcher; parent is the enclosing collection lifetime.
func Start(ctx, parent context.Context, policy Policy, hooks Hooks) (*Watch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if policy.QueryGrace < 0 || policy.ParentGrace < 0 {
		return nil, errors.New("cancellation grace must not be negative")
	}
	if parent == nil {
		parent = ctx
	}
	watch := &Watch{ctx: ctx, parent: parent, policy: policy, hooks: hooks, completed: make(chan struct{}), watcherDone: make(chan struct{})}
	watch.stop = context.AfterFunc(ctx, func() {
		defer close(watch.watcherDone)
		watch.mu.Lock()
		finished := watch.finished
		watch.mu.Unlock()
		if finished {
			return
		}
		grace := policy.ParentGrace
		cause := "parent"
		if policy.Query && parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			grace = policy.QueryGrace
			cause = "query"
		}
		cutoff := time.Now().Add(grace)
		if cause == "query" {
			if deadline, ok := ctx.Deadline(); ok {
				cutoff = Cutoff(deadline, time.Time{}, grace, policy.ParentGrace)
			}
		}
		timer := time.NewTimer(time.Until(cutoff))
		defer timer.Stop()
		parentDone := parent.Done()
		for {
			select {
			case <-watch.completed:
				return
			case <-parentDone:
				parentDone = nil
				parentCutoff := time.Now().Add(policy.ParentGrace)
				if parentCutoff.Before(cutoff) {
					cutoff = parentCutoff
					timer.Reset(time.Until(cutoff))
				}
			case <-timer.C:
				watch.dispose()
				return
			}
		}
	})
	return watch, nil
}
func (watch *Watch) dispose() {
	if watch.hooks.Dispose != nil && watch.disposed.CompareAndSwap(false, true) {
		watch.disposeErr = watch.hooks.Dispose()
	}
}

// Finish joins the watcher before releasing any physical-session state. A
// cancellation without protocol reuse proof disposes the transport even when
// the operation returned before the grace timer fired. No worker is detached.
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
		if watch.ctx.Err() != nil {
			cause = "parent"
			if watch.policy.Query && watch.parent.Err() == nil && errors.Is(watch.ctx.Err(), context.DeadlineExceeded) {
				cause = "query"
			}
			watch.dispose()
			evidence.TransportDiscarded = watch.disposed.Load() && watch.disposeErr == nil
		} else if operationErr == nil {
			evidence.BreakJoined = true
			evidence.ProtocolSettled = true
			evidence.TransactionUsable = true
		}
		watch.outcome = Outcome{Cause: cause, Health: Classify(evidence), Evidence: evidence}
		watch.returnedErr = errors.Join(operationErr, watch.ctx.Err(), watch.disposeErr)
	})
	return watch.outcome, watch.returnedErr
}
