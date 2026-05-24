#!/usr/bin/env bash
# Deploy XTrinode operator to GCP GKE cluster
# Prerequisites: see docs/TOOLING.md for versions.
#   - gcloud auth, gke-gcloud-auth-plugin, Helm, kubectl, and openssl

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
HELM="${HELM:-helm}"
KUBECTL="${KUBECTL:-kubectl}"
MAKE="${MAKE:-make}"
GCLOUD="${GCLOUD:-gcloud}"
OPENSSL="${OPENSSL:-openssl}"
DEFAULT_IMAGE_VERSION="$(
  awk '/^appVersion:/ {print $2; exit}' "${ROOT_DIR}/helm/xtrinode/Chart.yaml" 2>/dev/null |
    tr -d '"' || true
)"

PROJECT_ID="${GCP_PROJECT_ID:-project-40642592-0c4f-4ce1-9d6}"
CLUSTER_NAME="${GCP_CLUSTER_NAME:-xtrinode-gke-test}"
ZONE="${GCP_ZONE:-us-central1-a}"
REGION="${GCP_REGION:-us-central1}"
REGISTRY="${REGION}-docker.pkg.dev/${PROJECT_ID}"
VERSION="${VERSION:-${DEFAULT_IMAGE_VERSION:-0.1.0}}"
NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
GATEWAY_REPLICA_COUNT="${GATEWAY_REPLICA_COUNT:-1}"
GATEWAY_REDIS_ENABLED="${GATEWAY_REDIS_ENABLED:-false}"
GATEWAY_IN_CHART_REDIS_ENABLED="${GATEWAY_IN_CHART_REDIS_ENABLED:-$GATEWAY_REDIS_ENABLED}"
GATEWAY_AUTH_ENABLED="${GATEWAY_AUTH_ENABLED:-true}"
GATEWAY_AUTH_SECRET="${GATEWAY_AUTH_SECRET:-trino-gateway-api-keys}"
GATEWAY_AUTH_SECRET_KEY="${GATEWAY_AUTH_SECRET_KEY:-api-keys}"
GATEWAY_API_KEY_ID="${GATEWAY_API_KEY_ID:-default}"
GATEWAY_API_KEY="${GATEWAY_API_KEY:-}"
OBSERVABILITY_NAMESPACE="${OBSERVABILITY_NAMESPACE:-monitoring}"
PROMETHEUS_ENABLED="${PROMETHEUS_ENABLED:-true}"
PROMETHEUS_STORAGE_CLASS="${PROMETHEUS_STORAGE_CLASS:-standard}"
VECTOR_ENABLED="${VECTOR_ENABLED:-true}"
VECTOR_NAMESPACE="${VECTOR_NAMESPACE:-observability}"
VECTOR_LOG_LEVEL="${VECTOR_LOG_LEVEL:-info}"
WEBHOOK_ENABLED="${WEBHOOK_ENABLED:-true}"
ENVIRONMENT="${ENVIRONMENT:-testing}"
API_SERVER_AUTH_SECRET="${API_SERVER_AUTH_SECRET:-xtrinode-api-server-auth}"
API_SERVER_AUTH_TOKEN="${API_SERVER_AUTH_TOKEN:-$("$OPENSSL" rand -hex 32)}"
API_SERVER_RESUME_AUTH_SECRET="${API_SERVER_RESUME_AUTH_SECRET:-xtrinode-api-server-resume-auth}"
API_SERVER_RESUME_AUTH_TOKEN="${API_SERVER_RESUME_AUTH_TOKEN:-$("$OPENSSL" rand -hex 32)}"
PROMETHEUS_URL="http://prometheus-operated.${OBSERVABILITY_NAMESPACE}.svc.cluster.local:9090"

echo "=== Deploying XTrinode to GCP ==="
echo "Project: $PROJECT_ID, Cluster: $CLUSTER_NAME"
echo "XTrinode image tag: $VERSION"
echo "Gateway replicas: $GATEWAY_REPLICA_COUNT, Redis enabled: $GATEWAY_REDIS_ENABLED"
echo "Gateway auth enabled: $GATEWAY_AUTH_ENABLED"
echo "Prometheus enabled: $PROMETHEUS_ENABLED, Vector enabled: $VECTOR_ENABLED"
echo "Operator admission webhooks enabled: $WEBHOOK_ENABLED"
echo ""

# Get cluster credentials (try region first for regional clusters, then zone)
echo "Configuring kubectl..."
if "$GCLOUD" container clusters get-credentials "$CLUSTER_NAME" --region "$REGION" --project "$PROJECT_ID" 2>/dev/null; then
  echo "Using regional cluster ($REGION)"
elif "$GCLOUD" container clusters get-credentials "$CLUSTER_NAME" --zone "$ZONE" --project "$PROJECT_ID" 2>/dev/null; then
  echo "Using zonal cluster ($ZONE)"
else
  echo "Error: Could not get cluster credentials. Is the cluster created?"
  exit 1
fi

# Ensure namespaces exist
"$KUBECTL" get namespace "$NAMESPACE" || "$KUBECTL" create namespace "$NAMESPACE"
"$KUBECTL" get namespace xtrinode-gateway || "$KUBECTL" create namespace xtrinode-gateway
"$KUBECTL" get namespace team-test || "$KUBECTL" create namespace team-test

auth_token_file="$(mktemp)"
resume_auth_token_file="$(mktemp)"
gateway_auth_keys_file="$(mktemp)"
trap 'rm -f "$auth_token_file" "$resume_auth_token_file" "$gateway_auth_keys_file"' EXIT
printf '%s' "$API_SERVER_AUTH_TOKEN" > "$auth_token_file"
printf '%s' "$API_SERVER_RESUME_AUTH_TOKEN" > "$resume_auth_token_file"
"$KUBECTL" create secret generic "$API_SERVER_AUTH_SECRET" \
  --namespace "$NAMESPACE" \
  --from-file=token="$auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create secret generic "$API_SERVER_RESUME_AUTH_SECRET" \
  --namespace "$NAMESPACE" \
  --from-file=token="$resume_auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" create secret generic "$API_SERVER_RESUME_AUTH_SECRET" \
  --namespace xtrinode-gateway \
  --from-file=token="$resume_auth_token_file" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -

if [ "$GATEWAY_AUTH_ENABLED" = "true" ]; then
  existing_gateway_keys="$(
    "$KUBECTL" get secret "$GATEWAY_AUTH_SECRET" \
      --namespace xtrinode-gateway \
      -o "go-template={{ index .data \"$GATEWAY_AUTH_SECRET_KEY\" }}" 2>/dev/null |
      base64 -d 2>/dev/null || true
  )"
  if [ -n "$GATEWAY_API_KEY" ]; then
    printf '%s: "%s"\n' "$GATEWAY_API_KEY_ID" "$GATEWAY_API_KEY" > "$gateway_auth_keys_file"
  elif [ -n "$existing_gateway_keys" ]; then
    printf '%s' "$existing_gateway_keys" > "$gateway_auth_keys_file"
  else
    printf '%s: "%s"\n' "$GATEWAY_API_KEY_ID" "$("$OPENSSL" rand -hex 32)" > "$gateway_auth_keys_file"
  fi
  "$KUBECTL" create secret generic "$GATEWAY_AUTH_SECRET" \
    --namespace xtrinode-gateway \
    --from-file="$GATEWAY_AUTH_SECRET_KEY=$gateway_auth_keys_file" \
    --dry-run=client -o yaml | "$KUBECTL" apply -f -
fi

# Generate CRDs (outputs to helm chart crds/), then apply them explicitly.
# Helm installs CRDs only on first install and does not upgrade existing CRDs.
echo "Generating manifests (CRDs)..."
cd "$ROOT_DIR" && "$MAKE" manifests
"$KUBECTL" apply -f helm/xtrinode-operator/crds

# Build Helm dependencies from the chart locks. This reuses vendored chart archives when present
# and only downloads missing dependencies.
echo "Building Helm dependencies..."
cd "$ROOT_DIR"
"$HELM" dependency build helm/xtrinode-operator
"$HELM" dependency build helm/xtrinode-observability

if [ "$PROMETHEUS_ENABLED" = "true" ] || [ "$VECTOR_ENABLED" = "true" ]; then
  echo "Deploying XTrinode observability stack..."
  "$HELM" upgrade --install xtrinode-observability helm/xtrinode-observability \
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
    --set vector.region="$REGION" \
    --set vector.logLevel="$VECTOR_LOG_LEVEL" \
    --set vector.serviceMonitor.enabled="$PROMETHEUS_ENABLED" \
    --wait \
    --timeout=10m
fi

# Deploy operator (includes KEDA subchart)
echo "Deploying XTrinode operator..."
"$HELM" upgrade --install xtrinode-operator helm/xtrinode-operator \
  --take-ownership \
  --namespace "$NAMESPACE" \
  --set image.repository="${REGISTRY}/xtrinode-operator/xtrinode-operator" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set keda.enabled=true \
  --set webhook.enabled="$WEBHOOK_ENABLED" \
  --set serviceMonitor.forceRender="$PROMETHEUS_ENABLED" \
  --set operator.prometheus.address="$PROMETHEUS_URL" \
  --set operator.capiProvider=gcp
"$KUBECTL" rollout restart deployment/xtrinode-operator -n "$NAMESPACE"
"$KUBECTL" rollout status deployment/xtrinode-operator -n "$NAMESPACE" --timeout=5m

# Deploy API server (control plane - resume/suspend, metrics)
echo "Deploying XTrinode API server..."
"$HELM" upgrade --install xtrinode-api-server helm/xtrinode-api-server \
  --take-ownership \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --set image.repository="${REGISTRY}/xtrinode-api-server/xtrinode-api-server" \
  --set image.tag="$VERSION" \
  --set image.pullPolicy=Always \
  --set apiServer.auth.enabled=true \
  --set apiServer.auth.existingSecret="$API_SERVER_AUTH_SECRET" \
  --set apiServer.auth.resume.enabled=true \
  --set apiServer.auth.resume.existingSecret="$API_SERVER_RESUME_AUTH_SECRET" \
  --set metrics.serviceMonitor.forceRender="$PROMETHEUS_ENABLED"
"$KUBECTL" rollout restart deployment/xtrinode-api-server -n "$NAMESPACE"
"$KUBECTL" rollout status deployment/xtrinode-api-server -n "$NAMESPACE" --timeout=5m

# Deploy gateway (query routing, auto-resume trigger)
# API server lives in the control-plane namespace; gateway must reach it via service DNS.
echo "Deploying XTrinode gateway..."
"$HELM" upgrade --install xtrinode-gateway helm/xtrinode-gateway \
  --force \
  --namespace xtrinode-gateway \
  --create-namespace \
  --set image.repository="${REGISTRY}/xtrinode-gateway/xtrinode-gateway" \
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
  --set gateway.auth.apiKey.namespace=xtrinode-gateway \
  --set gateway.apiServerURL="http://xtrinode-api-server.${NAMESPACE}.svc.cluster.local:8081/api/v1"
"$KUBECTL" rollout restart deployment/xtrinode-gateway -n xtrinode-gateway
"$KUBECTL" rollout status deployment/xtrinode-gateway -n xtrinode-gateway --timeout=5m
if [ "$GATEWAY_IN_CHART_REDIS_ENABLED" = "true" ]; then
  "$KUBECTL" rollout status deployment/xtrinode-gateway-redis -n xtrinode-gateway --timeout=5m
fi

# Optional: Create Postgres secret and XTrinodeCatalog if POSTGRES_HOST is set (from Terraform output)
if [ -n "${POSTGRES_HOST}" ]; then
  echo "Creating Postgres secret and XTrinodeCatalog..."
  "$KUBECTL" create secret generic trino-catalog-postgres-analytics-secret \
    --namespace "$NAMESPACE" \
    --from-literal=POSTGRES_HOST="${POSTGRES_HOST}" \
    --from-literal=POSTGRES_PORT="${POSTGRES_PORT:-5432}" \
    --from-literal=POSTGRES_DATABASE="${POSTGRES_DATABASE:-xtrinode_analytics}" \
    --from-literal=POSTGRES_USER="${POSTGRES_USER:-xtrinode_admin}" \
    --from-literal=POSTGRES_PASSWORD="${POSTGRES_PASSWORD}" \
    --from-literal=JDBC_URL="jdbc:postgresql://${POSTGRES_HOST}:5432/${POSTGRES_DATABASE:-xtrinode_analytics}" \
    --dry-run=client -o yaml | "$KUBECTL" apply -f -
  "$KUBECTL" create secret generic trino-catalog-postgres-analytics-secret \
    --namespace team-test \
    --from-literal=POSTGRES_HOST="${POSTGRES_HOST}" \
    --from-literal=POSTGRES_PORT="${POSTGRES_PORT:-5432}" \
    --from-literal=POSTGRES_DATABASE="${POSTGRES_DATABASE:-xtrinode_analytics}" \
    --from-literal=POSTGRES_USER="${POSTGRES_USER:-xtrinode_admin}" \
    --from-literal=POSTGRES_PASSWORD="${POSTGRES_PASSWORD}" \
    --from-literal=JDBC_URL="jdbc:postgresql://${POSTGRES_HOST}:5432/${POSTGRES_DATABASE:-xtrinode_analytics}" \
    --dry-run=client -o yaml | "$KUBECTL" apply -f -
  echo "Postgres secrets created. Create XTrinodeCatalog manually or via examples/xtrinode-gcp-test.yaml"
fi

echo ""
echo "=== Deploy complete ==="
echo "Check status:"
echo "  kubectl get pods -n $NAMESPACE"
echo "  kubectl get pods -n xtrinode-gateway"
echo ""
