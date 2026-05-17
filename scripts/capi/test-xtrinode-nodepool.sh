#!/usr/bin/env bash
# Create a real XTrinode-managed GCPManagedMachinePool against a CAPG GKE cluster.
set -euo pipefail

TARGET_NAMESPACE="${TARGET_NAMESPACE:-xtrinode-capg-real}"
CLUSTER_NAME="${CLUSTER_NAME:-xtrinode-capg-workload}"
XTRINODE_NAME="${XTRINODE_NAME:-capg-nodepool}"
NODEPOOL_NAME="${NODEPOOL_NAME:-np-capg-real}"
GCP_ZONE="${GCP_ZONE:-us-central1-a}"
GCP_MACHINE_TYPE="${GCP_MACHINE_TYPE:-e2-standard-2}"
GCP_DISK_TYPE="${GCP_DISK_TYPE:-pd-standard}"
MIN_NODES="${MIN_NODES:-1}"
MAX_NODES="${MAX_NODES:-1}"
TRINO_WORKERS="${TRINO_WORKERS:-1}"
AUTO_SUSPEND_AFTER="${AUTO_SUSPEND_AFTER:-1h}"
WAIT_FOR_NODEPOOL="${WAIT_FOR_NODEPOOL:-true}"
WAIT_FOR_WORKLOAD_NODES="${WAIT_FOR_WORKLOAD_NODES:-true}"
WAIT_FOR_TRINO_ROLLOUT="${WAIT_FOR_TRINO_ROLLOUT:-false}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-30m}"

if [ "$WAIT_FOR_TRINO_ROLLOUT" = "true" ]; then
  XTRINODE_SUSPENDED="${XTRINODE_SUSPENDED:-false}"
else
  XTRINODE_SUSPENDED="${XTRINODE_SUSPENDED:-true}"
fi
NODEPOOL_SCALE_DOWN_ON_SUSPEND="${NODEPOOL_SCALE_DOWN_ON_SUSPEND:-false}"

if [ "$WAIT_FOR_TRINO_ROLLOUT" = "true" ] && [ "$XTRINODE_SUSPENDED" = "true" ]; then
  echo "ERROR: WAIT_FOR_TRINO_ROLLOUT=true requires XTRINODE_SUSPENDED=false" >&2
  exit 1
fi

duration_to_seconds() {
  case "$1" in
    *s) echo "$(( ${1%s} ))" ;;
    *m) echo "$(( ${1%m} * 60 ))" ;;
    *h) echo "$(( ${1%h} * 3600 ))" ;;
    *) echo "$1" ;;
  esac
}

current_workload_node_readiness() {
  local kubeconfig="$1"
  local nodepool="$2"

  kubectl --kubeconfig="$kubeconfig" get nodes \
    -l "xtrinode.io/node-pool=${nodepool}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{end}{"\n"}{end}' \
    2>/dev/null || true
}

workload_nodepool_converged() {
  local kubeconfig="$1"
  local nodepool="$2"
  local min_nodes="$3"
  local max_nodes="$4"
  local readiness
  local node_count

  readiness="$(current_workload_node_readiness "$kubeconfig" "$nodepool")"
  if [ -z "$readiness" ]; then
    return 1
  fi

  printf '%s\n' "$readiness"
  node_count="$(printf '%s\n' "$readiness" | awk 'NF { count++ } END { print count + 0 }')"
  if [ "$node_count" -lt "$min_nodes" ] || [ "$node_count" -gt "$max_nodes" ]; then
    return 1
  fi

  printf '%s\n' "$readiness" | awk 'NF && $2 != "True" { bad = 1 } END { exit bad }'
}

CONTROL_PLANE_VERSION="$(
  kubectl get gcpmanagedcontrolplane "${CLUSTER_NAME}-control-plane" -n "$TARGET_NAMESPACE" \
    -o jsonpath='{.status.version}' 2>/dev/null || true
)"
KUBERNETES_VERSION="${KUBERNETES_VERSION:-}"
if [ -z "$KUBERNETES_VERSION" ] && [ -n "$CONTROL_PLANE_VERSION" ]; then
  KUBERNETES_VERSION="v${CONTROL_PLANE_VERSION#v}"
fi
KUBERNETES_VERSION="${KUBERNETES_VERSION:-v1.35.3}"

kubectl get "cluster/${CLUSTER_NAME}" -n "$TARGET_NAMESPACE" >/dev/null

cat <<EOF | kubectl apply -f -
apiVersion: analytics.xtrinode.io/v1
kind: XTrinode
metadata:
  name: ${XTRINODE_NAME}
  namespace: ${TARGET_NAMESPACE}
  labels:
    xtrinode: ${XTRINODE_NAME}
    team: xtrinode
    environment: capg-smoke
spec:
  size: xs
  maxWorkers: ${TRINO_WORKERS}
  minWorkers: 0
  suspended: ${XTRINODE_SUSPENDED}
  autoSuspendAfter: "${AUTO_SUSPEND_AFTER}"
  nodePool:
    name: ${NODEPOOL_NAME}
    provider: gcp
    providerMode: managed
    clusterName: ${CLUSTER_NAME}
    kubernetesVersion: ${KUBERNETES_VERSION}
    scaleDownOnSuspend: ${NODEPOOL_SCALE_DOWN_ON_SUSPEND}
    minNodes: ${MIN_NODES}
    maxNodes: ${MAX_NODES}
    zones:
      - ${GCP_ZONE}
    gcp:
      machineType: ${GCP_MACHINE_TYPE}
      diskType: ${GCP_DISK_TYPE}
    nodeLabels:
      xtrinode.io/runtime: ${XTRINODE_NAME}
      xtrinode.io/node-pool: ${NODEPOOL_NAME}
    nodeTaints:
      - key: xtrinode.io/runtime
        value: ${XTRINODE_NAME}
        effect: NoSchedule
    resourceTags:
      xtrinode-runtime: ${XTRINODE_NAME}
      xtrinode-smoke: "true"
  routing:
    header: "X-Trino-XTrinode=${XTRINODE_NAME}"
    hostname: "${XTRINODE_NAME}.trino.local"
  keda:
    enabled: false
  valuesOverlay:
    server:
      workers: ${TRINO_WORKERS}
EOF

kubectl get xtrinode "$XTRINODE_NAME" -n "$TARGET_NAMESPACE" -o wide
kubectl get machinepool,gcpmanagedmachinepool -n "$TARGET_NAMESPACE" -o wide

if [ "$WAIT_FOR_NODEPOOL" = "true" ]; then
  kubectl wait "gcpmanagedmachinepool/${NODEPOOL_NAME}" -n "$TARGET_NAMESPACE" --for=jsonpath='{.status.ready}'=true --timeout="$WAIT_TIMEOUT"
  if [ "$WAIT_FOR_TRINO_ROLLOUT" = "true" ]; then
    kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$TARGET_NAMESPACE" --for=condition=Ready=True --timeout="$WAIT_TIMEOUT"
  else
    kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$TARGET_NAMESPACE" --for=jsonpath='{.status.conditions[?(@.type=="NodePoolReady")].status}'=True --timeout="$WAIT_TIMEOUT"
  fi

  if [ "$WAIT_FOR_WORKLOAD_NODES" = "true" ]; then
    workload_kubeconfig="$(mktemp)"
    trap 'rm -f "$workload_kubeconfig"' EXIT
    kubectl get secret "${CLUSTER_NAME}-user-kubeconfig" -n "$TARGET_NAMESPACE" \
      -o jsonpath='{.data.value}' | base64 -d > "$workload_kubeconfig"
    wait_seconds="$(duration_to_seconds "$WAIT_TIMEOUT")"
    deadline="$(( SECONDS + wait_seconds ))"
    until kubectl --kubeconfig="$workload_kubeconfig" get nodes \
      -l "xtrinode.io/node-pool=${NODEPOOL_NAME}" \
      -o name 2>/dev/null | grep -q .; do
      if [ "$SECONDS" -ge "$deadline" ]; then
        echo "Timed out waiting for workload nodes labelled xtrinode.io/node-pool=${NODEPOOL_NAME}"
        exit 1
      fi
      sleep 10
    done
    expected_min_nodes="$MIN_NODES"
    if [ "$expected_min_nodes" -lt 1 ]; then
      expected_min_nodes=1
    fi
    expected_max_nodes="$MAX_NODES"
    if [ "$expected_max_nodes" -lt "$expected_min_nodes" ]; then
      expected_max_nodes="$expected_min_nodes"
    fi
    until workload_nodepool_converged "$workload_kubeconfig" "$NODEPOOL_NAME" "$expected_min_nodes" "$expected_max_nodes"; do
      if [ "$SECONDS" -ge "$deadline" ]; then
        echo "Timed out waiting for ${expected_min_nodes}-${expected_max_nodes} current workload nodes labelled xtrinode.io/node-pool=${NODEPOOL_NAME} to be Ready"
        exit 1
      fi
      sleep 10
    done
    kubectl --kubeconfig="$workload_kubeconfig" get nodes \
      -l "xtrinode.io/node-pool=${NODEPOOL_NAME}" \
      -L xtrinode.io/node-pool,xtrinode.io/runtime
  fi

  if [ "$WAIT_FOR_TRINO_ROLLOUT" = "true" ]; then
    kubectl rollout status "deployment/trino-${XTRINODE_NAME}-coordinator" -n "$TARGET_NAMESPACE" --timeout="$WAIT_TIMEOUT"
    if [ "$TRINO_WORKERS" -gt 0 ]; then
      kubectl rollout status "deployment/trino-${XTRINODE_NAME}-worker" -n "$TARGET_NAMESPACE" --timeout="$WAIT_TIMEOUT"
    fi
  else
    if kubectl get pods -n "$TARGET_NAMESPACE" \
      -l "app.kubernetes.io/instance=${XTRINODE_NAME},app.kubernetes.io/name=trino" \
      -o name 2>/dev/null | grep -q .; then
      echo "ERROR: provisioning-only CAPG smoke found management-cluster Trino pods for ${XTRINODE_NAME}" >&2
      kubectl get pods -n "$TARGET_NAMESPACE" \
        -l "app.kubernetes.io/instance=${XTRINODE_NAME},app.kubernetes.io/name=trino" \
        -o wide
      exit 1
    fi
    echo "Skipping Trino rollout wait; CAPG managed node pools are created in the workload cluster and the smoke runtime remains suspended."
  fi
fi

kubectl get xtrinode "$XTRINODE_NAME" -n "$TARGET_NAMESPACE" -o wide
if [ "$WAIT_FOR_TRINO_ROLLOUT" = "true" ]; then
  kubectl get deployment "trino-${XTRINODE_NAME}-coordinator" "trino-${XTRINODE_NAME}-worker" -n "$TARGET_NAMESPACE" -o wide
else
  kubectl get pods -n "$TARGET_NAMESPACE" \
    -l "app.kubernetes.io/instance=${XTRINODE_NAME},app.kubernetes.io/name=trino" \
    -o wide
fi
kubectl get machinepool,gcpmanagedmachinepool -n "$TARGET_NAMESPACE" -o wide
