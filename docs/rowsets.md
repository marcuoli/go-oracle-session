# Full query datasets

Use rowset for generic native cursor consumption and complete JSON. Oracle facts,
SQL source selection and application acceptance criteria belong to their callers.

For a caller-owned database/sql handle, rowset.Query returns the actual sql.Rows
from QueryContext. The caller owns Next, Scan, Err, Close and stream budgets.
QueryJSON is the complete JSON alternative over the same supplied SQL/arguments;
it consumes and closes its own cursor. Neither function opens a connection,
changes containers, retries SQL or adds a cancellation watcher.

For a library-owned connection, keep the existing Session.Stream, Pool.Stream or
Tx.Stream cursor. These streams retain their lease and cleanup authority. Pass the
stream directly to rowset.ReadJSON; never extract its private native cursor.
Successful complete JSON requires the stream's Complete result after EOF/cleanup.

## Borrowed native handle

The handle is already admitted and remains caller-owned. Its query context must
stay alive until iteration/finalization finishes.

    columns := []rowset.Column{
        {Name: "ID", Kind: rowset.Integer},
        {Name: "VALUE", Kind: rowset.Text},
    }
    data, err := rowset.QueryJSON(ctx, conn, approvedSQL, columns,
        rowset.Limits{MaxRows: 1000, MaxBytes: 1 << 20}, boundArgs...)
    if err != nil {
        return err
    }
    // data is a complete JSON table; use encoding/json or an existing publisher.

## Owned session stream

    cursor, err := owned.Stream(ctx, statement, session.StreamOptions{})
    if err != nil {
        return err
    }
    data, err := rowset.ReadJSON(ctx, cursor, columns, rowset.Limits{})
    if err != nil {
        return err
    }
    // ReadJSON has closed the complete stream and returned its lease.

Do not concurrently iterate or close a cursor while Read/ReadJSON owns it.
Use the source operation's context: Read does not install another cancellation
authority and cannot interrupt a driver that ignores its owner's context.

## JSON contract and limits

The result is an ordered table: columns contains name/kind descriptors and rows
contains arrays in the same projection order. SQL NULL becomes null; text remains
an exact string, including whitespace and literal commas; declared integers are
exact int64 JSON numbers. Native database/sql Scan semantics remain available for
other driver domains. JSON currently supports declared text/integer columns and
rejects unsupported/lossy values, including floats, instead of guessing dates,
LOB consumption or numeric precision. Choose a reviewed domain projection when
more types require explicit semantics.

Zero limits select 10000 rows and 8 MiB; the allowed maxima are 1000000 rows and
64 MiB. Negative/excessive values fail. Bounds count standard-JSON encoded bytes;
they are result retention bounds, not a new driver/transport memory guarantee.
The source stream's own row limit may be stricter and remains authoritative.

No successful prefix is returned on query/schema/scan/terminal/Close/context/
conversion/capacity failures. Errors retain exposed causes for errors.Is/As while
ordinary Error strings omit SQL, arguments and driver details. database/sql can
hide an independent Close error after a prior Next error; failure remains failure,
and this package does not replace a driver to recover inaccessible causes.
A capacity failure during an owned stream closes it early and retires that
session under its existing policy; successful EOF permits normal continuation.

Database datasets serialize as rows or JSON, never CSV/plaintext. Genuine
DBMS_OUTPUT is the text exception. Typed text values, SQL source and diagnostics
are separate from formatting a database resultset. Nothing here authorizes SQL
as read-only, chooses a driver, changes endpoint policy or establishes complete
RAC/container visibility.

## Validation boundary

Deterministic SQL fixtures are unit tests, not native Oracle evidence. Cursor
conversion/finalization and owned-stream continuation have focused regressions;
actual server/driver/platform combinations require separate caller qualification.
The generic package adds no Oracle view SQL or numeric server-version ceiling.
