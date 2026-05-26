# Go Code Hygiene Audit

Date: 2026-05-26

Scope: read-only hygiene review of Go code under `xtrinode/`. This is not a
semantic bug review. Generated code and tests are called out separately where
they would distort the production-code picture.

## Short Verdict

The Go codebase is generally healthy: it is formatted, `make lint-go` passes
cleanly, and the Go test suite passes. The package
layout mostly follows the repository guidance: domain packages are clear, command
entrypoints mostly wire services together, errors are generally wrapped, and test
coverage is substantial.

It is not perfectly clean against the specific bars in the question:

- There are Go files over 1,000 lines. Excluding generated code and tests, there
  are three: `api/v1/xtrinode_webhook.go`, `api/v1/xtrinodecatalog_webhook.go`,
  and `api/v1/xtrinode_types.go`.
- There are some deep branch/nesting hotspots. Most functions are readable, but
  `pkg/gateway/request_handler.go:45` is a real nested-control-flow smell, and
  the Trino resource builders contain several long additive builder functions.
- Literal copy-paste duplication is not widespread, but there is meaningful
  structural repetition in managed node-pool code, admission SubjectAccessReview
  code, coordinator/worker deployment builders, and valuesOverlay map handling.

## Checks Run

- `rg --files -g '*.go'`
- `/usr/local/go/bin/gofmt -l $(rg --files -g '*.go')`
  - Result: no files printed; formatting is clean.
- Lint command:

  ```sh
  PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/trinode-go-build-cache \
    GOLANGCI_LINT_CACHE=/tmp/trinode-golangci-lint-cache make lint-go
  ```

  - Result: `0 issues.`
- Test command:

  ```sh
  PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/trinode-go-build-cache \
    /usr/local/go/bin/go test ./...
  ```

  - Result: passed.
- Custom static scan for file size, approximate function size, approximate
  control nesting, and repeated normalized line windows.
  - The branch/complexity numbers below are heuristic rankings, not official
    `gocyclo` output.

## Size Metrics

| Metric | Count |
| --- | ---: |
| Go files | 223 |
| Total Go LOC | 76,622 |
| Production, non-generated LOC | 36,973 |
| Test LOC | 36,834 |
| Generated LOC | 2,815 |

Largest packages by total Go LOC:

| Package | Files | Total LOC | Test LOC | Production non-generated LOC |
| --- | ---: | ---: | ---: | ---: |
| `xtrinode/controllers` | 52 | 19,551 | 10,840 | 8,711 |
| `xtrinode/api/v1` | 16 | 13,234 | 4,638 | 5,781 |
| `xtrinode/pkg/gateway` | 32 | 11,697 | 6,290 | 5,407 |
| `xtrinode/internal/trino/resources` | 49 | 11,431 | 4,808 | 6,623 |
| `xtrinode/pkg/api-server` | 12 | 4,006 | 1,929 | 2,077 |

## Files Over 1,000 Lines

All Go files over 1,000 lines:

| LOC | File | Notes |
| ---: | --- | --- |
| 2,815 | `xtrinode/api/v1/zz_generated.deepcopy.go` | Generated; not a hygiene concern. |
| 2,038 | `xtrinode/controllers/xtrinode_controller_test.go` | Test file. |
| 2,007 | `xtrinode/api/v1/xtrinode_webhook.go` | Production. |
| 1,959 | `xtrinode/api/v1/xtrinode_webhook_test.go` | Test file. |
| 1,621 | `xtrinode/pkg/gateway/service_test.go` | Test file. |
| 1,472 | `xtrinode/api/v1/xtrinode_admission_regression_test.go` | Test file. |
| 1,330 | `xtrinode/controllers/xtrinodecatalog_controller_test.go` | Test file. |
| 1,286 | `xtrinode/controllers/xtrinode_helpers_test.go` | Test file. |
| 1,138 | `xtrinode/api/v1/xtrinodecatalog_webhook.go` | Production. |
| 1,052 | `xtrinode/api/v1/xtrinode_types.go` | Production CRD types. |
| 1,023 | `xtrinode/controllers/nodepool_regression_test.go` | Test file. |

Production, non-generated files over 1,000 lines:

| LOC | File | Assessment |
| ---: | --- | --- |
| 2,007 | `xtrinode/api/v1/xtrinode_webhook.go` | Too large. It mixes webhook adapter code, authorization, defaulting, validation, and policy checks. |
| 1,138 | `xtrinode/api/v1/xtrinodecatalog_webhook.go` | Borderline-large. Some size is connector-union boilerplate, but it is still hard to scan. |
| 1,052 | `xtrinode/api/v1/xtrinode_types.go` | Acceptable if treated as CRD schema/type definition, but it should not keep accumulating logic. |

Near misses:

- `xtrinode/internal/trino/resources/configmap.go`: 982 LOC.
- `xtrinode/controllers/nodepool_helpers.go`: 937 LOC.
- `xtrinode/internal/config/config.go`: 847 LOC.
- `xtrinode/controllers/connector_registry.go`: 830 LOC.
- `xtrinode/internal/keda/keda.go`: 804 LOC.

## Nesting And Function Size

The code is not full of deeply nested `if` branches. A lot of the code uses
guard returns and small domain helpers. The main issue is different: several
functions are long additive builders that accumulate Kubernetes objects by
handling many feature flags and overlay knobs.

Notable production hotspots from the static scan:

| Function | File | Lines | Approx branch score | Approx max control nesting | Assessment |
| --- | --- | ---: | ---: | ---: | --- |
| `buildTrinoContainer` | `internal/trino/resources/pod_containers.go:14` | 376 | 81 | 3 | Too long; many independent mount/env/probe/overlay concerns in one function. |
| `buildVolumes` | `internal/trino/resources/pod_volumes.go:11` | 313 | 55 | 4 | Too long; similar overlay and role-specific volume patterns repeat. |
| `BuildTrinoResourceSet` | `internal/trino/resources/builder.go:23` | 246 | 19 | 2 | Large but mostly orchestration. |
| `ApplyTrinoResources` | `internal/trino/resources/apply.go:37` | 230 | 67 | 3 | Repetitive nil-check/apply/error wrapping. |
| `DeleteTrinoResources` | `internal/trino/resources/apply.go:269` | 199 | 91 | 4 | Repetitive nil-check/delete/not-found handling. |
| `buildWorkerDeployment` | `internal/trino/resources/worker.go:36` | 229 | 50 | 4 | Structurally parallels coordinator deployment. |
| `buildCoordinatorDeployment` | `internal/trino/resources/coordinator.go:36` | 223 | 51 | 4 | Structurally parallels worker deployment. |
| `run` | `cmd/gateway/main.go:156` | 187 | 32 | 4 | Startup wiring is sizeable; still mostly setup code. |
| `run` | `cmd/api-server/main.go:118` | 160 | 23 | 3 | Startup wiring is sizeable. |
| `loadRoutes` | `pkg/gateway/route_cache.go:14` | 153 | 30 | 3 | Route parsing/normalization has enough branches to deserve helper extraction if it grows. |
| `handleRequest` | `pkg/gateway/request_handler.go:45` | 132 | 25 | 6 | Highest real nesting hotspot. Sticky routing and backend lookup should be split. |

Specific nesting observations:

- `pkg/gateway/request_handler.go:45` nests query-id handling, sticky lookup,
  backend scan, default-route fallback, routability check, deletion, backend
  reselection, metrics, and proxying in one handler. This is the clearest
  "deep branch" issue.
- `internal/trino/resources/pod_containers.go:14` and
  `internal/trino/resources/pod_volumes.go:11` are not deeply nested throughout,
  but they are long because every overlay feature is handled inline.
- `internal/trino/resources/apply.go:37` and `apply.go:269` are more repetitive
  than mentally complex. A declarative list of named apply/delete operations
  would reduce size and branch count without changing behavior.
- The command `run` functions are longer than ideal for "wiring-only"
  entrypoints, but they are still recognizable startup orchestration rather than
  business logic.

## DRY And Repetition

The custom duplicate scan did not find duplicated production function bodies of
20+ lines. That is a good sign: there is not a lot of raw copy-paste of whole
functions.

There is still meaningful repetition:

1. Managed node-pool provider setup

   Files:

   - `xtrinode/controllers/nodepool_aws_managed.go:15`
   - `xtrinode/controllers/nodepool_azure_managed.go:15`
   - `xtrinode/controllers/nodepool_gcp_managed.go:16`

   The first 40-ish lines are nearly identical: compute node-pool inputs, check
   `MachinePool` existence, validate create-time requirements, build an
   unstructured provider object, set labels, set owner refs, apply the provider
   object, then call `buildAndApplyManagedMachinePool`.

   This is the strongest DRY candidate. There is already a shared helper for the
   CAPI `MachinePool`; the remaining provider-specific files could share a small
   "prepare managed provider pool" helper while keeping cloud-specific fields
   explicit.

2. Admission SubjectAccessReview handling

   Files:

   - `xtrinode/api/v1/xtrinode_webhook.go:61`
   - `xtrinode/api/v1/xtrinodecatalog_webhook.go:50`

   Both copy request user info into a `SubjectAccessReview`, copy `UserInfo.Extra`
   into `sar.Spec.Extra`, create the SAR, and interpret `Allowed`, `Reason`, and
   `EvaluationError`. The resource attributes differ, but the mechanics are
   shared. A small helper in `api/v1` would reduce duplication and make future
   admission authorization changes less error-prone.

3. Coordinator and worker deployment builders

   Files:

   - `xtrinode/internal/trino/resources/coordinator.go:36`
   - `xtrinode/internal/trino/resources/worker.go:36`

   These have legitimate role-specific behavior, but they also repeat common
   deployment assembly: base deployment shape, image pull secrets, sidecars,
   scheduling, deployment strategy, revision history, revision stamping, and
   rollout hash stamping. Do not force an abstract builder too early, but common
   helpers for role overlay lookups and deployment settings would pay off.

4. valuesOverlay access patterns

   `GetValuesOverlayMap()` appears 201 times across Go files, with 185
   occurrences concentrated in `internal/trino/resources` and `api/v1`.

   The repeated pattern is usually:

   ```go
   if xtrinode.Spec.GetValuesOverlayMap() != nil {
       if section, ok := xtrinode.Spec.GetValuesOverlayMap()["section"].(map[string]interface{}); ok {
           ...
       }
   }
   ```

   This is not all "bad duplication"; it reflects Helm-shaped
   values. But it does make resource builders noisy and increases the chance that
   two features interpret the same overlay shape differently. The existing
   `values_overlay_mounts.go` is a move in the right direction; more typed/local
   overlay access helpers would improve hygiene.

5. Apply/delete resource loops

   `internal/trino/resources/apply.go` repeats the same pattern dozens of times:
   nil check, apply/delete, wrap error with a resource name. The ordering is
   important, so this should not become clever. A slice of ordered operations
   with name, object, force/optional flags, and operation type would make it more
   maintainable.

## Hygiene Positives

- Formatting is clean under `/usr/local/go/bin/gofmt`.
- `golangci-lint` passes with a reasonably strict config:
  `errcheck`, checked type assertions, `govet` shadow, `staticcheck`, `gocyclo`,
  `gosec`, `noctx`, `nolintlint`, and others.
- `TODO`, `FIXME`, `HACK`, and `XXX` did not appear in Go files during the scan.
- Tests are substantial: test LOC is roughly equal to production non-generated
  LOC.
- The module-level package boundaries are mostly clear:
  controllers, API types/webhooks, Trino resource builders, gateway, API server,
  KEDA, autosuspend, rollout, status, and config each have recognizable ownership.
- Error handling style is generally solid: errors are wrapped with `%w`, and
  many handlers use early returns instead of large `else` blocks.

## Recommended Cleanup Order

1. Flatten `pkg/gateway/request_handler.go:45`.

   Extract sticky route resolution into a helper that returns selected backend
   details or an invalidation/reselect signal. This would remove the deepest
   nested branch chain without changing routing behavior.

2. Split the largest Trino resource builders by concern.

   Good first extraction targets:

   - container ports/probes/env/mounts from `buildTrinoContainer`
   - overlay env and additional mounts from `buildTrinoContainer`
   - global, role-specific, auth, session, Kafka, and additional overlay volumes
     from `buildVolumes`

   Keep the explicit Go builders. Do not replace them with Helm templating
   semantics.

3. Replace repetitive apply/delete blocks with ordered operation descriptors.

   Preserve dependency order and special cases:

   - deployments use non-forced ownership
   - optional ServiceMonitor CRDs need no-match handling
   - worker deployment replicas are removed when autoscaling owns replicas

4. Share the managed node-pool setup skeleton.

   Keep AWS/Azure/GCP field mapping explicit, but share common setup:

   - compute defaults and names
   - check `MachinePool` existence
   - run create validation
   - create provider `Unstructured` with GVK/name/namespace/cluster label/owner
   - convert labels, zones, taints, and tags where field paths differ

5. Share SubjectAccessReview plumbing in `api/v1`.

   A helper for building SAR user info and interpreting SAR status would reduce
   admission duplication while preserving separate policy/resource attributes.

6. Set a production file-size budget.

   Suggested practical budget:

   - hard warning at 1,000 LOC for production non-generated files
   - soft warning at 150 LOC for production functions
   - ignore generated files
   - allow large table-driven test files, but split them when navigation becomes
     painful

## Bottom Line

This is not messy Go code. The tooling and tests are in good shape, and the
package boundaries are mostly coherent. The main hygiene debt is concentrated in
a few places: large admission files, long Kubernetes resource builders, one
deeply nested gateway request handler, and a handful of repeated provider and
admission patterns.

If the goal is "no huge files, no deep branches, no DRY issues", the repo is not
there yet. If the goal is "healthy codebase with identifiable, bounded cleanup
targets", it is already in decent shape.
