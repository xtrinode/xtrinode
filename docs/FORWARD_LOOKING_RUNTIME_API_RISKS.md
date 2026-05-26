# Forward-Looking Runtime API Risks

## Status

Post-resolved-runtime-shape risk scoping.

XTrinode now has a resolved runtime shape and first-class runtime-shape fields:
`spec.resources`, `spec.placement`, `spec.routing.capacityUnits`,
`spec.nodePool.schedulePods`, `spec.placement.existingNodePool`,
`spec.nodePool.deletionPolicy`, and `status.observedRuntimeShape`.

This document no longer describes current production bugs. It tracks API risks
that remain after the resolved-shape implementation.

## Summary

| Risk | Current Status | Becomes Important When |
| --- | --- | --- |
| `size: custom` | Not supported. A normal runtime still has a required preset baseline, and typed resources can override that baseline. | XTrinode adds a truly preset-free custom size or reusable sizing profiles. |
| Gateway capacity versioning | Route capacity is resolved from the runtime shape or `spec.routing.capacityUnits`; the route publishes runtime-shape version, hash, and observed generation. | Operators need richer provenance such as capacity source or profile version across delayed route reloads, UIs, or profile adoption. |
| Typed-field versus overlay precedence | Typed fields own standard resources, placement, capacity, and node-pool binding. Admission rejects common overlapping overlay keys. | Future overlay policy needs registry, Secret-reference, and organization-specific examples without turning overlay into a tenant API. |

## Risk 1: `size: custom`

### Current Behavior

`spec.size` is required and must be one of `xs`, `s`, `m`, `l`, or `xl`.
Presets provide the baseline resources and machine-type recommendations.

Custom pod resources are now first-class through `spec.resources`:

```yaml
spec:
  size: s
  resources:
    worker:
      requests:
        cpu: "12"
        memory: 48Gi
```

There is still no valid `size: custom` value. A runtime with typed resource
overrides is "preset plus overrides", not a preset-free custom profile.

### Why This Is Not A Current Bug

The current API is coherent because every runtime still starts from a preset,
and the observed runtime shape exposes the resolved resources and capacity.
Route capacity and guardrails consume the resolved shape rather than the literal
preset alone.

### Remaining Risk

If XTrinode later introduces `size: custom` or `sizingProfileRef`, the API must
define how that interacts with the required preset field, status, capacity,
admission warnings, and node-pool recommendations.

Recommended semantics:

- `xs/s/m/l/xl` mean known presets.
- `custom` means resource shape comes from typed fields.
- `sizingProfileRef` means resource shape comes from a platform-owned profile.
- Route capacity uses resolved capacity, not the literal size string.
- Status shows both the sizing source and the resolved shape.

## Risk 2: Gateway Capacity Versioning

### Current Behavior

`RegisterRoute` resolves `internal/runtimeshape.Resolve(xtrinode)` and writes
the resolved `capacityUnits` into the gateway route. If
`spec.routing.capacityUnits` is set, it wins. Otherwise capacity is derived from
resolved worker CPU requests and the resolved capacity worker count.

The route carries the resolved number plus compact diagnostics:

```yaml
backends:
  - name: analytics/iceberg-gcs
    namespace: analytics
    tier: s
    capacityUnits: 8
    runtimeShapeVersion: v1
    runtimeShapeHash: 5f3b8d...
    observedGeneration: 12
```

The gateway does not duplicate the full runtime shape. It only carries enough
metadata to diagnose stale or surprising route entries.

### Remaining Risk

When profiles, UIs, or delayed route reload debugging become important,
operators may need to answer a richer question:

```text
Which source, profile version, and resolved runtime shape produced this capacity number?
```

Potential future route metadata:

```yaml
backends:
  - name: analytics/iceberg-gcs
    namespace: analytics
    tier: s
    capacityUnits: 8
    capacitySource: resolvedRuntimeShape
    runtimeShapeHash: 5f3b8d...
    observedGeneration: 12
    profileVersion: v3
```

The gateway does not need to understand the full shape. It only needs enough
metadata to expose stale or surprising capacity decisions.

## Risk 3: Typed-Field Versus Overlay Precedence

### Current Behavior

The standard runtime shape is owned by typed fields:

- `spec.resources`
- `spec.placement`
- `spec.routing.capacityUnits`
- `spec.nodePool.schedulePods`
- worker-count settings and KEDA/native-HPA mode

`valuesOverlay` remains privileged and is still read by native resource builders
for advanced pod, image, Service, auth, volume, Secret, Trino config, and rollout
settings. It is not a tenant-safe raw Helm pass-through.

Admission now rejects overlay keys that overlap typed runtime-shape fields such
as role resources, scheduling, and deployment rollout strategy knobs.

### Remaining Risk

The compatibility question is how hard admission should be when privileged
overlay content tries to configure a concept that now has a typed field.

Example conflict:

```yaml
spec:
  resources:
    worker:
      requests:
        cpu: "4"
  valuesOverlay:
    worker:
      resources:
        requests:
          cpu: "8"
```

Recommended long-term rule:

```text
Typed fields own supported runtime-shape concepts.
valuesOverlay owns only privileged concepts that do not have typed fields.
```

Current transition state:

1. Preset defaults are applied.
2. Typed fields override preset defaults.
3. Ambiguous overlay equivalents are rejected.
4. Remaining future policy focuses on overlay content that is still privileged
   but not typed-shape overlap, such as image registries, Secret references, and
   local Service annotation policy.

## Final Invariant

Future custom/profile APIs and route metadata should preserve this rule:

```text
Preset sizes, custom sizes, typed fields, overlay compatibility, and gateway
capacity have explicit ownership and versioning, so users can predict the
rendered runtime shape before the operator applies it.
```
