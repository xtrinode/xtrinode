#!/usr/bin/env bash
# Delete the local k3d cluster and its development registry.
set -euo pipefail

K3D_CLUSTER_NAME="${K3D_CLUSTER_NAME:-xtrinode-dev}"
K3D_REGISTRY_NAME="${K3D_REGISTRY_NAME:-xtrinode-registry}"
K3D="${K3D:-k3d}"

if ! "$K3D" version >/dev/null 2>&1; then
  echo "ERROR: k3d is required. Install it or set K3D=/path/to/k3d." >&2
  exit 1
fi

"$K3D" cluster delete "$K3D_CLUSTER_NAME" >/dev/null 2>&1 || true
"$K3D" registry delete "k3d-${K3D_REGISTRY_NAME}" >/dev/null 2>&1 || true

echo "Deleted k3d cluster ${K3D_CLUSTER_NAME}"
