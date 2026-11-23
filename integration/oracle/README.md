# Native Oracle qualification

This opt-in test module exercises released go-oracle-session with official
upstream drivers. It keeps concrete driver dependencies out of the main library.
Use Go 1.27.4, GOWORK=off and project-owned external scratch/cache/output paths.
Do not add local replacements to this module or treat a skipped native test as
qualification. The source repository tracks exact native evidence and outstanding qualification separately.

## Build and offline checks

From the repository root, the existing validation scripts support offline checks:

```text
./scripts/validate.ps1 -NativeHarness -DriverMajor 2 -Scan
bash scripts/validate.sh --native-harness 3 --scan
```

From this module directory, select one driver target explicitly:

```text
go test -tags integration ./...
go test -tags 'integration goora2' ./...
go test -tags integration -c -trimpath -buildvcs=false -o <external-output>/oracle-native-goora3.test .
go test -tags 'integration goora2' -c -trimpath -buildvcs=false -o <external-output>/oracle-native-goora2.test .
```

The native test skips unless ORACLE_TEST_CONFIG_STDIN=1. The local error-classifier
regression still runs. Native target selection is fixed at build time: upstream
v2 and v3 both register the database/sql name `oracle` and must not be linked
into the same test binary. No authentication or SQL retry selects another driver.

## Dedicated fixture and private inputs

The base suite writes to a dedicated disposable schema's NATIVE_TX table. The
settled profile also commits NATIVE_EVENT dispatch markers as described below.
Never supply an application account. Before running it, the operator must create
and own the fixture, record its initial state and approve its complete teardown:

- A fresh uniquely named account with CREATE SESSION, explicit SELECT on
  SYS.V_$INSTANCE and EXECUTE on SYS.DBMS_LOCK.
- NATIVE_TX with ID NUMBER PRIMARY KEY and PAYLOAD VARCHAR2(100), initially empty,
  with sufficient quota in a dedicated permitted tablespace.
- NATIVE_FIXTURE with TOKEN VARCHAR2(32) NOT NULL and exactly one unpredictable
  per-run marker value. Record that marker in the private test input.

The test does not provision users, change privileges or remove schema objects.
Each fresh physical session verifies the exact expected user, per-run marker,
base numeric version and optional full release before its test operation.
The two modes use ID 1 and ID 2 and deliberately verify rollback and commit.
Keep evidence before a rerun; reset only those owned synthetic rows under the
fixture lifecycle, or use a new fixture. Never clear another account's data.

Feed one JSON object through a protected credential provider to the binary's
stdin, then close stdin. The configuration fields are:

| Field | Meaning |
| --- | --- |
| DSN | Private driver connection string, including original credential bytes and reviewed connection options. |
| Driver | Exactly `go-ora/v3` or `go-ora/v2`, matching the built target. |
| ExpectedVersion | Expected numeric server-version prefix, from independently verified identity. |
| ExpectedFullVersion | Exact full numeric release when available; empty for servers without VERSION_FULL. Marketing labels do not replace numeric identity. |
| FixtureUser | Exact disposable schema username. |
| FixtureToken | Unique marker stored in NATIVE_FIXTURE. |
| AllowFixtureWrites | Must explicitly be true for this suite. |

Enable ORACLE_TEST_CONFIG_STDIN=1 only for that process. Run the binary with
`-test.v -test.timeout=90s -test.run=TestNativeSession` through a bounded execution
channel. Do not put credentials in command arguments, source, shell history or
logs. Errors expose only fixed operation names, types, Oracle codes and terminal
condition flags; external driver diagnostics must also be reviewed for secrets.

The suite covers direct/prepared queries, numeric text and SQL NULL, untyped NULL
framing, multibyte OUT capacity, repeated named/OUT binds, streaming EOF,
transaction visibility across independent sessions, early retirement and client deadline retirement.
An intentionally disposed socket can produce a later driver-logoff close error;
only an exclusively already-closed-socket chain is accepted in those retirement
cases. Other or joined unexpected cleanup errors remain failures. Client return
and retirement do not establish server-side dispatch or query termination.

## Recorded limits

The initial boundary checks used Linux/amd64, Go 1.27.4 and released Session v0.5.0:

- Official go-ora/v3 v3.0.1 panicked on a short Oracle 9.2.0.8 ACCEPT packet;
  official v2 v2.9.0 rejected the valid dedicated fixture login. Neither result
  qualifies upstream-only Oracle 9i support.
- On Oracle 26ai 23.26.3.0.0, upstream v3 with its public FAST LOGIN=DISABLE option
  passed the original core cases, but the additional untyped-NULL and multibyte
  character-capacity regressions failed. Preserve the complete failure evidence;
  do not weaken these tests or call the whole native suite passed.
- A separate non-shipping consumer-reference build using its existing approved
  retained driver configuration passed both boundary versions, including those
  value regressions. That configuration is not added to or shipped by this test
  module and is not official-upstream qualification.

Native pool/LOB and server-side termination coverage, other exact patches/platforms
and the complete consumer adapter path remain separate acceptance work. The
cancellation profiles below add specific proof-dependent transition evidence. Keep existing consumer adapters and their
retirement/approval gates intact. Recheck official upstream releases at the next
dependency review; no shared fork, cache patch or protocol rewrite is authorized.
## Native cancellation profiles

The default integration targets include native continuation against released
go-oracle-session v0.19.0. Use GOWORK=off without a first-party replacement.
For an unreleased first-party candidate, use a separate external modfile; do not
change application manifests or introduce a third-party replacement.

Set `SettledCancellation=true` and an explicit `CancellationOutcome` in private
stdin. `reuse` requires same-session continuation, `retire` requires retirement
and a separately admitted session for independent SQL, and `qualified` requires
each transition to match recorded native settlement proof. The last mode cannot
pass a retired session when native proof was affirmative or a reused session when
proof was missing. Unknown joined errors fail every mode. Default retirement
checks remain separate and the original SQL is never retried.

Create the additional NATIVE_EVENT table and NATIVE_WAIT function from
`fixture-settled.sql` in the exact disposable schema. The function commits an
autonomous dispatch marker before a finite three-second wait. An independently
admitted observer must see the operation's unique marker. The test then checks
cancellation causes, native acknowledgement/settlement and physical-session
identity before any independent continuation. These fixture writes require the
existing explicit write opt-in and must be included in teardown. They establish
server dispatch, not universal server-side termination or crash durability.

The modern public session/validation bridge and legacy retained capability stay
in these test adapters. Actual connection options such as FAST LOGIN=DISABLE or
ENABLE_OOB must be recorded per profile; do not infer Windows or forwarded-port
qualification from Linux direct-TCP results. Existing upstream value/connection
failures remain visible and do not require replacing an approved working consumer
configuration before this shared contract can be used.