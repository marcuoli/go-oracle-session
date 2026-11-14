# Package ownership

- `inventory` owns bounded RFC 4180 headers, credential-byte preservation, duplicate/effective labels and redacted validation categories. Optional URL validation delegates engine policy to the caller. File and byte admission share the same size limit.
- `transport` returns actual sockets and terminally tracks one physical connection plus redirects. Repeated/concurrent closure joins the same result. It contacts only the requested endpoint.
- `cancel` owns joined cancellation watchers, deadline provenance and explicit health evidence. A returned operation alone does not establish reusable protocol state.
- `session` owns one leased connection, cancellable admission, bounded non-replayed catalog queries, exact value admission and cursor ownership. A private forwarding connector retains driver cursor-close failures that database/sql would otherwise discard after a fetch failure. This adapter changes no driver source and adds no SQL execution engine.

Driver/URL selection, SQL catalogs, endpoint identity and container admission, credentials, retries/recovery choices, progress, localized text and product acceptance criteria remain caller-owned. A closed or interrupted session is not silently reconnected. Internal sql.DB/Conn/Tx/Stmt objects do not escape. The owned Tx wrapper retains its parent lease; scoped DriverCallback access is restricted to qualified metadata and cannot transfer driver ownership.

Inspect safe top-level error formatting; unwrapped driver errors may contain private connection details. Dispose must be idempotent, unblock owned I/O and return after closure. Queued-query cancellation affects only that waiter. Close refuses new queries and starts cleanup before waiting for active work; timeout disposal must release that work and all cleanup joins before return. No worker is detached to manufacture a timeout.

Compatibility checks retain synthetic driver cases, deadline/cleanup/cursor failures, exact values, socket identity and strict CSV tests. These are module contracts and do not prove any native database migration pair.
Execution uses the same pinned connection and cancellation boundaries as Query.
Explicit context-prepared mode advertises no direct exec/query interface, letting
database/sql apply statement conversion hooks in its one-shot internal prepare
path. Transaction methods use a private serialized command view and retain the
parent lease; they never reacquire it. Parent shutdown cancels owned transactions
and joins both cursor and transaction finalizers. Driver terminal evidence and
physical retirement join automatic rollback before an uncertain lease is returned.
This is resource ownership, not an Oracle DDL or database recovery policy.
The [Oracle compatibility contract](oracle-compatibility.md) preserves the 9.2
baseline through 26ai and later releases without a numeric upper cutoff. Optional
capabilities and caller-selected SQL must not impose a new minimum on unrelated
baseline operations. Contextual interfaces, exact values, joined cancellation,
physical disposal and no replay remain required across the target range; native
qualification belongs to each exact connector/client/server/platform combination.
