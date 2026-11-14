# Changelog

## Unreleased

- Define the Oracle 9.2-through-26ai compatibility target, forward compatibility policy and native qualification requirements; preserve capability-gated optional features without a version ceiling.

## v0.4.1

- Update the module/build requirement and validation workflows to Go 1.27.4.
- Refresh official dependency versions across the full module graph and update govulncheck to v1.9.0; retain explicit indirect version floors for metadata-only dependencies.
- Update official upstream snapshots where no stable release exists, including the metadata-only gomarkdown security fix (GHSA-cv23-7vc5-jfh7).
- Keep stable x/tools v0.52.0 with a compatible official x/exp snapshot; the newest x/exp snapshot requires an unreleased x/tools version.
- Document separately tested source compatibility and its unresolved lower boundary in the README; preserve runtime APIs and behavior.

## v0.4.0

- Add non-replayed Exec, owned transactions and native input/output bind forwarding without a concrete driver dependency.
- Add explicit contextual prepared execution and scoped driver metadata callbacks; preserve direct mode and existing query/stream defaults.
- Retain pool transaction affinity through joined commit/rollback/cancellation cleanup, safe unknown outcomes and single lease retirement.

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
