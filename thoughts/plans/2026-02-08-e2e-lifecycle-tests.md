# E2E AgentTask Lifecycle Tests - Implementation Plan

## Overview

Add comprehensive e2e tests that validate the full AgentTask lifecycle in a Kind cluster with real agent-sandbox provisioning: task creation, sandbox provisioning, runner assignment, task execution (data fetch + status report), callback notification, and terminal cleanup.

## Current State Analysis

**What exists:**
- E2e tests (`test/e2e/`) validate controller startup and metrics endpoint only
- Runner stub (`cmd/shepherd-runner/`) accepts task assignment and exits without calling API
- `config/test` overlay strips GitHub credentials, sets `imagePullPolicy: IfNotPresent`
- `make test-e2e` / `test-e2e-interactive` / `test-e2e-existing` Makefile targets
- Agent-sandbox CRDs synced to `config/crd/external/` for envtest only (not deployed to cluster)

**What's broken:**
- `e2e_suite_test.go:53` calls `make docker-build` which doesn't exist (Makefile uses Ko)
- `e2e_suite_test.go:36` hardcodes `example.com/shepherd:v0.0.1` image (doesn't match Ko output)
- `e2e_test.go:72` uses `make deploy` instead of `make deploy-test` (overwrites test config)
- `test/utils/utils.go:139` reads `KIND_CLUSTER` env var but Makefile uses `KIND_CLUSTER_NAME`

**What's missing:**
- Agent-sandbox operator not deployed in Kind cluster
- No SandboxTemplate for e2e runner
- No lifecycle test (create task -> verify terminal state -> verify cleanup)
- Runner stub doesn't exercise API (data fetch, status reporting)

### Key Discoveries:
- Controller RBAC already includes permissions for `sandboxclaims` (create/delete/get/list/watch) and `sandboxes` (get) — `config/rbac/role.yaml:14-41`
- Controller hardcodes internal API URL: `http://shepherd-shepherd-api.shepherd-system.svc.cluster.local:8081` — `config/manager/manager.yaml:68`
- Runner receives `{taskID, apiURL}` in assignment payload — `cmd/shepherd-runner/main.go:14-17`
- SandboxTemplate requires only `spec.podTemplate.spec.containers` — `config/crd/external/extensions.agents.x-k8s.io_sandboxtemplates.yaml`
- POC template in `poc/sandbox/manifests/sandbox-template.yaml` demonstrates the pattern

## Desired End State

After this plan:
1. `make test-e2e` creates a Kind cluster with agent-sandbox + shepherd deployed, runs lifecycle tests, tears down
2. `make test-e2e-interactive` does the same but keeps the cluster alive
3. Tests validate: AgentTask creation -> SandboxClaim -> Sandbox Ready -> runner assignment -> data fetch -> status report -> Succeeded condition -> Notified condition -> SandboxClaim cleanup
4. Runner stub exercises the full internal API (data + status endpoints)

**Verification:** `make test-e2e` passes end-to-end with no manual intervention.

## What We're NOT Doing

- GitHub token testing in e2e (unit tests cover this in `pkg/api/handler_token_test.go`)
- In-cluster GitHub API stub
- Callback payload/HMAC verification (unit tests cover this in `pkg/api/callback_test.go`)
- Multiple concurrent task tests
- Custom SandboxTemplate variations
- Timeout/failure scenario tests (follow-up plan)

## Implementation Approach

Fix broken infrastructure first, then add agent-sandbox support, extend the runner stub, and finally add the lifecycle test. Each phase produces independently testable changes.

---

## Phase 1: Fix E2E Test Infrastructure

### Overview
Remove broken BeforeSuite build/load logic and fix BeforeAll to use the correct deploy target. The Makefile prerequisites handle building and deploying; the test code should not duplicate this.

### Changes Required:

#### 1. Fix BeforeSuite
**File**: `test/e2e/e2e_suite_test.go`
**Changes**: Remove the `make docker-build` and `LoadImageToKindClusterWithName` calls. The Makefile's `ko-build-kind` already builds and loads images before tests run. Keep cert-manager setup.

Remove lines 52-61 (the docker-build and image load block). The BeforeSuite should only call `setupCertManager()`.

```go
var _ = BeforeSuite(func() {
	setupCertManager()
})
```

Also remove the `managerImage` variable (line 36) since it's no longer used.

#### 2. Fix BeforeAll deployment
**File**: `test/e2e/e2e_test.go`
**Changes**: Replace `make deploy IMG=...` with `make deploy-test` to use the test overlay (IfNotPresent + no GitHub secrets).

Change line 72:
```go
// Before:
cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
// After:
cmd = exec.Command("make", "deploy-test")
```

#### 3. Fix Kind cluster name in test utils
**File**: `test/utils/utils.go`
**Changes**: Read `KIND_CLUSTER_NAME` env var (matching Makefile) instead of `KIND_CLUSTER` in `LoadImageToKindClusterWithName`. This function is still used by CertManager setup path.

Change line 139:
```go
// Before:
if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
// After:
if v, ok := os.LookupEnv("KIND_CLUSTER_NAME"); ok {
```

### Success Criteria:

#### Automated Verification:
- [ ] `go vet ./test/...` passes
- [ ] `make test-e2e-interactive` runs the two existing tests (controller startup + metrics) successfully

#### Manual Verification:
- [ ] Confirm `make test-e2e-interactive` deploys using `config/test` overlay (verify with `kubectl get deploy -n shepherd-system shepherd-shepherd-api -o yaml` — no GitHub env vars present)

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation from the human that the manual testing was successful before proceeding to the next phase.

---

## Phase 2: Add Agent-Sandbox to E2E Infrastructure

### Overview
Add Makefile targets to install agent-sandbox and create a SandboxTemplate for the e2e runner. Update test-e2e targets to include agent-sandbox setup.

### Changes Required:

#### 1. Add agent-sandbox Makefile targets
**File**: `Makefile`
**Changes**: Add `AGENT_SANDBOX_VERSION` variable and `install-agent-sandbox` target after the Kind section (after line 146).

```makefile
## Agent-Sandbox
AGENT_SANDBOX_VERSION ?= v0.1.1

.PHONY: install-agent-sandbox
install-agent-sandbox: ## Install agent-sandbox operator into the cluster.
	$(KUBECTL) apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/$(AGENT_SANDBOX_VERSION)/manifest.yaml
	$(KUBECTL) apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/$(AGENT_SANDBOX_VERSION)/extensions.yaml
	$(KUBECTL) wait --for=condition=Available deployment -l control-plane=controller-manager -n agent-sandbox-system --timeout=2m
```

Note: The `kubectl wait` label selector should be verified against the actual agent-sandbox release manifests. If agent-sandbox doesn't use that label, adjust accordingly.

#### 2. Add e2e fixture deployment target
**File**: `Makefile`
**Changes**: Add `deploy-e2e-fixtures` target to create the SandboxTemplate.

```makefile
.PHONY: deploy-e2e-fixtures
deploy-e2e-fixtures: ## Deploy e2e test fixtures (SandboxTemplate).
	$(KUBECTL) apply -f test/e2e/fixtures/
```

#### 3. Update test-e2e targets
**File**: `Makefile`
**Changes**: Add `install-agent-sandbox` and `deploy-e2e-fixtures` to the dependency chain.

```makefile
test-e2e: kind-create ko-build-kind install-agent-sandbox install deploy-test deploy-e2e-fixtures
	go test ./test/e2e/ -v -count=1 -timeout 10m
	$(MAKE) kind-delete
```

Also update `test-e2e-interactive`:
```makefile
test-e2e-interactive:
	@if kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		echo "Reusing existing cluster: $(KIND_CLUSTER_NAME)"; \
	else \
		echo "Creating new cluster: $(KIND_CLUSTER_NAME)"; \
		$(MAKE) kind-create; \
	fi
	$(MAKE) ko-build-kind install-agent-sandbox install deploy-test deploy-e2e-fixtures
	go test ./test/e2e/ -v -count=1 -timeout 10m
```

Note: Timeout increased from 5m to 10m to account for agent-sandbox provisioning time.

#### 4. Create SandboxTemplate fixture
**File**: `test/e2e/fixtures/sandbox-template.yaml` (new file)
**Changes**: Create SandboxTemplate referencing `shepherd-runner:latest` image.

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-runner
  namespace: shepherd-system
spec:
  podTemplate:
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: runner
          image: shepherd-runner:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8888
              protocol: TCP
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8888
            initialDelaySeconds: 2
            periodSeconds: 5
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - "ALL"
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "200m"
      restartPolicy: Never
```

#### 5. Create Kind cluster config with port mapping
**File**: `test/e2e/kind-config.yaml` (new file)
**Changes**: Configure Kind to map a NodePort to the host so the test process can reach the API service directly (no port-forward needed).

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: 30080
    protocol: TCP
```

#### 6. Update Makefile to use Kind config
**File**: `Makefile`
**Changes**: Update `kind-create` to use the config file.

```makefile
kind-create: ## Create a kind cluster for development/testing.
	kind create cluster --name "$(KIND_CLUSTER_NAME)" --config test/e2e/kind-config.yaml
```

#### 7. Add NodePort patch to test overlay
**File**: `config/test/kustomization.yaml`
**Changes**: Add a patch to expose the API service as NodePort on port 30080 (matching the Kind extraPortMappings). Add after the existing patches.

```yaml
- patch: |-
    - op: add
      path: /spec/type
      value: NodePort
    - op: add
      path: /spec/ports/0/nodePort
      value: 30080
  target:
    kind: Service
    name: shepherd-shepherd-api
```

This lets the test process reach the API at `localhost:30080` without any port-forward setup.

#### 8. Wait for agent-sandbox in BeforeAll
**File**: `test/e2e/e2e_test.go`
**Changes**: After deploying CRDs and controller, verify agent-sandbox is ready. Add after the `make deploy-test` call in BeforeAll.

```go
By("verifying agent-sandbox controller is available")
cmd = exec.Command("kubectl", "wait", "--for=condition=Available",
	"deployment", "-l", "control-plane=controller-manager",
	"-n", "agent-sandbox-system", "--timeout=2m")
_, err = utils.Run(cmd)
Expect(err).NotTo(HaveOccurred(), "agent-sandbox controller not available")
```

### Success Criteria:

#### Automated Verification:
- [ ] `make test-e2e-interactive` succeeds (existing tests still pass)
- [ ] `kubectl get sandboxtemplates -n shepherd-system` shows `e2e-runner` template
- [ ] `kubectl get deployment -n agent-sandbox-system` shows running controller

#### Manual Verification:
- [ ] Create a test SandboxClaim manually and verify agent-sandbox provisions a Sandbox pod:
  ```bash
  kubectl apply -f - <<EOF
  apiVersion: extensions.agents.x-k8s.io/v1alpha1
  kind: SandboxClaim
  metadata:
    name: manual-test
    namespace: shepherd-system
  spec:
    sandboxTemplateRef:
      name: e2e-runner
  EOF
  kubectl get sandboxclaim manual-test -n shepherd-system -w
  # Wait for Ready condition, then clean up:
  kubectl delete sandboxclaim manual-test -n shepherd-system
  ```

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual verification that agent-sandbox provisions sandboxes correctly before proceeding to the next phase.

---

## Phase 3: Extend Runner Stub

### Overview
After receiving a task assignment, the runner stub should fetch task data and report a completed status via the internal API, then exit. This exercises the full runner-to-API flow.

### Changes Required:

#### 1. Add API interaction after assignment
**File**: `cmd/shepherd-runner/main.go`
**Changes**: Replace the immediate exit after assignment with a function that calls the data and status endpoints.

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// TaskAssignment is the payload sent by the operator when assigning a task.
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	assigned := make(chan TaskAssignment, 1)

	mux := newMux(assigned)

	srv := &http.Server{Addr: ":8888", Handler: mux}
	go func() {
		slog.Info("runner stub listening", "addr", ":8888")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	select {
	case ta := <-assigned:
		slog.Info("task assigned", "taskID", ta.TaskID, "apiURL", ta.APIURL)
		if err := executeTask(ctx, ta); err != nil {
			slog.Error("task execution failed, reporting failure", "error", err)
			_ = reportStatus(ctx, ta, "failed", err.Error())
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	_ = srv.Shutdown(context.Background())
}

func executeTask(ctx context.Context, ta TaskAssignment) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. Fetch task data
	dataURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/data"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dataURL, nil)
	if err != nil {
		return fmt.Errorf("creating data request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching task data: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected data response: %d %s", resp.StatusCode, string(body))
	}
	slog.Info("task data fetched", "taskID", ta.TaskID)

	// 2. Report completed status
	return reportStatus(ctx, ta, "completed", "stub runner completed successfully")
}

func reportStatus(ctx context.Context, ta TaskAssignment, event, message string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	statusURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/status"
	payload, _ := json.Marshal(map[string]string{
		"event":   event,
		"message": message,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, statusURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reporting status: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status response: %d", resp.StatusCode)
	}
	slog.Info("status reported", "taskID", ta.TaskID, "event", event)
	return nil
}

// newMux unchanged from current implementation
func newMux(assigned chan<- TaskAssignment) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var ta TaskAssignment
		if err := json.NewDecoder(r.Body).Decode(&ta); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		slog.Info("received task assignment", "taskID", ta.TaskID, "apiURL", ta.APIURL)
		select {
		case assigned <- ta:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			http.Error(w, "task already assigned", http.StatusConflict)
		}
	})
	return mux
}
```

#### 2. Update runner test
**File**: `cmd/shepherd-runner/main_test.go`
**Changes**: Add test for `executeTask` and `reportStatus` using httptest servers. Follow existing test patterns.

```go
func TestExecuteTask(t *testing.T) {
	// Mock API server
	var dataRequested, statusRequested atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/data"):
			dataRequested.Store(true)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"description": "test task",
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/status"):
			statusRequested.Store(true)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"accepted"}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer api.Close()

	ta := TaskAssignment{TaskID: "test-task", APIURL: api.URL}
	err := executeTask(context.Background(), ta)
	require.NoError(t, err)
	assert.True(t, dataRequested.Load(), "should have requested task data")
	assert.True(t, statusRequested.Load(), "should have reported status")
}
```

### Success Criteria:

#### Automated Verification:
- [ ] `go test ./cmd/shepherd-runner/ -v` passes
- [ ] `go vet ./cmd/shepherd-runner/` passes
- [ ] `make lint` passes

#### Manual Verification:
- [ ] None needed — unit test validates the behavior

**Implementation Note**: After completing this phase and all automated verification passes, proceed to Phase 4.

---

## Phase 4: AgentTask Lifecycle E2E Test

### Overview
Add the core lifecycle test: create an AgentTask **via the public API** (POST /api/v1/tasks), wait for it to reach `Succeeded` state via the full provisioning flow (API validation + creation -> controller -> SandboxClaim -> agent-sandbox -> Sandbox -> runner -> API -> callback -> terminal).

Using the API instead of `kubectl apply` is important because:
- Tests the actual creation flow (API validation, context compression, random task name generation)
- More realistic — production tasks come through the API, not kubectl
- Validates the API server's public port is reachable and working
- `kubectl apply` would bypass all handler logic in `pkg/api/handler_tasks.go`

### Changes Required:

#### 1. Add lifecycle test using the public API via NodePort
**File**: `test/e2e/e2e_test.go`
**Changes**: Add a new `Describe` block that creates a task via `POST /api/v1/tasks` to the NodePort exposed in Phase 2 (`localhost:30080`), then uses `Eventually` with kubectl to wait for each lifecycle stage. The task name comes from the API response (randomly generated by the server as `task-<8-char-random>`).

The API request body matches the `CreateTaskRequest` type from `pkg/api/types.go`:

```json
{
  "repo":        {"url": "...", "ref": "..."},
  "task":        {"description": "...", "context": "..."},
  "callbackURL": "...",
  "runner":      {"sandboxTemplateName": "...", "timeout": "..."}
}
```

The API returns `TaskResponse` with an `id` field containing the generated task name.

Add a constant for the API address (alongside existing constants):

```go
const apiURL = "http://localhost:30080"
```

```go
var _ = Describe("AgentTask Lifecycle", Ordered, func() {
	var taskName string // Set after API creates the task

	BeforeAll(func() {
		By("creating the AgentTask via the public API")
		reqBody := `{
			"repo": {"url": "https://github.com/test-org/test-repo.git", "ref": "main"},
			"task": {
				"description": "E2E lifecycle test task",
				"context": "This is an e2e test to validate the full task lifecycle"
			},
			"callbackURL": "https://example.com/callback",
			"runner": {
				"sandboxTemplateName": "e2e-runner",
				"timeout": "5m"
			}
		}`
		resp, err := http.Post(
			apiURL+"/api/v1/tasks",
			"application/json",
			strings.NewReader(reqBody),
		)
		Expect(err).NotTo(HaveOccurred(), "Failed to POST task to API")
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusCreated),
			"Expected 201 Created from API")

		var taskResp struct {
			ID string `json:"id"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&taskResp)).To(Succeed())
		Expect(taskResp.ID).NotTo(BeEmpty(), "API should return a task ID")
		taskName = taskResp.ID
		GinkgoWriter.Printf("Created task: %s\n", taskName)
	})

	AfterAll(func() {
		if taskName != "" {
			By("cleaning up the AgentTask")
			cmd := exec.Command("kubectl", "delete", "agenttask", taskName,
				"-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		}
	})

	It("should create a SandboxClaim for the task", func() {
		verifySandboxClaim := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "sandboxclaim", taskName,
				"-n", namespace, "-o", "jsonpath={.metadata.name}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal(taskName))
		}
		Eventually(verifySandboxClaim, 30*time.Second, time.Second).Should(Succeed())
	})

	It("should reach Running state when sandbox is ready", func() {
		verifyRunning := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "agenttask", taskName,
				"-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type==\"Succeeded\")].reason}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"))
		}
		Eventually(verifyRunning, 3*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should reach Succeeded state after runner completes", func() {
		verifySucceeded := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "agenttask", taskName,
				"-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type==\"Succeeded\")].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"))
		}
		Eventually(verifySucceeded, 3*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should set Notified condition after terminal state", func() {
		verifyNotified := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "agenttask", taskName,
				"-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type==\"Notified\")].reason}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			// CallbackSent if example.com responds 2xx, CallbackFailed otherwise — either is valid
			g.Expect(output).To(SatisfyAny(
				Equal("CallbackSent"),
				Equal("CallbackFailed"),
			))
		}
		Eventually(verifyNotified, 30*time.Second, 2*time.Second).Should(Succeed())
	})

	It("should clean up the SandboxClaim after terminal state", func() {
		verifyClaimDeleted := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "sandboxclaim", taskName,
				"-n", namespace, "--no-headers")
			_, err := utils.Run(cmd)
			g.Expect(err).To(HaveOccurred(), "SandboxClaim should be deleted")
		}
		Eventually(verifyClaimDeleted, 60*time.Second, 2*time.Second).Should(Succeed())
	})
})
```

#### 2. Add lifecycle resource logs to diagnostic collection
**File**: `test/e2e/e2e_test.go`
**Changes**: In the `AfterEach` failure handler, also collect runner pod logs and AgentTask status for debugging.

Add to the diagnostic collection block (after the existing controller log collection):

```go
By("Fetching AgentTask status")
cmd = exec.Command("kubectl", "get", "agenttask", "-n", namespace, "-o", "yaml")
agentTaskOutput, err := utils.Run(cmd)
if err == nil {
	_, _ = fmt.Fprintf(GinkgoWriter, "AgentTask status:\n%s\n", agentTaskOutput)
}

By("Fetching SandboxClaim status")
cmd = exec.Command("kubectl", "get", "sandboxclaim", "-n", namespace, "-o", "yaml")
claimOutput, err := utils.Run(cmd)
if err == nil {
	_, _ = fmt.Fprintf(GinkgoWriter, "SandboxClaim status:\n%s\n", claimOutput)
}

By("Fetching Sandbox status")
cmd = exec.Command("kubectl", "get", "sandbox", "-n", namespace, "-o", "yaml")
sandboxOutput, err := utils.Run(cmd)
if err == nil {
	_, _ = fmt.Fprintf(GinkgoWriter, "Sandbox status:\n%s\n", sandboxOutput)
}
```

### Success Criteria:

#### Automated Verification:
- [ ] `make test-e2e` passes with all tests (existing + lifecycle)
- [ ] `make test-e2e-interactive` passes and keeps cluster alive

#### Manual Verification:
- [ ] After `make test-e2e-interactive`, inspect cluster state:
  ```bash
  kubectl get agenttask -n shepherd-system -o wide
  kubectl get sandboxclaim -n shepherd-system
  kubectl get sandbox -n shepherd-system
  ```
- [ ] Verify task shows `Succeeded` in STATUS column
- [ ] Verify no orphaned SandboxClaims remain

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation that the full lifecycle works correctly before marking the plan as complete.

---

## Testing Strategy

### Unit Tests:
- Runner stub: `TestExecuteTask` validates data fetch + status report flow (Phase 3)
- No changes to existing controller or API unit tests

### E2E Tests:
- **Existing**: Controller startup, metrics endpoint (unchanged)
- **New**: Full AgentTask lifecycle (create -> SandboxClaim -> Running -> Succeeded -> Notified -> cleanup)

### Manual Testing Steps:
1. `make test-e2e-interactive` — full automated flow
2. Create an AgentTask manually and watch it progress through states
3. Check runner pod logs to confirm data fetch and status report

## Performance Considerations

- Test timeout increased from 5m to 10m to account for:
  - Agent-sandbox operator startup (~30s)
  - Sandbox pod provisioning (~15-30s)
  - Runner execution (~5s)
  - Reconciliation cycles (~10-30s between stages)
- Individual test step timeouts use 30s-3min windows with 2s polling

## Dependencies

- **agent-sandbox v0.1.1**: Must be installable from GitHub releases
- **Kind**: Must support loading multiple images
- **cert-manager**: Already handled by existing test infrastructure

## References

- Research: `thoughts/research/2026-02-08-e2e-testing-approaches.md`
- POC SandboxTemplate: `poc/sandbox/manifests/sandbox-template.yaml`
- Controller reconciliation: `internal/controller/agenttask_controller.go:67-215`
- Runner stub: `cmd/shepherd-runner/main.go`
- Test overlay: `config/test/kustomization.yaml`
- Controller RBAC: `config/rbac/role.yaml`
