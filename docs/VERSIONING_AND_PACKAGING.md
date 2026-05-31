# Versioning and Packaging Guide

## Versioning Strategy

### **Semantic Versioning**

We use Semantic Versioning-style release versions:

- **Format**: `MAJOR.MINOR.PATCH` or `MAJOR.MINOR.PATCH-PRERELEASE`
- **MAJOR**: Breaking changes (incompatible API changes)
- **MINOR**: New features (backward-compatible)
- **PATCH**: Bug fixes (backward-compatible)
- **Build metadata**: `+build` suffixes are not supported because Docker image tags do not support
  `+`.

### **Version Sources**

1. **Merged release PR** (Primary):

   - The umbrella Helm chart `version` controls GitHub Release tags and chart release assets.
   - Operator, API server, and gateway chart `appVersion` fields control Docker image publishing for
     each component independently.
   - Component chart `version` fields may move independently, but umbrella dependency versions must
     match the corresponding component chart versions.
   - The PR must be opened from a CODEOWNER-owned branch, approved, and merged by a CODEOWNER.
   - GitHub Actions creates the annotated `v<umbrella-version>` tag after the merge only when the
     umbrella chart `version` changes.
   - Manual release tag pushes should be blocked with a repository ruleset.

   Docker images are tagged with each component image version, for example
   `ghcr.io/xtrinode/xtrinode-operator:0.1.0`. GitHub Release assets are tied to the umbrella chart
   version, for example `xtrinode-0.1.0.tgz`.

   The managed Trino runtime image is pinned separately, for example `trinodb/trino:480`. That
   runtime pin is aligned with upstream chart `trino-1.42.2`, but it is not an XTrinode chart or
   control-plane image version.

2. **Makefile Variable**:

   ```bash
   make docker-build OPERATOR_IMAGE_TAG=0.1.0 GATEWAY_IMAGE_TAG=0.1.0 API_SERVER_IMAGE_TAG=0.1.0
   ```

3. **Git Tag Detection**:
   - Exact `v<release-version>` tags are used as the default version, with the leading `v` stripped.
   - If the current commit is not exactly tagged, the default version is `dev`.
   - Set `OPERATOR_IMAGE_TAG`, `GATEWAY_IMAGE_TAG`, or `API_SERVER_IMAGE_TAG` explicitly for manual
     component image builds from an untagged checkout.

---

## Docker Image Packaging

### **Image Registry**

- **Default**: GitHub Container Registry (`ghcr.io`)
- **Format**: `ghcr.io/<owner>/<component-image>:<appVersion>`
- **Example**: `ghcr.io/xtrinode/xtrinode-operator:0.1.0`

### **Image Tags**

Stable component image `appVersion` releases create these tags:

1. **Version Tag**: `0.1.0` (exact image version)
2. **Major.Minor Tag**: `0.1` (latest patch for minor version)
3. **Major Tag**: `0` (latest minor for major version)
4. **Latest Tag**: `latest` (latest stable release; not created for prereleases)

Image publishing is component-scoped. If only `helm/xtrinode-gateway/Chart.yaml` `appVersion`
changes, only the gateway image is built, scanned, and pushed.

Patch, minor, and major image releases use the same tagging rules. For example, an appVersion bump
to `0.1.1` publishes `0.1.1`, `0.1`, `0`, and `latest`; `0.2.0` publishes `0.2.0`, `0.2`, `0`, and
`latest`; `1.0.0` publishes `1.0.0`, `1.0`, `1`, and `latest`. Prerelease image versions do not
move floating tags; `1.0.0-rc.1` publishes only `1.0.0-rc.1`. Umbrella prerelease chart versions
create prerelease GitHub Releases.

Exact image version tags are treated as immutable. Before publishing a component image, release CI
checks whether `ghcr.io/<owner>/<component-image>:<appVersion>` already exists. If it exists with a
different `org.opencontainers.image.revision` or version label, CI fails and the component
`appVersion` must be bumped. If it already exists for the same release commit, CI treats the run as
a recovery rerun, skips the rebuild and exact-tag push, and restores stable floating tags from the
existing exact tag.

### **Architecture Support**

- **Architecture**: `linux/amd64`
- **Build**: Uses Docker Buildx for release image publishing
- **CI**: Docker image builds run only on the release publishing path, not on pull request commits
- **Override**: Set `DOCKER_PLATFORMS` explicitly for local experimental platform builds; the
  release workflow publishes `linux/amd64`

### **Image Build Process**

```bash
# Local build
make docker-build OPERATOR_IMAGE_TAG=0.1.0 GATEWAY_IMAGE_TAG=0.1.0 API_SERVER_IMAGE_TAG=0.1.0

# Manual component Buildx push, outside release automation
make docker-buildx-operator OPERATOR_IMAGE_TAG=0.1.0 IMG=ghcr.io/xtrinode/xtrinode-operator:0.1.0

# Push to registry
make docker-push OPERATOR_IMAGE_TAG=0.1.0 GATEWAY_IMAGE_TAG=0.1.0 API_SERVER_IMAGE_TAG=0.1.0
```

For a single component, use the component-specific targets and image variable:

```bash
make docker-build-operator OPERATOR_IMAGE_TAG=0.1.0 IMG=ghcr.io/xtrinode/xtrinode-operator:0.1.0
make docker-push-operator OPERATOR_IMAGE_TAG=0.1.0 IMG=ghcr.io/xtrinode/xtrinode-operator:0.1.0
```

---

## Helm Chart Packaging

### **Chart Version**

- **Location**: `helm/xtrinode/Chart.yaml`
- **Version**: Chart package version, for example `0.1.0`
- **AppVersion**: Default component image version for component charts. The umbrella chart
  `appVersion` is product metadata and does not drive image publishing.
- **Trino runtime tag**: Managed workload image tag, for example `480`, configured separately via
  `TRINO_IMAGE_TAG`, `internal/config`, or `XTrinode.spec.valuesOverlay.image`. The current Trino
  compatibility target is upstream chart `trino-1.42.2` / app `480`.

### **Chart Packaging**

```bash
# Package chart
helm package helm/xtrinode-operator

# Output: xtrinode-operator-0.1.0.tgz
```

### **Chart Distribution**

1. **GitHub Releases** (Current):
   - Chart packaged and uploaded to GitHub Releases
   - Download: `https://github.com/xtrinode/xtrinode/releases/download/v0.1.0/xtrinode-operator-0.1.0.tgz`

2. **OCI Registry / Helm Repository**:
   - Not implemented.
   - Do not expect release CI to push Helm charts to GHCR OCI repositories or a Helm repository.

---

## Release Process

### **1. Pre-Release Checklist**

- [ ] All tests passing (`make test`)
- [ ] Code coverage > 70% (`make test-coverage`)
- [ ] Linting passes (`make lint`)
- [ ] Manifests up to date (`make verify-manifests`)
- [ ] Documentation updated
- [ ] Release notes inputs reviewed; GitHub Release notes are generated from merged commits

### **2. Create Release PR**

```bash
# 1. Update release metadata:
#    - For a GitHub/Helm release, update helm/xtrinode/Chart.yaml version.
#    - For a component image release, update that component chart appVersion.
#    - For a component chart version bump, update that component chart version and
#      the matching umbrella dependency version.
#    helm/xtrinode-api-server/Chart.yaml
#    helm/xtrinode-gateway/Chart.yaml
#    helm/xtrinode-operator/Chart.yaml
#    helm/xtrinode/Chart.yaml
#
# 2. Refresh the umbrella lock after dependency version changes:
helm dependency update helm/xtrinode
#
# 3. Commit the version bump and open a PR
git add .
git commit -m "Release v0.1.0"
git push origin release/v0.1.0
```

### **3. GitHub Actions Workflow**

When a release PR opened from an explicit CODEOWNER-owned branch is merged to `main` by an explicit
CODEOWNER, GitHub Actions automatically:

1. Runs tests
2. Creates the annotated release tag when the umbrella chart version changed
3. Builds Linux amd64 Docker images only for components whose `appVersion` changed
4. Pushes changed images to `ghcr.io`
5. Packages Helm charts when the umbrella chart version changed
6. Creates GitHub Release when the umbrella chart version changed
7. Uploads Helm charts to the release when the umbrella chart version changed

### **4. Verify Release**

```bash
# Check Docker image
docker pull ghcr.io/xtrinode/xtrinode-operator:0.1.0

# Check GitHub Release
# Visit: https://github.com/xtrinode/xtrinode/releases/tag/v0.1.0

# Test Helm chart
helm install xtrinode-operator ./helm/xtrinode-operator \
  --set image.tag=0.1.0
```

---

## Version Information in Binary

The operator binary includes version information:

```go
// Build-time variables
var (
    version   string // Set via -ldflags
    commit    string // Git commit SHA
    buildDate string // Build timestamp
)
```

**Access via CLI**:

```bash
make build-operator
./bin/operator --version
# Output: xtrinode-operator version 0.1.0 (commit: abc1234, built: 2025-01-15T10:00:00Z)
```

---

## CI/CD Pipeline

### **On Pull Request or Push to Main**

1. **Lint**: Go fmt, vet, golangci-lint, Helm lint, and markdown lint
2. **Test**: Unit tests with coverage
3. **Verify**: Manifests up to date
4. **Security**: Trivy filesystem/config checks
5. **Image Build**: Skipped; image publishing is limited to the release path

CI and local toolchain versions are centralized in [TOOLING.md](TOOLING.md). Node-based documentation tooling is pinned
through `package.json` and `package-lock.json`.

### **On Release PR Merge**

1. **Detect Chart Release**: From the updated umbrella chart version
2. **Authorize**: Confirm the PR author, branch owner, and merger are explicit CODEOWNERS
3. **Tag**: Create `v<umbrella-version>` from the umbrella chart version after CI passes
4. **Detect Images**: Compare each component chart `appVersion` independently
5. **Guard Tags**: Refuse conflicting existing exact image tags before publishing
6. **Scan**: Build and Trivy-scan each changed component image before pushing tags
7. **Build**: Linux amd64 Docker images for changed components
8. **Push**: Changed images with component image tags (`0.1.0`, `0.1`, `0`, `latest` for stable
   versions; exact tag only for prereleases)
9. **Package**: Helm charts when the umbrella chart version changed
10. **Release**: Create GitHub Release with notes and chart artifacts when the umbrella chart version changed

---

## What Gets Pushed Where

### **Docker Images** → `ghcr.io`

- **Registry**: GitHub Container Registry
- **Repositories**:
  `ghcr.io/<owner>/xtrinode-operator`,
  `ghcr.io/<owner>/xtrinode-api-server`,
  `ghcr.io/<owner>/xtrinode-gateway`
- **Tags**: Stable component image version, major.minor, major, latest; prerelease exact tag only
- **Architecture**: linux/amd64
- **Trigger**: Per-component chart `appVersion` changes

### **Helm Charts** → GitHub Releases

- **Location**: GitHub Releases (attached `.tgz` file)
- **Format**:
  `xtrinode-<version>.tgz`,
  `xtrinode-operator-<version>.tgz`,
  `xtrinode-api-server-<version>.tgz`,
  `xtrinode-gateway-<version>.tgz`
- **Trigger**: Umbrella chart `version` changes
- **Not published**: OCI Helm chart publishing is not implemented; release charts are GitHub
  Release assets only.

### **Coverage Reports** → GitHub Actions Artifacts

- **Location**: Pull request and main-branch workflow runs
- **Format**: `coverage-report` artifact containing `xtrinode/coverage.out`
- **Retention**: 7 days
- **Release assets**: Coverage reports are not attached to GitHub Releases

### **Terraform** → Validation Only

- Terraform CI validates configuration. Release CI does not publish Terraform state, plans, modules,
  or artifacts.

### **Source Code** → GitHub Repository

- **Branches**: `main`
- **Tags**: `v*.*.*` release tags created by the release workflow
- **Releases**: GitHub Releases (with notes and assets)

---

## Version Detection

### **In Makefile**

```makefile
GIT_TAG ?= $(shell git describe --tags --exact-match 2>/dev/null || true)
VERSION ?= $(if $(GIT_TAG),$(patsubst v%,%,$(GIT_TAG)),dev)
OPERATOR_IMAGE_VERSION ?= $(shell awk '/^appVersion:/ {print $$2; exit}' helm/xtrinode-operator/Chart.yaml 2>/dev/null | tr -d '"' || echo "dev")
GATEWAY_IMAGE_VERSION ?= $(shell awk '/^appVersion:/ {print $$2; exit}' helm/xtrinode-gateway/Chart.yaml 2>/dev/null | tr -d '"' || echo "dev")
API_SERVER_IMAGE_VERSION ?= $(shell awk '/^appVersion:/ {print $$2; exit}' helm/xtrinode-api-server/Chart.yaml 2>/dev/null | tr -d '"' || echo "dev")
OPERATOR_IMAGE_TAG ?= $(if $(GIT_TAG),$(OPERATOR_IMAGE_VERSION),dev)
GATEWAY_IMAGE_TAG ?= $(if $(GIT_TAG),$(GATEWAY_IMAGE_VERSION),dev)
API_SERVER_IMAGE_TAG ?= $(if $(GIT_TAG),$(API_SERVER_IMAGE_VERSION),dev)
OPERATOR_CLOUD_IMAGE_TAG ?= $(if $(filter dev,$(OPERATOR_IMAGE_TAG)),$(OPERATOR_IMAGE_VERSION),$(OPERATOR_IMAGE_TAG))
GATEWAY_CLOUD_IMAGE_TAG ?= $(if $(filter dev,$(GATEWAY_IMAGE_TAG)),$(GATEWAY_IMAGE_VERSION),$(GATEWAY_IMAGE_TAG))
API_SERVER_CLOUD_IMAGE_TAG ?= $(if $(filter dev,$(API_SERVER_IMAGE_TAG)),$(API_SERVER_IMAGE_VERSION),$(API_SERVER_IMAGE_TAG))
```

`VERSION` identifies the umbrella chart release when the checkout is exactly on a release tag.
Component image tag defaults resolve from each component chart `appVersion` on release tags and to
`dev` otherwise. Cloud publish/deploy defaults are component-specific: each `*_CLOUD_IMAGE_TAG`
uses that component's chart `appVersion` when the matching local `*_IMAGE_TAG` would otherwise be
`dev`.

### **In GitHub Actions**

Release version detection lives in `scripts/ci/prepare-release.sh` and is called through
`make ci-prepare-release`.

### **In Go Code**

```go
// Set via ldflags
-ldflags "-X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)"
```

---

## Best Practices

1. **Release Through PRs**: Do not push release tags manually
2. **Update Chart.yaml deliberately**: Keep umbrella dependency versions aligned with the matching
   component chart versions. Bump component `appVersion` fields only for images that should publish.
3. **Test Before Release**: Run full test suite and linting
4. **Document Changes**: Keep docs and PR descriptions clear enough for generated release notes
5. **Image Builds**: Keep release image builds on the release publishing path
6. **Immutable Tags**: Never overwrite exact version tags (use a new component `appVersion`)
7. **Release Notes**: Include meaningful release notes in GitHub Releases

---

## Dependency Update Coverage

Dependabot updates the main Go module, CI Go tool pins in `tools/go.mod`, Node-based CI tools in
`package.json`, GitHub Actions references, Docker base images, external Helm chart dependencies, and
Terraform modules. The workflows pin actions by full commit SHA and keep the readable tag in a
comment.

Makefile-owned runner tool pins such as `HELM_VERSION`, `NODE_VERSION`, and `TERRAFORM_VERSION` are
centralized for review. Dependabot does not have a native ecosystem that updates those arbitrary
workflow tool inputs automatically.

---

## References

- **Semantic Versioning**: <https://semver.org/>
- **GitHub Container Registry**:
  <https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry>
- **Helm Chart Versioning**: <https://helm.sh/docs/topics/charts/#the-chartyaml-file>
- **Docker Buildx**: <https://docs.docker.com/build/builders/>
