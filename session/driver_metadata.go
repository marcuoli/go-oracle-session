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

// DriverCallback inspects driver-specific metadata while the physical session
// is exclusively leased. Use the provided bounded context, return promptly and
// retain no driver object/resource. Do not close the connection, manage a
// transaction, issue SQL, or call this Session/Tx's methods inside the callback.
// The caller qualifies driver-specific metadata operations and disposal hooks.
type DriverCallback func(context.Context, any) error

// WithDriver provides scoped access to the original driver for metadata and
// capability interop without SQL annotation, retries, or exposing sql.DB/Conn.
// The callback must obey DriverCallback ownership rules; the driver cannot be
// retained beyond it. Pool admission factories can inspect each real Session.
// Inspect before BeginTx: an active Tx retains this Session gate until it ends.
func (s *Session) WithDriver(ctx context.Context, callback DriverCallback) (returnedErr error) {
	if callback == nil {
		return errors.New("nil driver callback")
	}
	operation, cancel := context.WithTimeout(ctx, s.options.QueryTimeout)
	defer cancel()
	if err := s.acquire(operation); err != nil {
		return safeError("driver", reference.Reference{}, err)
	}
	defer s.release()
	if s.closing.Load() || s.closed || s.unusable {
		return ErrUnavailable
	}
	watch, err := sessioncancel.Start(operation, ctx, sessioncancel.Policy{Query: true, ParentGrace: min(100*time.Millisecond, s.options.CleanupTimeout)}, sessioncancel.Hooks{Dispose: s.options.Dispose})
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			s.unusable = true
			returnedErr = joinCauses(returnedErr, s.options.Dispose())
		}
		outcome, err := watch.Finish(returnedErr)
		if outcome.Cause != "none" || errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
			s.unusable = true
		}
		returnedErr = safeError("driver", reference.Reference{}, err)
	}()
	returnedErr = s.connection.Raw(func(native any) error {
		switch native := native.(type) {
		case *directConnection:
			return callback(operation, native.Conn)
		case *preparedConnection:
			return callback(operation, native.Conn)
		default:
			return ErrUnsupported
		}
	})
	completed = true
	return returnedErr
}
