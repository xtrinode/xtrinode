#!/usr/bin/env bash
# Local k3d operator reconcile stress: lightweight suspended runtimes and catalog churn.
set -euo pipefail

NAMESPACE="${NAMESPACE:-team-operator-stress}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-xtrinode-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-xtrinode-gateway}"
OPERATOR_SERVICE="${OPERATOR_SERVICE:-xtrinode-operator}"
OPERATOR_STRESS_COUNT="${OPERATOR_STRESS_COUNT:-12}"
OPERATOR_STRESS_PATCH_ROUNDS="${OPERATOR_STRESS_PATCH_ROUNDS:-3}"
OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS="${OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS:-240}"
OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA="${OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA:-0}"
OPERATOR_STRESS_METRICS_PORT="${OPERATOR_STRESS_METRICS_PORT:-18082}"
OPERATOR_STRESS_CLEANUP_ON_SUCCESS="${OPERATOR_STRESS_CLEANUP_ON_SUCCESS:-true}"

LABEL_KEY="stress.xtrinode.io/run"
LABEL_VALUE="local-operator"
LABEL_SELECTOR="${LABEL_KEY}=${LABEL_VALUE}"
RESOURCE_PREFIX="op-stress"

METRICS_BEFORE=""
METRICS_AFTER=""
METRICS_PF_PID=""

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $1" >&2
    exit 1
  fi
}

require_int() {
  local name="$1"
  local value="$2"
  if [[ -z "$value" || "$value" =~ [^0-9] ]]; then
    echo "ERROR: ${name} must be a non-negative integer, got ${value:-<empty>}" >&2
    exit 1
  fi
}

cleanup_processes() {
  if [ -n "${METRICS_PF_PID:-}" ]; then
    kill "$METRICS_PF_PID" >/dev/null 2>&1 || true
    wait "$METRICS_PF_PID" >/dev/null 2>&1 || true
  fi
  [ -n "${METRICS_BEFORE:-}" ] && rm -f "$METRICS_BEFORE"
  [ -n "${METRICS_AFTER:-}" ] && rm -f "$METRICS_AFTER"
}

on_exit() {
  local exit_status=$?
  if [ "$exit_status" -ne 0 ]; then
    dump_debug || true
  fi
  cleanup_processes
  exit "$exit_status"
}

namespace_exists() {
  kubectl get namespace "$NAMESPACE" >/dev/null 2>&1
}

ensure_namespace() {
  if ! namespace_exists; then
    kubectl create namespace "$NAMESPACE"
  fi
}

wait_for_resource_count() {
  local kind="$1"
  local expected="$2"
  local timeout_seconds="${3:-90}"
  local deadline=$((SECONDS + timeout_seconds))
  local count=""

  while [ "$SECONDS" -lt "$deadline" ]; do
    count="$(kubectl get "$kind" -n "$NAMESPACE" -l "$LABEL_SELECTOR" --ignore-not-found -o json \
      | jq 'if type == "array" then length else (.items // []) | length end')"
    count="${count:-0}"
    if [ "$count" -eq "$expected" ]; then
      return 0
    fi
    sleep 2
  done

  echo "Timed out waiting for ${kind} count ${expected}; last count=${count:-unknown}" >&2
  return 1
}

force_remove_xtrinode_finalizers() {
  local resource=""
  kubectl get xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" --ignore-not-found -o name \
    | while read -r resource; do
        [ -z "$resource" ] && continue
        kubectl patch "$resource" -n "$NAMESPACE" --type=merge -p '{"metadata":{"finalizers":[]}}' >/dev/null || true
      done
}

cleanup_labeled_resources() {
  if ! namespace_exists; then
    return 0
  fi

  echo "Cleaning operator stress resources in namespace ${NAMESPACE}..."
  kubectl delete xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" --wait=false --ignore-not-found=true >/dev/null || true
  kubectl delete xtrinodecatalog -n "$NAMESPACE" -l "$LABEL_SELECTOR" --wait=false --ignore-not-found=true >/dev/null || true
  kubectl delete configmap -n "$NAMESPACE" -l "$LABEL_SELECTOR" --ignore-not-found=true >/dev/null || true

  if ! wait_for_resource_count xtrinode 0 90; then
    echo "Forcing finalizer removal for labeled stress XTrinode resources..." >&2
    force_remove_xtrinode_finalizers
    kubectl delete xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" --wait=false --ignore-not-found=true >/dev/null || true
    wait_for_resource_count xtrinode 0 60
  fi
  wait_for_resource_count xtrinodecatalog 0 60
}

idx_name() {
  printf "%s-%03d" "$RESOURCE_PREFIX" "$1"
}

catalog_name() {
  printf "%s-cat-%03d" "$RESOURCE_PREFIX" "$1"
}

create_catalog() {
  local index="$1"
  local name
  name="$(catalog_name "$index")"
  kubectl apply -f - <<EOF
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    ${LABEL_KEY}: ${LABEL_VALUE}
spec:
  labels:
    ${LABEL_KEY}: ${LABEL_VALUE}
  connector:
    tpch:
      properties:
        tpch.splits-per-node: "1"
EOF
}

create_xtrinode() {
  local index="$1"
  local name
  name="$(idx_name "$index")"
  kubectl apply -f - <<EOF
apiVersion: analytics.xtrinode.io/v1
kind: XTrinode
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    ${LABEL_KEY}: ${LABEL_VALUE}
spec:
  size: xs
  minWorkers: 0
  maxWorkers: 1
  suspended: true
  autoSuspendAfter: "5m"
  wakeMinWorkers: 0
  wakeTTL: "1m"
  routing:
    header: "X-Trino-XTrinode=${name}"
    routingGroup: "${name}"
EOF
}

create_resources() {
  local i=""
  echo "Creating ${OPERATOR_STRESS_COUNT} suspended XTrinode resources and catalogs..."
  for i in $(seq 1 "$OPERATOR_STRESS_COUNT"); do
    create_catalog "$i"
    create_xtrinode "$i"
  done
}

patch_resources() {
  local round="$1"
  local i=""
  local ttl_minutes=$((round + 1))
  local auto_suspend_minutes=$((round + 5))
  local splits_per_node=$((round + 1))

  echo "Patch round ${round}/${OPERATOR_STRESS_PATCH_ROUNDS}..."
  for i in $(seq 1 "$OPERATOR_STRESS_COUNT"); do
    local runtime_name
    local cat_name
    runtime_name="$(idx_name "$i")"
    cat_name="$(catalog_name "$i")"
    kubectl patch xtrinode "$runtime_name" -n "$NAMESPACE" --type=merge \
      -p "{\"metadata\":{\"annotations\":{\"stress.xtrinode.io/round\":\"${round}\"}},\"spec\":{\"wakeTTL\":\"${ttl_minutes}m\",\"autoSuspendAfter\":\"${auto_suspend_minutes}m\",\"wakeMinWorkers\":0}}" >/dev/null
    kubectl patch xtrinodecatalog "$cat_name" -n "$NAMESPACE" --type=merge \
      -p "{\"metadata\":{\"annotations\":{\"stress.xtrinode.io/round\":\"${round}\"}},\"spec\":{\"connector\":{\"tpch\":{\"properties\":{\"tpch.splits-per-node\":\"${splits_per_node}\"}}}}}" >/dev/null
  done
}

wait_for_xtrinodes_suspended() {
  local deadline=$((SECONDS + OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS))
  local total=0
  local suspended=0
  local error_count=0

  echo "Waiting for all stress XTrinode resources to reach Suspended..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    read -r total suspended error_count < <(
      kubectl get xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o json \
        | jq -r '(if type == "array" then . else (.items // []) end) as $items | [$items | length, ([$items[] | select(.status.phase == "Suspended")] | length), ([$items[] | select(.status.phase == "Error")] | length)] | @tsv'
    )
    if [ "$total" -eq "$OPERATOR_STRESS_COUNT" ] && [ "$suspended" -eq "$OPERATOR_STRESS_COUNT" ]; then
      return 0
    fi
    if [ "$error_count" -gt 0 ]; then
      echo "ERROR: ${error_count} stress XTrinode resources reached Error phase" >&2
      kubectl get xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o wide
      return 1
    fi
    sleep 3
  done

  echo "Timed out waiting for Suspended resources; total=${total}, suspended=${suspended}, errors=${error_count}" >&2
  kubectl get xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o yaml
  return 1
}

wait_for_catalogs_ready() {
  local deadline=$((SECONDS + OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS))
  local total=0
  local ready=0
  local error_count=0

  echo "Waiting for all stress catalogs to reach Ready..."
  while [ "$SECONDS" -lt "$deadline" ]; do
    read -r total ready error_count < <(
      kubectl get xtrinodecatalog -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o json \
        | jq -r '(if type == "array" then . else (.items // []) end) as $items | [$items | length, ([$items[] | select(.status.phase == "Ready")] | length), ([$items[] | select(.status.phase == "Error")] | length)] | @tsv'
    )
    if [ "$total" -eq "$OPERATOR_STRESS_COUNT" ] && [ "$ready" -eq "$OPERATOR_STRESS_COUNT" ]; then
      return 0
    fi
    if [ "$error_count" -gt 0 ]; then
      echo "ERROR: ${error_count} stress catalogs reached Error phase" >&2
      kubectl get xtrinodecatalog -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o wide
      return 1
    fi
    sleep 3
  done

  echo "Timed out waiting for Ready catalogs; total=${total}, ready=${ready}, errors=${error_count}" >&2
  kubectl get xtrinodecatalog -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o yaml
  return 1
}

start_metrics_port_forward() {
  local log_file="/tmp/xtrinode-operator-stress-metrics-port-forward.log"
  rm -f "$log_file"
  kubectl port-forward -n "$OPERATOR_NAMESPACE" "svc/${OPERATOR_SERVICE}" "${OPERATOR_STRESS_METRICS_PORT}:8080" \
    >"$log_file" 2>&1 &
  METRICS_PF_PID=$!

  for _ in $(seq 1 45); do
    if curl -fsS "http://127.0.0.1:${OPERATOR_STRESS_METRICS_PORT}/metrics" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "ERROR: timed out waiting for operator metrics port-forward" >&2
  cat "$log_file" >&2 || true
  return 1
}

capture_metrics() {
  local output_file="$1"
  curl -fsS "http://127.0.0.1:${OPERATOR_STRESS_METRICS_PORT}/metrics" >"$output_file"
}

metric_sum() {
  local file="$1"
  local metric="$2"
  local result="${3:-}"

  awk \
    -v metric="$metric" \
    -v namespace_label="namespace=\"${NAMESPACE}\"" \
    -v name_prefix="name=\"${RESOURCE_PREFIX}-" \
    -v result_label="${result:+result=\"${result}\"}" \
    '
      $1 ~ "^" metric "\\{" &&
      index($0, namespace_label) &&
      index($0, name_prefix) &&
      (result_label == "" || index($0, result_label)) {
        sum += $NF
      }
      END { printf "%.0f\n", sum + 0 }
    ' "$file"
}

operator_restart_sum() {
  kubectl get pods -n "$OPERATOR_NAMESPACE" -l app.kubernetes.io/name=xtrinode-operator -o json \
    | jq '[.items[].status.containerStatuses[]?.restartCount] | add // 0'
}

assert_operator_available() {
  kubectl wait deployment/xtrinode-operator -n "$OPERATOR_NAMESPACE" --for=condition=Available \
    --timeout="${OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS}s"
}

assert_metrics() {
  local success_before="$1"
  local success_after="$2"
  local errors_before="$3"
  local errors_after="$4"
  local reconcile_delta=$((success_after - success_before))
  local error_delta=$((errors_after - errors_before))

  echo "Operator stress reconcile observation delta: ${reconcile_delta}"
  echo "Operator stress reconcile error delta: ${error_delta}"

  if [ "$reconcile_delta" -lt "$OPERATOR_STRESS_COUNT" ]; then
    echo "ERROR: expected at least ${OPERATOR_STRESS_COUNT} observed stress reconciles, got ${reconcile_delta}" >&2
    return 1
  fi
  if [ "$error_delta" -gt "$OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA" ]; then
    echo "ERROR: expected reconcile error delta <= ${OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA}, got ${error_delta}" >&2
    return 1
  fi
}

dump_debug() {
  echo "=== Operator stress debug ===" >&2
  kubectl get pods -n "$OPERATOR_NAMESPACE" -o wide >&2 || true
  kubectl get pods -n "$GATEWAY_NAMESPACE" -o wide >&2 || true
  if namespace_exists; then
    kubectl get xtrinode -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o wide >&2 || true
    kubectl get xtrinodecatalog -n "$NAMESPACE" -l "$LABEL_SELECTOR" -o wide >&2 || true
    kubectl get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
  fi
  kubectl logs -n "$OPERATOR_NAMESPACE" deployment/xtrinode-operator --tail=160 >&2 || true
}

run_stress() {
  require_cmd kubectl
  require_cmd jq
  require_cmd curl
  require_int OPERATOR_STRESS_COUNT "$OPERATOR_STRESS_COUNT"
  require_int OPERATOR_STRESS_PATCH_ROUNDS "$OPERATOR_STRESS_PATCH_ROUNDS"
  require_int OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS "$OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS"
  require_int OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA "$OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA"
  require_int OPERATOR_STRESS_METRICS_PORT "$OPERATOR_STRESS_METRICS_PORT"

  METRICS_BEFORE="$(mktemp /tmp/xtrinode-operator-stress-before.XXXXXX)"
  METRICS_AFTER="$(mktemp /tmp/xtrinode-operator-stress-after.XXXXXX)"

  assert_operator_available
  ensure_namespace
  cleanup_labeled_resources
  start_metrics_port_forward

  local restarts_before
  local restarts_after
  local success_before
  local success_after
  local errors_before
  local errors_after
  local round=""

  restarts_before="$(operator_restart_sum)"
  capture_metrics "$METRICS_BEFORE"
  success_before="$(metric_sum "$METRICS_BEFORE" xtrinode_reconcile_duration_seconds_count)"
  errors_before="$(metric_sum "$METRICS_BEFORE" xtrinode_reconcile_errors_total)"

  create_resources
  wait_for_catalogs_ready
  wait_for_xtrinodes_suspended

  for round in $(seq 1 "$OPERATOR_STRESS_PATCH_ROUNDS"); do
    patch_resources "$round"
    wait_for_catalogs_ready
    wait_for_xtrinodes_suspended
  done

  assert_operator_available
  restarts_after="$(operator_restart_sum)"
  if [ "$restarts_after" -ne "$restarts_before" ]; then
    echo "ERROR: operator restart count changed from ${restarts_before} to ${restarts_after}" >&2
    return 1
  fi

  capture_metrics "$METRICS_AFTER"
  success_after="$(metric_sum "$METRICS_AFTER" xtrinode_reconcile_duration_seconds_count)"
  errors_after="$(metric_sum "$METRICS_AFTER" xtrinode_reconcile_errors_total)"
  assert_metrics "$success_before" "$success_after" "$errors_before" "$errors_after"

  if [ "$OPERATOR_STRESS_CLEANUP_ON_SUCCESS" = "true" ]; then
    cleanup_labeled_resources
  fi

  echo "Operator stress completed: count=${OPERATOR_STRESS_COUNT}, patch_rounds=${OPERATOR_STRESS_PATCH_ROUNDS}"
}

main() {
  local command="${1:-run}"
  case "$command" in
    run)
      trap on_exit EXIT
      run_stress
      ;;
    cleanup)
      require_cmd kubectl
      require_cmd jq
      cleanup_labeled_resources
      ;;
    debug)
      require_cmd kubectl
      dump_debug
      ;;
    *)
      echo "usage: $0 [run|cleanup|debug]" >&2
      exit 2
      ;;
  esac
}

main "$@"
