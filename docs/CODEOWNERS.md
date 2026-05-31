# Code Ownership and Release Governance

This repository uses `.github/CODEOWNERS` as the source of truth for review ownership. Before making
the repository public, enable GitHub branch protection or a repository ruleset for `main` with:

- Pull requests required before merge.
- At least one approving review required.
- Code owner review required for every pull request, including external contributor and fork PRs.
- Required status checks enabled, including `Release PR Policy`.
- Conversation resolution required.
- Direct pushes to `main` blocked except for trusted automation.
- Bypass limited to explicitly trusted maintainers or automation.
- Manual creation or movement of `v*.*.*` tags blocked or restricted, with the release workflow
  allowed to create new release tags.

## Ownership Defaults

The current CODEOWNERS file uses the `@xtrinode` GitHub account. The project contact email is
`xtrinode@gmail.com`. If the public repository later moves under an organization with teams, extend
ownership with the real maintainer team, for example `@xtrinode/maintainers`.

The top-level `* @xtrinode` rule intentionally covers every tracked file. More specific entries are
kept for governance-sensitive paths so ownership remains obvious during review, but the wildcard is
the exhaustive fallback for newly added files and directories.

Repository automation is explicitly owned. Changes under `.github/`, especially
`.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/dependabot.yml`, and
`.github/CODEOWNERS`, require the same code owner review as product code. The directory-level
`.github/` rule is intentional so newly added workflows are protected by default.

Directory-specific entries should point at directories or files that exist in the repository. New
top-level ownership rules should be added when the corresponding root path is introduced, not before.

## Release Rule

Releases are intentionally tied to merged pull requests, not manual tag pushes.

1. An explicit CODEOWNER opens a pull request from a branch owned by an explicit CODEOWNER. The PR
   changes release metadata: the umbrella Helm chart version, a component chart version, or an
   operator, API server, or gateway chart `appVersion`.
2. CI must pass on the pull request.
3. A CODEOWNER must approve the pull request.
4. A CODEOWNER must merge the pull request into `main`.
5. The release workflow creates the missing `v<umbrella-version>` tag only when the umbrella chart
   version changes, packages Helm charts only for that umbrella release, and publishes only the
   component images whose `appVersion` changed.

The CI workflow checks the release PR author and branch owner against explicit individual owners in
`.github/CODEOWNERS`, and it validates release metadata. The umbrella chart dependency versions must
match the corresponding operator, API server, and gateway chart `version` fields, but component chart
versions may differ from the umbrella chart version. The operator, API server, and gateway
`appVersion` fields are independent Docker image versions. The managed Trino runtime image tag is
pinned separately and must not be used as a control-plane image tag. The release workflow repeats
those owner checks, also checks the user who merged the pull request, and reuses the same version
metadata validation. Team ownership is still useful for review enforcement, but release authoring,
branch ownership, and merging need individual CODEOWNER entries unless the workflows are extended to
resolve team membership.

`Release PR Policy` must be configured as a required check on `main`; workflow logic cannot enforce
that requirement by itself. CODEOWNER approval is enforced by GitHub branch protection or rulesets,
not by the workflow file.

If the umbrella chart version does not change, the release workflow does not create a Git tag or
GitHub Release. Docker images can still publish from the merge commit without a release tag when one
or more component `appVersion` fields change. If a PR changes the umbrella chart version to one that
already has a matching tag, the workflow fails so the release version can be corrected before
publishing.
