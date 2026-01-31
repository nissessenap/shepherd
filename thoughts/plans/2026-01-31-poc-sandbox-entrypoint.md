# PoC: Agent-Sandbox Entrypoint Integration

## Overview

Validate the agent-sandbox integration by building a minimal entrypoint binary, deploying it inside a sandbox via SandboxTemplate + SandboxClaim, and then writing a small Go orchestrator that programmatically creates claims and communicates with the runner. This PoC informs decisions for the full shepherd operator migration to agent-sandbox.

## Current State Analysis

- The shepherd operator currently uses Kubernetes Jobs with an init container pattern
- agent-sandbox is deployed on a Kind cluster with extensions enabled
- agent-sandbox provides: SandboxTemplate (pod definition + network policy), SandboxClaim (provisions from template), headless Service per sandbox with FQDN `{name}.{ns}.svc.cluster.local`
- agent-sandbox module path: `sigs.k8s.io/agent-sandbox`
  - Core types: `sigs.k8s.io/agent-sandbox/api/v1alpha1` (Sandbox)
  - Extension types: `sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1` (SandboxTemplate, SandboxClaim)

### Key Discoveries:
- Importing agent-sandbox types is lightweight — the `api/v1alpha1` and `extensions/api/v1alpha1` packages are just structs + scheme registration, no controller-runtime dependency at the import level
- SandboxClaim creates a Sandbox which creates a Pod + headless Service automatically
- `Sandbox.Status.ServiceFQDN` provides the FQDN to reach the pod (e.g., `my-sandbox.default.svc.cluster.local`)
- `AutomountServiceAccountToken` defaults to `false` in agent-sandbox
- NetworkPolicy is created per-claim using the claim's UID as pod selector
- SandboxClaim `Ready=True` condition indicates the pod is running and service exists
- The SandboxClaim controller does NOT propagate `shutdownTime` to the Sandbox — the claim controller enforces expiration itself

## Desired End State

A working PoC that demonstrates:
1. A Go entrypoint binary that runs inside a sandbox pod, serving HTTP on `:8888`
2. A SandboxTemplate + SandboxClaim that provisions the sandbox
3. Manual verification via `kubectl apply` + `curl` to the headless service
4. A Go orchestrator program that creates a SandboxClaim, watches for Ready, and POSTs a task to the runner

### How to Verify

**Phase 1 (manual):**
- `kubectl apply` the SandboxTemplate and SandboxClaim
- Pod becomes Ready, headless Service is created
- `curl` to the sandbox's headless service on port 8888 returns healthz OK
- `POST /task` to the sandbox triggers work and the container reports completion

**Phase 2 (orchestrator):**
- Run the orchestrator binary locally (with kubeconfig)
- It creates a SandboxClaim, watches for Ready, POSTs a task, and logs the result
- The sandbox pod exits after completing the task

## What We're NOT Doing

- Warm pools (SandboxWarmPool) — cold start only
- NetworkPolicy in the template — keep it simple for PoC
- gVisor runtime class — requires gVisor on the cluster
- GitHub token generation or real task execution
- API server integration
- CRD changes to AgentTask
- Operator modifications
- Production-quality error handling or retries

## Implementation Approach

Two phases: First, build the entrypoint and validate manually. Second, build a Go orchestrator that automates what we did manually. Both phases share the entrypoint binary.

The PoC lives in `poc/sandbox/` with its own `go.mod` to avoid polluting shepherd's dependency tree with agent-sandbox types.

---

## Phase 1: Entrypoint Binary + Manual Validation

### Overview

Build a thin Go HTTP server that listens on `:8888`, serves `/healthz`, and accepts `POST /task`. On receiving a task, it logs the work, does a simulated action (creates a file, sleeps briefly), reports completion by printing to stdout, and exits. Build it with ko, deploy via SandboxTemplate + SandboxClaim, validate with kubectl + curl.

### Changes Required

#### 1. Directory Structure

```
poc/sandbox/
├── go.mod
├── go.sum
├── cmd/
│   ├── entrypoint/          # The runner entrypoint binary
│   │   └── main.go
│   └── orchestrator/        # Phase 2: Go orchestrator
│       └── main.go
├── manifests/
│   ├── sandbox-template.yaml
│   └── sandbox-claim.yaml
├── Makefile
└── README.md
```

#### 2. Go Module

**File**: `poc/sandbox/go.mod`

```go
module github.com/NissesSenap/shepherd/poc/sandbox

go 1.25.3

require (
    sigs.k8s.io/agent-sandbox v0.1.0
    k8s.io/api v0.35.0
    k8s.io/apimachinery v0.35.0
    k8s.io/client-go v0.35.0
    sigs.k8s.io/controller-runtime v0.23.0
)
```

Note: agent-sandbox v0.1.0 is published upstream. The K8s dependency versions may need adjustment to match what agent-sandbox v0.1.0 requires (it uses controller-runtime v0.22.2 / k8s v0.34.x). If there's a version conflict, we may need to align — `go mod tidy` will resolve this.

#### 3. Entrypoint Binary

**File**: `poc/sandbox/cmd/entrypoint/main.go`

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"
)

// TaskAssignment is the payload POSTed to /task.
type TaskAssignment struct {
    TaskID  string `json:"taskID"`
    Message string `json:"message"`
}

func main() {
    log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    log.Info("entrypoint starting", "port", 8888)

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    taskCh := make(chan TaskAssignment, 1)

    mux := http.NewServeMux()

    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
        var req TaskAssignment
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, "invalid request", http.StatusBadRequest)
            return
        }
        log.Info("received task assignment", "taskID", req.TaskID, "message", req.Message)

        select {
        case taskCh <- req:
            w.WriteHeader(http.StatusAccepted)
            json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
        default:
            http.Error(w, "task already assigned", http.StatusConflict)
        }
    })

    srv := &http.Server{Addr: ":8888", Handler: mux}
    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error("server error", "error", err)
            os.Exit(1)
        }
    }()

    log.Info("waiting for task assignment...")

    // Block until task arrives or context is cancelled
    var assignment TaskAssignment
    select {
    case assignment = <-taskCh:
        log.Info("task received, shutting down HTTP server")
    case <-ctx.Done():
        log.Info("context cancelled, shutting down")
        srv.Shutdown(context.Background())
        return
    }

    // Shutdown HTTP server before doing work
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer shutdownCancel()
    srv.Shutdown(shutdownCtx)

    // Phase 2: Do the work
    log.Info("executing task", "taskID", assignment.TaskID)

    // Simulate work: write a file, sleep
    workDir := "/tmp/work"
    os.MkdirAll(workDir, 0o755)
    resultFile := fmt.Sprintf("%s/result-%s.txt", workDir, assignment.TaskID)
    content := fmt.Sprintf("Task %s completed.\nMessage: %s\nTime: %s\n",
        assignment.TaskID, assignment.Message, time.Now().Format(time.RFC3339))
    if err := os.WriteFile(resultFile, []byte(content), 0o644); err != nil {
        log.Error("failed to write result", "error", err)
        os.Exit(1)
    }

    log.Info("work complete", "taskID", assignment.TaskID, "resultFile", resultFile)

    // Simulate some processing time
    time.Sleep(2 * time.Second)

    log.Info("task finished successfully", "taskID", assignment.TaskID)
}
```

#### 4. SandboxTemplate Manifest

**File**: `poc/sandbox/manifests/sandbox-template.yaml`

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: poc-runner
  namespace: default
spec:
  podTemplate:
    spec:
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        runAsNonRoot: true
      containers:
      - name: runner
        image: ko.local/nissessenap/shepherd/poc/sandbox/cmd/entrypoint:latest
        ports:
        - containerPort: 8888
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8888
          initialDelaySeconds: 2
          periodSeconds: 5
        resources:
          requests:
            memory: "64Mi"
            cpu: "50m"
          limits:
            memory: "128Mi"
            cpu: "200m"
      restartPolicy: Never
```

Note: The image reference will need to be updated after building with ko. When using Kind, the image needs to be loaded into the Kind cluster.

#### 5. SandboxClaim Manifest

**File**: `poc/sandbox/manifests/sandbox-claim.yaml`

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxClaim
metadata:
  name: poc-test-task
  namespace: default
spec:
  sandboxTemplateRef:
    name: poc-runner
  lifecycle:
    shutdownTime: "2026-02-01T00:00:00Z"  # adjust to current time + 1 hour
    shutdownPolicy: Retain
```

#### 6. Makefile

**File**: `poc/sandbox/Makefile`

```makefile
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
```

#### 7. README

**File**: `poc/sandbox/README.md`

```markdown
# PoC: Agent-Sandbox Entrypoint

Validates the agent-sandbox integration for shepherd.

## Prerequisites

- Kind cluster with agent-sandbox installed (with extensions enabled)
- ko installed
- kubectl configured

## Quick Start

### Phase 1: Manual Validation

1. Build and load image into Kind:
   ```bash
   make kind-load
   ```

2. Update `manifests/sandbox-template.yaml` image to match ko output.

3. Deploy:
   ```bash
   make deploy
   ```

4. Wait for sandbox to be ready:
   ```bash
   make status
   ```

5. Port-forward to test (or exec into a pod in the cluster):
   ```bash
   # Option A: port-forward the headless service
   kubectl port-forward svc/poc-test-task 8888:8888

   # Option B: exec into a debug pod
   kubectl run -it --rm curl --image=curlimages/curl -- sh
   curl http://poc-test-task.default.svc.cluster.local:8888/healthz
   ```

6. Send a task:
   ```bash
   curl -X POST http://localhost:8888/task \
     -H 'Content-Type: application/json' \
     -d '{"taskID":"test-001","message":"Hello from PoC"}'
   ```

7. Check logs:
   ```bash
   make logs
   ```

### Phase 2: Orchestrator

1. Build:
   ```bash
   make build-orchestrator
   ```

2. Run (requires kubeconfig):
   ```bash
   ./bin/orchestrator \
     --template=poc-runner \
     --namespace=default \
     --task-id=orchestrated-001 \
     --message="Hello from orchestrator"
   ```

## Cleanup

```bash
make clean
```
```

### Success Criteria

#### Automated Verification:
- [x] `cd poc/sandbox && go build ./cmd/entrypoint/` compiles successfully
- [x] `cd poc/sandbox && go vet ./cmd/entrypoint/` is clean
- [x] Entrypoint image builds with ko: `make build-entrypoint`
- [ ] Image loads into Kind cluster: `make kind-load`

#### Manual Verification:
- [ ] `make deploy` creates SandboxTemplate and SandboxClaim
- [ ] `make status` shows SandboxClaim Ready=True within 60 seconds
- [ ] Headless Service exists with correct name
- [ ] `curl /healthz` on port 8888 returns 200 OK
- [ ] `POST /task` returns 202 Accepted
- [ ] Pod logs show task execution and completion
- [ ] Pod exits with code 0 after task completes
- [ ] Second `POST /task` returns 409 Conflict (task already assigned)

**Pause for manual review before Phase 2.**

---

## Phase 2: Go Orchestrator

### Overview

Build a small Go program that runs locally (not in a container), creates a SandboxClaim, watches for the Sandbox to become Ready, then POSTs a task to the runner via the headless service FQDN. This simulates what the shepherd operator will do and validates the programmatic flow.

### Changes Required

#### 1. Orchestrator Binary

**File**: `poc/sandbox/cmd/orchestrator/main.go`

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "time"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"

    sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
    extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

    apimeta "k8s.io/apimachinery/pkg/api/meta"
)

var scheme = runtime.NewScheme()

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(sandboxv1alpha1.AddToScheme(scheme))
    utilruntime.Must(extensionsv1alpha1.AddToScheme(scheme))
}

func main() {
    log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    templateName := flag.String("template", "poc-runner", "SandboxTemplate name")
    namespace := flag.String("namespace", "default", "Namespace")
    taskID := flag.String("task-id", "poc-task-001", "Task ID to send")
    message := flag.String("message", "Hello from orchestrator", "Task message")
    timeout := flag.Duration("timeout", 5*time.Minute, "Overall timeout")
    flag.Parse()

    ctx, cancel := context.WithTimeout(context.Background(), *timeout)
    defer cancel()

    // Build K8s client
    cfg := ctrl.GetConfigOrDie()
    k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
    if err != nil {
        log.Error("failed to create k8s client", "error", err)
        os.Exit(1)
    }

    claimName := fmt.Sprintf("poc-%s", *taskID)
    shutdownTime := metav1.NewTime(time.Now().Add(*timeout))

    // Step 1: Create SandboxClaim
    log.Info("creating SandboxClaim", "name", claimName, "template", *templateName)
    claim := &extensionsv1alpha1.SandboxClaim{
        ObjectMeta: metav1.ObjectMeta{
            Name:      claimName,
            Namespace: *namespace,
        },
        Spec: extensionsv1alpha1.SandboxClaimSpec{
            TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
                Name: *templateName,
            },
            Lifecycle: &extensionsv1alpha1.Lifecycle{
                ShutdownTime:   &shutdownTime,
                ShutdownPolicy: extensionsv1alpha1.ShutdownPolicyRetain,
            },
        },
    }

    if err := k8sClient.Create(ctx, claim); err != nil {
        log.Error("failed to create SandboxClaim", "error", err)
        os.Exit(1)
    }
    log.Info("SandboxClaim created", "name", claimName)

    // Step 2: Wait for Sandbox to be Ready
    log.Info("waiting for Sandbox to become Ready...")
    sandboxFQDN, err := waitForSandboxReady(ctx, k8sClient, claimName, *namespace, log)
    if err != nil {
        log.Error("sandbox did not become ready", "error", err)
        os.Exit(1)
    }
    log.Info("sandbox is ready", "fqdn", sandboxFQDN)

    // Step 3: POST task to runner
    log.Info("sending task to runner", "taskID", *taskID)
    if err := sendTask(ctx, sandboxFQDN, *taskID, *message, log); err != nil {
        log.Error("failed to send task", "error", err)
        os.Exit(1)
    }

    log.Info("task sent successfully! Check pod logs for execution output.")

    // Step 4: Optionally wait for pod to complete
    log.Info("waiting for pod to finish...")
    if err := waitForPodCompletion(ctx, k8sClient, claimName, *namespace, log); err != nil {
        log.Error("pod did not complete cleanly", "error", err)
        os.Exit(1)
    }

    log.Info("PoC complete! Sandbox ran task successfully.")
}

func waitForSandboxReady(ctx context.Context, c client.Client, name, ns string, log *slog.Logger) (string, error) {
    ticker := time.NewTicker(2 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return "", ctx.Err()
        case <-ticker.C:
            // Get the Sandbox (created by the claim controller)
            var sandbox sandboxv1alpha1.Sandbox
            if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &sandbox); err != nil {
                log.Info("sandbox not found yet, waiting...")
                continue
            }

            // Check Ready condition
            readyCond := apimeta.FindStatusCondition(sandbox.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
            if readyCond == nil {
                log.Info("sandbox exists but no Ready condition yet")
                continue
            }

            if readyCond.Status == metav1.ConditionTrue {
                return sandbox.Status.ServiceFQDN, nil
            }

            log.Info("sandbox not ready", "reason", readyCond.Reason, "message", readyCond.Message)
        }
    }
}

func sendTask(ctx context.Context, fqdn, taskID, message string, log *slog.Logger) error {
    url := fmt.Sprintf("http://%s:8888/task", fqdn)

    body := map[string]string{
        "taskID":  taskID,
        "message": message,
    }
    jsonBody, err := json.Marshal(body)
    if err != nil {
        return fmt.Errorf("marshaling task: %w", err)
    }

    // Retry a few times — the pod might be ready but the HTTP server
    // hasn't started serving yet
    var lastErr error
    for i := 0; i < 5; i++ {
        req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
        if err != nil {
            return fmt.Errorf("creating request: %w", err)
        }
        req.Header.Set("Content-Type", "application/json")

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            lastErr = err
            log.Info("POST failed, retrying", "attempt", i+1, "error", err)
            time.Sleep(2 * time.Second)
            continue
        }
        resp.Body.Close()

        if resp.StatusCode == http.StatusAccepted {
            log.Info("task accepted by runner", "statusCode", resp.StatusCode)
            return nil
        }

        lastErr = fmt.Errorf("unexpected status: %d", resp.StatusCode)
        log.Info("unexpected response, retrying", "status", resp.StatusCode)
        time.Sleep(2 * time.Second)
    }

    return fmt.Errorf("failed after retries: %w", lastErr)
}

func waitForPodCompletion(ctx context.Context, c client.Client, sandboxName, ns string, log *slog.Logger) error {
    ticker := time.NewTicker(3 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            var sandbox sandboxv1alpha1.Sandbox
            if err := c.Get(ctx, client.ObjectKey{Name: sandboxName, Namespace: ns}, &sandbox); err != nil {
                return fmt.Errorf("getting sandbox: %w", err)
            }

            // When the pod exits, the Ready condition changes
            readyCond := apimeta.FindStatusCondition(sandbox.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
            if readyCond == nil {
                continue
            }

            // If no longer ready and reason is pod-related, the container exited
            if readyCond.Status == metav1.ConditionFalse {
                log.Info("sandbox no longer ready — pod likely completed",
                    "reason", readyCond.Reason, "message", readyCond.Message)
                return nil
            }

            log.Info("pod still running...")
        }
    }
}
```

**Important**: The orchestrator runs locally (not in a container) and needs to reach the headless service inside the cluster. When running against a Kind cluster, the orchestrator cannot directly resolve cluster DNS. Options:
1. **Port-forward**: `kubectl port-forward svc/poc-test-task 8888:8888` and use `localhost:8888` instead of the FQDN
2. **Run inside cluster**: Deploy the orchestrator as a Job/Pod that has access to cluster DNS

The orchestrator code above uses the FQDN. For local development against Kind, we'll add a `--local` flag that uses port-forwarding or overrides the URL.

#### 2. Add Local Mode to Orchestrator

Add this flag and logic to the orchestrator:

```go
localMode := flag.Bool("local", false, "Use kubectl port-forward instead of cluster DNS")
```

When `--local` is set:
- After detecting the sandbox is ready, start a background `kubectl port-forward` process
- Use `localhost:8888` instead of the FQDN for the task POST
- Kill the port-forward process after completion

This is a pragmatic approach for PoC testing without requiring the orchestrator to run inside the cluster.

#### 3. Update Makefile

Add to `poc/sandbox/Makefile`:

```makefile
.PHONY: orchestrate
orchestrate: build-orchestrator ## Run orchestrator with default settings.
	./bin/orchestrator --template=poc-runner --namespace=default --task-id=test-$(shell date +%s) --message="Automated PoC test" --local
```

### Success Criteria

#### Automated Verification:
- [x] `cd poc/sandbox && go build ./cmd/orchestrator/` compiles successfully
- [x] `cd poc/sandbox && go vet ./...` is clean
- [x] `cd poc/sandbox && go test ./...` passes

#### Manual Verification:
- [ ] Orchestrator creates SandboxClaim programmatically
- [ ] Orchestrator detects Sandbox Ready=True
- [ ] Orchestrator successfully POSTs task to runner
- [ ] Pod logs show the task was received and executed
- [ ] Pod exits with code 0
- [ ] Orchestrator detects pod completion and exits cleanly
- [ ] Running orchestrator twice with different task-ids works (creates separate sandboxes)

---

## Learnings to Capture

After completing both phases, document answers to these questions in `thoughts/research/2026-02-XX-poc-sandbox-learnings.md`:

1. **Startup latency**: How long from SandboxClaim creation to pod Ready? This informs warm pool sizing.
2. **DNS resolution timing**: After Ready=True, how quickly can we reach the HTTP server? Is there a gap?
3. **Pod exit behavior**: What happens to the Sandbox/SandboxClaim status when the pod exits cleanly? What about non-zero exit?
4. **Container restartPolicy**: Does `restartPolicy: Never` work correctly with agent-sandbox? Does it try to restart the pod?
5. **Image pull**: What's the experience with ko-built images in Kind? Any gotchas?
6. **Resource cleanup**: After the task completes and the pod exits, what resources remain? Do we need to explicitly delete the SandboxClaim?
7. **Headless service DNS**: How does DNS resolution work for the headless service? Is there a delay between service creation and DNS availability?
8. **Port conflicts**: Any issues with multiple sandboxes using the same containerPort (8888)?
9. **Log access**: Can we easily access pod logs after the container exits? How long are they retained?
10. **Module integration**: Any surprises when importing agent-sandbox types into the main shepherd module (for the real operator migration)?

## Testing Strategy

### Unit Tests
For the PoC, minimal:
- Entrypoint: Test the HTTP handler in isolation (accept task, reject duplicate)
- Orchestrator: No unit tests — it's integration-focused by nature

### Integration Tests
The entire PoC IS the integration test. Manual verification covers the critical path.

## Performance Considerations

- The PoC intentionally uses cold start (no warm pool) to measure baseline startup latency
- The 2-second simulated work time is placeholder — real claude invocations will run for minutes
- Port-forward in local mode adds latency and is not representative of in-cluster performance

## References

- Settled architecture: `thoughts/research/2026-01-30-agent-sandbox-integration.md`
- API server plan: `thoughts/plans/2026-01-28-api-server-implementation.md`
- agent-sandbox examples: `~/go/src/github.com/NissesSenap/agent-sandbox/extensions/examples/`
- agent-sandbox SandboxClaim controller: `~/go/src/github.com/NissesSenap/agent-sandbox/extensions/controllers/sandboxclaim_controller.go`
