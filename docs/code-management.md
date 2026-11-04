# Code management

## Issues and branches

Use typed imperative titles (feat, fix, docs, refactor, test, build, security, chore), one matching type label and relevant area labels. Each issue contains `## Validation and acceptance` with concrete Markdown checkboxes. Keep partial or untested outcomes unchecked. Branches use codex/<type>/<issue>-<description>, with feature for feat.

## Review and validation

Prepare a focused PR with its own newest-first CHANGELOG.md entry. Preserve existing behavior and add meaningful regression tests for intentional changes. Follow the code-comment convention in CONTRIBUTING.md and review nearby comments with each change. Run format, complete tests, vet, module verification, affected race tests and configured builds; record compiler, module graph, build tags and platform. Review applicable source/binary advisory findings and retain documented exceptions explicitly.

A bounded independent review precedes integration. Merge through the source forge, then synchronize canonical main with a fast-forward. Verify remote refs and required checks. Preserve unrelated source and credentials.

## Releases and public copy

The configured Gitea origin owns the history. GitHub is a public copy with the same branch and tag identities. Module releases use ordinary immutable tags such as v0.1.0. The copy workflow uses a repository-scoped deploy key, explicit non-forced atomic branch/tag pushes and no deletion. Release pages are published separately. Audit all public content and commit metadata first; private provenance remains outside the repository.

## Storage and issue closeout

Use project-first external storage for logs, binaries, caches and scratch. The local scripts use V:/Development/<module>; an explicit DEVELOPMENT_ROOT may select the approved equivalent, and CI uses RUNNER_TEMP/development. Keep permanent source in its owning cloud-backed tree.

Record each issue worktree's actual administrative Git path. Before closure, reconcile tracked, untracked and ignored files, integrate every in-scope commit, preserve unique evidence, remove temporary worktrees through Git and verify deregistration plus checkout/admin-directory removal. Keep the issue open if integration, publication or cleanup remains. Never force removal or alter permissions to manufacture completion.
