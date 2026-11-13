# Changelog

## v0.3.0

- Add fixed-capacity admitted-session pools and owned native streaming cursors,
  with query/queue deadlines, explicit timeout opt-out, independent cursor cleanup,
  complete-versus-partial status and no automatic replay or recovery.
- Preserve existing buffered Session APIs, exact values and default budgets;
  cover lease ownership, cancellation, cleanup failures, capacity and shutdown.

## v0.2.1

- Expose explicit descriptor and sqlnet security settings to driver adapters, preserving scope, quoted text and duplicates so unsupported certificate constraints cannot be silently ignored.


## v0.2.0

- Add functional oraclenet contracts for bounded alias lookup, typed local failures, descriptor/sqlnet wallet precedence and native/environment wallet paths.
- Preserve final Unicode configuration lines without a trailing newline, and cap actual TNS file reads after the initial size check.
- Preserve existing inventory, cancellation, transport and session APIs; configuration resolution performs no database dial or wallet-content read.

## v0.1.0

- Add functional packages for strict connection inventory, joined cancellation/health evidence, direct physical socket tracking and leased exact-value catalog queries.
- Retain cancellation, concurrent disposal, driver fetch/close failure and precision regressions with caller-owned official connectors and policy.
- Add generic examples, useful comment rules, exact compiler validation, native CI and reviewed immutable module publication.
