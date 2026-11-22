package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
)

// SampleBorrowed runs one reviewed query on an exclusively borrowed connection.
// It forwards statement text and arguments unchanged. The read callback may use
// native row metadata and scans, and must finish using borrowed values before it
// returns. It must leave cursor closure to SampleBorrowed and must not retain
// the rows or use the connection concurrently. Do not use it in a transaction.
//
// SampleBorrowed closes the rows without fetching any more, then calls finalize
// on the original driver connection through sql.Conn.Raw. The finalizer uses
// its native argument exclusively. Calling back into sql.Conn or sql.DB can
// deadlock or acquire a different connection. The finalizer must qualify both
// protocol and transaction health under the caller's context. The caller
// owns the deadline and cancellation policy, and the driver and
// finalizer must honor it. A nil error permits reuse of the still-borrowed
// connection. An intentional sample does not establish a complete result.
//
// On a failure after opening the rows, or cancellation during query admission,
// SampleBorrowed marks the database/sql connection bad. Use it only with driver
// configurations that the caller has qualified to retire the physical session
// on that path, including when the driver manages a native pool. Callback panics
// propagate after cleanup. An ordinary query rejection follows database/sql
// handling. It opens no new lease. Errors expose the query or sample
// stage and underlying causes reported by database/sql.
func SampleBorrowed(ctx context.Context, conn *sql.Conn, statement Statement, read func(*sql.Rows) error, finalize SampleFinalizer) (returnedErr error) {
	if conn == nil || read == nil || finalize == nil {
		return safeError("sample", statement.Reference, ErrUnsupported)
	}
	if err := validateStatement(statement); err != nil {
		return safeError("query", statement.Reference, err)
	}
	if err := ctx.Err(); err != nil {
		return safeError("query", statement.Reference, err)
	}

	rows, err := conn.QueryContext(ctx, statement.Text, statement.Args...)
	if err != nil {
		cause := errors.Join(err, ctx.Err())
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			cause = retireBorrowedSample(conn, cause)
		}
		return safeError("query", statement.Reference, cause)
	}

	reusable := false
	defer func() {
		if !reusable {
			returnedErr = safeError("sample", statement.Reference, retireBorrowedSample(conn, returnedErr))
		}
	}()

	if err := errors.Join(readBorrowedSample(ctx, rows, read), ctx.Err()); err != nil {
		return err
	}
	err = conn.Raw(func(native any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		health, err := finalize(ctx, native)
		if err == nil && (!health.ProtocolSettled || !health.TransactionUsable) {
			return ErrUnavailable
		}
		return err
	})
	if err := errors.Join(err, ctx.Err()); err != nil {
		return err
	}
	reusable = true
	return nil
}

func readBorrowedSample(ctx context.Context, rows *sql.Rows, read func(*sql.Rows) error) (err error) {
	defer func() {
		// Close releases any RawBytes scan before Err observes terminal state.
		err = errors.Join(err, rows.Close(), rows.Err())
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return read(rows)
}

func retireBorrowedSample(conn *sql.Conn, cause error) error {
	err := conn.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		err = nil
	}
	return errors.Join(joinCauses(cause, ErrUnavailable), err)
}
