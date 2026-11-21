# Query JSON Envelope Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans inline, followed by one fresh bounded whole-branch review. Follow the approved workflow without another design-approval loop.

**Goal:** Replace repeated consumer envelope mechanics with one compatible additive rowset API.

**Architecture:** Freeze an ordered metadata object and reserve its exact overhead before opening. Use existing Read for consumption/finalization, then optional validation and one bounded complete JSON result.

**Tech Stack:** Go 1.27.4, database/sql, encoding/json, existing synthetic native-cursor fixtures.

**Spec:** json-envelope-spec.md

## Global Constraints

- Preserve existing ReadJSON/QueryJSON APIs, exact values/NULL/order/duplicates, caller context and typed error chains.
- SQL, fact validation, connections and cancellation authority remain caller-owned; no Oracle SQL, driver or dependency changes.
- Work inline in the issue-owned V: worktree; preserve canonical main, issue6 and private inputs.
- Publication uses ordinary immutable module refs; no source archive distribution assets.

## Review Focus

- Competing canceled context and invalid limits/header/lazy admission retain legacy input precedence: TestQueryEnvelopeJSONInputPrecedence.
- Metadata with escaped/reserved data or non-object values cannot undermine reservation: TestQueryEnvelopeJSONRejectsInvalidHeader.
- Failed opener returning a live or typed-nil cursor must not leak/iterate: TestQueryEnvelopeJSONFinalizesFailedOpen.
- Exact total budget, Unicode escaping and stricter row caps cannot return a prefix: budget and failure tests.
- Validator failure/cancellation occurs only after finalized consumption: TestQueryEnvelopeJSONValidationAndCancellation.

## Task 1: Add and qualify the envelope API

**Files:** rowset/envelope.go, rowset/envelope_test.go, rowset/cursor_test.go, rowset/rows.go only if needed to reuse existing nil-cursor guard.

**Interfaces:** CursorOpener func(context.Context) (Cursor,error); OpenQuery(Queryer,string,...any) CursorOpener; EnvelopeOptions{Provenance any; Header any; Validate func(Table)error; MaxRows int}; QueryEnvelopeJSON(context.Context,CursorOpener,[]Column,Limits,EnvelopeOptions)(json.RawMessage,error); EnvelopeError.Stage and Unwrap.

- [x] Write meaningful failing exact-output, total-budget, custom-header, stage/precedence, owned-cursor and failure tests; observe the missing API failure.
- [x] Implement lazy OpenQuery and the bounded envelope operation; stages limits/header/budget/query/consume/validate/encode preserve underlying causes and static messages.
- [x] Run rowset tests/race and existing API regression tests; commit the tested API and notify the consumer owner for preview.

## Task 2: Review, integrate and close

**Files:** CHANGELOG.md, README.md, docs/code-management.md only if a delivery rule needs clarification; private validation/coordination/cleanup receipts on V:.

- [ ] Document exported contracts and usage; run configured full validation/builds/scans with Go1.27.4.
- [ ] Obtain one independent whole-branch review; resolve important findings with regression coverage.
- [ ] Record the exact SDK production added/deleted lines and eligible/final commits; coordinate consumer evidence and the minimum compatible release.
- [ ] Complete normal reviewed source PR/CI, public parity, canonical synchronization and immutable module publication; verify an official-module external consumer without local replace.
- [ ] After consumer detachment, reconcile and remove exact task source/admin/scratch through Git, preserve required evidence, verify protected state and close/reread the issue.
