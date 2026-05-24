#!/usr/bin/env bash
# Preload third-party images used by the local k3d/Tilt/e2e stack into k3d nodes.
set -euo pipefail

K3D_CLUSTER_NAME="${K3D_CLUSTER_NAME:-xtrinode-dev}"
TRINO_IMAGE_REPOSITORY="${TRINO_IMAGE_REPOSITORY:-trinodb/trino}"
TRINO_IMAGE_TAG="${TRINO_IMAGE_TAG:-480}"
LOCAL_PRELOAD_IMAGES_ENABLED="${LOCAL_PRELOAD_IMAGES_ENABLED:-true}"
LOCAL_PRELOAD_SKIP_PULL="${LOCAL_PRELOAD_SKIP_PULL:-false}"
K3D="${K3D:-k3d}"
DOCKER="${DOCKER:-docker}"

DEFAULT_LOCAL_PRELOAD_IMAGES="${TRINO_IMAGE_REPOSITORY}:${TRINO_IMAGE_TAG}
postgres:16-alpine
python:3.12-alpine
redis:7.4-alpine
bitnami/jmx-exporter@sha256:7c0014b7e1d736faec9760a89727389ba1ba7ad920c764417167abecfb7fd032
quay.io/prometheus-operator/prometheus-operator:v0.90.1
quay.io/prometheus-operator/prometheus-config-reloader:v0.90.1
quay.io/prometheus/prometheus:v3.11.3-distroless
ghcr.io/jkroepke/kube-webhook-certgen:1.8.2
docker.io/busybox:latest
registry.k8s.io/kubectl:v1.32.3
ghcr.io/kedacore/keda:2.19.0
ghcr.io/kedacore/keda-metrics-apiserver:2.19.0
ghcr.io/kedacore/keda-admission-webhooks:2.19.0"

LOCAL_PRELOAD_IMAGES="${LOCAL_PRELOAD_IMAGES:-$DEFAULT_LOCAL_PRELOAD_IMAGES}"

if [ "$LOCAL_PRELOAD_IMAGES_ENABLED" != "true" ]; then
  echo "Local image preload is disabled"
  exit 0
fi

if ! "$K3D" version >/dev/null 2>&1; then
  echo "ERROR: k3d is required. Install it or set K3D=/path/to/k3d." >&2
  exit 1
fi

if ! "$K3D" cluster list "$K3D_CLUSTER_NAME" >/dev/null 2>&1; then
  echo "ERROR: k3d cluster not found: ${K3D_CLUSTER_NAME}. Run: make k3d-up" >&2
  exit 1
fi

tar_files=()
cleanup() {
  for tar_file in "${tar_files[@]}"; do
    rm -f "$tar_file"
  done
}
trap cleanup EXIT

pull_if_needed() {
  local image="$1"

  if "$DOCKER" image inspect "$image" >/dev/null 2>&1; then
    echo "Image already present locally: ${image}"
    return
  fi

  if [ "$LOCAL_PRELOAD_SKIP_PULL" = "true" ]; then
    echo "ERROR: image is not present locally and LOCAL_PRELOAD_SKIP_PULL=true: ${image}" >&2
    exit 1
  fi

  echo "Pulling local preload image: ${image}"
  "$DOCKER" pull "$image"
}

tagged_images=()
digest_images=()
for image in $LOCAL_PRELOAD_IMAGES; do
  [ -n "$image" ] || continue
  pull_if_needed "$image"
  if [[ "$image" == *@sha256:* ]]; then
    digest_images+=("$image")
  else
    tagged_images+=("$image")
  fi
done

if [ "${#tagged_images[@]}" -gt 0 ]; then
  echo "Importing tagged images into k3d cluster ${K3D_CLUSTER_NAME}"
  "$K3D" image import --cluster "$K3D_CLUSTER_NAME" "${tagged_images[@]}"
fi

for image in "${digest_images[@]}"; do
  safe_name="${image//[^A-Za-z0-9_.-]/_}"
  tar_file="${TMPDIR:-/tmp}/xtrinode-preload-${safe_name}.tar"
  tar_files+=("$tar_file")
  echo "Saving digest image archive: ${image}"
  "$DOCKER" save -o "$tar_file" "$image"
  echo "Importing digest image archive into k3d cluster ${K3D_CLUSTER_NAME}: ${image}"
  "$K3D" image import --cluster "$K3D_CLUSTER_NAME" "$tar_file"
done

echo "Local k3d image preload complete"
