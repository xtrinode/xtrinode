# Runtime Operability And Policy Gaps

## Status

Post-resolved-runtime-shape design review notes.

The original full-spec pod rollout coupling has been addressed in the renderer:
route metadata changes now update gateway routing without changing Trino
pod-template rollout hashes, as long as the rendered Trino pod template and
mounted runtime config are unchanged. The resolved runtime shape is now
implemented, so the remaining concern in this document is preserving that domain
separation as future shape, status, profile, and policy surfaces grow.

This document scopes additional XTrinode runtime concerns that are adjacent to,
but separate from, the core resolved runtime shape work.

It builds on:

- [RESOLVED_RUNTIME_SHAPE_DESIGN.md](RESOLVED_RUNTIME_SHAPE_DESIGN.md)
- [RUNTIME_DESIGN_GAPS.md](RUNTIME_DESIGN_GAPS.md)
- [FORWARD_LOOKING_RUNTIME_API_RISKS.md](FORWARD_LOOKING_RUNTIME_API_RISKS.md)
- [VALUESOVERLAY_SECURITY_MODEL.md](VALUESOVERLAY_SECURITY_MODEL.md)

The gaps here are mostly about ownership policy, lifecycle races, rollout
policy, provider portability, deletion semantics, status shape, profile drift,
and webhook availability.

## Summary

| Priority | Gap | Evidence Level | Main Risk |
| --- | --- | --- | --- |
| P1 | Namespace guardrail ownership policy | Implemented current contract | Guardrail modes and object names are explicit; status reports the calculated recommendation. |
| P1 | Autosuspend and route-state race semantics | Partially implemented | The route-state contract and local race e2e exist; the broader suspend/resume/lease lifecycle contract still needs a state table. |
| P1 | Runtime shape rollout domain clarity | Current issue resolved; future design guardrail | Future runtime-shape expansion must preserve pod/config/route hash separation. |
| P1 | Managed node-pool deletion policy | Partially implemented | `Delete`, `Retain`, and `ScaleToZero` exist; spec-removal remains a future choice. |
| P2 | Provider-specific placement portability | Partially implemented | `existingNodePool` shortcuts exist, but they remain provider-label conveniences, not portable abstraction. |
| P2 | Status size control | Partially implemented | Compact status and route diagnostics exist; future profile/custom work must preserve the size budget. |
| P2 | Profile/version drift | Future design risk | Shared profiles can mutate many runtimes unless versioning is explicit. |
| P2 | Admission webhook availability policy | Implemented current contract | Fail-closed protects privileged fields and local live e2e verifies XTrinode writes fail while the webhook service is unavailable. |

## Gap 1: Namespace Guardrail Ownership Policy

### Current Behavior

XTrinode can reconcile namespace-level guardrail objects with configured names:

- `xtrinode-namespace-quota`
- `xtrinode-namespace-limits`

The operator builds these objects from the XTrinodes in the namespace. The
chart/operator setting `namespaceGuardrails.mode` controls ownership behavior,
and `namespaceGuardrails.resourceQuotaName` plus
`namespaceGuardrails.limitRangeName` define the exact objects XTrinode owns:

| Mode | Behavior |
| --- | --- |
| `managed` | XTrinode creates and fully owns its named guardrail objects. |
| `createOnly` | XTrinode creates missing objects but does not force ownership of existing objects. |
| `observe` | XTrinode reports readiness without applying guardrail objects. |
| `disabled` | XTrinode skips namespace guardrail management. |

`managed` is the current default provider mode.

Representative implementation:

- `xtrinode/controllers/namespace_guardrails.go`
- `ensureResourceQuota`
- `ensureLimitRange`
- `buildNamespaceResourceQuota`
- `buildNamespaceLimitRange`
- `namespaceGuardrailMode`

In `managed` mode, the operator also labels the namespace as managed for
guardrail scope.

### Remaining Gap

The mode and object-name settings make ownership explicit. It is still important
to document how XTrinode guardrails interact with another platform component
that already manages namespace quota, limit ranges, or namespace labels.

Risks:

- A platform team may already have a `ResourceQuota` or `LimitRange` policy with
  different assumptions.
- Multiple Kubernetes `ResourceQuota` objects are jointly enforced, so an
  XTrinode quota can combine with another quota in a surprising way.
- Multiple `LimitRange` objects can make defaulting and validation harder to
  reason about.
- Force ownership is appropriate for operator-owned objects, but dangerous if
  the object was intended to be shared.
- Namespace labels may conflict with external namespace classification policy.

### Current Contract

Keep guardrail ownership explicit and improve platform ergonomics.

Example platform configuration:

```yaml
guardrails:
  mode: managed
  resourceQuotaName: xtrinode-namespace-quota
  limitRangeName: xtrinode-namespace-limits
  namespaceLabels:
    enabled: true
```

The operator owns only its named guardrail objects, not all namespace quota
policy.

### Implementation Notes

- Done: add chart/operator settings for guardrail mode.
- Done: add chart/operator settings for guardrail object names.
- Done: avoid force ownership in `createOnly` and skip mutation in `observe` or
  `disabled`.
- Done: add status that reports calculated guardrail recommendations in
  `observe` and `disabled` modes.
- Document the interaction with platform-managed `ResourceQuota` and
  `LimitRange` objects.

## Gap 2: Autosuspend And Route-State Race Semantics

### Current Behavior

Autosuspend, resume, gateway routing, and leases are split across several paths:

- The gateway routes traffic according to backend route state.
- Backend state is derived from `spec.suspended` and `status.phase`.
- The API server requests resume through annotations and per-runtime or
  per-pool Leases.
- The operator processes resume and autosuspend annotations.
- The operator publishes pending routes while runtimes are not ready.
- Suspend performs graceful query checks before scaling deployments down.

Representative implementation:

- `xtrinode/pkg/gateway/gateway.go`
- `xtrinode/pkg/api-server/resume.go`
- `xtrinode/controllers/commands.go`
- `xtrinode/controllers/xtrinode_suspend.go`
- `xtrinode/controllers/runtime_readiness.go`
- `xtrinode/controllers/xtrinode_reconciliation_steps.go`

There are already safeguards: route states fail closed, suspend checks running
queries before scale-down, fresh suspend publishes a `PAUSED` route before drain
checks, and resume is Lease-gated.

### Current Route-State Contract

The implemented route-state contract is:

| Runtime or route state | New gateway queries | Sticky follow-up requests | Resume behavior | Scale-down behavior |
| --- | --- | --- | --- | --- |
| `RUNNING` | Routed to a healthy active coordinator. | Routed to the sticky backend while it remains valid. | Not requested. | Not allowed. |
| `DRAINING` | Rejected for new query selection. | Still routed to the sticky backend so in-flight queries can finish. | Not requested. | Allowed only after graceful drain says active queries are gone. |
| `PAUSED` | Rejected with retry guidance; gateway may request resume. | Rejected unless a valid sticky draining backend still exists. | Lease-gated resume request. | Already scaled down or preparing to scale down. |
| `RESUMING` | Rejected with retry guidance while resources become ready. | Rejected unless a valid sticky draining backend still exists. | Existing resume continues; duplicate requests are Lease-gated. | Not allowed. |
| `REMOVED` or missing route | Not routed. | Sticky entries are invalidated by normal backend selection. | Not requested unless another matching paused/resuming route exists. | Finalizer cleanup or route repair owns removal. |

The key invariant is that a backend must not remain selectable for new queries
once a suspend or delete path starts destructive scale-down. New queries during
`PAUSED` or `RESUMING` transitions receive a controlled retry response instead
of reaching a stale coordinator.

### Why This Is A Gap

The implementation has the right pieces, but the lifecycle contract should be
written as an explicit concurrency policy.

Important races:

- A query arrives while autosuspend has requested suspend but before the
  operator has reconciled and published `PAUSED`.
- A resume request arrives while suspend is in `Suspending`.
- Gateway sees an old `RUNNING` route while the operator is about to scale
  down.
- Autosuspend triggers after the wake TTL expires while traffic is beginning.
- A pool-level Lease gates resume, but a runtime-level transition is already in
  flight.
- Graceful shutdown finds no active queries, then a new query arrives through a
  stale gateway cache before the route reload observes `PAUSED`.

Some of these races may already be practically safe, but the expected outcome is
not centralized in one lifecycle specification.

### Better Design

Define route-state and lifecycle-state precedence.

Recommended rule:

```text
Route state must become non-RUNNING before any path starts destructive scale-down.
```

Recommended suspend sequence:

1. Mark backend `PAUSED`.
2. Stop accepting new queries through the gateway route.
3. Drain or wait for running queries.
4. Disable KEDA/HPA.
5. Scale deployments down.
6. Mark status `Suspended`.
7. Publish final `PAUSED` route.

Recommended resume sequence:

1. Acquire Lease.
2. Clear `spec.suspended`.
3. Publish `RESUMING`.
4. Create/update resources.
5. Wait for readiness.
6. Publish `RUNNING`.
7. Release/expire Lease naturally.

### Implementation Notes

- Remaining: add a lifecycle state table for `Ready`, `Suspending`,
  `Suspended`, `Resuming`, `Reconciling`, and `Error`.
- Done: add an explicit route-state table for `RUNNING`, `DRAINING`, `PAUSED`,
  and `RESUMING`.
- Done: fresh suspend updates route state before query drain checks can pass.
- Done: local e2e covers new queries arriving after suspend publishes `PAUSED`
  and while resume publishes `RESUMING`; both receive controlled retry responses
  instead of being routed to a stale backend.
- Add tests for query arrival during autosuspend, route reload delay, and resume
  during suspend.
- Include Lease key behavior in the state transition documentation.

## Gap 3: Runtime Shape Rollout Domains

### Current Behavior

XTrinode still computes a broad base revision from the full `XTrinode` spec plus
the operator version, but that revision is now used for resource metadata,
status, convergence, and debugging rather than as the production pod-template
rollout trigger.

Representative implementation:

- `ComputeXTrinodeRevision` hashes `xtrinode.Spec`.
- `BuildTrinoResourceSet` keeps that base revision on rendered resource
  metadata.
- Role ConfigMap names are derived from rendered ConfigMap data.
- Coordinator and worker pod-template revisions are derived from the rendered
  pod template plus external runtime content digests.
- Coordinator and worker deployments also receive component rollout-hash
  annotations.

This means a route metadata change, such as changing `spec.routing.routingGroup`
after routing is already enabled, updates gateway routing without changing the
Trino pod-template hash. A change from no routing block to a routing block can
still roll pods because it enables `http-server.process-forwarded=true`, which
is real rendered Trino config.

Historical behavior was broader: the full-spec base revision was stamped onto
pod templates, so non-pod fields could force a new ReplicaSet.

Current coverage:

- Unit coverage verifies route-only spec changes keep coordinator and worker
  pod-template revisions and rollout hashes stable.
- Local Robot e2e coverage verifies a `routingGroup` change updates the gateway
  route while Deployment pod-template and Kubernetes Deployment revision signals
  remain unchanged.
- Local Robot e2e coverage also verifies rendered mounted ConfigMap, Secret,
  overlay `env`, and typed `helmChartConfig.envFrom` changes still roll the
  runtime.

### Why This Is Still A Design Gap

The immediate full-spec rollout coupling is addressed in the renderer, and the
current resolved runtime shape preserves the same separation. Future runtime API
growth must keep that boundary.

Not every plan or spec change should roll pods.

Examples:

- Gateway routing capacity changes should update routes, not restart Trino.
- Status-only or policy-only metadata should not roll pods.
- Some node-pool bounds changes affect capacity but not existing pod templates.
- Some guardrail policy changes affect namespace objects but not Trino pods.
- Placement, image, resources, JVM config, mounted Secret data, and catalog
  mount changes do need pod rollout.

Over-rolling is safer than under-rolling, but it can be expensive for long-lived
queries and large clusters.

### Better Design

Split the resolved shape into rollout domains.

Recommended domains:

| Domain | Example Inputs | Rolls Pods |
| --- | --- | --- |
| Pod template | image, env, resources, placement, volumes, security context | Yes |
| Trino config | JVM, config.properties, auth, catalogs, access control | Yes |
| Gateway route | routing group, capacity, backend state | No |
| Guardrails | quota and limit recommendations | No |
| Node-pool provisioning | machine type, min/max nodes, labels, taints | Usually no Trino rollout unless placement changes |
| Status/debug | observed shape hash, diagnostics | No |

The rollout hash should continue to include only inputs that affect the rendered
pod template or mounted runtime config. Route capacity should have its own route
hash or shape hash.

### Implementation Notes

- Keep `baseRevision` for debugging and object labeling.
- Do not stamp full-spec base revision onto pod templates if it includes
  non-pod-affecting fields.
- Add `podTemplateHash`, `configHash`, `routePlanHash`, and `guardrailPlanHash`
  if needed.
- Keep tests that route-only changes do not change Deployment pod-template
  annotations.
- Keep tests that resource, image, placement, mounted external data, and Trino
  config changes do roll pods.

## Gap 4: Managed Node-Pool Deletion Policy

### Current Behavior

On XTrinode finalizer cleanup, the operator calls `DeleteNodePool` when
`spec.nodePool` is present and `spec.nodePool.deletionPolicy` is `Delete`.
`DeleteNodePool` deletes the provider-specific MachinePool, MachineDeployment,
or managed provider pool resources according to provider and mode.

If `spec.nodePool.deletionPolicy` is `Retain`, finalizer cleanup removes
XTrinode owner references from the provider node-pool resources, leaves those
resources in place, and emits a retention event. The observed runtime shape
records the resolved deletion policy.

If `spec.nodePool.deletionPolicy` is `ScaleToZero`, finalizer cleanup uses the
same node-pool scale path as suspend to set minimum size to zero, then removes
XTrinode owner references so the provider pool object remains.

Presence, identity, and shape changes to `spec.nodePool` require break-glass in
the update webhook. Switching from any non-delete policy to `Delete` also
requires break-glass.

- Deleting the XTrinode deletes its node-pool resources by default.
- Setting `deletionPolicy: Retain` keeps node-pool resources in place.
- Setting `deletionPolicy: ScaleToZero` scales node-pool min size to zero,
  removes XTrinode ownership, and leaves the pool object in place.
- Removing `spec.nodePool` is treated as a breaking presence change and leaves
  any previously created node-pool resources for platform cleanup.
- Suspended runtimes may scale node-pool min nodes down depending on
  `scaleDownOnSuspend`.

Representative implementation:

- `xtrinode/controllers/xtrinode_finalizer.go`
- `xtrinode/controllers/nodepool_adapter.go`
- `xtrinode/api/v1/xtrinode_update_checks.go`

### Remaining Gap

Delete, retain, and scale-to-zero on XTrinode deletion are explicit. Remaining
lifecycle policy questions are about delete-on-spec-removal and shared or
misconfigured node-pool ownership.

Questions that need precise answers:

- Should XTrinode support deleting the old pool when `spec.nodePool` is removed
  instead of the current retain/platform-cleanup behavior?
- Should deletion wait for Trino drain first?
- What if multiple runtimes are accidentally pointed at the same node-pool name?
- What if a provider deletion hangs or leaves cloud resources behind?
- What if a platform wants node pools to outlive runtimes for warm capacity?

### Better Design

Continue expanding explicit node-pool lifecycle policy only where behavior is
well defined.

Example:

```yaml
spec:
  nodePool:
    deletionPolicy: Delete
```

Possible values:

| Policy | Behavior |
| --- | --- |
| `Delete` | Delete provider node-pool resources when the XTrinode is deleted. |
| `Retain` | Remove XTrinode ownership and leave the node pool in place when the XTrinode is deleted. |
| `ScaleToZero` | Set node-pool min size to zero, remove XTrinode ownership, and keep the pool object. |
| `DeleteOnSpecRemoval` | Future: delete the old pool when `spec.nodePool` is removed. |

Default remains `Delete`, and the resolved behavior is visible in status.

### Implementation Notes

- Done: add `spec.nodePool.deletionPolicy`.
- Done: add observed runtime shape status showing node-pool deletion policy.
- Done: require break-glass when switching from a non-delete policy to `Delete`.
- Done: emit events when node-pool deletion starts, succeeds, fails, or is
  retained.
- Done: remove XTrinode owner references before completing a Retain finalizer
  cleanup, so Kubernetes garbage collection does not delete retained pools.
- Done: support `ScaleToZero` finalizer cleanup for providers using the existing
  node-pool scale path, followed by retention cleanup.
- Handle finalizer cleanup errors as critical if policy is `Delete` and cost
  leak prevention is required.

## Gap 5: Provider-Specific Placement Portability

### Current Behavior

Users can place pods on existing node pools by setting provider-specific node
selectors through typed placement, or by using the
`spec.placement.existingNodePool` convenience field for common managed
providers.

For GKE, the common selector is:

```yaml
cloud.google.com/gke-nodepool: xtrinode-s
```

EKS, AKS, self-managed CAPI clusters, and custom clusters use different label
sets and taint conventions.

### Remaining Gap

The typed convenience field makes common provider mappings easier, but it cannot
erase provider differences.

Risks:

- A `provider: gcp` shortcut may not work on non-GKE clusters.
- CAPI-created pools may expose labels differently from managed cloud pools.
- Self-managed clusters may require labels in bootstrap templates, not managed
  pool fields.
- Taints are often provider- or organization-specific.
- Users may expect an existing-node-pool abstraction to be portable when it is
  only a convenience mapping.

### Better Design

Support both explicit Kubernetes placement and provider conveniences.

Portable explicit placement:

```yaml
spec:
  placement:
    nodeSelector:
      workload.xtrinode.io/pool: analytics
```

Provider convenience:

```yaml
spec:
  placement:
    existingNodePool:
      provider: gcp
      name: xtrinode-s
```

The convenience form expands into documented selectors. Raw explicit placement
remains available and should be used for custom labels, self-managed clusters,
or provider setups that do not use the default labels.

### Implementation Notes

- Keep raw Kubernetes placement as the canonical low-level API.
- Done: treat provider mapping as syntactic sugar with documented support
  limits.
- Remaining: add status fields showing expanded selector and provider mapping
  source if users need that debug surface.
- For self-managed CAPI, document bootstrap-template label requirements.
- Do not promise cross-provider portability for provider-specific shortcuts.

## Gap 6: Status Size Control

### Current Behavior

`XTrinodeStatus` is currently compact. It stores phase, coordinator URL, worker
count, revisions, observed digests, wake state, conditions, and a compact
`observedRuntimeShape`.

The implemented observed runtime shape is intentionally summary-level. The
remaining risk is future expansion that starts mirroring full rendered resource
specs into status.

Regression coverage verifies that large placement inputs do not bloat
`status.observedRuntimeShape`. Gateway route entries carry compact
runtime-shape diagnostics instead of copying the full shape.

### Why This Is A Gap

Kubernetes object status should remain readable and bounded.

Risks:

- Large placement structures, affinity rules, topology spread constraints, or
  rendered resource maps could bloat the CR.
- Repeating full pod templates in status creates noisy diffs.
- Large status objects can approach Kubernetes object size limits.
- Status bloat makes UI and CLI output harder to scan.
- Sensitive-looking references may appear in status even if Secret values are
  not copied.

### Better Design

Keep status as a summary, not a rendered manifest cache.

Recommended status shape:

```yaml
status:
  observedRuntimeShape:
    hash: 5f3b8d...
    preset: s
    autoscalingMode: fixed
    capacityUnits: 4
    workers:
      fixed: 2
      min: 2
      max: 2
      quota: 2
      capacity: 2
    worker:
      requests:
        cpu: "4"
        memory: 16Gi
    nodePool:
      provisioningRequested: true
      provider: gcp
      name: xtrinode-s
      schedulePods: true
```

Avoid storing full pod specs, full affinity trees, full Secret references, or
full generated ConfigMaps in status.

### Implementation Notes

- Add explicit status size budget guidance.
- Store hashes and summaries for large structures.
- Keep detailed rendered state in generated Kubernetes objects, not CR status.
- Done: add tests that status remains bounded for large placement inputs.
- Avoid copying Secret names unless needed for debug and already visible in spec.

## Gap 7: Profile And Version Drift

### Current Behavior

There is no `sizingProfileRef` today. This is a future API risk if reusable
runtime profiles are introduced.

### Why This Is A Gap

Profiles are useful, but shared mutable profiles can change many runtimes at
once.

Questions:

- If a platform owner edits a profile, do all referencing runtimes change
  automatically?
- Do runtimes pin profile version at creation time?
- Is profile rollout immediate, staged, or opt-in?
- Can tenants override profile fields?
- How does a profile change interact with node-pool machine types and
  guardrails?
- How are profile changes audited?

Without explicit versioning, profile edits can become hidden mass rollouts.

### Better Design

Make profile update semantics explicit.

Possible models:

| Model | Behavior |
| --- | --- |
| Follow latest | Runtimes automatically adopt profile changes. |
| Pin version | Runtimes reference an immutable profile version. |
| Adopt by annotation | Runtimes stay pinned until an operator approves adoption. |
| Copy-on-write | Runtime spec receives resolved fields at creation time. |

Recommended default for production is version pinning or explicit adoption.

Example:

```yaml
spec:
  sizingProfileRef:
    name: warehouse-heavy
    version: v3
```

### Implementation Notes

- Make profiles immutable by version.
- Record resolved profile version in status.
- Include profile version in the resolved runtime shape hash.
- Add warnings for profile updates that would roll pods or resize node pools.
- Add tooling to list runtimes using a profile version.

## Gap 8: Admission Webhook Availability Policy

### Current Behavior

The operator chart enables admission webhooks by default and sets
`failurePolicy: Fail` by default.

This is the correct default for privileged fields such as `valuesOverlay`,
because unsafe changes should fail closed if admission cannot evaluate them.

However, the same webhook availability also affects ordinary `XTrinode` create
and update operations. If the webhook is down, normal runtime updates can be
blocked too.

Representative implementation:

- `helm/xtrinode-operator/values.yaml`
- `helm/xtrinode-operator/templates/webhook.yaml`
- `xtrinode/api/v1/xtrinode_webhook.go`

### Why This Is A Gap

This is a platform tradeoff, not a simple bug.

Fail-closed protects the cluster from privileged or invalid specs. But webhook
outages can become control-plane outages for normal runtime changes.

Questions:

- Which fields must fail closed?
- Which validations are convenience warnings versus safety requirements?
- Should privileged overlay admission be separated from baseline spec
  validation?
- What operational SLO should the webhook have?
- What is the emergency procedure if the webhook blocks critical updates?

### Better Design

Document the policy explicitly.

Recommended default:

```text
Fail closed for all XTrinode admission in production.
Treat webhook availability as part of the XTrinode control-plane SLO.
```

If finer behavior is needed, consider splitting policy surfaces:

- Baseline schema/defaulting webhook.
- Privileged overlay policy webhook or admission policy.
- Optional external policy layer for organization-specific rules.

The exact split must account for Kubernetes admission limitations: a webhook's
`failurePolicy` applies to the webhook call, not to individual fields after the
webhook is unreachable.

### Implementation Notes

- Done: local e2e scales the webhook-serving operator deployment to zero and
  verifies an otherwise valid XTrinode write fails closed through Kubernetes
  admission.
- Document emergency recovery steps for webhook certificate or service failure.
- Add Helm notes explaining the security impact of changing
  `webhook.failurePolicy` to `Ignore`.
- Consider Kubernetes admission `matchConditions` for high-risk field-specific
  policies where cluster versions support them.
- Add health alerts for webhook service, certificate expiry, and admission
  latency.
- Keep `valuesOverlay` mutation protected by a fail-closed path.

## Recommended Next Work

1. Document namespace guardrail ownership modes in operator-facing install docs.
2. Write the lifecycle-state and lease transition table; the route-state table already exists.
3. Preserve rollout hash domains when expanding `observedRuntimeShape`.
4. Define status size budget guidance for future `observedRuntimeShape`
   expansion.
5. Decide profile versioning before introducing `sizingProfileRef`.
6. Document webhook fail-closed operations and recovery.

## Final Invariant

The target state is:

```text
Runtime policy decisions should be explicit: who owns namespace guardrails, when
routes change during suspend/resume, which shape changes roll pods, what happens
to managed node pools on deletion, how provider placement is expanded, how large
status may grow, how profiles roll forward, and which admission paths fail
closed.
```
