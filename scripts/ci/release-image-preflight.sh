#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
: "${RELEASE_IMAGE:?RELEASE_IMAGE is required}"
: "${RELEASE_IMAGE_VERSION:?RELEASE_IMAGE_VERSION is required}"
: "${TARGET_SHA:?TARGET_SHA is required}"

DOCKER="${DOCKER:-docker}"

output() {
  printf '%s=%s\n' "$1" "$2" >> "${GITHUB_OUTPUT}"
}

restore_stable_floating_tags() {
  local exact_ref="${1:?exact image ref is required}"
  local major major_minor minor_rest tag

  if [[ "${RELEASE_IMAGE_VERSION}" == *-* ]]; then
    return
  fi

  major="${RELEASE_IMAGE_VERSION%%.*}"
  minor_rest="${RELEASE_IMAGE_VERSION#*.}"
  major_minor="${major}.${minor_rest%%.*}"

  for tag in "${major_minor}" "${major}" latest; do
    echo "Ensuring ${RELEASE_IMAGE}:${tag} points at ${exact_ref}."
    "${DOCKER}" buildx imagetools create --tag "${RELEASE_IMAGE}:${tag}" "${exact_ref}"
  done
}

exact_ref="${RELEASE_IMAGE}:${RELEASE_IMAGE_VERSION}"

if ! manifest_error="$("${DOCKER}" manifest inspect "${exact_ref}" 2>&1 >/dev/null)"; then
  if printf '%s\n' "${manifest_error}" | grep -Eiq 'manifest unknown|not found|no such manifest'; then
    echo "No existing exact release image tag found: ${exact_ref}."
    output exact_tag_exists false
    output should_push true
    exit 0
  fi

  echo "::error::Could not verify whether exact release image tag exists: ${exact_ref}."
  echo "::error::Failing closed to avoid overwriting an existing release image tag."
  if [ -n "${manifest_error}" ]; then
    echo "::error::docker manifest inspect: ${manifest_error}"
  fi
  exit 1
fi

output exact_tag_exists true

echo "Exact release image tag already exists: ${exact_ref}. Verifying image labels before rerun recovery."
"${DOCKER}" pull "${exact_ref}" >/dev/null

revision="$("${DOCKER}" image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${exact_ref}" 2>/dev/null || true)"
version="$("${DOCKER}" image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "${exact_ref}" 2>/dev/null || true)"

if [ "${revision}" != "${TARGET_SHA}" ]; then
  echo "::error::${exact_ref} already exists with revision '${revision:-missing}', expected '${TARGET_SHA}'."
  echo "::error::Do not overwrite exact release image tags. Bump the component appVersion or repair the registry manually."
  exit 1
fi

if [ "${version}" != "${RELEASE_IMAGE_VERSION}" ]; then
  echo "::error::${exact_ref} already exists with version label '${version:-missing}', expected '${RELEASE_IMAGE_VERSION}'."
  echo "::error::Do not overwrite exact release image tags. Bump the component appVersion or repair the registry manually."
  exit 1
fi

restore_stable_floating_tags "${exact_ref}"

output should_push false
echo "Existing exact release image tag matches ${TARGET_SHA}; skipping rebuild and exact-tag push."
