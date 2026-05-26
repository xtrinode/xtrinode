# valuesOverlay Security Model

## Status

Implemented baseline plus remaining hardening targets.

`spec.valuesOverlay` is intentionally powerful. It lets platform owners reach
selected Trino chart-shaped settings that are not modeled as typed XTrinode
fields, or that intentionally remain privileged. That power makes it unsafe as
an unrestricted tenant API.

The desired security boundary is:

```text
Typed XTrinode fields are the normal tenant contract.
valuesOverlay is a privileged platform-owner escape hatch.
```

## Problem

`valuesOverlay` can influence generated Kubernetes resources and Trino
configuration. Depending on the supported overlay keys used by the native
builders, or the denied keys a user may attempt to set, it can affect or target:

- Container images and pull policy.
- Environment variables. Global overlay `envFrom` is denied.
- ConfigMap and Secret mounts.
- Extra volumes and volume mounts.
- Sidecar containers, which are denied by the built-in policy.
- Pod and container security context.
- Node selectors, tolerations, affinity, and topology spread.
- Service annotations, ports, and exposed ports.
- NetworkPolicy configuration.
- Trino authentication, listener, JVM, and config properties.
- Resource requests and limits.

Those controls are useful for platform operations, but they are also obvious
injection surfaces if a tenant can set arbitrary overlay content.

## Threats

| Threat | Example |
| --- | --- |
| Image substitution | User points Trino or a sidecar at an untrusted image. |
| Secret exposure | User mounts or imports Secrets through `envFrom`, `secretMounts`, or custom volumes. |
| Privilege escalation | User sets privileged container settings, host namespaces, added capabilities, or host mounts. |
| Network exposure | User adds Service annotations or exposed ports that create unintended cloud load balancers. |
| Scheduling escape | User targets nodes outside the intended runtime pool through selectors or tolerations. |
| Policy bypass | User configures sidecars or networking that bypass expected gateway or namespace policy. |
| Lifecycle breakage | User disables Trino HTTP or changes ports used by gateway, autosuspend, or graceful shutdown. |
| Resource abuse | User sets high requests, high limits, or many workers without matching guardrail accounting. |
| Config injection | User changes Trino config properties that weaken auth, logging, access control, or query limits. |

## Current Protection

The current webhook already treats `valuesOverlay` as privileged input.

On create or update, if `spec.valuesOverlay` changes, admission performs a
`SubjectAccessReview` for the requesting user. The request is allowed only when
the user has `update` permission on
`analytics.xtrinode.io/xtrinodes/valuesoverlay` in the target namespace.

Current behavior:

- `spec.valuesOverlay` changes are detected as privileged spec changes.
- Missing admission user info fails closed.
- Missing valuesOverlay authorizer fails closed.
- Unauthorized users are rejected.
- Authorized users receive warnings that `valuesOverlay` is privileged input.
- The authorization check uses the dedicated `xtrinodes/valuesoverlay`
  subresource name rather than reusing status permissions.
- Overlay content that duplicates typed runtime-shape fields is rejected. This
  includes pod resources, scheduling fields, and deployment rollout knobs under
  role overlays.
- High-risk pod and exposure settings are rejected by default, including
  privileged containers, privilege escalation, added capabilities, host
  namespaces, hostPath role volumes, sidecars, global `envFrom`, and external
  `LoadBalancer`/`NodePort` Service types.
- Denied overlay paths are not kept as renderer backdoors: sidecars and global
  `envFrom` are not rendered, hostPath volumes and privileged container
  contexts fail resource building, and external Service types are clamped to
  `ClusterIP`.
- Some lifecycle-breaking Trino overrides are rejected, including disabling the
  HTTP listener or overriding `http-server.http.port` directly.
- Trino HTTP authentication settings are checked for compatibility with
  `spec.trinoControlAuth`.

Representative code paths:

| Behavior | Path |
| --- | --- |
| SubjectAccessReview authorization | `xtrinode/api/v1/xtrinode_webhook.go` |
| Privileged field detection | `xtrinode/api/v1/xtrinode_webhook.go` |
| Overlay change warning | `xtrinode/api/v1/xtrinode_update_checks.go` |
| Trino lifecycle compatibility checks | `xtrinode/api/v1/xtrinode_webhook.go` |

## Current Gaps

The current gate is useful, but it is not a sandbox.

Remaining gaps:

- There is no per-key authorization. A user who can set one overlay field can
  set all supported overlay fields.
- There is no built-in allowlist for image registries or repositories.
- There is no built-in Secret reference policy beyond the normal Kubernetes RBAC
  model and whatever the resource builders support.
- The built-in deny policy covers the highest-risk pod and typed-shape
  conflicts, but it is not a complete organization-specific policy for every
  image, Secret, annotation, or Service setting.
- Warnings help humans, but warnings do not prevent an authorized dangerous
  overlay that is still outside the built-in deny rules.

This means the current protection is best understood as:

```text
Only users with the dedicated valuesOverlay permission may use valuesOverlay.
Those users are still trusted with the supported overlay surface outside the
built-in deny rules.
```

That is acceptable for platform-owner workflows, but it is not enough for
self-service multi-tenant use.

## Recommended Target Model

### 1. Keep valuesOverlay Privileged

Continue to treat any `valuesOverlay` create, update, or deletion as privileged.

The validating webhook should continue to fail closed when the admission request
or authorizer is unavailable.

### 2. Use Dedicated Overlay RBAC

The webhook uses a dedicated permission check:

```text
verb: update
group: analytics.xtrinode.io
resource: xtrinodes/valuesoverlay
namespace: <runtime namespace>
```

Example Role:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: xtrinode-valuesoverlay-editor
rules:
  - apiGroups:
      - analytics.xtrinode.io
    resources:
      - xtrinodes/valuesoverlay
    verbs:
      - update
```

Kubernetes RBAC can evaluate this through `SubjectAccessReview` even if
`valuesoverlay` is used only as an authorization subresource name by the
webhook. This makes the policy readable: users with this permission can mutate
the privileged overlay.

### 3. Keep Safe Knobs In Typed Fields

Common tenant customization should not require `valuesOverlay`.

The current API already provides typed fields for common runtime-shape needs:

- CPU and memory requests/limits.
- Node selectors, tolerations, affinity, and topology spread that are allowed by
  platform policy.
- Gateway routing capacity.
- KEDA/autoscaling behavior through the structured `spec.keda` surface.
- Catalog configuration.
- Node-pool provisioning and scheduling binding.
- Trino control authentication.

This lets normal tenants change supported runtime behavior without receiving
the broader overlay privilege.

### 4. Validate Overlay Content

Webhook validation rejects the highest-risk built-in overlay content and
typed-field conflicts.

Default deny unless explicitly enabled:

- `hostPath` volumes.
- Privileged containers.
- Host network, host PID, and host IPC.
- Added Linux capabilities.
- `allowPrivilegeEscalation: true`.
- Writable host mounts.
- Sidecars.
- Arbitrary `envFrom`.
- Secret mounts outside approved Secret references.
- Untrusted image registries.
- Service annotations that create external load balancers.
- Direct Trino listener changes that break gateway or lifecycle control.

Default allow for platform-owner namespaces may be broader, but tenant
namespaces should use a narrower overlay policy.

### 5. Add External Policy Examples

Document examples for:

- Kubernetes `ValidatingAdmissionPolicy`.
- OPA Gatekeeper.
- Kyverno.

Those policies should be able to enforce local platform rules such as approved
registries, forbidden pod security fields, and allowed node-pool labels.

## Recommended Admission Decision Matrix

| User intent | Normal tenant | Overlay editor | Platform admin |
| --- | --- | --- | --- |
| Change CPU/memory through typed fields | Allow, validate quota | Allow | Allow |
| Change placement through typed fields | Allow if policy permits | Allow | Allow |
| Set pod resources through `valuesOverlay` | Deny; use typed resource fields | Deny; use typed resource fields | Deny; use typed resource fields |
| Set custom image | Deny | Allow only if registry is approved | Allow |
| Add sidecar | Deny | Deny by default or require extra policy | Allow |
| Mount arbitrary Secret | Deny | Deny by default or require extra policy | Allow |
| Use `hostPath` | Deny | Deny | Break-glass only |
| Disable Trino HTTP listener | Deny | Deny | Deny until lifecycle supports it |
| Change generated HTTP port directly | Deny | Deny | Deny; use supported service field |

## Relationship To Resolved Runtime Shape

The resolved runtime shape reduces security pressure on `valuesOverlay` by
giving users safe typed fields for common needs.

Specifically:

- `spec.resources` replaces overlay-based resource overrides.
- `spec.placement` replaces overlay-based node selectors and tolerations.
- `spec.routing.capacityUnits` replaces implicit size-based capacity hacks.
- `spec.nodePool.schedulePods` replaces ad hoc placement coupling.

Tenants should not need `valuesOverlay` for normal sizing, placement, or
capacity customization.

## Implementation Plan

### Phase 1: Document And Preserve Current Gate

- Keep current privileged admission behavior.
- Keep failure-closed webhook behavior.
- Keep current warnings.
- Document that `valuesOverlay` is platform-owner-only.

### Phase 2: Dedicated RBAC Permission

- Done: the `SubjectAccessReview` target is `xtrinodes/valuesoverlay`.
- Done: admission tests cover allowed and denied overlay mutation.
- Remaining: add chart examples for platform-owner overlay-editor roles.

### Phase 3: Typed Field Migration

- Keep typed resource, placement, routing capacity, and node-pool scheduling
  fields as the standard runtime-shape surface.
- Done: overlapping runtime-shape overlay keys are rejected in favor of typed
  fields.
- Continue to prefer typed fields for tenant workflows.

### Phase 4: Overlay Content Validation

- Done: high-risk pod security, host namespace, hostPath, sidecar, global
  `envFrom`, external Service type, and typed-shape conflicts are hard denied.
- Remaining: add registry, Secret-reference, annotation, and organization-local
  policy examples where built-in checks should stay portable.

### Phase 5: External Policy Library

- Add example `ValidatingAdmissionPolicy`, Gatekeeper, and Kyverno policies.
- Include policies for image registries, pod security, Secret references,
  Service annotations, and node-pool labels.

## Final Invariant

After hardening, the following should be true:

```text
A normal tenant can customize XTrinode through typed fields without receiving
valuesOverlay privilege, and a user who can mutate valuesOverlay is explicitly
trusted with the privileged pod/config surface that valuesOverlay exposes.
```
