# Image URL to use all building/pushing image targets
IMG ?= shepherd:latest
RUNNER_IMG ?= shepherd-runner:latest

# Kind cluster name used by kind-create / kind-delete / ko-build-kind
KIND_CLUSTER_NAME ?= shepherd

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: sync-external-crds
sync-external-crds: ## Copy external CRDs from Go module cache for envtest.
	@mkdir -p config/crd/external
	@MODCACHE=$$(go env GOMODCACHE) && \
	 SANDBOX_VERSION=$$(go list -m -f '{{.Version}}' sigs.k8s.io/agent-sandbox) && \
	 cp -f "$${MODCACHE}/sigs.k8s.io/agent-sandbox@$${SANDBOX_VERSION}/k8s/crds/"*.yaml config/crd/external/ && \
	 chmod u=rw,go=r config/crd/external/*.yaml
	@echo "Synced agent-sandbox CRDs to config/crd/external/"

.PHONY: test
test: manifests generate fmt vet setup-envtest sync-external-crds ## Run all tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ E2E Testing

.PHONY: test-e2e
test-e2e: kind-create ko-build-kind install deploy-test ## Spin up kind cluster, build/load images, deploy, and run e2e tests.
	go test ./test/e2e/ -v -count=1 -timeout 5m
	$(MAKE) kind-delete

.PHONY: test-e2e-interactive
test-e2e-interactive: ## Run e2e tests, keeping the Kind cluster alive for debugging.
	@if kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		echo "Reusing existing cluster: $(KIND_CLUSTER_NAME)"; \
	else \
		echo "Creating new cluster: $(KIND_CLUSTER_NAME)"; \
		$(MAKE) kind-create; \
	fi
	$(MAKE) ko-build-kind install deploy-test
	go test ./test/e2e/ -v -count=1 -timeout 5m

.PHONY: test-e2e-existing
test-e2e-existing: ## Run e2e tests against an already-running cluster.
	go test ./test/e2e/ -v -count=1 -timeout 5m

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/shepherd ./cmd/shepherd/

.PHONY: run
run: manifests generate fmt vet ## Run the operator from your host.
	SHEPHERD_API_URL=http://localhost:8080 \
	go run ./cmd/shepherd/ operator --leader-election=false

.PHONY: ko-build-local
ko-build-local: ko ## Build shepherd image locally with ko (sets IMG).
	$(eval IMG := $(shell KO_DOCKER_REPO=$(KO_DOCKER_REPO) "$(KO)" build --sbom=none --bare --local ./cmd/shepherd/))
	@echo "IMG=$(IMG)"

.PHONY: ko-build-runner-local
ko-build-runner-local: ko ## Build runner stub image locally with ko (sets RUNNER_IMG).
	$(eval RUNNER_IMG := $(shell KO_DOCKER_REPO=$(KO_DOCKER_REPO) "$(KO)" build --sbom=none --bare --local ./cmd/shepherd-runner/))
	@echo "RUNNER_IMG=$(RUNNER_IMG)"

.PHONY: build-smoke
build-smoke: ko-build-local ko-build-runner-local manifests kustomize ## Verify ko builds + kustomize render.
	"$(KUSTOMIZE)" build config/default > /dev/null
	@echo "Build smoke test passed: ko images built, kustomize renders cleanly"

.PHONY: ko-build-kind
ko-build-kind: ko-build-local ko-build-runner-local ## Build images and load them into the kind cluster.
	docker tag "$(IMG)" shepherd:latest
	docker tag "$(RUNNER_IMG)" shepherd-runner:latest
	kind load docker-image shepherd:latest --name "$(KIND_CLUSTER_NAME)"
	kind load docker-image shepherd-runner:latest --name "$(KIND_CLUSTER_NAME)"

##@ Kind

.PHONY: kind-create
kind-create: ## Create a kind cluster for development/testing.
	kind create cluster --name "$(KIND_CLUSTER_NAME)"

.PHONY: kind-delete
kind-delete: ## Delete the kind cluster.
	kind delete cluster --name "$(KIND_CLUSTER_NAME)"

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/default && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: deploy-test
deploy-test: manifests kustomize ## Deploy for local testing (IfNotPresent pull policy, no GitHub secrets).
	cd config/test && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/test | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
KO ?= $(LOCALBIN)/ko

## Tool Versions
KUSTOMIZE_VERSION ?= v5.7.1
CONTROLLER_TOOLS_VERSION ?= v0.20.0
KO_VERSION ?= v0.17.1

## Ko
KO_DOCKER_REPO ?= ko.local/nissessenap/shepherd

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.7.2
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: ko
ko: $(KO) ## Download ko locally if necessary.
$(KO): $(LOCALBIN)
	$(call go-install-tool,$(KO),github.com/google/ko,$(KO_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
