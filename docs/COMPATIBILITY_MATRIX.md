# Compatibility Matrix

This matrix records the versions the repository is built and tested against. Keep it aligned with
`xtrinode/go.mod`, `tools/go.mod`, the root `Makefile`, and Helm chart dependencies.

| Component | Current version | Source |
| --- | --- | --- |
| Go toolchain | `1.26.3` | `xtrinode/go.mod`, `tools/go.mod` |
| Local Kubernetes target | `v1.32.3` | `Makefile` `K3D_K3S_IMAGE`, `KUBECTL_VERSION` |
| Kubernetes API libraries | `v0.35.0` | `xtrinode/go.mod` |
| controller-runtime | `v0.22.4` | `xtrinode/go.mod` |
| controller-tools / controller-gen | `v0.20.1` | `tools/go.mod` |
| KEDA Go module | `v2.18.3` | `xtrinode/go.mod` |
| KEDA Helm chart | `2.19.0` | `helm/xtrinode-operator/Chart.yaml` |
| XTrinode Helm chart version | `0.1.0` | `helm/xtrinode*/Chart.yaml` `version` fields and umbrella dependencies |
| XTrinode component image version | `0.1.0` | `helm/xtrinode*/Chart.yaml` `appVersion` fields |
| Default Trino runtime image tag | `480` | `Makefile` `TRINO_IMAGE_TAG`, `internal/config` |
| Trino runtime compatibility target | `trino-1.42.2` / app `480` | Upstream Trino chart reference and runtime image pin |
| golangci-lint | `v2.12.1` | `tools/go.mod` |

## Provider Validation

| Provider path | Current posture | Notes |
| --- | --- | --- |
| GCP / GKE / CAPG | Fully exercised cloud path | Includes Terraform, deployment automation, CAPG bootstrap, managed nodepool smoke coverage, and KEDA/resume smoke coverage. |
| AWS / EKS / CAPA | Experimental provider-validation path | Terraform, deploy scripts, API-server auth wiring, and unit-tested nodepool resource generation exist, but the path is not thoroughly live-smoke validated. |
| Azure / AKS / CAPZ | Experimental provider-validation path | Terraform, deploy scripts, API-server auth wiring, and unit-tested nodepool resource generation exist, but the path is not thoroughly live-smoke validated. |

## Rules

- Do not bump Kubernetes libraries, controller-runtime, or controller-tools independently unless
  generated CRDs/manifests are regenerated and `make ci-verify-manifests` passes.
- Keep the KEDA Go module and Helm chart versions intentionally reviewed together. They do not have
  to be byte-identical, but drift should be explained in the changing PR.
- Keep all XTrinode chart `version` fields and umbrella dependency versions aligned with the
  XTrinode release version.
- Keep all XTrinode chart `appVersion` fields aligned with the same XTrinode release version. Do
  not use the Trino runtime tag or upstream Trino chart version as the operator, API server, or
  gateway image or chart version.
- Treat Trino runtime image/chart drift as an explicit compatibility decision. Catalog property
  names and metrics exposed by Trino can change across Trino releases.
- Update this file in the same patch as version bumps.
