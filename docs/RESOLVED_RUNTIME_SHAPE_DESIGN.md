# Resolved Runtime Shape Design

## Status

Implemented design.

This document describes how XTrinode closes the gap between `spec.size`, typed
runtime shape fields, namespace guardrails, gateway capacity, and CAPI
node-pool provisioning.

The short version: presets provide defaults, typed CRD fields express the
supported runtime shape contract, `valuesOverlay` remains an advanced escape
hatch for non-shape Trino and pod settings, and every subsystem consumes one
resolved runtime shape.

For adjacent runtime-shape gaps and follow-up design work, see
[RUNTIME_DESIGN_GAPS.md](RUNTIME_DESIGN_GAPS.md).

For implementation targeting and priority order, see
[RUNTIME_DESIGN_PRIORITY.md](RUNTIME_DESIGN_PRIORITY.md).

## Original Problem

Before this design, XTrinode had multiple ways to describe runtime shape:

- `spec.size` selects a preset from `internal/sizing/presets.go`.
- role resource overrides could be supplied through the advanced overlay.
- `spec.nodePool` can provision a provider node pool through CAPI/CAPG/CAPA/CAPZ.
- role scheduling overrides could be supplied through the advanced overlay.

Those inputs are valid individually, but they were not resolved into a
single canonical runtime shape. Different subsystems infer runtime capacity from
different sources.

## Original Mismatches

| Area | Current source | Gap |
| --- | --- | --- |
| Trino pod resources | Preset, then overlay role resources | Actual pods can differ from `spec.size`. |
| Namespace guardrails | Preset and worker counts | Guardrails may not match overridden pod requests and limits. |
| Gateway route capacity | `spec.size` mapped to capacity units | Routing can understate or overstate an overridden runtime. |
| API server resume choice | `spec.size` mapped to capacity | Resume ordering can prefer the wrong backend. |
| Node-pool recommendation | `spec.size` recommended machine type | Recommendation can ignore actual overridden resources. |
| Node-pool provisioning | Explicit `spec.nodePool` | Provisioning does not automatically imply pod placement. |
| Existing node-pool placement | Overlay role scheduling | Placement is possible, but hidden inside an advanced overlay surface. |
| Status/debugging | Scattered fields and events | There is no compact observed shape showing what was actually resolved. |

The biggest practical issue was that overlay role resources and scheduling
could change the running runtime shape while quota, routing, and recommendations
still reasoned from the original size preset.

## Design Goals

- Resolve the runtime shape once per reconciliation.
- Make pod resources, worker counts, capacity, placement, and node-pool intent
  visible as one resolved shape.
- Keep existing `spec.size` behavior stable.
- Keep `valuesOverlay` available for advanced non-shape settings such as image,
  Trino config, probes, auth, mounts, sidecars, services, and rollout policy.
- Make typed CRD fields the only standard contract for resources, placement,
  worker counts, and routing capacity.
- Avoid implicit cloud provisioning unless `spec.nodePool` or a future explicit
  provisioning field is set.
- Avoid implicit pod placement onto a provisioned node pool unless the user asks
  for that binding.
- Make guardrails and gateway capacity reflect the same resolved resources that
  Kubernetes receives.

## Non-Goals

- Do not turn `valuesOverlay` into a full Helm values pass-through.
- Do not make the management-cluster operator apply Trino resources into a
  different workload cluster.
- Do not silently create cloud node pools from `spec.size`.
- Do not silently move pods onto a node pool only because a node pool was
  created.
- Do not require every advanced Trino knob to become a first-class CRD field.

## Model

XTrinode uses an internal resolver that produces a `ResolvedRuntimeShape`.

The resolver is the only place that combines presets, typed overrides,
autoscaling/fixed worker semantics, placement, and node-pool intent.

Simplified internal shape:

```go
type ResolvedRuntimeShape struct {
    PresetName string

    Coordinator corev1.ResourceRequirements
    Worker      corev1.ResourceRequirements

    MinWorkers   int32
    MaxWorkers   int32
    FixedWorkers *int32

    CapacityUnits int32

    Placement PlacementShape
    NodePool  NodePoolShape

    Source RuntimeShapeSource
    Hash   string
}

type PlacementShape struct {
    Coordinator SchedulingShape
    Worker      SchedulingShape
}

type SchedulingShape struct {
    NodeSelector map[string]string
    Tolerations  []corev1.Toleration
    Affinity     *corev1.Affinity
}

type NodePoolShape struct {
    ProvisioningRequested bool
    Provider              string
    ProviderMode          string
    Name                  string
    MinNodes              int32
    MaxNodes              int32
    MachineType           string
    SchedulePods          bool
    DeletionPolicy        string
}
```

The implementation package is `internal/runtimeshape`. The important
requirement is that every controller path calls the same resolver instead of
re-deriving size locally.

## Resolution Order

The resolved shape is resolved in this order:

1. Load the `spec.size` preset.
2. Apply typed resource fields, if present.
3. Apply typed placement fields, if present.
4. Resolve node-pool provisioning intent.
5. Resolve worker-count mode from fixed workers, active KEDA, or privileged
   native-HPA overlay.
6. Apply `spec.nodePool.schedulePods` placement binding.
7. Apply explicit or derived route capacity.
8. Validate the final shape.
9. Compute a stable shape hash for status and debugging.

Typed fields win over presets. Overlay role resources, overlay role scheduling,
and overlay coordinator replicas are not part of the standard runtime shape.
Native HPA remains a privileged overlay autoscaling mode, and its worker bounds
are represented in the resolved shape when active.

## CRD Fields

### Typed Resources

First-class resource fields:

```yaml
spec:
  size: s
  resources:
    coordinator:
      requests:
        cpu: "2"
        memory: 8Gi
      limits:
        cpu: "4"
        memory: 16Gi
    worker:
      requests:
        cpu: "4"
        memory: 16Gi
      limits:
        cpu: "8"
        memory: 32Gi
```

These fields map directly to Kubernetes `corev1.ResourceRequirements`.

Preset values remain the default. Typed resource fields override preset
resources. Overlay role resources are intentionally not honored as standard
runtime shape input.

### Typed Placement

First-class placement fields:

```yaml
spec:
  placement:
    coordinator:
      nodeSelector:
        cloud.google.com/gke-nodepool: xtrinode-s
      tolerations: []
    worker:
      nodeSelector:
        cloud.google.com/gke-nodepool: xtrinode-s
      tolerations: []
```

For common cases, also support a compact all-roles form:

```yaml
spec:
  placement:
    existingNodePool:
      provider: gcp
      name: xtrinode-s
    tolerations: []
```

The resolver expands the compact form into coordinator and worker placement
unless role-specific placement overrides it.

Overlay role scheduling is intentionally not honored as standard runtime shape
input.

For common managed-provider existing pools, `spec.placement.existingNodePool`
can expand a provider and pool name into the documented provider node selector:

```yaml
spec:
  placement:
    existingNodePool:
      provider: gcp
      name: xtrinode-s
```

This is convenience mapping for AKS, EKS, and GKE default labels. Raw
`nodeSelector` remains the canonical API for non-standard labels or custom
clusters.

### Typed Routing Capacity

Optional explicit capacity override:

```yaml
spec:
  routing:
    capacityUnits: 5
```

If omitted, capacity units are derived from resolved worker resources and
resolved worker count. The default calculation is deterministic.

Recommended default:

```text
capacityUnits = ceil(workerCpuRequest * resolvedCapacityWorkers / baselineCpu)
```

Where:

- `baselineCpu` is the CPU request represented by one capacity unit.
- For fixed workers, `resolvedCapacityWorkers` is the fixed worker count.
- For KEDA runtimes, `resolvedCapacityWorkers` is normally `maxWorkers`.
- Operators may later expose a policy to use min, max, or weighted capacity for
  autoscaled pools.

### Node-Pool Scheduling Binding

`spec.nodePool` continues to mean provisioning intent. Use the explicit binding
flag when XTrinode should also schedule pods onto the provisioned pool:

```yaml
spec:
  nodePool:
    provider: gcp
    providerMode: managed
    name: xtrinode-s
    minNodes: 2
    maxNodes: 2
    deletionPolicy: Delete
    schedulePods: true
    gcp:
      machineType: n2-standard-8
```

When `schedulePods: true`, the node-pool controller ensures that the created
pool has a stable label, and the runtime resource builder adds a matching node
selector through the resolved placement.

When `schedulePods` is false or omitted, node-pool provisioning and pod
placement remain independent. Users can still provide typed placement.

## Node-Pool Semantics

### Existing Node Pools

For an existing GKE node pool, do not set `spec.nodePool`. Use placement only.
The provider shortcut is:

```yaml
spec:
  size: s
  minWorkers: 2
  maxWorkers: 2
  keda:
    enabled: false
  placement:
    existingNodePool:
      provider: gcp
      name: xtrinode-s
```

The equivalent raw selector is:

```yaml
spec:
  placement:
    nodeSelector:
      cloud.google.com/gke-nodepool: xtrinode-s
```

This means:

- XTrinode does not create a node pool.
- Kubernetes schedules pods onto nodes matching the selector.
- Guardrails and routing capacity still come from the resolved shape.

### CAPI-Managed Node Pools

For a managed CAPI/CAPG node pool, set `spec.nodePool`:

```yaml
spec:
  size: s
  nodePool:
    provider: gcp
    providerMode: managed
    clusterName: workload-or-operator-cluster
    name: xtrinode-s
    minNodes: 2
    maxNodes: 2
    kubernetesVersion: v1.29.0
    deletionPolicy: Delete
    schedulePods: true
    gcp:
      machineType: n2-standard-8
```

This means:

- XTrinode creates or updates provider-specific CAPI resources.
- `schedulePods: true` binds runtime scheduling to the created pool.
- Machine type still remains explicit unless the project chooses to default it
  from the resolved sizing recommendation.

Provider machine type defaulting and recommendations are based on the resolved
shape when typed worker resources are set, with the preset as the fallback.

## Guardrail Semantics

Namespace guardrails consume `ResolvedRuntimeShape`.

Quota inputs:

```text
coordinator requests + resolvedQuotaWorkers * worker requests
coordinator limits   + resolvedQuotaWorkers * worker limits
```

Where:

- Fixed-size runtimes use the fixed worker count.
- KEDA-active runtimes use `maxWorkers`; `spec.keda.enabled=true` without a
  metric or scaler input remains fixed-worker mode.
- Suspended runtimes still reserve enough quota for resume unless a future
  policy explicitly releases quota while suspended.
- Quota also includes rolling-update surge headroom for coordinator and worker
  Deployments. The default Kubernetes `25%` surge is used unless
  `spec.rolloutPolicy.rollingUpdateStrategy.maxSurge` is set.

This prevents a custom worker override from bypassing guardrails or receiving
quota based on the wrong preset.

## Gateway And Resume Semantics

Gateway route generation publishes capacity from the resolved shape:

```yaml
backends:
  - name: analytics/iceberg-gcs
    tier: s
    capacityUnits: 5
    runtimeShapeVersion: v1
    runtimeShapeHash: 5f3b8d...
    observedGeneration: 12
```

`tier` remains the human-facing preset label. `capacityUnits` is the actual
routing weight. The runtime-shape fields are compact diagnostics for stale route
debugging; the gateway route does not copy the full observed shape.

The API server uses the same resolved capacity units when choosing resume
candidates. It does not independently map `spec.size` to capacity.

## Coordinator Semantics

Each runtime has at most one Trino coordinator. The coordinator Deployment may
be scaled to `0` while suspended and `1` while active, but multi-coordinator
leader election is not part of this design because Trino does not support that
runtime shape.

## Status Semantics

The controller records the observed resolved runtime shape in status:

```yaml
status:
  observedRuntimeShape:
    version: v1
    hash: 5f3b8d...
    preset: s
    autoscalingMode: fixed
    coordinator:
      requests:
        cpu: "2"
        memory: 8Gi
      limits:
        cpu: "4"
        memory: 16Gi
    worker:
      requests:
        cpu: "4"
        memory: 16Gi
      limits:
        cpu: "8"
        memory: 32Gi
    workers:
      fixed: 2
      min: 2
      max: 2
      quota: 2
      capacity: 2
    capacityUnits: 5
    nodePool:
      provisioningRequested: false
      schedulePods: false
      deletionPolicy: Delete
```

This gives operators and users one place to answer:

- Which preset was used?
- Which resources are actually applied?
- How many workers does routing capacity assume?
- Is node-pool provisioning enabled?
- Is runtime placement bound to a node pool?
- Which shape status version produced the current hash?

Status must be observed state, not another desired-state input.

## Admission And Validation

Admission validates the resolved shape after defaults are applied.

Recommended checks:

- `spec.size` exists and resolves to a known preset.
- Worker and coordinator requests and limits parse as Kubernetes quantities.
- Resource limits are greater than or equal to requests when both are set.
- Worker count bounds are coherent.
- Fixed-size runtimes have a deterministic worker count.
- `routing.capacityUnits` is positive when set.
- `nodePool.schedulePods: true` requires `spec.nodePool.name` or a stable
  generated name.
- `spec.placement.existingNodePool` must use a supported provider and cannot be
  combined with `spec.nodePool.schedulePods`.
- `spec.nodePool.deletionPolicy` must be `Delete`, `Retain`, or
  `ScaleToZero`; changing a non-delete policy back to `Delete` requires
  break-glass.
- Managed GCP node pools require `spec.nodePool.gcp.machineType` unless machine
  type defaulting from resolved shape is implemented.
- Managed node-pool labels used for scheduling cannot conflict with user
  placement selectors.

Recommended warnings:

- Resolved worker resources exceed the recommended provider machine type.
- `spec.nodePool` is set but no placement or `schedulePods` binding targets it.
- Placement targets an existing node-pool label but `spec.nodePool` is also set
  to create a different pool.

## Compatibility Boundaries

The standard runtime shape has one supported API surface.

Rules:

- `spec.size` keeps its current meaning.
- `spec.nodePool` keeps its provisioning meaning.
- `valuesOverlay` stays available for privileged non-shape settings.
- Role resources, role scheduling, worker counts, and coordinator replicas must
  be expressed through typed runtime shape fields or lifecycle state, not through
  `valuesOverlay`.
- Typed fields are optional; when omitted, presets and runtime defaults provide
  the shape.

## Implementation Notes

The implementation includes:

| Consumer | Implementation |
| --- | --- |
| Resolver | `xtrinode/internal/runtimeshape` |
| Public API | `spec.resources`, `spec.placement`, `spec.placement.existingNodePool`, `spec.routing.capacityUnits`, `spec.nodePool.schedulePods`, `spec.nodePool.deletionPolicy`, `status.observedRuntimeShape` |
| Pod resources | `xtrinode/internal/trino/resources/pod_containers.go` |
| Pod placement | `xtrinode/internal/trino/resources/scheduling.go`, coordinator and worker builders |
| Guardrails | `xtrinode/controllers/namespace_guardrails.go` |
| Gateway route capacity | `xtrinode/pkg/gateway` |
| Resume ranking | `xtrinode/pkg/api-server` |
| Node-pool warnings and binding validation | `xtrinode/api/v1/xtrinode_webhook.go` |

Unrelated `valuesOverlay` areas remain available for advanced Trino settings.

## Example: Fixed-Size GKE Runtime On Existing Node Pool

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinode
metadata:
  name: iceberg-gcs
  namespace: ananke-live
spec:
  size: s
  autoSuspendAfter: 5m
  minWorkers: 2
  maxWorkers: 2
  keda:
    enabled: false
  resources:
    worker:
      requests:
        cpu: "4"
        memory: 16Gi
      limits:
        cpu: "8"
        memory: 32Gi
  placement:
    nodeSelector:
      cloud.google.com/gke-nodepool: xtrinode-s
```

Expected interpretation:

- No CAPI node pool is created.
- Worker pods request 4 CPU and 16Gi memory.
- Worker pods schedule onto the existing `xtrinode-s` GKE node pool.
- Guardrails are calculated from two workers plus one coordinator.
- Gateway capacity is derived from two workers unless explicitly overridden.
- Status exposes the resolved resources and capacity.

## Example: CAPI-Managed GCP Pool With Explicit Binding

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinode
metadata:
  name: iceberg-gcs
  namespace: ananke-live
spec:
  size: s
  autoSuspendAfter: 5m
  minWorkers: 2
  maxWorkers: 2
  keda:
    enabled: false
  nodePool:
    provider: gcp
    providerMode: managed
    clusterName: ananke-private-gke
    name: xtrinode-s
    minNodes: 2
    maxNodes: 2
    kubernetesVersion: v1.29.0
    schedulePods: true
    gcp:
      machineType: n2-standard-8
```

Expected interpretation:

- XTrinode creates or updates a GCP managed machine pool.
- The managed pool receives a stable scheduling label.
- `schedulePods: true` makes the resolved placement target that label.
- Pod resources still come from `size: s` unless `spec.resources` overrides
  them.
- Guardrails, gateway capacity, and status all use the resolved shape.

## Code Touchpoints

The main implementation touchpoints are:

| Area | Representative path | Runtime-shape responsibility |
| --- | --- | --- |
| Size presets | `xtrinode/internal/sizing/presets.go` | Remain base defaults. |
| Pod resources | `xtrinode/internal/trino/resources/pod_containers.go` | Consume resolved resources. |
| Pod placement | `xtrinode/internal/trino/resources/scheduling.go`, coordinator and worker builders | Consume resolved placement. |
| Namespace guardrails | `xtrinode/controllers/namespace_guardrails.go` | Use resolved resources and worker counts. |
| Gateway route capacity | `xtrinode/pkg/gateway/gateway.go` | Publish resolved capacity units. |
| API resume choice | `xtrinode/pkg/api-server/resume.go` | Rank by resolved capacity units. |
| Node-pool managed provisioning | `xtrinode/controllers/nodepool_*_managed.go` | Apply explicit schedule binding labels when requested. |
| Webhook defaulting and validation | `xtrinode/api/v1/xtrinode_webhook.go` | Validate the final shape and warn on ambiguous inputs. |
| Status types | `xtrinode/api/v1/xtrinode_types.go` | Expose observed resolved shape fields. |

## Final Invariant

The invariant is:

```text
The resources, worker counts, placement, guardrails, route capacity, resume
ranking, node-pool recommendations, and status for an XTrinode runtime all come
from the same resolved runtime shape.
```
