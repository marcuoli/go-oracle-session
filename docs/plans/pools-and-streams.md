# Bounded pools and owned streams

The approved change adds APIs without changing Session.Open/Query defaults or
buffered exact-value semantics. Implementation uses the existing leased session,
cursor-error tracking and disposal watcher; it introduces no second SQL engine.

- NewPool(ctx, PoolOptions{Size, QueryTimeout, Open}) owns distinct sessions from
  the caller's admission factory. QueryTimeout includes waiting for a pool slot;
  each session's configured budget can expire earlier. No automatic replay or
  connection recovery occurs. Retiring unusable sessions reduces capacity, and
  exhausted pools fail instead of leaving callers queued forever.
- Session.Stream and Pool.Stream accept StreamOptions{NoQueryTimeout, MaxRows}.
  Zero MaxRows uses the session bound; negative limits fail. The explicit timeout
  opt-out retains caller cancellation and positive connection/cleanup budgets.
- Stream Next/Scan/Columns/Err/Close/Complete retains the physical session until
  cursor cleanup and cancellation joining finish. Native Scan conversions differ
  deliberately from buffered Query's exact codec and byte budget. Driver value
  and lazy LOB lifetimes remain those of database/sql and the qualified driver.
- Complete requires EOF, successful Scan calls and successful transport cleanup;
  it does not certify application processing or lazy LOB consumption. Early Close
  cancels the stream, returns cancellation and retires its physical session.
- Idle cancellation auto-finalizes streams. Callback joins happen outside the
  stream lock and never join themselves. Session.Close cancels active streams;
  pool shutdown marks ownership before closing sessions to avoid recursive close.

## Implementation and checks

- [x] Record isolated source state and preserve unrelated work.
- [x] Verify the original complete suite on the configured Go 1.27.3 compiler.
- [x] Observe failing regression tests for new public APIs.
- [x] Implement stream lifecycle and fixed-capacity pool with no legacy Query refactor.
- [x] Cover native Scan, exact row boundaries, EOF/early close, idle and blocked
  cancellation, fetch/close failures, queued budgets, setup cleanup, no replay,
  pool exhaustion, concurrent shutdown and safe error formatting.
- [x] Run Windows/Linux suites and race, vet, module/build/advisory checks and
  independent source review. Native Oracle and additional platform coverage are
  separate consumer/CI gates.
- [ ] Coordinate consumer head changes, integrate, sync canonical source/public
  copy as authorized, and verify exact task-owned source/scratch cleanup.

Synthetic fixtures do not establish native Oracle, godror, Exec/transaction or
PL/SQL output-bind qualification. Preserve existing consumers and independently
validate those paths before replacing them.
