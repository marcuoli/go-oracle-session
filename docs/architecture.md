# Package ownership

- `inventory` owns bounded RFC 4180 headers, credential-byte preservation, duplicate/effective labels and redacted validation categories. Optional URL validation delegates engine policy to the caller. File and byte admission share the same size limit.
- `transport` returns actual sockets and terminally tracks one physical connection plus redirects. Repeated/concurrent closure joins the same result. It contacts only the requested endpoint.
- `cancel` owns joined cancellation watchers, deadline provenance and explicit health evidence. A returned operation alone does not establish reusable protocol state.
- `session` owns one leased connection, cancellable admission, bounded non-replayed catalog queries, exact value admission and cursor ownership. A private forwarding connector retains driver cursor-close failures that database/sql would otherwise discard after a fetch failure. This adapter changes no driver source and adds no SQL execution engine.

The optional `memory` package owns only configured-memory SELECT selection and strict scalar/provenance decoding. It does not execute statements, authorize inheritance, bind endpoint identities, aggregate instances or retry operations. Its caller explicitly admits the PDB, inheritance and Oracle-version/fallback conditions. The session and transport packages remain catalog-free.

Driver/URL selection, all other SQL catalogs, endpoint identity and container admission, credentials, retries/recovery choices, progress, localized text and product acceptance criteria remain caller-owned. A closed or interrupted session is not silently reconnected. No statement, transaction or connection escapes the owning API.

Inspect safe top-level error formatting; unwrapped driver errors may contain private connection details. Dispose must be idempotent, unblock owned I/O and return after closure. Queued-query cancellation affects only that waiter. Close refuses new queries and starts cleanup before waiting for active work; timeout disposal must release that work and all cleanup joins before return. No worker is detached to manufacture a timeout.

Compatibility checks retain synthetic driver cases, deadline/cleanup/cursor failures, exact values, socket identity and strict CSV tests. These are module contracts and do not prove any native database migration pair.