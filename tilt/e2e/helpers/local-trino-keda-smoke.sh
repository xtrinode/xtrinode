#!/usr/bin/env bash
# Real-Trino local e2e smoke for k3d: operator + API server + gateway + KEDA.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
NAMESPACE="${NAMESPACE:-team-local}"
XTRINODE_NAME="${XTRINODE_NAME:-local-trino-keda}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
GATEWAY_SERVICE="${GATEWAY_SERVICE:-xtrinode-gateway}"
API_SERVER_NAMESPACE="${API_SERVER_NAMESPACE:-xtrinode-system}"
API_SERVER_SERVICE="${API_SERVER_SERVICE:-xtrinode-api-server}"
GATEWAY_PORT="${GATEWAY_PORT:-18080}"
API_SERVER_PORT="${API_SERVER_PORT:-18081}"
API_SERVER_AUTH_TOKEN="${API_SERVER_AUTH_TOKEN:-local-dev-api-server-token}"
WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-900}"
CURL_CONNECT_TIMEOUT_SECONDS="${CURL_CONNECT_TIMEOUT_SECONDS:-5}"
CURL_MAX_TIME_SECONDS="${CURL_MAX_TIME_SECONDS:-30}"
CURL_HEALTH_MAX_TIME_SECONDS="${CURL_HEALTH_MAX_TIME_SECONDS:-5}"
MANIFEST="${MANIFEST:-${ROOT_DIR}/tilt/examples/xtrinode-real-trino-keda.yaml}"
POSTGRES_MANIFEST="${POSTGRES_MANIFEST:-${ROOT_DIR}/tilt/examples/postgres-analytics.yaml}"
RESET_SMOKE="${RESET_SMOKE:-true}"
TRINO_IMAGE_REPOSITORY="${TRINO_IMAGE_REPOSITORY:-trinodb/trino}"
TRINO_IMAGE_TAG="${TRINO_IMAGE_TAG:-480}"
SCALEOUT_ENABLED="${SCALEOUT_ENABLED:-false}"
SCALEOUT_MAX_WORKERS="${SCALEOUT_MAX_WORKERS:-2}"
SCALEOUT_THRESHOLD="${SCALEOUT_THRESHOLD:-0.5}"
SCALEOUT_QUERY="${SCALEOUT_QUERY:-SELECT count(*) FROM \"local-tpch\".sf1000.lineitem WHERE rand() >= 0}"
SCALEOUT_WAIT_SECONDS="${SCALEOUT_WAIT_SECONDS:-420}"
SCALEOUT_POLL_INTERVAL_SECONDS="${SCALEOUT_POLL_INTERVAL_SECONDS:-1}"
RESUME_REQUESTED_ANNOTATION="xtrinode.analytics.xtrinode.io/resume-requested"
RESUME_REQUESTED_AT_ANNOTATION="xtrinode.analytics.xtrinode.io/resume-requested-at"
SUSPEND_REQUESTED_ANNOTATION="xtrinode.analytics.xtrinode.io/suspend-requested"
SUSPEND_REQUESTED_AT_ANNOTATION="xtrinode.analytics.xtrinode.io/suspend-requested-at"
WAKE_MIN_WORKERS_ANNOTATION="xtrinode.analytics.xtrinode.io/wake-min-workers"
WAKE_TTL_ANNOTATION="xtrinode.analytics.xtrinode.io/wake-ttl"
RENDERED_MANIFEST=""

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $1" >&2
    exit 1
  fi
}

require_cmd kubectl
require_cmd curl
require_cmd jq

cleanup() {
  if [ -n "${SCALEOUT_POLL_PID:-}" ]; then
    kill "$SCALEOUT_POLL_PID" >/dev/null 2>&1 || true
    wait "$SCALEOUT_POLL_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "${SCALEOUT_QUERY_ID:-}" ]; then
    cancel_query "$SCALEOUT_QUERY_ID" "${SCALEOUT_NEXT_URI:-}"
    SCALEOUT_QUERY_ID=""
  fi
  if [ -n "${GATEWAY_PF_PID:-}" ]; then
    kill "$GATEWAY_PF_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "${API_PF_PID:-}" ]; then
    kill "$API_PF_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "${RENDERED_MANIFEST:-}" ]; then
    rm -f "$RENDERED_MANIFEST"
  fi
}

on_exit() {
  local exit_status=$?
  if [ "$exit_status" -ne 0 ]; then
    dump_debug
  fi
  cleanup
  exit "$exit_status"
}

wait_for_jsonpath() {
  local description="$1"
  local command="$2"
  local expected="$3"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local value=""

  echo "Waiting for ${description}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    value="$(eval "$command" 2>/dev/null || true)"
    if [ "$value" = "$expected" ]; then
      echo "  ${description}: ${value}"
      return 0
    fi
    sleep 5
  done

  echo "Timed out waiting for ${description}; last value: ${value:-<empty>}" >&2
  return 1
}

wait_for_last_activity_since() {
  local min_epoch="$1"
  local deadline=$((SECONDS + 180))
  local last_activity=""
  local last_epoch=""

  echo "Waiting for autosuspend lastActivity to observe short query..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    last_activity="$(kubectl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o jsonpath='{.status.lastActivity}' 2>/dev/null || true)"
    if [ -n "$last_activity" ]; then
      last_epoch="$(date -u -d "$last_activity" +%s 2>/dev/null || echo 0)"
      if [ "$last_epoch" -ge "$min_epoch" ]; then
        echo "  lastActivity=${last_activity}"
        return 0
      fi
    fi
    sleep 5
  done

  echo "Timed out waiting for recent lastActivity; last value: ${last_activity:-<empty>}" >&2
  return 1
}

wait_for_deployment_available() {
  local deployment="$1"
  local min_available="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local available=""

  echo "Waiting for ${deployment} availableReplicas >= ${min_available}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    available="$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)"
    available="${available:-0}"
    if [ "$available" -ge "$min_available" ]; then
      echo "  ${deployment} availableReplicas=${available}"
      return 0
    fi
    sleep 5
  done

  kubectl get deployment "$deployment" -n "$NAMESPACE" -o wide || true
  kubectl describe deployment "$deployment" -n "$NAMESPACE" || true
  return 1
}

wait_for_deployment_replicas() {
  local deployment="$1"
  local expected="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local replicas=""

  echo "Waiting for ${deployment} spec.replicas == ${expected}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    replicas="$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
    replicas="${replicas:-0}"
    if [ "$replicas" = "$expected" ]; then
      echo "  ${deployment} spec.replicas=${replicas}"
      return 0
    fi
    sleep 5
  done

  kubectl get deployment "$deployment" -n "$NAMESPACE" -o wide || true
  return 1
}

wait_for_service_endpoints_empty() {
  local service="$1"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local endpoints=""

  echo "Waiting for ${service} endpoints to drain..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    endpoints="$(kubectl get endpoints "$service" -n "$NAMESPACE" -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || true)"
    if [ -z "$endpoints" ]; then
      echo "  ${service} endpoints drained"
      return 0
    fi
    sleep 2
  done

  kubectl get endpoints "$service" -n "$NAMESPACE" -o yaml || true
  return 1
}

wait_for_scaledobject_max_replicas() {
  local expected="$1"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local replicas=""

  echo "Waiting for ScaledObject maxReplicaCount == ${expected}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    replicas="$(kubectl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o jsonpath='{.spec.maxReplicaCount}' 2>/dev/null || true)"
    if [ "$replicas" = "$expected" ]; then
      echo "  ScaledObject maxReplicaCount=${replicas}"
      return 0
    fi
    sleep 5
  done

  kubectl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o yaml || true
  return 1
}

wait_for_deployment_replicas_with_timeout() {
  local deployment="$1"
  local expected="$2"
  local timeout="$3"
  local deadline=$((SECONDS + timeout))
  local replicas=""

  echo "Waiting for ${deployment} spec.replicas == ${expected} within ${timeout}s..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    replicas="$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
    replicas="${replicas:-0}"
    if [ "$replicas" = "$expected" ]; then
      echo "  ${deployment} spec.replicas=${replicas}"
      return 0
    fi
    sleep 5
  done

  kubectl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o yaml || true
  kubectl get deployment "$deployment" -n "$NAMESPACE" -o wide || true
  return 1
}

xtrinode_suspended_state() {
  local value
  value="$(kubectl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.suspended}' 2>/dev/null || true)"
  if [ -z "$value" ]; then
    echo "false"
    return 0
  fi
  echo "$value"
}

xtrinode_annotation_state() {
  local annotation="$1"
  local value
  value="$(kubectl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o json | jq -r --arg key "$annotation" '.metadata.annotations[$key] // ""' 2>/dev/null || true)"
  if [ -z "$value" ]; then
    echo "absent"
    return 0
  fi
  echo "$value"
}

start_port_forward() {
  local namespace="$1"
  local service="$2"
  local local_port="$3"
  local remote_port="$4"
  local log_file="$5"

  kubectl port-forward -n "$namespace" "svc/${service}" "${local_port}:${remote_port}" >"$log_file" 2>&1 &
  echo "$!"
}

start_gateway_port_forward() {
  echo "Port-forwarding gateway on localhost:${GATEWAY_PORT}..."
  if [ -n "${GATEWAY_PF_PID:-}" ]; then
    kill "$GATEWAY_PF_PID" >/dev/null 2>&1 || true
  fi
  GATEWAY_PF_PID="$(start_port_forward "$GATEWAY_NAMESPACE" "$GATEWAY_SERVICE" "$GATEWAY_PORT" 8080 /tmp/xtrinode-gateway-port-forward.log)"

  for _ in $(seq 1 45); do
    if curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_HEALTH_MAX_TIME_SECONDS" "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  cat /tmp/xtrinode-gateway-port-forward.log || true
  return 1
}

start_api_port_forward() {
  echo "Port-forwarding API server on localhost:${API_SERVER_PORT}..."
  if [ -n "${API_PF_PID:-}" ]; then
    kill "$API_PF_PID" >/dev/null 2>&1 || true
  fi
  API_PF_PID="$(start_port_forward "$API_SERVER_NAMESPACE" "$API_SERVER_SERVICE" "$API_SERVER_PORT" 8081 /tmp/xtrinode-api-port-forward.log)"

  for _ in $(seq 1 45); do
    if curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_HEALTH_MAX_TIME_SECONDS" "http://127.0.0.1:${API_SERVER_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  cat /tmp/xtrinode-api-port-forward.log || true
  return 1
}

ensure_gateway_port_forward() {
  if [ -n "${GATEWAY_PF_PID:-}" ] && kill -0 "$GATEWAY_PF_PID" >/dev/null 2>&1; then
    if curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_HEALTH_MAX_TIME_SECONDS" "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
  fi
  start_gateway_port_forward
}

wait_for_gateway_backend() {
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local code=""
  local ready_count=0

  echo "Waiting for gateway to reach Trino /v1/info..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    ensure_gateway_port_forward
    code="$(curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o /dev/null -w "%{http_code}" \
      -H "X-Trino-User: local-e2e" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "http://127.0.0.1:${GATEWAY_PORT}/v1/info" || true)"
    if [ "$code" = "200" ]; then
      ready_count=$((ready_count + 1))
      if [ "$ready_count" -ge 2 ]; then
        echo "  gateway backend /v1/info is ready"
        return 0
      fi
    else
      ready_count=0
    fi
    sleep 2
  done

  echo "Timed out waiting for gateway backend; last HTTP status: ${code:-<empty>}" >&2
  return 1
}

post_statement() {
  local sql="$1"
  local output="$2"
  ensure_gateway_port_forward >&2
  curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o "$output" -w "%{http_code}" \
    -X POST "http://127.0.0.1:${GATEWAY_PORT}/v1/statement" \
    -H "X-Trino-User: local-e2e" \
    -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
    --data "$sql"
}

wait_for_statement_accepted() {
  local sql="$1"
  local output="$2"
  local description="$3"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local code=""

  echo "Waiting for ${description} statement to be accepted..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    code="$(post_statement "$sql" "$output" || true)"
    echo "  ${description} statement HTTP status: ${code:-<empty>}"
    jq '{id, state, stats: .stats.state, error, code, message}' "$output" || cat "$output"
    if [ "$code" = "200" ]; then
      return 0
    fi
    sleep 5
  done

  echo "Timed out waiting for ${description} statement to be accepted" >&2
  return 1
}

wait_for_gateway_resume_trigger() {
  local sql="$1"
  local output="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local code=""

  echo "Waiting for gateway request to trigger resume..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    code="$(post_statement "$sql" "$output" || true)"
    echo "Gateway resume-trigger HTTP status: ${code:-<empty>}"
    jq '{triggered, gated, retryAfter, error, code, message}' "$output" || cat "$output"
    if [ "$code" = "503" ]; then
      return 0
    fi
    sleep 5
  done

  echo "Timed out waiting for gateway request to return resume-trigger 503; last status: ${code:-<empty>}" >&2
  return 1
}

drain_query() {
	local body="$1"
	local state next_uri path code

  state="$(jq -r '.stats.state // .state // empty' "$body" 2>/dev/null || true)"
  next_uri="$(jq -r '.nextUri // empty' "$body" 2>/dev/null || true)"
  echo "Initial query state: ${state:-unknown}"

  while [ -n "$next_uri" ]; do
    path="$(printf '%s' "$next_uri" | sed -E 's#^https?://[^/]+##')"
    ensure_gateway_port_forward
    code="$(curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o "$body" -w "%{http_code}" \
      -H "X-Trino-User: local-e2e" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "http://127.0.0.1:${GATEWAY_PORT}${path}" || true)"
    if [ "$code" != "200" ]; then
      echo "Query poll HTTP status: ${code:-<empty>}; retrying"
      sleep 2
      continue
    fi
    state="$(jq -r '.stats.state // .state // empty' "$body" 2>/dev/null || true)"
    next_uri="$(jq -r '.nextUri // empty' "$body" 2>/dev/null || true)"
    echo "Query state: ${state:-unknown}"
    case "$state" in
      FINISHED) return 0 ;;
      FAILED|CANCELED|CANCELLED)
        echo "Query ended unsuccessfully: ${state}" >&2
        jq '.error // empty' "$body" >&2 || true
        return 1
        ;;
    esac
    sleep 1
  done

  if [ "$state" = "FINISHED" ]; then
    return 0
  fi

  echo "Query did not reach FINISHED; final state: ${state:-unknown}" >&2
  jq '.error // empty' "$body" >&2 || true
  return 1
}

follow_query_until_done() {
  local body="$1"
  local state next_uri path code

  while true; do
    state="$(jq -r '.stats.state // .state // empty' "$body" 2>/dev/null || true)"
    next_uri="$(jq -r '.nextUri // empty' "$body" 2>/dev/null || true)"
    echo "Scale-out query state: ${state:-unknown}"

    case "$state" in
      FINISHED|FAILED|CANCELED|CANCELLED)
        return 0
        ;;
    esac

    if [ -z "$next_uri" ] || [ "$next_uri" = "null" ]; then
      return 0
    fi

    path="$(printf '%s' "$next_uri" | sed -E 's#^https?://[^/]+##')"
    ensure_gateway_port_forward
    code="$(curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o "$body" -w "%{http_code}" \
      -H "X-Trino-User: local-e2e" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "http://127.0.0.1:${GATEWAY_PORT}${path}" || true)"
    if [ "$code" != "200" ]; then
      echo "Scale-out query poll HTTP status: ${code:-<empty>}; retrying"
      sleep "$SCALEOUT_POLL_INTERVAL_SECONDS"
      continue
    fi
    sleep "$SCALEOUT_POLL_INTERVAL_SECONDS"
  done
}

cancel_query() {
  local query_id="$1"
  local next_uri="${2:-}"
  local path=""

  if [ -z "$query_id" ] || [ "$query_id" = "null" ]; then
    query_id=""
  fi

  echo "Cancelling scale-out query ${query_id:-unknown}..."
  ensure_gateway_port_forward
  if [ -n "$next_uri" ] && [ "$next_uri" != "null" ]; then
    path="$(printf '%s' "$next_uri" | sed -E 's#^https?://[^/]+##')"
    curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -X DELETE \
      -H "X-Trino-User: local-e2e" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "http://127.0.0.1:${GATEWAY_PORT}${path}" >/dev/null 2>&1 || true
  fi
  if [ -n "$query_id" ]; then
    curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -X DELETE \
      -H "X-Trino-User: local-e2e" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "http://127.0.0.1:${GATEWAY_PORT}/v1/query/${query_id}" >/dev/null 2>&1 || true
  fi
}

delete_lease_state() {
  kubectl delete lease -n "$OPERATOR_NAMESPACE" \
    -l 'app.kubernetes.io/name=xtrinode-operator,app.kubernetes.io/component=api-server' \
    --ignore-not-found=true >/dev/null 2>&1 || true
}

cleanup_generated_resources() {
  kubectl delete \
    deployment,service,configmap,poddisruptionbudget,serviceaccount,role,rolebinding,horizontalpodautoscaler,scaledobject,triggerauthentication \
    -n "$NAMESPACE" \
    -l "app.kubernetes.io/instance=${XTRINODE_NAME}" \
    --ignore-not-found=true \
    --wait=false >/dev/null 2>&1 || true
  kubectl delete pod -n "$NAMESPACE" \
    -l "app.kubernetes.io/instance=${XTRINODE_NAME}" \
    --ignore-not-found=true \
    --force \
    --grace-period=0 >/dev/null 2>&1 || true
}

wait_for_no_smoke_pods() {
  local deadline=$((SECONDS + 120))
  local pods=""

  echo "Waiting for previous ${XTRINODE_NAME} pods to disappear..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    pods="$(kubectl get pods -n "$NAMESPACE" \
      -l "app.kubernetes.io/instance=${XTRINODE_NAME}" \
      -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)"
    if [ -z "$pods" ]; then
      echo "  previous pods removed"
      return 0
    fi
    sleep 2
  done

  echo "Timed out waiting for old pods to disappear after force cleanup: ${pods}" >&2
  kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=${XTRINODE_NAME}" -o wide || true
  return 1
}

render_manifest() {
  local output="$1"

  kubectl apply --dry-run=client -f "$MANIFEST" -o json | jq \
    --arg repository "$TRINO_IMAGE_REPOSITORY" \
    --arg tag "$TRINO_IMAGE_TAG" \
    --arg scaleoutEnabled "$SCALEOUT_ENABLED" \
    --argjson maxWorkers "$SCALEOUT_MAX_WORKERS" \
    --arg threshold "$SCALEOUT_THRESHOLD" '
      def patch_xtrinode:
        .spec.autoSuspendAfter = "30m" |
        .spec.valuesOverlay = (.spec.valuesOverlay // {}) |
        .spec.valuesOverlay.image = (.spec.valuesOverlay.image // {}) |
        .spec.valuesOverlay.image.repository = $repository |
        .spec.valuesOverlay.image.tag = $tag |
        if $scaleoutEnabled == "true" then
          .spec.minWorkers = 1 |
          .spec.maxWorkers = $maxWorkers |
          .spec.keda = (.spec.keda // {}) |
          .spec.keda.enabled = true |
          .spec.keda.scalerType = "http" |
          .spec.keda.scalingMetric = "query" |
          .spec.keda.threshold = $threshold |
          .spec.keda.scaleDownCooldown = "30s"
        else
          .
        end;
      if .kind == "List" then
        .items |= map(if .kind == "XTrinode" then patch_xtrinode else . end)
      elif .kind == "XTrinode" then
        patch_xtrinode
      else
        .
      end
    ' >"$output"
}

dump_debug() {
  echo "=== Debug: XTrinode ==="
  kubectl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o yaml || true
  echo "=== Debug: Pods ==="
  kubectl get pods -n "$NAMESPACE" -o wide || true
  echo "=== Debug: KEDA ==="
  kubectl get scaledobject,horizontalpodautoscaler -n "$NAMESPACE" -o wide || true
  echo "=== Debug: Endpoints ==="
  kubectl get endpoints -n "$NAMESPACE" -o wide || true
  echo "=== Debug: Operator logs ==="
  kubectl logs -n "$OPERATOR_NAMESPACE" deployment/xtrinode-operator --tail=120 || true
  echo "=== Debug: Gateway logs ==="
  kubectl logs -n "$GATEWAY_NAMESPACE" deployment/xtrinode-gateway --tail=120 || true
}

trap on_exit EXIT

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

echo "Applying ${POSTGRES_MANIFEST}"
kubectl apply -f "$POSTGRES_MANIFEST"
wait_for_deployment_available "postgres" 1

if [ "$RESET_SMOKE" = "true" ]; then
  echo "Resetting previous local smoke resources..."
  kubectl delete "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --wait=false --ignore-not-found=true >/dev/null 2>&1 || true
  if ! kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=delete --timeout=120s >/dev/null 2>&1; then
    kubectl patch "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --type=merge -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || true
    kubectl delete "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --wait=false --ignore-not-found=true >/dev/null 2>&1 || true
    kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=delete --timeout=60s >/dev/null 2>&1 || true
  fi
  cleanup_generated_resources
  wait_for_no_smoke_pods
  kubectl delete "xtrinodecatalog/local-tpch" -n "$NAMESPACE" --ignore-not-found=true >/dev/null 2>&1 || true
  kubectl delete "xtrinodecatalog/postgres" -n "$NAMESPACE" --ignore-not-found=true >/dev/null 2>&1 || true
  delete_lease_state
fi

echo "Applying ${MANIFEST}"
if [ "$SCALEOUT_ENABLED" = "true" ]; then
  echo "Enabling local scale-out mode: maxWorkers=${SCALEOUT_MAX_WORKERS}, threshold=${SCALEOUT_THRESHOLD}"
fi
RENDERED_MANIFEST="$(mktemp)"
render_manifest "$RENDERED_MANIFEST"
kubectl apply -f "$RENDERED_MANIFEST"

kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
kubectl wait "scaledobject/trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
if [ "$SCALEOUT_ENABLED" = "true" ]; then
  wait_for_scaledobject_max_replicas "$SCALEOUT_MAX_WORKERS"
fi
wait_for_deployment_available "trino-${XTRINODE_NAME}-coordinator" 1
wait_for_deployment_available "trino-${XTRINODE_NAME}-worker" 1
wait_for_jsonpath "gateway route registered" \
  "kubectl get configmap trino-gateway-routes -n ${GATEWAY_NAMESPACE} -o jsonpath='{.data.routes\\.yaml}' | grep -q 'name: ${XTRINODE_NAME}' && echo ${XTRINODE_NAME}" \
  "$XTRINODE_NAME"

start_gateway_port_forward
start_api_port_forward
wait_for_gateway_backend

status_body="$(mktemp)"
curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" \
  -H "Authorization: Bearer ${API_SERVER_AUTH_TOKEN}" \
  "http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/status" \
  -o "$status_body"
jq -e '.phase' "$status_body" >/dev/null

query_body="$(mktemp)"
wait_for_statement_accepted 'SELECT count(*) FROM "local-tpch".sf1.nation' "$query_body" "initial"
drain_query "$query_body"

short_activity_epoch="$(date -u +%s)"
short_query_body="$(mktemp)"
wait_for_statement_accepted 'SELECT 1' "$short_query_body" "short autosuspend activity"
drain_query "$short_query_body"
wait_for_last_activity_since "$short_activity_epoch"

if [ "$SCALEOUT_ENABLED" = "true" ]; then
  scaleout_body="$(mktemp)"
  wait_for_statement_accepted "$SCALEOUT_QUERY" "$scaleout_body" "scale-out"
  SCALEOUT_QUERY_ID="$(jq -r '.id // empty' "$scaleout_body")"
  SCALEOUT_NEXT_URI="$(jq -r '.nextUri // empty' "$scaleout_body")"
  scaleout_poll_log="$(mktemp)"
  follow_query_until_done "$scaleout_body" >"$scaleout_poll_log" 2>&1 &
  SCALEOUT_POLL_PID="$!"

  wait_for_deployment_replicas_with_timeout "trino-${XTRINODE_NAME}-worker" "$SCALEOUT_MAX_WORKERS" "$SCALEOUT_WAIT_SECONDS"
  wait_for_deployment_available "trino-${XTRINODE_NAME}-worker" "$SCALEOUT_MAX_WORKERS"
  cancel_query "$SCALEOUT_QUERY_ID" "$SCALEOUT_NEXT_URI"
  SCALEOUT_QUERY_ID=""
  kill "$SCALEOUT_POLL_PID" >/dev/null 2>&1 || true
  wait "$SCALEOUT_POLL_PID" >/dev/null 2>&1 || true
  SCALEOUT_POLL_PID=""
  wait_for_deployment_replicas_with_timeout "trino-${XTRINODE_NAME}-worker" 1 "$SCALEOUT_WAIT_SECONDS"
fi

suspend_body="$(mktemp)"
suspend_code="$(curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o "$suspend_body" -w "%{http_code}" \
  -X POST "http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/suspend" \
  -H "Authorization: Bearer ${API_SERVER_AUTH_TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{}')"
echo "Suspend HTTP status: ${suspend_code}"
jq '{status, desired, lease, error, code}' "$suspend_body"
if [ "$suspend_code" != "202" ]; then
  exit 1
fi

wait_for_jsonpath "spec.suspended=true" "xtrinode_suspended_state" "true"
wait_for_jsonpath "suspend command annotation cleared" "xtrinode_annotation_state '$SUSPEND_REQUESTED_ANNOTATION'" "absent"
wait_for_jsonpath "suspend timestamp annotation cleared" "xtrinode_annotation_state '$SUSPEND_REQUESTED_AT_ANNOTATION'" "absent"
wait_for_deployment_replicas "trino-${XTRINODE_NAME}-coordinator" 0
wait_for_deployment_replicas "trino-${XTRINODE_NAME}-worker" 0
wait_for_service_endpoints_empty "trino-${XTRINODE_NAME}"

resume_body="$(mktemp)"
wait_for_gateway_resume_trigger "SELECT 1" "$resume_body"

gated_body="$(mktemp)"
gated_code="$(curl -sS --connect-timeout "$CURL_CONNECT_TIMEOUT_SECONDS" --max-time "$CURL_MAX_TIME_SECONDS" -o "$gated_body" -w "%{http_code}" \
  -X POST "http://127.0.0.1:${API_SERVER_PORT}/api/v1/resume" \
  -H "Authorization: Bearer ${API_SERVER_AUTH_TOKEN}" \
  -H "Content-Type: application/json" \
  --data "{\"candidate\":{\"namespace\":\"${NAMESPACE}\",\"name\":\"${XTRINODE_NAME}\"},\"reason\":\"local-e2e-lease-check\"}")"
echo "Immediate second resume HTTP status: ${gated_code}"
jq '{triggered, gated, retryAfter, keyType, error, holder}' "$gated_body"
if [ "$gated_code" != "503" ]; then
  echo "Expected immediate second resume request to be lease-gated with 503" >&2
  exit 1
fi

wait_for_jsonpath "spec.suspended=false" "xtrinode_suspended_state" "false"
wait_for_jsonpath "resume command annotation cleared" "xtrinode_annotation_state '$RESUME_REQUESTED_ANNOTATION'" "absent"
wait_for_jsonpath "resume timestamp annotation cleared" "xtrinode_annotation_state '$RESUME_REQUESTED_AT_ANNOTATION'" "absent"
wait_for_jsonpath "wake min workers annotation cleared" "xtrinode_annotation_state '$WAKE_MIN_WORKERS_ANNOTATION'" "absent"
wait_for_jsonpath "wake ttl annotation cleared" "xtrinode_annotation_state '$WAKE_TTL_ANNOTATION'" "absent"
kubectl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
wait_for_deployment_available "trino-${XTRINODE_NAME}-coordinator" 1
wait_for_deployment_available "trino-${XTRINODE_NAME}-worker" 1
wait_for_gateway_backend

retry_body="$(mktemp)"
retry_code="$(post_statement "SELECT 1" "$retry_body")"
echo "Post-resume statement HTTP status: ${retry_code}"
jq '{id, state, stats: .stats.state, error}' "$retry_body"
if [ "$retry_code" != "200" ]; then
  exit 1
fi
drain_query "$retry_body"

kubectl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o wide
kubectl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o wide
kubectl get deployment "trino-${XTRINODE_NAME}-coordinator" "trino-${XTRINODE_NAME}-worker" -n "$NAMESPACE" -o wide
echo "Local real-Trino KEDA e2e smoke passed"
