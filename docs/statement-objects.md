# Tables and views in execution diagnostics

Give each reviewed SQL variant its direct object list alongside the statement.
Record dictionary spelling for the schema and name, including the case of quoted
identifiers. Supply table or view as the kind when the catalog knows it. For an
unresolved schema, leave it empty. Keep references to synonyms and objects
behind them distinct. A variant that uses several tables or views can supply
all of them in display order.

Pass the original error returned by the execution layer to
`diagnostic.StatementObjects(executionErr, objects)`. The helper works with
errors returned by `session` and `rowset`. On success, pass a nil error to get
the same list. Each returned entry contains the catalog values and an optional
`ProblemCode`. A caller can show the entire list and highlight the marked
entry, then display its normal safe execution summary.

For example, a statement lists `APP.ORDERS` and `APP.ORDER_LINES`. A server
error naming `APP.ORDER_LINES` in the supported `ORA-00942` format marks that
entry. If Oracle sends the bare form, the same list is available with no
highlight. Oracle uses `ORA-00942` for absence and some access restrictions.
Say that Oracle reported a problem with the object. Its existence and the
privileges needed to access it require separate evidence.

The initial match requires an English named `ORA-00942` line with an exact,
unique local schema and object name on the primary line of an error leaf.
It understands independently quoted components. Unsupported message formats, `ORA-01031` and a target outside the
catalog yield no highlight. A declared database link or a remote Oracle stack
marker also leaves the list unmarked because the qualified name can name a
different database. A matching name from a synonym's underlying object
does not imply that an explicitly listed synonym was reported by Oracle.

The helper uses the original wrapped error and returns only reviewed names
and a fixed code. It performs no further database operations and keeps the
original execution result and error available. It does not interpret go-ora
error positions because their mapping to the submitted text has not been
qualified across driver, server, and text-encoding combinations. The feature
adds no SQL or release requirement. The named format is capability-dependent,
so a numeric release alone does not predict attribution.

## Native qualification

The go-ora compatibility connector was built with Go 1.27.4 and
CGO disabled for Windows/amd64. An existing non-SYSDBA account on each
READ WRITE fixture ran the same read-only two-object statements through
both session and rowset. Successful queries returned the complete ordered
catalog list. The session could execute a successful query after the errors.

| Oracle Database | Connection profile and scope | Observed ORA-00942 |
| --- | --- | --- |
| 9.2.0.8.0 | Retained go-ora V2, non-CDB | Bare message. Both reviewed objects were returned without a highlight. |
| 26ai, numeric 23.26.1.0.0 | Retained go-ora V2 and V3, direct PDB | Schema and object were named. Only the named object was highlighted, including a quoted mixed-case name. |

The cases exercised missing objects in both positions of a two-object list.
The results cover these fixtures, accounts and error cases. Other releases,
patches, locales, privileges and states were not exercised in this run.

Oracle references checked on 2026-11-21

- [ORA-00942](https://docs.oracle.com/en/error-help/db/ora-00942/) documents
  optional schema/object detail and several possible causes.
- [Oracle Database 19c Error Messages](https://docs.oracle.com/en/database/oracle/oracle-database/19/errmg/ORA-00910.html)
  documents the older bare message.
- [ORA-01031](https://docs.oracle.com/en/error-help/db/ora-01031/) documents the
  privilege failure without a table or view name.
- [ORA-02063](https://docs.oracle.com/en/error-help/db/ora-02063/) and [ORA-02068](https://docs.oracle.com/en/error-help/db/ora-02068/) identify remote error evidence.
