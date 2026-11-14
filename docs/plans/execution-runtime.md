# Execution runtime implementation plan

Goal: add non-replayed execution and owned transactions while preserving all published APIs and budgets.
Architecture: reuse Session query/stream/cancellation machinery through an internal command runner. A transaction holds its parent session lease and serializes operations through a private Session view. Only terminal joined cleanup returns that lease. Driver wrappers forward official context interfaces and retain terminal transaction evidence; no driver dependency or raw connection escapes.

Constraints: Go 1.27.3; official upstream dependencies only; no consumer edits; immutable previous releases; generic public examples. Existing direct-query mode remains the default. Native Oracle/driver qualification is separate.

## Public contracts
- ExecOptions{NoQueryTimeout bool}; Session.Exec and Pool.Exec(ctx, Statement, ExecOptions) return (sql.Result, error). Only nil error establishes joined execution completion; output destinations can be mutated even on failure and must then be discarded.
- Options.PreparedContext explicitly selects one-shot context-prepared statement execution. Default direct mode is unchanged. Capabilities() reports observed connection interfaces, not native qualification. ErrUnsupported identifies an unavailable contextual capability, without legacy fallback.
- TxOptions{Isolation sql.IsolationLevel, ReadOnly, NoQueryTimeout bool}; Session.BeginTx and Pool.BeginTx(ctx, TxOptions) return (*Tx,error). Begin budget includes queueing but its timer stops on successful admission; transaction lifetime retains caller cancellation. Every subsequent command has its own normal budget.
- Tx.Exec/Query/Stream use the retained connection, never reacquire the parent lease. Commit(ctx) rejects an open cursor with ErrBusy; Rollback(ctx) cancels/joins it. Terminal operations are serialized, never replayed; repeated calls return sql.ErrTxDone. Outcome() distinguishes active, committed, rolled back and unknown. Failed commit/rollback retire the session and identify ErrOutcomeUnknown when the database outcome cannot be proven.
- Cancellation may trigger database/sql automatic rollback. Track driver terminal completion and retire/close the sql.Conn to join trailing database/sql cleanup before returning the parent lease. No rollback/DDL atomicity or output-destination rollback guarantee is implied.

## Tasks and checks
- [x] Driver boundary and Exec: add exec.go, driver_context.go and execution_test.go; extend cursor.go/Options only as needed. Observe failing capability/output/ErrBadConn tests, then verify unchanged native argument values, statement checker precedence and exactly one execution.
- [x] Transaction owner: add transaction.go and transaction_test.go. Exercise retained pool capacity, successful commit reuse, rollback failure/unknown commit, queued/in-flight/idle cancellation, timeout opt-outs, cursor versus commit, concurrent terminal calls and shutdown joins. Cover failed Begin and setup cleanup.
- [ ] Documentation/integration: update Go doc, README and CHANGELOG; document application adapter and session-affinity boundaries. Run full configured validation/race/advisory/platform checks and independent review. Publish only after source PR/CI/canonical sync; verify released module and exact worktree/admin/scratch cleanup.

Review focus: cancellation racing terminal publication; automatic rollback returning ErrTxDone before its worker joins; prepared statement Close failure after successful execution; driver out destinations changed before an error; transaction startup/shutdown registration races. Tests belong to the owning task above.

Implementation method: inline, with bounded read-only driver-pattern research and one whole-branch independent review. No further design-approval loop is required by the approved repository workflow.

Review rulings: register the transaction callback/active owner under the parent's
publication mutex; reject busy commit before arming cancellation; retain automatic
rollback causes; gate Outcome and Err on the same finalizer barrier. Scoped
WithDriver callbacks support native metadata without annotated driver-magic SQL.
Native resources do not escape the callback; SQL and transaction control there
remain prohibited by its contract. Startup cancellation also retains rollback
failure causes. Adversarial regressions cover each confirmed lifecycle defect.