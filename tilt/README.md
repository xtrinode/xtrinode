# Local Development With k3d, Tilt, and Real Trino

This workflow runs the XTrinode control plane locally with Docker, k3d, Helm, KEDA, the gateway, and a real Trino
coordinator/worker pair.

## Prerequisites

- Docker
- kubectl
- Helm
- jq
- Tilt, for the interactive dev loop

k3d must be installed separately or passed with `K3D=/path/to/k3d`. `make ensure-k3d` only verifies availability:

```bash
make ensure-k3d
```

## Headless E2E

Run the full local stack and all local Robot e2e suites:

```bash
make test-e2e-local
```

This creates a `k3d-xtrinode-dev` cluster, creates a local registry on `localhost:5001`, builds and pushes the
operator/API server/gateway images, installs the Helm charts, applies
`tilt/examples/postgres-analytics.yaml` and `tilt/examples/xtrinode-real-trino-keda.yaml`, and verifies:

- CRD reconciliation
- KEDA admission/webhook behavior that is installed locally
- Redis health and gateway Redis wiring
- real Trino coordinator and worker rollout
- local Postgres fixture readiness
- KEDA `ScaledObject` creation
- gateway route registration
- reconciliation after manual deployment and gateway route ConfigMap drift
- TPCH and PostgreSQL catalog ConfigMap generation and catalog mounts
- PostgreSQL catalog password injection through a Kubernetes Secret
- rollouts from mounted external ConfigMap and Secret data changes
- PostgreSQL queries routed through the gateway
- API server success and error contracts
- gateway success and missing-route status codes
- query routing through the gateway
- suspend through the API server
- gateway-triggered resume
- lease-gated duplicate resume requests
- interrupted lifecycle cleanup for gateway routes, KEDA handoff, suspend, and resume
- opt-in query-driven KEDA scale-out above one worker

Run only the faster contract suites when you want to check the deployed XTrinode stack without forcing a full suspend/resume
cycle:

```bash
make test-e2e-local-contracts
```

It verifies CRDs, KEDA deployments, KEDA admission rejection for invalid memory scale-to-zero, Redis health, gateway
Redis wiring, the rendered Trino image, catalog ConfigMaps, gateway route config, API health/status/list shape, API error
contracts for missing resources, bad methods, bad paths, invalid namespaces, invalid actions, invalid JSON bodies,
gateway `/v1/info` routing, and gateway missing-route 404s.

Run only the real-Trino lifecycle smoke suite:

```bash
make test-e2e-local-smoke
```

Run the heavier real-Trino KEDA scale-out suite:

```bash
make test-e2e-local-scaleout
```

This patches the local XTrinode to `maxWorkers: 2`, switches KEDA to query-based `metrics-api` scaling, starts a long
TPCH query, waits for the worker Deployment to scale from 1 to 2, cancels the query, and waits for KEDA to return the
Deployment to 1 worker.

Run the live interrupted lifecycle cleanup suite:

```bash
make test-e2e-local-lifecycle-cleanup
```

This suite is tagged `lifecycle-cleanup`. It requires the local k3d stack with the operator, API server, gateway, KEDA,
Redis, Postgres, and the usual Robot prerequisites (`kubectl`, `curl`, and `jq`). It intentionally scales the operator
deployment to zero around route, KEDA, suspend, and resume transitions, then verifies reconciliation repairs stale route
state, restores the KEDA `ScaledObject`, clears lifecycle command annotations, and leaves the backend routable.

Run only the local PostgreSQL catalog integration suite:

```bash
make test-e2e-local-postgres
```

This deploys the local Postgres fixture, mounts the generated `postgres` catalog into the XTrinode Trino pods, verifies
the password placeholder is backed by `postgres-credentials`, and runs `SELECT count(*) FROM postgres.public.orders`
through the gateway.

Run the local headless Locust load-test and autoscaling e2e:

```bash
make test-e2e-local-loadtest
```

The load-test is wrapped by Robot and uses the same uv project as the e2e suites. It first runs a short
rate-limit-friendly HTTP stats smoke, then switches the local XTrinode to query-metric KEDA scaling and proves Locust
traffic drives the worker Deployment from 1 to 2 replicas before scaling back down. Tune the smoke with
`LOADTEST_USERS`, `LOADTEST_SPAWN_RATE`, `LOADTEST_RUN_TIME`, `LOADTEST_WAIT_MIN`, `LOADTEST_WAIT_MAX`,
`LOADTEST_MIN_REQUESTS`, and `LOADTEST_QUERY`. Tune the autoscale phase with `LOADTEST_AUTOSCALE_USERS`,
`LOADTEST_AUTOSCALE_SPAWN_RATE`, `LOADTEST_AUTOSCALE_RUN_TIME`, `LOADTEST_AUTOSCALE_WAIT_SECONDS`,
`LOADTEST_AUTOSCALE_MAX_WORKERS`, `LOADTEST_AUTOSCALE_THRESHOLD`, `LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS`, and
`LOADTEST_AUTOSCALE_QUERY`.

Run the local operator reconcile stress suite:

```bash
make test-e2e-local-operator-stress
```

This uses the same local k3d control plane but keeps workload pressure low: it creates lightweight suspended
`XTrinode` resources and `XTrinodeCatalog` resources, patches them in rounds, then verifies they converge without
operator restarts or reconcile error deltas. Tune it with `OPERATOR_STRESS_NAMESPACE`, `OPERATOR_STRESS_COUNT`,
`OPERATOR_STRESS_PATCH_ROUNDS`, `OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS`, `OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA`,
and `OPERATOR_STRESS_METRICS_PORT`.

Delete the local cluster:

```bash
make k3d-down
```

## Tilt Dev Loop

Start the cluster first, then Tilt:

```bash
make k3d-up
make tilt-up
```

Tilt builds the four local images and deploys the Helm charts with local development values from
`tilt/deployments/values/`. It exposes:

- Gateway: `http://127.0.0.1:18080`
- API server: `http://127.0.0.1:18081`

The Tilt e2e resources are manual. Trigger them from the Tilt UI when you want local validation.

The Tiltfile intentionally keeps orchestration in shell scripts and uses Tilt for image builds, YAML rendering, resource
grouping, port-forwards, and manual e2e triggers. That keeps the same scripts reusable from Make and CI while still
giving Tilt a good interactive workflow.

Useful Tilt args:

```bash
tilt up -f tilt/Tiltfile -- --image_tag=tilt --trino_tag=480
```

Manual Tilt e2e resources:

- `local-e2e-contracts`
- `local-e2e-smoke`
- `local-e2e-scaleout`
- `local-e2e-lifecycle-cleanup`
- `local-e2e-all`
- `local-loadtest`
- `local-operator-stress`

## Robot E2E

Robot Framework is the canonical local e2e test surface. The suites live under `tilt/e2e/robot/` and are split by
behavior:

- `00_control_plane_contracts.robot`
- `10_xtrinode_resource_contracts.robot`
- `15_gateway_namespace_routing_contracts.robot`
- `16_namespace_guardrail_contracts.robot`
- `17_privileged_admission_contracts.robot`
- `18_reconciliation_edge_contracts.robot`
- `20_api_gateway_contracts.robot`
- `25_api_server_auth_integration.robot`
- `26_gateway_auth_integration.robot`
- `30_real_trino_lifecycle.robot`
- `35_real_trino_wake_ttl.robot`
- `36_trino_password_lifecycle_auth.robot`
- `38_lifecycle_cleanup_interruptions.robot`
- `40_real_trino_scaleout.robot`
- `45_prometheus_autoscaler.robot`
- `50_postgres_catalog.robot`
- `55_gateway_process_contracts.robot`

Shell stays in `tilt/e2e/helpers/` only for the full real-Trino lifecycle helper, where process cleanup and query draining
are more practical than pure Robot keywords.

```bash
uv run --project tilt/e2e robot --version
make test-e2e-local
```

## Notes

- Local e2e intentionally uses real `trinodb/trino:480`, one coordinator, one baseline worker, and low resource
  overrides.
- Local e2e also deploys `postgres:16-alpine` with an in-memory `runtime` database and a small `public.orders`
  dataset for catalog wiring and load-test queries.
- Local startup preloads third-party images into the k3d nodes before deploying the stack, including KEDA,
  Prometheus, Redis, Postgres, Python process backends, Trino, and the digest-pinned JMX exporter. This keeps the
  Robot suites from depending on k3s node-side registry DNS during test execution.
- KEDA is enabled in the operator chart and in the local XTrinode. The lifecycle smoke keeps `maxWorkers: 1`; the
  separate scale-out suite opts into `maxWorkers: 2` so the default loop stays light while the heavier path still proves
  metric-driven scaling above one worker.
- The lifecycle smoke covers operator-driven suspend and gateway/API-driven resume from 0 to 1 worker. The scale-out
  suite covers KEDA query metrics driving the worker Deployment from 1 to 2 and back to 1.
- The local operator chart installs the XTrinode mutating/validating admission webhooks, and the contract suite verifies
  invalid XTrinode specs are rejected through the Kubernetes API.
- Cloud node pool/CAPI tests remain separate. k3d validates the local Kubernetes behavior, not real cloud provider node
  pools.
