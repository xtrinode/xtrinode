#!/usr/bin/env bash
# Delete a CAPG-managed workload cluster from the management cluster.
set -euo pipefail

TARGET_NAMESPACE="${TARGET_NAMESPACE:-xtrinode-capg-real}"
CLUSTER_NAME="${CLUSTER_NAME:-xtrinode-capg-workload}"
WAIT_FOR_DELETE="${WAIT_FOR_DELETE:-true}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-30m}"
FORCE_FINALIZERS="${FORCE_FINALIZERS:-false}"
KUBECTL="${KUBECTL:-kubectl}"

echo "Deleting CAPG workload cluster..."
echo "  TARGET_NAMESPACE=${TARGET_NAMESPACE}"
echo "  CLUSTER_NAME=${CLUSTER_NAME}"

if ! "$KUBECTL" get "cluster/${CLUSTER_NAME}" -n "${TARGET_NAMESPACE}" >/dev/null 2>&1; then
  echo "Cluster ${TARGET_NAMESPACE}/${CLUSTER_NAME} does not exist; nothing to delete."
  exit 0
fi

"$KUBECTL" delete "cluster/${CLUSTER_NAME}" -n "${TARGET_NAMESPACE}" --wait=false

if [ "${WAIT_FOR_DELETE}" = "true" ]; then
  if "$KUBECTL" wait "cluster/${CLUSTER_NAME}" -n "${TARGET_NAMESPACE}" --for=delete --timeout="${WAIT_TIMEOUT}"; then
    echo "CAPG workload cluster deleted."
    exit 0
  fi

  if [ "${FORCE_FINALIZERS}" != "true" ]; then
    echo "Cluster deletion did not finish within ${WAIT_TIMEOUT}."
    echo "Inspect with: ${KUBECTL} get cluster,machinepool,gcpmanagedcluster,gcpmanagedcontrolplane,gcpmanagedmachinepool -n ${TARGET_NAMESPACE} -o wide"
    echo "If cloud resources are already gone and finalizers are stuck, retry with FORCE_FINALIZERS=true."
    exit 1
  fi
fi

echo "Force-removing finalizers from CAPG resources for ${CLUSTER_NAME}..."
for resource in \
  "cluster/${CLUSTER_NAME}" \
  "gcpmanagedcluster/${CLUSTER_NAME}" \
  "gcpmanagedcontrolplane/${CLUSTER_NAME}-control-plane"; do
  "$KUBECTL" patch "${resource}" -n "${TARGET_NAMESPACE}" --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
done

for kind in machinepool gcpmanagedmachinepool; do
  for name in $("$KUBECTL" get "${kind}" -n "${TARGET_NAMESPACE}" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do
    "$KUBECTL" patch "${kind}/${name}" -n "${TARGET_NAMESPACE}" --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
  done
done

"$KUBECTL" delete "cluster/${CLUSTER_NAME}" -n "${TARGET_NAMESPACE}" --ignore-not-found=true --wait=false
"$KUBECTL" wait "cluster/${CLUSTER_NAME}" -n "${TARGET_NAMESPACE}" --for=delete --timeout="${WAIT_TIMEOUT}" 2>/dev/null || true
echo "CAPG workload cluster delete request completed."
