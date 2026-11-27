# Shared pure-Go driver compatibility

The optional import github.com/marcuoli/go-oracle-session/goora/compat packages
both retained go-ora implementations in one immutable module. It requires no
Oracle Client installation, CGO or Oracle shared library. Existing SDK callers
continue supplying their own connector until they explicitly adopt this package.

The retained baselines are official go-ora/v2 v2.9.0 and go-ora/v3 v3.0.1, plus
the compatibility changes recorded in internal/compatdriver/sources.json.
This is an explicitly authorized retained-source profile (#46), distinct from
an unmodified official driver. Both MIT licenses ship with the SDK. Keep those
notices when distributing an executable that links this package.

## Selection and admission

NewConnector(dsn string, options Options) returns a concurrent driver.Connector.
Options contains Mode, an optional Dialer and a connection-local
LoginWarningHandler func(string) bool. Callbacks must support concurrent
physical admissions; warning text can contain private data and needs caller
redaction. Each Connect creates fresh native configuration/registries and
receives its original context.

| Mode | Behavior |
| --- | --- |
| V2, the zero/default | Select the legacy-compatible retained implementation directly. |
| V3 | Select the modern retained implementation directly. |
| Negotiated | Start V3. Select V2 only when the handshake confirms legacy authentication is required and cleanup succeeds. |
| Automatic | Start V2. Select V3 only when Oracle returns ORA-12269 in the initial native encryption negotiation and cleanup succeeds. |

Automatic and Negotiated are opt-in. Use the mode whose starting backend fits
the deployment. The compatibility decision uses a specific protocol signal
before authentication and permits at most one alternate attempt on the same
context and configured connection profile. Connections using external Oracle
authentication, wallets or client TLS certificates stay on the initial driver.
Authentication, EOF, cancellation, cleanup failures, and panics are terminal.
SQL is never replayed. A change to the zero-value default requires native
qualification across the release matrix, strict encryption and the affected
encrypted Oracle 12.1 profile.

[Oracle ORA-12269](https://docs.oracle.com/en/error-help/db/ora-12269/) identifies
a rejected client encryption or integrity adapter version. The server's strict
policy still applies to the alternate driver. V3 must independently negotiate
and pass admission. Check both backend attempts if admission still fails.

V2 removes only FAST LOGIN options that its parser cannot accept. V3 selects
FAST LOGIN=FALSE through the existing goora.PrepareURL policy. Other decoded URL
values, endpoint and credential bytes remain caller-owned. Native administrative
role options must be supplied explicitly; a migration string such as a username
followed by an administrative role is not a literal URL username.

~~~go
connector, err := compat.NewConnector(dsn, compat.Options{})
if err != nil {
    return err
}
db := sql.OpenDB(connector)
defer db.Close()
~~~

The example assumes caller-owned dsn and imports database/sql and
github.com/marcuoli/go-oracle-session/goora/compat. It does not perform endpoint
admission or select SQL. DriverName registers the same default policy as
"go-oracle-session". Existing official "oracle" registration remains independent.
IsDriver identifies the shared factory; ConnectionMode identifies a raw
driver.Conn. A database/sql leased connection exposes CompatibilityMode through
Raw without exposing a private native cursor.

## Custom TLS configuration

NewConnectorWithTLSConfig accepts a caller-maintained TLS configuration for a
DSN that selects TCPS. It keeps the same backend selection and connection
ownership as NewConnector. The supplied configuration takes precedence over
SSL VERIFY and wallet-derived TLS trust and client certificates. The driver
sets the TLS server name to the
addressed host. Each physical connection receives a separate config clone so
that concurrent admissions do not share that driver mutation. The caller
owns the trust roots, certificates and callback policy.

## Configure and dial observations

Use `compat.InspectV2Config` to observe connection timeout, ordered endpoints,
service, SID, instance, administrative mode, TLS and wallet presence. It uses
the retained V2 parser, including wallet reads, and returns the original error
for caller-owned redaction. A successful snapshot carries no credentials,
wallet contents or full connection descriptor.

Use `transport.DialContext` when the caller already owns its sockets. It uses
the supplied dialer and returns the original socket and error from one dial.
TCP uses the existing go-hostdiscovery classification. Other networks report
the actual addresses without assigning a TCP status. The caller retains
logging, cancellation and disposal.

## Handshake observations

ConnectWithDiagnostics runs the same connector selection as Connect and returns
ordered backend attempts for that call. Each attempt records its backend and
elapsed time. V3 can additionally report the last observed handshake phase,
negotiated protocol and security values and authentication state. V2 has no
native handshake snapshot. The operation preserves the normal connection and
error contract and does not enable protocol tracing.

Callers can record the snapshot using their existing connector wrapper.
The separate diagnostic.Connection API classifies and redacts error causes.
Endpoint identity, credentials, deadlines and logging policy remain with the
caller. Observations do not authorize switching backends or retrying SQL.

Use ConnectObserved when recording an outcome must complete before another
backend can start or a connection can escape. An optional Before callback can
reject admission without dialing. An optional After callback receives the
attempt and original backend error before fallback or return. A callback
failure stops selection, closes any connection, and preserves cleanup errors.
Do not persist the original error without caller-owned redaction. The observer
does not change the V2 default, context, or permitted reasons for fallback.

## SQL-free native probe

PingProtocol checks a driver connection exclusively owned by the caller. It
returns ErrUnsupported when a SQL-free probe is unavailable, including retained
V2 servers below 10.1.0.2. It does not execute SQL or replace the connection.
Discard the connection if the dispatched probe fails or is canceled. The
caller still owns transaction and cancellation-reuse qualification.

See the [consumer connection wiring](../skills/go-oracle-session/references/consumer-connection.md)
for the common connector, diagnostics and session setup.

## Values, statements and resource ownership

Out{Dest, Size, In} preserves destination, character capacity and IN/OUT intent.
Both connection and contextual statement checkers handle it and standard
sql.Out. NewNumber accepts exact signed decimal text with an optional point and
base-ten exponent: at most 1024 input bytes, 38 significant digits and nonzero
magnitudes from 1e-130 to below1e126. Malformed or inexact input returns
ErrInvalidNumber without input text. Cross-backend conversion uses decimal text,
never floating point. OracleCode extracts typed native codes without authorizing
retry.

V2 creates a fresh cursor before each later independent prepared execution.
It never repeats a failed operation. A failed transaction completion retires
physical health, including automatic rollback whose lifetime context has expired.
Post-dispatch and cursor-cleanup failures preserve terminal causes rather than
requesting database/sql replay. DATA FINAL is a terminal protocol cause.

Clob is not safe for concurrent use or copying after use. Keep its physical
connection and statement/cursor leased through Read, Value and Free; consume row
values before advancing. V2 materializes output and its statement owns native
locator cleanup. V3 may retain a lazy locator. Neither promises bounded
whole-value memory usage.

SetValue accepts nil, text or bytes. Free an existing bound value before
replacing/rebinding it. Value distinguishes SQL NULL from allocated empty CLOBs.
Unread/failed lazy values expose no partial prefix. A failed Read is sticky;
Read/Free failure invalidates its physical owner without replay or another
protocol/logoff exchange.

A lazy V3 Read uses one read-specific cancellation authority, replacing the
native unjoined break watcher for that read. Cancellation closes captured
transport outside the protocol mutex, shares close-once ownership with external
disposal, and joins its callback before returning. The canceled connection is
retired; this is not native cancellation-reuse or remote SQL-termination proof.

Other operations preserve the caller's existing command/cancellation owner.
The native break and settlement capabilities can be used by a qualified
session.Cancellation adapter; they do not independently establish reusable
protocol or transaction state. Keep caller adapters until equivalent end-to-end
behavior and their existing retirement approvals are satisfied.

## Qualification recorded on 2026-11-19

Windows/amd64, Go1.27.4, CGO_ENABLED=0, no Oracle Client:

| Server observation | Selected backend/login | SDK-native scope |
| --- | --- | --- |
| 9.2.0.8.0 | V2, existing lab account without SYSDBA login; non-CDB | Four simultaneous physical admissions, exact 9007199254740993 native NUMBER literal/bind and decimal text/NULL/Unicode, three repeated mixed OUT operations at 32767-character capacity, 5000-byte scalar input and 125000-byte temporary CLOB: passed. |
| 12.2.0.1.0 | V2, existing SYSDBA lab account in a PDB | Same bounded scalar/bind/OUT/input/CLOB scope: passed. |
| VERSION=23.0.0.0.0, VERSION_FULL=23.26.1.0.0 | V2 and V3, existing SYSTEM lab account without SYSDBA login in a PDB | Same bounded scope: passed for both; VERSION_FULL separately read with both backends. |

The native NUMBER result qualifies the single 9007199254740993 integer literal/bind;
constructor decimal/range boundaries remain synthetic. The tested operations use
session-local/temporary state and no new schema
objects, users or grants. Patch inventory was not queried; 9.2/12.2 patch-set evidence
does not identify its installed PSU. SYSTEM is an administrative lab account,
not qualification for an ordinary customer account. Native active cancellation,
server termination, transactions/DML, MOUNT/root, other non-CDB versions, RAC members, TCPS,
wallets, all types and all application features remain outside these results.

Synthetic regressions separately cover bounded ACCEPT/TTC, selection/cleanup,
NULL/character capacity, exact values, concurrent factories, terminal LOB
failure/disposal, canceled transaction retirement and no replay after cleanup.
Synthetic cancellation/failure evidence is not native cancellation qualification.

| Required family | Native status for this shared package |
| --- | --- |
| 9.2 | Bounded V2 scope above, observed 9.2.0.8.0 only. |
| 10.1 | Not executed; unqualified. |
| 10.2 | Not executed; unqualified. |
| 11.1 | Not executed; unqualified. |
| 11.2 | Not executed; unqualified. |
| 12.1 | Not executed; unqualified. |
| 12.2 | Bounded V2 scope above only. |
| 18c | Not executed; unqualified. |
| 19c | Not executed; unqualified. |
| 21c | Not executed; unqualified. |
| 23c/23ai | No separate earlier release/patch executed. |
| 26ai | Bounded observed 23.26.1.0.0 scope above only. |
| Subsequent releases | No artificial upper cutoff; unexecuted combinations remain unqualified. |

## Native harness query selection

TestNativeCompatibility is skipped unless explicitly enabled with private stdin
configuration. The baseline identity projection uses USER,
V$DATABASE.NAME, V$INSTANCE.VERSION and USERENV.ISDBA; scalar tests use DUAL,
TO_CHAR, a native NUMBER literal/typed bind and explicit NULL. Prepared OUT and temporary-CLOB blocks are fixture
operations, not application policy or a catalog exported by this library.

Container metadata is selected only by the explicit Container harness option.
ExtendedVersion independently selects V$INSTANCE.VERSION_FULL for 18c+ targets.
Neither projection is silently enabled on legacy targets. This harness delegates
target selection to its operator; do not enable an optional projection without
matching documented applicability.

| Projection | Release applicability/static selection | Native SQL/driver qualification |
| --- | --- | --- |
| Baseline identity/scalar/OUT/temporary CLOB | No newer optional columns; legacy target selection still requires per-release documentation and account/state checks. | Exact 9.2/12.2 V2 and 23.26.1 V2/V3 observations above; all other rows unexecuted. |
| USERENV.CON_NAME | Explicit container option; legacy9.2/10/11 cases omit it. | PDB observations above only; root/non-CDB/MOUNT not qualified. |
| V$INSTANCE.VERSION_FULL | Explicit extended option; available from 18c; not executed on 12.2 or older. | Read through V2 and V3 on 23.26.1.0.0; no per-patch inference for 18/19/21 or earlier 23. |

Oracle Database Reference 18c and 26ai V$INSTANCE pages were read on 2026-11-19.
The 18c footnote explicitly introduces VERSION_FULL at18c; both pages describe
current-instance state, not all RAC members. No MOS identifier or broader
patch certification was inferred.

- https://docs.oracle.com/en/database/oracle/oracle-database/18/refrn/V-INSTANCE.html
- https://docs.oracle.com/en/database/oracle/oracle-database/26/refrn/V-INSTANCE.html

### Autonomous Database driver matrix

The opt-in ADB matrix selects two wallet-based URLs and one wallet-backed TNS
alias by their exact names in a caller-owned inventory. Pass the selection
and paths through private stdin JSON. The test reads the existing inventory
and Oracle Net files, runs both bundled drivers for each route, and continues
through failures. Ordinary SDK connections still default to V2.

~~~json
{
  "SourceRevision": "<SDK source revision>",
  "ProofDirectory": "<existing empty directory for this run>",
  "InventoryPath": "<private inventory CSV>",
  "WalletProfiles": ["<first wallet profile name>", "<second wallet profile name>"],
  "TNSProfile": "<TNS profile name>",
  "TNSAdmin": "<directory containing the alias and its wallet configuration>"
}
~~~

Use the source revision of the test binary and a separate proof directory for
each run. Keep the inventory and wallet files in their approved locations.
The harness resolves the selected alias and uses its exact descriptor.

~~~sh
ORACLE_COMPAT_ADB_STDIN=1 go test -count=1 -run '^TestNativeADBMatrix$' -v ./goora/compat < "$private_adb_json"
~~~

Each case holds four physical sessions and checks the selected driver,
normal login, observed database version, Oracle's CLOUD_SERVICE and TCPS
session attributes, exact NUMBER/NULL/Unicode results, repeated OUT binds,
and the complete temporary CLOB. The proof files use fixed case labels.
They record admissions, Oracle failure codes and session facts without
exposing the login, directory, wallet, endpoint or descriptor.
Cases that fail admission fail the test without claiming SQL qualification.

The [Oracle SQL Language Reference](https://docs.oracle.com/en/database/oracle/oracle-database/26/sqlrf/SYS_CONTEXT.html)
documents CLOUD_SERVICE for Oracle cloud deployments and NETWORK_PROTOCOL
for the current connection. These cloud assertions apply only to the ADB
matrix. The legacy and strict native Oracle Net cases retain their own
projections and pass conditions.

### Strict native network encryption

Set `StrictNetworkEncryption: true` in the private stdin JSON to run the
network regression instead of the scalar, OUT and CLOB checks. Use `V3`
(`Mode: 1`) or `Negotiated` (`Mode: 2`) to test an initial V3 admission.
A password-authenticated V3 attempt uses upgraded AES or SHA-2 when the
Oracle Net peer selects a compatible service. The existing service offers
are preserved. Unusable upgraded selections are terminal on that attempt.
Select `Automatic` (`Mode: 3`) to test V2's strict rejection followed by one
retained V3 admission per lease. Omit both fields for the usual V2
compatibility profile. Run each configured fixture with uncached results:

~~~sh
ORACLE_COMPAT_NATIVE_STDIN=1 go test -count=1 -run '^TestNativeCompatibility$' -v ./goora/compat < "$private_fixture_json"
~~~

Use a disposable Oracle Net TCP fixture and record its observed numeric release,
image or patch identity, account and service. Read back the effective server
settings from the fixture. The [upstream #729 profile](https://github.com/sijms/go-ora/issues/729)
sets `SQLNET.ENCRYPTION_SERVER=required`, `SQLNET.CRYPTO_CHECKSUM_SERVER=required`,
and `SQLNET.ALLOW_WEAK_CRYPTO_CLIENTS=FALSE`.
The test holds four concurrent physical leases, pings each and requires both
`AES256 Encryption service adapter` and `SHA256 Crypto-checksumming service adapter`
in Oracle's banners for that lease. Generic service banners are insufficient.
The supplied account needs read access to `V$SESSION_CONNECT_INFO`.

For the network profile set the expected user, database, role, and container
where applicable. Enable `ExtendedVersion` and set `Expected.VersionFull` when the
account has access to `V$INSTANCE.VERSION_FULL`. Otherwise attach the
independently observed fixture release to the result. `Expected.Version` applies
to the baseline profile. The strict receipt records the stage and admission count
on failure, and the verbose test log records the error. Keep a separate run for
the value profile and for each version, account and security configuration.
Preserve failed outcomes and rerun the same profile against upstream fixes.

| Release family | Network projection selection and native evidence |
| --- | --- |
| 9.2, 10.1 | Run the baseline profile. The strict fixture profile is unqualified. Oracle 9.2 documents the banner view but its `USERENV` list lacks `SID`. |
| 10.2, 11.1, 11.2, 12.1, 12.2, 18c, 19c, 21c | Run the baseline profile. Enable the strict profile only where the exact release, patch, account and Oracle Net policy support the settings and the current-session SID projection. Strict execution is unqualified. |
| 23c/23ai, 26ai | The supplied fixture can select both profiles separately. See the strict qualification below for exact native results. |
| Later releases | Select by documented and observed applicability. Each new configuration starts unqualified. |

Oracle defines the own-session `SID` context and the Oracle Net service banners
in the [26ai SQL Language Reference](https://docs.oracle.com/en/database/oracle/oracle-database/26/sqlrf/SYS_CONTEXT.html)
and [26ai Database Reference](https://docs.oracle.com/en/database/oracle/oracle-database/26/refrn/V-SESSION_CONNECT_INFO.html).
The [9.2 SQL Reference](https://docs.oracle.com/cd/B10500_01/server.920/a96540/functions122a.htm)
documents the legacy context attributes. Oracle's
[network encryption and integrity guide](https://docs.oracle.com/en/cloud/paas/database-dbaas-cloud/csdbi/use-network-encryption-and-integrity.html)
distinguishes generic service banners from negotiated algorithm adapters.

### Strict qualification recorded on 2026-11-21

Linux/amd64, Go 1.27.4, pure Go, TCP to the disposable Oracle Free PDB as
`PDBADMIN` without SYSDBA. Both images required native encryption and
checksumming and rejected weak clients. The source image digests for
`container-registry.oracle.com/database/free` were
`sha256:109421eb0e97db4ba5f01086d02c20ace673b809cd8c9a1a6e193bb04424abfb`
for 23ai and `sha256:0d714311690f0481b49819ff6f35f756441ca165ad3d757c90a8e351fbf567da`
for 26ai. The Oracle client control requested AES256 and SHA256 explicitly.

| Release | Bundled V3 | Independent Oracle client control |
| --- | --- | --- |
| Oracle 23ai Free `23.9.0.25.07` | 0/4 strict physical admissions, EOF | Same user, Oracle reported AES256 and SHA256 |
| Oracle 26ai Free `23.26.4.0.0` | 0/4 strict physical admissions, EOF | Same user, Oracle reported AES256 and SHA256 |

The server returned these banners on the control sessions. Bundled V3 returned
EOF before acquiring a lease, so its banner and runtime checks were unreachable.
Official upstream V3 commit
[`62f8a42`](https://github.com/sijms/go-ora/commit/62f8a42fe821414cec8fcacb1b877dfef836f798)
passed two strict connections to each image in a separate run. The compatibility
profile defaults to retained V2, which also failed the strict fixtures in the
separate v0.17.0 test. Track strict SDK and wider version qualification in #25.

## Oracle9.2 scalar wire behavior

Native 9.2.0.8 testing exposed two distinct issues in output framing.
A large scalar character OUT buffer can split a PL/SQL bind row with another
TTC7 message. The reader resumes at the next parameter and rejects incomplete
continuations before assigning caller destinations. Requested character
capacity is preserved. Arrays/UDTs retain their separate existing paths.

Variable-width 9i CLOB reads use length-prefixed code units in the tested wire
path. Expand them only after all data messages join, using the locator's existing
byte order. Invalid/truncated encoding returns no prefix and retires physical
state; binary LOBs and newer families keep their original bytes.

The original 32767-character OUT capacity, 5000-byte input and 125000-byte Unicode
CLOB passed after these changes. Smaller-capacity and smaller-input diagnostic
runs were retained as failure-isolation evidence, not substitutes for acceptance.

Oracle9i PL/SQL User's Guide and Reference, "PL/SQL Datatypes", read 2026-11-19,
documents a 32767-byte PL/SQL VARCHAR2 maximum and a 4000-byte SQL column maximum.
These language limits are separate from the observed retained-driver wire fix:
https://docs.oracle.com/cd/A97630_01/appdev.920/a96624/03_types.htm

## Scalar PL/SQL character input before Oracle 12.2

The retained V2 encoder normally promotes character inputs beyond the
negotiated SQL VARCHAR2 capacity to LONG. Oracle PL/SQL supports scalar
VARCHAR2 values up to 32767 bytes. On Oracle 9i through 12.1, LONG framing
for a valid scalar PL/SQL input can fail with ORA-03120 before a temporary
CLOB is returned. The V2 statement encoder keeps VARCHAR2 framing for these
inputs when their encoded size is within 32767 bytes.

The guard uses the parsed server major and minor versions, statement type, direction,
scalar shape, and encoded byte length. Oracle 12.2 and newer use the negotiated
type selection. SQL statements, array and custom-type parameters, and OUT or
IN OUT parameters follow their existing bind paths.

Oracle defines the PL/SQL VARCHAR2 limit in the
[Oracle9i PL/SQL User's Guide and Reference](https://docs.oracle.com/cd/A97630_01/appdev.920/a96624/03_types.htm)
and the
[Oracle Database PL/SQL Language Reference for 11g Release 1](https://docs.oracle.com/html/B28370_05/datatypes.htm).
The [Oracle Database Error Messages guide](https://docs.oracle.com/en/error-help/db/ora-03120/)
describes the conversion overflow.

Native comparisons on 2026-11-22 used Go 1.27.4 on Windows/amd64 with the
pure-Go V2 default and the same prepared CLOB test on each exact fixture.
The run held four physical leases, assigned a 5000-byte Unicode value to
a PL/SQL VARCHAR2, and verified the complete 125000-byte CLOB.

| Observed Oracle release | Original V2 | Corrected V2 |
| --- | --- | --- |
| 10.2.0.1.0 and 10.2.0.5.0 | ORA-03120 during execution | Pass |
| 11.2.0.2.0 and 11.2.0.4.0 | ORA-03120 during execution | Pass |
| 12.1.0.2.0 | ORA-03120 during execution | Pass |

The result covers the supplied fixture accounts and states. The harness
tests scalar values, repeated prepared OUT calls and the temporary CLOB.
Oracle 10.1 and 11.1 remain unqualified.

## Official-fix adoption and scans

Fresh official release/tag/proxy and fix checks remain required. A merged
upstream fix is distinct from a containing release and a qualified replacement.
General dependencies stay upstream-only. Do not create consumer copies, local
replacements, overlays or modified caches. Existing separately approved retained
consumer sources and retirement confirmations remain protected.

The retained paths are SDK source, so advisory scanners cannot automatically
associate them with upstream driver module versions. Record official baseline
advisories separately from the full configured source/binary scans; an empty
module scanner result alone does not audit all retained code. Preserve baseline
sums, per-file hashes, licenses and reviewed deltas when assessing any official
replacement. A successful module release does not migrate an installed app.
