# XTrinode Operator Makefile

# Image configuration
REGISTRY ?= ghcr.io/xtrinode
OPERATOR_IMAGE_NAME ?= xtrinode-operator
GATEWAY_IMAGE_NAME ?= xtrinode-gateway
API_SERVER_IMAGE_NAME ?= xtrinode-api-server
GIT_TAG ?= $(shell git describe --tags --exact-match 2>/dev/null || true)
VERSION ?= $(if $(GIT_TAG),$(patsubst v%,%,$(GIT_TAG)),dev)
IMAGE_VERSION ?= $(shell awk '/^appVersion:/ {print $$2; exit}' helm/xtrinode/Chart.yaml 2>/dev/null | tr -d '"' || echo "$(VERSION)")
IMAGE_TAG ?= $(if $(GIT_TAG),$(IMAGE_VERSION),$(VERSION))
CLOUD_IMAGE_TAG ?= $(if $(filter dev,$(IMAGE_TAG)),$(IMAGE_VERSION),$(IMAGE_TAG))
IMG ?= $(REGISTRY)/$(OPERATOR_IMAGE_NAME):$(IMAGE_TAG)
GATEWAY_IMG ?= $(REGISTRY)/$(GATEWAY_IMAGE_NAME):$(IMAGE_TAG)
API_SERVER_IMG ?= $(REGISTRY)/$(API_SERVER_IMAGE_NAME):$(IMAGE_TAG)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go configuration
GO_VERSION ?= $(shell awk '/^go / {print $$2; exit}' $(OPERATOR_DIR)/go.mod 2>/dev/null || echo 1.26.3)
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0
OPERATOR_DIR ?= xtrinode

# Kubernetes configuration - Multi-namespace strategy
# See docs/ARCHITECTURE.md#namespace-and-isolation-model for architecture details
OPERATOR_NAMESPACE ?= xtrinode-system
GATEWAY_NAMESPACE ?= xtrinode-gateway
API_SERVER_NAMESPACE ?= xtrinode-system

RELEASE_NAME ?= xtrinode-operator

# Helm configuration
HELM_CHART_PATH ?= helm/xtrinode-operator
GATEWAY_HELM_CHART_PATH ?= helm/xtrinode-gateway
API_SERVER_HELM_CHART_PATH ?= helm/xtrinode-api-server
UMBRELLA_HELM_CHART_PATH ?= helm/xtrinode
OBSERVABILITY_HELM_CHART_PATH ?= helm/xtrinode-observability
VECTOR_HELM_CHART_PATH ?= helm/xtrinode-vector
HELM_LINT_CHARTS ?= \
	xtrinode-operator=$(HELM_CHART_PATH) \
	xtrinode-api-server=$(API_SERVER_HELM_CHART_PATH) \
	xtrinode-gateway=$(GATEWAY_HELM_CHART_PATH) \
	xtrinode=$(UMBRELLA_HELM_CHART_PATH) \
	xtrinode-observability=$(OBSERVABILITY_HELM_CHART_PATH) \
	xtrinode-vector=$(VECTOR_HELM_CHART_PATH)
HELM_TEMPLATE_CHARTS ?= $(HELM_LINT_CHARTS)

# Docker configuration
DOCKERFILE ?= docker/Dockerfile
ALPINE_VERSION ?= $(shell version=$$(awk -F= '/^ARG ALPINE_VERSION=/ {print $$2; exit}' $(DOCKERFILE) 2>/dev/null); printf '%s' "$${version:-3.23}")
DOCKER_PLATFORMS ?= linux/amd64,linux/arm64
DOCKER_BUILDER ?= xtrinode-builder
OPERATOR_DOCKERFILE ?= $(DOCKERFILE)
GATEWAY_DOCKERFILE ?= $(DOCKERFILE)
API_SERVER_DOCKERFILE ?= $(DOCKERFILE)
OPERATOR_PACKAGE ?= ./cmd/operator
GATEWAY_PACKAGE ?= ./cmd/gateway
API_SERVER_PACKAGE ?= ./cmd/api-server
OPERATOR_PORT ?= 8081
GATEWAY_PORT ?= 8080
API_SERVER_PORT ?= 8081
BIN_DIR ?= bin
OPERATOR_BIN ?= $(BIN_DIR)/operator
GATEWAY_BIN ?= $(BIN_DIR)/gateway
API_SERVER_BIN ?= $(BIN_DIR)/api-server

# Local k3d, Tilt, and Robot configuration
K3D ?= k3d
K3D_CLUSTER_NAME ?= xtrinode-dev
K3D_REGISTRY_NAME ?= xtrinode-registry
K3D_REGISTRY_PORT ?= 5001
K3D_AGENTS ?= 1
K3D_K3S_IMAGE ?= rancher/k3s:v1.32.3-k3s1
TILT ?= tilt
TILTFILE ?= tilt/Tiltfile
TILT_IMAGE_TAG ?= tilt
TILT_TRINO_IMAGE_TAG ?= $(TRINO_IMAGE_TAG)
TILT_ARGS ?= -- --image_tag=$(TILT_IMAGE_TAG) --trino_tag=$(TILT_TRINO_IMAGE_TAG) --uv=$(UV)
LOCAL_IMAGE_TAG ?= dev
LOCAL_COMPONENTS ?= operator api-server gateway
LOCAL_REGISTRY_HOST ?= localhost:$(K3D_REGISTRY_PORT)
LOCAL_REGISTRY_CLUSTER ?= $(K3D_REGISTRY_NAME):5000
ROBOT ?= robot
UV ?= uv
ROBOT_PROJECT ?= tilt/e2e
ROBOT_OUTPUT_DIR ?= tilt/e2e/results
UV_CACHE_DIR ?= /tmp/xtrinode-uv-cache
ROBOT_RUNNER_DEFAULT = UV_CACHE_DIR=$(UV_CACHE_DIR) $(UV) run --project $(ROBOT_PROJECT) $(ROBOT)
ROBOT_RUNNER ?= $(ROBOT_RUNNER_DEFAULT)
TRINO_IMAGE_REPOSITORY ?= trinodb/trino
TRINO_IMAGE_TAG ?= 480
LOCAL_PRELOAD_IMAGES ?=
LOCAL_PRELOAD_IMAGES_ENABLED ?= true
LOCAL_PRELOAD_SKIP_PULL ?= false
LOADTEST_USERS ?= 1
LOADTEST_SPAWN_RATE ?= 1
LOADTEST_RUN_TIME ?= 15s
LOADTEST_WAIT_MIN ?= 0.5
LOADTEST_WAIT_MAX ?= 1.0
LOADTEST_MIN_REQUESTS ?= 2
LOADTEST_QUERY ?= SELECT count(*) FROM postgres.public.orders
LOADTEST_AUTOSCALE_USERS ?= 2
LOADTEST_AUTOSCALE_SPAWN_RATE ?= 1
LOADTEST_AUTOSCALE_RUN_TIME ?= 300s
LOADTEST_AUTOSCALE_WAIT_SECONDS ?= 420
LOADTEST_AUTOSCALE_MAX_WORKERS ?= 2
LOADTEST_AUTOSCALE_THRESHOLD ?= 0.5
LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS ?= 420
LOADTEST_AUTOSCALE_QUERY ?= SELECT count(*) FROM "local-tpch".sf1000.lineitem WHERE rand() >= 0
OPERATOR_STRESS_NAMESPACE ?= team-operator-stress
OPERATOR_STRESS_COUNT ?= 12
OPERATOR_STRESS_PATCH_ROUNDS ?= 3
OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS ?= 240
OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA ?= 0
OPERATOR_STRESS_METRICS_PORT ?= 18082
LOCAL_E2E_ROBOT_ARGS = \
	--outputdir "$(ROBOT_OUTPUT_DIR)" \
	--variable NAMESPACE:team-local \
	--variable XTRINODE_NAME:local-trino-keda \
	--variable OPERATOR_NAMESPACE:$(OPERATOR_NAMESPACE) \
	--variable GATEWAY_NAMESPACE:$(GATEWAY_NAMESPACE) \
	--variable API_SERVER_NAMESPACE:$(API_SERVER_NAMESPACE) \
	--variable TRINO_IMAGE_REPOSITORY:$(TRINO_IMAGE_REPOSITORY) \
	--variable TRINO_IMAGE_TAG:$(TRINO_IMAGE_TAG) \
	--variable UV:$(UV)

# Terraform configuration
TF_DIR ?= terraform
TF_ENV ?= dev
TF_VAR_FILE ?= terraform.tfvars
AWS_TF_DIR ?= $(TF_DIR)/aws
AZURE_TF_DIR ?= $(TF_DIR)/azure
GCP_TF_DIR ?= $(TF_DIR)/gcp
TERRAFORM_CLOUDS ?= gcp
TERRAFORM_CONFIG_TARGETS ?= $(foreach cloud,$(TERRAFORM_CLOUDS),$(TF_DIR)/$(cloud))
GCP_PROJECT_ID ?= $(shell awk -F= '/^[[:space:]]*gcp_project_id[[:space:]]*=/ {v=$$2; sub(/[[:space:]]+#.*/,"",v); gsub(/[ "]/,"",v); print v; exit}' $(GCP_TF_DIR)/$(TF_VAR_FILE) 2>/dev/null)
GCP_REGION ?= $(or $(shell awk -F= '/^[[:space:]]*gcp_region[[:space:]]*=/ {v=$$2; sub(/[[:space:]]+#.*/,"",v); gsub(/[ "]/,"",v); print v; exit}' $(GCP_TF_DIR)/$(TF_VAR_FILE) 2>/dev/null),us-central1)
GCP_ZONE ?= $(or $(shell awk -F= '/^[[:space:]]*gcp_zone[[:space:]]*=/ {v=$$2; sub(/[[:space:]]+#.*/,"",v); gsub(/[ "]/,"",v); print v; exit}' $(GCP_TF_DIR)/$(TF_VAR_FILE) 2>/dev/null),us-central1-a)
GCP_CLUSTER_NAME ?= $(or $(shell awk -F= '/^[[:space:]]*cluster_name[[:space:]]*=/ {v=$$2; sub(/[[:space:]]+#.*/,"",v); gsub(/[ "]/,"",v); print v; exit}' $(GCP_TF_DIR)/$(TF_VAR_FILE) 2>/dev/null),xtrinode-gke-test)
GCP_OPERATOR_NAMESPACE ?= xtrinode-system
GATEWAY_REPLICA_COUNT ?= 1
GATEWAY_REDIS_ENABLED ?= false
CAPG_WORKLOAD_CLUSTER_NAME ?= xtrinode-capg-workload
CAPG_WORKLOAD_NAMESPACE ?= xtrinode-capg-real
CAPG_WORKLOAD_KUBECONFIG ?= /tmp/$(CAPG_WORKLOAD_CLUSTER_NAME).kubeconfig
CAPG_WAIT_TIMEOUT ?= 30m
CONFIRM_DESTROY ?=
FORCE_NAMESPACE_FINALIZERS ?= false
TEARDOWN_FORCE_NAMESPACE_FINALIZERS ?= true
TEARDOWN_NAMESPACE_WAIT_TIMEOUT ?= 45s

# Linting configuration
GOLANGCI_LINT_VERSION ?= $(shell version=$$(awk '$$1 == "github.com/golangci/golangci-lint/v2" {print $$2; exit}' tools/go.mod 2>/dev/null); printf '%s' "$${version:-v2.12.1}")
GOLANGCI_LINT_BIN ?= $(shell go env GOPATH 2>/dev/null)/bin/golangci-lint
CONTROLLER_GEN_VERSION ?= $(shell version=$$(awk '$$1 == "sigs.k8s.io/controller-tools" {print $$2; exit}' tools/go.mod 2>/dev/null); printf '%s' "$${version:-v0.20.1}")
SETUP_ENVTEST_VERSION ?= v0.24.0
ENVTEST_K8S_VERSION ?= 1.32.0
SETUP_ENVTEST_BIN ?= $(shell go env GOPATH 2>/dev/null)/bin/setup-envtest
LINT_TIMEOUT ?= 1m

# CI tool versions
HELM_VERSION ?= v3.20.0
NODE_VERSION ?= 22
TERRAFORM_VERSION ?= 1.9.0
K3D_VERSION ?= v5.8.3
KUBECTL_VERSION ?= v1.32.3
UV_VERSION ?= 0.9.17
MARKDOWNLINT_CLI_VERSION ?= $(shell version=$$(sed -n 's/.*"markdownlint-cli": "[^0-9v]*\([^"]*\)".*/\1/p' package.json 2>/dev/null | head -n 1); printf '%s' "$${version:-0.47.0}")
MARKDOWNLINT_DOCKER_IMAGE ?= node:22-alpine

# Security tooling
TRIVY_SEVERITY ?= CRITICAL,HIGH
TRIVY_FS_SARIF ?= trivy-fs-results.sarif
TRIVY_IGNORE_FILE ?= .trivyignore.yaml
TRIVY_IGNORE_ARGS := $(if $(wildcard $(TRIVY_IGNORE_FILE)),--ignorefile $(TRIVY_IGNORE_FILE),)
TRIVY_CONFIG_TARGETS ?= \
	docker \
	examples \
	$(TERRAFORM_CONFIG_TARGETS) \
	tilt \
	$(HELM_CHART_PATH) \
	$(API_SERVER_HELM_CHART_PATH) \
	$(GATEWAY_HELM_CHART_PATH) \
	$(UMBRELLA_HELM_CHART_PATH) \
	$(OBSERVABILITY_HELM_CHART_PATH) \
	$(VECTOR_HELM_CHART_PATH)

# Test configuration
COVERAGE_THRESHOLD ?= $(if $(coverage),$(coverage),65)
CI_COVERAGE_THRESHOLD ?= $(COVERAGE_THRESHOLD)
TEST_TIMEOUT ?= 10m
TEST_FLAGS ?= -v -race -timeout $(TEST_TIMEOUT) -failfast -count=1

.PHONY: help
help: ## Display this help message
	@echo "XTrinode Operator Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  %-25s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Variables:"
	@echo "  REGISTRY             Docker registry/repository prefix (default: ghcr.io/xtrinode)"
	@echo "  OPERATOR_IMAGE_NAME  Operator image name (default: xtrinode-operator)"
	@echo "  IMG                  Operator image tag (default: \$${REGISTRY}/\$${OPERATOR_IMAGE_NAME}:\$${IMAGE_TAG})"
	@echo "  GIT_TAG              Exact git tag at HEAD, when present"
	@echo "  VERSION              Version tag (default: exact git tag without leading v, or 'dev')"
	@echo "  IMAGE_VERSION        Component image version from Helm appVersion (default: umbrella appVersion)"
	@echo "  IMAGE_TAG            Docker image tag (default: appVersion on exact release tags, otherwise \$${VERSION})"
	@echo "  CLOUD_IMAGE_TAG      Cloud publish/deploy tag (default: appVersion when IMAGE_TAG is dev)"
	@echo "  GO_VERSION           Go image version (default: go.mod go directive)"
	@echo "  ALPINE_VERSION       Alpine image version (default: 3.23)"
	@echo "  DOCKER_PLATFORMS     buildx platforms (default: linux/amd64,linux/arm64)"
	@echo "  NODE_VERSION         Required Node.js major version for markdown tooling (default: 22)"
	@echo "  OPERATOR_NAMESPACE   Operator namespace (default: xtrinode-system)"
	@echo "  GATEWAY_NAMESPACE    Gateway namespace (default: xtrinode-gateway)"
	@echo "  API_SERVER_NAMESPACE API Server namespace (default: xtrinode-system)"
	@echo "  COVERAGE_THRESHOLD   Minimum test coverage (default: 65)"
	@echo "  TF_ENV               Terraform environment (default: dev)"
	@echo "  TF_VAR_FILE          Terraform variables file (default: terraform.tfvars)"
	@echo "  TERRAFORM_CLOUDS     Terraform clouds used by generic/CI checks (default: gcp)"
	@echo ""
	@echo "Deployment: make deploy-gcp | make deploy-aws | make deploy-azure | make deploy"
	@echo "Docker release: git tag v0.1.2 && make docker-release"
	@echo "Release GCP: make release-operator-gcp VERSION=0.1.2  (build, push, rollout)"
	@echo "See docs/DEPLOYMENT.md for full flow."

# =============================================================================
# Development Commands
# =============================================================================

.PHONY: build-operator
build-operator: manifests ## Build the operator binary
	@echo "Building operator binary..."
	@mkdir -p "$(BIN_DIR)"
	cd $(OPERATOR_DIR) && CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)" \
		-o "$(CURDIR)/$(OPERATOR_BIN)" ./cmd/operator
	@echo "Binary built: $(OPERATOR_BIN)"

.PHONY: build-gateway
build-gateway: ## Build the gateway binary
	@echo "Building gateway binary..."
	@mkdir -p "$(BIN_DIR)"
	cd $(OPERATOR_DIR) && CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)" \
		-o "$(CURDIR)/$(GATEWAY_BIN)" ./cmd/gateway
	@echo "Binary built: $(GATEWAY_BIN)"

.PHONY: build-api-server
build-api-server: ## Build the API server binary
	@echo "Building API server binary..."
	@mkdir -p "$(BIN_DIR)"
	cd $(OPERATOR_DIR) && CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)" \
		-o "$(CURDIR)/$(API_SERVER_BIN)" ./cmd/api-server
	@echo "Binary built: $(API_SERVER_BIN)"

.PHONY: build-all
build-all: build-operator build-gateway build-api-server ## Build all binaries

.PHONY: run-gateway
run-gateway: ## Run the gateway locally (requires kubeconfig)
	@echo "Running gateway locally..."
	cd $(OPERATOR_DIR) && go run ./cmd/gateway

.PHONY: run-api-server
run-api-server: ## Run the API server locally (requires kubeconfig)
	@echo "Running API server locally..."
	cd $(OPERATOR_DIR) && go run ./cmd/api-server

.PHONY: run-operator
run-operator: ## Run the operator locally (requires kubeconfig)
	@echo "Running operator locally..."
	cd $(OPERATOR_DIR) && go run ./cmd/operator

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -f "$(OPERATOR_BIN)" "$(GATEWAY_BIN)" "$(API_SERVER_BIN)"
	rm -f $(OPERATOR_DIR)/coverage.out $(OPERATOR_DIR)/coverage.html
	rm -rf $(OPERATOR_DIR)/dist/
	find . -type d -name ".terraform" -exec rm -rf {} + 2>/dev/null || true
	@echo "Terraform state and lock files are left intact."
	@echo "Clean complete"

# =============================================================================
# Code Quality Commands
# =============================================================================

.PHONY: gofmt
gofmt: ## Check Go code formatting without modifying
	@echo "Checking Go code formatting..."
	@cd $(OPERATOR_DIR) && gofmt -d -s . | tee /tmp/gofmt.diff || true
	@if [ -s /tmp/gofmt.diff ]; then \
		echo "ERROR: Code is not formatted. Run 'make gofmt-fix' to fix."; \
		cat /tmp/gofmt.diff; \
		rm -f /tmp/gofmt.diff; \
		exit 1; \
	fi
	@rm -f /tmp/gofmt.diff
	@echo "Go formatting check passed"

.PHONY: gofmt-fix
gofmt-fix: ## Fix Go code formatting
	@echo "Fixing Go code formatting..."
	cd $(OPERATOR_DIR) && gofmt -s -w .
	@echo "Go formatting fixed"

.PHONY: govet
govet: ## Run go vet
	@echo "Running go vet..."
	cd $(OPERATOR_DIR) && go vet ./...
	@echo "Vet complete"

.PHONY: ensure-golangci-lint
ensure-golangci-lint:
	@expected="$(GOLANGCI_LINT_VERSION)"; expected="$${expected#v}"; \
	actual=""; \
	if [ -x "$(GOLANGCI_LINT_BIN)" ]; then \
		actual="$$($(GOLANGCI_LINT_BIN) version 2>/dev/null | awk '/version/ {print $$4; exit}')"; \
	fi; \
	if [ "$$actual" != "$$expected" ]; then \
		echo "Installing golangci-lint..."; \
		curl --proto '=https' --tlsv1.2 -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh | sh -s -- -b $$(go env GOPATH)/bin $(GOLANGCI_LINT_VERSION); \
	fi

.PHONY: ensure-controller-gen
ensure-controller-gen:
	@actual=""; \
	if [ -x "$$(go env GOPATH)/bin/controller-gen" ]; then \
		actual="$$($$(go env GOPATH)/bin/controller-gen --version 2>/dev/null | awk '{print $$2; exit}')"; \
	fi; \
	if [ "$$actual" != "$(CONTROLLER_GEN_VERSION)" ]; then \
		echo "Installing controller-gen..."; \
		cd $(OPERATOR_DIR) && go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION); \
	fi

.PHONY: ensure-trivy
ensure-trivy:
	@if ! command -v trivy >/dev/null 2>&1; then \
		echo "Error: trivy not found. Install from https://trivy.dev/latest/getting-started/installation/"; \
		exit 1; \
	fi

.PHONY: lint
lint: lint-go lint-helm lint-markdown lint-terraform ## Run all linters

.PHONY: lint-go
lint-go: ensure-golangci-lint ## Run Go linter (golangci-lint)
	@echo "Running golangci-lint..."
	cd $(OPERATOR_DIR) && $(GOLANGCI_LINT_BIN) run --timeout $(LINT_TIMEOUT) ./...
	@echo "Go linting complete"

.PHONY: lint-go-fix
lint-go-fix: ensure-golangci-lint ## Run golangci-lint with auto-fix
	@echo "Running golangci-lint with auto-fix..."
	cd $(OPERATOR_DIR) && $(GOLANGCI_LINT_BIN) run --timeout $(LINT_TIMEOUT) --fix ./...
	@echo "Go linting fixes applied"

.PHONY: lint-go-all
lint-go-all: gofmt govet lint-go ## Run all Go quality checks (format, vet, lint)
	@echo "All linting checks complete"

.PHONY: lint-helm
lint-helm: helm-deps ## Run Helm chart linter for configured charts
	@echo "Running Helm lint for configured charts..."
	@if ! command -v helm >/dev/null 2>&1; then \
		echo "Error: helm not found. Install from https://helm.sh/docs/intro/install/"; \
		exit 1; \
	fi
	@status=0; \
	for chart in $(HELM_LINT_CHARTS); do \
		name="$${chart%%=*}"; \
		path="$${chart#*=}"; \
		echo "Running Helm lint for $$name..."; \
		helm lint "$$path" || status=$$?; \
	done; \
	exit $$status
	@echo "Testing Helm production guardrails..."
	@scripts/test/helm-prod-guardrails.sh
	@echo "Helm production guardrail tests complete"
	@echo "Helm linting complete"

.PHONY: test-helm-prod-guardrails
test-helm-prod-guardrails: helm-deps ## Verify production Helm guardrails render or fail as intended
	@echo "Testing Helm production guardrails..."
	@scripts/test/helm-prod-guardrails.sh
	@echo "Helm production guardrail tests complete"

.PHONY: lint-markdown
lint-markdown: ## Run Markdown linter
	@echo "Running Markdown lint..."
	@if command -v markdownlint >/dev/null 2>&1 && command -v node >/dev/null 2>&1 && [ "$$(node -p 'Number(process.versions.node.split(".")[0])')" -eq "$(NODE_VERSION)" ]; then \
		markdownlint '**/*.md' --ignore node_modules --ignore docs/internal; \
	elif command -v npx >/dev/null 2>&1 && command -v node >/dev/null 2>&1 && [ "$$(node -p 'Number(process.versions.node.split(".")[0])')" -eq "$(NODE_VERSION)" ]; then \
		npx --yes "markdownlint-cli@$(MARKDOWNLINT_CLI_VERSION)" '**/*.md' --ignore node_modules --ignore docs/internal; \
	elif command -v docker >/dev/null 2>&1; then \
		echo "Using Dockerized Node 22 markdownlint because local Node is $$(node --version 2>/dev/null || echo unavailable)"; \
		docker run --rm -v "$(CURDIR)":/work:ro -w /work $(MARKDOWNLINT_DOCKER_IMAGE) sh -c "npm install -g markdownlint-cli@$(MARKDOWNLINT_CLI_VERSION) >/tmp/markdownlint-install.log && markdownlint '**/*.md' --ignore node_modules --ignore docs/internal"; \
	else \
		echo "ERROR: markdownlint requires Node.js $(NODE_VERSION) or Docker fallback."; \
		exit 1; \
	fi
	@echo "Markdown linting complete"

.PHONY: lint-terraform
lint-terraform: ## Run Terraform linter for configured clouds (tflint)
	@echo "Running Terraform lint for configured clouds: $(TERRAFORM_CLOUDS)"
	@if ! command -v tflint >/dev/null 2>&1; then \
		echo "Skipping tflint (not installed). Install from https://github.com/terraform-linters/tflint"; \
	else \
		status=0; \
		for cloud in $(TERRAFORM_CLOUDS); do \
			dir="$(TF_DIR)/$$cloud"; \
			echo "Running tflint in $$dir..."; \
			(cd "$$dir" && tflint --init && tflint) || status=$$?; \
		done; \
		exit $$status; \
	fi
	@echo "Terraform linting complete"

.PHONY: lint-terraform-format
lint-terraform-format: ## Check Terraform formatting for configured clouds
	@echo "Checking Terraform formatting for configured clouds: $(TERRAFORM_CLOUDS)"
	@status=0; \
	for cloud in $(TERRAFORM_CLOUDS); do \
		dir="$(TF_DIR)/$$cloud"; \
		echo "Checking Terraform formatting in $$dir..."; \
		(cd "$$dir" && terraform fmt -check -recursive) || status=$$?; \
	done; \
	if [ "$$status" -ne 0 ]; then \
		echo "Run 'make terraform-fmt' to fix formatting"; \
		exit "$$status"; \
	fi
	@echo "Terraform formatting OK"

# =============================================================================
# Helm Repository Setup
# =============================================================================

.PHONY: helm-repo-setup
helm-repo-setup: ## Set up Helm repositories (Trino, KEDA, Prometheus)
	@echo "Setting up Helm repositories..."
	@helm repo add trino https://trinodb.github.io/charts 2>/dev/null || true
	@helm repo add kedacore https://kedacore.github.io/charts 2>/dev/null || true
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
	@helm repo update trino kedacore prometheus-community 2>/dev/null || true
	@echo "Helm repository setup complete"

.PHONY: helm-deps
helm-deps: helm-repo-setup ## Build Helm chart dependencies from checked-in lock files
	@echo "Building Helm dependencies..."
	@helm dependency build $(HELM_CHART_PATH)
	@helm dependency build $(UMBRELLA_HELM_CHART_PATH)
	@helm dependency build $(OBSERVABILITY_HELM_CHART_PATH)
	@echo "Helm dependencies built"

.PHONY: helm-deps-update
helm-deps-update: helm-repo-setup ## Update Helm chart dependencies and lock files
	@echo "Updating Helm dependencies..."
	@helm dependency update $(HELM_CHART_PATH)
	@helm dependency update $(UMBRELLA_HELM_CHART_PATH)
	@helm dependency update $(OBSERVABILITY_HELM_CHART_PATH)
	@echo "Helm dependencies updated"

.PHONY: helm-template
helm-template: helm-deps ## Render Helm charts to validate dependencies and templates
	@echo "Rendering Helm charts..."
	@status=0; \
	for chart in $(HELM_TEMPLATE_CHARTS); do \
		name="$${chart%%=*}"; \
		path="$${chart#*=}"; \
		echo "Rendering Helm chart $$name..."; \
		helm template "$$name" "$$path" >/dev/null || status=$$?; \
	done; \
	exit $$status
	@echo "Helm rendering complete"

# =============================================================================
# KEDA Commands
# =============================================================================

KEDA_NAMESPACE ?= keda
OBSERVABILITY_NAMESPACE ?= monitoring
OBSERVABILITY_RELEASE ?= xtrinode-observability
PROMETHEUS_ENABLED ?= true
PROMETHEUS_STORAGE_CLASS ?= standard
GRAFANA_ENABLED ?= false
VECTOR_ENABLED ?= true
VECTOR_NAMESPACE ?= observability
VECTOR_LOG_LEVEL ?= info
WEBHOOK_ENABLED ?= true

.PHONY: install-keda
install-keda: helm-repo-setup ## Install KEDA (required for XTrinode worker autoscaling)
	@echo "Installing KEDA..."
	@helm upgrade --install keda kedacore/keda -n $(KEDA_NAMESPACE) --create-namespace
	@echo "KEDA installed in namespace $(KEDA_NAMESPACE)"

.PHONY: uninstall-keda
uninstall-keda: ## Uninstall KEDA
	@echo "Uninstalling KEDA..."
	@helm uninstall keda -n $(KEDA_NAMESPACE) 2>/dev/null || true
	@echo "KEDA uninstalled"

.PHONY: install-observability
install-observability: helm-repo-setup ## Install XTrinode observability chart (Prometheus + Vector)
	@echo "Installing XTrinode observability stack..."
	helm dependency update helm/xtrinode-observability
	helm upgrade --install $(OBSERVABILITY_RELEASE) helm/xtrinode-observability \
		-n $(OBSERVABILITY_NAMESPACE) --create-namespace \
		--set prometheus-stack.enabled=$(PROMETHEUS_ENABLED) \
		--set prometheus-stack.defaultRules.create=false \
		--set prometheus-stack.prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.storageClassName=$(PROMETHEUS_STORAGE_CLASS) \
		--set prometheus-stack.alertmanager.enabled=false \
		--set prometheus-stack.nodeExporter.enabled=false \
		--set prometheus-stack.kubeStateMetrics.enabled=false \
		--set prometheus-stack.kubeApiServer.enabled=false \
		--set prometheus-stack.kubelet.enabled=false \
		--set prometheus-stack.kubeControllerManager.enabled=false \
		--set prometheus-stack.coreDns.enabled=false \
		--set prometheus-stack.kubeDns.enabled=false \
		--set prometheus-stack.kubeEtcd.enabled=false \
		--set prometheus-stack.kubeScheduler.enabled=false \
		--set prometheus-stack.kubeProxy.enabled=false \
		--set prometheus-stack.grafana.enabled=$(GRAFANA_ENABLED) \
		--set vector.enabled=$(VECTOR_ENABLED) \
		--set vector.namespaceOverride=$(VECTOR_NAMESPACE) \
		--set vector.clusterName=$(GCP_CLUSTER_NAME) \
		--set vector.environment=$(TF_ENV) \
		--set vector.region=$(GCP_REGION) \
		--set vector.logLevel=$(VECTOR_LOG_LEVEL) \
		--set vector.serviceMonitor.enabled=$(PROMETHEUS_ENABLED) \
		--wait --timeout=10m
	@echo "Observability stack installed in namespace $(OBSERVABILITY_NAMESPACE)"

.PHONY: uninstall-observability
uninstall-observability: ## Uninstall XTrinode observability chart
	@echo "Uninstalling XTrinode observability stack..."
	@helm uninstall $(OBSERVABILITY_RELEASE) -n $(OBSERVABILITY_NAMESPACE) 2>/dev/null || true
	@echo "Observability stack uninstalled"

# =============================================================================
# Local k3d + Tilt Development
# =============================================================================

.PHONY: ensure-k3d
ensure-k3d: ## Check that k3d is installed or available at K3D=/path/to/k3d
	@if command -v "$(K3D)" >/dev/null 2>&1; then \
		"$(K3D)" version; \
	elif [ -x "$(K3D)" ]; then \
		"$(K3D)" version; \
	else \
		echo "ERROR: k3d not found."; \
		echo "Install k3d with your package manager, or set K3D=/path/to/k3d."; \
		echo "Example: curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash"; \
		exit 1; \
	fi

.PHONY: ensure-tilt
ensure-tilt: ## Check that Tilt is installed or available at TILT=/path/to/tilt
	@if command -v "$(TILT)" >/dev/null 2>&1; then \
		"$(TILT)" version; \
	elif [ -x "$(TILT)" ]; then \
		"$(TILT)" version; \
	else \
		echo "ERROR: Tilt not found."; \
		echo "Install Tilt from https://docs.tilt.dev/install.html, or set TILT=/path/to/tilt."; \
		exit 1; \
	fi

.PHONY: k3d-up
k3d-up: ensure-k3d ## Create the local k3d cluster and registry for XTrinode development
	K3D="$(K3D)" \
	K3D_CLUSTER_NAME="$(K3D_CLUSTER_NAME)" \
	K3D_REGISTRY_NAME="$(K3D_REGISTRY_NAME)" \
	K3D_REGISTRY_PORT="$(K3D_REGISTRY_PORT)" \
	K3D_AGENTS="$(K3D_AGENTS)" \
	K3D_K3S_IMAGE="$(K3D_K3S_IMAGE)" \
	tilt/scripts/k3d-up.sh

.PHONY: k3d-down
k3d-down: ## Delete the local k3d cluster and registry
	K3D="$(K3D)" \
	K3D_CLUSTER_NAME="$(K3D_CLUSTER_NAME)" \
	K3D_REGISTRY_NAME="$(K3D_REGISTRY_NAME)" \
	tilt/scripts/k3d-down.sh

.PHONY: local-images
local-images: ## Build and push local dev images to the k3d registry
	LOCAL_IMAGE_TAG="$(LOCAL_IMAGE_TAG)" \
	LOCAL_COMPONENTS="$(LOCAL_COMPONENTS)" \
	K3D_REGISTRY_PORT="$(K3D_REGISTRY_PORT)" \
	LOCAL_REGISTRY_HOST="$(LOCAL_REGISTRY_HOST)" \
	GO_VERSION="$(GO_VERSION)" \
	ALPINE_VERSION="$(ALPINE_VERSION)" \
	tilt/scripts/build-push-local-images.sh

.PHONY: preload-local-images
preload-local-images: k3d-up ## Pull and import third-party local e2e images into k3d nodes
	K3D="$(K3D)" \
	K3D_CLUSTER_NAME="$(K3D_CLUSTER_NAME)" \
	TRINO_IMAGE_REPOSITORY="$(TRINO_IMAGE_REPOSITORY)" \
	TRINO_IMAGE_TAG="$(TRINO_IMAGE_TAG)" \
	LOCAL_PRELOAD_IMAGES="$(LOCAL_PRELOAD_IMAGES)" \
	LOCAL_PRELOAD_IMAGES_ENABLED="$(LOCAL_PRELOAD_IMAGES_ENABLED)" \
	LOCAL_PRELOAD_SKIP_PULL="$(LOCAL_PRELOAD_SKIP_PULL)" \
	tilt/scripts/preload-local-images.sh

.PHONY: deploy-local-k3d
deploy-local-k3d: ## Deploy operator, API server, gateway, Redis, and KEDA into local k3d
	LOCAL_IMAGE_TAG="$(LOCAL_IMAGE_TAG)" \
	K3D_REGISTRY_NAME="$(K3D_REGISTRY_NAME)" \
	LOCAL_REGISTRY_CLUSTER="$(LOCAL_REGISTRY_CLUSTER)" \
	OPERATOR_NAMESPACE="$(OPERATOR_NAMESPACE)" \
	API_SERVER_NAMESPACE="$(API_SERVER_NAMESPACE)" \
	GATEWAY_NAMESPACE="$(GATEWAY_NAMESPACE)" \
	tilt/scripts/deploy-local-stack.sh

.PHONY: local-stack-up
local-stack-up: ## Create k3d, preload dependencies, build local images, and deploy the local stack
	$(MAKE) preload-local-images
	$(MAKE) local-images
	$(MAKE) deploy-local-k3d

.PHONY: local-stack-down
local-stack-down: k3d-down ## Delete the local k3d cluster and registry

.PHONY: dev-up
dev-up: local-stack-up ## Alias for local-stack-up

.PHONY: dev-down
dev-down: local-stack-down ## Alias for local-stack-down

.PHONY: tilt-up
tilt-up: ensure-tilt preload-local-images ## Start Tilt against the local k3d cluster
	LOCAL_PRELOAD_IMAGES_ENABLED=false "$(TILT)" up -f "$(TILTFILE)" $(TILT_ARGS)

.PHONY: tilt-ci
tilt-ci: ensure-tilt preload-local-images ## Run Tilt headlessly until all auto-init resources are healthy
	LOCAL_PRELOAD_IMAGES_ENABLED=false "$(TILT)" ci -f "$(TILTFILE)" $(TILT_ARGS)

.PHONY: tilt-down
tilt-down: ensure-tilt ## Delete Tilt-managed resources from the current k3d context
	"$(TILT)" down -f "$(TILTFILE)"

.PHONY: tilt-clean
tilt-clean: tilt-down k3d-down ## Delete Tilt-managed resources, then delete the local k3d cluster

# =============================================================================
# Testing Commands
# =============================================================================

.PHONY: test
test: ## Run unit tests (strict mode: failfast, timeout, no retries)
	@echo "Running unit tests with strict policy..."
	cd $(OPERATOR_DIR) && go test $(TEST_FLAGS) -coverprofile=coverage.out -covermode=atomic \
		./api/v1 ./controllers ./pkg/api-server ./pkg/gateway ./pkg/gateway/auth
	@echo "Checking test coverage threshold..."
	@cd $(OPERATOR_DIR) && coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
		echo "Current coverage: $$coverage%"; \
		if [ -n "$$coverage" ] && [ -n "$(COVERAGE_THRESHOLD)" ]; then \
			if ! awk -v coverage="$$coverage" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { exit (coverage + 0 >= threshold + 0) ? 0 : 1 }'; then \
				echo "ERROR: Coverage $$coverage% is below threshold $(COVERAGE_THRESHOLD)%"; \
				exit 1; \
			fi; \
		fi
	@echo "Tests complete - all passed with coverage >= $(COVERAGE_THRESHOLD)%"

.PHONY: test-coverage
test-coverage: ## Run tests and generate coverage report (strict mode)
	@echo "Running tests with coverage (strict mode)..."
	cd $(OPERATOR_DIR) && go test $(TEST_FLAGS) -coverprofile=coverage.out -covermode=atomic ./...
	@echo ""
	@echo "=== Coverage Summary ==="
	cd $(OPERATOR_DIR) && go tool cover -func=coverage.out | grep -E "(^github.com|^total)" | \
		awk '{printf "%-60s %6s\n", $$1, $$3}'
	@echo ""
	@cd $(OPERATOR_DIR) && coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total Coverage: $$coverage%"; \
	if [ -n "$$coverage" ] && [ -n "$(COVERAGE_THRESHOLD)" ]; then \
		if ! awk -v coverage="$$coverage" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { exit (coverage + 0 >= threshold + 0) ? 0 : 1 }'; then \
			echo "ERROR: Coverage $$coverage% is below threshold $(COVERAGE_THRESHOLD)%"; \
			exit 1; \
		fi; \
	fi
	@echo ""
	@echo "Generating HTML coverage report..."
	cd $(OPERATOR_DIR) && go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: $(OPERATOR_DIR)/coverage.html"

.PHONY: test-coverage-html
test-coverage-html: test-coverage ## Open coverage report in browser
	@echo "Opening coverage report..."
	@if command -v xdg-open >/dev/null 2>&1; then \
		xdg-open $(OPERATOR_DIR)/coverage.html; \
	elif command -v open >/dev/null 2>&1; then \
		open $(OPERATOR_DIR)/coverage.html; \
	else \
		echo "Coverage report: $(OPERATOR_DIR)/coverage.html"; \
	fi

.PHONY: test-coverage-package
test-coverage-package: ## Show coverage per package (usage: make test-coverage-package PKG=./pkg/sizing)
	@if [ -z "$(PKG)" ]; then \
		echo "Usage: make test-coverage-package PKG=./pkg/sizing"; \
		exit 1; \
	fi
	@echo "Running coverage for $(PKG) (strict mode)..."
	cd $(OPERATOR_DIR) && go test $(TEST_FLAGS) -coverprofile=coverage-$(shell basename $(PKG)).out -covermode=atomic $(PKG)
	cd $(OPERATOR_DIR) && go tool cover -func=coverage-$(shell basename $(PKG)).out | tail -1

.PHONY: test-coverage-summary
test-coverage-summary: ## Show coverage summary for all packages
	@echo "=== Coverage Summary by Package ==="
	@echo ""
	cd $(OPERATOR_DIR) && go test -race -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out -covermode=atomic ./... 2>&1 | \
		grep -E "(ok|FAIL).*coverage:" | \
		sed 's/ok[[:space:]]*/ok /' | \
		awk '{ \
			pkg = ""; \
			coverage = ""; \
			for (i=2; i<=NF; i++) { \
				if ($$i ~ /^coverage:/) { \
					coverage = $$(i+1) " " $$(i+2) " " $$(i+3); \
					break; \
				} else { \
					if (pkg == "") pkg = $$i; else pkg = pkg " " $$i; \
				} \
			} \
			if (coverage != "") printf "%-60s %s\n", pkg, coverage; \
		}'
	@echo ""
	@echo "=== Overall Coverage ==="
	cd $(OPERATOR_DIR) && go tool cover -func=coverage.out 2>/dev/null | grep total || \
		(go test -race -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out -covermode=atomic ./... >/dev/null 2>&1 && \
		 go tool cover -func=coverage.out | grep total)

.PHONY: test-unit
test-unit: ## Run unit tests only (strict mode)
	@echo "Running unit tests (strict mode)..."
	cd $(OPERATOR_DIR) && go test $(TEST_FLAGS) ./pkg/... ./controllers/... ./api/...

.PHONY: ensure-setup-envtest
ensure-setup-envtest: ## Install setup-envtest if needed
	@if [ ! -x "$(SETUP_ENVTEST_BIN)" ]; then \
		echo "Installing setup-envtest $(SETUP_ENVTEST_VERSION)..."; \
		go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION); \
	fi

.PHONY: test-integration
test-integration: ensure-setup-envtest ## Run integration tests (strict mode)
	@echo "Running integration tests (strict mode)..."
	cd $(OPERATOR_DIR) && KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST_BIN) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test $(TEST_FLAGS) -tags=integration ./tests/integration/...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (requires cluster, strict mode)
	@echo "Running e2e tests (strict mode)..."
	@if ! kubectl cluster-info >/dev/null 2>&1; then \
		echo "Error: No Kubernetes cluster available"; \
		exit 1; \
	fi
	cd $(OPERATOR_DIR) && go test $(TEST_FLAGS) -tags=e2e ./tests/e2e/...

.PHONY: ensure-robot
ensure-robot: ## Check that the uv-managed Robot Framework e2e runner works
	@if [ "$(ROBOT_RUNNER)" = "$(ROBOT_RUNNER_DEFAULT)" ] && ! command -v "$(UV)" >/dev/null 2>&1; then \
		echo "ERROR: uv is required by the default local e2e runner."; \
		echo "Install uv from https://docs.astral.sh/uv/getting-started/installation/."; \
		echo "Or override ROBOT_RUNNER=robot if Robot Framework is already installed another way."; \
		exit 1; \
	fi
	@$(ROBOT_RUNNER) --version || [ "$$?" -eq 251 ]

.PHONY: test-e2e-local
test-e2e-local: ensure-robot dev-up ## Run all local k3d e2e Robot suites
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) tilt/e2e/robot

.PHONY: test-e2e-local-contracts
test-e2e-local-contracts: ensure-robot dev-up ## Run local control-plane/API/gateway contract Robot suites
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include contracts tilt/e2e/robot

.PHONY: test-e2e-local-integration
test-e2e-local-integration: ensure-robot dev-up ## Run local Robot integration suites
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include integration tilt/e2e/robot

.PHONY: test-e2e-local-gateway-auth
test-e2e-local-gateway-auth: ensure-robot dev-up ## Run local gateway API-key authentication Robot suite
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include gateway-auth tilt/e2e/robot

.PHONY: test-e2e-local-smoke
test-e2e-local-smoke: ensure-robot dev-up ## Run local real-Trino lifecycle smoke Robot suite
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include smoke tilt/e2e/robot

.PHONY: test-e2e-local-scaleout
test-e2e-local-scaleout: ensure-robot dev-up ## Run local real-Trino KEDA scale-out Robot suite
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include scaleout tilt/e2e/robot

.PHONY: test-e2e-local-postgres
test-e2e-local-postgres: ensure-robot dev-up ## Run local Postgres catalog integration Robot suite
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) --include postgres tilt/e2e/robot

.PHONY: test-e2e-local-loadtest
test-e2e-local-loadtest: ensure-robot dev-up ## Run local Locust load-test through the Robot wrapper
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) \
		--variable LOADTEST_USERS:$(LOADTEST_USERS) \
		--variable LOADTEST_SPAWN_RATE:$(LOADTEST_SPAWN_RATE) \
		--variable LOADTEST_RUN_TIME:$(LOADTEST_RUN_TIME) \
		--variable LOADTEST_WAIT_MIN:$(LOADTEST_WAIT_MIN) \
		--variable LOADTEST_WAIT_MAX:$(LOADTEST_WAIT_MAX) \
		--variable LOADTEST_MIN_REQUESTS:$(LOADTEST_MIN_REQUESTS) \
		--variable LOADTEST_QUERY:'$(LOADTEST_QUERY)' \
		--variable LOADTEST_AUTOSCALE_USERS:$(LOADTEST_AUTOSCALE_USERS) \
		--variable LOADTEST_AUTOSCALE_SPAWN_RATE:$(LOADTEST_AUTOSCALE_SPAWN_RATE) \
		--variable LOADTEST_AUTOSCALE_RUN_TIME:$(LOADTEST_AUTOSCALE_RUN_TIME) \
		--variable LOADTEST_AUTOSCALE_WAIT_SECONDS:$(LOADTEST_AUTOSCALE_WAIT_SECONDS) \
		--variable LOADTEST_AUTOSCALE_MAX_WORKERS:$(LOADTEST_AUTOSCALE_MAX_WORKERS) \
		--variable LOADTEST_AUTOSCALE_THRESHOLD:$(LOADTEST_AUTOSCALE_THRESHOLD) \
		--variable LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS:$(LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS) \
		--variable LOADTEST_AUTOSCALE_QUERY:'$(LOADTEST_AUTOSCALE_QUERY)' \
		tilt/loadtest/robot

.PHONY: test-e2e-local-operator-stress
test-e2e-local-operator-stress: ensure-robot dev-up ## Run local k3d operator reconcile stress suite
	$(ROBOT_RUNNER) $(LOCAL_E2E_ROBOT_ARGS) \
		--variable OPERATOR_STRESS_NAMESPACE:$(OPERATOR_STRESS_NAMESPACE) \
		--variable OPERATOR_STRESS_COUNT:$(OPERATOR_STRESS_COUNT) \
		--variable OPERATOR_STRESS_PATCH_ROUNDS:$(OPERATOR_STRESS_PATCH_ROUNDS) \
		--variable OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS:$(OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS) \
		--variable OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA:$(OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA) \
		--variable OPERATOR_STRESS_METRICS_PORT:$(OPERATOR_STRESS_METRICS_PORT) \
		tilt/operator-stress/robot

.PHONY: test-e2e-local-robot
test-e2e-local-robot: test-e2e-local ## Alias for the canonical local Robot e2e target

# =============================================================================
# Code Generation Commands
# =============================================================================

.PHONY: generate
generate: manifests ## Generate code (CRDs, RBAC, etc.)
	@echo "Generating code..."
	cd $(OPERATOR_DIR) && PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...
	@echo "Code generation complete"

.PHONY: manifests
manifests: ## Generate CRD manifests (outputs directly to Helm chart crds/)
	@echo "Generating CRD manifests..."
	@$(MAKE) ensure-controller-gen
	@mkdir -p helm/xtrinode-operator/crds
	cd $(OPERATOR_DIR) && $$(go env GOPATH)/bin/controller-gen rbac:roleName=xtrinode-operator-role crd:allowDangerousTypes=true object paths="./api/v1/..." output:crd:artifacts:config=../helm/xtrinode-operator/crds
	@echo "Manifests generated"

.PHONY: verify-manifests
verify-manifests: manifests ## Verify manifests are up to date
	@echo "Verifying manifests..."
	@if git diff --exit-code helm/xtrinode-operator/crds; then \
		echo "Manifests are up to date"; \
	else \
		echo "ERROR: Manifests are out of date. Run 'make manifests' and commit changes."; \
		exit 1; \
	fi

# =============================================================================
# Docker Commands
# =============================================================================

define docker_build
	@echo "Building $(1) Docker image: $($(2))"
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		--build-arg APP_PACKAGE=$($(3)_PACKAGE) \
		--build-arg APP_PORT=$($(3)_PORT) \
		--build-arg VERSION=$(IMAGE_TAG) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $($(2)) \
		-f $($(3)_DOCKERFILE) \
		$(OPERATOR_DIR)
	@echo "$(1) image built: $($(2))"
endef

define docker_push
	@echo "Pushing $(1) Docker image: $($(2))"
	docker push $($(2))
	@echo "$(1) image pushed"
endef

define docker_buildx
	@echo "Building and pushing multi-arch $(1) Docker image: $($(2))"
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		--build-arg APP_PACKAGE=$($(3)_PACKAGE) \
		--build-arg APP_PORT=$($(3)_PORT) \
		--build-arg VERSION=$(IMAGE_TAG) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $($(2)) \
		--push \
		-f $($(3)_DOCKERFILE) \
		$(OPERATOR_DIR)
	@echo "Multi-arch $(1) image built and pushed"
endef

.PHONY: docker-build
docker-build: docker-build-operator docker-build-gateway docker-build-api-server ## Build all Docker images

.PHONY: docker-build-operator
docker-build-operator: ## Build operator Docker image
	$(call docker_build,Operator,IMG,OPERATOR)

.PHONY: docker-build-gateway
docker-build-gateway: ## Build gateway Docker image
	$(call docker_build,Gateway,GATEWAY_IMG,GATEWAY)

.PHONY: docker-build-api-server
docker-build-api-server: ## Build API server Docker image
	$(call docker_build,API server,API_SERVER_IMG,API_SERVER)

.PHONY: docker-push
docker-push: docker-push-operator docker-push-gateway docker-push-api-server ## Build and push all Docker images

.PHONY: docker-push-operator
docker-push-operator: docker-build-operator ## Build and push operator Docker image
	$(call docker_push,Operator,IMG)

.PHONY: docker-push-gateway
docker-push-gateway: docker-build-gateway ## Build and push gateway Docker image
	$(call docker_push,Gateway,GATEWAY_IMG)

.PHONY: docker-push-api-server
docker-push-api-server: docker-build-api-server ## Build and push API server Docker image
	$(call docker_push,API server,API_SERVER_IMG)

.PHONY: docker-buildx
docker-buildx: docker-buildx-operator docker-buildx-gateway docker-buildx-api-server ## Build and push multi-arch Docker images (requires buildx)

.PHONY: docker-buildx-builder
docker-buildx-builder:
	@docker buildx inspect $(DOCKER_BUILDER) >/dev/null 2>&1 || docker buildx create --use --name $(DOCKER_BUILDER)
	@docker buildx use $(DOCKER_BUILDER)

.PHONY: docker-buildx-operator
docker-buildx-operator: docker-buildx-builder ## Build multi-arch operator Docker image
	$(call docker_buildx,Operator,IMG,OPERATOR)

.PHONY: docker-buildx-gateway
docker-buildx-gateway: docker-buildx-builder ## Build multi-arch gateway Docker image
	$(call docker_buildx,Gateway,GATEWAY_IMG,GATEWAY)

.PHONY: docker-buildx-api-server
docker-buildx-api-server: docker-buildx-builder ## Build multi-arch API server Docker image
	$(call docker_buildx,API server,API_SERVER_IMG,API_SERVER)

.PHONY: require-release-tag
require-release-tag:
	@if [ -z "$(GIT_TAG)" ]; then \
		echo "ERROR: release targets must run from an exact git tag."; \
		echo "Create and check out a release tag, for example: git tag v$$(awk '/^version:/ {print $$2; exit}' $(UMBRELLA_HELM_CHART_PATH)/Chart.yaml | tr -d '\"')"; \
		exit 1; \
	fi
	@tag_version="$(GIT_TAG)"; tag_version="$${tag_version#v}"; \
	if [ "$(VERSION)" != "$$tag_version" ]; then \
		echo "ERROR: VERSION ($(VERSION)) does not match git tag $(GIT_TAG) ($$tag_version)."; \
		exit 1; \
	fi
	@if [ "$(IMAGE_TAG)" != "$(IMAGE_VERSION)" ]; then \
		echo "ERROR: IMAGE_TAG ($(IMAGE_TAG)) must match IMAGE_VERSION ($(IMAGE_VERSION)) for release targets."; \
		exit 1; \
	fi

.PHONY: docker-release
docker-release: require-release-tag ## Build and push all Docker release images from the exact git tag
	$(MAKE) docker-buildx

# =============================================================================
# Kubernetes Deployment Commands
# =============================================================================

.PHONY: create-namespaces
create-namespaces: ## Create required namespaces (idempotent)
	@echo "Creating namespaces for multi-namespace architecture..."
	kubectl create namespace $(OPERATOR_NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
	kubectl create namespace $(GATEWAY_NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
	@echo "Namespaces created: $(OPERATOR_NAMESPACE), $(GATEWAY_NAMESPACE)"

.PHONY: deploy
deploy: manifests create-namespaces deploy-operator deploy-api-server deploy-gateway ## Deploy all components to cluster (generic: ghcr.io/xtrinode, xtrinode-system)

.PHONY: deploy-gcp
deploy-gcp: helm-repo-setup ## Full GCP deploy: manifests + helm (operator, api-server, gateway) to GKE. Prereqs: gcloud auth, gke-gcloud-auth-plugin, images in Artifact Registry.
	bash scripts/deploy-gcp.sh

GCP_REGISTRY ?= $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/xtrinode-operator/xtrinode-operator
GCP_OPERATOR_IMAGE ?= $(GCP_REGISTRY)
GCP_GATEWAY_IMAGE ?= $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/xtrinode-gateway/xtrinode-gateway
GCP_API_SERVER_IMAGE ?= $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/xtrinode-api-server/xtrinode-api-server

.PHONY: gcp-docker-login
gcp-docker-login: ## Configure Docker auth for GCP Artifact Registry
	gcloud auth configure-docker $(GCP_REGION)-docker.pkg.dev --quiet

.PHONY: gcp-images-push
gcp-images-push: gcp-docker-login ## Build and push all XTrinode images to GCP Artifact Registry
	@echo "Building and pushing GCP images with tag $(CLOUD_IMAGE_TAG)..."
	docker build --build-arg GO_VERSION=$(GO_VERSION) --build-arg ALPINE_VERSION=$(ALPINE_VERSION) --build-arg APP_PACKAGE=$(OPERATOR_PACKAGE) --build-arg APP_PORT=$(OPERATOR_PORT) --build-arg VERSION=$(CLOUD_IMAGE_TAG) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(GCP_OPERATOR_IMAGE):$(CLOUD_IMAGE_TAG) -f $(OPERATOR_DOCKERFILE) $(OPERATOR_DIR)
	docker push $(GCP_OPERATOR_IMAGE):$(CLOUD_IMAGE_TAG)
	docker build --build-arg GO_VERSION=$(GO_VERSION) --build-arg ALPINE_VERSION=$(ALPINE_VERSION) --build-arg APP_PACKAGE=$(GATEWAY_PACKAGE) --build-arg APP_PORT=$(GATEWAY_PORT) --build-arg VERSION=$(CLOUD_IMAGE_TAG) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(GCP_GATEWAY_IMAGE):$(CLOUD_IMAGE_TAG) -f $(GATEWAY_DOCKERFILE) $(OPERATOR_DIR)
	docker push $(GCP_GATEWAY_IMAGE):$(CLOUD_IMAGE_TAG)
	docker build --build-arg GO_VERSION=$(GO_VERSION) --build-arg ALPINE_VERSION=$(ALPINE_VERSION) --build-arg APP_PACKAGE=$(API_SERVER_PACKAGE) --build-arg APP_PORT=$(API_SERVER_PORT) --build-arg VERSION=$(CLOUD_IMAGE_TAG) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(GCP_API_SERVER_IMAGE):$(CLOUD_IMAGE_TAG) -f $(API_SERVER_DOCKERFILE) $(OPERATOR_DIR)
	docker push $(GCP_API_SERVER_IMAGE):$(CLOUD_IMAGE_TAG)
	@echo "GCP images pushed."

.PHONY: gcp-operator-image-push
gcp-operator-image-push: gcp-docker-login ## Build and push only the XTrinode operator image to GCP Artifact Registry
	@echo "Building and pushing GCP operator image with tag $(CLOUD_IMAGE_TAG)..."
	docker build --build-arg GO_VERSION=$(GO_VERSION) --build-arg ALPINE_VERSION=$(ALPINE_VERSION) --build-arg APP_PACKAGE=$(OPERATOR_PACKAGE) --build-arg APP_PORT=$(OPERATOR_PORT) --build-arg VERSION=$(CLOUD_IMAGE_TAG) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(GCP_OPERATOR_IMAGE):$(CLOUD_IMAGE_TAG) -f $(OPERATOR_DOCKERFILE) $(OPERATOR_DIR)
	docker push $(GCP_OPERATOR_IMAGE):$(CLOUD_IMAGE_TAG)
	@echo "GCP operator image pushed."

.PHONY: release-operator-gcp
release-operator-gcp: ## Build operator, push to GCP Artifact Registry, helm upgrade, rollout restart. Set CLOUD_IMAGE_TAG for tag (default: appVersion for cloud publishes).
	@echo "=== Release operator to GCP ==="
	@echo "Tag: $(CLOUD_IMAGE_TAG)"
	$(MAKE) docker-build-operator IMAGE_TAG=$(CLOUD_IMAGE_TAG) IMG=$(REGISTRY)/$(OPERATOR_IMAGE_NAME):$(CLOUD_IMAGE_TAG)
	docker tag $(REGISTRY)/$(OPERATOR_IMAGE_NAME):$(CLOUD_IMAGE_TAG) $(GCP_REGISTRY):$(CLOUD_IMAGE_TAG)
	docker push $(GCP_REGISTRY):$(CLOUD_IMAGE_TAG)
	@echo "Configuring kubectl for GKE..."
	gcloud container clusters get-credentials $(GCP_CLUSTER_NAME) --zone $(GCP_ZONE) --project $(GCP_PROJECT_ID)
	@echo "Generating and applying CRDs..."
	$(MAKE) manifests
	kubectl apply -f $(HELM_CHART_PATH)/crds
	@echo "Upgrading Helm release..."
	helm upgrade --install xtrinode-operator $(HELM_CHART_PATH) \
		-n $(GCP_OPERATOR_NAMESPACE) \
		--set image.repository=$(GCP_REGISTRY) \
		--set image.tag=$(CLOUD_IMAGE_TAG) \
		--set image.pullPolicy=Always \
		--reuse-values
	@echo "Restarting operator deployment..."
	kubectl rollout restart deployment xtrinode-operator -n $(GCP_OPERATOR_NAMESPACE)
	kubectl rollout status deployment xtrinode-operator -n $(GCP_OPERATOR_NAMESPACE) --timeout=120s
	@echo "=== Release complete ==="

.PHONY: deploy-aws
deploy-aws: helm-repo-setup ## Experimental AWS provider-validation deploy: terraform + build + push + helm.
	bash scripts/deploy-aws.sh

.PHONY: deploy-azure
deploy-azure: helm-repo-setup ## Experimental Azure provider-validation deploy: terraform + build + push + helm.
	bash scripts/deploy-azure.sh

.PHONY: deploy-operator
deploy-operator: manifests helm-deps ## Deploy operator (includes KEDA subchart when keda.enabled=true)
	@echo "Deploying operator to namespace: $(OPERATOR_NAMESPACE)"
	helm upgrade --install $(RELEASE_NAME) $(HELM_CHART_PATH) \
		-n $(OPERATOR_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(REGISTRY)/$(OPERATOR_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "Operator deployment complete"

.PHONY: deploy-api-server
deploy-api-server: ## Deploy API server to xtrinode-system namespace via Helm
	@echo "Deploying API server to namespace: $(API_SERVER_NAMESPACE)"
	helm upgrade --install xtrinode-api-server $(API_SERVER_HELM_CHART_PATH) \
		-n $(API_SERVER_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(REGISTRY)/$(API_SERVER_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "API server deployment complete"

.PHONY: deploy-gateway
deploy-gateway: ## Deploy gateway to xtrinode-gateway namespace via Helm
	@echo "Deploying gateway to namespace: $(GATEWAY_NAMESPACE)"
	helm upgrade --install xtrinode-gateway $(GATEWAY_HELM_CHART_PATH) \
		-n $(GATEWAY_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(REGISTRY)/$(GATEWAY_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "Gateway deployment complete"

.PHONY: deploy-local
deploy-local: create-namespaces ## Deploy all components with local images (for Kind)
	@echo "Deploying operator with local image..."
	helm upgrade --install $(RELEASE_NAME) $(HELM_CHART_PATH) \
		-n $(OPERATOR_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(OPERATOR_IMAGE_NAME) \
		--set image.tag=dev \
		--set image.pullPolicy=IfNotPresent \
		--wait
	@echo "Deploying API server with local image..."
	helm upgrade --install xtrinode-api-server $(API_SERVER_HELM_CHART_PATH) \
		-n $(API_SERVER_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(API_SERVER_IMAGE_NAME) \
		--set image.tag=dev \
		--set image.pullPolicy=IfNotPresent \
		--wait
	@echo "Deploying gateway with local image..."
	helm upgrade --install xtrinode-gateway $(GATEWAY_HELM_CHART_PATH) \
		-n $(GATEWAY_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(GATEWAY_IMAGE_NAME) \
		--set image.tag=dev \
		--set image.pullPolicy=IfNotPresent \
		--wait
	@echo "Local deployment complete"

.PHONY: undeploy
undeploy: ## Remove all components from cluster
	@echo "Removing operator from namespace: $(OPERATOR_NAMESPACE)"
	helm uninstall $(RELEASE_NAME) -n $(OPERATOR_NAMESPACE) || true
	@echo "Removing API server from namespace: $(API_SERVER_NAMESPACE)"
	helm uninstall xtrinode-api-server -n $(API_SERVER_NAMESPACE) || true
	@echo "Removing gateway from namespace: $(GATEWAY_NAMESPACE)"
	helm uninstall xtrinode-gateway -n $(GATEWAY_NAMESPACE) || true
	@echo "Undeployment complete"

.PHONY: upgrade
upgrade: upgrade-operator upgrade-api-server upgrade-gateway ## Upgrade all components

.PHONY: upgrade-operator
upgrade-operator: ## Upgrade operator deployment
	@echo "Upgrading operator..."
	helm upgrade $(RELEASE_NAME) $(HELM_CHART_PATH) \
		-n $(OPERATOR_NAMESPACE) \
		--set image.repository=$(REGISTRY)/$(OPERATOR_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "Operator upgrade complete"

.PHONY: upgrade-api-server
upgrade-api-server: ## Upgrade API server deployment
	@echo "Upgrading API server..."
	helm upgrade xtrinode-api-server $(API_SERVER_HELM_CHART_PATH) \
		-n $(API_SERVER_NAMESPACE) \
		--set image.repository=$(REGISTRY)/$(API_SERVER_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "API server upgrade complete"

.PHONY: upgrade-gateway
upgrade-gateway: ## Upgrade gateway deployment
	@echo "Upgrading gateway..."
	helm upgrade xtrinode-gateway $(GATEWAY_HELM_CHART_PATH) \
		-n $(GATEWAY_NAMESPACE) \
		--set image.repository=$(REGISTRY)/$(GATEWAY_IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait
	@echo "Gateway upgrade complete"

# =============================================================================
# Terraform Commands
# =============================================================================

.PHONY: terraform-for-configured-clouds
terraform-for-configured-clouds:
	@if [ -z "$(TERRAFORM_ACTION)" ]; then \
		echo "ERROR: TERRAFORM_ACTION is required"; \
		exit 1; \
	fi
	@status=0; \
	for cloud in $(TERRAFORM_CLOUDS); do \
		target="terraform-$(TERRAFORM_ACTION)-$$cloud"; \
		echo "Running $$target..."; \
		$(MAKE) --no-print-directory "$$target" || status=$$?; \
	done; \
	exit $$status

.PHONY: terraform-init
terraform-init: ## Initialize Terraform for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=init

.PHONY: terraform-init-aws
terraform-init-aws: ## Initialize AWS Terraform
	@echo "Initializing AWS Terraform..."
	cd $(AWS_TF_DIR) && terraform init
	@echo "AWS Terraform initialized"

.PHONY: terraform-init-azure
terraform-init-azure: ## Initialize Azure Terraform
	@echo "Initializing Azure Terraform..."
	cd $(AZURE_TF_DIR) && terraform init
	@echo "Azure Terraform initialized"

.PHONY: terraform-init-gcp
terraform-init-gcp: ## Initialize GCP Terraform
	@echo "Initializing GCP Terraform..."
	cd $(GCP_TF_DIR) && terraform init
	@echo "GCP Terraform initialized"

.PHONY: terraform-plan
terraform-plan: ## Plan Terraform deployments for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=plan

.PHONY: terraform-plan-aws
terraform-plan-aws: terraform-init-aws ## Plan AWS Terraform deployment
	@echo "Planning AWS Terraform deployment..."
	cd $(AWS_TF_DIR) && terraform plan -var-file=$(TF_VAR_FILE) -out=tfplan
	@echo "AWS Terraform plan complete"

.PHONY: terraform-plan-azure
terraform-plan-azure: terraform-init-azure ## Plan Azure Terraform deployment
	@echo "Planning Azure Terraform deployment..."
	cd $(AZURE_TF_DIR) && terraform plan -var-file=$(TF_VAR_FILE) -out=tfplan
	@echo "Azure Terraform plan complete"

.PHONY: terraform-plan-gcp
terraform-plan-gcp: terraform-init-gcp ## Plan GCP Terraform deployment
	@echo "Planning GCP Terraform deployment..."
	cd $(GCP_TF_DIR) && terraform plan -var-file=$(TF_VAR_FILE) -out=tfplan
	@echo "GCP Terraform plan complete"

.PHONY: terraform-apply
terraform-apply: ## Apply Terraform deployments for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=apply

.PHONY: terraform-apply-aws
terraform-apply-aws: terraform-plan-aws ## Apply AWS Terraform deployment
	@echo "Applying AWS Terraform deployment..."
	cd $(AWS_TF_DIR) && terraform apply tfplan
	@echo "AWS Terraform deployment complete"

.PHONY: terraform-apply-azure
terraform-apply-azure: terraform-plan-azure ## Apply Azure Terraform deployment
	@echo "Applying Azure Terraform deployment..."
	cd $(AZURE_TF_DIR) && terraform apply tfplan
	@echo "Azure Terraform deployment complete"

.PHONY: terraform-apply-gcp
terraform-apply-gcp: terraform-plan-gcp ## Apply GCP Terraform deployment
	@echo "Applying GCP Terraform deployment..."
	cd $(GCP_TF_DIR) && terraform apply tfplan
	@echo "GCP Terraform deployment complete"

.PHONY: terraform-apply-gcp-cluster
terraform-apply-gcp-cluster: terraform-init-gcp ## Create only the GCP management cluster substrate before Kubernetes provider resources
	@echo "Applying GCP management cluster substrate..."
	cd $(GCP_TF_DIR) && terraform apply -var-file=$(TF_VAR_FILE) \
		-target=google_compute_network.xtrinode \
		-target=google_compute_subnetwork.xtrinode \
		-target=google_compute_firewall.xtrinode_internal \
		-target=google_compute_router.xtrinode \
		-target=google_compute_router_nat.xtrinode \
		-target=google_container_cluster.xtrinode \
		-target=google_container_node_pool.xtrinode \
		-auto-approve
	@echo "GCP management cluster substrate is ready"

.PHONY: terraform-destroy
terraform-destroy: ## Destroy Terraform deployments for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=destroy

.PHONY: terraform-destroy-aws
terraform-destroy-aws: ## Destroy AWS Terraform deployment
	@echo "Destroying AWS Terraform deployment..."
	cd $(AWS_TF_DIR) && terraform destroy -var-file=$(TF_VAR_FILE) -auto-approve
	@echo "AWS Terraform destroyed"

.PHONY: eks-restrict-to-my-ip
eks-restrict-to-my-ip: ## Restrict EKS public endpoint to your current IP (run when IP changes)
	@echo "Fetching your current public IP..."
	@MY_IP=$$(curl -s --max-time 5 https://ifconfig.me 2>/dev/null || curl -s --max-time 5 https://api.ipify.org 2>/dev/null); \
	if [ -z "$$MY_IP" ]; then echo "ERROR: Could not fetch IP"; exit 1; fi; \
	echo "Your IP: $$MY_IP"; \
	echo "Updating EKS public_access_cidrs and applying..."; \
	cd $(AWS_TF_DIR) && terraform apply -var-file=$(TF_VAR_FILE) \
		-var='eks_public_access=true' \
		-var="eks_public_access_cidrs=[\"$$MY_IP/32\"]" \
		-auto-approve
	@echo "EKS access restricted to your IP. Run again when your IP changes."

.PHONY: terraform-destroy-azure
terraform-destroy-azure: ## Destroy Azure Terraform deployment
	@echo "Destroying Azure Terraform deployment..."
	cd $(AZURE_TF_DIR) && terraform destroy -var-file=$(TF_VAR_FILE) -auto-approve
	@echo "Azure Terraform destroyed"

.PHONY: terraform-destroy-gcp
terraform-destroy-gcp: ## Destroy GCP Terraform deployment
	@echo "Destroying GCP Terraform deployment..."
	cd $(GCP_TF_DIR) && terraform destroy -var-file=$(TF_VAR_FILE) -auto-approve
	@echo "GCP Terraform destroyed"

# GCP teardown prep: remove finalizers, patch PDBs, delete Cloud SQL, orphan PG resources, disable deletion protection
.PHONY: gcp-teardown-prep
gcp-teardown-prep: ## Full cleanup before terraform destroy (dev/testing - no blockers)
	@echo "=== GCP teardown prep ==="
	@echo "Configuring kubectl for GKE..."
	gcloud container clusters get-credentials $(GCP_CLUSTER_NAME) --zone $(GCP_ZONE) --project $(GCP_PROJECT_ID) 2>/dev/null || true
	@echo "Patching PDB, uninstalling Helm, cleaning namespaces..."
	kubectl patch pdb xtrinode-gateway -n $(GATEWAY_NAMESPACE) -p '{"spec":{"minAvailable":0}}' 2>/dev/null || true
	helm uninstall xtrinode-gateway -n $(GATEWAY_NAMESPACE) 2>/dev/null || true
	helm uninstall xtrinode-api-server -n $(GCP_OPERATOR_NAMESPACE) 2>/dev/null || true
	helm uninstall xtrinode-operator -n $(GCP_OPERATOR_NAMESPACE) 2>/dev/null || true
	NAMESPACES="$(GCP_OPERATOR_NAMESPACE) team-test $(GATEWAY_NAMESPACE) $(CAPG_WORKLOAD_NAMESPACE) gke-managed-networking-dra-driver monitoring capi-operator-system capi-system capg-system" \
		WAIT_TIMEOUT=$(TEARDOWN_NAMESPACE_WAIT_TIMEOUT) \
		FORCE_NAMESPACE_FINALIZERS=$(FORCE_NAMESPACE_FINALIZERS) \
		bash scripts/k8s/cleanup-finalizers.sh
	@echo "Disabling GKE deletion protection if the cluster still exists..."
	gcloud container clusters update $(GCP_CLUSTER_NAME) --zone $(GCP_ZONE) --project=$(GCP_PROJECT_ID) --no-deletion-protection 2>/dev/null || true
	@echo "Deleting Cloud SQL (if exists) and orphaned private IP range..."
	gcloud services enable sqladmin.googleapis.com --project=$(GCP_PROJECT_ID) 2>/dev/null || true
	gcloud sql instances delete $(GCP_CLUSTER_NAME)-postgres --project=$(GCP_PROJECT_ID) --quiet 2>/dev/null || true
	gcloud compute addresses delete xtrinode-private-ip-range --global --project=$(GCP_PROJECT_ID) --quiet 2>/dev/null || true
	@echo "Removing namespaces and service connection from Terraform state..."
	cd $(GCP_TF_DIR) && terraform state rm kubernetes_namespace.xtrinode_system kubernetes_namespace.test_team 2>/dev/null || true
	cd $(GCP_TF_DIR) && terraform state rm \
		'kubernetes_namespace.capg_operator[0]' \
		'kubernetes_namespace.capi_core[0]' \
		'kubernetes_namespace.capi_bootstrap[0]' \
		'kubernetes_namespace.capi_control_plane[0]' \
		'kubernetes_namespace.capg[0]' 2>/dev/null || true
	cd $(GCP_TF_DIR) && terraform state list 2>/dev/null | awk '/^(kubernetes_|helm_release\.)/ { print }' | xargs -r terraform state rm 2>/dev/null || true
	cd $(GCP_TF_DIR) && (terraform state rm 'google_service_networking_connection.private_vpc_connection[0]' 'google_compute_global_address.private_ip_range[0]' 2>/dev/null || \
		terraform state rm google_service_networking_connection.private_vpc_connection google_compute_global_address.private_ip_range 2>/dev/null) || true
	@echo "=== Teardown prep complete. Run: make terraform-destroy-gcp ==="

# =============================================================================
# GCP from-zero CAPG flow
# =============================================================================

.PHONY: gcp-flow
gcp-flow: ## Print the ordered GCP/CAPG from-zero runbook
	@echo "GCP/CAPG from-zero runbook"
	@echo ""
	@echo "Current scope:"
	@echo "  GCP_PROJECT_ID=$(GCP_PROJECT_ID)"
	@echo "  GCP_REGION=$(GCP_REGION)"
	@echo "  GCP_ZONE=$(GCP_ZONE)"
	@echo "  GCP_CLUSTER_NAME=$(GCP_CLUSTER_NAME)        # Terraform-created GKE management cluster"
	@echo "  CAPG_WORKLOAD_CLUSTER_NAME=$(CAPG_WORKLOAD_CLUSTER_NAME)  # CAPG-created GKE workload cluster"
	@echo "  CAPG_WORKLOAD_NAMESPACE=$(CAPG_WORKLOAD_NAMESPACE)"
	@echo ""
	@echo "Manual sequence:"
	@echo "  1. make gcp-preflight"
	@echo "  2. make gcp-teardown-all CONFIRM_DESTROY=gcp"
	@echo "  3. make gcp-management-up"
	@echo "  4. make gcp-capg-management-up"
	@echo "  5. make gcp-images-push"
	@echo "  6. make gcp-control-plane-deploy"
	@echo "  7. make gcp-capg-workload-up"
	@echo "  8. make gcp-capg-nodepool-smoke"
	@echo "  9. make gcp-capg-workload-nodes"
	@echo ""
	@echo "Optional autoscaling/resume smoke:"
	@echo "  make gcp-observability-up"
	@echo "  make gcp-gateway-redis-up GATEWAY_REPLICA_COUNT=2"
	@echo "  make gcp-keda-resume-smoke"
	@echo ""
	@echo "Note:"
	@echo "  The CAPG nodepool smoke validates CAPI/GCPManagedMachinePool reconciliation from"
	@echo "  the management cluster. By default it keeps the smoke XTrinode suspended, so no"
	@echo "  management-cluster Trino pods are started. Trino pods schedule in the cluster"
	@echo "  where the operator runs when a runtime is resumed."
	@echo "  To run Trino on the CAPG workload cluster, install the XTrinode stack into that workload"
	@echo "  cluster using its generated kubeconfig."
	@echo ""
	@echo "One-shot recreate:"
	@echo "  make gcp-recreate-all CONFIRM_DESTROY=gcp"
	@echo "  make gcp-recreate-all CONFIRM_DESTROY=gcp FORCE_NAMESPACE_FINALIZERS=true"

.PHONY: gcp-preflight
gcp-preflight: ## Check local tools and print active GCP/CAPG scope
	@command -v terraform >/dev/null || (echo "ERROR: terraform not found" && exit 1)
	@command -v gcloud >/dev/null || (echo "ERROR: gcloud not found" && exit 1)
	@command -v kubectl >/dev/null || (echo "ERROR: kubectl not found" && exit 1)
	@command -v helm >/dev/null || (echo "ERROR: helm not found" && exit 1)
	@if [ -z "$(GCP_PROJECT_ID)" ]; then echo "ERROR: GCP_PROJECT_ID is empty; set it or configure $(GCP_TF_DIR)/$(TF_VAR_FILE)"; exit 1; fi
	@echo "GCP/CAPG scope:"
	@echo "  project: $(GCP_PROJECT_ID)"
	@echo "  management cluster: $(GCP_CLUSTER_NAME) ($(GCP_ZONE))"
	@echo "  workload cluster: $(CAPG_WORKLOAD_CLUSTER_NAME) namespace $(CAPG_WORKLOAD_NAMESPACE)"
	@echo "  terraform dir: $(GCP_TF_DIR)"
	@echo "  terraform var file: $(TF_VAR_FILE)"

.PHONY: gcp-refresh-edge-tests
gcp-refresh-edge-tests: ## Static checks for GCP teardown/CAPG smoke edge-case wiring
	bash scripts/test/gcp-refresh-edge-cases.sh

.PHONY: require-gcp-destroy-confirm
require-gcp-destroy-confirm:
	@if [ "$(CONFIRM_DESTROY)" != "gcp" ]; then \
		echo "Refusing destructive GCP teardown. Re-run with CONFIRM_DESTROY=gcp"; \
		exit 1; \
	fi

.PHONY: gcp-configure-kubectl
gcp-configure-kubectl: ## Configure kubectl for the Terraform-created GKE management cluster
	@echo "Configuring kubectl for GCP management cluster..."
	@cmd=$$(terraform -chdir=$(GCP_TF_DIR) output -raw configure_kubectl 2>/dev/null || true); \
	if [ -n "$$cmd" ]; then \
		echo "$$cmd"; \
		eval "$$cmd"; \
	else \
		gcloud container clusters get-credentials $(GCP_CLUSTER_NAME) --zone $(GCP_ZONE) --project $(GCP_PROJECT_ID); \
	fi

.PHONY: gcp-capg-workload-down
gcp-capg-workload-down: ## Delete the CAPG-created GKE workload cluster from the management cluster
	@if $(MAKE) --no-print-directory gcp-configure-kubectl >/dev/null 2>&1; then \
		TARGET_NAMESPACE=$(CAPG_WORKLOAD_NAMESPACE) CLUSTER_NAME=$(CAPG_WORKLOAD_CLUSTER_NAME) WAIT_TIMEOUT=$(CAPG_WAIT_TIMEOUT) bash scripts/capi/delete-cluster.sh; \
	else \
		echo "Management cluster is not reachable; skipping CAPG workload cluster deletion."; \
	fi

.PHONY: gcp-teardown-all
gcp-teardown-all: require-gcp-destroy-confirm ## Delete CAPG workload cluster, prep GCP cleanup, then destroy Terraform GCP management infra
	@echo "=== Tearing down GCP/CAPG test environment ==="
	@$(MAKE) --no-print-directory gcp-capg-workload-down
	@$(MAKE) --no-print-directory gcp-teardown-prep FORCE_NAMESPACE_FINALIZERS=$(TEARDOWN_FORCE_NAMESPACE_FINALIZERS)
	@$(MAKE) --no-print-directory terraform-destroy-gcp
	@echo "=== GCP/CAPG teardown complete ==="

.PHONY: gcp-management-up
gcp-management-up: terraform-apply-gcp-cluster gcp-configure-kubectl terraform-apply-gcp gcp-configure-kubectl ## Create Terraform GCP management cluster, then apply Kubernetes/cloud add-ons
	@echo "GCP management cluster is ready."

.PHONY: gcp-capg-management-up
gcp-capg-management-up: gcp-configure-kubectl ## Install/bootstrap Cluster API Operator and CAPG into the management cluster
	@echo "Bootstrapping CAPG management components..."
	bash scripts/capi/bootstrap.sh -var-file=$(TF_VAR_FILE) -auto-approve
	kubectl wait deployment/capi-operator-cluster-api-operator -n capi-operator-system --for=condition=Available --timeout=5m
	kubectl get pods -n capi-operator-system
	kubectl get pods -n capi-system
	kubectl get pods -n capg-system

.PHONY: gcp-observability-up
gcp-observability-up: gcp-configure-kubectl install-observability ## Install Prometheus and Vector on the management cluster

.PHONY: gcp-control-plane-deploy
gcp-control-plane-deploy: gcp-configure-kubectl ## Deploy XTrinode operator, API server, and gateway to the management cluster
	@echo "Deploying XTrinode control plane to GCP management cluster..."
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) GCP_REGION=$(GCP_REGION) GCP_ZONE=$(GCP_ZONE) GCP_CLUSTER_NAME=$(GCP_CLUSTER_NAME) OPERATOR_NAMESPACE=$(GCP_OPERATOR_NAMESPACE) VERSION=$(VERSION) GATEWAY_REPLICA_COUNT=$(GATEWAY_REPLICA_COUNT) GATEWAY_REDIS_ENABLED=$(GATEWAY_REDIS_ENABLED) PROMETHEUS_ENABLED=$(PROMETHEUS_ENABLED) PROMETHEUS_STORAGE_CLASS=$(PROMETHEUS_STORAGE_CLASS) VECTOR_ENABLED=$(VECTOR_ENABLED) VECTOR_NAMESPACE=$(VECTOR_NAMESPACE) VECTOR_LOG_LEVEL=$(VECTOR_LOG_LEVEL) WEBHOOK_ENABLED=$(WEBHOOK_ENABLED) bash scripts/deploy-gcp.sh

.PHONY: gcp-gateway-redis-up
gcp-gateway-redis-up: ## Redeploy gateway with in-chart Redis enabled
	-kubectl delete deployment/xtrinode-gateway -n $(GATEWAY_NAMESPACE) --ignore-not-found=true --wait=true
	@$(MAKE) --no-print-directory gcp-control-plane-deploy GATEWAY_REDIS_ENABLED=true GATEWAY_REPLICA_COUNT=$(GATEWAY_REPLICA_COUNT)
	kubectl rollout status deployment/xtrinode-gateway -n $(GATEWAY_NAMESPACE) --timeout=5m
	kubectl rollout status deployment/xtrinode-gateway-redis -n $(GATEWAY_NAMESPACE) --timeout=5m

.PHONY: gcp-keda-resume-smoke
gcp-keda-resume-smoke: gcp-configure-kubectl ## Test gateway Prometheus KEDA scale-up/down, auto-suspend, and auto-resume
	NAMESPACE=team-test GATEWAY_NAMESPACE=$(GATEWAY_NAMESPACE) bash scripts/smoke/gcp-keda-resume-smoke.sh

.PHONY: gcp-capg-workload-up
gcp-capg-workload-up: gcp-configure-kubectl ## Create the CAPG-managed GKE workload cluster
	@echo "Creating CAPG workload cluster..."
	TARGET_NAMESPACE=$(CAPG_WORKLOAD_NAMESPACE) CLUSTER_NAME=$(CAPG_WORKLOAD_CLUSTER_NAME) WAIT_TIMEOUT=$(CAPG_WAIT_TIMEOUT) bash scripts/capi/create-cluster.sh

.PHONY: gcp-capg-nodepool-smoke
gcp-capg-nodepool-smoke: gcp-configure-kubectl ## Apply a XTrinode CR that creates a managed CAPG nodepool
	@echo "Running CAPG nodepool smoke..."
	TARGET_NAMESPACE=$(CAPG_WORKLOAD_NAMESPACE) CLUSTER_NAME=$(CAPG_WORKLOAD_CLUSTER_NAME) WAIT_TIMEOUT=$(CAPG_WAIT_TIMEOUT) bash scripts/capi/test-xtrinode-nodepool.sh

.PHONY: gcp-capg-workload-kubeconfig
gcp-capg-workload-kubeconfig: gcp-configure-kubectl ## Write the generated CAPG workload cluster kubeconfig
	kubectl get secret $(CAPG_WORKLOAD_CLUSTER_NAME)-user-kubeconfig -n $(CAPG_WORKLOAD_NAMESPACE) -o jsonpath='{.data.value}' | base64 -d > $(CAPG_WORKLOAD_KUBECONFIG)
	@echo "Wrote $(CAPG_WORKLOAD_KUBECONFIG)"

.PHONY: gcp-capg-workload-nodes
gcp-capg-workload-nodes: gcp-capg-workload-kubeconfig ## Show nodes in the CAPG-created workload cluster
	kubectl --kubeconfig=$(CAPG_WORKLOAD_KUBECONFIG) get nodes -L xtrinode.io/node-pool,xtrinode.io/runtime

.PHONY: gcp-recreate-all
gcp-recreate-all: require-gcp-destroy-confirm ## From zero: teardown, recreate GCP management cluster, bootstrap CAPG, deploy XTrinode, create workload cluster, smoke nodepool
	@$(MAKE) --no-print-directory gcp-teardown-all CONFIRM_DESTROY=$(CONFIRM_DESTROY)
	@$(MAKE) --no-print-directory gcp-management-up
	@$(MAKE) --no-print-directory gcp-capg-management-up
	@$(MAKE) --no-print-directory gcp-images-push
	@$(MAKE) --no-print-directory gcp-control-plane-deploy
	@$(MAKE) --no-print-directory gcp-capg-workload-up
	@$(MAKE) --no-print-directory gcp-capg-nodepool-smoke
	@$(MAKE) --no-print-directory gcp-capg-workload-nodes

.PHONY: terraform-fmt
terraform-fmt: ## Format Terraform files for configured clouds
	@echo "Formatting Terraform files for configured clouds: $(TERRAFORM_CLOUDS)"
	@status=0; \
	for cloud in $(TERRAFORM_CLOUDS); do \
		dir="$(TF_DIR)/$$cloud"; \
		echo "Formatting Terraform in $$dir..."; \
		(cd "$$dir" && terraform fmt -recursive) || status=$$?; \
	done; \
	exit $$status
	@echo "Terraform formatting complete"

.PHONY: terraform-validate
terraform-validate: ## Validate Terraform for configured clouds
	@echo "Validating Terraform for configured clouds: $(TERRAFORM_CLOUDS)"
	@status=0; \
	for cloud in $(TERRAFORM_CLOUDS); do \
		$(MAKE) --no-print-directory terraform-validate-cloud CLOUD=$$cloud || status=$$?; \
	done; \
	exit $$status
	@echo "Terraform validation complete"

.PHONY: terraform-validate-cloud
terraform-validate-cloud: ## Validate Terraform for one cloud (usage: make terraform-validate-cloud CLOUD=gcp)
	@if [ -z "$(CLOUD)" ]; then \
		echo "Usage: make terraform-validate-cloud CLOUD=gcp"; \
		exit 1; \
	fi
	@if [ ! -d "$(TF_DIR)/$(CLOUD)" ]; then \
		echo "ERROR: Terraform directory not found: $(TF_DIR)/$(CLOUD)"; \
		exit 1; \
	fi
	@echo "Checking Terraform formatting for $(CLOUD)..."
	cd $(TF_DIR)/$(CLOUD) && terraform fmt -check -recursive
	@echo "Initializing Terraform for $(CLOUD)..."
	cd $(TF_DIR)/$(CLOUD) && terraform init -backend=false
	@echo "Validating Terraform for $(CLOUD)..."
	cd $(TF_DIR)/$(CLOUD) && terraform validate
	@echo "Terraform validation complete for $(CLOUD)"

.PHONY: terraform-output
terraform-output: ## Show Terraform outputs for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=output

.PHONY: terraform-output-aws
terraform-output-aws: ## Show AWS Terraform outputs
	@echo "AWS Terraform Outputs:"
	cd $(AWS_TF_DIR) && terraform output

.PHONY: terraform-output-azure
terraform-output-azure: ## Show Azure Terraform outputs
	@echo "Azure Terraform Outputs:"
	cd $(AZURE_TF_DIR) && terraform output

.PHONY: terraform-output-gcp
terraform-output-gcp: ## Show GCP Terraform outputs
	@echo "GCP Terraform Outputs:"
	cd $(GCP_TF_DIR) && terraform output

.PHONY: terraform-refresh
terraform-refresh: ## Refresh Terraform state for configured clouds
	@$(MAKE) --no-print-directory terraform-for-configured-clouds TERRAFORM_ACTION=refresh

.PHONY: terraform-refresh-aws
terraform-refresh-aws: ## Refresh AWS Terraform state
	@echo "Refreshing AWS Terraform state..."
	cd $(AWS_TF_DIR) && terraform refresh -var-file=$(TF_VAR_FILE)

.PHONY: terraform-refresh-azure
terraform-refresh-azure: ## Refresh Azure Terraform state
	@echo "Refreshing Azure Terraform state..."
	cd $(AZURE_TF_DIR) && terraform refresh -var-file=$(TF_VAR_FILE)

.PHONY: terraform-refresh-gcp
terraform-refresh-gcp: ## Refresh GCP Terraform state
	@echo "Refreshing GCP Terraform state..."
	cd $(GCP_TF_DIR) && terraform refresh -var-file=$(TF_VAR_FILE)

# =============================================================================
# Dependency Management Commands
# =============================================================================

.PHONY: godeps
godeps: ## Download Go dependencies
	@echo "Downloading dependencies..."
	cd $(OPERATOR_DIR) && go mod download
	cd $(OPERATOR_DIR) && go mod verify
	@echo "Dependencies downloaded"

.PHONY: godeps-update
godeps-update: ## Update Go dependencies
	@echo "Updating dependencies..."
	cd $(OPERATOR_DIR) && go get -u ./...
	cd $(OPERATOR_DIR) && go mod tidy
	@echo "Dependencies updated"

.PHONY: godeps-vendor
godeps-vendor: ## Vendor dependencies
	@echo "Vendoring dependencies..."
	cd $(OPERATOR_DIR) && go mod vendor
	@echo "Dependencies vendored"

# =============================================================================
# Release Commands
# =============================================================================

.PHONY: release
release: require-release-tag ci-lint ci-test ci-verify-manifests ci-terraform-validate-all ci-security docker-build ## Validate release locally from an exact git tag
	@echo "Validating release $(VERSION)..."
	@if [ "$(VERSION)" = "dev" ]; then \
		echo "ERROR: VERSION is 'dev'. Set VERSION to the release version."; \
		exit 1; \
	fi
	@echo "Release $(VERSION) validated from tag $(GIT_TAG). A CODEOWNER must open and merge the release PR."

.PHONY: release-notes
release-notes: ## Generate release notes from git commits
	@echo "Generating release notes..."
	@git log --pretty=format:"- %s (%h)" $(shell git describe --tags --abbrev=0 2>/dev/null || echo "HEAD")..HEAD

# =============================================================================
# CI/CD Helpers
# =============================================================================

.PHONY: check
check: gofmt govet lint-go ## Run all code quality checks

.PHONY: ci-lint
ci-lint: check lint-helm ## Run CI Go and Helm lint checks

.PHONY: ci-test
ci-test: ## Run CI test checks
	@$(MAKE) test-coverage COVERAGE_THRESHOLD=$(CI_COVERAGE_THRESHOLD)

.PHONY: ci-verify-manifests
ci-verify-manifests: verify-manifests ## Run CI manifest verification

.PHONY: ci-terraform-validate
ci-terraform-validate: terraform-validate-cloud ## Run CI Terraform validation (usage: make ci-terraform-validate CLOUD=gcp)

.PHONY: ci-terraform-validate-all
ci-terraform-validate-all: ## Run CI Terraform validation for configured clouds
	@status=0; \
	for cloud in $(TERRAFORM_CLOUDS); do \
		$(MAKE) --no-print-directory ci-terraform-validate CLOUD=$$cloud || status=$$?; \
	done; \
	exit $$status

.PHONY: print-var
print-var: ## Print a Make variable value (usage: make print-var VAR=GO_VERSION)
	@if [ -z "$(VAR)" ]; then \
		echo "ERROR: VAR is required"; \
		exit 1; \
	fi
	@printf '%s\n' "$($(VAR))"

.PHONY: ci-tool-versions-output
ci-tool-versions-output: ## Write CI tool versions to GITHUB_OUTPUT
	@: "$${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
	@printf 'alpine=%s\n' "$(ALPINE_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'go=%s\n' "$(GO_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'helm=%s\n' "$(HELM_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'k3d=%s\n' "$(K3D_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'kubectl=%s\n' "$(KUBECTL_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'terraform=%s\n' "$(TERRAFORM_VERSION)" >> "$$GITHUB_OUTPUT"
	@printf 'uv=%s\n' "$(UV_VERSION)" >> "$$GITHUB_OUTPUT"

.PHONY: ci-image-matrix-output
ci-image-matrix-output: ## Write the Docker image matrix to GITHUB_OUTPUT
	@: "$${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
	@printf 'matrix={"include":[{"component":"operator","image":"%s","package":"%s","port":"%s"},{"component":"gateway","image":"%s","package":"%s","port":"%s"},{"component":"api-server","image":"%s","package":"%s","port":"%s"}]}\n' \
		"$(OPERATOR_IMAGE_NAME)" "$(OPERATOR_PACKAGE)" "$(OPERATOR_PORT)" \
		"$(GATEWAY_IMAGE_NAME)" "$(GATEWAY_PACKAGE)" "$(GATEWAY_PORT)" \
		"$(API_SERVER_IMAGE_NAME)" "$(API_SERVER_PACKAGE)" "$(API_SERVER_PORT)" >> "$$GITHUB_OUTPUT"

.PHONY: ci-build-date-output
ci-build-date-output: ## Write an RFC3339 UTC build timestamp to GITHUB_OUTPUT
	@: "$${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
	@printf 'value=%s\n' "$$(date -u +'%Y-%m-%dT%H:%M:%SZ')" >> "$$GITHUB_OUTPUT"

.PHONY: ci-release-pr-policy
ci-release-pr-policy: ## Validate CODEOWNER release PR policy
	scripts/ci/release-pr-policy.sh

.PHONY: ci-prepare-release
ci-prepare-release: ## Decide whether the current main push should publish a release
	scripts/ci/prepare-release.sh

.PHONY: ci-create-release-tag
ci-create-release-tag: ## Create the release tag after release CI passes
	scripts/ci/create-release-tag.sh

.PHONY: ci-helm-deps-release
ci-helm-deps-release: helm-repo-setup ## Build Helm dependencies for release packaging
	helm dependency build $(HELM_CHART_PATH)
	helm dependency build $(UMBRELLA_HELM_CHART_PATH)

.PHONY: ci-package-helm-release
ci-package-helm-release: ## Package all release Helm charts (usage: RELEASE_VERSION=1.2.3 make ci-package-helm-release)
	@if [ -z "$(RELEASE_VERSION)" ]; then \
		echo "ERROR: RELEASE_VERSION is required"; \
		exit 1; \
	fi
	mkdir -p dist
	helm package "$(UMBRELLA_HELM_CHART_PATH)" --version "$(RELEASE_VERSION)" --app-version "$(IMAGE_VERSION)" --destination dist/
	helm package "$(HELM_CHART_PATH)" --version "$(RELEASE_VERSION)" --app-version "$(IMAGE_VERSION)" --destination dist/
	helm package "$(API_SERVER_HELM_CHART_PATH)" --version "$(RELEASE_VERSION)" --app-version "$(IMAGE_VERSION)" --destination dist/
	helm package "$(GATEWAY_HELM_CHART_PATH)" --version "$(RELEASE_VERSION)" --app-version "$(IMAGE_VERSION)" --destination dist/

.PHONY: ci-release-notes
ci-release-notes: ## Write release notes to GITHUB_OUTPUT
	scripts/ci/release-notes.sh

.PHONY: ci-scan-image
ci-scan-image: ensure-trivy ## Scan a built container image with Trivy (usage: make ci-scan-image IMAGE=repo/name:tag)
	@if [ -z "$(IMAGE)" ]; then \
		echo "ERROR: IMAGE is required"; \
		exit 1; \
	fi
	trivy image --severity $(TRIVY_SEVERITY) --exit-code 1 "$(IMAGE)"

.PHONY: security-scan-fs
security-scan-fs: ensure-trivy ## Run Trivy filesystem scan and write SARIF output
	@echo "Running Trivy filesystem scan..."
	trivy fs --severity $(TRIVY_SEVERITY) $(TRIVY_IGNORE_ARGS) --exit-code 1 --format sarif --output $(TRIVY_FS_SARIF) .
	@echo "Filesystem scan complete: $(TRIVY_FS_SARIF)"

.PHONY: security-scan-config
security-scan-config: ensure-trivy helm-template ## Run Trivy config scan and fail on findings
	@echo "Running Trivy config scan..."
	@status=0; \
	for target in $(TRIVY_CONFIG_TARGETS); do \
		echo "Scanning $$target..."; \
		trivy config --severity $(TRIVY_SEVERITY) --exit-code 1 \
			$(TRIVY_IGNORE_ARGS) \
			--skip-files "$(DOCKERFILE).dockerignore" \
			--skip-files "$(TF_DIR)/**/tfplan*" \
			"$$target" || status=$$?; \
	done; \
	exit $$status
	@echo "Config scan complete"

.PHONY: ci-security
ci-security: security-scan-fs security-scan-config ## Run CI security checks

.PHONY: ci-e2e-local-contracts
ci-e2e-local-contracts: test-e2e-local-contracts ## Run the CI local k3d contract e2e suite

.PHONY: ci-build
ci-build: godeps ci-lint ci-test ci-verify-manifests ci-terraform-validate-all ci-security docker-build ## CI build pipeline

.PHONY: ci-release
ci-release: ci-build ## CI release validation; publishing is handled by the release workflow

# =============================================================================
# Documentation Commands
# =============================================================================

.PHONY: godocs
godocs: ## Generate Go documentation
	@echo "Generating Go documentation..."
	@if ! command -v godoc >/dev/null 2>&1; then \
		echo "Installing godoc..."; \
		go install golang.org/x/tools/cmd/godoc@latest; \
	fi
	@echo "Go documentation generated. Run 'make godocs-serve' to view."

.PHONY: godocs-serve
godocs-serve: ## Serve Go documentation locally
	@echo "Starting godoc server on http://localhost:6060"
	@echo "Press Ctrl+C to stop"
	@if ! command -v godoc >/dev/null 2>&1; then \
		echo "Installing godoc..."; \
		go install golang.org/x/tools/cmd/godoc@latest; \
	fi
	$$(go env GOPATH)/bin/godoc -http=:6060

.PHONY: godocs-open
godocs-open: ## Open Go documentation in browser
	@echo "Opening Go documentation in browser..."
	@if command -v xdg-open >/dev/null 2>&1; then \
		xdg-open http://localhost:6060/pkg/github.com/xtrinode/xtrinode/; \
	elif command -v open >/dev/null 2>&1; then \
		open http://localhost:6060/pkg/github.com/xtrinode/xtrinode/; \
	else \
		echo "Please open http://localhost:6060/pkg/github.com/xtrinode/xtrinode/ in your browser"; \
	fi

.PHONY: verify
verify: verify-manifests check test terraform-validate ## Verify everything (manifests, code quality, tests, terraform)
