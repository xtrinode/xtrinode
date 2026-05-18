#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/release-lib.sh"

: "${BEFORE_SHA:?BEFORE_SHA is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

output() {
  printf '%s=%s\n' "$1" "$2" >> "${GITHUB_OUTPUT}"
}

output should_release false
output should_publish_images false
output target_sha "$(git rev-parse HEAD)"

if [ "${BEFORE_SHA}" = "0000000000000000000000000000000000000000" ]; then
  echo "Initial branch push; skipping release."
  exit 0
fi

pr_number="$(
  gh api \
    -H "Accept: application/vnd.github+json" \
    "/repos/${GITHUB_REPOSITORY}/commits/${GITHUB_SHA}/pulls" \
    --jq '[.[] | select(.merged_at != null)] | last | .number // ""'
)"

if [ -z "${pr_number}" ]; then
  echo "No merged pull request is associated with ${GITHUB_SHA}; skipping release."
  exit 0
fi

merged_by="$(
  gh api \
    -H "Accept: application/vnd.github+json" \
    "/repos/${GITHUB_REPOSITORY}/pulls/${pr_number}" \
    --jq '.merged_by.login // ""'
)"

if [ -z "${merged_by}" ]; then
  echo "::error::Could not determine who merged PR #${pr_number}."
  exit 1
fi

pr_author="$(
  gh api \
    -H "Accept: application/vnd.github+json" \
    "/repos/${GITHUB_REPOSITORY}/pulls/${pr_number}" \
    --jq '.user.login // ""'
)"

if [ -z "${pr_author}" ]; then
  echo "::error::Could not determine who opened PR #${pr_number}."
  exit 1
fi

pr_head_owner="$(
  gh api \
    -H "Accept: application/vnd.github+json" \
    "/repos/${GITHUB_REPOSITORY}/pulls/${pr_number}" \
    --jq '.head.repo.owner.login // ""'
)"

if [ -z "${pr_head_owner}" ]; then
  echo "::error::Could not determine who owns the head branch for PR #${pr_number}."
  exit 1
fi

current_version="$(awk '/^version:/ {print $2; exit}' helm/xtrinode/Chart.yaml | tr -d '"')"
current_image_version="$(release_image_version)"
previous_version="$(
  git show "${BEFORE_SHA}:helm/xtrinode/Chart.yaml" 2>/dev/null |
    awk '/^version:/ {print $2; exit}' |
    tr -d '"' || true
)"
previous_image_version="$(
  git show "${BEFORE_SHA}:helm/xtrinode/Chart.yaml" 2>/dev/null |
    awk '/^appVersion:/ {print $2; exit}' |
    tr -d '"' || true
)"

output pr_number "${pr_number}"
output pr_author "${pr_author}"
output pr_head_owner "${pr_head_owner}"
output merged_by "${merged_by}"
output version "${current_version}"
output image_version "${current_image_version}"
output tag "v${current_version}"

if [ "${current_version}" = "${previous_version}" ]; then
  echo "Chart version did not change (${current_version}); skipping release."
  exit 0
fi

if [ -z "${previous_version}" ]; then
  echo "No previous XTrinode chart version found at ${BEFORE_SHA}; initial project import does not create a release."
  exit 0
fi

validate_release_version_metadata "${current_version}"

if [ "${current_image_version}" != "${previous_image_version}" ]; then
  output should_publish_images true
fi

owners="$(codeowners_at_ref "${BEFORE_SHA}")"

if ! printf '%s\n' "${owners}" | grep -Fxq "@${pr_author}"; then
  echo "::error::PR #${pr_number} was opened by @${pr_author}, who is not an explicit CODEOWNER."
  echo "::error::Release tags are only created from PRs opened by users listed in .github/CODEOWNERS."
  exit 1
fi

if ! printf '%s\n' "${owners}" | grep -Fxq "@${pr_head_owner}"; then
  echo "::error::PR #${pr_number} branch is owned by @${pr_head_owner}, not an explicit CODEOWNER."
  echo "::error::Release tags are only created from PR branches owned by .github/CODEOWNERS users."
  exit 1
fi

if ! printf '%s\n' "${owners}" | grep -Fxq "@${merged_by}"; then
  echo "::error::PR #${pr_number} was merged by @${merged_by}, who is not an explicit CODEOWNER."
  echo "::error::Release tags are only created when the PR merger is listed in .github/CODEOWNERS."
  exit 1
fi

git fetch --tags --force
if git show-ref --tags --verify --quiet "refs/tags/v${current_version}"; then
  existing_tag_sha="$(git rev-list -n 1 "v${current_version}")"
  target_sha="$(git rev-parse HEAD)"
  if [ "${existing_tag_sha}" != "${target_sha}" ]; then
    echo "::error::Tag v${current_version} already exists at ${existing_tag_sha}, expected ${target_sha}."
    echo "::error::Bump the chart version before release."
    exit 1
  fi
  echo "Tag v${current_version} already exists at the target commit; continuing release rerun."
fi

output should_release true
echo "Release v${current_version} will be created from PR #${pr_number}."
echo "Release image version: ${current_image_version}."
echo "Release PR author: @${pr_author}; branch owner: @${pr_head_owner}; merger: @${merged_by}."
