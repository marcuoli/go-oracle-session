---
name: go-oracle-session
description: Review go-oracle-session consumer connection policy, opt-in shared pure-Go v2/v3 compatibility and go-ora migrations. Use for Oracle admission failures, full-login policy, short ACCEPT parsing and exact driver/cancellation qualification; not general SQL tuning.
---

# Oracle connection compatibility

Inspect the caller's actual connector and installed executable. Existing SDK
interfaces remain driver-neutral; the additive goora/compat package owns an
explicitly authorized retained pure-Go v2/v3 profile in the SDK module.
Importing a module does not install this Codex skill.

Read the relevant [compatibility reference](references/go-ora-compatibility.md)
and the [shared connector contract](https://github.com/marcuoli/go-oracle-session/blob/main/docs/shared-driver-compatibility.md).

## Identify the failing boundary

Record exact SDK/driver/source versions, module origins/sums, replacements,
GOWORK, executable build metadata, failure phase and observed numeric server
version/patch. Keep credential-safe failures and distinguish source/synthetic,
native handshake, runtime and installed-application evidence.

- Repeated physical-connection EOF: official connectors can opt into
  goora.PrepareURL. Shared compat prepares each selected backend itself:
  V2 removes FAST LOGIN; V3 enforces FALSE. Full login is not a parser fix.
- Short ACCEPT: official v3.0.1 and its unreleased upstream fix remain distinct
  from the SDK retained decoder and its executed boundary regressions.
  Do not patch caches, rewrite sockets/packets or create another driver copy.
- Shared compatibility: NewConnector defaults to V2; explicit V3 selects only
  modern. Negotiated is opt-in and can switch only on the clean pre-authentication
  capability signal after cleanup. Authentication/transport/panic failures never
  select another backend; no SQL is replayed.
- Migration or old executable: verify the official SDK module with GOWORK=off,
  no local replacement, then rebuild/install and inspect exact artifact identity.
  Existing caller-owned official connectors do not change merely from an SDK bump.

Refresh official releases, tag/proxy and relevant bug/fix evidence before driver
adoption/build review. Distinguish no fix, merged unreleased, published and
consumer-qualified fixes; dated snapshots and closed issues are insufficient.

## Preserve ownership and qualification

Keep pure Go without Oracle Client/OCI or automatic authentication/SQL fallback.
Caller policy owns credentials, administrative-role translation, SQL, endpoint/
container/member admission, recovery, progress and warning redaction.

Preserve one cancellation authority and joined disposal. A native break or
settlement acknowledgement alone does not prove reusable protocol/transactions.
Lazy modern CLOB Read replaces its native watcher with one joined terminal
read scope; canceled/failed read or Free retires physical health. Keep the lease
and statement/cursor until Read/Value/Free finish. V2 materializes output;
whole-value bounded memory is not promised. Failed transaction completion and
post-dispatch cursor cleanup cannot return uncertain state for replay/reuse.

The SDK retained source is the specific issue46 exception. It does not authorize
new copies or retirement of a consumer's separately retained implementations.
Preserve those sources/adapters/tests and any explicit retirement approval.
Do not add another design/publication approval loop when current task authority
already covers ordinary delivery.

Record per-configuration evidence across 9.2,10.1/10.2,11.1/11.2,12.1/12.2,
18,19,21,23c/23ai,26ai and later releases. No numeric ceiling. Native bounded
12.2 V2 and23.26.1 V2/V3 results do not qualify other patches, roles, states,
members, features or active cancellation. Keep NULL, empty, unsupported,
unavailable and failed outcomes distinct.

Use only Oracle-published/certified technical sources. Read applicable reference
content and record title, URL, date and version/state/role preconditions.
Keep private paths, endpoints, identities, credentials and provenance out of
shared instructions. This skill does not expand task, lab or consumer permissions.
