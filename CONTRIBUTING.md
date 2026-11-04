# Contributing

Use Go 1.27.3 and keep changes focused. Public library behavior must be independent of any particular consuming application. Read [code management](docs/code-management.md) and [architecture](docs/architecture.md).

## Code comments

Every exported type, function, method, variable and constant group needs a useful Go doc comment. Start with the symbol name and explain its contract. Include relevant zero-value behavior, limits, ownership, mutation, concurrency, error identity and partial-success semantics. Do not claim durability, synchronization or security guarantees that the implementation does not provide.

Place implementation comments beside the decision they explain. State the concrete problem, adopted solution and non-obvious compatibility constraint. For example, a comment beside a filesystem operation should explain why its flags preserve the no-overwrite contract, rather than simply repeating the operation's name. Link a public issue or regression check when it materially explains the behavior.

Update comments when behavior changes. Avoid obvious restatements, speculative claims, originating/consuming application names, private endpoints, machine paths, secrets and transient debugging notes. Keep package examples generic and runnable. Preserve legally required license/copyright notices.

## Checks and integration

On Windows, run `./scripts/validate.ps1 -Race` with an existing supported C compiler. On Linux/macOS, run `bash scripts/validate.sh --race`. The scripts require configured project storage, keep caches/output out of source and do not publish. Commit go.mod/go.sum, tests, documentation and the changelog together. Implementation PRs belong on the configured source forge; public-copy branches must not diverge.
