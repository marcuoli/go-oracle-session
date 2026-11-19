# go-ora compatibility and upstream fixes

This guide explains two different connection failures, the boundary owned by
this SDK, and how a consuming application qualifies an official driver fix.
The required connection path remains pure Go, without Oracle Client installation
or shared libraries. Concrete drivers stay at caller and integration-test
boundaries; the SDK does not select a different driver after a failure.

The target remains Oracle Database 9.2 through 26ai and subsequent releases.
That target is not evidence that every driver/server combination works. Apply
the [Oracle compatibility contract](https://github.com/marcuoli/go-oracle-session/blob/main/docs/oracle-compatibility.md), retain exact
numeric versions and patch levels, and keep unexecuted combinations unqualified.

## Two failures with different remedies

| Failure | Trigger and owning layer | What the SDK can provide |
| --- | --- | --- |
| Repeated-connection EOF, [upstream #732](https://github.com/sijms/go-ora/issues/732) | Cached fast-login authentication state can fail on later physical admissions. A first successful connection does not cover later or concurrent connections. | Opt-in `goora.PrepareURL` selects the driver's supported `FAST LOGIN=FALSE` setting before connector creation. Each exact driver/server configuration still needs qualification. |
| Short ACCEPT panic, [upstream #759](https://github.com/sijms/go-ora/issues/759) and [PR #737](https://github.com/sijms/go-ora/pull/737) | The official v3.0.1 private network parser reads an optional four-byte field starting at offset 41 after checking only the 32-byte minimum. A packet shorter than 45 bytes cannot supply that field. | Describe and track the upstream defect. A driver parser bounds check belongs upstream; URL preparation cannot supply missing packet bytes or bypass an unconditional read. |

These are packet/authentication conditions, not reliable database-version
predicates. An issue title mentioning an older server does not establish that
every release below a marketing version fails, or that newer servers are immune.
Do not reject a server solely by its version to hide either failure.

### Connection ownership and call path

The caller supplies an official `driver.Connector`. A caller's admission
factory, or `session.Open` through `database/sql`, invokes that connector.
The driver establishes the physical Oracle Net connection; its packet dispatch
handles ACCEPT and calls the private `network.newAcceptPacketFromData`.
Authentication and endpoint/container/member admission must complete before
the application treats the new session as ready.

The [tagged packet dispatcher](https://github.com/sijms/go-ora/blob/025c51529284177330cde75c2e60c137c5f30fe1/v3/network/session.go)
and [tagged ACCEPT parser](https://github.com/sijms/go-ora/blob/025c51529284177330cde75c2e60c137c5f30fe1/v3/network/accept_packet.go)
show that the optional field is decoded unconditionally in v3.0.1. The public
fast-login option does not guard that read. Consequently, full login may address
the repeated-admission EOF while the same driver can still panic on a short
ACCEPT packet.

The upstream parser is private to the driver and owns the wire layout. Moving
its implementation into this SDK would not replace the function executed by
the official connector. Forks, patched module caches, local replacements,
vendored edits, overlays and build-time rewriting are not new SDK remedies.
Do not copy a consumer's retained driver patch into the SDK or another app.

A `recover` boundary can only report a failed operation; it cannot finish a
half-decoded handshake or establish safe reuse. It must not turn the panic into
success, retain the uncertain connection, or retry authentication/SQL.
Likewise, retry loops, socket wrappers and packet rewriting do not repair the
parser and can change socket capabilities, TLS/OOB behavior, ownership or
cancellation. Preserve the existing disposal and joined-cleanup contract.
There is no automatic godror/OCI, alternate-driver, authentication or SQL fallback.

## Supported full-login helper

The optional import is
`github.com/marcuoli/go-oracle-session/goora`. Its
`PrepareURL(*url.URL) error` is available from SDK v0.11.0. Call it on a
caller-owned URL before constructing the official connector, including each
pool factory's connector configuration. Prepare before concurrent use or keep
a separately owned URL per factory invocation.

The helper:

- Replaces all decoded option names recognized by the driver's uppercase
  normalization as FAST LOGIN with one canonical FALSE value.
- Preserves unrelated option values and duplicates, credentials and all
  non-query URL fields. Standard `url.Values.Encode` canonicalizes query
  order and escaping; raw query bytes are not preserved.
- Returns the static `goora.ErrInvalidURL` for nil input or a malformed query,
  without partially mutating the URL or putting URL data in the error.
- Imports no concrete driver, opens no connection and wraps no driver
  interfaces. Endpoint validation, other options, prefetch/LOB policy and
  initialization SQL remain caller-owned.

This credential-free example only prepares an in-memory URL:

```go
package main

import (
	"fmt"
	"net/url"

	"github.com/marcuoli/go-oracle-session/goora"
)

func main() {
	u := &url.URL{
		Scheme:   "oracle",
		Host:     "db.example.invalid:1521",
		Path:     "/service",
		RawQuery: "fast+login=TRUE",
	}
	if err := goora.PrepareURL(u); err != nil {
		fmt.Println("invalid connection query")
		return
	}
	fmt.Println(u.Query().Get("FAST LOGIN"))
}
```

Output: `FALSE`. The example performs no authentication, network access or SQL.
It is not a reproduction of either driver failure. Disabling fast login does
not fix the short-ACCEPT parser or qualify native cancellation reuse.

## Dated upstream evidence

The following snapshot was checked on **2026-11-19**. Recheck it before each
driver adoption/build review; a closed issue is not a published or qualified fix.

| Source | Observed state |
| --- | --- |
| [Official latest release](https://github.com/sijms/go-ora/releases/latest), [tags API](https://api.github.com/repos/sijms/go-ora/tags?per_page=100) and [v3 module proxy latest](https://proxy.golang.org/github.com/sijms/go-ora/v3/@latest) | All selected/reported v3.0.1 as the current v3 release, commit `025c51529284177330cde75c2e60c137c5f30fe1`, published 2026-07-24. The inspected tagged parser still has the unconditional read. |
| [PR #737](https://github.com/sijms/go-ora/pull/737) and [merge commit](https://github.com/sijms/go-ora/commit/e7c904f3ad15c8a4dad21d1926954b44c8420dc8) | Merged on 2026-08-05 at `e7c904f3ad15c8a4dad21d1926954b44c8420dc8`. It gates this optional field on at least 45 bytes and adds a synthetic 32-byte short-packet regression. That fix is absent from the checked v3.0.1 tag. |
| [Issue #759](https://github.com/sijms/go-ora/issues/759) | Open; short-ACCEPT panic report. Its status is separate from the merged PR. |
| [Issue #732](https://github.com/sijms/go-ora/issues/732) | Closed; repeated-admission EOF report. The public full-login setting is distinct from a parser fix and still requires consumer qualification. |

The bounds change addresses one optional field. It is not a claim that all
malformed packets, offsets, server releases or connection paths are qualified.
For future driver-owner decoder coverage, consider 32, 40, 41, 44, 45 and
61-byte forms, malformed declared lengths/offsets and optional-field values.
These are recommended boundary fixtures, not tests executed by this skill or
SDK. The inspected upstream PR adds a 32-byte fixture; wider executed coverage
and an incident's actual packet shape require separate evidence. Keep decoder
regressions with the driver owner instead of copying a parser here to self-test.
Inspect the actual containing release and its regressions when it is published;
merging a commit into the upstream default branch is not release adoption.

## Adopting a compatible official release

1. Resolve a compatible official released driver containing the relevant fix.
   Verify the tag's commit and the changed parser/tests, not just the release
   title. Until one is available, retain the last compatible official version
   and document the exact blocker/exposure. Do not introduce a new local patch,
   replacement or unreviewed default-branch/pseudo-version workaround.
2. Update the consuming application's selected driver graph. This SDK's
   driver-neutral module and URL helper do not force a caller-owned concrete
   driver version. An SDK update alone therefore does not select a fixed driver.
3. Validate the application with `GOWORK=off` and no local replacement for the
   official-driver profile. Record actual compiler, build tags, target OS/arch,
   CGO/build flags, selected module versions/sums and source commits. Keep a
   separately approved retained-exception profile explicitly distinct.
4. Rebuild each applicable application artifact, inspect its embedded Go build
   metadata, and install it through the owning workflow. Resolve the exact
   executable launched, including PATH/shims, and compare installed bytes/hash
   and metadata with the qualified artifact. An old installed binary does not
   acquire a new dependency from a source checkout or SDK release.
5. Run the three independent evidence stages below. Preserve failed outcomes,
   unsupported cases and native gaps; do not silently switch driver/auth modes,
   endpoints, credentials, roles or SQL to make qualification pass.

With process-scoped `GOWORK=off` and approved external output/cache locations,
use the selected driver import path when collecting evidence, for example:

```text
go env -json GOVERSION GOWORK GOOS GOARCH CGO_ENABLED GOFLAGS
go list -m -json github.com/sijms/go-ora/v3
go mod graph
go mod verify
go version -m <rebuilt-artifact>
go version -m <installed-artifact>
```

Check the module JSON's `Replace` field and the full graph; retain hashes and
exact dependency origins. Keep private module names, source paths and build
metadata in private evidence when necessary. Do not paste an unsanitized graph,
binary path or connection configuration into public reports.

An existing, separately approved retained driver exception remains owned by
its consuming project. Preserve its source, replacements, adapters and tests
until the owner's full comparative criteria and any required explicit user
confirmation permit retirement. Its approval does not authorize a new exception
elsewhere, and passing its tests does not qualify the official driver.

## Three independent qualification stages

| Stage | Evidence needed | What it does not establish |
| --- | --- | --- |
| Parser regression | Exact upstream source/release plus short and extended packet fixtures and expected fields/errors. Distinguish source inspection from executed tests. Record whether truncated/invalid-offset cases were actually exercised. | Native handshake, authentication, endpoint identity or database operations. |
| Native handshake/admission | Exact official driver and server patch, platform, account/role, TCP/TCPS and Net settings; joined connect/disposal behavior and actual physical identity. Keep credentials private. | Correct rows/binds/LOBs, transactions, cancellation settlement or future connection reuse. |
| Runtime/application | Existing consumer regressions for exact values/NULLs, prepared operations, buffers/streams, metadata, binds/LOBs, transactions, warnings and query/parent/stage cancellation with joined cleanup and qualified reuse/retirement. | Untested releases, roles, members, patches or application features. |

This documentation change performs no native database qualification. A parser
test, a successful generic SELECT, a returned Ping or a timeout that returns
within budget cannot substitute for later stages. Keep the original adapter and
its single cancellation authority until equivalent end-to-end behavior and its
retirement criteria are demonstrated.

### Repeated and concurrent physical admissions

A pool's configured size or repeated queries on one lease do not prove multiple
physical handshakes. Exercise a cold first connection, additional sequential
physical connections, close/reopen phases and concurrent factory admissions.
Hold distinct leases simultaneously where necessary, and record per-factory
connect counts/generations plus actual connection identities using the caller's
version-selected admission logic. Assert expected pool/worker capacity without
mistaking a logical checkout for a new physical connection.

Apply the same supported URL policy at every relevant connector creation.
Recheck endpoint/database/container and required instance/member identity after
each fresh admission. Do not count a new physical session as reuse of the old
one. Replacement for a later independent command is caller policy; neither this
helper nor the SDK automatically replays a failed command or reconnects it.

### Per-combination release matrix

Maintain a row for every actual driver/server/patch/platform/configuration
combination, splitting rows further by login/role, OPEN/MOUNT, non-CDB/root/PDB
and single-instance/RAC/member scope where applicable. A SYS result does not
qualify another login. Mark inapplicable combinations explicitly; never require
modern container metadata on a legacy server.

The template below lists the required families. **NR** means not run by this
documentation change, not a statement that no other project has evidence.
Replace NR only with attributable evidence for the exact combination.

| Oracle family | Numeric release / patch or RU | Parser regression | Native admission | Runtime/application |
| --- | --- | --- | --- | --- |
| 9iR2 | Record observed 9.2.x / patch | NR | NR | NR |
| 10gR1 | Record observed 10.1.x / patch | NR | NR | NR |
| 10gR2 | Record observed 10.2.x / patch | NR | NR | NR |
| 11gR1 | Record observed 11.1.x / patch | NR | NR | NR |
| 11gR2 | Record observed 11.2.x / patch | NR | NR | NR |
| 12cR1 | Record observed 12.1.x / patch | NR | NR | NR |
| 12cR2 | Record observed 12.2.x / RU | NR | NR | NR |
| 18c | Record observed version / RU | NR | NR | NR |
| 19c | Record observed version / RU | NR | NR | NR |
| 21c | Record observed version / RU | NR | NR | NR |
| 23c / 23ai | Record observed version / RU | NR | NR | NR |
| 26ai | Record observed numeric version / RU; do not infer major 26 | NR | NR | NR |
| Subsequent releases | Record observed version and capabilities | NR | NR | NR |

Attach source/driver/SDK revisions, platform/architecture, configuration, evidence
location/date and remaining gaps to each row. The short-parser defect in the
checked official driver remains a handshake blocker for affected packet forms;
a full-login success on another form or server does not remove that gap.
Unknown newer versions keep the established path when capabilities permit;
neither branding nor one minimum-version check proves every field/feature.

For each caller-owned SQL projection, independently record documentation/MOS
applicability, selected statement/columns or unsupported-without-execution,
static selection/decoding checks, native SQL execution and driver/application
qualification. A field can disappear and return. Preserve exact values, NULLs,
diagnostics and empty versus unsupported/unavailable/failed states. This guide
adds no SQL and supplies no missing native matrix evidence.

## Credential-safe failure evidence

Preserve the original failure kind, panic message and relevant stack frames,
driver/parser location, packet length if safely known, operation/phase and an
external elapsed time before converting the failure to a generic application
message. Add driver/SDK/source revisions, build metadata and physical connection
generation/identity. Record what was observed versus inferred.

Redact credentials, URL userinfo and secret query parameters, private endpoints,
usernames, wallet material, local paths and private provenance before exporting
evidence. Do not dump authentication packets or intercept global driver output.
A bounded original-error record should retain the fault without exposing its
payload. A parser panic is not proof of an invalid password, server version,
successful authentication or a usable connection.

Keep the failed operation failed and join owned cleanup. No automatic SQL replay,
alternate driver/authentication attempt, silent empty success or reuse of an
uncertain connection is introduced by this guide or the URL helper.
