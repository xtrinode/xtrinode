# Runtime Remaining Work

## Status

Follow-up tracker only.

The current implemented runtime, gateway, guardrail, node-pool, overlay, and
admission behavior is documented in [ARCHITECTURE.md](ARCHITECTURE.md). This
document intentionally records remaining work and non-blocking cleanup so those
items do not duplicate current-state architecture.

## Priority Summary

| Order | Work | Why Next |
| --- | --- | --- |
| 1 | Stronger scheduling and node-pool fit diagnosis | Current conditions catch common blockers, but provider capacity, DaemonSet overhead, quota, taints, and provisioning failures need better inputs. |
| 2 | Lifecycle ordering table | Route-state behavior and race e2e exist; operators still need a compact suspend/resume/autosuspend/KEDA/lease ordering reference. |
| 3 | Node-pool spec-removal policy | XTrinode deletion policies exist; removing `spec.nodePool` from a live object still needs explicit semantics. |
| 4 | Overlay external policy examples | Built-in denies are portable; local registry, Secret, annotation, and node-label rules belong in optional policy examples. |
| 5 | Admission webhook operations guide | Fail-closed is the right default; recovery and rollout guidance should be documented. |
| 6 | Custom/profile sizing APIs | Add `size: custom` or `sizingProfileRef` only after versioning and rollout semantics are clear. |

## Runtime Shape And Scheduling

Implemented baseline:

- `spec.size` selects the preset baseline.
- `spec.resources`, `spec.placement`, and `spec.routing.capacityUnits` define
  typed runtime-shape overrides.
- `spec.nodePool.schedulePods=true` binds pods to the managed node pool.
- `spec.placement.existingNodePool` expands common provider pool names to
  documented node selectors.
- Guardrails, gateway routes, API-server resume ranking, pod rendering, and
  status consume the same resolved shape.
- `status.observedRuntimeShape` is compact and versioned.
- `SchedulingReady`, `PlacementReady`, `TaintsReady`, `QuotaReady`,
  `CapacityReady`, and `NodePoolFitReady` report common blockers.
- Common Trino per-node memory settings are validated against resolved worker
  memory limits.

Remaining work:

- Account for allocatable capacity, DaemonSet overhead, pod density, taints,
  quota, and provider provisioning failures in node-pool fit.
- Add richer Event/status summaries without duplicating Kubernetes scheduling
  state.
- Add provider-friendly placement shortcuts only where explicit selectors are
  not enough.
- Add status fields for expanded existing-node-pool selector/provider mapping
  only if the current route/status diagnostics are not enough.
- Expand Trino memory policy only for workload-independent settings; heap
  fractions, fault-tolerant execution tuning, and coordinator-heavy workloads
  still need policy.
- Preserve bounded status and compact route diagnostics as more provenance is
  added, including explicit size-budget guidance for future status fields.

## Operability And Lifecycle

Implemented baseline:

- Gateway route states enforce query acceptance during running, resuming,
  paused, draining, and removed states.
- E2e coverage verifies queries arriving during suspend/resume transitions.
- XTrinode deletion supports node-pool `Delete`, `Retain`, and `ScaleToZero`.
- Namespace guardrail modes and object names are explicit.
- KEDA CRDs are a platform dependency for the operator install; runtime KEDA
  remains opt-in per XTrinode.
- Native HPA remains a supported privileged worker autoscaling mode through
  `valuesOverlay.server.autoscaling`.
- Route-only metadata changes do not roll Trino pods when pod templates and
  mounted runtime config are unchanged.
- Runtime-shape changes may roll Trino pods when rendered pod templates or
  mounted runtime config change; route capacity and guardrail plan changes have
  separate diagnostics.

Remaining work:

- Add a compact ordering table for suspend, resume, autosuspend, KEDA handoff,
  native HPA, leases, route states, and query acceptance.
- Extend lifecycle race tests around autosuspend and route reload delay if those
  paths start carrying more state.
- Define behavior when `spec.nodePool` is removed from an existing XTrinode,
  including whether any `DeleteOnSpecRemoval` mode should exist.
- Document provider-specific `ScaleToZero` caveats once adapters expose richer
  scale and readiness feedback.
- Add guidance for clusters that already have namespace quota, limit ranges, or
  namespace label policy.
- Decide only if needed whether suspended runtimes can release namespace
  guardrail quota; current behavior reserves enough quota for resume.
- Write webhook rollout, recovery, SLO, and API-server outage runbook guidance.
- Add Helm notes for the security impact of changing webhook `failurePolicy`.
- Add alerts or runbook checks for webhook service health, certificate expiry,
  and admission latency/errors.

## valuesOverlay Policy

Implemented baseline:

- Mutating `spec.valuesOverlay` requires `update` on
  `analytics.xtrinode.io/xtrinodes/valuesoverlay`.
- Status permission does not grant overlay mutation rights.
- Missing user info or missing authorizer fails closed.
- Authorized users receive warnings that `valuesOverlay` is privileged input.
- Admission rejects typed-shape conflicts and portable high-risk pod, security,
  host namespace, hostPath, sidecar, global `envFrom`, and external Service
  type settings.
- Resource builders do not keep denied renderer backdoors: denied sidecars and
  global `envFrom` are not rendered, hostPath volumes and privileged container
  contexts fail building, and denied external Service types are clamped to
  `ClusterIP`.
- Lifecycle-breaking Trino HTTP overrides are rejected.

Remaining work:

- Add optional Role/RoleBinding examples for trusted overlay editors.
- Add policy examples for approved image registries.
- Add policy examples for Secret references by namespace, name prefix, or label.
- Add policy examples for approved Service annotations.
- Add policy examples for allowed node selectors, tolerations, and existing-pool
  shortcuts.
- Add per-key overlay authorization only if real teams need separate roles for
  images, mounts, networking, or Trino config.

## Future API Risks

Not currently implemented:

- `size: custom`
- `sizingProfileRef`
- shared sizing profile versions
- profile rollout policy
- richer route capacity provenance

Future work:

- Decide whether `custom` means preset-free resources or whether
  preset-plus-overrides remains the long-term model, including admission
  validation and status reporting for the sizing source.
- Define profile ownership, immutable versions, rollout rules, and status
  provenance before adding profiles.
- Choose profile adoption semantics explicitly: follow latest, pin immutable
  versions, or require manual adoption.
- Define whether tenants may override profile fields and how profile updates
  interact with node-pool machine types, route capacity, and pod rollouts.
- Record resolved profile version in status and include it in the runtime-shape
  hash if profiles are added.
- Add warnings or tooling for profile updates that would roll pods, resize node
  pools, or affect many runtimes.
- Add route `capacitySource`, profile version, or shape-source metadata only if
  stale-capacity debugging needs it.

## Non-Blocking Go Cleanup

These are cleanup items, not product contracts:

- Split large API validation/type files only when doing related API work.
- Extract narrow gateway request-handling helpers around route selection and
  resume response mapping.
- Deduplicate coordinator/worker resource builder policy only where it removes
  real branching or repeated logic.
- Consolidate provider-neutral node-pool defaults after provider contracts
  settle.
- Prefer typed helpers for overlay map parsing where a supported overlay area
  continues to grow.

Keep `make lint-go`, `go test ./...`, and manifest verification clean after each
cleanup slice.

Cleanup guardrails:

- Keep command entrypoints wiring-only.
- Keep generated files out of manual cleanup.
- Do not mix broad hygiene refactors with behavior changes.
- Keep provider-specific code readable rather than forcing premature abstraction.

## Keep Out Of This Slice

- Broad hygiene refactors.
- Provider abstraction rewrites without a concrete provider gap.
- Making `valuesOverlay` tenant-safe.
- Storing full runtime specs in status or gateway routes.
