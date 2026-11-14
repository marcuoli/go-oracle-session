# Oracle compatibility

## Supported compatibility target

Preserve the Oracle Database 9.2 baseline through Oracle AI Database 26ai,
including the intervening release families, and maintain forward compatibility
with subsequent database releases. New optional features must not raise the
baseline for existing operations. Do not reject an otherwise usable newer
server solely because its version is absent from a known-release list.

This is the library's compatibility requirement. A verified combination also
needs evidence for the exact library commit, upstream driver, Oracle Client
when used, server patch level, operating system and architecture. Neither a
successful connection nor synthetic module CI qualifies every operation.
Native range qualification is tracked in the source repository's issue #16; it remains incomplete.

## Compatibility boundaries

The library owns connection/session/cursor/transaction resource contracts. The
caller selects an unmodified official upstream connector, compatible client
libraries and reviewed SQL. Authentication, protocol negotiation, supported
Oracle Net settings and database SQL features depend on that combination.
One driver/client build is not assumed to cover the entire server range.

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

## Qualification requirements

Cover 9.2, 10.1/10.2, 11.1/11.2, 12.1/12.2, 18c, 19c, 21c, the 23ai/26ai
release line and subsequent releases. Record exact patches instead of assuming
a result on one patch applies to every patch or family. Cover applicable
single-instance/RAC and non-CDB/CDB/PDB deployments without requiring modern
container metadata on legacy servers. Identify inapplicable cases explicitly.

For each selected connector/client/server/platform combination, record:

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
qualification when the driver/client, relevant behavior or server release changes.
Future releases enter this process without an artificial numeric upper bound;
their combinations remain unverified until the required evidence exists.

## Oracle references

Oracle-published references checked on 2026-11-13:

- [Instant Client FAQ](https://www.oracle.com/database/technologies/faq-instant-client.html),
  interoperability section: client/server combinations depend on Oracle's
  interoperability matrix, and individual tools may have narrower requirements.
- [About Oracle Instant Client, 26ai](https://docs.oracle.com/en/database/oracle/oracle-database/26/ntcli/oracle-instant-client.html):
  Oracle refers certified configurations to its interoperability matrix.
  These public pages reference My Oracle Support Doc ID 207303.1; consult the
  current matrix for the exact pair rather than inferring certification here.
- [About Oracle Database Release Numbers, 26ai](https://docs.oracle.com/en/database/oracle/oracle-database/26/upgrd/oracle-database-release-numbers.html):
  numeric release components and the 26ai `23.26` example.

The target range does not change Oracle's product lifecycle, client/server
certification or the consuming application's feature qualification obligations.
