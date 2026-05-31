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

current_version="$(chart_field helm/xtrinode/Chart.yaml version)"
current_app_version="$(chart_field helm/xtrinode/Chart.yaml appVersion)"
target_sha="$(git rev-parse HEAD)"

output should_release false
output should_publish_images false
output image_matrix '{"include":[]}'
output target_sha "${target_sha}"
output version "${current_version}"
output app_version "${current_app_version}"
output tag "v${current_version}"

for component in $(release_components); do
  output "${component//-/_}_chart_version" "$(chart_field "$(component_chart_path "${component}")" version)"
  output "${component//-/_}_image_version" "$(chart_field "$(component_chart_path "${component}")" appVersion)"
done

if [ "${BEFORE_SHA}" = "0000000000000000000000000000000000000000" ]; then
  echo "Initial branch push; skipping release."
  exit 0
fi

previous_version="$(chart_field_at_ref "${BEFORE_SHA}" helm/xtrinode/Chart.yaml version)"

if [ -z "${previous_version}" ]; then
  echo "No previous XTrinode umbrella chart version found at ${BEFORE_SHA}; initial project import does not create a release."
  exit 0
fi

if ! release_metadata_changed_since "${BEFORE_SHA}"; then
  echo "Release metadata did not change; skipping release and image publishing."
  exit 0
fi

validate_release_version_metadata

should_release=false
if [ "${current_version}" != "${previous_version}" ]; then
  should_release=true
fi

should_publish_images=false
matrix_entries=""
for component in $(release_components); do
  chart="$(component_chart_path "${component}")"
  current_image_version="$(chart_field "${chart}" appVersion)"
  previous_image_version="$(chart_field_at_ref "${BEFORE_SHA}" "${chart}" appVersion)"

  if [ "${current_image_version}" != "${previous_image_version}" ]; then
    should_publish_images=true
    entry="$(printf '{"component":"%s","image":"%s","package":"%s","port":"%s","version":"%s"}' \
      "${component}" \
      "$(component_image_name "${component}")" \
      "$(component_package "${component}")" \
      "$(component_port "${component}")" \
      "${current_image_version}")"
    if [ -z "${matrix_entries}" ]; then
      matrix_entries="${entry}"
    else
      matrix_entries="${matrix_entries},${entry}"
    fi
  fi
done

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

output pr_number "${pr_number}"
output pr_author "${pr_author}"
output pr_head_owner "${pr_head_owner}"
output merged_by "${merged_by}"

owners="$(codeowners_at_ref "${BEFORE_SHA}")"

if ! printf '%s\n' "${owners}" | grep -Fxq "@${pr_author}"; then
  echo "::error::PR #${pr_number} was opened by @${pr_author}, who is not an explicit CODEOWNER."
  echo "::error::Release metadata is only published from PRs opened by users listed in .github/CODEOWNERS."
  exit 1
fi

if ! printf '%s\n' "${owners}" | grep -Fxq "@${pr_head_owner}"; then
  echo "::error::PR #${pr_number} branch is owned by @${pr_head_owner}, not an explicit CODEOWNER."
  echo "::error::Release metadata is only published from PR branches owned by .github/CODEOWNERS users."
  exit 1
fi

if ! printf '%s\n' "${owners}" | grep -Fxq "@${merged_by}"; then
  echo "::error::PR #${pr_number} was merged by @${merged_by}, who is not an explicit CODEOWNER."
  echo "::error::Release metadata is only published when the PR merger is listed in .github/CODEOWNERS."
  exit 1
fi

if [ "${should_release}" = true ]; then
  git fetch --tags --force
  if git show-ref --tags --verify --quiet "refs/tags/v${current_version}"; then
    existing_tag_sha="$(git rev-list -n 1 "v${current_version}")"
    if [ "${existing_tag_sha}" != "${target_sha}" ]; then
      echo "::error::Tag v${current_version} already exists at ${existing_tag_sha}, expected ${target_sha}."
      echo "::error::Bump the umbrella chart version before release."
      exit 1
    fi
    echo "Tag v${current_version} already exists at the target commit; continuing release rerun."
  fi
  output should_release true
fi

if [ "${should_publish_images}" = true ]; then
  output should_publish_images true
  output image_matrix "{\"include\":[${matrix_entries}]}"
fi

if [ "${should_release}" = true ]; then
  echo "GitHub Release v${current_version} will be created from PR #${pr_number}."
else
  echo "Umbrella chart version did not change; no GitHub Release tag will be created."
fi

if [ "${should_publish_images}" = true ]; then
  echo "Changed component images will be published to GHCR: ${matrix_entries}."
else
  echo "No component appVersion changed; no Docker images will be published."
fi

echo "Release PR author: @${pr_author}; branch owner: @${pr_head_owner}; merger: @${merged_by}."
