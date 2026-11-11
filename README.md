# go-oracle-session

Reusable single-session query ownership, cancellation evidence, direct socket tracking and strict credential-bearing connection inventory. Use the functional imports `session`, `cancel`, `transport` and `inventory`.

Applications provide an official `driver.Connector` implementing `driver.QueryerContext`, reviewed catalog SQL, stable command references, exact value codecs and endpoint/acceptance policy. No concrete database driver is forced on consumers. Queries use one leased connection and are never replayed by this module. A driver declining context queries is rejected; prepare fallback is intentionally unavailable.

Numeric catalog expressions should produce explicit text, for example `TO_CHAR` with a stable numeric convention. Default values preserve text, integers and SQL NULL; floats are rejected. A result is returned only after the cursor, terminal error, cleanup cause and cancellation watcher have joined. No retained prefix counts as a complete result.

Configure positive connect, query and cleanup budgets plus a required idempotent physical-disposal hook. The hook must unblock and join all owned connector/driver I/O. Cleanup includes admission and may outlive the parent cancellation to release owned resources. Query admission honors its own context without cancelling another active query. These contracts cannot provide a hard deadline for a driver, codec or hook that does not return as required. Actual database/version/platform qualification belongs to consumers.

The inventory parser preserves password bytes; do not serialize entries or log underlying error chains. Default diagnostic formatting exposes safe operation codes and CSV source-line facts. This is not a general-purpose SQL safety engine, credential store, pool, recovery manager or database certification.

Go 1.27.3 is required. Run `scripts/validate.ps1 -Race -Scan` or `scripts/validate.sh --race --scan` with configured external development storage. `examples/inventory` demonstrates synthetic parsing without contacting a database. See [architecture](docs/architecture.md), [comments](CONTRIBUTING.md) and [delivery](docs/code-management.md).

License: UPL 1.0.
The `oraclenet` package resolves bounded `tnsnames.ora` aliases and wallet paths without a driver. It preserves connection-compatible alias precedence and descriptor text, handles `MY_WALLET_DIRECTORY`, `sqlnet.ora` file wallets and native environment expansion, and never reads wallet contents. Callers decide when a selected TCPS endpoint needs client credentials; ordinary TCP endpoints must not inherit a wallet. `ResolveDescriptorWallet` considers explicit descriptor settings only, without automatic file discovery. Application diagnostics and TLS negotiation remain caller-owned.

## Optional configured-memory facts

The memory package selects fixed connected-PDB SGA/PGA queries and validates exact GiB text with source/current container IDs, instance identity, parameter name and availability. Inheritance must be explicitly admitted. Effective-value mode is only for Oracle 18+ after a primary NULL/no-row result; invalid evidence and access failures are not retry triggers. The package performs no I/O or RAC aggregation. Session/transport APIs remain catalog-free. See examples/memory for a runnable query-selection example. Source tests are prepared; native compatibility and reader-specific use require separate qualification.

This unqualified prototype is preserved for contract transfer to the dedicated go-oracle-info fact library. It is not an accepted production package and must not be released from this repository. Session and transport ownership remain unchanged. Resume only after the owning contract and storage constraints are resolved.
