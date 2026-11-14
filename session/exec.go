package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
	"github.com/marcuoli/go-sqltemplate/reference"
)

// ExecOptions controls the execution deadline. NoQueryTimeout disables only the
// configured query/queue deadline; caller cancellation and cleanup budgets remain.
type ExecOptions struct{ NoQueryTimeout bool }

// Exec executes once on the leased connection, forwarding native bind values.
// A nil error establishes joined execution, not transaction commit or DDL atomicity.
// Drivers can mutate caller-owned output destinations before returning an error;
// discard such values on failure. Do not access destinations concurrently.
func (s *Session) Exec(ctx context.Context, statement Statement, options ExecOptions) (result sql.Result, returnedErr error) {
	operation, cancel := operationContext(ctx, s.options.QueryTimeout, options.NoQueryTimeout)
	defer cancel()
	if err := s.acquire(operation); err != nil {
		return nil, safeError("exec", statement.Reference, err)
	}
	defer s.release()
	if s.closing.Load() || s.closed || s.unusable {
		return nil, ErrUnavailable
	}
	if err := statement.Reference.Validate(); err != nil {
		return nil, err
	}
	if statement.Text == "" || len(statement.Text) > 1<<20 {
		return nil, errors.New("invalid catalog statement")
	}
	text, err := statement.Reference.Annotate(statement.Text)
	if err != nil {
		return nil, err
	}
	watch, err := sessioncancel.Start(operation, ctx, sessioncancel.Policy{Query: true, ParentGrace: min(100*time.Millisecond, s.options.CleanupTimeout)}, sessioncancel.Hooks{Dispose: s.options.Dispose})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupErr := s.cursorErrors.take()
		returnedErr = joinCauses(returnedErr, cleanupErr)
		outcome, err := watch.Finish(returnedErr)
		if cleanupErr != nil || outcome.Cause != "none" || errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
			s.unusable = true
		}
		returnedErr = safeError("exec", statement.Reference, err)
		if returnedErr != nil {
			result = nil
		} else {
			result = projectedResult{Result: result, reference: statement.Reference}
		}
	}()
	result, err = s.runner().ExecContext(operation, text, statement.Args...)
	if result == nil && err == nil {
		err = errors.New("driver returned nil execution result")
	}
	return result, err
}

type projectedResult struct {
	sql.Result
	reference reference.Reference
}

// LastInsertId delegates metadata retrieval with safe error formatting.
func (r projectedResult) LastInsertId() (int64, error) {
	n, err := r.Result.LastInsertId()
	return n, safeError("exec-result", r.reference, err)
}

// RowsAffected delegates metadata retrieval with safe error formatting.
func (r projectedResult) RowsAffected() (int64, error) {
	n, err := r.Result.RowsAffected()
	return n, safeError("exec-result", r.reference, err)
}

// Exec leases one session for one execution. It never replays a failed command.
// Separate calls need not share session-local state; use a caller-owned Session
// for multi-command affinity rather than starting a transaction merely to pin it.
func (p *Pool) Exec(ctx context.Context, statement Statement, options ExecOptions) (result sql.Result, returnedErr error) {
	operation, cancel := operationContext(ctx, p.options.QueryTimeout, options.NoQueryTimeout)
	defer cancel()
	client, err := p.acquire(operation)
	if err != nil {
		return nil, safeError("pool-exec", statement.Reference, err)
	}
	defer func() {
		returnedErr = joinCauses(returnedErr, p.release(client))
		if returnedErr != nil {
			result = nil
		}
	}()
	return client.Exec(operation, statement, options)
}
func operationContext(ctx context.Context, budget time.Duration, optOut bool) (context.Context, context.CancelFunc) {
	if optOut {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, budget)
}

// boundedCleanup arms disposal while synchronous cleanup joins; it does not
// dispose a successfully cleaned, reusable connection as Session.Close does.
func boundedCleanup(ctx context.Context, options Options, closeOwned func() error) error {
	parent := context.WithoutCancel(ctx)
	bounded, cancel := context.WithTimeout(parent, options.CleanupTimeout)
	defer cancel()
	watch, err := sessioncancel.Start(bounded, parent, sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: options.Dispose})
	if err != nil {
		return err
	}
	_, err = watch.Finish(closeOwned())
	return err
}
