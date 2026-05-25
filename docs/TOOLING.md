# Tooling

This page is the local development toolchain contract. The Makefile runs project tasks and assumes required tools are
already installed. Do not add `ensure-*` targets, curl or `go install` bootstrappers, containerized linter fallbacks,
`npx` fallbacks, or silent skips to Makefile recipes. CI installs CI-only tools in workflow setup steps; local setup is
documented here.

Run this to print the pinned versions used by the Makefile and CI helpers:

```bash
make tool-versions
```

## Required Baseline

| Tool | Version | Used by | Source of truth |
| --- | --- | --- | --- |
| Bash and GNU Make | Current stable | Makefile and `make lint-shell` script syntax checks | Local install |
| `curl`, `jq`, `yq`, `openssl` | Current stable | Deployment scripts and smoke workflows | Local install |
| Go | `1.26.3` | Build, test, code generation, Go lint | `xtrinode/go.mod`, `tools/go.mod`, `GO_VERSION` |
| golangci-lint | `v2.12.2` | `make lint-go` | `tools/go.mod`, `GOLANGCI_LINT_VERSION` |
| controller-gen | `v0.19.0` | `make manifests`, `make verify-manifests` | `tools/go.mod`, `CONTROLLER_GEN_VERSION` |
| setup-envtest | `v0.24.0` | `make test-integration` | `SETUP_ENVTEST_VERSION` |
| envtest Kubernetes assets | `1.34.0` | `make test-integration` | `ENVTEST_K8S_VERSION` |
| Ruby | `4.0.5` | `make lint-yaml` | `.ruby-version`, `RUBY_VERSION` |
| godoc | `v0.1.0-deprecated` | `make godocs*` | `GODOC_VERSION`, local `$HOME/go/bin/godoc` |
| Node.js | `>=22` | Markdown tooling | `.nvmrc`, `package.json`, `NODE_VERSION` |
| npm | `10.9.7` | Local JavaScript tool install and scripts | `package.json` `packageManager` |
| markdownlint-cli | `0.48.0` | `make lint-markdown` | `package.json`, `package-lock.json`, `MARKDOWNLINT_CLI_VERSION` |
| Docker with Buildx | Current stable | Image builds, k3d, local registry workflows | Local install |
| Helm | `v3.20.0` | Helm dependency, lint, template, deploy targets | `HELM_VERSION` |
| kubectl | `v1.34.8` | Cluster and deploy targets | `KUBECTL_VERSION` |
| Terraform | `1.15.4` | Terraform init, plan, validate, apply targets | `TERRAFORM_VERSION` |
| tflint | `0.60.0` | `make lint-terraform` | `TFLINT_VERSION` |
| k3d | `v5.8.3` | Local Kubernetes stack | `K3D_VERSION`, local `bin/k3d` |
| Tilt | `v0.37.3` | Local development loop | `TILT_VERSION`, local `bin/tilt` |
| uv | `0.11.13` | Robot Framework and Locust e2e runner | `UV_VERSION`, local `bin/uv` |
| clusterctl | `v1.11.3` | CAPG workload cluster generation | `CLUSTERCTL_VERSION`, local `bin/clusterctl` |
| CAPG provider | `v1.11.1` | GCP Cluster API workload clusters | `CAPG_VERSION` |
| Python | `>=3.11` | Local e2e dependencies managed by uv | `tilt/e2e/pyproject.toml` |
| Trivy | Current stable | Security scan targets | Local install |

Cloud-provider workflows also need the provider CLI for the target cloud:

- GCP: `gcloud`, `gke-gcloud-auth-plugin`, and `clusterctl` for CAPG workload-cluster flows
- AWS: AWS CLI v2 as `aws`
- Azure: Azure CLI as `az`

Go-based helper binaries default to `$HOME/go/bin`:

```bash
$HOME/go/bin/golangci-lint
$HOME/go/bin/controller-gen
$HOME/go/bin/setup-envtest
$HOME/go/bin/godoc
```

Override `GOLANGCI_LINT`, `CONTROLLER_GEN`, `SETUP_ENVTEST`, or `GODOC` when using another location.

## Runtime and Provider Compatibility

| Component | Current version | Source |
| --- | --- | --- |
| Local Kubernetes target | `v1.34.8` | `K3D_K3S_IMAGE`, `KUBECTL_VERSION` |
| Kubernetes API libraries | `v0.34.3` | `xtrinode/go.mod` replace directives |
| controller-runtime | `v0.22.4` | `xtrinode/go.mod` replace directives |
| KEDA API types | `github.com/kedacore/keda/v2 v2.19.0` | `xtrinode/go.mod` |
| KEDA Helm chart | `2.19.0` | `helm/xtrinode-operator/Chart.yaml` |
| XTrinode Helm chart version | `0.1.0` | `helm/xtrinode*/Chart.yaml` `version` fields and umbrella dependencies |
| XTrinode component image version | `0.1.0` | `helm/xtrinode*/Chart.yaml` `appVersion` fields |
| Default Trino runtime image tag | `480` | `TRINO_IMAGE_TAG`, `internal/config` |
| Trino runtime compatibility target | `trino-1.42.2` / app `480` | Upstream Trino chart reference and runtime image pin |

KEDA 2.19.0 publishes Kubernetes `v1.32 - v1.34` as its tested support window. Keep local Kubernetes on
the `v1.34` minor until the KEDA chart line used here documents Kubernetes 1.35 support and its importable Go API
aligns with controller-runtime `v0.23` or newer. Treat Kubernetes or KEDA bumps as requiring a real local scale-out
e2e run covering both metrics-api and Prometheus-backed scaling.

## Provider Validation

| Provider path | Current posture | Notes |
| --- | --- | --- |
| GCP / GKE / CAPG | Fully exercised cloud path | Includes Terraform, deployment automation, CAPG bootstrap, managed nodepool smoke coverage, and KEDA/resume smoke coverage. |
| AWS / EKS / CAPA | Experimental provider-validation path | Terraform, deploy scripts, API-server auth wiring, and unit-tested nodepool resource generation exist, but the path is not thoroughly live-smoke validated. |
| Azure / AKS / CAPZ | Experimental provider-validation path | Terraform, deploy scripts, API-server auth wiring, and unit-tested nodepool resource generation exist, but the path is not thoroughly live-smoke validated. |

## Compatibility Rules

- Do not bump Kubernetes libraries, controller-runtime, or controller-tools independently unless generated CRDs/manifests
  are regenerated and `make ci-verify-manifests` passes.
- Keep `controller-gen` on the controller-tools line matching the Kubernetes API library minor; for Kubernetes `v0.34.x`
  this is controller-tools `v0.19.x`.
- For KEDA 2.19.0, keep the main module's Go `replace` directives aligned with KEDA's Kubernetes `v0.34.3` and
  controller-runtime `v0.22.4` pins. Dependency module `replace` directives do not propagate into this module.
- Revisit the KEDA-aligned `replace` directives only when the imported KEDA Go API no longer needs those pins.
- Keep all XTrinode chart `version` fields and umbrella dependency versions aligned with the XTrinode release version.
- Keep all XTrinode chart `appVersion` fields aligned with the same XTrinode release version. Do not use the Trino
  runtime tag or upstream Trino chart version as the operator, API server, or gateway image or chart version.
- Treat Trino runtime image/chart drift as an explicit compatibility decision. Catalog property names and metrics exposed
  by Trino can change across Trino releases.
- Update this file in the same patch as version bumps.

## Local Node Setup

The repository pins Node.js through `.nvmrc` and keeps markdownlint as a local npm dependency. Use Node `22` or newer,
then install the locked package set:

```bash
nvm use
npm ci
```

`make lint-markdown` delegates to `npm run lint:markdown`. It does not use a globally installed `markdownlint`, `npx`,
or a Docker image. If `npm ci` has not been run, the target should fail instead of downloading a fallback linter.

## Local Stack Binaries

The local k3d/Tilt/e2e workflow defaults to repository-local binaries:

```bash
bin/k3d
bin/tilt
bin/uv
bin/clusterctl
```

These paths are ignored by git and are machine-local. Override `K3D`, `TILT`, `UV`, or `CLUSTERCTL` when using tools
from another location.

## Command Overrides

Most recipes expose command variables for non-standard installs:

```bash
make ci-lint GO=/usr/local/go/bin/go GOLANGCI_LINT=/opt/bin/golangci-lint
make lint-terraform TERRAFORM=/opt/bin/terraform TFLINT=/opt/bin/tflint
make dev-up K3D=/opt/bin/k3d TILT=/opt/bin/tilt UV=/opt/bin/uv HELM=/opt/bin/helm KUBECTL=/opt/bin/kubectl DOCKER=/usr/bin/docker
```

Supported command variables include `GO`, `GOFMT`, `GO_TOOL_BIN`, `GOLANGCI_LINT`, `CONTROLLER_GEN`, `SETUP_ENVTEST`,
`RUBY`, `NPM`, `HELM`, `KUBECTL`, `TERRAFORM`, `TFLINT`, `K3D`, `TILT`, `UV`, `DOCKER`, `AWS`, `AZ`, `GCLOUD`,
`CURL`, `OPENSSL`, `CLUSTERCTL`, `TRIVY`, `MAKE_CMD`, `GODOC`, and `OPEN`.

## CI Setup

CI reads versions from `make ci-tool-versions-output`. Workflow setup steps install CI-scoped Go tools, Node/npm
dependencies, Helm, Terraform, and Trivy before invoking Make targets. `make ci-lint` also runs `make lint-shell`, which
parses every tracked `*.sh` file with `bash -n`, and `make lint-yaml`, which parses tracked non-template YAML files with
Ruby's standard YAML parser. The Makefile should stay a clean command surface, not a bootstrap layer.
