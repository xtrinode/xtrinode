#!/usr/bin/env bash
# Static edge-case checks for the GCP refresh, teardown, and CAPG smoke scripts.
# This script does not create, delete, or mutate cloud/Kubernetes resources.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECKS=0

pass() {
  CHECKS="$((CHECKS + 1))"
  printf 'ok - %s\n' "$1"
}

fail() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

require_contains() {
  local file="$1"
  local text="$2"
  local message="$3"

  if grep -Fq "$text" "$ROOT_DIR/$file"; then
    pass "$message"
  else
    fail "$message"
  fi
}

require_matches() {
  local file="$1"
  local pattern="$2"
  local message="$3"

  if grep -Eq "$pattern" "$ROOT_DIR/$file"; then
    pass "$message"
  else
    fail "$message"
  fi
}

require_not_contains() {
  local file="$1"
  local text="$2"
  local message="$3"

  if grep -Fq "$text" "$ROOT_DIR/$file"; then
    fail "$message"
  else
    pass "$message"
  fi
}

echo "Checking shell syntax..."
for script in \
  scripts/k8s/cleanup-finalizers.sh \
  scripts/capi/test-xtrinode-nodepool.sh \
  scripts/smoke/gcp-keda-resume-smoke.sh; do
  bash -n "$ROOT_DIR/$script"
  pass "$script parses"
done

echo "Checking teardown safety contracts..."
require_contains "Makefile" "TEARDOWN_FORCE_NAMESPACE_FINALIZERS ?= true" \
  "full GCP teardown opts into force-finalizing stuck namespaces"
require_contains "Makefile" "TEARDOWN_NAMESPACE_WAIT_TIMEOUT ?= 45s" \
  "full GCP teardown uses bounded namespace wait time"
require_contains "Makefile" 'gcp-teardown-prep FORCE_NAMESPACE_FINALIZERS=$(TEARDOWN_FORCE_NAMESPACE_FINALIZERS)' \
  "full GCP teardown passes force-finalizer setting into prep"
require_contains "Makefile" 'WAIT_TIMEOUT=$(TEARDOWN_NAMESPACE_WAIT_TIMEOUT)' \
  "teardown prep passes the bounded wait timeout to cleanup script"
require_contains "Makefile" '$(GCLOUD) container clusters update $(GCP_CLUSTER_NAME) --zone $(GCP_ZONE) --project=$(GCP_PROJECT_ID) --no-deletion-protection' \
  "GKE deletion protection is disabled through gcloud instead of Terraform replacement"
require_not_contains "Makefile" "terraform apply -target=google_container_cluster.xtrinode" \
  "teardown prep does not use targeted Terraform apply for deletion protection"
require_contains "Makefile" '$(GCLOUD) compute addresses delete xtrinode-private-ip-range' \
  "teardown deletes current XTrinode private service range"
require_matches "Makefile" "\\$\\(TERRAFORM\\) state list .*awk '.*/\\^\\(kubernetes_\\|helm_release\\\\\\.\\)/" \
  "teardown removes leftover Kubernetes and Helm resources from Terraform state"

echo "Checking finalizer cleanup edge cases..."
require_contains "scripts/k8s/cleanup-finalizers.sh" 'FORCE_NAMESPACE_FINALIZERS="${FORCE_NAMESPACE_FINALIZERS:-false}"' \
  "cleanup script does not force namespace finalizers by default"
require_contains "scripts/k8s/cleanup-finalizers.sh" 'DELETE_NAMESPACES="${DELETE_NAMESPACES:-true}"' \
  "cleanup script can run in patch-only mode"
require_contains "scripts/k8s/cleanup-finalizers.sh" "xtrinodes.analytics.xtrinode.io" \
  "cleanup handles current XTrinode CRDs"
require_contains "scripts/k8s/cleanup-finalizers.sh" "coreproviders.operator.cluster.x-k8s.io" \
  "cleanup handles CAPI operator provider CRDs"
require_contains "scripts/k8s/cleanup-finalizers.sh" 'items_json="$(kubectl get "$resource" -A -o json 2>/dev/null || true)"' \
  "cleanup tolerates failed resource listing during teardown"
require_contains "scripts/k8s/cleanup-finalizers.sh" 'kubectl wait "namespace/${namespace}" --for=delete --timeout="$WAIT_TIMEOUT" >/dev/null 2>&1 &' \
  "cleanup waits for namespace deletion in parallel"

echo "Checking CAPG nodepool smoke contracts..."
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'WAIT_FOR_WORKLOAD_NODES="${WAIT_FOR_WORKLOAD_NODES:-true}"' \
  "CAPG smoke waits for workload-cluster nodes by default"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'WAIT_FOR_TRINO_ROLLOUT="${WAIT_FOR_TRINO_ROLLOUT:-false}"' \
  "CAPG smoke does not wait for management-cluster Trino rollout by default"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'VERIFY_NODEPOOL_REMOVAL_RETAIN="${VERIFY_NODEPOOL_REMOVAL_RETAIN:-false}"' \
  "CAPG smoke keeps nodepool removal retain verification opt-in"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'XTRINODE_SUSPENDED="${XTRINODE_SUSPENDED:-true}"' \
  "CAPG provisioning-only smoke keeps the runtime suspended by default"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'NODEPOOL_SCALE_DOWN_ON_SUSPEND="${NODEPOOL_SCALE_DOWN_ON_SUSPEND:-false}"' \
  "CAPG provisioning-only smoke keeps the managed node pool provisioned while suspended"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'provisioning-only CAPG smoke found management-cluster Trino pods' \
  "CAPG provisioning-only smoke fails if management-cluster Trino pods appear"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" '${CLUSTER_NAME}-user-kubeconfig' \
  "CAPG smoke reads the generated workload-cluster kubeconfig"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'xtrinode.io/node-pool=${NODEPOOL_NAME}' \
  "CAPG smoke verifies workload nodes by XTrinode nodepool label"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'Timed out waiting for workload nodes labelled xtrinode.io/node-pool=${NODEPOOL_NAME}' \
  "CAPG smoke handles the zero-matching-node wait edge case"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'NodePoolRetained' \
  "CAPG smoke can verify spec.nodePool removal retention"
require_contains "scripts/capi/test-xtrinode-nodepool.sh" 'Retained MachinePool and GCPManagedMachinePool no longer have XTrinode owner references.' \
  "CAPG retain smoke checks provider resources were retained without XTrinode owners"
require_contains "Makefile" "gcp-capg-nodepool-retain-smoke" \
  "Makefile exposes the CAPG nodepool retain smoke"

printf 'Completed %s edge-case checks.\n' "$CHECKS"
