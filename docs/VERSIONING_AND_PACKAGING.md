# Versioning and Packaging Guide

## Versioning Strategy

### **Semantic Versioning**

We use [Semantic Versioning](https://semver.org/) (SemVer) for releases:

- **Format**: `MAJOR.MINOR.PATCH` (e.g., `1.2.3`)
- **MAJOR**: Breaking changes (incompatible API changes)
- **MINOR**: New features (backward-compatible)
- **PATCH**: Bug fixes (backward-compatible)

### **Version Sources**

1. **Merged release PR** (Primary):

   - A release PR bumps all XTrinode Helm chart `version` fields, `appVersion` fields, and umbrella
     dependency versions to the intended XTrinode release.
   - The PR must be opened from a CODEOWNER-owned branch, approved, and merged by a CODEOWNER.
   - GitHub Actions creates the annotated `vMAJOR.MINOR.PATCH` tag after the merge.
   - Manual release tag pushes should be blocked with a repository ruleset.

   Docker images are tagged with the XTrinode component image version, for example
   `ghcr.io/xtrinode/xtrinode-operator:0.1.0`. All XTrinode Helm charts use the same release
   version, for example `xtrinode-0.1.0.tgz`.

   The managed Trino runtime image is pinned separately, for example `trinodb/trino:480`. That
   runtime pin is aligned with upstream chart `trino-1.42.2`, but it is not an XTrinode chart or
   control-plane image version.

2. **Makefile Variable**:

   ```bash
   make docker-build VERSION=0.1.0
   ```

3. **Git Tag Detection** (Fallback):
   - Exact `vMAJOR.MINOR.PATCH` tags are used as the default version, with the leading `v` stripped.
   - If the current commit is not exactly tagged, the default version is `dev`.
   - Set `VERSION=0.1.0` explicitly for manual component image builds.

---

## Docker Image Packaging

### **Image Registry**

- **Default**: GitHub Container Registry (`ghcr.io`)
- **Format**: `ghcr.io/<owner>/<component-image>:<appVersion>`
- **Example**: `ghcr.io/xtrinode/xtrinode-operator:0.1.0`

### **Image Tags**

Multiple tags are created from the component image `appVersion` for each build:

1. **Version Tag**: `0.1.0` (exact image version)
2. **Major.Minor Tag**: `0.1` (latest patch for minor version)
3. **Major Tag**: `0` (latest minor for major version)
4. **Latest Tag**: `latest` (latest release, main branch only)

### **Architecture Support**

- **Architecture**: `linux/amd64`
- **Build**: Uses Docker Buildx for release image publishing
- **CI**: Docker image builds run only on the release publishing path, not on pull request commits

### **Image Build Process**

```bash
# Local build
make docker-build VERSION=0.1.0

# Buildx release publish
make docker-buildx

# Push to registry
make docker-push VERSION=0.1.0
```

For a single component, use the component-specific targets and image variable:

```bash
make docker-build-operator IMG=ghcr.io/xtrinode/xtrinode-operator:0.1.0
make docker-push-operator IMG=ghcr.io/xtrinode/xtrinode-operator:0.1.0
```

---

## Helm Chart Packaging

### **Chart Version**

- **Location**: `helm/xtrinode/Chart.yaml`
- **Version**: XTrinode chart package version, for example `0.1.0`
- **AppVersion**: XTrinode component image version, matching the XTrinode release version
- **Trino runtime tag**: Managed workload image tag, for example `480`, configured separately via
  `TRINO_IMAGE_TAG`, `internal/config`, or `XTrinode.spec.valuesOverlay.image`. The current Trino
  compatibility target is upstream chart `trino-1.42.2` / app `480`.

### **Chart Packaging**

```bash
# Package chart
cd helm/xtrinode-operator
helm package . --version 0.1.0 --app-version 0.1.0

# Output: xtrinode-operator-0.1.0.tgz
```

### **Chart Distribution**

1. **GitHub Releases** (Current):
   - Chart packaged and uploaded to GitHub Releases
   - Download: `https://github.com/xtrinode/xtrinode/releases/download/v0.1.0/xtrinode-operator-0.1.0.tgz`

2. **OCI Registry** (Future):

   ```bash
   helm push xtrinode-operator-0.1.0.tgz oci://ghcr.io/xtrinode/xtrinode-operator-chart
   ```

3. **Helm Repository** (Future):
   - Host chart repository (e.g., GitHub Pages)
   - Add repo: `helm repo add xtrinode https://xtrinode.github.io/xtrinode/charts`

---

## Release Process

### **1. Pre-Release Checklist**

- [ ] All tests passing (`make test`)
- [ ] Code coverage > 70% (`make test-coverage`)
- [ ] Linting passes (`make lint`)
- [ ] Manifests up to date (`make verify-manifests`)
- [ ] Documentation updated
- [ ] CHANGELOG.md updated

### **2. Create Release PR**

```bash
# 1. Update chart version fields, appVersion fields, and umbrella dependency versions:
#    helm/xtrinode-api-server/Chart.yaml
#    helm/xtrinode-gateway/Chart.yaml
#    helm/xtrinode-operator/Chart.yaml
#    helm/xtrinode/Chart.yaml
#
# 2. Commit the version bump and open a PR
git add .
git commit -m "Release v0.1.0"
git push origin release/v0.1.0
```

### **3. GitHub Actions Workflow**

When a release PR opened from an explicit CODEOWNER-owned branch is merged to `main` by an explicit
CODEOWNER, GitHub Actions automatically:

1. Runs tests
2. Creates the annotated release tag
3. Builds Linux amd64 Docker images
4. Pushes images to `ghcr.io`
5. Packages Helm charts
6. Creates GitHub Release
7. Uploads Helm charts to the release

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

1. **Lint**: Go fmt, vet, golangci-lint, and Helm lint
2. **Test**: Unit tests with coverage
3. **Verify**: Manifests up to date
4. **Security**: Trivy filesystem/config checks
5. **Image Build**: Skipped; image publishing is limited to the release path

### **On Release PR Merge**

1. **Detect Version**: From the updated umbrella chart version
2. **Authorize**: Confirm the PR author, branch owner, and merger are explicit CODEOWNERS
3. **Tag**: Create `vMAJOR.MINOR.PATCH` from the chart version after CI passes
4. **Scan**: Build and Trivy-scan each release image before pushing tags
5. **Build**: Linux amd64 Docker images
6. **Push**: Images with component image tags (`0.1.0`, `0.1`, `0`, `latest`)
7. **Package**: Helm charts
8. **Release**: Create GitHub Release with notes and chart artifacts

---

## What Gets Pushed Where

### **Docker Images** → `ghcr.io`

- **Registry**: GitHub Container Registry
- **Repository**: `ghcr.io/<owner>/xtrinode-operator`
- **Tags**: Component image version, major.minor, major, latest
- **Architecture**: linux/amd64

### **Helm Charts** → GitHub Releases

- **Location**: GitHub Releases (attached `.tgz` file)
- **Format**: `xtrinode-operator-<version>.tgz`
- **Future**: OCI registry (`ghcr.io/<owner>/xtrinode-operator-chart`)

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
IMAGE_VERSION ?= $(shell awk '/^appVersion:/ {print $$2; exit}' helm/xtrinode/Chart.yaml 2>/dev/null | tr -d '"' || echo "$(VERSION)")
IMAGE_TAG ?= $(if $(GIT_TAG),$(IMAGE_VERSION),$(VERSION))
CLOUD_IMAGE_TAG ?= $(if $(filter dev,$(IMAGE_TAG)),$(IMAGE_VERSION),$(IMAGE_TAG))
```

`VERSION` identifies the XTrinode release when the checkout is exactly on a release tag. `IMAGE_TAG`
identifies the default local control-plane image tag and resolves to the same chart `appVersion` on
release tags. `CLOUD_IMAGE_TAG` keeps cloud publish/deploy defaults on `appVersion` instead of
publishing the local `dev` tag by accident.

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
2. **Update Chart.yaml deliberately**: Keep all XTrinode chart `version` fields, umbrella dependency
   versions, and `appVersion` fields in sync on the XTrinode release version.
3. **Test Before Release**: Run full test suite and linting
4. **Document Changes**: Update CHANGELOG.md for each release
5. **Image Builds**: Keep release image builds on the release publishing path
6. **Immutable Tags**: Never overwrite version tags (use new version)
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
