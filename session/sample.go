package session

import (
	"context"
	"errors"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
)

// SampleHealth records caller-qualified native evidence after a cursor closes.
// Neither a successful Close nor a generic Ping establishes these facts for all
// drivers. False means proof is missing, and prevents physical-session reuse.
type SampleHealth struct {
	ProtocolSettled, TransactionUsable bool
}

// SampleFinalizer settles and checks the original driver after cursor cleanup.
// It must use the bounded context, join its I/O, and retain no driver resource.
// It must not issue application SQL, manage transactions, close the connection,
// or call Session/Tx methods. The caller qualifies its driver-specific behavior
// and the disposal hook on the actual database, driver version and platform.
type SampleFinalizer func(context.Context, any) (SampleHealth, error)

// SampleFinalization separates successful EOF from intentional early stopping
// and physical-session health. StoppedEarly does not imply successful cleanup;
// inspect the returned error and classify Evidence before reusing a session.
// EOFObserved certifies only the stream's normal completion contract.
type SampleFinalization struct {
	EOFObserved, StoppedEarly bool
	Evidence                  sessioncancel.Evidence
}

// FinalizeSample closes the cursor without calling Next again, then runs a
// qualified finalizer on the same physical connection under the original query
// context and CleanupTimeout. Native close/settling may perform I/O. Only
// affirmative health and joined error-free cleanup permit reuse. The sample
// stays incomplete even when reuse is proven.
//
// Call from the goroutine owning Next/Scan, after consuming borrowed values.
// Close/cancellation may interrupt it. Transaction streams and a nil finalizer
// return ErrUnsupported and retire the session when called before completion.
// Existing terminal results are returned unchanged without invoking a callback.
// Callback panics propagate after owned cleanup joins and the session retires.
func (cursor *Stream) FinalizeSample(finalizer SampleFinalizer) (SampleFinalization, error) {
	cursor.mu.Lock()
	var finishHere bool
	var recovered any
	if !cursor.finished.Load() {
		cursor.sample.StoppedEarly = true
		cause := cursor.ctx.Err()
		if finalizer == nil || cursor.session.commands != nil {
			cause = joinCauses(cause, ErrUnsupported)
			finalizer = nil
		}
		finishHere, recovered = cursor.finishWithSampleLocked(cause, false, finalizer)
	}
	outcome, err := cursor.sample, cursor.err
	cursor.mu.Unlock()
	if finishHere {
		cursor.endCancellation()
	}
	cursor.joinCancellation()
	if recovered != nil {
		panic(recovered)
	}
	return outcome, err
}

// probeSample does not reacquire the already-held Session gate. Raw is limited
// to non-transaction streams: database/sql may close a bad Raw connection while
// a transaction still owns it. Its watcher joins even when the callback panics.
func (cursor *Stream) probeSample(finalizer SampleFinalizer) (health SampleHealth, returnedErr error, recovered any) {
	operation, cancel := context.WithTimeout(cursor.ctx, cursor.session.options.CleanupTimeout)
	defer cancel()
	watch, err := sessioncancel.Start(operation, cursor.ctx, sessioncancel.Policy{}, sessioncancel.Hooks{Dispose: cursor.session.options.Dispose})
	if err != nil {
		return health, err, nil
	}
	defer func() {
		if value := recover(); value != nil {
			recovered = value
			returnedErr = joinCauses(returnedErr, ErrUnavailable)
		}
		_, returnedErr = watch.Finish(returnedErr)
	}()
	returnedErr = cursor.session.connection.Raw(func(native any) error {
		switch native := native.(type) {
		case *directConnection:
			health, err = finalizer(operation, native.Conn)
		case *preparedConnection:
			health, err = finalizer(operation, native.Conn)
		default:
			err = ErrUnsupported
		}
		return err
	})
	if !health.ProtocolSettled || !health.TransactionUsable {
		returnedErr = errors.Join(returnedErr, ErrUnavailable)
	}
	return health, returnedErr, nil
}
