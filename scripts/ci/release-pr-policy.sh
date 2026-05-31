#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/release-lib.sh"

: "${BASE_REF:?BASE_REF is required}"
: "${HEAD_REPO_OWNER:?HEAD_REPO_OWNER is required}"
: "${PR_AUTHOR:?PR_AUTHOR is required}"

git fetch origin "${BASE_REF}" --depth=1

previous_version="$(chart_field_at_ref FETCH_HEAD helm/xtrinode/Chart.yaml version)"

if [ -z "${previous_version}" ]; then
  echo "No previous XTrinode umbrella chart version found on ${BASE_REF}; release PR policy does not apply."
  exit 0
fi

if ! release_metadata_changed_since FETCH_HEAD; then
  echo "Release metadata did not change; release PR policy does not apply."
  exit 0
fi

validate_release_version_metadata

owners="$(codeowners_at_ref FETCH_HEAD)"

if ! printf '%s\n' "${owners}" | grep -Fxq "@${PR_AUTHOR}"; then
  echo "::error::Release PR was opened by @${PR_AUTHOR}, who is not an explicit CODEOWNER."
  echo "::error::Only an explicit CODEOWNER may open a release metadata PR."
  exit 1
fi

if ! printf '%s\n' "${owners}" | grep -Fxq "@${HEAD_REPO_OWNER}"; then
  echo "::error::Release PR branch is owned by @${HEAD_REPO_OWNER}, not an explicit CODEOWNER."
  echo "::error::Release metadata PR branches must be owned by an explicit CODEOWNER."
  exit 1
fi

echo "Release PR metadata is valid; author @${PR_AUTHOR} and branch owner @${HEAD_REPO_OWNER} are CODEOWNERS."
