package session

import (
	"context"
	"errors"
)

// QueryOptions selects positive retention bounds for one buffered query. Zero
// fields independently inherit the session defaults; negative fields are invalid.
// MaxBytes counts the exact text bytes admitted by the existing Codec, excluding
// column names, slice overhead and encoded JSON. It cannot bound memory allocated
// by the driver or Codec before a value is admitted. Options are copied per call;
// they never change session defaults, codec, deadlines, streaming or cleanup policy.
type QueryOptions struct {
	MaxRows  int
	MaxBytes int
}

// ErrInvalidQueryOptions rejects negative buffered-query retention bounds before
// waiting for a connection/operation lease or sending SQL. Zero means inherit.
var ErrInvalidQueryOptions = errors.New("invalid buffered query limits")

func (options QueryOptions) validate() error {
	if options.MaxRows < 0 || options.MaxBytes < 0 {
		return ErrInvalidQueryOptions
	}
	return nil
}

func (options QueryOptions) resolve(defaultRows, defaultBytes int) (QueryOptions, error) {
	if err := options.validate(); err != nil {
		return QueryOptions{}, err
	}
	if options.MaxRows == 0 {
		options.MaxRows = defaultRows
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = defaultBytes
	}
	return options, nil
}

// QueryWithOptions executes one complete buffered query using per-call row/text
// bounds and the session's existing codec, lease, deadline and cancellation owner.
// Invalid options fail before lease admission. EOF, cursor/statement cleanup and
// cancellation finalization must all succeed before a Result is published. SQL is
// never replayed, and any failed prefix is discarded. The session is serialized
// as with Query; simultaneous callers retain their own copied limits.
func (session *Session) QueryWithOptions(ctx context.Context, statement Statement, options QueryOptions) (Result, error) {
	return session.query(ctx, ctx, statement, options)
}

// QueryWithOptions leases one pool session for a complete buffered query with
// per-call retention bounds. Zero fields inherit that session's defaults. Negative
// options fail before pool admission. Queueing remains within the original pool
// query deadline; query/cleanup failures are never replayed on another session,
// and a lease-return failure invalidates the result just as it does for Query.
func (pool *Pool) QueryWithOptions(ctx context.Context, statement Statement, options QueryOptions) (result Result, returnedErr error) {
	if err := options.validate(); err != nil {
		return Result{}, safeError("pool-query", statement.Reference, err)
	}
	operation, cancel := context.WithTimeout(ctx, pool.options.QueryTimeout)
	defer cancel()
	client, err := pool.acquire(operation)
	if err != nil {
		return Result{}, safeError("pool-query", statement.Reference, err)
	}
	defer func() {
		returnedErr = joinCauses(returnedErr, pool.release(client))
		if returnedErr != nil {
			result = Result{}
		}
	}()
	return client.query(operation, ctx, statement, options)
}

// QueryWithOptions executes one complete buffered query within the retained
// transaction using independent per-call row/text bounds. It retains the same
// transaction operation context, gate, codec and finalization rules as Query;
// zero options inherit defaults, and negative options fail before admission.
func (tx *Tx) QueryWithOptions(ctx context.Context, statement Statement, options QueryOptions) (Result, error) {
	if err := options.validate(); err != nil {
		return Result{}, safeError("query", statement.Reference, err)
	}
	ctx, finish := tx.operationContext(ctx)
	defer finish()
	return tx.view.QueryWithOptions(ctx, statement, options)
}
