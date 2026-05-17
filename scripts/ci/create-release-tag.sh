#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_TAG:?RELEASE_TAG is required}"

git fetch --tags --force
if git show-ref --tags --verify --quiet "refs/tags/${RELEASE_TAG}"; then
  existing_tag_sha="$(git rev-list -n 1 "${RELEASE_TAG}")"
  target_sha="$(git rev-parse HEAD)"
  if [ "${existing_tag_sha}" != "${target_sha}" ]; then
    echo "::error::Tag ${RELEASE_TAG} already exists at ${existing_tag_sha}, expected ${target_sha}."
    exit 1
  fi
  echo "Tag ${RELEASE_TAG} already exists at the target commit; skipping tag creation."
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git tag -a "${RELEASE_TAG}" -m "Release ${RELEASE_TAG}" HEAD
git push origin "refs/tags/${RELEASE_TAG}"
