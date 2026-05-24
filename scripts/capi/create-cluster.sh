#!/usr/bin/env bash
# Create a separate GKE workload cluster via CAPG. Run after bootstrap.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TF_DIR="$REPO_ROOT/terraform/gcp"
TERRAFORM="${TERRAFORM:-terraform}"
KUBECTL="${KUBECTL:-kubectl}"
CLUSTERCTL="${CLUSTERCTL:-clusterctl}"

tf_output() {
  local name="$1"
  "$TERRAFORM" -chdir="$TF_DIR" output -raw "$name"
}

GCP_PROJECT="${GCP_PROJECT:-${GCP_PROJECT_ID:-}}"
GCP_REGION="${GCP_REGION:-}"
GCP_ZONE="${GCP_ZONE:-}"
GCP_NETWORK_NAME="${GCP_NETWORK_NAME:-}"
GCP_SUBNET_NAME="${GCP_SUBNET_NAME:-}"

[ -n "$GCP_PROJECT" ] || GCP_PROJECT="$(tf_output gcp_project_id)"
[ -n "$GCP_REGION" ] || GCP_REGION="$(tf_output gcp_region)"
[ -n "$GCP_ZONE" ] || GCP_ZONE="$(tf_output gke_cluster_location)"
[ -n "$GCP_NETWORK_NAME" ] || GCP_NETWORK_NAME="$(tf_output network_name)"
[ -n "$GCP_SUBNET_NAME" ] || GCP_SUBNET_NAME="$(tf_output subnet_name)"

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

"$CLUSTERCTL" generate cluster "$CLUSTER_NAME" \
  --target-namespace "$TARGET_NAMESPACE" \
  --flavor gke \
  --infrastructure "gcp:${CAPG_VERSION}" > "$OUTPUT_FILE"
echo "Generated $OUTPUT_FILE"

"$KUBECTL" create namespace "$TARGET_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" apply --server-side --dry-run=server -f "$OUTPUT_FILE"
"$KUBECTL" apply -f "$OUTPUT_FILE"

if [ "$WORKER_MACHINE_COUNT" = "0" ]; then
  echo "Removing unused zero-node default MachinePool ${CLUSTER_NAME}-mp-0..."
  "$KUBECTL" delete "machinepool/${CLUSTER_NAME}-mp-0" -n "$TARGET_NAMESPACE" --ignore-not-found=true
fi

if [ -n "${GCP_SUBNET_NAME:-}" ]; then
  echo "Patching GCPManagedCluster for manual-mode VPC subnet..."
  "$KUBECTL" patch gcpmanagedcluster "$CLUSTER_NAME" -n "$TARGET_NAMESPACE" --type=merge -p \
    "{\"spec\":{\"network\":{\"name\":\"${GCP_NETWORK_NAME}\",\"autoCreateSubnetworks\":false,\"subnets\":[{\"name\":\"${GCP_SUBNET_NAME}\",\"region\":\"${GCP_REGION}\"}]}}}"
fi

echo "Cluster creation started. Status:"
"$KUBECTL" get cluster,machinepool,gcpmanagedcluster,gcpmanagedcontrolplane,gcpmanagedmachinepool -n "$TARGET_NAMESPACE" -o wide

if [ "$WAIT_FOR_CLUSTER" = "true" ]; then
  "$KUBECTL" wait "cluster/${CLUSTER_NAME}" -n "$TARGET_NAMESPACE" --for=condition=Available=True --timeout="$WAIT_TIMEOUT"
fi

echo "Get kubeconfig:"
echo "  ${KUBECTL} get secret ${CLUSTER_NAME}-user-kubeconfig -n ${TARGET_NAMESPACE} -o jsonpath='{.data.value}' | base64 -d > ${CLUSTER_NAME}.kubeconfig"
