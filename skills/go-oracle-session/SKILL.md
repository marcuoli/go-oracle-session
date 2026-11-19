---
name: go-oracle-session
description: Review go-oracle-session consumer connection policy and go-ora driver compatibility in Go applications. Use for initial or repeated Oracle admission failures, short ACCEPT panics, supported full-login policy and driver migration qualification; not generic SQL tuning or inventory-only CSV edits.
---

# Oracle connection compatibility

Work on the caller's actual connector and installed application. The SDK owns
session/resource contracts and supported URL policy; the caller selects the
concrete driver, SQL and endpoint admission. Importing the Go module does not
install or load this Codex skill.

## Identify the failing boundary

Establish the selected SDK/driver versions and origins, replacements/workspace,
actual executable/build metadata, failure phase and observed numeric server
version/patch. Preserve the original credential-safe failure and distinguish
source inspection, parser tests, native handshake and runtime evidence.

For go-ora admission failures, read the relevant sections of the
[compatibility reference](references/go-ora-compatibility.md):

- **Repeated physical-connection EOF:** inspect supported full-login policy.
  Reuse `goora.PrepareURL` before connector creation, including pool factories.
  It canonicalizes FAST LOGIN to FALSE; it does not repair packet parsing.
- **Short ACCEPT panic:** follow private parser ownership and upstream fix
  status. Do not move/copy the driver's decoder into the SDK, patch caches or
  rewrite sockets/packets. Panic containment can report failure and retire
  uncertain ownership; it is not a repaired handshake or permission to retry.
- **Driver adoption or an old installed binary:** follow official-release,
  standalone graph and rebuilt/installed artifact verification. An SDK update
  does not select a caller-owned driver or replace an executable.
- **Reuse/migration claims:** apply the separate qualification stages and
  release-family matrix, including fresh sequential/concurrent physical
  admissions and actual endpoint/container/member identity.

Before recommending a driver change, refresh official release, tag/module-proxy
and relevant issue/fix evidence. Distinguish a merged fix, a containing published
release and a fix qualified in this consumer; do not treat the reference's dated
snapshot or a closed issue as current adoption proof.

## Preserve the established contracts

Keep the required pure-Go go-ora path without adding Oracle Client or an
automatic godror/alternate-driver, authentication or SQL fallback. Do not replay
a failed SQL command. Fresh admission for a later independent command remains
caller policy, with its original identity and cleanup requirements.

Preserve the original cancellation authority, exact values and joined
cursor/driver/socket cleanup. A Ping, returned call, typed cancellation
acknowledgement or synthetic parser pass alone does not prove reusable protocol
and transaction state. Keep existing adapters until equivalent end-to-end
qualification and their retirement criteria are met.

Use compatible official upstream releases for new dependency adoption.
Separately approved retained source/replacement exceptions stay with their
owners and tests; never propagate or retire them merely to adopt this SDK.
Follow any required explicit retirement confirmation and existing task
authorization rather than inventing a new approval loop.

Record exact driver/SDK/source/build/server/patch/platform configuration,
evidence and gaps across the required 9.2-through-26ai and later-release target.
Use observed capabilities and numeric versions without an artificial upper
cutoff. Preserve failed, unavailable, unsupported, empty and NULL outcomes;
unexecuted combinations remain unqualified.

For broader API or query-selection contracts, consult the public
[SDK README](https://github.com/marcuoli/go-oracle-session/blob/main/README.md)
and [Oracle compatibility policy](https://github.com/marcuoli/go-oracle-session/blob/main/docs/oracle-compatibility.md).
Keep private incident paths, endpoints, identities, credentials and provenance
out of shared instructions and public diagnostics. This skill does not expand
the task's implementation, migration, publication or lab permissions.
