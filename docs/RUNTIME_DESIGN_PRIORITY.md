# Runtime Design Priority

## Status

Post-#46 implementation priority.

The first resolved-runtime-shape slice has landed. The priority list below is
therefore a follow-up backlog, not a pre-implementation plan.

It builds on:

- [RESOLVED_RUNTIME_SHAPE_DESIGN.md](RESOLVED_RUNTIME_SHAPE_DESIGN.md)
- [RUNTIME_DESIGN_GAPS.md](RUNTIME_DESIGN_GAPS.md)
- [RUNTIME_OPERABILITY_AND_POLICY_GAPS.md](RUNTIME_OPERABILITY_AND_POLICY_GAPS.md)
- [FORWARD_LOOKING_RUNTIME_API_RISKS.md](FORWARD_LOOKING_RUNTIME_API_RISKS.md)
- [VALUESOVERLAY_SECURITY_MODEL.md](VALUESOVERLAY_SECURITY_MODEL.md)

## Implemented Baseline

The current code already includes:

- `internal/runtimeshape.Resolve`.
- Typed `spec.resources`.
- Typed `spec.placement`.
- Typed `spec.placement.existingNodePool` provider shortcuts.
- `spec.routing.capacityUnits`.
- `spec.nodePool.schedulePods`.
- `spec.nodePool.deletionPolicy` with `Delete`, `Retain`, and `ScaleToZero`.
- Compact versioned `status.observedRuntimeShape`.
- Guardrails, gateway route capacity, API-server resume ranking, pod resources,
  and pod placement consuming the resolved shape.
- Pod rollout hashes decoupled from route-only metadata changes.
- Gateway route diagnostics for runtime-shape version, hash, and observed
  generation.
- Dedicated `xtrinodes/valuesoverlay` RBAC and built-in overlay content denies.
- `SchedulingReady`, `PlacementReady`, `TaintsReady`, `QuotaReady`,
  `CapacityReady`, and `NodePoolFitReady` status conditions.
- Local lifecycle cleanup interruption e2e coverage through
  `make test-e2e-local-lifecycle-cleanup`.

## Priority Order

| Order | Target | Why Next | Primary Docs |
| --- | --- | --- | --- |
| 1 | Stronger scheduling and fit diagnosis | Classification exists for common placement, taint, quota, and capacity blockers; remaining work needs provider allocatable data, Events, and provisioning signals. | `RUNTIME_DESIGN_GAPS.md` |
| 2 | Runtime lifecycle policy table | The route-state table exists; the full suspend/resume/lease lifecycle table should still be written down. | `RUNTIME_OPERABILITY_AND_POLICY_GAPS.md` |
| 3 | Overlay external policy examples | Built-in deny rules exist; registry, Secret-reference, and organization-specific policies should be documented as examples. | `VALUESOVERLAY_SECURITY_MODEL.md` |
| 4 | Admission webhook operations | `failurePolicy: Fail` is the right secure default and local live outage e2e covers fail-closed writes; recovery/SLO guidance is still operational docs work. | `RUNTIME_OPERABILITY_AND_POLICY_GAPS.md` |
| 5 | Future custom/profile APIs | `size: custom`, `sizingProfileRef`, and profile versioning should wait until the current typed surface is stable. | `FORWARD_LOOKING_RUNTIME_API_RISKS.md` |

## Completed And Near-Term Engineering Slices

### Slice 1: KEDA Platform Contract

- Done: KEDA CRDs are documented as a hard platform dependency for the operator
  install.
- Keep the runtime rule: KEDA workers require `spec.keda.enabled=true` and a
  metric/scaler configuration.
- Runtime reconcile already reports `KEDAPlatformMissing` when ScaledObject APIs
  are unavailable.

### Slice 2: Fit And Schedulability Status

- Done: summarize pod and deployment scheduling blockers in `XTrinode`
  conditions.
- Done: classify common placement, taint/toleration, namespace quota, and node
  capacity blockers.
- Done: add best-effort fit checks from resolved resources to selected machine
  types.
- Done: treat unknown machine types as warnings, not hard failures.
- Done: include `schedulePods` and explicit `spec.placement` conflicts in
  status.
- Remaining work is provider-specific allocatable inputs and external
  provisioning signals.

### Slice 3: Overlay Hardening

- Done: privileged overlay authorization uses the dedicated
  `xtrinodes/valuesoverlay` permission.
- Done: built-in validation rejects typed-shape conflicts and high-risk pod,
  host mount, sidecar, and external Service exposure fields.
- Remaining: add image registry, Secret-reference, and organization-local
  policy examples.

### Slice 4: Lifecycle Contract

- Done: document route-state precedence for `RUNNING`, `RESUMING`, `PAUSED`,
  and `DRAINING`.
- Remaining: document suspend and resume ordering around KEDA, native HPA,
  leases, and gateway route repair.
- Keep the local interruption suite as the regression target.

## Final Target

The target order is:

```text
Keep the resolved shape as the single truth.
Make operational policy explicit.
Harden privileged escape hatches.
Add custom/profile/version APIs last.
```
