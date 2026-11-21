# Bounded query JSON envelopes

Add one generic rowset operation that opens a caller-selected cursor lazily,
delegates all iteration and finalization to Read, optionally validates the
completed Table, and returns only complete bounded JSON. Existing ReadJSON and
QueryJSON signatures and behavior stay compatible.

Default metadata is an ordered provenance member. A caller may supply a complete
metadata object instead, excluding the reserved top-level data member; this
supports additional fields without SDK knowledge of their meaning. Encode and
freeze metadata before opening, reserve its exact final overhead, and append
the encoded Table as the final data member. Optional MaxRows only narrows Limits.
Preserve native column casing, exact integers, SQL NULL, row order and duplicates.

Expose lazy OpenQuery and a CursorOpener for native or owned Stream cursors.
Forward the original context; add no watcher, retry, SQL or connection policy.
Error stages let callers preserve their existing domain wrappers and input
precedence. Return nil JSON on any error, including closure and cancellation.
Without a validator, table encoding uses existing ReadJSON and preserves its
typed encoding error layer. Early input/open/validation errors keep their own
precedence; Read retains
its finalization joins, and final encoding checks the caller context.

Use Go 1.27.4 and existing dependencies. Use synthetic fixtures only; no Oracle
SQL or server compatibility selection changes. Record exact production added
and deleted lines independently from tests/docs and the consumer's own reduction.
