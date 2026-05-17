#!/usr/bin/env bash
# Bootstrap CAPI/CAPG into the Terraform-created GKE management cluster.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TF_DIR="$REPO_ROOT/terraform/gcp"

TF_TARGETS=(
  "-target=google_service_account.capg"
  "-target=google_project_iam_member.capg"
  "-target=google_service_account_key.capg"
  "-target=kubernetes_namespace.capg_operator"
  "-target=kubernetes_namespace.capi_core"
  "-target=kubernetes_namespace.capi_bootstrap"
  "-target=kubernetes_namespace.capi_control_plane"
  "-target=kubernetes_namespace.capg"
  "-target=kubernetes_secret.capg_gcp_credentials"
  "-target=helm_release.cert_manager"
  "-target=helm_release.capi_operator"
)

terraform -chdir="$TF_DIR" apply -var="capg_enabled=true" "${TF_TARGETS[@]}" "$@"

CONFIGURE_KUBECTL="$(terraform -chdir="$TF_DIR" output -raw configure_kubectl)"
echo "Configuring kubectl for the management cluster..."
$CONFIGURE_KUBECTL

echo "CAPG bootstrap requested. Verify with:"
echo "  kubectl get pods -n capi-operator-system"
echo "  kubectl get pods -n capi-system"
echo "  kubectl get pods -n capg-system"
