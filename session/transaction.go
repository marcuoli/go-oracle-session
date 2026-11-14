package session

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
	"github.com/marcuoli/go-sqltemplate/reference"
)

// ErrBusy identifies a transaction commit attempted with an outstanding cursor.
var ErrBusy = errors.New("transaction has an outstanding cursor")

// ErrOutcomeUnknown means terminal driver evidence cannot establish the database
// outcome. It never authorizes retrying a commit or replaying SQL.
var ErrOutcomeUnknown = errors.New("transaction outcome is unknown")

// TxOptions selects standard driver transaction options. NoQueryTimeout disables
// only BeginTx's admission/setup deadline, not caller cancellation or cleanup.
// After BeginTx succeeds, each command receives its own normal query budget.
type TxOptions struct {
	Isolation      sql.IsolationLevel
	ReadOnly       bool
	NoQueryTimeout bool
}

// TxOutcome describes terminal driver evidence, independently of cleanup errors.
type TxOutcome uint32

// TxActive, TxCommitted, TxRolledBack and TxUnknown distinguish an active
// transaction, confirmed terminal calls and missing/failed terminal evidence.
const (
	TxActive TxOutcome = iota
	TxCommitted
	TxRolledBack
	TxUnknown
)

// Tx owns one physical session until terminal cleanup joins. Methods serialize
// through a private operation gate, never the already-held parent lease. Call
// Commit or Rollback; caller cancellation automatically rolls back and retires.
// Driver hooks must unblock and join I/O. The API makes no DDL atomicity claim.
type Tx struct {
	parent, view *Session
	sql          *sql.Tx
	tracking     *trackedTx
	ctx          context.Context
	cancel       context.CancelFunc
	watch        *sessioncancel.Watch
	terminal     sync.Once
	outcome      atomic.Uint32
	err          error
	stopCancel   func() bool
	callbackDone chan struct{}
	onFinish     func() error
}

// BeginTx retains this session for one transaction. Queueing and setup share a
// query budget unless explicitly disabled. The setup timer is stopped on success;
// the retained transaction context then follows only the caller's lifetime.
func (s *Session) BeginTx(ctx context.Context, options TxOptions) (*Tx, error) {
	return s.beginTx(ctx, ctx, options, nil)
}

// BeginTx leases one pool session until the transaction ends. It never replaces
// or replays an interrupted transaction; failed cleanup retires that capacity.
func (p *Pool) BeginTx(ctx context.Context, options TxOptions) (*Tx, error) {
	admission, cancel := operationContext(ctx, p.options.QueryTimeout, options.NoQueryTimeout)
	defer cancel()
	client, err := p.acquire(admission)
	if err != nil {
		return nil, safeError("pool-begin", reference.Reference{}, err)
	}
	return client.beginTx(ctx, admission, options, func() error { return p.release(client) })
}
func (s *Session) beginTx(parent, admission context.Context, options TxOptions, onFinish func() error) (transaction *Tx, returnedErr error) {
	budget, cancelBudget := operationContext(admission, s.options.QueryTimeout, options.NoQueryTimeout)
	defer cancelBudget()
	acquired, transferred, retire := false, false, false
	defer func() {
		if !transferred {
			if acquired {
				s.release()
			}
			if retire {
				returnedErr = joinCauses(returnedErr, s.close(context.Background(), false))
			}
			if onFinish != nil {
				returnedErr = joinCauses(returnedErr, onFinish())
			}
			returnedErr = safeError("begin", reference.Reference{}, returnedErr)
		}
	}()
	if err := s.acquire(budget); err != nil {
		return nil, err
	}
	acquired = true
	if s.closing.Load() || s.closed || s.unusable {
		return nil, ErrUnavailable
	}
	if !s.driverState.capabilities.Transactions {
		return nil, ErrUnsupported
	}
	lifetime, cancel := context.WithCancel(parent)
	// BeginTx retains its context for the full sql.Tx lifetime. Forward only an
	// admission-time cancellation callback, then stop/join it before canceling the
	// temporary setup context, so success does not immediately roll back the Tx.
	setupDone := make(chan struct{})
	stopSetup := context.AfterFunc(budget, func() { cancel(); close(setupDone) })
	stopBudget := func() {
		if stopSetup() {
			close(setupDone)
		}
		<-setupDone
	}
	watch, err := sessioncancel.Start(lifetime, parent, sessioncancel.Policy{Query: true, ParentGrace: min(100*time.Millisecond, s.options.CleanupTimeout)}, sessioncancel.Hooks{Dispose: s.options.Dispose})
	if err != nil {
		stopBudget()
		cancel()
		return nil, err
	}
	s.driverState.mu.Lock()
	s.driverState.transaction = nil
	s.driverState.mu.Unlock()
	sqlTx, err := s.connection.BeginTx(lifetime, &sql.TxOptions{Isolation: options.Isolation, ReadOnly: options.ReadOnly})
	stopBudget()
	if budget.Err() != nil {
		cancel()
	}
	if err != nil {
		retire = true
		s.unusable = true
		_, err = watch.Finish(errors.Join(err, budget.Err()))
		cancel()
		return nil, err
	}
	s.driverState.mu.Lock()
	tracking := s.driverState.transaction
	s.driverState.mu.Unlock()
	viewGate := make(chan struct{}, 1)
	viewGate <- struct{}{}
	view := &Session{gate: viewGate, options: s.options, cursorErrors: s.cursorErrors, commands: sqlTx, driverState: s.driverState}
	tx := &Tx{parent: s, view: view, sql: sqlTx, tracking: tracking, ctx: lifetime, cancel: cancel, watch: watch, callbackDone: make(chan struct{}), onFinish: onFinish}
	// Register while the physical parent gate remains held; public Close cannot
	// begin its finalizer wait before every admitted transaction is registered.
	s.streamFinalizers.Add(1)
	// The callback can start immediately for an already canceled lifetime.
	// Publish both its stop function and the active owner behind one barrier.
	s.streamMu.Lock()
	tx.stopCancel = context.AfterFunc(lifetime, func() {
		s.streamMu.Lock()
		s.streamMu.Unlock()
		defer tx.endCallback()
		tx.terminate(context.Background(), false, true)
	})
	s.activeTx = tx
	if s.closing.Load() {
		cancel()
	}
	s.streamMu.Unlock()
	transferred = true
	if lifetime.Err() != nil {
		rollbackErr := tx.Rollback(context.Background())
		cause := errors.Join(lifetime.Err(), budget.Err(), rollbackErr, tx.Err())
		return nil, safeError("begin", reference.Reference{}, cause)
	}
	return tx, nil
}

// Exec executes once within the retained transaction; output-bind ownership and
// failure semantics are the same as Session.Exec. Successful Exec is not commit.
func (tx *Tx) Exec(ctx context.Context, statement Statement, options ExecOptions) (sql.Result, error) {
	ctx, finish := tx.operationContext(ctx)
	defer finish()
	return tx.view.Exec(ctx, statement, options)
}

// Query returns one exact buffered result within the retained transaction.
func (tx *Tx) Query(ctx context.Context, statement Statement) (Result, error) {
	ctx, finish := tx.operationContext(ctx)
	defer finish()
	return tx.view.Query(ctx, statement)
}

// Stream retains the transaction's operation gate until cursor finalization.
// Finish or close it before Commit. Rollback cancels and joins outstanding I/O.
func (tx *Tx) Stream(ctx context.Context, statement Statement, options StreamOptions) (*Stream, error) {
	operation, finish := tx.operationContext(ctx)
	return tx.view.startStream(operation, ctx, statement, options, func() error { finish(); return nil })
}
func (tx *Tx) operationContext(ctx context.Context) (context.Context, func()) {
	operation, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stop := context.AfterFunc(tx.ctx, func() { cancel(); close(done) })
	return operation, func() {
		if stop() {
			close(done)
		}
		<-done
		cancel()
	}
}

// Outcome returns currently published terminal evidence. An execution or output
// bind succeeding does not change TxActive; only terminal cleanup publishes it.
func (tx *Tx) Outcome() TxOutcome {
	select {
	case <-tx.callbackDone:
		return TxOutcome(tx.outcome.Load())
	default:
		return TxActive
	}
}

// Commit commits once after prior operations finish. An open cursor returns
// ErrBusy without ending the transaction; close/exhaust it or call Rollback.
// A failed driver commit yields ErrOutcomeUnknown and retires the session.
func (tx *Tx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return safeError("commit", reference.Reference{}, err)
	}
	// ErrBusy is a precondition rejection, so it must not arm a cancellation
	// observer capable of ending the transaction while this decision waits.
	tx.view.streamMu.Lock()
	busy := tx.view.activeStream != nil
	tx.view.streamMu.Unlock()
	if busy {
		return ErrBusy
	}
	operation, cancel := context.WithTimeout(ctx, tx.parent.options.CleanupTimeout)
	done := make(chan struct{})
	stop := context.AfterFunc(operation, func() { tx.cancel(); close(done) })
	defer func() {
		if stop() {
			close(done)
		}
		<-done
		cancel()
	}()
	return tx.terminate(operation, true, false)
}

// Rollback cancels/joins an outstanding cursor before rolling back once. Cleanup
// retains a bounded independent lifetime after caller cancellation. Repeated
// terminal calls return sql.ErrTxDone and never replay the driver operation.
func (tx *Tx) Rollback(ctx context.Context) error { return tx.terminate(ctx, false, false) }

func (tx *Tx) terminate(ctx context.Context, commit, callback bool) error {
	won, finishHere := false, false
	tx.terminal.Do(func() {
		won = true
		finishHere = tx.stopCancel()
		tx.view.closing.Store(true)
		tx.view.cancelActiveStream()
		var terminalErr error
		cleanupErr := boundedCleanup(ctx, tx.parent.options, func() error {
			<-tx.view.gate
			defer tx.view.release()
			// The gate joins driver I/O; finalizers additionally publish errors and
			// return their nested lease after releasing that gate. Join both lifetimes.
			tx.view.streamFinalizers.Wait()
			if commit && !tx.view.unusable && tx.ctx.Err() == nil {
				terminalErr = tx.sql.Commit()
			} else {
				terminalErr = tx.sql.Rollback()
				if commit {
					terminalErr = joinCauses(terminalErr, ErrUnavailable)
				}
			}
			if tx.tracking != nil {
				<-tx.tracking.done
			}
			if errors.Is(terminalErr, sql.ErrTxDone) {
				terminalErr = nil
				if commit {
					terminalErr = ErrUnavailable
				}
			}
			if tx.tracking != nil {
				terminalErr = joinCauses(terminalErr, tx.tracking.err)
			}
			return terminalErr
		})
		outcome := TxUnknown
		if tx.tracking != nil && tx.tracking.err == nil {
			if tx.tracking.action == "commit" {
				outcome = TxCommitted
			} else if tx.tracking.action == "rollback" {
				outcome = TxRolledBack
			}
		}
		if outcome == TxUnknown {
			cleanupErr = joinCauses(cleanupErr, ErrOutcomeUnknown)
		}
		_, cleanupErr = tx.watch.Finish(cleanupErr)
		unhealthy := tx.view.unusable || cleanupErr != nil || tx.ctx.Err() != nil || outcome == TxUnknown
		if unhealthy {
			tx.parent.unusable = true
		}
		tx.parent.streamMu.Lock()
		if tx.parent.activeTx == tx {
			tx.parent.activeTx = nil
		}
		tx.parent.streamMu.Unlock()
		tx.parent.release()
		// On cancellation sql.Tx.Rollback may return ErrTxDone before its automatic
		// rollback worker closes statements and releases sql.Conn's lock. Physical
		// close waits for that lock, joining the trailing worker before lease return.
		if unhealthy {
			cleanupErr = joinCauses(cleanupErr, tx.parent.close(ctx, false))
		}
		if tx.onFinish != nil {
			cleanupErr = joinCauses(cleanupErr, tx.onFinish())
		}
		tx.outcome.Store(uint32(outcome))
		operation := "rollback"
		if commit {
			operation = "commit"
		}
		tx.err = safeError(operation, reference.Reference{}, cleanupErr)
		tx.cancel()
	})
	if finishHere {
		tx.endCallback()
	}
	// A cancellation callback cannot join itself. A callback that loses the
	// terminal once still completes its defer; public callers join it below.
	if !callback {
		<-tx.callbackDone
	}
	if !won {
		return sql.ErrTxDone
	}
	return tx.err
}
func (tx *Tx) endCallback() { close(tx.callbackDone); tx.parent.streamFinalizers.Done() }

// Err returns the safely formatted terminal error after finalization joins, or
// nil while still active or after success. Inspect Outcome to distinguish those
// states. Underlying driver causes are for programmatic inspection only.
func (tx *Tx) Err() error {
	select {
	case <-tx.callbackDone:
		return tx.err
	default:
		return nil
	}
}
