# Repository guidance

This repository is a reusable Go library. Organize implementation and tests in cohesive responsibility directories. Keep only thin compatibility aliases at the root for already-published APIs; new consumers use the functional package imports. Keep private implementation internal only when necessary. Preserve typed errors, compatibility and documented platform behavior.

## Development and comments

- Use Go 1.27.3. Follow CONTRIBUTING.md and docs/code-management.md.
- Document exported symbols with Go doc comments starting with their names. Explain observable behavior, inputs, ownership, concurrency, side effects, errors and limitations where relevant.
- Explain the concrete reason for non-obvious code: the problem, chosen behavior and compatibility constraint. Keep comments next to the implementation and update them with behavior changes.
- Comments must be concise and accurate. Do not restate obvious code, make unverified safety claims, name consuming/originating applications, or include private paths, credentials or transient investigation history.
- Use generic examples and synthetic inputs. Preserve copyright/license notices. Keep private provenance and delivery evidence outside this repository.
- Use official upstream dependencies; never maintain patched dependency copies or local replacement fixes.

## Change workflow

- Track one bounded change per issue using a typed imperative title, one type:* label and applicable area:* labels. Include the exact heading `## Validation and acceptance` and evidence-backed checkboxes.
- Use codex/<type>/<issue>-<description> branches (feature for feat), reviewed PRs and a CHANGELOG.md entry in each merged PR. Preserve unrelated work and never force pushes or immutable tags.
- Validate format, tests, vet, modules, applicable race checks, target builds and advisories with the configured compiler. CI validates native Windows, Linux and macOS behavior.
- Use scripts/validate.ps1 or scripts/validate.sh. Local generated files use project-first V: development storage; CI uses its explicitly supplied runner storage. Never generate scratch in source or a user temp directory.
- Keep permanent canonical source in its cloud-backed I: tree. Associate every temporary worktree with an open issue, preserve unique files/evidence, record its actual Git admin path, remove via Git and verify registration/checkout/admin removal before closure.

## Source and publication

The configured Gitea origin owns implementation history. GitHub is the public distribution copy. Audit source and commit metadata before public pushes; use the configured public noreply identity. Libraries use conventional immutable module tags, starting at v0.1.0. Validate consumers with GOWORK=off and no local replace. Distinguish implemented, merged, released and integrated outcomes.

- Before adding a reusable mechanism, inspect existing modules and record additional sharing candidates in the owning issue. Extract only a cohesive contract needed by real consumers; keep product policy at the boundary.
