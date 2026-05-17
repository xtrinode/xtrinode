#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

require_grep() {
  local pattern="$1"
  local file="$2"
  if ! grep -q -- "${pattern}" "${file}"; then
    fail "expected pattern '${pattern}' in ${file}"
  fi
}

reject_grep() {
  local pattern="$1"
  local file="$2"
  if grep -q -- "${pattern}" "${file}"; then
    fail "unexpected pattern '${pattern}' in ${file}"
  fi
}

gateway_default="${tmpdir}/gateway-default.yaml"
helm template xtrinode-gateway helm/xtrinode-gateway > "${gateway_default}"
require_grep "minAvailable: 0" "${gateway_default}"
require_grep "--read-header-timeout=5s" "${gateway_default}"
require_grep "--read-timeout=0s" "${gateway_default}"
require_grep "--write-timeout=0s" "${gateway_default}"
require_grep "--idle-timeout=60s" "${gateway_default}"

gateway_custom="${tmpdir}/gateway-custom-http.yaml"
helm template xtrinode-gateway helm/xtrinode-gateway \
  --set gateway.http.readHeaderTimeout=9s \
  --set gateway.http.readTimeout=7s \
  --set gateway.http.writeTimeout=0s \
  --set gateway.http.idleTimeout=75s > "${gateway_custom}"
require_grep "--read-header-timeout=9s" "${gateway_custom}"
require_grep "--read-timeout=7s" "${gateway_custom}"
require_grep "--write-timeout=0s" "${gateway_custom}"
require_grep "--idle-timeout=75s" "${gateway_custom}"

if helm template xtrinode-gateway helm/xtrinode-gateway --set replicaCount=2 \
  > "${tmpdir}/gateway-replica-no-redis.out" 2> "${tmpdir}/gateway-replica-no-redis.err"; then
  fail "gateway replicaCount=2 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/gateway-replica-no-redis.err"

helm template xtrinode-gateway helm/xtrinode-gateway \
  --set replicaCount=2 \
  --set gateway.redis.enabled=true > "${tmpdir}/gateway-replica-redis.yaml"
require_grep "name: wait-for-redis" "${tmpdir}/gateway-replica-redis.yaml"

if helm template xtrinode-gateway helm/xtrinode-gateway --set autoscaling.enabled=true \
  > "${tmpdir}/gateway-hpa-no-redis.out" 2> "${tmpdir}/gateway-hpa-no-redis.err"; then
  fail "gateway HPA with maxReplicas > 1 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/gateway-hpa-no-redis.err"

helm template xtrinode-gateway helm/xtrinode-gateway \
  --set autoscaling.enabled=true \
  --set gateway.redis.enabled=true > "${tmpdir}/gateway-hpa-redis.yaml"

if helm template xtrinode-gateway helm/xtrinode-gateway \
  --set keda.enabled=true \
  --set keda.cpu.enabled=true \
  > "${tmpdir}/gateway-keda-no-redis.out" 2> "${tmpdir}/gateway-keda-no-redis.err"; then
  fail "gateway KEDA with maxReplicas > 1 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/gateway-keda-no-redis.err"

helm template xtrinode-gateway helm/xtrinode-gateway \
  --set keda.enabled=true \
  --set keda.cpu.enabled=true \
  --set gateway.redis.enabled=true > "${tmpdir}/gateway-keda-redis.yaml"

if helm template xtrinode-gateway helm/xtrinode-gateway \
  --set gateway.auth.enabled=true \
  --set gateway.auth.type=oauth \
  --set gateway.auth.oauth.issuer=https://issuer.example \
  > "${tmpdir}/gateway-oauth-no-audience.out" 2> "${tmpdir}/gateway-oauth-no-audience.err"; then
  fail "OAuth gateway auth without an audience should fail rendering"
fi
require_grep "gateway.auth.oauth.audience is required" "${tmpdir}/gateway-oauth-no-audience.err"

gateway_oauth="${tmpdir}/gateway-oauth.yaml"
helm template xtrinode-gateway helm/xtrinode-gateway \
  --set gateway.auth.enabled=true \
  --set gateway.auth.type=oauth \
  --set gateway.auth.oauth.issuer=https://issuer.example \
  --set gateway.auth.oauth.audience=trino > "${gateway_oauth}"
require_grep "--auth-oauth-audience=trino" "${gateway_oauth}"

helm dependency build helm/xtrinode --skip-refresh >/dev/null

umbrella_default="${tmpdir}/umbrella-default.yaml"
helm template xtrinode helm/xtrinode > "${umbrella_default}"
require_grep "--read-header-timeout=5s" "${umbrella_default}"
require_grep "--read-timeout=0s" "${umbrella_default}"
require_grep "--write-timeout=0s" "${umbrella_default}"
require_grep "--idle-timeout=60s" "${umbrella_default}"

if helm template xtrinode helm/xtrinode --set xtrinode-gateway.replicaCount=2 \
  > "${tmpdir}/umbrella-replica-no-redis.out" 2> "${tmpdir}/umbrella-replica-no-redis.err"; then
  fail "umbrella gateway replicaCount=2 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/umbrella-replica-no-redis.err"

helm template xtrinode helm/xtrinode \
  --set xtrinode-gateway.replicaCount=2 \
  --set xtrinode-gateway.gateway.redis.enabled=true > "${tmpdir}/umbrella-replica-redis.yaml"

if helm template xtrinode helm/xtrinode --set xtrinode-gateway.autoscaling.enabled=true \
  > "${tmpdir}/umbrella-hpa-no-redis.out" 2> "${tmpdir}/umbrella-hpa-no-redis.err"; then
  fail "umbrella gateway HPA with maxReplicas > 1 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/umbrella-hpa-no-redis.err"

helm template xtrinode helm/xtrinode \
  --set xtrinode-gateway.autoscaling.enabled=true \
  --set xtrinode-gateway.gateway.redis.enabled=true > "${tmpdir}/umbrella-hpa-redis.yaml"

if helm template xtrinode helm/xtrinode \
  --set xtrinode-gateway.keda.enabled=true \
  --set xtrinode-gateway.keda.cpu.enabled=true \
  > "${tmpdir}/umbrella-keda-no-redis.out" 2> "${tmpdir}/umbrella-keda-no-redis.err"; then
  fail "umbrella gateway KEDA with maxReplicas > 1 without Redis should fail rendering"
fi
require_grep "multiple replicas require gateway.redis.enabled=true or redis.enabled=true" "${tmpdir}/umbrella-keda-no-redis.err"

helm template xtrinode helm/xtrinode \
  --set xtrinode-gateway.keda.enabled=true \
  --set xtrinode-gateway.keda.cpu.enabled=true \
  --set xtrinode-gateway.gateway.redis.enabled=true > "${tmpdir}/umbrella-keda-redis.yaml"

operator_pdb="${tmpdir}/operator-pdb.yaml"
helm template xtrinode-operator helm/xtrinode-operator \
  --show-only templates/poddisruptionbudget.yaml > "${operator_pdb}"
require_grep "minAvailable: 0" "${operator_pdb}"

keda_clusterrole="${tmpdir}/keda-clusterrole.yaml"
helm template xtrinode-operator helm/xtrinode-operator \
  --show-only charts/keda/templates/manager/clusterrole.yaml > "${keda_clusterrole}"
reject_grep '"\*/scale"' "${keda_clusterrole}"
reject_grep '  - "\*"' "${keda_clusterrole}"
require_grep "deployments/scale" "${keda_clusterrole}"
require_grep "statefulsets/scale" "${keda_clusterrole}"
require_grep "secrets" "${keda_clusterrole}"

make -n security-scan-fs | grep -q -- "--ignorefile .trivyignore.yaml" ||
  fail "security-scan-fs should pass .trivyignore.yaml to Trivy"
make -n security-scan-config | grep -q -- "--ignorefile .trivyignore.yaml" ||
  fail "security-scan-config should pass .trivyignore.yaml to Trivy"
