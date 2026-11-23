# go-oracle-session

![Module/build Go 1.27.4](https://img.shields.io/badge/module%2Fbuild-Go%201.27.4-00ADD8)
![Source compatibility Go 1.27.3 verified](https://img.shields.io/badge/source%20compatibility-Go%201.27.3%20verified-2ea44f)

Reusable single-session query ownership, cancellation evidence, direct socket tracking and strict credential-bearing connection inventory. Use the functional imports `session`, `cancel`, `transport` and `inventory`.

Applications provide a selected `driver.Connector`, reviewed catalog SQL, stable command references, exact value codecs and endpoint/acceptance policy. The default requires direct `driver.QueryerContext` support and rejects fallback. `Options.PreparedContext` explicitly selects contextual prepared statements instead. No concrete driver dependency is forced on consumers; operations use one leased connection and are never replayed.

Set `Options.PreserveStatementText` only when caller-approved SQL must reach the
driver unchanged, such as a prepared plan whose visible SQL hash must match the
executed text. The default still inserts the stable-reference comment. Both modes
require a valid Reference and the same nonempty/1 MiB text bound; safe error
references, binds, limits and admission are unchanged. The option applies to
Query/Stream/Exec and transaction views; pool factories configure each Session.
It supplies no SQL authorization or read-only guarantee.

Numeric catalog expressions should produce explicit text, for example `TO_CHAR` with a stable numeric convention. Default values preserve text, integers and SQL NULL; floats are rejected. A result is returned only after the cursor, terminal error, cleanup cause and cancellation watcher have joined. No retained prefix counts as a complete result.

Configure positive connect, query and cleanup budgets plus a required idempotent physical-disposal hook. The hook must unblock and join all owned connector/driver I/O. Cleanup includes admission and may outlive the parent cancellation to release owned resources. Query admission honors its own context without cancelling another active query. These contracts cannot provide a hard deadline for a driver, codec or hook that does not return as required. Actual database/version/platform qualification belongs to consumers.

The inventory parser preserves password bytes; do not serialize entries or log underlying error chains. Default diagnostic formatting exposes safe operation codes and CSV source-line facts. This is not a general-purpose SQL safety engine, credential store, recovery manager or database certification.

For explicit wallet references, opt in with inventory.ParseAuthenticated or
inventory.ReadFileAuthenticated. They return AuthEntry values: Auth is
password (the default) or wallet, with a WalletReference only for wallet rows.
All four legacy CSV headers remain required. The additional headers are auth,
wallet_directory and wallet_credential. Wallet rows leave username/password
empty; password rows leave wallet fields empty. References preserve exact bytes,
require a current-platform absolute directory and a nonblank case-sensitive key,
and are each bounded to 4096 bytes of valid UTF-8 without control characters.
Admission does not expand paths, open wallet files or resolve credentials;
callers own that work. Existing Parse, ReadFile, Entry and Options remain unchanged.

The unchanged shipping module and current build require Go 1.27.4. Run `scripts/validate.ps1 -Race -Scan` or `scripts/validate.sh --race --scan` with configured external development storage. `examples/inventory` demonstrates synthetic parsing without contacting a database. See [architecture](docs/architecture.md), [comments](CONTRIBUTING.md) and [delivery](docs/code-management.md).

## Oracle compatibility

The required connection path uses go-ora's pure Go stack and must remain free of
Oracle Client installation and shared-library requirements. Existing functional APIs
remain driver-neutral. The optional goora/compat package ships the explicitly
authorized retained v2/v3 profile as complete SDK module content; see the
[shared driver contract, ownership and native matrix](docs/shared-driver-compatibility.md).

The compatibility target is **Oracle Database 9.2 through Oracle AI Database
26ai**, with forward compatibility for subsequent releases. Preserve legacy
baseline operations while gating optional newer features by capability; do not
impose a numeric upper-version cutoff. Exact driver/client/server/platform
combinations require native qualification. See the [compatibility contract and
qualification checklist](docs/oracle-compatibility.md); the full range is not
yet natively qualified by this module.

For configured SGA and PGA facts, use
[`go-oracle-info/memory`](https://pkg.go.dev/github.com/marcuoli/go-oracle-info/memory)
with a caller-admitted session. The fact library owns Oracle SQL selection and
decoding, including container scope, value provenance and the guarded
Autonomous Database fallback. The session library owns query execution and
connection lifetime.

## Go compatibility

The shipping module and configured builds require Go 1.27.4.

The SDK source-compatibility floor is Go 1.27.3. The exact 1.27.3 compiler
passed Windows/amd64 tests, race checks, vet, module verification and builds
with only the Go directive lowered in an isolated copy of the SDK. The same
source cross-built for Linux/amd64 and macOS/arm64.

Go 1.27.2 rejected the unchanged go-sqltemplate v0.1.1 requirement before
compilation. Go-textoutput v0.1.1 also declares Go 1.27.3. Mingo v0.19.0
identified the Go 1.27 family. That analysis does not resolve patch versions.
The unchanged shipping module still requires 1.27.4. The source probe is
separate from native Oracle qualification.
The independent native harness requires Go 1.27.4 through its released SDK dependency.

License: UPL 1.0.

## Pools and native streaming

`session.NewPool(ctx, session.PoolOptions{Size: 4, QueryTimeout: budget,
Open: openAdmittedSession})` owns a fixed set of independent sessions. The
application factory opens and admits each fresh physical session, including its
settings and disposal hook. Ownership transfers to the pool. The query budget
includes waiting for a free session; a session's own budget may expire earlier.
`Pool.Query` retains the existing complete, exact buffered result contract.
For one larger or smaller complete inventory, use the additive
`Session.QueryWithOptions`, `Pool.QueryWithOptions` or `Tx.QueryWithOptions` with
`session.QueryOptions{MaxRows: ..., MaxBytes: ...}`. Zero fields inherit defaults;
negative limits fail before lease admission. These copied limits affect only that
call's retained rows/text bytes, preserving codecs and cancellation/cleanup. See
[per-call buffered-query limits](docs/query-limits.md).

`Session.Stream` and `Pool.Stream` take `session.StreamOptions` and expose an
owned cursor with `Next`, `Scan`, `Columns`, `Err`, `Close` and `Complete`.
Streaming keeps its session leased until cursor cleanup finishes. Native `Scan`
preserves database/sql conversions; buffered Query's codec and MaxBytes do not
limit native driver values. Applications own memory limits and must consume
borrowed bytes and lazy LOB values before advancing or closing the cursor.
Complete means EOF, no Scan failure and successful cursor/cancellation cleanup;
it does not establish application processing or lazy value consumption.

Zero stream MaxRows uses the configured session bound; a positive override is
allowed and a negative one fails. Exactly the limit followed by EOF succeeds;
an additional row fails with ErrCapacity. NoQueryTimeout explicitly disables
the stream's pool/session query deadlines while retaining parent cancellation
and positive connection/cleanup budgets. Cursor cleanup remains bounded even
when Next closes the driver cursor internally at EOF.

Always close an unexhausted stream. Early Close reports cancellation, marks it
incomplete and, by default, retires the physical session. Qualified native
cancellation may preserve it as described below. Close after successful EOF returns
nil. Pool Close wakes queued callers, cancels active streams and joins owned
session cleanup. Retiring unusable sessions reduces pool capacity; exhaustion
returns ErrUnavailable. There is no automatic SQL replay, reconnection or recovery
policy. Callers decide whether and when to construct a new admitted pool.

The original Session.Open/Query options and exact result behavior remain stable.
Native driver/version/platform qualification remains the consuming application's work.

The `oraclenet` package resolves bounded `tnsnames.ora` aliases and wallet paths without a driver. It preserves connection-compatible alias precedence and descriptor text, handles `MY_WALLET_DIRECTORY`, `sqlnet.ora` file wallets and native environment expansion, and never reads wallet contents. Callers decide when a selected TCPS endpoint needs client credentials; ordinary TCP endpoints must not inherit a wallet. `ResolveDescriptorWallet` considers explicit descriptor settings only, without automatic file discovery. Application diagnostics and TLS negotiation remain caller-owned.


### Native cancellation and continuation

`Options.Cancellation` opts one physical session into an owned native break and
caller-qualified reuse proof. Configure `QueryGrace`, `Break` and `Reusable` for
that exact connection; the proof must inspect every operation/cleanup cause,
require a typed native acknowledgement when a break was requested, and establish
settled protocol plus usable transaction state. Nil configuration keeps the
existing retirement behavior. A canceled operation still fails, discards any
buffered result and leaves a stream incomplete even when the next command can
reuse the session.

The same command owner observes the original context and joins the native break,
operation and cleanup. It gives driver commands a value-preserving noncanceling
context to avoid a second cancellation authority. Parent/stage cancellation
shortens query grace, and independent cleanup remains bounded. Transaction
rollback waits for active command/break completion. Unknown proof, a blocked
break, unexpected joined causes or fatal connection errors retire the session;
callers may admit a fresh session for later independent SQL. Failed SQL is never
replayed and no recovery policy is selected automatically.

### Intentional partial samples

For an intentional sample that must continue on the same admitted connection,
call `Stream.FinalizeSample` from the Next/Scan owner after consuming borrowed
values. It closes the cursor without calling Next again, then invokes a
caller-qualified `SampleFinalizer` on the original driver. Driver close/settling
can still perform I/O. The callback is bounded by the original query context and
positive CleanupTimeout, and must join I/O and retain no resources. Native health
operations must preserve the admitted physical session; some driver health getters
can release pooled connections and are unsuitable without explicit qualification.

Only affirmative `SampleHealth` protocol/transaction evidence plus error-free
joined cleanup allow reuse. A generic Ping or successful cursor Close is not
universal proof; qualify the actual native hook, disposal, database version and
platform. The library supplies no native health implementation. A missing hook,
missing evidence or unexpected callback/close/fetch failure retires the session.
If Cancellation is configured, that same owner handles interruption during the
finalizer; its cooperative context is signaled when disposal is committed, and
only affirmative native proof permits later reuse. Cancellation remains an error.
Transaction streams reject early finalization with ErrUnsupported.

The returned `SampleFinalization` separates EOFObserved, StoppedEarly and
cancellation/health Evidence. Successful early finalization leaves Complete false
and Err nil; it never makes retained rows a complete result. Existing terminal
outcomes are stable, and repeated finalization does not invoke the callback again.
Normal Close still cancels an unfinished stream; absent qualified cancellation
proof it retires its session.

### Samples on a caller-owned pool

Lease a native connection with `sql.DB.Conn`, then use `session.SampleBorrowed`
for an intentional sample on that exact connection. The callback works with
native `sql.Rows`, including `ColumnTypes` and `Scan`. Finish using any borrowed row
or LOB values in the callback and let the SDK close the cursor. Supply a
`SampleFinalizer` qualified for the actual driver and deployment. Use the existing
caller context and keep the lease exclusive until the call returns. The
finalizer works only with its native driver argument. It must avoid reentry
through database/sql while the raw connection is held.

The SDK leaves a qualified connection in the caller's hands. After failed,
canceled, or unproven cursor cleanup it marks the database/sql connection bad.
The caller must qualify that the configured driver retires the physical session
when database/sql closes it. This includes the driver's own native pooling,
when enabled. An ordinary query rejection follows database/sql handling.
This API handles intentional samples outside transactions. Consume complete
datasets through EOF using the normal native query or rowset interfaces.

### Selecting a borrowed-sample finalizer

`compat.QualifyBorrowedSample` checks the supplied native lease before the
caller dispatches its statement. It requires exclusive, nontransaction
ownership and checks read-only server identity on that connection. The initial
profiles are TCP Oracle 26ai 23.26.1.0.0, PRIMARY READ WRITE CDB root, with an
ordinary SYSTEM login. The checked providers are Windows amd64 retained V2
and Linux amd64 godror v0.51.0 with standalone OCI client 23.26.1.0.0.
OCI callers supply standalone and client-version facts from the selected
connector and raw driver.

An unsupported decision permits the caller to use its existing route before
dispatch. Treat any other error as a failure of admission or the identity
check. A successful decision binds the finalizer to the original native
connection. Keep the same lease and cancellation ownership for
`session.SampleBorrowed`. Transaction usability depends on the qualified
profile and the caller's nontransaction assurance.

## Execution and transactions

`Session.Exec` and `Pool.Exec` accept `Statement` and `ExecOptions` and return
`sql.Result`. `NoQueryTimeout` disables the configured queue/query deadline,
retaining caller cancellation and independent cleanup budgets. Bind values,
including `sql.Named`, `sql.Out` and driver-native output carriers, pass through
upstream conversion hooks. Only a nil error establishes joined execution success.
Drivers can mutate output destinations before an error; discard those values and
never access the destinations concurrently. Execution success does not prove a
transaction committed or make DDL atomic.

`Session.BeginTx` and `Pool.BeginTx` take `TxOptions` with standard isolation and
read-only settings plus an explicit setup-timeout opt-out. Queueing/setup share
an admission deadline. On success that temporary timer stops; transaction lifetime
follows caller cancellation, while each command gets its own query budget.
The owned `Tx` provides `Exec`, exact buffered `Query`, native `Stream`, `Commit`,
`Rollback`, `Outcome` and `Err`. Defer `Rollback` when admitting a transaction.
It retains one physical session, including pool capacity, until terminal cleanup
joins; nested operations serialize without reacquiring that parent lease.

Exhaust or close a transaction stream before commit. `Commit` returns `ErrBusy`
without ending an already-open cursor's transaction; `Rollback` cancels and joins
it. Commit honors its caller cancellation and positive cleanup budget. Rollback
uses independent bounded cleanup even after caller cancellation. Only one terminal
driver call is dispatched; subsequent calls return `sql.ErrTxDone`. `Outcome`
remains `TxActive` until finalization publishes matching `Err`; terminal driver
evidence becomes `TxCommitted`, `TxRolledBack` or `TxUnknown`. Failed or unproven
terminal outcomes include `ErrOutcomeUnknown` and retire the physical session.
A confirmed terminal outcome can still accompany a cleanup error. Do not replay
commands or commit after an unknown outcome. Automatic rollback on cancellation
is joined through tracked driver completion and physical connection retirement.

Direct mode negotiates `ExecerContext` and `ConnBeginTx` separately and reports
`ErrUnsupported` for missing contextual capabilities. Explicit prepared mode uses
`ConnPrepareContext` plus statement `StmtExecContext`/`StmtQueryContext`, statement
`NamedValueChecker` precedence and `ColumnConverter`; it never invokes legacy
fallback methods or retrying exported `sql.Stmt` helpers. `Capabilities()` describes
observed connection interfaces, not server or statement qualification.

`Session.WithDriver` accepts a `DriverCallback` for driver-specific
metadata under exclusive ownership. The callback receives the bounded context and
original driver only during that call; inspect before BeginTx, because an active
transaction retains the Session gate. Retain no driver/resource, issue no SQL,
manage no transaction, and do not call the same Session/Tx from inside it. This
supports native version/type inspection without SQL magic or returning internal
`sql.DB`, `sql.Conn`, `sql.Tx` or `sql.Stmt` objects. Inspect pool members in the
caller's admission factory. Qualify every native callback and disposal hook.

Application migration needs an adapter from native `Stream` to its own row-reading
interface; an owned stream is not a concrete `*sql.Rows`/`*sql.Row`. Session-local
multi-command sequences require a caller-owned `Session`. Independent `Pool.Exec`
calls need not use the same connection, and a transaction should not be used merely
to obtain affinity. Connect settings, admission, recovery, SQL policy, native driver
qualification and publication of complete output values remain caller-owned.
## Full query datasets

The functional rowset package provides native Query/QueryJSON and complete Read/ReadJSON for borrowed sql.Rows or existing lease-owning Stream cursors. Preserve cursor ownership and context; only complete, bounded JSON succeeds. See [rowset contracts and examples](docs/rowsets.md). Oracle fact selection and application policy remain outside this generic layer.

## Explicit Oracle Net text parsing

`oraclenet.ParseTNSNames([]byte)` and `oraclenet.ParseSQLNet([]byte)` parse
caller-supplied NUL-free UTF-8 up to 1 MiB without file, environment, wallet or
network access. Typed results retain complete parsed values with JSON field tags.
Callers retain the original bytes and their provenance separately; comment
removal and effective-alias selection are not a lossless source archive.

TNS aliases are uppercase and sorted by name. Multiple aliases are supported;
duplicate aliases use the last definition, matching resolver lookup. Descriptor
text is retained after comment removal. The pure API additionally rejects
unsupported scalar assignments and trailing syntax rather than silently skipping
them; existing resolver admission and lookup behavior are unchanged. SQLNet
assignments retain source order and duplicates, with uppercase names and opaque
nested values. Pure parsing requires nonempty scalar values to be one unquoted token
without whitespace or quotes, or one fully double-quoted token; unmatched quotes
and trailing tokens are rejected. These parsers do not validate parameter-specific meanings or
connection reachability.

Both APIs retain literal `IFILE` declarations without expanding or reading them.
An include means only the supplied file has been parsed, not the effective
configuration across its include graph. SQLNet entries include the `IFILE`
assignment as well as listing it separately in `Includes`; TNS `Entries` contains
only effective aliases. Errors use static capacity, encoding or syntax sentinels
and never return a partial prefix or supplied values.

## Explicit Oracle diagnostics

Use the functional `diagnostic` package only when a caller explicitly needs retained
Oracle messages in a private diagnostic artifact. Ordinary session errors keep their safe
operation/code formatting. `diagnostic.Oracle(err, knownSecrets...)` traverses bounded
wrapped/joined causes and returns owned `Message` values; it never mutates the errors or
the secret slice. Non-Oracle and code-only errors have no message. Include all known
connection secrets; Oracle text may contain object names and data from the database.

Credential assignments, authorization values and connection URLs are redacted before
known secrets and before stack splitting. Code tokens remain literal when a supplied
secret is contained entirely within the code. Invalid UTF-8 leaf text is ignored.
Oversized messages, more than 32 unique messages, graphs beyond 64 nodes, more than
64 supplied secrets or secret bytes beyond `diagnostic.MaxBytes` return
`diagnostic.ErrCapacity` with no diagnostic prefix. Each complete output is also bounded
by `diagnostic.MaxBytes`. A code-only historical error cannot recover its original server
message through this API.

## Optional go-ora URL policy

The optional `github.com/marcuoli/go-oracle-session/goora` package exposes
`PrepareURL(*url.URL) error`. Call it on the caller-owned URL before creating
the official go-ora connector, including connectors created by a pool's admission
factory. It selects full login by replacing all driver-recognized `FAST LOGIN`
keys with one `FALSE` value.

The helper uses `url.ParseQuery` and `url.Values.Encode`: other option values
and duplicate values remain, while query order and escaping are canonicalized.
Credentials and all non-query URL fields are unchanged. A nil URL or malformed
query returns static `goora.ErrInvalidURL` without mutation. Endpoint and
remaining driver-option validation still belong to the caller and driver.

This package imports no concrete driver and opens or wraps no connection.
Original driver interfaces, connection ownership, cancellation and streaming
remain with the configured path. Prefetch, LOB fetching and session SQL tuning
remain caller-owned. The supported full-login setting addresses cached-login
admission failures described in [upstream #732](https://github.com/sijms/go-ora/issues/732);
it does not establish native qualification for every driver/server combination.

## Codex skill

For shared connection-compatibility and driver-adoption guidance, use the
repository-owned [go-oracle-session skill](skills/go-oracle-session/SKILL.md)
and its [detailed reference](skills/go-oracle-session/references/go-ora-compatibility.md).

Install or discover the complete skill folder separately through your Codex
skill-installation or approved linked-folder workflow, preserving its references.
After discovery, normal automatic skill selection applies; it can also be invoked
as $go-oracle-session. Importing or updating the Go module does not register,
install or load a Codex skill. This repository is the canonical skill source;
applications can link to it instead of maintaining copies of the full guide.

## Complete rowset JSON envelopes

The additive `rowset.QueryEnvelopeJSON(ctx, open, schema, limits, options)`
reserves the exact enclosing JSON overhead before calling a lazy cursor opener.
Use `rowset.OpenQuery(queryer, statement, args...)` for a borrowed native handle,
or supply a `rowset.CursorOpener` returning an existing owned Stream.
Only `rowset.Read` consumes/finalizes the cursor; no connection, SQL, retry or
cancellation policy moves into the envelope operation.

`EnvelopeOptions.Provenance` supplies default metadata. An optional ordered
`Header` object can add fields while leaving the reserved final `data` member
to rowset. Metadata is encoded once, before opening. `Validate` inspects the
completed Table after finalization; `MaxRows` can impose a stricter cap.
`Limits.MaxBytes` counts the entire final envelope. Exact integers, NULLs,
native column casing, row order and duplicates retain the existing contract.

Every error returns nil JSON. `EnvelopeError.Stage` distinguishes limits,
header, budget, query, consume, validate and encode for caller-specific error
wrapping; `Unwrap` retains causes. Early input/opener/validation errors retain
their precedence over a competing cancellation. Existing `ReadJSON` and
`QueryJSON` APIs and behavior remain available unchanged.
