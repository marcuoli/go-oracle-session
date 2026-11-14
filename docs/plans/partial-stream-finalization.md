# Qualified partial stream finalization

Goal: finalize an intentionally retained sample without claiming source EOF, and
reuse the same admitted session only after affirmative qualified driver evidence.

## Contract

- Add SampleHealth{ProtocolSettled, TransactionUsable}, SampleFinalizer and
  SampleFinalization{EOFObserved, StoppedEarly, Evidence} in the session package.
  Evidence reuses cancel.Evidence/Classify rather than inventing a health engine.
- Stream.FinalizeSample(finalizer) belongs to the Next/Scan owner. It closes and
  joins the owned cursor without calling Next to drain unread values, runs the
  supplied native settle/health callback under the original query lifetime and
  positive cleanup budget, then joins cancellation before releasing its lease.
- The callback borrows the original driver only during the call. It must return
  promptly, retain no resources, issue no application SQL/transaction control and
  obey the admitted disposal contract. The caller qualifies actual driver,
  database, platform and protocol/transaction evidence; a generic Close or Ping
  success is not universal qualification.
- This operation supports ordinary Session and Pool streams. Transaction streams
  reject it with ErrUnsupported and retain conservative retirement/rollback
  requirements; raw driver callbacks inside database/sql transactions can deadlock.
- Missing/incomplete proof, fetch/close/probe failure or cancellation retires the
  session. Preserve independent causes and safe error formatting. Callback panics
  propagate only after owned cleanup and watchers join.
- Complete remains an EOF-only result. Successful partial finalization has
  StoppedEarly=true, EOFObserved=false and explicit reusable evidence. Repeated
  finalization/Close joins the same outcome without another callback.
- Existing Query/Stream/Close defaults and no-replay behavior remain unchanged.
  Applications own retained bytes, truncation labels, admission and plan policy.

## Implementation and verification

- [x] Add focused regressions, observe the missing API failure, then implement
  sample.go and the minimal stream finalizer extension.
- [x] Cover no-drain same-session continuation, pool capacity, EOF/idempotence,
  missing proof, independent failures, cancellation, callback panic and shutdown.
- [ ] Run configured Go 1.27.4 tests, race, vet, modules, builds and advisories;
  obtain bounded independent review and preserve unchanged legacy contracts.
- [ ] Have the native validation owner qualify its explicit driver callbacks
  and same-session continuation before claiming native acceptance.
- [ ] Integrate/release through the source workflow, synchronize canonical source,
  verify immutable publication and remove exact issue-owned temporary source and
  obsolete scratch. Consumer adoption remains gated on released qualification.

Public driver interfaces remain injected. No concrete driver dependency, local
third-party patch, automatic replay/reconnect or new SQL execution engine is added.
