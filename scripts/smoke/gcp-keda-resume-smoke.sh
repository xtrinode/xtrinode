#!/usr/bin/env bash
# Smoke test gateway Prometheus query scaling, KEDA scale-down, auto-suspend, and gateway-triggered resume.
set -euo pipefail

NAMESPACE="${NAMESPACE:-team-test}"
XTRINODE_NAME="${XTRINODE_NAME:-prom-query-smoke}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
GATEWAY_SERVICE="${GATEWAY_SERVICE:-xtrinode-gateway}"
GATEWAY_PORT="${GATEWAY_PORT:-18080}"
GATEWAY_AUTH_ENABLED="${GATEWAY_AUTH_ENABLED:-true}"
GATEWAY_AUTH_SECRET="${GATEWAY_AUTH_SECRET:-trino-gateway-api-keys}"
GATEWAY_AUTH_SECRET_KEY="${GATEWAY_AUTH_SECRET_KEY:-api-keys}"
GATEWAY_API_KEY_ID="${GATEWAY_API_KEY_ID:-default}"
GATEWAY_API_KEY="${GATEWAY_API_KEY:-}"
WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-600}"
DELETE_WAIT_SECONDS="${DELETE_WAIT_SECONDS:-420}"
AUTO_SUSPEND_WAIT_SECONDS="${AUTO_SUSPEND_WAIT_SECONDS:-75}"
QUERY_SQL="${QUERY_SQL:-SELECT count(*) FROM \"tpch-smoke\".sf1.lineitem}"
VERIFY_QUERY_SQL="${VERIFY_QUERY_SQL:-$QUERY_SQL}"
RESET_SMOKE="${RESET_SMOKE:-true}"
KUBECTL="${KUBECTL:-kubectl}"
GATEWAY_AUTH_CURL_ARGS=()

cleanup() {
  if [ -n "${GATEWAY_PF_PID:-}" ]; then
    kill "$GATEWAY_PF_PID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

kctl() {
  "$KUBECTL" "$@"
}

wait_for_jsonpath() {
  local description="$1"
  local command="$2"
  local expected="$3"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))

  echo "Waiting for ${description}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    value="$(eval "$command" 2>/dev/null || true)"
    if [ "$value" = "$expected" ]; then
      echo "  ${description}: ${value}"
      return 0
    fi
    sleep 5
  done

  echo "Timed out waiting for ${description}; last value: ${value:-<empty>}"
  return 1
}

wait_for_deployment_available() {
  local deployment="$1"
  local min_available="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))

  echo "Waiting for ${deployment} availableReplicas >= ${min_available}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    available="$(kctl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)"
    available="${available:-0}"
    if [ "$available" -ge "$min_available" ]; then
      echo "  ${deployment} availableReplicas=${available}"
      return 0
    fi
    sleep 5
  done

  kctl get deployment "$deployment" -n "$NAMESPACE" -o wide || true
  return 1
}

wait_for_worker_server_started() {
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local pod=""

  echo "Waiting for Trino worker server startup..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    pod="$(kctl get pod -n "$NAMESPACE" \
      -l "app.kubernetes.io/name=trino,app.kubernetes.io/instance=${XTRINODE_NAME},app.kubernetes.io/component=worker" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [ -n "$pod" ] && kctl logs "$pod" -n "$NAMESPACE" 2>/dev/null | grep -q 'SERVER STARTED'; then
      echo "  ${pod} reported SERVER STARTED"
      return 0
    fi
    sleep 5
  done

  kctl get pod -n "$NAMESPACE" \
    -l "app.kubernetes.io/name=trino,app.kubernetes.io/instance=${XTRINODE_NAME},app.kubernetes.io/component=worker" \
    -o wide || true
  return 1
}

wait_for_deployment_replicas() {
  local deployment="$1"
  local expected="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))

  echo "Waiting for ${deployment} spec.replicas == ${expected}..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    replicas="$(kctl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
    replicas="${replicas:-0}"
    if [ "$replicas" = "$expected" ]; then
      echo "  ${deployment} spec.replicas=${replicas}"
      return 0
    fi
    sleep 5
  done

  kctl get deployment "$deployment" -n "$NAMESPACE" -o wide || true
	return 1
}

xtrinode_suspended_state() {
  local value

  value="$(kctl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.suspended}' 2>/dev/null || true)"
  if [ -z "$value" ]; then
    echo "false"
    return 0
  fi
  echo "$value"
}

delete_smoke_resource() {
  local kind="$1"
  local name="$2"

  kctl delete "${kind}/${name}" -n "$NAMESPACE" --ignore-not-found=true >/dev/null 2>&1 || true
}

load_gateway_auth() {
  local encoded_keys keys

  if [ "$GATEWAY_AUTH_ENABLED" != "true" ]; then
    return 0
  fi

  if [ -z "$GATEWAY_API_KEY" ]; then
    encoded_keys="$(
      kctl get secret "$GATEWAY_AUTH_SECRET" \
        -n "$GATEWAY_NAMESPACE" \
        -o "go-template={{ index .data \"$GATEWAY_AUTH_SECRET_KEY\" }}" 2>/dev/null || true
    )"
    if [ -n "$encoded_keys" ]; then
      keys="$(printf '%s' "$encoded_keys" | base64 -d 2>/dev/null || true)"
    fi
    if [ -n "${keys:-}" ]; then
      GATEWAY_API_KEY="$(
        printf '%s\n' "$keys" |
          awk -F: -v id="$GATEWAY_API_KEY_ID" '
            $1 == id {
              val=$0
              sub(/^[^:]+:[[:space:]]*/, "", val)
              gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
              gsub(/^"|"$/, "", val)
              matched=1
              print val
              exit
            }
            key == "" && NF >= 2 {
              val=$0
              sub(/^[^:]+:[[:space:]]*/, "", val)
              gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
              gsub(/^"|"$/, "", val)
              key=val
            }
            END {
              if (!matched && key != "") {
                print key
              }
            }'
      )"
    fi
  fi

  if [ -z "$GATEWAY_API_KEY" ]; then
    echo "Gateway auth is enabled but no API key was found. Set GATEWAY_API_KEY or verify ${GATEWAY_NAMESPACE}/${GATEWAY_AUTH_SECRET}." >&2
    return 1
  fi

  GATEWAY_AUTH_CURL_ARGS=(-H "X-API-Key: ${GATEWAY_API_KEY}")
}

start_gateway_port_forward() {
  echo "Port-forwarding gateway on localhost:${GATEWAY_PORT}..." >&2
  if [ -n "${GATEWAY_PF_PID:-}" ]; then
    kill "$GATEWAY_PF_PID" >/dev/null 2>&1 || true
  fi
  kctl port-forward -n "$GATEWAY_NAMESPACE" "svc/${GATEWAY_SERVICE}" "${GATEWAY_PORT}:8080" >/tmp/xtrinode-gateway-port-forward.log 2>&1 &
  GATEWAY_PF_PID="$!"

  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  cat /tmp/xtrinode-gateway-port-forward.log || true
  return 1
}

ensure_gateway_port_forward() {
  if [ -n "${GATEWAY_PF_PID:-}" ] && kill -0 "$GATEWAY_PF_PID" >/dev/null 2>&1; then
    if curl -fsS "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
  fi

  echo "Restarting gateway port-forward..." >&2
  start_gateway_port_forward
}

wait_for_gateway_backend() {
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local code

  echo "Waiting for gateway to reach Trino /v1/info..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    ensure_gateway_port_forward
    code="$(curl -sS -o /dev/null -w "%{http_code}" \
      -H "X-Trino-User: smoke" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "${GATEWAY_AUTH_CURL_ARGS[@]}" \
      "http://127.0.0.1:${GATEWAY_PORT}/v1/info" || true)"
    if [ "$code" = "200" ]; then
      echo "  gateway backend /v1/info is ready"
      return 0
    fi
    sleep 5
  done

  echo "Timed out waiting for gateway backend; last HTTP status: ${code:-<empty>}"
  return 1
}

post_statement() {
  local sql="$1"
  local output="$2"
  ensure_gateway_port_forward
  curl -sS -o "$output" -w "%{http_code}" \
    -X POST "http://127.0.0.1:${GATEWAY_PORT}/v1/statement" \
    -H "X-Trino-User: smoke" \
    -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
    "${GATEWAY_AUTH_CURL_ARGS[@]}" \
    --data "$sql"
}

wait_for_statement_accepted() {
  local description="$1"
  local sql="$2"
  local output="$3"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local code=""

  echo "Waiting for ${description} statement to be accepted..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    code="$(post_statement "$sql" "$output" || true)"
    echo "  ${description} statement HTTP status: ${code:-<empty>}"
    if [ "$code" = "200" ]; then
      return 0
    fi
    cat "$output" || true
    sleep 5
  done

  echo "Timed out waiting for ${description} statement to be accepted" >&2
  return 1
}

drain_query() {
  local body="$1"
  local state next_uri path

  state="$(jq -r '.stats.state // .state // empty' "$body" 2>/dev/null || true)"
  next_uri="$(jq -r '.nextUri // empty' "$body" 2>/dev/null || true)"
  echo "Initial query state: ${state:-unknown}"

  while [ -n "$next_uri" ]; do
    path="$(printf '%s' "$next_uri" | sed -E 's#^https?://[^/]+##')"
    ensure_gateway_port_forward
    if ! curl -sS -o "$body" \
      -H "X-Trino-User: smoke" \
      -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
      "${GATEWAY_AUTH_CURL_ARGS[@]}" \
      "http://127.0.0.1:${GATEWAY_PORT}${path}"; then
      echo "Gateway request failed while draining query; retrying with a fresh port-forward..."
      start_gateway_port_forward
      curl -sS -o "$body" \
        -H "X-Trino-User: smoke" \
        -H "X-Trino-XTrinode: ${XTRINODE_NAME}" \
        "${GATEWAY_AUTH_CURL_ARGS[@]}" \
        "http://127.0.0.1:${GATEWAY_PORT}${path}"
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

is_worker_warmup_query_failure() {
  local body="$1"

  jq -e '.error.message? // "" | contains("Trino server is still initializing")' "$body" >/dev/null 2>&1
}

kctl create namespace "$NAMESPACE" --dry-run=client -o yaml | kctl apply -f -
load_gateway_auth
if [ "$RESET_SMOKE" = "true" ]; then
  echo "Resetting previous smoke resources..."
  kctl delete "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
  if ! kctl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=delete --timeout="${DELETE_WAIT_SECONDS}s" >/dev/null 2>&1; then
    echo "Previous XTrinode ${NAMESPACE}/${XTRINODE_NAME} did not delete cleanly."
    kctl get "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" -o yaml || true
    exit 1
  fi

  delete_smoke_resource xtrinodecatalog tpch-smoke
  delete_smoke_resource deployment "trino-${XTRINODE_NAME}-coordinator"
  delete_smoke_resource deployment "trino-${XTRINODE_NAME}-worker"
  delete_smoke_resource service "trino-${XTRINODE_NAME}"
  delete_smoke_resource service "trino-${XTRINODE_NAME}-coordinator"
  delete_smoke_resource configmap "trino-${XTRINODE_NAME}-config"
  delete_smoke_resource configmap trino-catalog-tpch-smoke
  delete_smoke_resource scaledobject "trino-${XTRINODE_NAME}-workers"
  delete_smoke_resource servicemonitor "trino-${XTRINODE_NAME}"
fi
kctl apply -f examples/xtrinode-keda-prometheus-query.yaml

wait_for_jsonpath "initial spec.suspended=false" \
  "xtrinode_suspended_state" \
  "false"
kctl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
kctl rollout status "deployment/trino-${XTRINODE_NAME}-coordinator" -n "$NAMESPACE" --timeout="${WAIT_TIMEOUT_SECONDS}s"
kctl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o yaml | sed -n '/triggers:/,/status:/p'
wait_for_jsonpath "gateway route registered" \
  "kctl get configmap trino-gateway-routes -n ${GATEWAY_NAMESPACE} -o jsonpath='{.data.routes\\.yaml}' | grep -q 'name: ${XTRINODE_NAME}' && echo ${XTRINODE_NAME}" \
  "$XTRINODE_NAME"

start_gateway_port_forward
wait_for_gateway_backend

query_body="$(mktemp)"
code="$(post_statement "$QUERY_SQL" "$query_body")"
echo "Initial statement HTTP status: ${code}"
if [ "$code" != "200" ]; then
  cat "$query_body"
  exit 1
fi

wait_for_deployment_available "trino-${XTRINODE_NAME}-worker" 1
if ! drain_query "$query_body"; then
  if ! is_worker_warmup_query_failure "$query_body"; then
    exit 1
  fi
  echo "Initial scale-trigger query hit worker warm-up; verifying query completion after worker startup..."
fi
echo "Pinning minWorkers=1 for post-scale query verification..."
kctl patch "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --type=merge -p '{"spec":{"minWorkers":1}}'
wait_for_jsonpath "ScaledObject minReplicaCount=1" \
  "kctl get scaledobject trino-${XTRINODE_NAME}-workers -n ${NAMESPACE} -o jsonpath='{.spec.minReplicaCount}'" \
  "1"
kctl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
kctl rollout status "deployment/trino-${XTRINODE_NAME}-coordinator" -n "$NAMESPACE" --timeout="${WAIT_TIMEOUT_SECONDS}s"
wait_for_deployment_available "trino-${XTRINODE_NAME}-worker" 1
wait_for_worker_server_started
wait_for_gateway_backend
verify_body="$(mktemp)"
wait_for_statement_accepted "verification" "$VERIFY_QUERY_SQL" "$verify_body"
drain_query "$verify_body"
echo "Restoring minWorkers=0 for KEDA scale-down verification..."
kctl patch "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --type=merge -p '{"spec":{"minWorkers":0}}'
wait_for_jsonpath "ScaledObject minReplicaCount=0" \
  "kctl get scaledobject trino-${XTRINODE_NAME}-workers -n ${NAMESPACE} -o jsonpath='{.spec.minReplicaCount}'" \
  "0"
wait_for_deployment_replicas "trino-${XTRINODE_NAME}-worker" 0

echo "Shortening autoSuspendAfter to 1m for the auto-suspend phase..."
kctl patch "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --type=merge -p '{"spec":{"autoSuspendAfter":"1m"}}'
echo "Waiting ${AUTO_SUSPEND_WAIT_SECONDS}s for auto-suspend window..."
sleep "$AUTO_SUSPEND_WAIT_SECONDS"
kctl annotate "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" "xtrinode.analytics.xtrinode.io/smoke-reconcile=$(date +%s)" --overwrite
wait_for_jsonpath "spec.suspended=true" \
  "xtrinode_suspended_state" \
  "true"

echo "Restoring autoSuspendAfter to 10m for the resume phase..."
kctl patch "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --type=merge -p '{"spec":{"autoSuspendAfter":"10m"}}'
resume_body="$(mktemp)"
resume_code="$(post_statement "SELECT 1" "$resume_body")"
echo "Resume-trigger statement HTTP status: ${resume_code}"
cat "$resume_body"
if [ "$resume_code" != "503" ]; then
  echo "Expected first request to suspended runtime to return 503 with retry-after."
  exit 1
fi

wait_for_jsonpath "spec.suspended=false" \
  "xtrinode_suspended_state" \
  "false"
kctl wait "xtrinode/${XTRINODE_NAME}" -n "$NAMESPACE" --for=condition=Ready=True --timeout="${WAIT_TIMEOUT_SECONDS}s"
kctl rollout status "deployment/trino-${XTRINODE_NAME}-coordinator" -n "$NAMESPACE" --timeout="${WAIT_TIMEOUT_SECONDS}s"
wait_for_gateway_backend

retry_body="$(mktemp)"
retry_code="$(post_statement "SELECT 1" "$retry_body")"
echo "Retry statement HTTP status: ${retry_code}"
cat "$retry_body" | jq '{id, state, stats: .stats.state, error}'
if [ "$retry_code" != "200" ]; then
  exit 1
fi

kctl get xtrinode "$XTRINODE_NAME" -n "$NAMESPACE" -o wide
kctl get deployment "trino-${XTRINODE_NAME}-coordinator" "trino-${XTRINODE_NAME}-worker" -n "$NAMESPACE" -o wide
kctl get scaledobject "trino-${XTRINODE_NAME}-workers" -n "$NAMESPACE" -o wide
