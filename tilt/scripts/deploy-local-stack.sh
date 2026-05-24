#!/usr/bin/env bash
# Deploy the XTrinode control plane and gateway into the local k3d cluster.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
API_SERVER_NAMESPACE="${API_SERVER_NAMESPACE:-xtrinode-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
LOCAL_IMAGE_TAG="${LOCAL_IMAGE_TAG:-dev}"
K3D_REGISTRY_NAME="${K3D_REGISTRY_NAME:-xtrinode-registry}"
LOCAL_REGISTRY_CLUSTER="${LOCAL_REGISTRY_CLUSTER:-${K3D_REGISTRY_NAME}:5000}"
HELM_TIMEOUT="${HELM_TIMEOUT:-8m}"
LOCAL_ROLLOUT_RESTART="${LOCAL_ROLLOUT_RESTART:-true}"
HELM="${HELM:-helm}"
KUBECTL="${KUBECTL:-kubectl}"

operator_chart="${ROOT_DIR}/helm/xtrinode-operator"
api_chart="${ROOT_DIR}/helm/xtrinode-api-server"
gateway_chart="${ROOT_DIR}/helm/xtrinode-gateway"
values_dir="${ROOT_DIR}/tilt/deployments/values"

observability_chart="${ROOT_DIR}/helm/xtrinode-observability"

"$KUBECTL" create namespace "$OPERATOR_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace "$GATEWAY_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace monitoring --dry-run=client -o yaml | "$KUBECTL" apply -f -

echo "Applying XTrinode CRDs"
"$KUBECTL" apply -f "${operator_chart}/crds"

echo "Deploying local Prometheus observability stack"
"$HELM" upgrade --install xtrinode-observability "$observability_chart" \
  -n monitoring \
  -f "${values_dir}/observability.yaml" \
  --wait \
  --timeout "$HELM_TIMEOUT"

echo "Deploying operator and bundled KEDA"
"$HELM" upgrade --install xtrinode-operator "$operator_chart" \
  -n "$OPERATOR_NAMESPACE" \
  -f "${values_dir}/operator.yaml" \
  --set "image.repository=${LOCAL_REGISTRY_CLUSTER}/xtrinode-operator" \
  --set "image.tag=${LOCAL_IMAGE_TAG}" \
  --wait \
  --timeout "$HELM_TIMEOUT"

echo "Deploying API server"
"$HELM" upgrade --install xtrinode-api-server "$api_chart" \
  -n "$API_SERVER_NAMESPACE" \
  -f "${values_dir}/api-server.yaml" \
  --set "image.repository=${LOCAL_REGISTRY_CLUSTER}/xtrinode-api-server" \
  --set "image.tag=${LOCAL_IMAGE_TAG}" \
  --wait \
  --timeout "$HELM_TIMEOUT"

echo "Deploying gateway with local Redis"
"$HELM" upgrade --install xtrinode-gateway "$gateway_chart" \
  -n "$GATEWAY_NAMESPACE" \
  -f "${values_dir}/gateway.yaml" \
  --set "image.repository=${LOCAL_REGISTRY_CLUSTER}/xtrinode-gateway" \
  --set "image.tag=${LOCAL_IMAGE_TAG}" \
  --wait \
  --timeout "$HELM_TIMEOUT"

if [ "$LOCAL_ROLLOUT_RESTART" = "true" ]; then
  echo "Restarting local mutable-tag deployments"
  "$KUBECTL" rollout restart deployment/xtrinode-operator -n "$OPERATOR_NAMESPACE"
  "$KUBECTL" rollout restart deployment/xtrinode-api-server -n "$API_SERVER_NAMESPACE"
  "$KUBECTL" rollout restart deployment/xtrinode-gateway -n "$GATEWAY_NAMESPACE"
fi

"$KUBECTL" rollout status deployment/xtrinode-operator -n "$OPERATOR_NAMESPACE" --timeout="$HELM_TIMEOUT"
"$KUBECTL" rollout status deployment/xtrinode-api-server -n "$API_SERVER_NAMESPACE" --timeout="$HELM_TIMEOUT"
"$KUBECTL" rollout status deployment/xtrinode-gateway -n "$GATEWAY_NAMESPACE" --timeout="$HELM_TIMEOUT"

echo "Local XTrinode stack is deployed"
