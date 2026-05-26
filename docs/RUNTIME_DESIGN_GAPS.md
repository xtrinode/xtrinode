# Runtime Design Gaps

## Status

Post-resolved-runtime-shape design review notes.

The original scattered-shape problem has been addressed by
`internal/runtimeshape.Resolve` and the typed runtime-shape API. This document
tracks the remaining runtime design gaps after that implementation.

It builds on:

- [RESOLVED_RUNTIME_SHAPE_DESIGN.md](RESOLVED_RUNTIME_SHAPE_DESIGN.md)
- [VALUESOVERLAY_SECURITY_MODEL.md](VALUESOVERLAY_SECURITY_MODEL.md)
- [FORWARD_LOOKING_RUNTIME_API_RISKS.md](FORWARD_LOOKING_RUNTIME_API_RISKS.md)
- [RUNTIME_OPERABILITY_AND_POLICY_GAPS.md](RUNTIME_OPERABILITY_AND_POLICY_GAPS.md)

## Implemented Baseline

The following items are no longer open design gaps:

- Pod resources come from presets plus typed `spec.resources`.
- Pod placement comes from typed `spec.placement`.
- `spec.placement.existingNodePool` expands common provider pool names to
  documented Kubernetes node selectors.
- `spec.nodePool.schedulePods=true` binds coordinator and worker pods to the
  XTrinode-managed node-pool label.
- Gateway and API-server capacity use resolved `capacityUnits`.
- Namespace guardrails use resolved resources and worker counts, including
  rolling-update surge headroom.
- `status.observedRuntimeShape` reports a compact resolved shape.
- Gateway routes carry runtime-shape version, hash, and observed generation
  diagnostics.
- Route-only metadata changes do not change Trino pod-template rollout hashes.
- Best-effort node-pool fit warnings and `NodePoolFitReady` status conditions
  exist for known machine types.
- Runtime pod and deployment scheduling blockers are summarized in
  `SchedulingReady` and classified into `PlacementReady`, `TaintsReady`,
  `QuotaReady`, and `CapacityReady` conditions.
- Common Trino per-node memory settings are validated against resolved worker
  memory limits.
- `valuesOverlay` uses dedicated `xtrinodes/valuesoverlay` RBAC and rejects
  typed-shape conflicts plus high-risk pod/security/exposure fields.

## Summary

| Priority | Gap | Evidence Level | Main Risk |
| --- | --- | --- | --- |
| P1 | KEDA platform dependency contract | Implemented current contract | KEDA CRDs are a platform dependency for the operator install; runtime KEDA remains opt-in per XTrinode. |
| P2 | Strong node-pool fit validation | Partially implemented | Best-effort warnings exist, but exact allocatable capacity, DaemonSet overhead, taints, and worker density are still platform-specific. |
| P2 | Deeper schedulability diagnosis | Implemented current contract | `SchedulingReady` summarizes scheduling blockers and focused conditions classify placement, taint, quota, and capacity failures; Events and provider provisioning failures remain the detailed source of truth. |
| P2 | Broader Trino memory policy | Partially implemented | Common per-node query memory checks exist; workload-specific heap and FTE tuning still need policy. |

Forward-looking API risks such as `size: custom`, gateway capacity versioning,
and sizing profiles are scoped separately in
[FORWARD_LOOKING_RUNTIME_API_RISKS.md](FORWARD_LOOKING_RUNTIME_API_RISKS.md).

## Gap 1: KEDA Platform Dependency Contract

### Current Behavior

Runtime-level KEDA workers are active only when `spec.keda.enabled=true` and at
least one scaler or metric configuration is present. Otherwise workers use a
fixed replica count from the resolved runtime shape.

The operator imports KEDA API types and registers watches for KEDA
`ScaledObject` and `TriggerAuthentication` resources. The current platform
contract is therefore explicit: KEDA CRDs are required for the operator install.
The Helm chart installs KEDA by default; if the KEDA subchart is disabled, KEDA
must already be installed in the cluster.

Runtime-level KEDA workers are still opt-in. `spec.keda.enabled=false` means
fixed workers for that runtime, not "the platform has no KEDA APIs installed."
Runtime reconcile marks `KEDAReady=False` with reason `KEDAPlatformMissing` when
a KEDA-enabled runtime cannot reach the ScaledObject API.

### Better Design

The selected current contract is:

- Helm always installs or requires KEDA CRDs.
- Runtime `keda.enabled: false` means fixed workers only, not "the platform has
  no KEDA APIs installed."
- KEDA-enabled runtime failures surface as `KEDAPlatformMissing` instead of an
  opaque apply error.

## Gap 2: Strong Node-Pool Fit Validation

### Current Behavior

`spec.nodePool` can create or update provider node-pool resources. Provider
machine type fields are validated for presence, and defaulting can recommend a
machine type from the selected size.

The operator now performs a best-effort fit check for known provider machine
types and surfaces the result through admission warnings and the
`NodePoolFitReady` condition.

The operator does not yet perform a strong fit check between:

- Resolved coordinator and worker requests.
- Node-pool machine type.
- Node count range.
- System and DaemonSet overhead.
- Taints and selectors.
- Desired worker density per node.

### Better Design

Validate or warn on node-pool fit using the resolved runtime shape.

Remaining strong validation would account for:

```text
worker request <= estimated allocatable node capacity
coordinator request <= estimated allocatable node capacity
required worker count <= maxNodes * workersPerNode
placement selector/tolerations can target the provisioned pool
```

The current implementation intentionally starts with warnings because exact
allocatable capacity depends on cloud image, kubelet reservations, DaemonSets,
and local platform policy.

## Gap 3: Deeper Schedulability Diagnosis

### Current Behavior

Kubernetes records scheduling problems in Pod conditions, Deployment conditions,
and Events. XTrinode now summarizes blockers in a `SchedulingReady` condition
and classifies common cases in `PlacementReady`, `TaintsReady`, `QuotaReady`,
and `CapacityReady` conditions on the `XTrinode` object.

Common failure modes:

- Placement selector matches no nodes.
- Node pool exists but labels do not match pod selectors.
- Taints are not tolerated.
- Namespace quota blocks pods.
- Requests exceed allocatable node capacity.
- Image pull failures caused by privileged image overrides.

### Current Contract

The operator classifies the common blocker families from
`PodScheduled=False` and `DeploymentReplicaFailure=True` status. It keeps
Kubernetes Pod Events as the detailed source of truth.

Example:

```yaml
status:
  conditions:
    - type: SchedulingReady
      status: "False"
      reason: SchedulingBlocked
      message: worker pod trino-example-worker-... did not schedule
    - type: PlacementReady
      status: "False"
      reason: PlacementBlocked
      message: worker nodeSelector cloud.google.com/gke-nodepool=xtrinode-s matched no ready nodes
```

## Gap 4: Broader Trino Memory Policy

### Current Behavior

The resolved runtime shape owns Kubernetes CPU and memory resources. The
resource builder uses typed memory limits for generated JVM and Trino memory
defaults, and admission validates common per-node query memory settings against
the resolved worker memory limit.

Remaining risk is mostly workload-specific: heap fractions, coordinator-heavy
workloads, and fault-tolerant execution settings may still need platform policy.

### Better Design

Continue expanding memory policy from the existing common checks:

```text
JVM heap < container memory limit
query.max-memory-per-node <= safe fraction of worker memory
coordinator memory settings <= coordinator memory limit
worker memory settings <= worker memory limit
```

Use hard rejects for impossible cases and warnings for workload-specific tuning.

## Gap 5: Existing Node-Pool Placement UX

### Current Behavior

Users can target existing pools through raw Kubernetes placement:

```yaml
spec:
  placement:
    nodeSelector:
      cloud.google.com/gke-nodepool: xtrinode-s
```

For common managed providers, users can also set
`spec.placement.existingNodePool`. The resolver expands it to provider-specific
selectors for AKS, EKS, and GKE:

```yaml
spec:
  placement:
    existingNodePool:
      provider: gcp
      name: xtrinode-s
```

### Better Design

The remaining design boundary is documentation and UX.

Raw `nodeSelector` remains the canonical advanced API for non-standard clusters.
Provider shortcuts are convenience mappings, not a portability guarantee.

## Gap 6: Typed-Field Versus Overlay Conflict Policy

### Current Behavior

Typed fields own the standard runtime shape. `valuesOverlay` remains privileged
for advanced settings and is not a raw Helm pass-through. Admission now rejects
overlay keys that duplicate typed runtime-shape fields such as resources,
placement, and role rollout strategy knobs.

### Better Design

Current policy:

1. Typed fields are authoritative for standard runtime-shape concepts.
2. Ambiguous overlay equivalents are rejected.
3. Privileged overlay remains available for supported non-shape Trino and pod
   settings that do not conflict with typed fields or the built-in deny rules.

## Recommended Next Work

1. Strengthen node-pool fit checks with provider allocatable data where a
   platform supplies it.
2. Add provider Events and provisioning signals to scheduling diagnostics where
   they can be read reliably.
3. Add registry and Secret-reference examples for external overlay policy.
4. Expand Trino memory validation only where settings are workload-independent.

## Final Invariant

The target state remains:

```text
Users describe runtime shape, capacity, and placement through typed fields;
platform owners use valuesOverlay only for privileged low-level overrides; and
controller, gateway, guardrail, status, and node-pool decisions come from the
same resolved runtime shape.
```
