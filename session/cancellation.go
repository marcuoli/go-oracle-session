package session

import (
	"context"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
)

// Cancellation opts one physical session into caller-qualified soft cancellation.
// Break and Reusable must belong exclusively to the connector's original native
// connection. Pool factories supply independent callbacks per Session. Do not
// retain another cancellation watcher around that connector.
//
// QueryGrace permits native break/settlement after a query deadline; parent or
// stage cancellation retains the shorter cleanup-bounded interruption grace.
// Break must return when Options.Dispose closes its I/O. Reusable must return
// promptly without I/O, only after verifying protocol settlement, usable
// transaction state and every cause in operationErr. A requested break also
// requires the driver's typed server cancellation acknowledgement. See
// cancel.Hooks for callback joining and panic behavior.
//
// A nil Options.Cancellation retains retirement on every cancellation. An
// affirmative proof permits a later command; the canceled command still fails
// and never publishes a buffered prefix or marks a stream complete.
type Cancellation struct {
	QueryGrace time.Duration
	Break      func() error
	Reusable   func(operationErr error, breakRequested bool) bool
}

func (s *Session) watchCommand(operation, parent context.Context) (*sessioncancel.Watch, error) {
	policy := sessioncancel.Policy{Query: true, ParentGrace: min(100*time.Millisecond, s.options.CleanupTimeout)}
	hooks := sessioncancel.Hooks{Dispose: s.options.Dispose}
	if c := s.options.Cancellation; c != nil {
		policy.QueryGrace = c.QueryGrace
		hooks.Break = c.Break
		hooks.Reusable = c.Reusable
	}
	return sessioncancel.Start(operation, parent, policy, hooks)
}

func (s *Session) driverContext(operation context.Context) context.Context {
	if s.options.Cancellation != nil {
		// The one joined Watch observes the original lifetime. Suppress only the
		// driver's/database/sql's duplicate cancellation, preserving context values.
		return context.WithoutCancel(operation)
	}
	return operation
}

func (s *Session) commandCleanup(watch *sessioncancel.Watch) {
	if s.options.Cancellation != nil {
		s.cursorErrors.setCloseHook(func(closeOwned func() error) error {
			return watch.Cleanup(s.options.CleanupTimeout, func(context.Context) error { return closeOwned() })
		})
	}
}

// nativeBeginContextKey preserves native setup cancellation while the Session
// owns transaction-lifetime rollback ordering above database/sql.
type nativeBeginContextKey struct{}
