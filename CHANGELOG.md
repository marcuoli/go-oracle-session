# Changelog

## Unreleased

- Add a protocol-only health probe for shared connections and document the common consumer setup (#61).

- Accept caller-provided TLS configuration in the shared connector while retaining the SDK's driver selection (#59).

- Let callers observe each connection attempt and stop admission or fallback if recording fails (#57).

- Expose per-attempt handshake diagnostics (#55) through the shared connector while preserving driver selection and connection errors.

## v0.16.0

- Report reviewed SQL tables and views and mark a uniquely named Oracle object error (#52).

## v0.15.0

- Add lazy, complete rowset JSON envelopes with exact total budgets, ordered custom metadata, optional validation/row caps and typed failure stages; preserve existing APIs and cursor ownership (#50).

## v0.14.0

- Add opt-in module-owned pure-Go v2/v3 compatibility with bounded decoder, exact bind/NUMBER/CLOB adapters and complete retained-source/license provenance (#46).
- Retire uncertain transaction/LOB state, join lazy-read cancellation/disposal and preserve terminal cleanup causes without SQL replay or reauthentication (#46).

## v0.13.0

- Add bounded redacted connection diagnostics for native ORA/TNS and typed DNS/TCP/timeout/TLS causes, preserving existing Oracle-only extraction and error identity (#47).

- Package shared go-ora compatibility guidance as a portable repository-owned Codex skill and preserve existing documentation links.

## v0.12.0

- Add opt-in authenticated inventory admission with exact wallet references, safe validation and unchanged legacy structs/contracts (#40).

- Document distinct go-ora login/parser failures, upstream ownership, official-fix adoption and exact compatibility qualification.

## v0.11.0

- Add opt-in goora.PrepareURL full-login policy with canonical query serialization, preserved option values and atomic safe errors (#38).

- Strengthen exact-value, unused transport closure and Oracle Net security boundary/ownership regressions (#36).

## v0.10.0

- Add explicit bounded Oracle diagnostic extraction with credential redaction, preserved error chains and no partial stack on overflow (#32).

## v0.9.1

- Bound buffered cursor cleanup under the command owner and retire failed cleanup; preserve transaction lifetime cancellation causes and stop CSV ingestion at its row limit (#33).
- Reuse default stream cleanup, strengthen file/schema/security/queue regressions and correct ongoing qualification tracking (#33).

## v0.9.0

- Add bounded pure Oracle Net text parsing with full typed entries, literal unresolved include declarations, strict whole-scalar admission and static errors; preserve existing resolver behavior (#30).

## v0.8.0

- Add immutable per-call QueryOptions and QueryWithOptions on Session, Pool and Tx; preserve default budgets and existing codec, cancellation, lease and finalization behavior (#26).

- Validate both native driver targets against released v0.7.0 without a first-party replacement; include cancellation-continuation checks in their normal integration builds and scans.

## v0.7.0

- Add opt-in per-session native cancellation hooks with one joined break/grace/cleanup owner, affirmative same-session reuse proof, preserved parent/stage causes and conservative fallback; retain default retirement and never replay interrupted SQL.
- Preserve native transaction rollback ordering and cooperative sample finalization under that owner; add direct/prepared, pool, transaction and native continuation regressions.

## v0.6.0

- Add generic rowset Query/QueryJSON and complete bounded cursor JSON, sharing native sql.Rows and existing owned Stream lifetimes without another transport, raw lease escape or Oracle fact policy.
- Require the pure-Go go-ora connection path without Oracle Client installation or shared libraries; qualify legacy and future compatibility on that path.
- Define the Oracle 9.2-through-26ai compatibility target, future-release policy and native qualification requirements; preserve existing connector workarounds and single-owner cancellation semantics before consumer migration.
- Add an isolated opt-in native qualification module with explicit fixture admission, separate official driver targets and regression cases for NULL framing, character output capacity and session lifecycle; retain unqualified combinations and upstream gaps explicitly.

## v0.5.0

- Add explicit preserved statement text across Query, Stream, Exec and transaction views, retaining default reference annotation and mandatory validation/bounds.
- Add explicit bounded sample finalization with caller-qualified native health, separate EOF/reuse evidence and no implicit fetch drain.
- Preserve early Close retirement and complete-result semantics; join finalizer interruption/panics, reject transaction Raw access, and retain independent terminal causes.

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
