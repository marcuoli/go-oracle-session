# Shared pure-Go dual-driver implementation plan

> **For agentic workers:** Use superpowers:executing-plans inline. One bounded independent whole-branch review is required after implementation; no implementer fan-out.

**Goal:** Ship an additive single-executable v2/v3 compatibility package as complete immutable SDK module content.

**Architecture:** Internal retained driver packages have unique registrations and rewritten owned imports. Public goora/compat adapts reviewed connector, bind, exact-value and LOB/cancellation contracts while caller-owned connection policy and SQL remain at the boundary.

**Tech Stack:** Go 1.27.4, database/sql/driver, retained official v2.9.0/v3.0.1 baselines plus reviewed compatibility deltas, existing transport/session/cancel contracts.

**Spec:** shared-compatibility-spec.md

Implementation and bounded qualification are complete at runtime 85b1b0e9.
Final delivery status and cleanup receipts are tracked by source issue46; this
plan is not a substitute for those live publication/closure checks.

## Global constraints

- Pure Go; no Oracle Client/OCI remedy. Existing SDK packages, issue6 and retained consumer copies remain unchanged.
- Source retention is the explicit scoped user exception recorded in issue46; no consumer replacements or build-time rewriting.
- Default V2, explicit V3 and opt-in pre-auth sentinel negotiation; no arbitrary failure fallback or auth/SQL replay.
- Preserve endpoint/credentials/context/deadline, typed/exact values, native cursor/LOB and joined cancellation ownership.
- Record static/synthetic/distribution/native outcomes separately; unexecuted combinations are not qualified.
- Canonical source stays on I:, issue worktree/output on project-first V:. Private provenance and lab inputs never enter Git.

## Review focus

- A malformed 41-byte ACCEPT or overflowing offset returns a safe failure, never panic-based selection.
- A joined sentinel+cleanup error, cancellation, failed authentication or transport never opens a second backend.
- Existing official-driver imports coexist without duplicate registration or hidden global policy changes.
- Direct/prepared OUT, NULL and multibyte/NUMBER/LOB values retain type, capacity, exactness and ownership.
- Concurrent admissions/row closure/cancellation finish exactly once; one context/cancellation authority survives.

## Task 1: Package the reviewed retained sources

Files: internal/compatdriver/v2/**, internal/compatdriver/v3/**, internal/compatdriver/README.md, internal/compatdriver/sources.json, AGENTS.md scoped exception.
- [x] Verify pinned origin source against its complete normalized manifest and current official tag/proxy/fix status; preserve exact private approval/source mapping.
- [x] Transplant only production sources, licenses and reviewed regression files; rewrite imports and registrations once into module-owned packages, omit nested manifests/private origin prose.
- [x] Run retained-package regressions, add/observe failing explicit 41-byte/offset/length cases where uncovered, then implement only necessary bounds fixes.
- [x] Verify completeness, upstream-versus-retained patch inventory, private-marker exclusion and no replace/module-boundary escape.
- [x] Commit reviewed foundation and record exact source/head/check ledger.

## Task 2: Add opt-in connector and stable adapters

Files: goora/compat/connector.go, policy.go, bind.go, number.go, lob.go, legacy.go, modern.go, parameter_coder.go, errors.go and focused tests.
Interfaces: Mode (V2/V3/Negotiated); Options with Mode, Dialer and LoginWarningHandler; NewConnector(dsn string, options Options) (*Connector,error); Connector.Connect/Driver; stable Out{Dest any;Size int;In bool}, exact Number constructor/string API, CLOB lifecycle adapter; exposed break/settlement capability without a watcher.
- [x] Pin concrete consumer signatures/source checkpoint before integration; record driver-specific FAST LOGIN policy and LOB ownership/memory distinctions.
- [x] Add failing registration, default/explicit/sentinel-only selection, exact-context/DSN, no-retry and joined-close tests.
- [x] Reuse generic reviewed connector/prepared/parameter-coder code without application logging or watchdog policy; implement connector and selected-driver identity.
- [x] Add failing OUT/NULL/multibyte/NUMBER/CLOB direct/prepared tests; implement matching named-value and lifetime adapters, preserve native optional interfaces.
- [x] Test existing official driver coexistence in an isolated downstream binary; commit green adapter contract and notify consumer owner.

## Task 3: Qualify standalone packaging and authorized native behavior

Files: integration/oracle compatibility target/adapters/tests as needed; package regression tests; V: native/distribution evidence.
- [x] Validate a Git-produced module-ready source ZIP and standalone candidate consumer using GOWORK=off, no local replacement and correct module boundaries; official released-module proof remains in Task4.
- [x] Reuse authorized fixtures/profiles only after confirming exact target identity/current ownership. Qualify repeated/concurrent physical admissions and the bounded exact-value/OUT/CLOB scope on available patches. Native active cancellation/discard remains explicitly unqualified.
- [x] Preserve full failure evidence and independently mark unsupported/unavailable/unexecuted families; do not reuse another application's outcome as SDK qualification.
- [x] Run scripts/validate.ps1 -Race -Scan with Go1.27.4, target builds and configured harness checks; retain logs and resolve required failures.

## Task 4: Review, document and deliver

Files: README.md, docs/oracle-compatibility.md, skills/go-oracle-session/**, CHANGELOG.md, scoped source-exception/patch manifests.
- [x] Document API, default versus negotiation limits, source retention, exact tested conditions and migration requirements; update SDK skill/reference to the real API.
- [x] Obtain bounded independent whole-branch review, fix important findings and run only newly affected checks.
- [ ] Complete reviewed source PR/CI, immutable module release, both-host parity and canonical sync; verify the downloaded official module with a standalone consumer and audit public source contents.
- [ ] Coordinate consumer switch/holds, inventory and remove issue-owned worktree/admin/source/scratch through Git, verify protected state, then close/reread issue46.

## Execution ruling

The user-authorized repository workflow says to proceed with approved work without another design-approval loop. Implement inline and coordinate the concrete API early with the consumer owner; do not add a skill-driven approval pause. Baseline runtime checks at unchanged cfdb294 already passed on all3 platforms and are reusable until covered inputs change.
