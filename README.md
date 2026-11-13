# go-oracle-session

Reusable single-session query ownership, cancellation evidence, direct socket tracking and strict credential-bearing connection inventory. Use the functional imports `session`, `cancel`, `transport` and `inventory`.

Applications provide an official `driver.Connector` implementing `driver.QueryerContext`, reviewed catalog SQL, stable command references, exact value codecs and endpoint/acceptance policy. No concrete database driver is forced on consumers. Queries use one leased connection and are never replayed by this module. A driver declining context queries is rejected; prepare fallback is intentionally unavailable.

Numeric catalog expressions should produce explicit text, for example `TO_CHAR` with a stable numeric convention. Default values preserve text, integers and SQL NULL; floats are rejected. A result is returned only after the cursor, terminal error, cleanup cause and cancellation watcher have joined. No retained prefix counts as a complete result.

Configure positive connect, query and cleanup budgets plus a required idempotent physical-disposal hook. The hook must unblock and join all owned connector/driver I/O. Cleanup includes admission and may outlive the parent cancellation to release owned resources. Query admission honors its own context without cancelling another active query. These contracts cannot provide a hard deadline for a driver, codec or hook that does not return as required. Actual database/version/platform qualification belongs to consumers.

The inventory parser preserves password bytes; do not serialize entries or log underlying error chains. Default diagnostic formatting exposes safe operation codes and CSV source-line facts. This is not a general-purpose SQL safety engine, credential store, recovery manager or database certification.

Go 1.27.3 is required. Run `scripts/validate.ps1 -Race -Scan` or `scripts/validate.sh --race --scan` with configured external development storage. `examples/inventory` demonstrates synthetic parsing without contacting a database. See [architecture](docs/architecture.md), [comments](CONTRIBUTING.md) and [delivery](docs/code-management.md).

License: UPL 1.0.

## Pools and native streaming

`session.NewPool(ctx, session.PoolOptions{Size: 4, QueryTimeout: budget,
Open: openAdmittedSession})` owns a fixed set of independent sessions. The
application factory opens and admits each fresh physical session, including its
settings and disposal hook. Ownership transfers to the pool. The query budget
includes waiting for a free session; a session's own budget may expire earlier.
`Pool.Query` retains the existing complete, exact buffered result contract.

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
incomplete and retires the physical session. Close after successful EOF returns
nil. Pool Close wakes queued callers, cancels active streams and joins owned
session cleanup. Retiring unusable sessions reduces pool capacity; exhaustion
returns ErrUnavailable. There is no automatic SQL replay, reconnection or recovery
policy. Callers decide whether and when to construct a new admitted pool.

The original Session.Open/Query options and exact result behavior remain stable.
These APIs do not add Exec, transactions or PL/SQL output-bind support. Native
driver/version/platform qualification remains the consuming application's work.

The `oraclenet` package resolves bounded `tnsnames.ora` aliases and wallet paths without a driver. It preserves connection-compatible alias precedence and descriptor text, handles `MY_WALLET_DIRECTORY`, `sqlnet.ora` file wallets and native environment expansion, and never reads wallet contents. Callers decide when a selected TCPS endpoint needs client credentials; ordinary TCP endpoints must not inherit a wallet. `ResolveDescriptorWallet` considers explicit descriptor settings only, without automatic file discovery. Application diagnostics and TLS negotiation remain caller-owned.
