#!/usr/bin/env bash
# Create a separate GKE workload cluster via CAPG. Run after bootstrap.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TF_DIR="$REPO_ROOT/terraform/gcp"
TOOL_CACHE_DIR="${XTRINODE_TOOL_CACHE_DIR:-${XDG_CACHE_HOME:-${HOME:-/tmp}/.cache}/xtrinode}"

tf_output() {
  local name="$1"
  terraform -chdir="$TF_DIR" output -raw "$name" 2>/dev/null || true
}

tf_var() {
  local name="$1"
  local file="$TF_DIR/terraform.tfvars"
  [ -f "$file" ] || return 0
  awk -F= -v key="$name" '
    $1 ~ "^[[:space:]]*" key "[[:space:]]*$" {
      value=$2
      sub(/[[:space:]]+#.*/, "", value)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      gsub(/^"|"$/, "", value)
      print value
      exit
    }
  ' "$file"
}

ensure_clusterctl() {
  if command -v clusterctl >/dev/null 2>&1; then
    command -v clusterctl
    return
  fi

  local version="${CLUSTERCTL_VERSION:-v1.11.3}"
  local bin="$TOOL_CACHE_DIR/clusterctl"
  if [ ! -x "$bin" ]; then
    mkdir -p "$TOOL_CACHE_DIR"
    echo "Installing clusterctl $version to $bin..." >&2
    curl -fsSL -o "$bin" "https://github.com/kubernetes-sigs/cluster-api/releases/download/${version}/clusterctl-linux-amd64"
    chmod +x "$bin"
  fi
  echo "$bin"
}

if [ -d "$TF_DIR" ] && command -v terraform >/dev/null 2>&1; then
  GCP_PROJECT="${GCP_PROJECT:-$(tf_output gcp_project_id)}"
  GCP_REGION="${GCP_REGION:-$(tf_output gcp_region)}"
  GCP_ZONE="${GCP_ZONE:-$(tf_output gke_cluster_location)}"
  GCP_NETWORK_NAME="${GCP_NETWORK_NAME:-$(tf_output network_name)}"
  GCP_SUBNET_NAME="${GCP_SUBNET_NAME:-$(tf_output subnet_name)}"
fi

GCP_PROJECT="${GCP_PROJECT:-$(tf_var gcp_project_id)}"
GCP_REGION="${GCP_REGION:-$(tf_var gcp_region)}"
GCP_ZONE="${GCP_ZONE:-$(tf_var gcp_zone)}"
GCP_NETWORK_NAME="${GCP_NETWORK_NAME:-$(tf_var network_name)}"
GCP_SUBNET_NAME="${GCP_SUBNET_NAME:-$(tf_var subnet_name)}"
GCP_PROJECT="${GCP_PROJECT:-${GCP_PROJECT_ID:-}}"
export GCP_REGION="${GCP_REGION:-us-central1}"
export GCP_ZONE="${GCP_ZONE:-us-central1-a}"
export GCP_NETWORK_NAME="${GCP_NETWORK_NAME:-xtrinode-network}"
export GCP_SUBNET_NAME="${GCP_SUBNET_NAME:-xtrinode-subnet}"
export CLUSTER_NAME="${CLUSTER_NAME:-xtrinode-capg-workload}"
export TARGET_NAMESPACE="${TARGET_NAMESPACE:-xtrinode-capg-real}"
export WORKER_MACHINE_COUNT="${WORKER_MACHINE_COUNT:-0}"
export CAPG_VERSION="${CAPG_VERSION:-v1.11.1}"
export OUTPUT_FILE="${OUTPUT_FILE:-/tmp/${CLUSTER_NAME}.yaml}"
WAIT_FOR_CLUSTER="${WAIT_FOR_CLUSTER:-true}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-30m}"

if [ -z "${GCP_PROJECT:-}" ]; then
  echo "GCP_PROJECT is not set and could not be read from Terraform outputs." >&2
  exit 1
fi

export GCP_PROJECT
CLUSTERCTL_BIN="$(ensure_clusterctl)"

echo "Creating GKE workload cluster manifest via CAPG..."
echo "  GCP_PROJECT=$GCP_PROJECT"
echo "  GCP_REGION=$GCP_REGION"
echo "  GCP_ZONE=$GCP_ZONE"
echo "  GCP_NETWORK_NAME=$GCP_NETWORK_NAME"
echo "  GCP_SUBNET_NAME=$GCP_SUBNET_NAME"
echo "  CLUSTER_NAME=$CLUSTER_NAME"
echo "  TARGET_NAMESPACE=$TARGET_NAMESPACE"
echo "  WORKER_MACHINE_COUNT=$WORKER_MACHINE_COUNT"
echo "  CAPG_VERSION=$CAPG_VERSION"

"$CLUSTERCTL_BIN" generate cluster "$CLUSTER_NAME" \
  --target-namespace "$TARGET_NAMESPACE" \
  --flavor gke \
  --infrastructure "gcp:${CAPG_VERSION}" > "$OUTPUT_FILE"
echo "Generated $OUTPUT_FILE"

kubectl create namespace "$TARGET_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side --dry-run=server -f "$OUTPUT_FILE"
kubectl apply -f "$OUTPUT_FILE"

if [ "$WORKER_MACHINE_COUNT" = "0" ]; then
  echo "Removing unused zero-node default MachinePool ${CLUSTER_NAME}-mp-0..."
  kubectl delete "machinepool/${CLUSTER_NAME}-mp-0" -n "$TARGET_NAMESPACE" --ignore-not-found=true
fi

if [ -n "${GCP_SUBNET_NAME:-}" ]; then
  echo "Patching GCPManagedCluster for manual-mode VPC subnet..."
  kubectl patch gcpmanagedcluster "$CLUSTER_NAME" -n "$TARGET_NAMESPACE" --type=merge -p \
    "{\"spec\":{\"network\":{\"name\":\"${GCP_NETWORK_NAME}\",\"autoCreateSubnetworks\":false,\"subnets\":[{\"name\":\"${GCP_SUBNET_NAME}\",\"region\":\"${GCP_REGION}\"}]}}}"
fi

echo "Cluster creation started. Status:"
kubectl get cluster,machinepool,gcpmanagedcluster,gcpmanagedcontrolplane,gcpmanagedmachinepool -n "$TARGET_NAMESPACE" -o wide

if [ "$WAIT_FOR_CLUSTER" = "true" ]; then
  kubectl wait "cluster/${CLUSTER_NAME}" -n "$TARGET_NAMESPACE" --for=condition=Available=True --timeout="$WAIT_TIMEOUT"
fi

echo "Get kubeconfig:"
echo "  kubectl get secret ${CLUSTER_NAME}-user-kubeconfig -n ${TARGET_NAMESPACE} -o jsonpath='{.data.value}' | base64 -d > ${CLUSTER_NAME}.kubeconfig"
