# XTrinode Repository Agent Guide

This is the single source of repository guidance for humans and coding agents. In this repository,
"agent" means a long-running XTrinode runtime process. Reusable Codex skills belong in separate
skill directories with a `SKILL.md`; the project itself should not keep root `SKILLS.md`, `.codex`,
`.agents`, or editor-specific rule directories.

## Repository Guidance Policy

- Keep durable project guidance in this file.
- Do not add package-local `AGENTS.md` files unless a subtree has materially different rules that
  cannot be explained here.
- Do not commit `.codex/`, `.agents/`, or `.cursor/`; those are local/editor state.
- Keep generated, local, and machine-specific files out of git.
- Prefer project conventions over new abstractions. Make focused changes and avoid unrelated
  cleanup in the same patch.

## System Agents

### Operator (`xtrinode/cmd/operator`)

Role: Kubernetes controller manager.

Responsibilities:

- Reconciles `XTrinode` CRs and creates or updates Trino Kubernetes resources.
- Reconciles `XTrinodeCatalog` CRs and catalog ConfigMaps.
- Manages KEDA `ScaledObject` resources for worker autoscaling.
- Drives auto-suspend by patching `spec.suspended = true` when a cluster is idle.
- Drives graceful shutdown by draining running queries before scale-down.
- Runs admission webhooks for `XTrinode` validation.
- Maintains status conditions and emits Kubernetes events.

Interacts with:

- Kubernetes API through controller-runtime.
- KEDA controller through `ScaledObject` CRDs.
- API Server for control-plane operations.

### API Server (`xtrinode/cmd/api-server`)

Role: REST gateway for operator control-plane operations.

Responsibilities:

- Exposes suspend, resume, status, health, and Prometheus metrics endpoints.
- Uses Kubernetes Lease objects to prevent resume stampedes.
- Receives resume and suspend requests from the Gateway and external clients.

Interacts with:

- Kubernetes API for `XTrinode` reads, patches, and Lease management.
- Gateway for upstream resume triggers.

### Gateway (`xtrinode/cmd/gateway`)

Role: HTTP reverse proxy for Trino query routing.

Responsibilities:

- Routes Trino HTTP requests by hostname, `X-Trino-XTrinode`, or default route.
- Maintains sticky query routing so follow-up requests reach the same backend.
- Load-balances across backends in the same routing group.
- Requests resume for suspended or sleeping XTrinodes when no backend is selectable or a coordinator
  connection fails. Plain Trino 503 responses get retry guidance and are not treated as proof of
  suspension.
- Enforces rate limits, circuit breaking, active health checks, and optional API key or bearer-token
  auth.
- Reloads routes dynamically from a ConfigMap.

Interacts with:

- Trino coordinator pods.
- API Server for resume and suspend requests.
- Kubernetes API for route ConfigMaps and auth Secrets.
- Redis when sticky routing or distributed rate limit state is externalized.

## Interaction Overview

```text
External Client / Trino CLI
        |
        v
   [Gateway :8080]
        |-- route lookup --> [ConfigMap: trino-gateway-routes]
        |-- sticky lookup -> [Redis or local LRU cache]
        |-- resume trigger -> [API Server :8081]
        |                          |
        |                          |-- acquire Lease ------> [Kubernetes API]
        |                          `-- patch XTrinode CR --> [Kubernetes API]
        |
        v
  [Trino Coordinator]
        |
        v
  [Trino Workers] <-- ScaledObject -- [KEDA Controller]
                                         ^
                                         |
                                    [Operator]
```

## Configuration Summary

| Agent | Default Port | Namespace | Key Config Source |
| --- | --- | --- | --- |
| Operator | 8081 health | `xtrinode-system` | CLI flags and `internal/config` |
| API Server | 8081 | `xtrinode-system` | CLI flags and `internal/config` |
| Gateway | 8080 | `xtrinode-gateway` | CLI flags and ConfigMap |

## Lifecycle Rules

- Operator leader election uses the `xtrinode-operator-leader-election` Lease.
- All agents handle `SIGINT` and `SIGTERM` with graceful drain timeouts.
- API Server resume operations are serialized with per-runtime or per-routing-group Lease objects.
- Operator auto-suspend checks `spec.autoSuspendAfter` during reconciliation.
- Gateway auto-resume calls the API Server when route state or connection errors indicate a backend
  should be resumed.

## Operator Module Wiring

The operator module hosts three binaries. Entrypoints should stay mostly wiring-only:

| Binary | Startup Pattern |
| --- | --- |
| `cmd/operator/main.go` | Configure scheme, controller-runtime manager, reconcilers, webhooks |
| `cmd/api-server/main.go` | Parse flags, build Kubernetes client, create server, drain on signal |
| `cmd/gateway/main.go` | Parse flags, create auth and route services, start proxy, drain on signal |

Operator dependencies are injected before registering reconcilers:

| Service | Interface | Implementation |
| --- | --- | --- |
| `NodePoolAdapter` | `controllers.NodePoolAdapterInterface` | `controllers.NewNodePoolAdapter` |
| `GatewayService` | `controllers.GatewayServiceInterface` | `controllers.NewGatewayService` |
| `KEDAService` | `controllers.KEDAServiceInterface` | `controllers.NewKEDAService` |
| `CatalogService` | `controllers.CatalogServiceInterface` | `controllers.NewCatalogService` |
| `TrinoResourcesService` | `controllers.TrinoResourcesServiceInterface` | `controllers.NewTrinoResourcesService` |
| `AutosuspendService` | `controllers.AutosuspendServiceInterface` | `controllers.NewAutosuspendService` |
| `GracefulShutdownService` | `controllers.GracefulShutdownServiceInterface` | `NewGracefulShutdownService` |

## Package Ownership

| Package | Responsibility |
| --- | --- |
| `api/v1` | CRD types, deepcopy generation, webhook validation, update warnings |
| `controllers` | Reconciliation, command handling, service wiring, node pool orchestration |
| `internal/trino/resources` | Kubernetes resource builders and apply/delete helpers |
| `internal/keda` | KEDA ScaledObject creation, enable/disable flows, scaler metadata |
| `internal/autosuspend` | Idle detection and suspend annotation/status updates |
| `internal/gracefulshutdown` | Query-drain checks and worker termination waiting |
| `internal/catalog` | Catalog ConfigMap discovery and secret reference extraction |
| `internal/config` | Shared constants, names, annotations, ports, timeouts |
| `internal/status`, `pkg/status` | Conditions, phases, and invariant helpers |
| `internal/httpclient`, `internal/retry`, `pkg/external` | Bounded external calls and retry helpers |
| `internal/rollout`, `internal/digest` | Content digest and rollout hash stamping |
| `pkg/api-server` | REST API, resume/suspend gates, Lease management |
| `pkg/gateway` | Reverse proxy, route cache, sticky routing, health checks, rate limiting |
| `pkg/gateway/auth` | Gateway API key and bearer-token authentication |
| `pkg/metrics` | Shared Prometheus metric definitions |

When changing `internal/trino/resources`, keep behavior aligned with the upstream Trino Helm chart
where practical, but prefer explicit Go builders and typed Kubernetes APIs over embedding Helm
templating semantics.

## Development Standards

- `cmd/<service>/main.go` is wiring only. Put business logic in `internal/` packages.
- Use `pkg/` only for libraries intended to be imported outside this module.
- Split by domain or responsibility, not by `helpers`, `utils`, or `common` dumping grounds.
- Prefer fewer cohesive packages until boundaries, cycles, or size justify splitting.
- Keep files readable. Around 200-550 LOC is the target for handwritten code.
- Export only what another package needs. Accept small interfaces and return concrete structs.
- Define interfaces on the consumer side unless the interface is truly shared.
- Pass `context.Context` as the first parameter for request-scoped work and I/O.
- Never store `context.Context` in structs and never pass a nil context.
- Wrap errors with useful context and `%w`; branch with `errors.Is` or `errors.As`.
- Keep HTTP handlers thin: parse, validate, call service, map result or error.
- Set timeouts for HTTP servers, HTTP clients, Kubernetes clients, and other outbound calls.
- Use table-driven tests where useful and keep unit tests fast and deterministic.
- Prefer injected clocks or explicit time control over sleeps in tests.
- Do not log secrets, tokens, credentials, or raw auth headers.

## Lint-Specific Go Rules

The project lint config expects these patterns:

- Use checked type assertions when the type is not statically guaranteed.
- Do not discard errors with `_`; handle or propagate them.
- Avoid shadowing an outer `err` with `:=` in nested scopes.
- Do not assign values that are immediately overwritten before being read.
- Use `result.RequeueAfter` instead of deprecated controller-runtime `result.Requeue` in new code.
- Simplify boolean conditionals such as `if x { return true }; return false` to `return x`.
- Run `gofmt` on changed Go files.

## Primary Tooling

- Root `Makefile` is the developer entrypoint for build, test, lint, Docker, Helm, and Terraform.
- CI is defined in `.github/workflows/ci.yml`.
- Release automation is defined in `.github/workflows/release.yml`.
- Local e2e suites are Makefile/Tilt workflows for explicit developer runs; they are not scheduled
  as a GitHub Actions workflow.
- Ownership and release-gating expectations are documented in `.github/CODEOWNERS` and
  `docs/CODEOWNERS.md`.
