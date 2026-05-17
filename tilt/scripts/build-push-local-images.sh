#!/usr/bin/env bash
# Build component images and push them to the k3d-managed local registry.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOCAL_IMAGE_TAG="${LOCAL_IMAGE_TAG:-dev}"
LOCAL_COMPONENTS="${LOCAL_COMPONENTS:-operator api-server gateway}"
K3D_REGISTRY_PORT="${K3D_REGISTRY_PORT:-5001}"
LOCAL_REGISTRY_HOST="${LOCAL_REGISTRY_HOST:-localhost:${K3D_REGISTRY_PORT}}"
GO_VERSION="${GO_VERSION:-$(awk '/^go / {print $2; exit}' "${ROOT_DIR}/xtrinode/go.mod")}"
ALPINE_VERSION="${ALPINE_VERSION:-3.23}"
GIT_COMMIT="${GIT_COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"

build_component() {
  local image_name="$1"
  local app_package="$2"
  local app_port="$3"
  local image_ref="${LOCAL_REGISTRY_HOST}/${image_name}:${LOCAL_IMAGE_TAG}"

  echo "Building ${image_ref}"
  docker build \
    --build-arg "GO_VERSION=${GO_VERSION}" \
    --build-arg "ALPINE_VERSION=${ALPINE_VERSION}" \
    --build-arg "APP_PACKAGE=${app_package}" \
    --build-arg "APP_PORT=${app_port}" \
    --build-arg "VERSION=${LOCAL_IMAGE_TAG}" \
    --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
    --build-arg "BUILD_DATE=${BUILD_DATE}" \
    -t "$image_ref" \
    -f "${ROOT_DIR}/docker/Dockerfile" \
    "${ROOT_DIR}/xtrinode"

  echo "Pushing ${image_ref}"
  docker push "$image_ref"
}

for component in $LOCAL_COMPONENTS; do
  case "$component" in
    operator)
      build_component "xtrinode-operator" "./cmd/operator" "8081"
      ;;
    api-server)
      build_component "xtrinode-api-server" "./cmd/api-server" "8081"
      ;;
    gateway)
      build_component "xtrinode-gateway" "./cmd/gateway" "8080"
      ;;
    *)
      echo "ERROR: unknown LOCAL_COMPONENTS entry: $component" >&2
      exit 1
      ;;
  esac
done

echo "Local images pushed with tag ${LOCAL_IMAGE_TAG}"
