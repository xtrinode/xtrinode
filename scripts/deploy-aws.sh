#!/usr/bin/env bash
#
# XTrinode AWS Deployment Script
# =============================
# End-to-end: build Docker images locally, push to ECR, provision infra via
# Terraform, deploy to EKS via Helm.
#
# Prerequisites: see docs/TOOLING.md for versions.
#   - AWS CLI v2 configured with an admin/root-equivalent profile
#   - Docker, Terraform, Helm, kubectl, curl, and openssl
#
# Usage:
#   ./scripts/deploy-aws.sh                  # Full deploy (infra + build + helm)
#   ./scripts/deploy-aws.sh --skip-terraform # Build + push + helm only (infra exists)
#   ./scripts/deploy-aws.sh --skip-build     # Terraform + helm only (images exist)
#   ./scripts/deploy-aws.sh --destroy        # Tear everything down
#
set -euo pipefail

# ─── Configuration ──────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TF_DIR="$ROOT_DIR/terraform/aws"
OPERATOR_DIR="$ROOT_DIR/xtrinode"
HELM_DIR="$ROOT_DIR/helm"
DOCKERFILE="$ROOT_DIR/docker/Dockerfile"
DOCKER="${DOCKER:-docker}"
TERRAFORM="${TERRAFORM:-terraform}"
HELM="${HELM:-helm}"
KUBECTL="${KUBECTL:-kubectl}"
MAKE="${MAKE:-make}"
AWS="${AWS:-aws}"
CURL="${CURL:-curl}"
OPENSSL="${OPENSSL:-openssl}"

# Overridable via environment
AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"
DEFAULT_IMAGE_VERSION="$(
  awk '/^appVersion:/ {print $2; exit}' "$ROOT_DIR/helm/xtrinode/Chart.yaml" 2>/dev/null |
    tr -d '"' || true
)"
VERSION="${VERSION:-${DEFAULT_IMAGE_VERSION:-0.1.0}}"
CLUSTER_NAME="${CLUSTER_NAME:-xtrinode-eks-test}"
ENVIRONMENT="${ENVIRONMENT:-testing}"
POSTGRES_PASSWORD="${TF_VAR_postgres_admin_password:-}"
POSTGRES_ENABLED="${POSTGRES_ENABLED:-${TF_VAR_postgres_enabled:-}}"

# Namespaces
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
OBSERVABILITY_NAMESPACE="${OBSERVABILITY_NAMESPACE:-monitoring}"
PROMETHEUS_ENABLED="${PROMETHEUS_ENABLED:-false}"
PROMETHEUS_STORAGE_CLASS="${PROMETHEUS_STORAGE_CLASS:-gp3}"
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

# Flags
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

# ─── Helpers ────────────────────────────────────────────────────────────────
info()  { echo -e "\n\033[1;34m▸ $*\033[0m"; }
ok()    { echo -e "\033[1;32m✔ $*\033[0m"; }
warn()  { echo -e "\033[1;33m⚠ $*\033[0m"; }
fail()  { echo -e "\033[1;31m✖ $*\033[0m" >&2; exit 1; }

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

# Verify AWS credentials
AWS_ACCOUNT_ID=$("$AWS" sts get-caller-identity --profile "$AWS_PROFILE" --query Account --output text 2>/dev/null) \
  || fail "AWS credentials not configured. Run: aws configure --profile $AWS_PROFILE"
ECR_REGISTRY="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
ok "AWS Account: $AWS_ACCOUNT_ID  Region: $AWS_REGION  Registry: $ECR_REGISTRY"
ok "XTrinode image tag: $VERSION"

if [ -z "$API_SERVER_AUTH_TOKEN" ]; then
  API_SERVER_AUTH_TOKEN="$(random_token)"
fi
if [ -z "$API_SERVER_RESUME_AUTH_TOKEN" ]; then
  API_SERVER_RESUME_AUTH_TOKEN="$(random_token)"
fi

# ─── Destroy mode ───────────────────────────────────────────────────────────
if [ "$DESTROY" = true ]; then
  info "Destroying all resources"
  warn "This will delete the EKS cluster, ECR repos, optional RDS, and all data!"
  read -rp "Type 'yes' to confirm: " confirm
  [ "$confirm" = "yes" ] || { echo "Aborted."; exit 0; }

  info "Removing Helm releases"
  "$AWS" eks update-kubeconfig --region "$AWS_REGION" --name "$CLUSTER_NAME" --profile "$AWS_PROFILE" 2>/dev/null || true
  "$HELM" uninstall xtrinode-gateway -n "$GATEWAY_NAMESPACE" 2>/dev/null || true
  "$HELM" uninstall xtrinode-api-server -n "$OPERATOR_NAMESPACE" 2>/dev/null || true
  "$HELM" uninstall xtrinode-operator -n "$OPERATOR_NAMESPACE" 2>/dev/null || true

  info "Running terraform destroy"
  cd "$TF_DIR"
  "$TERRAFORM" destroy -auto-approve \
    -var "aws_profile=${AWS_PROFILE}" \
    -var "aws_region=${AWS_REGION}" \
    -var "postgres_admin_password=${POSTGRES_PASSWORD:-dummy}" \
    -var "eks_public_access=true"
  ok "All AWS resources destroyed"
  exit 0
fi

# ─── Step 1: Terraform (infra) ─────────────────────────────────────────────
if [ "$SKIP_TERRAFORM" = false ]; then
  info "Step 1/5: Provisioning AWS infrastructure with Terraform"

  if postgres_enabled && [ -z "$POSTGRES_PASSWORD" ]; then
    read -rsp "Enter PostgreSQL admin password (TF_VAR_postgres_admin_password): " POSTGRES_PASSWORD
    echo
    [ -n "$POSTGRES_PASSWORD" ] || fail "PostgreSQL password is required when postgres_enabled=true."
    export TF_VAR_postgres_admin_password="$POSTGRES_PASSWORD"
  fi

  cd "$TF_DIR"
  "$TERRAFORM" init -upgrade

  info "Planning..."
  CURRENT_IP=$("$CURL" -fsS --max-time 5 https://checkip.amazonaws.com | tr -d '[:space:]')
  [ -n "$CURRENT_IP" ] || fail "Could not determine public IP for temporary EKS endpoint access."
  terraform_plan_args=(
    -var "aws_profile=${AWS_PROFILE}"
    -var "aws_region=${AWS_REGION}"
    -var "eks_public_access=true"
    -var "eks_public_access_cidrs=[\"${CURRENT_IP}/32\"]"
  )
  if [ -n "$POSTGRES_PASSWORD" ]; then
    terraform_plan_args+=(-var "postgres_admin_password=${POSTGRES_PASSWORD}")
  fi
  if [ -n "$POSTGRES_ENABLED" ]; then
    terraform_plan_args+=(-var "postgres_enabled=${POSTGRES_ENABLED}")
  fi
  "$TERRAFORM" plan "${terraform_plan_args[@]}" -out=tfplan

  info "Applying..."
  "$TERRAFORM" apply tfplan
  ok "Terraform apply complete"
else
  info "Step 1/5: Skipping Terraform (--skip-terraform)"
fi

# ─── Step 2: Configure kubectl ─────────────────────────────────────────────
info "Step 2/5: Configuring kubectl for EKS"
"$AWS" eks update-kubeconfig \
  --region "$AWS_REGION" \
  --name "$CLUSTER_NAME" \
  --profile "$AWS_PROFILE"

"$KUBECTL" cluster-info 2>/dev/null \
  || fail "Cannot connect to cluster. Is eks_public_access enabled?"
ok "kubectl configured — cluster reachable"

# ─── Step 3: Build Docker images ───────────────────────────────────────────
if [ "$SKIP_BUILD" = false ]; then
  info "Step 3/5: Building Docker images locally"
  GIT_COMMIT=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")
  BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  COMPONENTS=(
    "operator:./cmd/operator:8081"
    "api-server:./cmd/api-server:8081"
    "gateway:./cmd/gateway:8080"
  )

  for comp in "${COMPONENTS[@]}"; do
    IFS=':' read -r name app_package app_port <<< "$comp"
    image_name="xtrinode-${name}"
    tag="${ECR_REGISTRY}/${image_name}:${VERSION}"
    info "  Building ${image_name}..."
    "$DOCKER" build \
      --build-arg APP_PACKAGE="$app_package" \
      --build-arg APP_PORT="$app_port" \
      --build-arg VERSION="$VERSION" \
      --build-arg GIT_COMMIT="$GIT_COMMIT" \
      --build-arg BUILD_DATE="$BUILD_DATE" \
      -t "$tag" \
      -f "$DOCKERFILE" \
      "$OPERATOR_DIR"
    ok "  Built: $tag"
  done

  # ─── Step 4: Push to ECR ────────────────────────────────────────────────
  info "Step 4/5: Pushing images to ECR"
  "$AWS" ecr get-login-password --region "$AWS_REGION" --profile "$AWS_PROFILE" | \
    "$DOCKER" login --username AWS --password-stdin "$ECR_REGISTRY"

  for comp in "${COMPONENTS[@]}"; do
    IFS=':' read -r name _ <<< "$comp"
    image_name="xtrinode-${name}"
    info "  Pushing ${image_name}..."
    "$DOCKER" push "${ECR_REGISTRY}/${image_name}:${VERSION}"
    ok "  Pushed: ${ECR_REGISTRY}/${image_name}:${VERSION}"
  done
else
  info "Step 3/5: Skipping Docker build (--skip-build)"
  info "Step 4/5: Skipping Docker push (--skip-build)"
fi

# ─── Step 5: Helm deploy ───────────────────────────────────────────────────
info "Step 5/5: Deploying to EKS via Helm"

# Create namespaces
"$KUBECTL" create namespace "$OPERATOR_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace "$GATEWAY_NAMESPACE" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create namespace team-test --dry-run=client -o yaml | "$KUBECTL" apply -f -

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

# Generate CRDs (outputs to helm chart crds/), then apply them explicitly.
# Helm installs CRDs only on first install and does not upgrade existing CRDs.
info "  Generating manifests (CRDs)..."
cd "$ROOT_DIR" && "$MAKE" manifests
"$KUBECTL" apply -f "$HELM_DIR/xtrinode-operator/crds"

# Build Helm dependencies from chart locks.
info "  Building Helm dependencies..."
cd "$ROOT_DIR"
"$HELM" dependency build "$HELM_DIR/xtrinode-operator"
"$HELM" dependency build "$HELM_DIR/xtrinode-observability"

if [ "$PROMETHEUS_ENABLED" = "true" ] || [ "$VECTOR_ENABLED" = "true" ]; then
  info "  Deploying xtrinode-observability..."
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
    --set vector.region="$AWS_REGION" \
    --set vector.logLevel="$VECTOR_LOG_LEVEL" \
    --set vector.serviceMonitor.enabled="$PROMETHEUS_ENABLED" \
    --wait --timeout 10m
  ok "  Observability deployed"
fi

# Deploy operator
info "  Deploying xtrinode-operator..."
"$HELM" upgrade --install xtrinode-operator "$HELM_DIR/xtrinode-operator" \
  --take-ownership \
  --namespace "$OPERATOR_NAMESPACE" \
  --set image.repository="${ECR_REGISTRY}/xtrinode-operator" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set keda.enabled=true \
  --set webhook.enabled="$WEBHOOK_ENABLED" \
  --set serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --set operator.prometheus.address="$PROMETHEUS_URL" \
  --set operator.capiProvider=aws \
  --wait --timeout 5m
ok "  Operator deployed"

# Deploy API server
info "  Deploying xtrinode-api-server..."
"$HELM" upgrade --install xtrinode-api-server "$HELM_DIR/xtrinode-api-server" \
  --take-ownership \
  --namespace "$OPERATOR_NAMESPACE" \
  --set image.repository="${ECR_REGISTRY}/xtrinode-api-server" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set apiServer.auth.enabled=true \
  --set apiServer.auth.existingSecret="$API_SERVER_AUTH_SECRET" \
  --set apiServer.auth.resume.enabled=true \
  --set apiServer.auth.resume.existingSecret="$API_SERVER_RESUME_AUTH_SECRET" \
  --set metrics.serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --wait --timeout 5m
ok "  API server deployed"

# Deploy gateway
info "  Deploying xtrinode-gateway..."
"$HELM" upgrade --install xtrinode-gateway "$HELM_DIR/xtrinode-gateway" \
  --force \
  --namespace "$GATEWAY_NAMESPACE" \
  --set image.repository="${ECR_REGISTRY}/xtrinode-gateway" \
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
ok "  Gateway deployed"

# ─── Summary ───────────────────────────────────────────────────────────────
echo ""
echo "============================================================"
echo "  XTrinode Deployment Complete!"
echo "============================================================"
echo ""
echo "  Cluster:    $CLUSTER_NAME"
echo "  Region:     $AWS_REGION"
echo "  Registry:   $ECR_REGISTRY"
echo "  Image tag:  $VERSION"
echo ""
echo "  Namespaces:"
echo "    Operator + API Server:  $OPERATOR_NAMESPACE"
echo "    Gateway:                $GATEWAY_NAMESPACE"
echo ""
echo "  Verify:"
echo "    kubectl get pods -n $OPERATOR_NAMESPACE"
echo "    kubectl get pods -n $GATEWAY_NAMESPACE"
echo "    kubectl get xtrinodes -A"
echo ""
echo "  Logs:"
echo "    kubectl logs -n $OPERATOR_NAMESPACE -l app.kubernetes.io/name=xtrinode-operator -f"
echo "    kubectl logs -n $OPERATOR_NAMESPACE -l app.kubernetes.io/name=xtrinode-api-server -f"
echo "    kubectl logs -n $GATEWAY_NAMESPACE  -l app.kubernetes.io/name=xtrinode-gateway -f"
echo ""
echo "  ⚠  Remember to disable public EKS access when done testing:"
echo "    cd terraform/aws && terraform apply -var 'eks_public_access=false'"
echo ""
