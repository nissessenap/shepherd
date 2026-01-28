# Shepherd API Server Implementation Plan

## Overview

Implement the Shepherd REST API server (`shepherd api`) — the component that serves HTTP endpoints for task creation, status queries, and runner callbacks. It creates AgentTask CRDs, watches CRD status changes via a client-go informer, and notifies adapters via HMAC-signed callback URLs. This is the bridge between external adapters (GitHub, future GitLab) and the K8s operator.

## Current State Analysis

The operator MVP is complete (Phases 1-6 of `thoughts/plans/2026-01-27-operator-implementation.md`). The API command exists as a stub in `cmd/shepherd/main.go:42-44` returning "not implemented yet". No `pkg/api/` package exists. The chi router dependency is listed in the design doc but not yet in `go.mod`.

### Key Discoveries:
- `cmd/shepherd/main.go:38-44` — `APICmd` stub with `ListenAddr` field
- `api/v1alpha1/agenttask_types.go` — Full CRD types including `CallbackSpec.SecretRef` (to be removed)
- `api/v1alpha1/conditions.go` — Condition constants (`ConditionSucceeded`, reasons)
- `pkg/operator/operator.go` — Pattern to follow for `pkg/api/` wrapper (scheme, signal handling, Run function)
- `internal/controller/job_builder.go:95` — Runner receives `SHEPHERD_CALLBACK_URL` from `spec.callback.url`
- `go.mod` — Go 1.25.3, controller-runtime v0.23.0, K8s v0.35.0. No chi dependency yet.

## Desired End State

A working API server that:
- Listens on a configurable address (default `:8080`)
- Creates AgentTask CRDs from adapter requests (`POST /api/v1/tasks`)
- Lists/queries tasks with filters (`GET /api/v1/tasks?repo=...&issue=...&active=true`)
- Returns individual task details (`GET /api/v1/tasks/{taskID}`)
- Receives runner progress callbacks (`POST /api/v1/tasks/{taskID}/status`)
- Watches CRD status for terminal states and notifies adapters
- Signs adapter callbacks with HMAC-SHA256 using a shared secret
- Deduplicates terminal notifications using a `Notified` status condition
- Gzip-compresses context before storing in the CRD
- Has comprehensive unit and integration tests

### How to Verify

- `make test` passes all unit and integration tests
- `make build` compiles the API server
- `SHEPHERD_CALLBACK_SECRET=test shepherd api` starts and listens on `:8080`
- `POST /api/v1/tasks` creates an AgentTask CRD in the cluster
- `GET /api/v1/tasks` returns task list with filter support
- `GET /api/v1/tasks/{taskID}` returns task details
- `POST /api/v1/tasks/{taskID}/status` accepts runner callbacks
- Terminal CRD status changes trigger HMAC-signed adapter callbacks
- Duplicate terminal notifications are prevented via `Notified` condition

## What We're NOT Doing

- GitHub adapter (`shepherd github`)
- `shepherd all` mode (deferred)
- Helm charts (separate plan)
- Runner/init container images
- Per-task callback secrets (using shared API-level secret for MVP)
- Rate limiting or authentication on the API endpoints (future — cluster-internal for MVP)
- Pagination on list endpoint (future — MVP returns all matching tasks)
- OpenAPI/Swagger documentation generation

## Implementation Approach

Follow the same pattern as the operator: `pkg/api/` wrapper package with a `Run()` function, wired through Kong CLI. Use chi for HTTP routing, raw client-go for the CRD informer (user preference over controller-runtime for the API server), and the existing K8s client for CRD CRUD operations.

---

## Phase 1: API Scaffold + Chi Router + CRD Cleanup

### Overview

Create the `pkg/api/` package, wire it into the Kong CLI, add the chi dependency, and clean up the CRD types (remove `SecretRef`, add `Notified` condition constant). This phase establishes the skeleton that all subsequent phases build on.

### Changes Required

#### 1. Add Chi Dependency

```bash
go get github.com/go-chi/chi/v5
```

#### 2. CRD Type Cleanup

**File**: `api/v1alpha1/agenttask_types.go`

Remove `SecretRef` from `CallbackSpec`:

```go
type CallbackSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^https?://`
    URL string `json:"url"`
}
```

#### 3. Add Notified Condition Constant

**File**: `api/v1alpha1/conditions.go`

Add:

```go
const (
    // ... existing constants ...

    // ConditionNotified indicates the adapter callback has been sent for a terminal state.
    // Managed by the API server, not the operator.
    ConditionNotified = "Notified"

    // Reasons for ConditionNotified
    ReasonCallbackSent   = "CallbackSent"
    ReasonCallbackFailed = "CallbackFailed"
)
```

#### 4. Regenerate CRD Manifests

```bash
make generate
make manifests
```

#### 5. API Package

**File**: `pkg/api/server.go` (new file)

```go
package api

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "syscall"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(toolkitv1alpha1.AddToScheme(scheme))
}

// Options configures the API server.
type Options struct {
    ListenAddr     string
    CallbackSecret string
}

// Run starts the API server.
func Run(opts Options) error {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    log := ctrl.Log.WithName("api")

    // Build K8s client
    cfg := ctrl.GetConfigOrDie()
    k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
    if err != nil {
        return fmt.Errorf("creating k8s client: %w", err)
    }

    // Build router
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Recoverer)

    // Health endpoints
    r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })
    r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    // API routes (Phases 2-4)
    r.Route("/api/v1", func(r chi.Router) {
        // Phase 2: POST /api/v1/tasks
        // Phase 3: GET /api/v1/tasks, GET /api/v1/tasks/{taskID}
        // Phase 4: POST /api/v1/tasks/{taskID}/status
    })

    srv := &http.Server{
        Addr:    opts.ListenAddr,
        Handler: r,
    }

    // Start server in goroutine
    errCh := make(chan error, 1)
    go func() {
        log.Info("starting API server", "addr", opts.ListenAddr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            errCh <- err
        }
    }()

    // Wait for shutdown signal or error
    select {
    case <-ctx.Done():
        log.Info("shutting down API server")
        shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer shutdownCancel()
        return srv.Shutdown(shutdownCtx)
    case err := <-errCh:
        return fmt.Errorf("server error: %w", err)
    }
}
```

#### 6. Kong CLI Command

**File**: `cmd/shepherd/api.go` (new file, replaces the inline stub in main.go)

```go
package main

import (
    "github.com/NissesSenap/shepherd/pkg/api"
)

type APICmd struct {
    ListenAddr     string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
    CallbackSecret string `help:"HMAC secret for adapter callbacks" env:"SHEPHERD_CALLBACK_SECRET"`
}

func (c *APICmd) Run(_ *CLI) error {
    return api.Run(api.Options{
        ListenAddr:     c.ListenAddr,
        CallbackSecret: c.CallbackSecret,
    })
}
```

**File**: `cmd/shepherd/main.go`

Remove the inline `APICmd` struct and `Run` method (lines 38-44). The `CLI` struct's `API APICmd` field now references the type from `api.go`.

#### 7. Update Existing Tests

The `CallbackSpec.SecretRef` removal will require updating any tests that set `SecretRef`. Check `internal/controller/job_builder_test.go` — the `baseTask()` helper does not set `SecretRef`, so no test changes needed. Run `make test` to confirm.

### Success Criteria

#### Automated Verification:
- [ ] `go get github.com/go-chi/chi/v5` adds chi to go.mod
- [ ] `make generate && make manifests` succeeds after CRD changes
- [ ] `make build` compiles successfully
- [ ] `make test` passes all existing tests (no breakage from SecretRef removal)
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes (golangci-lint)
- [ ] `SHEPHERD_CALLBACK_SECRET=test shepherd api --help` shows correct flags

#### Manual Verification:
- [ ] `SHEPHERD_CALLBACK_SECRET=test shepherd api` starts and responds to `/healthz` with 200
- [ ] Generated CRD YAML no longer contains `secretRef` field

**Pause for manual review before Phase 2.**

---

## Phase 2: Task Creation Endpoint

### Overview

Implement `POST /api/v1/tasks` — the endpoint adapters call to create new AgentTask CRDs. Validates the request, gzip-compresses the context field, generates a unique task name, creates the CRD, and returns the task ID.

### Changes Required

#### 1. Request/Response Types

**File**: `pkg/api/types.go` (new file)

```go
package api

// CreateTaskRequest is the JSON body for POST /api/v1/tasks.
type CreateTaskRequest struct {
    Repo     RepoRequest   `json:"repo"`
    Task     TaskRequest   `json:"task"`
    Callback string        `json:"callbackUrl"`
    Runner   *RunnerConfig `json:"runner,omitempty"`
}

type RepoRequest struct {
    URL string `json:"url"`
    Ref string `json:"ref,omitempty"`
}

type TaskRequest struct {
    Description string `json:"description"`
    Context     string `json:"context,omitempty"`
    ContextURL  string `json:"contextUrl,omitempty"`
}

type RunnerConfig struct {
    Timeout            string `json:"timeout,omitempty"` // e.g. "30m"
    ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// TaskResponse is the JSON response for task endpoints.
type TaskResponse struct {
    ID             string            `json:"id"`
    Namespace      string            `json:"namespace"`
    Repo           RepoRequest       `json:"repo"`
    Task           TaskRequest       `json:"task"`
    CallbackURL    string            `json:"callbackUrl"`
    Status         TaskStatusSummary `json:"status"`
    CreatedAt      string            `json:"createdAt"`
    CompletionTime *string           `json:"completionTime,omitempty"`
}

type TaskStatusSummary struct {
    Phase   string  `json:"phase"`   // Pending, Running, Succeeded, Failed, TimedOut, OOM, Cancelled
    Message string  `json:"message"`
    JobName string  `json:"jobName,omitempty"`
    PRUrl   string  `json:"prUrl,omitempty"`
    Error   string  `json:"error,omitempty"`
}

// ErrorResponse is the standard error response.
type ErrorResponse struct {
    Error   string `json:"error"`
    Details string `json:"details,omitempty"`
}
```

#### 2. Context Compression

**File**: `pkg/api/compress.go` (new file)

```go
package api

import (
    "bytes"
    "compress/gzip"
    "encoding/base64"
    "fmt"
)

// compressContext gzip-compresses the context string and returns base64-encoded result.
// Returns ("", "", nil) if context is empty.
func compressContext(context string) (compressed string, encoding string, err error) {
    if context == "" {
        return "", "", nil
    }

    var buf bytes.Buffer
    gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
    if err != nil {
        return "", "", fmt.Errorf("creating gzip writer: %w", err)
    }
    if _, err := gz.Write([]byte(context)); err != nil {
        return "", "", fmt.Errorf("writing gzip data: %w", err)
    }
    if err := gz.Close(); err != nil {
        return "", "", fmt.Errorf("closing gzip writer: %w", err)
    }

    return base64.StdEncoding.EncodeToString(buf.Bytes()), "gzip", nil
}
```

#### 3. Task Handler

**File**: `pkg/api/handler_tasks.go` (new file)

```go
package api

import (
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/go-chi/chi/v5"
    "k8s.io/apimachinery/pkg/api/errors"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/util/rand"
    "sigs.k8s.io/controller-runtime/pkg/client"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// taskHandler holds dependencies for task endpoints.
type taskHandler struct {
    client    client.Client
    namespace string // default namespace for task creation
}

// createTask handles POST /api/v1/tasks.
func (h *taskHandler) createTask(w http.ResponseWriter, r *http.Request) {
    var req CreateTaskRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
        return
    }

    // Validate required fields
    if req.Repo.URL == "" {
        writeError(w, http.StatusBadRequest, "repo.url is required", "")
        return
    }
    if req.Task.Description == "" {
        writeError(w, http.StatusBadRequest, "task.description is required", "")
        return
    }
    if req.Callback == "" {
        writeError(w, http.StatusBadRequest, "callbackUrl is required", "")
        return
    }

    // Compress context
    compressedCtx, encoding, err := compressContext(req.Task.Context)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "failed to compress context", err.Error())
        return
    }

    // Build the context field — if no context was provided, use an empty
    // placeholder so the CRD validation (MinLength=1) still passes.
    // The init container will see the empty file and handle it.
    contextValue := compressedCtx
    if contextValue == "" {
        contextValue = "-" // minimal placeholder for required field
    }

    // Generate task name
    taskName := fmt.Sprintf("task-%s", rand.String(8))

    // Build runner spec
    runnerSpec := toolkitv1alpha1.RunnerSpec{}
    if req.Runner != nil {
        if req.Runner.Timeout != "" {
            d, err := time.ParseDuration(req.Runner.Timeout)
            if err != nil {
                writeError(w, http.StatusBadRequest, "invalid runner.timeout", err.Error())
                return
            }
            runnerSpec.Timeout = metav1.Duration{Duration: d}
        }
        runnerSpec.ServiceAccountName = req.Runner.ServiceAccountName
    }

    // Build labels for deduplication queries
    labels := map[string]string{}
    // Extract repo identifier for label (org/repo from URL)
    // Labels are set by the adapter if needed — the API doesn't parse GitHub URLs

    // Create AgentTask CRD
    task := &toolkitv1alpha1.AgentTask{
        ObjectMeta: metav1.ObjectMeta{
            Name:      taskName,
            Namespace: h.namespace,
            Labels:    labels,
        },
        Spec: toolkitv1alpha1.AgentTaskSpec{
            Repo: toolkitv1alpha1.RepoSpec{
                URL: req.Repo.URL,
                Ref: req.Repo.Ref,
            },
            Task: toolkitv1alpha1.TaskSpec{
                Description:     req.Task.Description,
                Context:         contextValue,
                ContextEncoding: encoding,
                ContextURL:      req.Task.ContextURL,
            },
            Callback: toolkitv1alpha1.CallbackSpec{
                URL: req.Callback,
            },
            Runner: runnerSpec,
        },
    }

    if err := h.client.Create(r.Context(), task); err != nil {
        if errors.IsAlreadyExists(err) {
            writeError(w, http.StatusConflict, "task already exists", err.Error())
            return
        }
        writeError(w, http.StatusInternalServerError, "failed to create task", err.Error())
        return
    }

    resp := taskToResponse(task)
    writeJSON(w, http.StatusCreated, resp)
}

// Helper functions

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg, details string) {
    writeJSON(w, status, ErrorResponse{Error: msg, Details: details})
}

func taskToResponse(task *toolkitv1alpha1.AgentTask) TaskResponse {
    resp := TaskResponse{
        ID:        task.Name,
        Namespace: task.Namespace,
        Repo: RepoRequest{
            URL: task.Spec.Repo.URL,
            Ref: task.Spec.Repo.Ref,
        },
        Task: TaskRequest{
            Description: task.Spec.Task.Description,
            ContextURL:  task.Spec.Task.ContextURL,
        },
        CallbackURL: task.Spec.Callback.URL,
        Status:      extractStatus(task),
        CreatedAt:   task.CreationTimestamp.UTC().Format(time.RFC3339),
    }
    if task.Status.CompletionTime != nil {
        ct := task.Status.CompletionTime.UTC().Format(time.RFC3339)
        resp.CompletionTime = &ct
    }
    return resp
}

func extractStatus(task *toolkitv1alpha1.AgentTask) TaskStatusSummary {
    cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    phase := "Pending"
    message := ""
    if cond != nil {
        phase = cond.Reason
        message = cond.Message
    }
    return TaskStatusSummary{
        Phase:   phase,
        Message: message,
        JobName: task.Status.JobName,
        PRUrl:   task.Status.Result.PRUrl,
        Error:   task.Status.Result.Error,
    }
}
```

#### 4. Wire Routes into Server

**File**: `pkg/api/server.go`

Update the `Run` function to create the handler and register routes:

```go
handler := &taskHandler{
    client:    k8sClient,
    namespace: opts.Namespace,
}

r.Route("/api/v1", func(r chi.Router) {
    r.Post("/tasks", handler.createTask)
})
```

#### 5. Add Namespace Option

**File**: `pkg/api/server.go` — add `Namespace` to `Options`:

```go
type Options struct {
    ListenAddr     string
    CallbackSecret string
    Namespace      string // default namespace for task creation
}
```

**File**: `cmd/shepherd/api.go` — add namespace flag:

```go
type APICmd struct {
    ListenAddr     string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
    CallbackSecret string `help:"HMAC secret for adapter callbacks" env:"SHEPHERD_CALLBACK_SECRET"`
    Namespace      string `help:"Namespace for task creation" default:"shepherd" env:"SHEPHERD_NAMESPACE"`
}
```

#### 6. Unit Tests

**File**: `pkg/api/compress_test.go` (new file)

```go
// Test: Empty context returns empty string and empty encoding
// Test: Non-empty context returns base64-encoded gzip data with encoding "gzip"
// Test: Compressed data can be decompressed back to original
// Test: Large context (1MB) compresses successfully
```

**File**: `pkg/api/handler_tasks_test.go` (new file)

Use `httptest` and a fake K8s client:

```go
// Test: Valid request creates AgentTask CRD and returns 201
// Test: Missing repo.url returns 400
// Test: Missing task.description returns 400
// Test: Missing callbackUrl returns 400
// Test: Context is gzip-compressed in CRD
// Test: Empty context uses placeholder
// Test: Runner timeout parsed correctly
// Test: Invalid runner timeout returns 400
// Test: Task name is generated with random suffix
// Test: Response includes correct task ID and status
// Test: K8s client error returns 500
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests (existing + new)
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes (golangci-lint)
- [ ] Unit tests cover validation, compression, CRD creation, error cases
- [ ] `curl -X POST localhost:8080/api/v1/tasks -d '{"repo":{"url":"https://github.com/test/repo"},"task":{"description":"test"},"callbackUrl":"http://localhost/cb"}' -H 'Content-Type: application/json'` returns 201 (requires running API with kubeconfig)

#### Manual Verification:
- [ ] Created CRD in cluster has gzip-compressed context
- [ ] CRD name follows `task-{random}` pattern
- [ ] Response JSON matches `TaskResponse` schema

**Pause for manual review before Phase 3.**

---

## Phase 3: Task Query Endpoints

### Overview

Implement `GET /api/v1/tasks` (list with filters) and `GET /api/v1/tasks/{taskID}` (single task detail). Supports the deduplication query pattern: `GET /api/v1/tasks?repo=...&issue=...&active=true`.

### Changes Required

#### 1. List Tasks Handler

**File**: `pkg/api/handler_tasks.go`

```go
// listTasks handles GET /api/v1/tasks.
// Query parameters:
//   - repo: filter by shepherd.io/repo label
//   - issue: filter by shepherd.io/issue label
//   - active: if "true", only return tasks with Succeeded=Unknown
func (h *taskHandler) listTasks(w http.ResponseWriter, r *http.Request) {
    var taskList toolkitv1alpha1.AgentTaskList

    listOpts := []client.ListOption{
        client.InNamespace(h.namespace),
    }

    // Build label selector from query params
    labelSelector := map[string]string{}
    if repo := r.URL.Query().Get("repo"); repo != "" {
        labelSelector["shepherd.io/repo"] = repo
    }
    if issue := r.URL.Query().Get("issue"); issue != "" {
        labelSelector["shepherd.io/issue"] = issue
    }
    if len(labelSelector) > 0 {
        listOpts = append(listOpts, client.MatchingLabels(labelSelector))
    }

    if err := h.client.List(r.Context(), &taskList, listOpts...); err != nil {
        writeError(w, http.StatusInternalServerError, "failed to list tasks", err.Error())
        return
    }

    // Filter active tasks in-memory if requested
    active := r.URL.Query().Get("active") == "true"

    var tasks []TaskResponse
    for i := range taskList.Items {
        task := &taskList.Items[i]
        if active && isTerminalFromStatus(task) {
            continue
        }
        tasks = append(tasks, taskToResponse(task))
    }

    if tasks == nil {
        tasks = []TaskResponse{} // return empty array, not null
    }

    writeJSON(w, http.StatusOK, tasks)
}

// isTerminalFromStatus checks if a task has reached a terminal condition.
func isTerminalFromStatus(task *toolkitv1alpha1.AgentTask) bool {
    cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    if cond == nil {
        return false
    }
    return cond.Status != metav1.ConditionUnknown
}
```

#### 2. Get Task Handler

```go
// getTask handles GET /api/v1/tasks/{taskID}.
func (h *taskHandler) getTask(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "taskID")

    var task toolkitv1alpha1.AgentTask
    key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
    if err := h.client.Get(r.Context(), key, &task); err != nil {
        if errors.IsNotFound(err) {
            writeError(w, http.StatusNotFound, "task not found", "")
            return
        }
        writeError(w, http.StatusInternalServerError, "failed to get task", err.Error())
        return
    }

    writeJSON(w, http.StatusOK, taskToResponse(&task))
}
```

#### 3. Wire Routes

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Post("/tasks", handler.createTask)
    r.Get("/tasks", handler.listTasks)
    r.Get("/tasks/{taskID}", handler.getTask)
})
```

#### 4. Support Adapter Labels on Task Creation

Update `createTask` to accept optional labels from the adapter:

```go
type CreateTaskRequest struct {
    // ... existing fields ...
    Labels map[string]string `json:"labels,omitempty"` // e.g. {"shepherd.io/repo": "org-repo", "shepherd.io/issue": "123"}
}
```

The API passes these labels through to the CRD. The adapter is responsible for setting the correct label values. The API does not parse repo URLs.

#### 5. Unit Tests

**File**: `pkg/api/handler_tasks_test.go` (extend)

```go
// Test: GET /api/v1/tasks returns empty array when no tasks exist
// Test: GET /api/v1/tasks returns all tasks
// Test: GET /api/v1/tasks?repo=org-repo filters by label
// Test: GET /api/v1/tasks?active=true excludes terminal tasks
// Test: GET /api/v1/tasks?repo=x&issue=123&active=true combines filters
// Test: GET /api/v1/tasks/{taskID} returns task details
// Test: GET /api/v1/tasks/{taskID} returns 404 for nonexistent task
// Test: Labels from CreateTaskRequest are applied to CRD
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes (golangci-lint)
- [ ] Unit tests cover list, filter, detail, and error cases
- [ ] Active filter correctly excludes Succeeded/Failed tasks

#### Manual Verification:
- [ ] `GET /api/v1/tasks` returns JSON array of tasks
- [ ] `GET /api/v1/tasks/{id}` returns task detail with status
- [ ] Label-based filtering works with `?repo=...&issue=...`

**Pause for manual review before Phase 4.**

---

## Phase 4: Runner Callback + Adapter Notification

### Overview

Implement `POST /api/v1/tasks/{taskID}/status` for runner progress callbacks, and the callback sender that forwards notifications to adapters with HMAC-SHA256 signatures. The runner POSTs progress updates to the API; the API updates the CRD and forwards to the adapter's callback URL.

### Changes Required

#### 1. Callback Sender

**File**: `pkg/api/callback.go` (new file)

```go
package api

import (
    "bytes"
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

// CallbackPayload is the JSON body sent to adapters.
type CallbackPayload struct {
    TaskID  string                 `json:"taskId"`
    Event   string                 `json:"event"` // started, progress, completed, failed
    Message string                 `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
}

// callbackSender sends HMAC-signed callbacks to adapters.
type callbackSender struct {
    secret     string
    httpClient *http.Client
}

func newCallbackSender(secret string) *callbackSender {
    return &callbackSender{
        secret: secret,
        httpClient: &http.Client{
            Timeout: 10 * time.Second,
        },
    }
}

// send POSTs a callback payload to the given URL with HMAC-SHA256 signature.
func (s *callbackSender) send(ctx context.Context, url string, payload CallbackPayload) error {
    body, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("marshaling callback payload: %w", err)
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("creating callback request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    // HMAC-SHA256 signature
    if s.secret != "" {
        mac := hmac.New(sha256.New, []byte(s.secret))
        mac.Write(body)
        sig := hex.EncodeToString(mac.Sum(nil))
        req.Header.Set("X-Shepherd-Signature", "sha256="+sig)
    }

    resp, err := s.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("sending callback to %s: %w", url, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 300 {
        return fmt.Errorf("callback to %s returned status %d", url, resp.StatusCode)
    }

    return nil
}
```

#### 2. Runner Status Callback Handler

**File**: `pkg/api/handler_status.go` (new file)

```go
package api

// StatusUpdateRequest is the JSON body from the runner.
type StatusUpdateRequest struct {
    Event   string                 `json:"event"` // started, progress, completed, failed
    Message string                 `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
}

// updateTaskStatus handles POST /api/v1/tasks/{taskID}/status.
func (h *taskHandler) updateTaskStatus(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "taskID")

    var req StatusUpdateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
        return
    }

    if req.Event == "" {
        writeError(w, http.StatusBadRequest, "event is required", "")
        return
    }

    // Fetch the task
    var task toolkitv1alpha1.AgentTask
    key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
    if err := h.client.Get(r.Context(), key, &task); err != nil {
        if errors.IsNotFound(err) {
            writeError(w, http.StatusNotFound, "task not found", "")
            return
        }
        writeError(w, http.StatusInternalServerError, "failed to get task", err.Error())
        return
    }

    // Update CRD status based on event
    updated := false
    switch req.Event {
    case "completed":
        if prURL, ok := req.Details["pr_url"].(string); ok {
            task.Status.Result.PRUrl = prURL
            updated = true
        }
    case "failed":
        if errMsg, ok := req.Details["error"].(string); ok {
            task.Status.Result.Error = errMsg
            updated = true
        }
    }

    if updated {
        if err := h.client.Status().Update(r.Context(), &task); err != nil {
            writeError(w, http.StatusInternalServerError, "failed to update task status", err.Error())
            return
        }
    }

    // Forward callback to adapter
    callbackURL := task.Spec.Callback.URL
    payload := CallbackPayload{
        TaskID:  taskID,
        Event:   req.Event,
        Message: req.Message,
        Details: req.Details,
    }

    // For terminal events, check dedup and set Notified condition
    isTerminal := req.Event == "completed" || req.Event == "failed"
    if isTerminal {
        notifiedCond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionNotified)
        if notifiedCond != nil && notifiedCond.Status == metav1.ConditionTrue {
            // Already notified — skip callback, return success
            w.WriteHeader(http.StatusOK)
            writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "note": "already notified"})
            return
        }
    }

    if err := h.callback.send(r.Context(), callbackURL, payload); err != nil {
        log.Error(err, "failed to send adapter callback", "taskID", taskID, "callbackURL", callbackURL)
        // Don't fail the request — the runner callback was accepted
    }

    // Set Notified condition for terminal events
    if isTerminal {
        meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
            Type:               toolkitv1alpha1.ConditionNotified,
            Status:             metav1.ConditionTrue,
            Reason:             toolkitv1alpha1.ReasonCallbackSent,
            Message:            fmt.Sprintf("Adapter notified: %s", req.Event),
            ObservedGeneration: task.Generation,
        })
        if err := h.client.Status().Update(r.Context(), &task); err != nil {
            log.Error(err, "failed to set Notified condition", "taskID", taskID)
        }
    }

    writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}
```

#### 3. Add Callback Sender to Handler

Update `taskHandler`:

```go
type taskHandler struct {
    client    client.Client
    namespace string
    callback  *callbackSender
}
```

Wire in `Run()`:

```go
cb := newCallbackSender(opts.CallbackSecret)
handler := &taskHandler{
    client:    k8sClient,
    namespace: opts.Namespace,
    callback:  cb,
}
```

#### 4. Wire Route

```go
r.Post("/tasks/{taskID}/status", handler.updateTaskStatus)
```

#### 5. Unit Tests

**File**: `pkg/api/callback_test.go` (new file)

```go
// Test: HMAC signature is correct (verify with known input/output)
// Test: Empty secret skips signature header
// Test: HTTP client timeout is respected
// Test: Non-2xx response returns error
// Test: Network error returns error
```

**File**: `pkg/api/handler_status_test.go` (new file)

```go
// Test: Valid "started" event forwards to adapter and returns 200
// Test: Valid "completed" event with pr_url updates CRD result and forwards
// Test: Valid "failed" event with error updates CRD result and forwards
// Test: Missing event field returns 400
// Test: Nonexistent taskID returns 404
// Test: Terminal event sets Notified condition on CRD
// Test: Already-notified terminal event skips duplicate callback
// Test: Adapter callback failure doesn't fail the runner's request
// Test: "progress" event forwards without updating CRD status
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes (golangci-lint)
- [ ] HMAC signature matches expected output for known input
- [ ] Runner callback accepted even if adapter callback fails
- [ ] Notified condition set after terminal callback
- [ ] Duplicate terminal callback is deduplicated

#### Manual Verification:
- [ ] `POST /api/v1/tasks/{id}/status` with `{"event":"started","message":"cloning"}` returns 200
- [ ] Adapter receives callback with `X-Shepherd-Signature` header
- [ ] CRD status updated with PR URL on completed event

**Pause for manual review before Phase 5.**

---

## Phase 5: CRD Status Watcher

### Overview

Implement the CRD status informer using raw client-go. The API watches AgentTask resources for terminal conditions (`Succeeded=True` or `Succeeded=False`). When detected, it sends an adapter callback and sets the `Notified` condition to prevent duplicates. This is the safety net path (design doc step 11) that ensures adapters are notified even if the runner crashes.

### Changes Required

#### 1. Status Watcher

**File**: `pkg/api/watcher.go` (new file)

```go
package api

import (
    "context"
    "fmt"
    "time"

    "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/watch"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/dynamic/dynamicinformer"
    "k8s.io/client-go/tools/cache"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// statusWatcher watches AgentTask resources for terminal states
// and sends adapter callbacks.
type statusWatcher struct {
    client   client.Client
    callback *callbackSender
    scheme   *runtime.Scheme
}

// run starts the informer and blocks until the context is cancelled.
func (w *statusWatcher) run(ctx context.Context, dynamicClient dynamic.Interface, namespace string) error {
    log := ctrl.Log.WithName("status-watcher")

    gvr := toolkitv1alpha1.GroupVersion.WithResource("agenttasks")

    factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
        dynamicClient,
        30*time.Minute, // resync period
        namespace,
        nil,
    )

    informer := factory.ForResource(gvr).Informer()

    informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        UpdateFunc: func(oldObj, newObj interface{}) {
            w.handleUpdate(ctx, oldObj, newObj)
        },
    })

    log.Info("starting CRD status watcher", "namespace", namespace)
    informer.Run(ctx.Done())
    return nil
}

func (w *statusWatcher) handleUpdate(ctx context.Context, oldObj, newObj interface{}) {
    log := ctrl.Log.WithName("status-watcher")

    // Convert unstructured to AgentTask
    newTask, err := w.toAgentTask(newObj)
    if err != nil {
        log.Error(err, "failed to convert new object to AgentTask")
        return
    }

    // Check if task just became terminal
    succeededCond := findCondition(newTask.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    if succeededCond == nil || succeededCond.Status == metav1.ConditionUnknown {
        return // not terminal
    }

    // Check if already notified
    notifiedCond := findCondition(newTask.Status.Conditions, toolkitv1alpha1.ConditionNotified)
    if notifiedCond != nil && notifiedCond.Status == metav1.ConditionTrue {
        return // already notified
    }

    // Determine event type
    event := "failed"
    if succeededCond.Status == metav1.ConditionTrue {
        event = "completed"
    }

    // Build callback payload
    payload := CallbackPayload{
        TaskID:  newTask.Name,
        Event:   event,
        Message: succeededCond.Message,
        Details: map[string]interface{}{},
    }
    if newTask.Status.Result.PRUrl != "" {
        payload.Details["pr_url"] = newTask.Status.Result.PRUrl
    }
    if newTask.Status.Result.Error != "" {
        payload.Details["error"] = newTask.Status.Result.Error
    }

    // Send callback
    callbackURL := newTask.Spec.Callback.URL
    if err := w.callback.send(ctx, callbackURL, payload); err != nil {
        log.Error(err, "failed to send terminal callback",
            "task", newTask.Name, "event", event, "callbackURL", callbackURL)

        // Set Notified condition as failed
        w.setNotifiedCondition(ctx, newTask, toolkitv1alpha1.ReasonCallbackFailed,
            fmt.Sprintf("Callback failed: %v", err))
        return
    }

    log.Info("sent terminal callback to adapter",
        "task", newTask.Name, "event", event, "callbackURL", callbackURL)

    // Set Notified condition as sent
    w.setNotifiedCondition(ctx, newTask, toolkitv1alpha1.ReasonCallbackSent,
        fmt.Sprintf("Adapter notified: %s", event))
}

func (w *statusWatcher) setNotifiedCondition(ctx context.Context, task *toolkitv1alpha1.AgentTask, reason, message string) {
    log := ctrl.Log.WithName("status-watcher")

    // Re-fetch to avoid conflicts
    var fresh toolkitv1alpha1.AgentTask
    if err := w.client.Get(ctx, client.ObjectKeyFromObject(task), &fresh); err != nil {
        log.Error(err, "failed to re-fetch task for Notified condition", "task", task.Name)
        return
    }

    statusMeta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
        Type:               toolkitv1alpha1.ConditionNotified,
        Status:             metav1.ConditionTrue,
        Reason:             reason,
        Message:            message,
        ObservedGeneration: fresh.Generation,
    })

    if err := w.client.Status().Update(ctx, &fresh); err != nil {
        log.Error(err, "failed to set Notified condition", "task", task.Name)
    }
}

func (w *statusWatcher) toAgentTask(obj interface{}) (*toolkitv1alpha1.AgentTask, error) {
    unstructured, ok := obj.(*unstructured.Unstructured)
    if !ok {
        return nil, fmt.Errorf("expected *unstructured.Unstructured, got %T", obj)
    }

    var task toolkitv1alpha1.AgentTask
    if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructured.Object, &task); err != nil {
        return nil, fmt.Errorf("converting unstructured to AgentTask: %w", err)
    }
    return &task, nil
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
    for i := range conditions {
        if conditions[i].Type == condType {
            return &conditions[i]
        }
    }
    return nil
}
```

#### 2. Integrate Watcher into Server

**File**: `pkg/api/server.go`

Update `Run()` to start the watcher in a goroutine alongside the HTTP server:

```go
func Run(opts Options) error {
    // ... existing setup ...

    // Build dynamic client for informer
    dynamicClient, err := dynamic.NewForConfig(cfg)
    if err != nil {
        return fmt.Errorf("creating dynamic client: %w", err)
    }

    // Start CRD status watcher
    watcher := &statusWatcher{
        client:   k8sClient,
        callback: cb,
        scheme:   scheme,
    }

    go func() {
        if err := watcher.run(ctx, dynamicClient, opts.Namespace); err != nil {
            log.Error(err, "status watcher failed")
        }
    }()

    // ... existing HTTP server start ...
}
```

#### 3. RBAC for API Server

The API server needs its own ServiceAccount and RBAC. Create a kustomize overlay or document the required permissions:

**Required RBAC for the API server's ServiceAccount:**
```yaml
rules:
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks"]
    verbs: ["get", "list", "watch", "create"]
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks/status"]
    verbs: ["get", "update", "patch"]
```

**File**: `config/api-rbac/role.yaml` (new file)

This is separate from the operator's RBAC. The API server does NOT need Job or Pod permissions.

#### 4. Unit Tests

**File**: `pkg/api/watcher_test.go` (new file)

```go
// Test: Terminal Succeeded=True triggers adapter callback
// Test: Terminal Succeeded=False triggers adapter callback with error details
// Test: Non-terminal update (Succeeded=Unknown) does not trigger callback
// Test: Already-notified task does not trigger duplicate callback
// Test: Callback failure sets Notified condition with CallbackFailed reason
// Test: Callback success sets Notified condition with CallbackSent reason
// Test: PR URL included in callback details when present
// Test: Error message included in callback details when present
```

**File**: `pkg/api/server_test.go` (new file)

```go
// Test: Server starts and responds to /healthz
// Test: Server shuts down gracefully on context cancellation
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes (golangci-lint)
- [ ] Watcher correctly detects terminal state transitions
- [ ] Notified condition prevents duplicate callbacks
- [ ] Callback failure is logged but doesn't crash the watcher
- [ ] RBAC YAML is generated/created for API server

#### Manual Verification:
- [ ] Create an AgentTask CRD manually, simulate Job completion → API sends callback to adapter
- [ ] Verify `Notified=True` condition appears on the CRD after callback
- [ ] Verify no duplicate callback when runner also reports completion
- [ ] Verify callback includes `X-Shepherd-Signature` header

---

## Testing Strategy

### Unit Tests (testify + httptest)
- Request/response serialization
- Validation: missing fields, invalid values
- Context compression: empty, normal, large
- HMAC signature: correctness, empty secret
- Callback sender: success, failure, timeout
- Handler logic: create, list, get, status update
- Watcher: terminal detection, dedup, condition setting

### Integration Tests (envtest or httptest with fake client)
- Full create → get → list lifecycle
- Active filter excludes terminal tasks
- Runner callback → adapter notification → Notified condition
- Status watcher → terminal detection → adapter notification
- Dedup: both paths fire, only one callback sent

### Test Patterns
- Use `sigs.k8s.io/controller-runtime/pkg/client/fake` for unit tests that need a K8s client
- Use `net/http/httptest` for HTTP handler tests
- Use `httptest.NewServer` for callback sender tests (mock adapter)
- Table-driven tests with testify for validation cases

## Performance Considerations

- Client-go informer caches AgentTask objects locally — no per-request API server calls for the watcher
- The informer's resync period (30 minutes) ensures eventual consistency if events are missed
- Callback sender has a 10-second timeout to avoid blocking the handler
- Adapter callback failures are logged but don't block runner callbacks

## Security Considerations

- HMAC-SHA256 signing prevents callback spoofing
- The API server runs inside the K8s cluster with its own ServiceAccount
- No authentication on API endpoints for MVP (cluster-internal networking)
- Input validation prevents invalid CRD creation
- Context compression reduces etcd storage footprint

## References

- Design doc: `docs/research/2026-01-27-shepherd-design.md`
- Operator plan: `thoughts/plans/2026-01-27-operator-implementation.md`
- OOM research: `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md`
- chi router: `https://github.com/go-chi/chi`
- K8s dynamic informer: `https://pkg.go.dev/k8s.io/client-go/dynamic/dynamicinformer`
