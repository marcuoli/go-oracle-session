# Retained pure-Go compatibility implementation

These internal packages are actual source content of the parent SDK module.
They are included by an immutable module ZIP without nested module manifests,
local replacements, vendoring, overlays or build-time rewriting. Consumers use
`goora/compat`; direct internal-driver registrations are implementation details.

Issue #46 records the explicit user-authorized retained-source exception.
Other dependencies continue to use official compatible releases. This exception
does not authorize changes to, adoption by, or retirement of any consumer's
existing retained drivers.

The official baselines are go-ora/v2 v2.9.0
(`f8dc23ec168d75fba8e70cccffdaa43023feee07`) and go-ora/v3 v3.0.1
(`025c51529284177330cde75c2e60c137c5f30fe1`). Original MIT licenses and notices
are retained in each package tree. `sources.json` records upstream module sums,
per-file official-baseline hashes where available, complete packaged hashes and
the retained compatibility contracts. Hashes normalize CRLF to LF.

Retained changes cover bounded ACCEPT/TTC decoding, connection-scoped warning
handling, pre-authentication legacy capability reporting and joined transport
cleanup, legacy authentication/lifecycle, ordinary break/settlement capability,
and optional scalar connection diagnostics. Module packaging additionally
rewrites owned import paths, gives both drivers unique registrations and uses a
generic synthetic Ping projection name. Original upstream tests/integration
examples outside the reviewed retained inclusion set are not claimed covered.

These sources are a retained implementation, not an unmodified official release.
Review a containing official upstream release and compare parser, admission,
values, LOBs and cancellation on each required target before retiring a retained
contract. A merged upstream change or successful synthetic test is insufficient.
The SDK compatibility package documents actual native results and unqualified
combinations separately. Private source-origin and authorization records remain
outside the repository.
