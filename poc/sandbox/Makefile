KO ?= ko
KO_DOCKER_REPO ?= ko.local/nissessenap/shepherd/poc/sandbox
KIND_CLUSTER ?= agent-sandbox

.PHONY: build-entrypoint
build-entrypoint: ## Build entrypoint image with ko.
	KO_DOCKER_REPO=$(KO_DOCKER_REPO) $(KO) build --sbom=none --bare ./cmd/entrypoint/

.PHONY: build-orchestrator
build-orchestrator: ## Build orchestrator binary (runs locally, not in container).
	go build -o bin/orchestrator ./cmd/orchestrator/

.PHONY: kind-load
kind-load: build-entrypoint ## Load entrypoint image into Kind cluster.
	KO_DOCKER_REPO=kind.local/poc-sandbox $(KO) build --sbom=none --bare ./cmd/entrypoint/
	@echo "Image loaded into Kind cluster"

.PHONY: deploy
deploy: ## Apply SandboxTemplate and SandboxClaim.
	kubectl apply -f manifests/sandbox-template.yaml
	kubectl apply -f manifests/sandbox-claim.yaml

.PHONY: undeploy
undeploy: ## Delete SandboxClaim and SandboxTemplate.
	kubectl delete -f manifests/sandbox-claim.yaml --ignore-not-found
	kubectl delete -f manifests/sandbox-template.yaml --ignore-not-found

.PHONY: status
status: ## Show sandbox status.
	@echo "=== SandboxClaim ==="
	kubectl get sandboxclaim poc-test-task -o wide 2>/dev/null || echo "not found"
	@echo ""
	@echo "=== Sandbox ==="
	kubectl get sandbox poc-test-task -o wide 2>/dev/null || echo "not found"
	@echo ""
	@echo "=== Pod ==="
	kubectl get pod -l agents.x-k8s.io/sandbox-name-hash -o wide 2>/dev/null || echo "no pods"
	@echo ""
	@echo "=== Service ==="
	kubectl get svc poc-test-task 2>/dev/null || echo "not found"

.PHONY: logs
logs: ## Show sandbox pod logs.
	kubectl logs -l agents.x-k8s.io/sandbox-name-hash --tail=50

.PHONY: test
test: ## Run Go tests.
	go test ./...

.PHONY: clean
clean: undeploy ## Clean up all resources.
