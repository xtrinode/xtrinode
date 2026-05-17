# Contributing to XTrinode

Thanks for contributing to XTrinode. This guide covers the practical path from a local change to a
reviewable pull request. For deeper repository rules, read [AGENTS.md](AGENTS.md); it is the
canonical engineering guide for humans and coding agents working in this repo.

## Contribution Priorities

We value contributions in this order:

1. Correctness and safety fixes: crashes, reconciliation bugs, data loss risks, unsafe auth or secret handling.
2. Deployment reliability: GKE, local k3d/Tilt, Helm, Terraform, image publishing, and cleanup flows.
3. Control-plane behavior: `XTrinode`, `XTrinodeCatalog`, lifecycle, KEDA, gateway routing, and API server gates.
4. Provider parity: AWS and Azure should track the validated GCP path, with clear notes when live smoke coverage is
   still pending.
5. Observability and operations: metrics, logs, status conditions, events, and troubleshooting material.
6. Documentation and examples: keep docs concise, accurate, and linked from the right canonical page.

Prefer focused pull requests. Avoid mixing behavior changes, broad refactors, generated output, and documentation
rewrites unless they are needed for one coherent change.

## Before You Start

- Check existing issues and docs to avoid duplicating work.
- Keep durable project guidance in [AGENTS.md](AGENTS.md), not in package-local agent files.
- Do not commit local/editor state such as `.codex/`, `.agents/`, `.cursor/`, kubeconfigs, Terraform state,
  `terraform.tfvars`, credentials, tokens, or generated machine-specific files.
- For security-sensitive issues, do not publish secrets or exploit details in public reports. Use the private security
  reporting path when available or contact the maintainers listed in [README.md](README.md#security).

## Fork And Pull Request Workflow

External contributors should use a fork and open pull requests back to this repository. Keep one branch per logical
change.

1. Fork `xtrinode/xtrinode` on GitHub.
2. Clone your fork and add the upstream repository:

   ```bash
   git clone git@github.com:<your-user>/xtrinode.git
   cd xtrinode
   git remote add upstream https://github.com/xtrinode/xtrinode.git
   git fetch upstream
   ```

3. Create a branch from the latest upstream `main`:

   ```bash
   git checkout -b fix/short-topic upstream/main
   ```

4. Make the change, run the relevant checks, then commit:

   ```bash
   git status
   git add <changed-files>
   git commit -m "fix: short description"
   ```

5. Push to your fork:

   ```bash
   git push -u origin fix/short-topic
   ```

6. Open a pull request with:
   - base repository: `xtrinode/xtrinode`
   - base branch: `main`
   - head repository: your fork
   - compare branch: your topic branch

Leave "Allow edits from maintainers" enabled when GitHub offers it. In the PR description, include the goal, notable
risk, validation run, and any skipped checks with a reason.

When upstream changes while your PR is open, rebase and update your fork branch:

```bash
git fetch upstream
git rebase upstream/main
git push --force-with-lease
```

Version-bump release PRs are different: they must be opened from a CODEOWNER-owned branch and merged by a CODEOWNER.
External contributors should not bump release versions unless a maintainer asks for that exact change.

## Development Setup

Required tools are listed in [README.md](README.md#build-requirements). The common baseline is:

- Go version matching `xtrinode/go.mod`
- Docker
- Helm
- Terraform
- `kubectl`
- Node.js 22 for markdown tooling
- k3d and Tilt for the local development stack

Useful first commands:

```bash
make help
make helm-deps
make build-all
make lint-markdown
```

For local cluster work:

```bash
make dev-up
make test-e2e-local-smoke
make dev-down
```

For cloud deployment flow, start with [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md). GCP is the most complete live-tested
path today; AWS and Azure should remain coherent with GCP but are documented as not fully live-smoke validated yet.

## Project Structure

```text
xtrinode/                  Go module for operator, API server, gateway, controllers, and shared packages
helm/                      Helm charts for operator, API server, gateway, umbrella chart, and observability
terraform/                 Cloud infrastructure modules for GCP, AWS, and Azure
scripts/                   Deployment, CAPI, and local automation scripts
docs/                      Architecture, deployment, operations, and provider documentation
examples/                  XTrinode and XTrinodeCatalog example manifests
tilt/                      Local k3d/Tilt and Robot Framework e2e workflows
.github/workflows/         CI and release automation
```

Key references:

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for control plane, query plane, routing, catalogs, scaling, and lifecycle.
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for cloud and local deployment paths.
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) for operational checks.
- [docs/CODEOWNERS.md](docs/CODEOWNERS.md) and [.github/CODEOWNERS](.github/CODEOWNERS) for ownership.

## Coding Standards

- Keep `cmd/<service>/main.go` wiring-only. Put business logic in `internal/` or existing domain packages.
- Prefer existing package boundaries and helper APIs over new abstractions.
- Use typed Kubernetes APIs and structured parsers where practical.
- Pass `context.Context` as the first argument for request-scoped work.
- Wrap errors with useful context and `%w`; use `errors.Is` and `errors.As` for branching.
- Do not log secrets, tokens, credentials, or raw auth headers.
- Keep HTTP handlers thin: parse, validate, call a service, map the response.
- Run `gofmt` on changed Go files.

The lint config also expects checked type assertions, handled errors, no avoidable shadowed `err`, and current
controller-runtime patterns.

## Tests And Validation

Run checks that match the risk of your change. For small docs-only changes, markdown lint and diff checks are usually
enough. For Go, Helm, Terraform, or deployment changes, run the relevant Make targets.

Common checks:

```bash
make ci-lint
make ci-test
make ci-verify-manifests
make ci-terraform-validate-all
make ci-security
```

To mirror the main CI build pipeline locally, run:

```bash
make ci-build
```

Targeted checks:

```bash
make test
make test-integration
make test-e2e-local-smoke
make test-e2e-local-contracts
```

Run the full local e2e surface before release-impacting changes or broad
control-plane/gateway/lifecycle changes:

```bash
make test-e2e-local
make test-e2e-local-loadtest
make test-e2e-local-operator-stress
```

Cloud live validation is separate from default CI because it needs real provider
credentials and a selected target cluster. For the GCP/CAPG path, use:

```bash
make gcp-capg-nodepool-smoke
make gcp-keda-resume-smoke
```

Cloud scripts should at least pass shell syntax validation:

```bash
bash -n scripts/deploy-gcp.sh scripts/deploy-aws.sh scripts/deploy-azure.sh
```

Terraform changes should pass formatting and validation for the touched provider module:

```bash
terraform fmt -check -recursive terraform/gcp terraform/aws terraform/azure
terraform -chdir=terraform/gcp validate
terraform -chdir=terraform/aws validate
terraform -chdir=terraform/azure validate
```

If a check is skipped, mention why in the PR.

## Documentation Standards

- Keep the root [README.md](README.md) short and route detailed explanations to docs.
- Treat [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) as the canonical explanation for system design, routing,
  catalog flow, runtime configuration layers, gateway auth/Redis/KEDA, and lifecycle.
- Keep provider-specific docs honest about validation status. Do not imply AWS or Azure is live-tested until it is.
- Prefer short redirect docs over duplicated long-form content when a topic has moved.
- Update examples and troubleshooting commands when labels, namespaces, resource names, or scripts change.

## Pull Request Checklist

Before opening or marking a PR ready:

- Scope is focused and intentional.
- Generated artifacts are updated only when needed.
- No credentials, kubeconfigs, Terraform state, `terraform.tfvars`, or local/editor files are included.
- Relevant tests and lint checks pass, or skipped checks are explicitly explained.
- User-facing behavior changes are reflected in docs and examples.
- Cloud-provider differences are called out rather than hidden.
- Breaking or risky operational changes include migration or rollback notes.

## Release-Sensitive Changes

Changes to CRDs, Helm chart defaults, Terraform modules, image tags, admission behavior, or gateway/API compatibility can
affect running clusters. For these changes, include:

- What changes for existing installations.
- Whether `make manifests`, Helm dependency updates, or chart lock updates are required.
- Whether rollout order matters.
- Any cleanup, migration, or rollback command operators should know.

## Community Conduct

Be direct, technical, and respectful. Review comments should focus on correctness, maintainability, security, and
operability. If a design is unclear, ask for the operational goal and constraints before expanding the scope.
