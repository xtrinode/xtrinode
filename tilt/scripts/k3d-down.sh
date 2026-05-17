#!/usr/bin/env bash
# Delete the local k3d cluster and its development registry.
set -euo pipefail

K3D_CLUSTER_NAME="${K3D_CLUSTER_NAME:-xtrinode-dev}"
K3D_REGISTRY_NAME="${K3D_REGISTRY_NAME:-xtrinode-registry}"

resolve_k3d() {
  if [ -n "${K3D:-}" ]; then
    if command -v "$K3D" >/dev/null 2>&1; then
      command -v "$K3D"
    else
      printf '%s\n' "$K3D"
    fi
    return
  fi
  if command -v k3d >/dev/null 2>&1; then
    command -v k3d
    return
  fi
}

K3D_BIN="$(resolve_k3d)"
if [ -z "$K3D_BIN" ] || [ ! -x "$K3D_BIN" ]; then
  echo "ERROR: k3d not found. Run: make ensure-k3d" >&2
  exit 1
fi

"$K3D_BIN" cluster delete "$K3D_CLUSTER_NAME" >/dev/null 2>&1 || true
"$K3D_BIN" registry delete "k3d-${K3D_REGISTRY_NAME}" >/dev/null 2>&1 || true

echo "Deleted k3d cluster ${K3D_CLUSTER_NAME}"
