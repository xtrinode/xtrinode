#!/usr/bin/env bash
# Create the local k3d cluster used by Tilt and local e2e tests.
set -euo pipefail

K3D_CLUSTER_NAME="${K3D_CLUSTER_NAME:-xtrinode-dev}"
K3D_REGISTRY_NAME="${K3D_REGISTRY_NAME:-xtrinode-registry}"
K3D_REGISTRY_PORT="${K3D_REGISTRY_PORT:-5001}"
K3D_AGENTS="${K3D_AGENTS:-1}"
K3D_K3S_IMAGE="${K3D_K3S_IMAGE:-rancher/k3s:v1.34.8-k3s1}"
K3D="${K3D:-k3d}"
KUBECTL="${KUBECTL:-kubectl}"

if ! "$K3D" version >/dev/null 2>&1; then
  echo "ERROR: k3d is required. Install it or set K3D=/path/to/k3d." >&2
  exit 1
fi

if "$K3D" cluster list "$K3D_CLUSTER_NAME" >/dev/null 2>&1; then
  echo "k3d cluster already exists: ${K3D_CLUSTER_NAME}"
  "$KUBECTL" config use-context "k3d-${K3D_CLUSTER_NAME}" >/dev/null
else
  config_file="$(mktemp)"
  trap 'rm -f "$config_file"' EXIT

  cat > "$config_file" <<EOF
apiVersion: k3d.io/v1alpha5
kind: Simple
metadata:
  name: ${K3D_CLUSTER_NAME}
servers: 1
agents: ${K3D_AGENTS}
image: ${K3D_K3S_IMAGE}
registries:
  create:
    name: ${K3D_REGISTRY_NAME}
    host: "0.0.0.0"
    hostPort: "${K3D_REGISTRY_PORT}"
options:
  k3d:
    wait: true
    timeout: "180s"
  k3s:
    extraArgs:
      - arg: "--disable=traefik"
        nodeFilters:
          - server:*
EOF

  echo "Creating k3d cluster ${K3D_CLUSTER_NAME} with registry localhost:${K3D_REGISTRY_PORT}"
  "$K3D" cluster create --config "$config_file"
fi

"$KUBECTL" config use-context "k3d-${K3D_CLUSTER_NAME}" >/dev/null
"$KUBECTL" wait node --all --for=condition=Ready --timeout=180s
"$KUBECTL" get nodes -o wide

echo "k3d cluster is ready: k3d-${K3D_CLUSTER_NAME}"
echo "Local push registry: localhost:${K3D_REGISTRY_PORT}"
echo "Cluster pull registry: ${K3D_REGISTRY_NAME}:5000"
