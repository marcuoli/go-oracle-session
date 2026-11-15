# Per-call buffered-query limits

`session.QueryOptions` supplies `MaxRows` and `MaxBytes` to one
`Session.QueryWithOptions`, `Pool.QueryWithOptions` or `Tx.QueryWithOptions` call.
Each zero field independently inherits the positive default configured when its
session was opened. Positive fields may raise or lower that call's limit. Negative
fields return `ErrInvalidQueryOptions` before SQL or lease admission, including
when another operation is holding the session, pool or transaction lease.

```go
result, err := existingSession.QueryWithOptions(ctx, statement, session.QueryOptions{
    MaxRows:  10000,
    MaxBytes: 16 << 20,
})
if err != nil {
    return err // result contains no failed prefix
}
// Decode the complete result using the owning source's reviewed projection.
```

The existing `Query(ctx, statement)` methods retain their signatures and delegate
with zero options. Overrides are copied into local values; they never mutate the
session defaults, another queued/concurrent query, subsequent `Query` calls,
`StreamOptions`, the codec or deadline/cancellation/cleanup policy. A pool resolves
zero fields against the particular session it leases, preserving existing
per-session configuration. Transactions retain their original private operation
view and parent lease.

The row limit counts every source row, including rows containing only NULL or
empty text. Exactly the limit followed by successful EOF is complete; another row
returns `ErrCapacity`. The byte limit sums the exact `session.Value.Text` bytes
produced by the existing codec across every row/column. UTF-8 characters can occupy
multiple bytes. NULL contributes no text bytes; a codec may still reject malformed
values. Exactly the byte limit is admitted. Column names, slice/object overhead and
JSON encoding are outside this retention count; use `rowset.Limits` when bounding
an encoded JSON dataset.

These are retained-result bounds. A driver or codec can allocate/materialize a
value before the limit check. This API does not add bounded lazy-LOB decoding or
change any existing codec support. Callers must qualify their actual connector,
server, values and cleanup hooks; synthetic tests do not establish native Oracle
CLOB or platform compatibility. No Oracle version floor or ceiling is added.

Execution uses the existing single query path, exact codec and serialized lease.
Queueing remains inside the original query deadlines. Cursor/statement cleanup,
terminal failures, cancellation and pool lease-return errors join before any result
is published. Any failure clears the entire result, including duration and column
metadata. SQL is not replayed. A capacity error is never a successful prefix or an
instruction to retry automatically. Existing session reuse/retirement policy still
applies independently of whether a per-call override was supplied.
