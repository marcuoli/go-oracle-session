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
| Negotiated | Start modern; select legacy only after its clean pre-authentication capability signal and completed transport cleanup. |

Negotiated is opt-in and does not select by a numeric database-version ceiling.
Authentication, transport, cancellation, joined cleanup and panic failures never
select another backend. At most one backend submits authentication. SQL is never
replayed. Explicit V2 is the conservative default, not proof of every native
server/patch/feature combination.

V2 removes only FAST LOGIN options that its parser cannot accept. V3 selects
FAST LOGIN=FALSE through the existing goora.PrepareURL policy. Other decoded URL
values, endpoint and credential bytes remain caller-owned. Native administrative
role options must be supplied explicitly; a migration string such as a username
followed by an administrative role is not a literal URL username.

~~~go
connector, err := compat.NewConnector(dsn, compat.Options{Mode: compat.V2})
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

## Oracle9.2 scalar wire behavior

Native 9.2.0.8 testing exposed and verified three additional retained changes.
A large scalar character OUT buffer can split a PL/SQL bind row with another
TTC7 message. The reader resumes at the next parameter and rejects incomplete
continuations before assigning caller destinations. Requested character
capacity is preserved. Arrays/UDTs retain their separate existing paths.

For 9i PL/SQL scalar input through 32767 bytes, keep VARCHAR2 rather than selecting
LONG merely because the SQL VARCHAR2 limit is 4000 bytes. This rule does not
rewrite SQL or change SQL/array/newer-server type selection.

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
