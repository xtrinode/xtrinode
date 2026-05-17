#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
: "${RELEASE_TAG:?RELEASE_TAG is required}"

previous_tag="$(
  git tag --sort=-version:refname |
    grep -E '^v[0-9]+\.[0-9]+\.[0-9]+' |
    grep -v "^${RELEASE_TAG}$" |
    head -n 1 || true
)"

if [ -z "${previous_tag}" ]; then
  notes="$(git log --pretty=format:"- %s (%h)" "${RELEASE_TAG}")"
else
  notes="$(git log --pretty=format:"- %s (%h)" "${previous_tag}..${RELEASE_TAG}")"
fi

{
  echo "notes<<EOF"
  echo "${notes}"
  echo "EOF"
} >> "${GITHUB_OUTPUT}"
