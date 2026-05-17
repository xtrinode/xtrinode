#!/usr/bin/env bash
# Best-effort cleanup for XTrinode/CAPI/KEDA resources before deleting namespaces.
set -euo pipefail

NAMESPACES="${NAMESPACES:-xtrinode-system team-test xtrinode-gateway xtrinode-capg-real gke-managed-networking-dra-driver}"
DELETE_NAMESPACES="${DELETE_NAMESPACES:-true}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-5m}"
FORCE_NAMESPACE_FINALIZERS="${FORCE_NAMESPACE_FINALIZERS:-false}"

patch_all_finalizers() {
  local resource="$1"
  local items_json

  if ! kubectl api-resources --verbs=list --namespaced=true -o name 2>/dev/null | grep -qx "$resource"; then
    return 0
  fi

  items_json="$(kubectl get "$resource" -A -o json 2>/dev/null || true)"
  [ -n "$items_json" ] || return 0

  printf '%s\n' "$items_json" |
    jq -r '.items[]? | [.metadata.namespace, .metadata.name] | @tsv' 2>/dev/null |
    while IFS=$'\t' read -r namespace name; do
      [ -n "$namespace" ] && [ -n "$name" ] || continue
      kubectl patch "$resource/$name" -n "$namespace" --type=merge \
        -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || true
    done || true
}

finalize_namespace() {
  local namespace="$1"

  if ! kubectl get namespace "$namespace" >/dev/null 2>&1; then
    return 0
  fi

  echo "Force-finalizing namespace ${namespace}"
  kubectl get namespace "$namespace" -o json |
    jq '.spec.finalizers=[] | .metadata.finalizers=[]' |
    kubectl replace --raw "/api/v1/namespaces/${namespace}/finalize" -f - >/dev/null 2>&1 || true
}

echo "Cleaning resource finalizers..."
for resource in \
  xtrinodes.analytics.xtrinode.io \
  xtrinodecatalogs.analytics.xtrinode.io \
  scaledobjects.keda.sh \
  triggerauthentications.keda.sh \
  scaledjobs.keda.sh \
  coreproviders.operator.cluster.x-k8s.io \
  bootstrapproviders.operator.cluster.x-k8s.io \
  controlplaneproviders.operator.cluster.x-k8s.io \
  infrastructureproviders.operator.cluster.x-k8s.io \
  addonproviders.operator.cluster.x-k8s.io \
  clusters.cluster.x-k8s.io \
  machinepools.cluster.x-k8s.io \
  machinedeployments.cluster.x-k8s.io \
  machines.cluster.x-k8s.io \
  gcpmanagedclusters.infrastructure.cluster.x-k8s.io \
  gcpmanagedcontrolplanes.infrastructure.cluster.x-k8s.io \
  gcpmanagedmachinepools.infrastructure.cluster.x-k8s.io; do
  patch_all_finalizers "$resource"
done

if [ "$DELETE_NAMESPACES" != "true" ]; then
  exit 0
fi

echo "Deleting namespaces: ${NAMESPACES}"
for namespace in $NAMESPACES; do
  kubectl delete namespace "$namespace" --wait=false >/dev/null 2>&1 || true
done

failed=""
wait_namespaces=()
wait_pids=()
for namespace in $NAMESPACES; do
  if kubectl get namespace "$namespace" >/dev/null 2>&1; then
    kubectl wait "namespace/${namespace}" --for=delete --timeout="$WAIT_TIMEOUT" >/dev/null 2>&1 &
    wait_namespaces+=("$namespace")
    wait_pids+=("$!")
  fi
done

for i in "${!wait_pids[@]}"; do
  if ! wait "${wait_pids[$i]}"; then
    failed="${failed} ${wait_namespaces[$i]}"
  fi
done

if [ -z "$failed" ]; then
  echo "Namespace cleanup completed."
  exit 0
fi

if [ "$FORCE_NAMESPACE_FINALIZERS" != "true" ]; then
  echo "Namespaces still terminating:${failed}"
  echo "Retry with FORCE_NAMESPACE_FINALIZERS=true only after confirming cloud resources are gone."
  exit 1
fi

for namespace in $failed; do
  finalize_namespace "$namespace"
done

echo "Namespace force-finalize requested."
