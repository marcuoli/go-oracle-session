# Oracle compatibility

## Supported compatibility target

Preserve the Oracle Database 9.2 baseline through Oracle AI Database 26ai,
including the intervening release families, and maintain forward compatibility
with subsequent database releases. New optional features must not raise the
baseline for existing operations. Do not reject an otherwise usable newer
server solely because its version is absent from a known-release list.

The required connectivity path uses go-ora's pure Go stack without an Oracle
Client installation or shared libraries. Keep concrete driver dependencies at
caller and integration-test boundaries so the reusable module remains
driver-neutral. Resolve and qualify go-ora compatibility gaps on that path;
switching to an OCI-based connector does not satisfy this requirement.

This is the library's compatibility requirement. A verified combination also
needs evidence for the exact library commit, upstream driver, server patch level,
operating system and architecture. Record Oracle Client as `not used` for this
path. Neither a successful connection nor synthetic module CI qualifies every
operation.
Official driver boundary qualification is tracked in the source repository's
issue #25. Full native range qualification remains incomplete; issue #16 records
the completed shared cancellation delivery.

## Compatibility boundaries

The library owns connection/session/cursor/transaction resource contracts. The
caller selects the configured go-ora connector and reviewed SQL. Shared upstream qualification uses unmodified official driver
releases. Existing consumer adapters and separately approved retained dependency
exceptions must remain in place until equivalent behavior is demonstrated; this
library does not replace or retire them. A consumer-reference result using such
an exception is distinct from qualification with official upstream dependencies.
Retained exceptions keep their owning project's retirement and approval criteria,
including explicit user confirmation where required. Passing comparative tests
or publishing this library does not itself authorize their removal. Authentication, protocol negotiation, supported
Oracle Net settings and database SQL features depend on that combination.
One driver release and configuration are not assumed to cover the entire server
range.

Retain contextual execution, joined cancellation and physical-disposal
requirements on legacy and newer servers. Missing required driver interfaces
return explicit unsupported errors; do not substitute legacy methods, weaken
cleanup guarantees, retry SQL or retry authentication using another driver.
Explicit prepared mode is selected before executing an operation.

Keep optional features separate from the baseline. Callers must select SQL,
views, columns and data types appropriate to observed server capabilities,
privileges and configuration. An unavailable optional feature must not prevent
unrelated baseline operations. A failure of an operation expected to work stays
a failure; do not turn it into a successful empty or zero result.

Use server-reported numeric versions and observed capabilities independently of
marketing names. For example, Oracle's 26ai documentation uses numeric releases
such as `23.26.0.0.0`; the label `26ai` is not numeric major 26. Unknown future
versions retain the established path when its required capabilities are present.
Do not infer support for new features solely from a higher version number.

## Preserving existing connection behavior

A migration must inventory the working connector and its tests before replacing
its connection/session owner. Preserve the following observable contracts;
passing a generic SELECT or a newer-server test is insufficient.

| Behavior to preserve | Shared boundary and acceptance |
| --- | --- |
| Legacy negotiation and authentication | Keep driver selection explicit and before password submission. Retain established legacy compatibility until published upstream fixes are verified; never turn a failed login into a second authentication attempt. |
| Repeated connections | Preserve reviewed public driver options such as disabling a failing fast-login optimization. Test first, repeated and concurrent admissions without changing credentials or endpoints. |
| Legacy prepared cursors | Verify repeated SQL and PL/SQL with identical and changed binds on the same physical session. Recreating a cursor before a later explicit operation is different from replaying a failed operation. |
| Bind conversion and exact values | Preserve native input/output carriers, destination identity, statement-level conversion precedence, character capacity versus byte capacity, large numeric values, typed and untyped NULLs, arrays and supported LOB ownership. |
| Physical socket semantics | Keep actual sockets available to the selected driver's native TLS/OOB behavior; do not substitute a wrapper that hides required socket interfaces. Track redirects and dispose only the owned connection. |
| One cancellation authority | Preserve the distinction between query, parent and stage interruption, independent cleanup budgets and joined query/cursor/break workers. Do not layer competing timeout controllers over a configured adapter. |
| Proven cancellation reuse | A typed cancellation acknowledgement alone is insufficient. Require joined operation, rows, break and cleanup plus protocol settlement and usable transaction state. Optional `session.Cancellation` uses the shared owner for native break, grace and an explicit settlement proof after all workers join. Nil configuration or unknown health retires the session; a canceled result never becomes successful. Keep the existing consumer path until its exact hooks and end-to-end behavior are qualified. |
| Warnings and diagnostics | Retain connection-scoped login warnings and bounded stage diagnostics without global stdout/stderr interception, credential transformation or authentication payload logging. |
| Oracle Net and admission | Preserve alias/descriptor precedence, explicit TLS/security settings, wallet scope, administrative-login semantics and the admitted physical/container identity. Product-specific SQL and recovery policy remain with callers. |

For each migration, record which behavior the shared library already provides,
which remains in the caller adapter, which depends on a retained exception and
which needs further qualification. Keep the original implementation and its regression
coverage until the complete consumer path passes; do not remove it merely
because the shared library is released. Missing upstream functionality must be
tracked explicitly under the dependency policy rather than recreated by a shared
driver fork, cache modification or hidden protocol patch.
## Qualification requirements

Cover 9.2, 10.1/10.2, 11.1/11.2, 12.1/12.2, 18c, 19c, 21c, the 23ai/26ai
release line and subsequent releases. Record exact patches instead of assuming
a result on one patch applies to every patch or family. Cover applicable
single-instance/RAC and non-CDB/CDB/PDB deployments without requiring modern
container metadata on legacy servers. Identify inapplicable cases explicitly.

For each selected go-ora connector/server/platform combination, record:

- Admission and session identity; required privileges; authentication and
  TCP/TCPS configuration actually tested, with credentials kept private.
- Exact NUMBER values beyond floating-point precision, SQL NULL, character
  conversion, supported input/output binds and data-type limits.
- Buffered queries, streaming EOF and early close, limits, Exec, transactions,
  commit/rollback outcomes and repeated prepared operations in the same session.
- Active cancellation, joined cursor/driver/socket cleanup and server-side
  termination evidence separately. Bounded client return alone does not prove
  the server stopped executing.
- Disposal and retirement on uncertain state; reuse only under the applicable
  qualified health contract. Never infer safe reuse from Ping alone.
- Expected unsupported capabilities and negative/error cases, plus operation
  and feature-specific gaps. A read-only query test does not qualify DML,
  output binds, transaction finalization or native LOB handling.

Use isolated approved fixtures for tests that change data or transactions;
read-only qualification must not silently expand into writes or privilege
changes. Preserve real failure evidence and upstream-only dependency rules.
Add reusable synthetic regressions for confirmed defects, and repeat native
qualification when the driver, relevant behavior or server release changes.
Future releases enter this process without an artificial numeric upper bound;
their combinations remain unverified until the required evidence exists.

## Oracle references

Oracle-published references checked on 2026-11-13:

- [About Oracle Database Release Numbers, 26ai](https://docs.oracle.com/en/database/oracle/oracle-database/26/upgrd/oracle-database-release-numbers.html):
  numeric release components and the 26ai `23.26` example.

The target range does not change Oracle's product lifecycle or the consuming
application's feature qualification obligations. Oracle Client interoperability
certification does not establish pure-Go driver compatibility.
