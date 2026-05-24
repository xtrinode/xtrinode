#!/usr/bin/env bash
#
# XTrinode Azure Deployment Script
# ================================
# End-to-end: provision Azure infrastructure with Terraform, build Docker
# images locally, push to ACR, and deploy XTrinode to AKS with Helm.
#
# Prerequisites: see docs/TOOLING.md for versions.
#   - Azure CLI authenticated (`az login`)
#   - Docker, Terraform, Helm, kubectl, and openssl
#
# Usage:
#   ./scripts/deploy-azure.sh                  # Full deploy
#   ./scripts/deploy-azure.sh --skip-terraform # Build + push + helm only
#   ./scripts/deploy-azure.sh --skip-build     # Terraform + helm only
#   ./scripts/deploy-azure.sh --destroy        # Tear everything down
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TF_DIR="$ROOT_DIR/terraform/azure"
OPERATOR_DIR="$ROOT_DIR/xtrinode"
HELM_DIR="$ROOT_DIR/helm"
DOCKERFILE="$ROOT_DIR/docker/Dockerfile"
DOCKER="${DOCKER:-docker}"
TERRAFORM="${TERRAFORM:-terraform}"
HELM="${HELM:-helm}"
KUBECTL="${KUBECTL:-kubectl}"
MAKE="${MAKE:-make}"
AZ="${AZ:-az}"
OPENSSL="${OPENSSL:-openssl}"

DEFAULT_IMAGE_VERSION="$(
  awk '/^appVersion:/ {print $2; exit}' "$ROOT_DIR/helm/xtrinode/Chart.yaml" 2>/dev/null |
    tr -d '"' || true
)"
VERSION="${VERSION:-${DEFAULT_IMAGE_VERSION:-0.1.0}}"

AZURE_SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID:-${ARM_SUBSCRIPTION_ID:-}}"
AZURE_REGION="${AZURE_REGION:-eastus}"
RESOURCE_GROUP_NAME="${RESOURCE_GROUP_NAME:-xtrinode-rg}"
CLUSTER_NAME="${CLUSTER_NAME:-xtrinode-aks-test}"
ENVIRONMENT="${ENVIRONMENT:-testing}"
POSTGRES_PASSWORD="${TF_VAR_postgres_admin_password:-}"
POSTGRES_ENABLED="${POSTGRES_ENABLED:-${TF_VAR_postgres_enabled:-}}"
ACR_LOGIN_SERVER="${ACR_LOGIN_SERVER:-}"

OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
OBSERVABILITY_NAMESPACE="${OBSERVABILITY_NAMESPACE:-monitoring}"
PROMETHEUS_ENABLED="${PROMETHEUS_ENABLED:-false}"
PROMETHEUS_STORAGE_CLASS="${PROMETHEUS_STORAGE_CLASS:-managed-premium}"
VECTOR_ENABLED="${VECTOR_ENABLED:-false}"
VECTOR_NAMESPACE="${VECTOR_NAMESPACE:-observability}"
VECTOR_LOG_LEVEL="${VECTOR_LOG_LEVEL:-info}"
WEBHOOK_ENABLED="${WEBHOOK_ENABLED:-true}"
GATEWAY_REPLICA_COUNT="${GATEWAY_REPLICA_COUNT:-1}"
GATEWAY_REDIS_ENABLED="${GATEWAY_REDIS_ENABLED:-false}"
GATEWAY_IN_CHART_REDIS_ENABLED="${GATEWAY_IN_CHART_REDIS_ENABLED:-$GATEWAY_REDIS_ENABLED}"
GATEWAY_AUTH_ENABLED="${GATEWAY_AUTH_ENABLED:-true}"
GATEWAY_AUTH_SECRET="${GATEWAY_AUTH_SECRET:-trino-gateway-api-keys}"
GATEWAY_AUTH_SECRET_KEY="${GATEWAY_AUTH_SECRET_KEY:-api-keys}"
GATEWAY_API_KEY_ID="${GATEWAY_API_KEY_ID:-default}"
GATEWAY_API_KEY="${GATEWAY_API_KEY:-}"
API_SERVER_AUTH_SECRET="${API_SERVER_AUTH_SECRET:-xtrinode-api-server-auth}"
API_SERVER_AUTH_TOKEN="${API_SERVER_AUTH_TOKEN:-}"
API_SERVER_RESUME_AUTH_SECRET="${API_SERVER_RESUME_AUTH_SECRET:-xtrinode-api-server-resume-auth}"
API_SERVER_RESUME_AUTH_TOKEN="${API_SERVER_RESUME_AUTH_TOKEN:-}"
PROMETHEUS_URL="http://prometheus-operated.${OBSERVABILITY_NAMESPACE}.svc.cluster.local:9090"

SKIP_TERRAFORM=false
SKIP_BUILD=false
DESTROY=false

for arg in "$@"; do
  case "$arg" in
    --skip-terraform) SKIP_TERRAFORM=true ;;
    --skip-build)     SKIP_BUILD=true ;;
    --destroy)        DESTROY=true ;;
    --help|-h)
      awk 'NR == 1 {next} /^#/ {sub(/^# ?/, ""); print; next} {exit}' "$0"
      exit 0
      ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

info() { printf '\n==> %s\n' "$*"; }
ok() { printf 'OK: %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

random_token() {
  "$OPENSSL" rand -hex 32
}

postgres_enabled() {
  case "${POSTGRES_ENABLED:-}" in
    true|1) return 0 ;;
    false|0) return 1 ;;
  esac

  [ -f "$TF_DIR/terraform.tfvars" ] &&
    grep -Eq '^[[:space:]]*postgres_enabled[[:space:]]*=[[:space:]]*true([[:space:]#]|$)' "$TF_DIR/terraform.tfvars"
}

terraform_vars=()
append_terraform_vars() {
  terraform_vars=(
    -var "azure_subscription_id=${AZURE_SUBSCRIPTION_ID}"
    -var "azure_region=${AZURE_REGION}"
    -var "resource_group_name=${RESOURCE_GROUP_NAME}"
    -var "cluster_name=${CLUSTER_NAME}"
  )
  if [ -n "${POSTGRES_PASSWORD:-}" ]; then
    terraform_vars+=(-var "postgres_admin_password=${POSTGRES_PASSWORD}")
  fi
  if [ -n "${POSTGRES_ENABLED:-}" ]; then
    terraform_vars+=(-var "postgres_enabled=${POSTGRES_ENABLED}")
  fi
}

terraform_output_raw() {
  local output_name="$1"
  "$TERRAFORM" -chdir="$TF_DIR" output -raw "$output_name" 2>/dev/null || true
}

resolve_acr_login_server() {
  if [ -n "$ACR_LOGIN_SERVER" ]; then
    printf '%s\n' "$ACR_LOGIN_SERVER"
    return
  fi

  local tf_login_server
  tf_login_server="$(terraform_output_raw acr_login_server)"
  if [ -n "$tf_login_server" ]; then
    printf '%s\n' "$tf_login_server"
    return
  fi

  local discovered
  discovered="$("$AZ" acr list --resource-group "$RESOURCE_GROUP_NAME" --query '[0].loginServer' -o tsv 2>/dev/null || true)"
  if [ -n "$discovered" ]; then
    printf '%s\n' "$discovered"
    return
  fi

  fail "Could not determine ACR login server. Set ACR_LOGIN_SERVER or run Terraform first."
}

if [ -n "$AZURE_SUBSCRIPTION_ID" ]; then
  "$AZ" account set --subscription "$AZURE_SUBSCRIPTION_ID"
fi

AZURE_SUBSCRIPTION_ID="$("$AZ" account show --query id -o tsv 2>/dev/null)" \
  || fail "Azure CLI is not authenticated. Run: az login"
[ -n "$AZURE_SUBSCRIPTION_ID" ] || fail "Azure subscription ID is empty."
ok "Azure subscription: $AZURE_SUBSCRIPTION_ID  Region: $AZURE_REGION"
ok "XTrinode image tag: $VERSION"

append_terraform_vars

if [ "$DESTROY" = true ]; then
  info "Destroying all Azure resources"
  warn "This will delete the AKS cluster, ACR, optional PostgreSQL, and all data."
  read -rp "Type 'yes' to confirm: " confirm
  [ "$confirm" = "yes" ] || { echo "Aborted."; exit 0; }

  info "Removing Helm releases"
  "$AZ" aks get-credentials \
    --resource-group "$RESOURCE_GROUP_NAME" \
    --name "$CLUSTER_NAME" \
    --overwrite-existing 2>/dev/null || true
  "$HELM" uninstall xtrinode-gateway -n "$GATEWAY_NAMESPACE" 2>/dev/null || true
  "$HELM" uninstall xtrinode-api-server -n "$OPERATOR_NAMESPACE" 2>/dev/null || true
  "$HELM" uninstall xtrinode-operator -n "$OPERATOR_NAMESPACE" 2>/dev/null || true

  info "Running terraform destroy"
  cd "$TF_DIR"
  "$TERRAFORM" destroy -auto-approve "${terraform_vars[@]}" \
    -var "postgres_admin_password=${POSTGRES_PASSWORD:-dummy}"
  ok "All Azure resources destroyed"
  exit 0
fi

if [ "$SKIP_TERRAFORM" = false ]; then
  info "Step 1/5: Provisioning Azure infrastructure with Terraform"

  if postgres_enabled && [ -z "$POSTGRES_PASSWORD" ]; then
    read -rsp "Enter PostgreSQL admin password (TF_VAR_postgres_admin_password): " POSTGRES_PASSWORD
    echo
    [ -n "$POSTGRES_PASSWORD" ] || fail "PostgreSQL password is required when postgres_enabled=true."
    export TF_VAR_postgres_admin_password="$POSTGRES_PASSWORD"
    append_terraform_vars
  fi

  cd "$TF_DIR"
  "$TERRAFORM" init -upgrade
  "$TERRAFORM" plan "${terraform_vars[@]}" -out=tfplan
  "$TERRAFORM" apply tfplan
  ok "Terraform apply complete"
else
  info "Step 1/5: Skipping Terraform (--skip-terraform)"
fi

info "Step 2/5: Configuring kubectl for AKS"
"$AZ" aks get-credentials \
  --resource-group "$RESOURCE_GROUP_NAME" \
  --name "$CLUSTER_NAME" \
  --overwrite-existing

"$KUBECTL" cluster-info >/dev/null 2>&1 \
  || fail "Cannot connect to AKS. For private clusters, run this from the VNet, Azure Cloud Shell command invoke, VPN, or bastion."
ok "kubectl configured; cluster reachable"

ACR_LOGIN_SERVER="$(resolve_acr_login_server)"
ACR_NAME="${ACR_LOGIN_SERVER%%.azurecr.io}"
[ -n "$ACR_NAME" ] || fail "Could not derive ACR name from login server: $ACR_LOGIN_SERVER"
ok "ACR: $ACR_LOGIN_SERVER"

COMPONENTS=(
  "operator:./cmd/operator:8081"
  "api-server:./cmd/api-server:8081"
  "gateway:./cmd/gateway:8080"
)

if [ "$SKIP_BUILD" = false ]; then
  info "Step 3/5: Building Docker images locally"
  GIT_COMMIT=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")
  BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  for comp in "${COMPONENTS[@]}"; do
    IFS=':' read -r name app_package app_port <<< "$comp"
    image_name="xtrinode-${name}"
    tag="${ACR_LOGIN_SERVER}/${image_name}:${VERSION}"
    info "Building ${image_name}"
    "$DOCKER" build \
      --build-arg APP_PACKAGE="$app_package" \
      --build-arg APP_PORT="$app_port" \
      --build-arg VERSION="$VERSION" \
      --build-arg GIT_COMMIT="$GIT_COMMIT" \
      --build-arg BUILD_DATE="$BUILD_DATE" \
      -t "$tag" \
      -t "${ACR_LOGIN_SERVER}/${image_name}:latest" \
      -f "$DOCKERFILE" \
      "$OPERATOR_DIR"
  done

  info "Step 4/5: Pushing images to ACR"
  "$AZ" acr login --name "$ACR_NAME"

  for comp in "${COMPONENTS[@]}"; do
    IFS=':' read -r name _ <<< "$comp"
    image_name="xtrinode-${name}"
    "$DOCKER" push "${ACR_LOGIN_SERVER}/${image_name}:${VERSION}"
    "$DOCKER" push "${ACR_LOGIN_SERVER}/${image_name}:latest"
  done
else
  info "Step 3/5: Skipping Docker build (--skip-build)"
  info "Step 4/5: Skipping Docker push (--skip-build)"
fi

info "Step 5/5: Deploying to AKS via Helm"
"$KUBECTL" create namespace "$OPERATOR_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace "$GATEWAY_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace team-test --dry-run=client -o yaml | "$KUBECTL" apply -f -

if [ -z "$API_SERVER_AUTH_TOKEN" ]; then
  API_SERVER_AUTH_TOKEN="$(random_token)"
fi
if [ -z "$API_SERVER_RESUME_AUTH_TOKEN" ]; then
  API_SERVER_RESUME_AUTH_TOKEN="$(random_token)"
fi

auth_token_file="$(mktemp)"
resume_auth_token_file="$(mktemp)"
gateway_auth_keys_file="$(mktemp)"
trap 'rm -f "$auth_token_file" "$resume_auth_token_file" "$gateway_auth_keys_file"' EXIT
printf '%s' "$API_SERVER_AUTH_TOKEN" > "$auth_token_file"
printf '%s' "$API_SERVER_RESUME_AUTH_TOKEN" > "$resume_auth_token_file"
"$KUBECTL" create secret generic "$API_SERVER_AUTH_SECRET" \
  --namespace "$OPERATOR_NAMESPACE" \
  --from-file=token="$auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create secret generic "$API_SERVER_RESUME_AUTH_SECRET" \
  --namespace "$OPERATOR_NAMESPACE" \
  --from-file=token="$resume_auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create secret generic "$API_SERVER_RESUME_AUTH_SECRET" \
  --namespace "$GATEWAY_NAMESPACE" \
  --from-file=token="$resume_auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -

if [ "$GATEWAY_AUTH_ENABLED" = "true" ]; then
  existing_gateway_keys="$(
    "$KUBECTL" get secret "$GATEWAY_AUTH_SECRET" \
      --namespace "$GATEWAY_NAMESPACE" \
      -o "go-template={{ index .data \"$GATEWAY_AUTH_SECRET_KEY\" }}" 2>/dev/null |
      base64 -d 2>/dev/null || true
  )"
  if [ -n "$GATEWAY_API_KEY" ]; then
    printf '%s: "%s"\n' "$GATEWAY_API_KEY_ID" "$GATEWAY_API_KEY" > "$gateway_auth_keys_file"
  elif [ -n "$existing_gateway_keys" ]; then
    printf '%s' "$existing_gateway_keys" > "$gateway_auth_keys_file"
  else
    printf '%s: "%s"\n' "$GATEWAY_API_KEY_ID" "$(random_token)" > "$gateway_auth_keys_file"
  fi
  "$KUBECTL" create secret generic "$GATEWAY_AUTH_SECRET" \
    --namespace "$GATEWAY_NAMESPACE" \
    --from-file="$GATEWAY_AUTH_SECRET_KEY=$gateway_auth_keys_file" \
    --dry-run=client -o yaml | "$KUBECTL" apply -f -
fi

info "Generating manifests (CRDs)"
cd "$ROOT_DIR" && "$MAKE" manifests
"$KUBECTL" apply -f "$HELM_DIR/xtrinode-operator/crds"

info "Building Helm dependencies"
"$HELM" dependency build "$HELM_DIR/xtrinode-operator"
"$HELM" dependency build "$HELM_DIR/xtrinode-observability"

if [ "$PROMETHEUS_ENABLED" = "true" ] || [ "$VECTOR_ENABLED" = "true" ]; then
  info "Deploying xtrinode-observability"
  "$HELM" upgrade --install xtrinode-observability "$HELM_DIR/xtrinode-observability" \
    --namespace "$OBSERVABILITY_NAMESPACE" \
    --create-namespace \
    --set prometheus-stack.enabled="$PROMETHEUS_ENABLED" \
    --set prometheus-stack.defaultRules.create=false \
    --set prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.storageClassName="$PROMETHEUS_STORAGE_CLASS" \
    --set prometheus-stack.alertmanager.enabled=false \
    --set prometheus-stack.nodeExporter.enabled=false \
    --set prometheus-stack.kubeStateMetrics.enabled=false \
    --set prometheus-stack.kubeApiServer.enabled=false \
    --set prometheus-stack.kubelet.enabled=false \
    --set prometheus-stack.kubeControllerManager.enabled=false \
    --set prometheus-stack.coreDns.enabled=false \
    --set prometheus-stack.kubeDns.enabled=false \
    --set prometheus-stack.kubeEtcd.enabled=false \
    --set prometheus-stack.kubeScheduler.enabled=false \
    --set prometheus-stack.kubeProxy.enabled=false \
    --set prometheus-stack.grafana.enabled="${GRAFANA_ENABLED:-false}" \
    --set vector.enabled="$VECTOR_ENABLED" \
    --set vector.namespaceOverride="$VECTOR_NAMESPACE" \
    --set vector.clusterName="$CLUSTER_NAME" \
    --set vector.environment="$ENVIRONMENT" \
    --set vector.region="$AZURE_REGION" \
    --set vector.logLevel="$VECTOR_LOG_LEVEL" \
    --set vector.serviceMonitor.enabled="$PROMETHEUS_ENABLED" \
    --wait --timeout 10m
fi

info "Deploying xtrinode-operator"
"$HELM" upgrade --install xtrinode-operator "$HELM_DIR/xtrinode-operator" \
  --take-ownership \
  --namespace "$OPERATOR_NAMESPACE" \
  --set image.repository="${ACR_LOGIN_SERVER}/xtrinode-operator" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set keda.enabled=true \
  --set webhook.enabled="$WEBHOOK_ENABLED" \
  --set serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --set operator.prometheus.address="$PROMETHEUS_URL" \
  --set operator.capiProvider=azure \
  --wait --timeout 5m

info "Deploying xtrinode-api-server"
"$HELM" upgrade --install xtrinode-api-server "$HELM_DIR/xtrinode-api-server" \
  --take-ownership \
  --namespace "$OPERATOR_NAMESPACE" \
  --set image.repository="${ACR_LOGIN_SERVER}/xtrinode-api-server" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set apiServer.auth.enabled=true \
  --set apiServer.auth.existingSecret="$API_SERVER_AUTH_SECRET" \
  --set apiServer.auth.resume.enabled=true \
  --set apiServer.auth.resume.existingSecret="$API_SERVER_RESUME_AUTH_SECRET" \
  --set metrics.serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --wait --timeout 5m

info "Deploying xtrinode-gateway"
"$HELM" upgrade --install xtrinode-gateway "$HELM_DIR/xtrinode-gateway" \
  --force \
  --namespace "$GATEWAY_NAMESPACE" \
  --set image.repository="${ACR_LOGIN_SERVER}/xtrinode-gateway" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set replicaCount="$GATEWAY_REPLICA_COUNT" \
  --set gateway.redis.enabled="$GATEWAY_REDIS_ENABLED" \
  --set redis.enabled="$GATEWAY_IN_CHART_REDIS_ENABLED" \
  --set serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --set gateway.apiServerAuth.enabled=true \
  --set gateway.apiServerAuth.existingSecret="$API_SERVER_RESUME_AUTH_SECRET" \
  --set gateway.auth.enabled="$GATEWAY_AUTH_ENABLED" \
  --set gateway.auth.type=api-key \
  --set gateway.auth.apiKey.secretName="$GATEWAY_AUTH_SECRET" \
  --set gateway.auth.apiKey.secretKey="$GATEWAY_AUTH_SECRET_KEY" \
  --set gateway.auth.apiKey.namespace="$GATEWAY_NAMESPACE" \
  --set gateway.apiServerURL="http://xtrinode-api-server.${OPERATOR_NAMESPACE}.svc.cluster.local:8081/api/v1" \
  --wait --timeout 5m

cat <<EOF

============================================================
  XTrinode Azure Deployment Complete
============================================================

  Cluster:    $CLUSTER_NAME
  Region:     $AZURE_REGION
  Resource group: $RESOURCE_GROUP_NAME
  Registry:   $ACR_LOGIN_SERVER
  Image tag:  $VERSION

  Namespaces:
    Operator + API Server:  $OPERATOR_NAMESPACE
    Gateway:                $GATEWAY_NAMESPACE

  Check:
    kubectl get pods -n $OPERATOR_NAMESPACE
    kubectl get pods -n $GATEWAY_NAMESPACE

EOF
