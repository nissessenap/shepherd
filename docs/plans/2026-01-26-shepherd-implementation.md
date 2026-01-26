# Shepherd MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Kubernetes-native background coding agent orchestrator that receives tasks via GitHub webhooks and runs Claude Code in isolated K8s jobs.

**Architecture:** Single Go binary with multiple targets (api, operator, github-adapter, all) using the Grafana dskit module pattern. Uses controller-runtime for K8s operator logic. GitHub App authentication via ghinstallation library.

**Tech Stack:** Go 1.25+, controller-runtime, dskit, ghinstallation, go-github, chi (HTTP router)

---

## Phase 1: Project Foundation

### Task 1: Initialize Go Module and Project Structure

**Files:**

- Create: `go.mod`
- Create: `cmd/shepherd/main.go`
- Create: `pkg/shepherd/config.go`
- Create: `pkg/shepherd/shepherd.go`

**Step 1: Initialize Go module**

Run: `go mod init github.com/NissesSenap/shepherd`
Expected: `go.mod` created

**Step 2: Create directory structure**

Run: `mkdir -p cmd/shepherd pkg/shepherd pkg/api pkg/operator pkg/adapters/github pkg/types api/v1alpha1 internal/controller config/crd/bases config/rbac config/manager config/samples images/runner images/init`
Expected: Directories created

**Step 3: Create minimal main.go**

```go
// cmd/shepherd/main.go
package main

import (
 "context"
 "flag"
 "fmt"
 "os"
 "os/signal"
 "syscall"

 "github.com/NissesSenap/shepherd/pkg/shepherd"
)

func main() {
 var cfg shepherd.Config
 cfg.RegisterFlags(flag.CommandLine)
 flag.Parse()

 if err := cfg.Validate(); err != nil {
  fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
  os.Exit(1)
 }

 ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
 defer cancel()

 s, err := shepherd.New(cfg)
 if err != nil {
  fmt.Fprintf(os.Stderr, "failed to create shepherd: %v\n", err)
  os.Exit(1)
 }

 if err := s.Run(ctx); err != nil {
  fmt.Fprintf(os.Stderr, "shepherd error: %v\n", err)
  os.Exit(1)
 }
}
```

**Step 4: Create config.go**

```go
// pkg/shepherd/config.go
package shepherd

import (
 "errors"
 "flag"
)

// Target constants for single-binary multi-target pattern
const (
 TargetAll           = "all"
 TargetAPI           = "api"
 TargetOperator      = "operator"
 TargetGitHubAdapter = "github-adapter"
)

// Config holds all configuration for Shepherd
type Config struct {
 Target string

 // API configuration
 APIListenAddr string

 // Operator configuration
 MetricsAddr         string
 HealthProbeAddr     string
 LeaderElection      bool
 LeaderElectionID    string

 // GitHub Adapter configuration
 GitHubWebhookSecret string
 GitHubAppID         int64
 GitHubPrivateKey    string
}

// RegisterFlags registers configuration flags
func (c *Config) RegisterFlags(f *flag.FlagSet) {
 f.StringVar(&c.Target, "target", TargetAll, "Component to run: all, api, operator, github-adapter")
 f.StringVar(&c.APIListenAddr, "api.listen-addr", ":8080", "API server listen address")
 f.StringVar(&c.MetricsAddr, "metrics.addr", ":9090", "Metrics server address")
 f.StringVar(&c.HealthProbeAddr, "health.addr", ":8081", "Health probe address")
 f.BoolVar(&c.LeaderElection, "leader-election", false, "Enable leader election")
 f.StringVar(&c.LeaderElectionID, "leader-election-id", "shepherd-operator", "Leader election ID")
 f.StringVar(&c.GitHubWebhookSecret, "github.webhook-secret", "", "GitHub webhook secret")
 f.Int64Var(&c.GitHubAppID, "github.app-id", 0, "GitHub App ID")
 f.StringVar(&c.GitHubPrivateKey, "github.private-key", "", "Path to GitHub App private key")
}

// Validate validates the configuration
func (c *Config) Validate() error {
 switch c.Target {
 case TargetAll, TargetAPI, TargetOperator, TargetGitHubAdapter:
  // valid
 default:
  return errors.New("invalid target: must be one of all, api, operator, github-adapter")
 }
 return nil
}
```

**Step 5: Create shepherd.go (module orchestrator)**

```go
// pkg/shepherd/shepherd.go
package shepherd

import (
 "context"
 "fmt"

 "golang.org/x/sync/errgroup"
)

// Module represents a runnable component
type Module interface {
 Name() string
 Run(ctx context.Context) error
}

// Shepherd orchestrates all modules
type Shepherd struct {
 cfg     Config
 modules []Module
}

// New creates a new Shepherd instance
func New(cfg Config) (*Shepherd, error) {
 s := &Shepherd{cfg: cfg}

 if err := s.initModules(); err != nil {
  return nil, fmt.Errorf("init modules: %w", err)
 }

 return s, nil
}

func (s *Shepherd) initModules() error {
 switch s.cfg.Target {
 case TargetAll:
  // Initialize all modules
  // TODO: Add modules as they are implemented
 case TargetAPI:
  // TODO: Initialize API module
 case TargetOperator:
  // TODO: Initialize Operator module
 case TargetGitHubAdapter:
  // TODO: Initialize GitHub Adapter module
 }
 return nil
}

// Run starts all modules and blocks until context is cancelled
func (s *Shepherd) Run(ctx context.Context) error {
 if len(s.modules) == 0 {
  fmt.Println("No modules to run for target:", s.cfg.Target)
  <-ctx.Done()
  return nil
 }

 g, ctx := errgroup.WithContext(ctx)

 for _, m := range s.modules {
  m := m // capture for goroutine
  g.Go(func() error {
   fmt.Printf("Starting module: %s\n", m.Name())
   return m.Run(ctx)
  })
 }

 return g.Wait()
}
```

**Step 6: Add dependencies**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go mod tidy`
Expected: Dependencies resolved

**Step 7: Verify it compiles**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go build ./cmd/shepherd`
Expected: Binary created with no errors

**Step 8: Commit**

```bash
git add -A
git commit -m "feat: initialize project structure with multi-target pattern

- Add cmd/shepherd entrypoint
- Add pkg/shepherd with config and module orchestration
- Implement Loki/Mimir style single-binary multi-target pattern
- Support targets: all, api, operator, github-adapter

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 2: Define AgentTask CRD Types

**Files:**

- Create: `api/v1alpha1/groupversion_info.go`
- Create: `api/v1alpha1/agenttask_types.go`
- Create: `api/v1alpha1/zz_generated.deepcopy.go` (generated)

**Step 1: Create groupversion_info.go**

```go
// api/v1alpha1/groupversion_info.go
package v1alpha1

import (
 "k8s.io/apimachinery/pkg/runtime/schema"
 "sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
 // GroupVersion is group version used to register these objects
 GroupVersion = schema.GroupVersion{Group: "shepherd.io", Version: "v1alpha1"}

 // SchemeBuilder is used to add go types to the GroupVersionKind scheme
 SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

 // AddToScheme adds the types in this group-version to the given scheme
 AddToScheme = SchemeBuilder.AddToScheme
)
```

**Step 2: Create agenttask_types.go**

```go
// api/v1alpha1/agenttask_types.go
package v1alpha1

import (
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepoSpec defines the repository to work on
type RepoSpec struct {
 // URL is the git repository URL
 // +kubebuilder:validation:Required
 // +kubebuilder:validation:Pattern=`^https://`
 URL string `json:"url"`
}

// TaskSpec defines the task to perform
type TaskSpec struct {
 // Description is the task description for the AI agent
 // +kubebuilder:validation:Required
 // +kubebuilder:validation:MinLength=1
 Description string `json:"description"`

 // Context provides additional context (issue body, comments, etc.)
 // +optional
 Context string `json:"context,omitempty"`
}

// CallbackSpec defines where to send status updates
type CallbackSpec struct {
 // URL is the callback endpoint
 // +kubebuilder:validation:Required
 // +kubebuilder:validation:Pattern=`^https?://`
 URL string `json:"url"`

 // SecretRef references a secret containing the callback authentication
 // +optional
 SecretRef string `json:"secretRef,omitempty"`
}

// RunnerSpec defines the runner configuration
type RunnerSpec struct {
 // Image is the runner container image (must be pre-approved)
 // +kubebuilder:default="shepherd-runner:latest"
 Image string `json:"image,omitempty"`

 // Timeout is the maximum duration for the task
 // +kubebuilder:default="30m"
 Timeout metav1.Duration `json:"timeout,omitempty"`
}

// AgentTaskSpec defines the desired state of AgentTask
type AgentTaskSpec struct {
 // Repo specifies the repository to work on
 // +kubebuilder:validation:Required
 Repo RepoSpec `json:"repo"`

 // Task specifies what the agent should do
 // +kubebuilder:validation:Required
 Task TaskSpec `json:"task"`

 // Callback specifies where to send status updates
 // +kubebuilder:validation:Required
 Callback CallbackSpec `json:"callback"`

 // Runner configures the runner job
 // +optional
 Runner RunnerSpec `json:"runner,omitempty"`
}

// TaskEvent represents a status event during task execution
type TaskEvent struct {
 // Timestamp of the event
 Timestamp metav1.Time `json:"timestamp"`

 // Message describing the event
 Message string `json:"message"`
}

// TaskResult contains the outcome of a completed task
type TaskResult struct {
 // PRUrl is the URL of the created pull request (if any)
 // +optional
 PRUrl string `json:"prUrl,omitempty"`

 // Error contains error details if the task failed
 // +optional
 Error string `json:"error,omitempty"`
}

// AgentTaskStatus defines the observed state of AgentTask
type AgentTaskStatus struct {
 // Conditions represent the latest available observations
 // +optional
 Conditions []metav1.Condition `json:"conditions,omitempty"`

 // JobName is the name of the K8s Job running this task
 // +optional
 JobName string `json:"jobName,omitempty"`

 // Result contains the task outcome
 // +optional
 Result TaskResult `json:"result,omitempty"`

 // Events contains status updates from the runner
 // +optional
 Events []TaskEvent `json:"events,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTask is the Schema for the agenttasks API
type AgentTask struct {
 metav1.TypeMeta   `json:",inline"`
 metav1.ObjectMeta `json:"metadata,omitempty"`

 Spec   AgentTaskSpec   `json:"spec,omitempty"`
 Status AgentTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask
type AgentTaskList struct {
 metav1.TypeMeta `json:",inline"`
 metav1.ListMeta `json:"metadata,omitempty"`
 Items           []AgentTask `json:"items"`
}

func init() {
 SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}
```

**Step 3: Add controller-runtime dependency**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go get sigs.k8s.io/controller-runtime@v0.19.0`
Expected: Dependency added

**Step 4: Generate DeepCopy methods**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest`
Expected: controller-gen installed

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && controller-gen object paths="./api/..."`
Expected: `zz_generated.deepcopy.go` created

**Step 5: Verify it compiles**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go build ./...`
Expected: No errors

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: define AgentTask CRD types

- Add shepherd.io/v1alpha1 API group
- Define AgentTask spec with repo, task, callback, runner fields
- Define status with conditions, jobName, result, events
- Add kubebuilder markers for validation and status subresource

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 3: Generate CRD Manifests

**Files:**

- Create: `config/crd/bases/shepherd.io_agenttasks.yaml` (generated)
- Create: `Makefile`

**Step 1: Create Makefile**

```makefile
# Makefile
CONTROLLER_GEN ?= controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.16.0

.PHONY: generate
generate: controller-gen
 $(CONTROLLER_GEN) object paths="./api/..."

.PHONY: manifests
manifests: controller-gen
 $(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: controller-gen
controller-gen:
 @which controller-gen > /dev/null || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: build
build:
 go build -o bin/shepherd ./cmd/shepherd

.PHONY: test
test:
 go test ./... -v

.PHONY: fmt
fmt:
 go fmt ./...

.PHONY: vet
vet:
 go vet ./...
```

**Step 2: Generate CRD manifests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && make manifests`
Expected: `config/crd/bases/shepherd.io_agenttasks.yaml` created

**Step 3: Verify CRD YAML looks correct**

Run: `cat /home/edvin/go/src/github.com/NissesSenap/shepherd/config/crd/bases/shepherd.io_agenttasks.yaml | head -50`
Expected: Valid CRD YAML with apiVersion, kind, spec

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add Makefile and generate CRD manifests

- Add Makefile with generate, manifests, build, test targets
- Generate shepherd.io_agenttasks.yaml CRD

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 2: API Server

### Task 4: Create API Server Module

**Files:**

- Create: `pkg/api/server.go`
- Create: `pkg/api/handlers.go`
- Create: `pkg/api/types.go`
- Modify: `pkg/shepherd/shepherd.go`

**Step 1: Add chi router dependency**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go get github.com/go-chi/chi/v5`
Expected: Dependency added

**Step 2: Create pkg/api/types.go**

```go
// pkg/api/types.go
package api

// CreateTaskRequest is the request body for POST /api/v1/tasks
type CreateTaskRequest struct {
 RepoURL     string `json:"repo_url"`
 Description string `json:"description"`
 Context     string `json:"context,omitempty"`
 CallbackURL string `json:"callback_url"`
}

// CreateTaskResponse is the response for POST /api/v1/tasks
type CreateTaskResponse struct {
 TaskID string `json:"task_id"`
}

// StatusUpdateRequest is the request body for POST /api/v1/tasks/{id}/status
type StatusUpdateRequest struct {
 Event   string            `json:"event"` // started, progress, completed, failed
 Message string            `json:"message"`
 Details map[string]string `json:"details,omitempty"`
}

// TaskStatusResponse is the response for GET /api/v1/tasks/{id}
type TaskStatusResponse struct {
 TaskID  string            `json:"task_id"`
 Status  string            `json:"status"`
 Message string            `json:"message,omitempty"`
 Result  map[string]string `json:"result,omitempty"`
}

// ErrorResponse is returned for errors
type ErrorResponse struct {
 Error string `json:"error"`
}
```

**Step 3: Create pkg/api/handlers.go**

```go
// pkg/api/handlers.go
package api

import (
 "encoding/json"
 "net/http"

 "github.com/go-chi/chi/v5"
)

// Handlers contains HTTP handlers for the API
type Handlers struct {
 // TODO: Add K8s client for CRD operations
}

// NewHandlers creates new API handlers
func NewHandlers() *Handlers {
 return &Handlers{}
}

// CreateTask handles POST /api/v1/tasks
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
 var req CreateTaskRequest
 if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
  h.writeError(w, http.StatusBadRequest, "invalid request body")
  return
 }

 // Validate required fields
 if req.RepoURL == "" {
  h.writeError(w, http.StatusBadRequest, "repo_url is required")
  return
 }
 if req.Description == "" {
  h.writeError(w, http.StatusBadRequest, "description is required")
  return
 }
 if req.CallbackURL == "" {
  h.writeError(w, http.StatusBadRequest, "callback_url is required")
  return
 }

 // TODO: Create AgentTask CRD
 taskID := "task-placeholder"

 h.writeJSON(w, http.StatusCreated, CreateTaskResponse{TaskID: taskID})
}

// GetTaskStatus handles GET /api/v1/tasks/{id}
func (h *Handlers) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
 taskID := chi.URLParam(r, "id")
 if taskID == "" {
  h.writeError(w, http.StatusBadRequest, "task id is required")
  return
 }

 // TODO: Fetch AgentTask CRD status
 h.writeJSON(w, http.StatusOK, TaskStatusResponse{
  TaskID: taskID,
  Status: "pending",
 })
}

// UpdateTaskStatus handles POST /api/v1/tasks/{id}/status
func (h *Handlers) UpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
 taskID := chi.URLParam(r, "id")
 if taskID == "" {
  h.writeError(w, http.StatusBadRequest, "task id is required")
  return
 }

 var req StatusUpdateRequest
 if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
  h.writeError(w, http.StatusBadRequest, "invalid request body")
  return
 }

 // TODO: Update AgentTask CRD status and notify callback
 w.WriteHeader(http.StatusAccepted)
}

// HealthCheck handles GET /healthz
func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
 w.WriteHeader(http.StatusOK)
 w.Write([]byte("ok"))
}

// ReadyCheck handles GET /readyz
func (h *Handlers) ReadyCheck(w http.ResponseWriter, r *http.Request) {
 // TODO: Check K8s connectivity
 w.WriteHeader(http.StatusOK)
 w.Write([]byte("ok"))
}

func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v interface{}) {
 w.Header().Set("Content-Type", "application/json")
 w.WriteHeader(status)
 json.NewEncoder(w).Encode(v)
}

func (h *Handlers) writeError(w http.ResponseWriter, status int, message string) {
 h.writeJSON(w, status, ErrorResponse{Error: message})
}
```

**Step 4: Create pkg/api/server.go**

```go
// pkg/api/server.go
package api

import (
 "context"
 "fmt"
 "net/http"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/go-chi/chi/v5/middleware"
)

// Server is the API server module
type Server struct {
 addr    string
 server  *http.Server
}

// Config holds API server configuration
type Config struct {
 ListenAddr string
}

// NewServer creates a new API server
func NewServer(cfg Config) (*Server, error) {
 handlers := NewHandlers()

 r := chi.NewRouter()

 // Middleware
 r.Use(middleware.Logger)
 r.Use(middleware.Recoverer)
 r.Use(middleware.Timeout(30 * time.Second))

 // Health endpoints
 r.Get("/healthz", handlers.HealthCheck)
 r.Get("/readyz", handlers.ReadyCheck)

 // API routes
 r.Route("/api/v1", func(r chi.Router) {
  r.Post("/tasks", handlers.CreateTask)
  r.Get("/tasks/{id}", handlers.GetTaskStatus)
  r.Post("/tasks/{id}/status", handlers.UpdateTaskStatus)
 })

 return &Server{
  addr: cfg.ListenAddr,
  server: &http.Server{
   Addr:    cfg.ListenAddr,
   Handler: r,
  },
 }, nil
}

// Name returns the module name
func (s *Server) Name() string {
 return "api"
}

// Run starts the API server
func (s *Server) Run(ctx context.Context) error {
 errCh := make(chan error, 1)

 go func() {
  fmt.Printf("API server listening on %s\n", s.addr)
  if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
   errCh <- err
  }
 }()

 select {
 case <-ctx.Done():
  shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  return s.server.Shutdown(shutdownCtx)
 case err := <-errCh:
  return err
 }
}
```

**Step 5: Wire API server into Shepherd**

Update `pkg/shepherd/shepherd.go`:

```go
// pkg/shepherd/shepherd.go
package shepherd

import (
 "context"
 "fmt"

 "github.com/NissesSenap/shepherd/pkg/api"
 "golang.org/x/sync/errgroup"
)

// Module represents a runnable component
type Module interface {
 Name() string
 Run(ctx context.Context) error
}

// Shepherd orchestrates all modules
type Shepherd struct {
 cfg     Config
 modules []Module
}

// New creates a new Shepherd instance
func New(cfg Config) (*Shepherd, error) {
 s := &Shepherd{cfg: cfg}

 if err := s.initModules(); err != nil {
  return nil, fmt.Errorf("init modules: %w", err)
 }

 return s, nil
}

func (s *Shepherd) initModules() error {
 switch s.cfg.Target {
 case TargetAll:
  if err := s.initAPI(); err != nil {
   return err
  }
  // TODO: Add operator and github-adapter modules
 case TargetAPI:
  if err := s.initAPI(); err != nil {
   return err
  }
 case TargetOperator:
  // TODO: Initialize Operator module
 case TargetGitHubAdapter:
  // TODO: Initialize GitHub Adapter module
 }
 return nil
}

func (s *Shepherd) initAPI() error {
 apiServer, err := api.NewServer(api.Config{
  ListenAddr: s.cfg.APIListenAddr,
 })
 if err != nil {
  return fmt.Errorf("create api server: %w", err)
 }
 s.modules = append(s.modules, apiServer)
 return nil
}

// Run starts all modules and blocks until context is cancelled
func (s *Shepherd) Run(ctx context.Context) error {
 if len(s.modules) == 0 {
  fmt.Println("No modules to run for target:", s.cfg.Target)
  <-ctx.Done()
  return nil
 }

 g, ctx := errgroup.WithContext(ctx)

 for _, m := range s.modules {
  m := m // capture for goroutine
  g.Go(func() error {
   fmt.Printf("Starting module: %s\n", m.Name())
   return m.Run(ctx)
  })
 }

 return g.Wait()
}
```

**Step 6: Verify it compiles and runs**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go build ./cmd/shepherd && ./shepherd -target=api &`
Expected: "API server listening on :8080"

Run: `curl http://localhost:8080/healthz`
Expected: "ok"

Run: `curl http://localhost:8080/api/v1/tasks/test-123`
Expected: JSON response with task status

Run: `pkill shepherd`
Expected: Server stopped

**Step 7: Commit**

```bash
git add -A
git commit -m "feat: implement API server module

- Add REST API with chi router
- Implement POST /api/v1/tasks (create task)
- Implement GET /api/v1/tasks/{id} (get status)
- Implement POST /api/v1/tasks/{id}/status (update status)
- Add health and ready endpoints
- Wire API server into multi-target system

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 5: Add API Tests

**Files:**

- Create: `pkg/api/handlers_test.go`

**Step 1: Create handlers_test.go**

```go
// pkg/api/handlers_test.go
package api

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/go-chi/chi/v5"
)

func TestCreateTask_Success(t *testing.T) {
 handlers := NewHandlers()

 body := CreateTaskRequest{
  RepoURL:     "https://github.com/org/repo.git",
  Description: "Fix the bug",
  CallbackURL: "https://callback.example.com/webhook",
 }
 bodyBytes, _ := json.Marshal(body)

 req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
 req.Header.Set("Content-Type", "application/json")
 w := httptest.NewRecorder()

 handlers.CreateTask(w, req)

 if w.Code != http.StatusCreated {
  t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
 }

 var resp CreateTaskResponse
 if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
  t.Fatalf("failed to decode response: %v", err)
 }

 if resp.TaskID == "" {
  t.Error("expected task_id to be non-empty")
 }
}

func TestCreateTask_MissingRepoURL(t *testing.T) {
 handlers := NewHandlers()

 body := CreateTaskRequest{
  Description: "Fix the bug",
  CallbackURL: "https://callback.example.com/webhook",
 }
 bodyBytes, _ := json.Marshal(body)

 req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
 req.Header.Set("Content-Type", "application/json")
 w := httptest.NewRecorder()

 handlers.CreateTask(w, req)

 if w.Code != http.StatusBadRequest {
  t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
 }
}

func TestCreateTask_MissingDescription(t *testing.T) {
 handlers := NewHandlers()

 body := CreateTaskRequest{
  RepoURL:     "https://github.com/org/repo.git",
  CallbackURL: "https://callback.example.com/webhook",
 }
 bodyBytes, _ := json.Marshal(body)

 req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
 req.Header.Set("Content-Type", "application/json")
 w := httptest.NewRecorder()

 handlers.CreateTask(w, req)

 if w.Code != http.StatusBadRequest {
  t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
 }
}

func TestGetTaskStatus(t *testing.T) {
 handlers := NewHandlers()

 r := chi.NewRouter()
 r.Get("/api/v1/tasks/{id}", handlers.GetTaskStatus)

 req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/test-123", nil)
 w := httptest.NewRecorder()

 r.ServeHTTP(w, req)

 if w.Code != http.StatusOK {
  t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
 }

 var resp TaskStatusResponse
 if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
  t.Fatalf("failed to decode response: %v", err)
 }

 if resp.TaskID != "test-123" {
  t.Errorf("expected task_id test-123, got %s", resp.TaskID)
 }
}

func TestHealthCheck(t *testing.T) {
 handlers := NewHandlers()

 req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
 w := httptest.NewRecorder()

 handlers.HealthCheck(w, req)

 if w.Code != http.StatusOK {
  t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
 }

 if w.Body.String() != "ok" {
  t.Errorf("expected body 'ok', got '%s'", w.Body.String())
 }
}
```

**Step 2: Run tests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go test ./pkg/api/... -v`
Expected: All tests pass

**Step 3: Commit**

```bash
git add -A
git commit -m "test: add API handler tests

- Test CreateTask success and validation
- Test GetTaskStatus
- Test HealthCheck

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 3: Operator Module

### Task 6: Create Operator Module with Controller-Runtime

**Files:**

- Create: `pkg/operator/operator.go`
- Create: `internal/controller/agenttask_controller.go`
- Modify: `pkg/shepherd/shepherd.go`

**Step 1: Create pkg/operator/operator.go**

```go
// pkg/operator/operator.go
package operator

import (
 "context"
 "fmt"

 "k8s.io/apimachinery/pkg/runtime"
 utilruntime "k8s.io/apimachinery/pkg/util/runtime"
 clientgoscheme "k8s.io/client-go/kubernetes/scheme"
 ctrl "sigs.k8s.io/controller-runtime"
 "sigs.k8s.io/controller-runtime/pkg/healthz"

 shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
 "github.com/NissesSenap/shepherd/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
 utilruntime.Must(clientgoscheme.AddToScheme(scheme))
 utilruntime.Must(shepherdv1alpha1.AddToScheme(scheme))
}

// Config holds operator configuration
type Config struct {
 MetricsAddr      string
 HealthProbeAddr  string
 LeaderElection   bool
 LeaderElectionID string
}

// Operator is the K8s operator module
type Operator struct {
 cfg Config
 mgr ctrl.Manager
}

// NewOperator creates a new Operator
func NewOperator(cfg Config) (*Operator, error) {
 mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
  Scheme:                 scheme,
  HealthProbeBindAddress: cfg.HealthProbeAddr,
  LeaderElection:         cfg.LeaderElection,
  LeaderElectionID:       cfg.LeaderElectionID,
 })
 if err != nil {
  return nil, fmt.Errorf("create manager: %w", err)
 }

 // Setup AgentTask controller
 if err := (&controller.AgentTaskReconciler{
  Client: mgr.GetClient(),
  Scheme: mgr.GetScheme(),
 }).SetupWithManager(mgr); err != nil {
  return nil, fmt.Errorf("setup controller: %w", err)
 }

 // Add health checks
 if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
  return nil, fmt.Errorf("add healthz check: %w", err)
 }
 if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
  return nil, fmt.Errorf("add readyz check: %w", err)
 }

 return &Operator{
  cfg: cfg,
  mgr: mgr,
 }, nil
}

// Name returns the module name
func (o *Operator) Name() string {
 return "operator"
}

// Run starts the operator
func (o *Operator) Run(ctx context.Context) error {
 fmt.Printf("Operator starting with leader election: %v\n", o.cfg.LeaderElection)
 return o.mgr.Start(ctx)
}
```

**Step 2: Create internal/controller/agenttask_controller.go**

```go
// internal/controller/agenttask_controller.go
package controller

import (
 "context"
 "fmt"

 batchv1 "k8s.io/api/batch/v1"
 corev1 "k8s.io/api/core/v1"
 "k8s.io/apimachinery/pkg/api/errors"
 "k8s.io/apimachinery/pkg/api/meta"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime"
 ctrl "sigs.k8s.io/controller-runtime"
 "sigs.k8s.io/controller-runtime/pkg/client"
 "sigs.k8s.io/controller-runtime/pkg/log"

 shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// AgentTaskReconciler reconciles an AgentTask object
type AgentTaskReconciler struct {
 client.Client
 Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shepherd.io,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
 logger := log.FromContext(ctx)

 // Fetch the AgentTask
 task := &shepherdv1alpha1.AgentTask{}
 if err := r.Get(ctx, req.NamespacedName, task); err != nil {
  if errors.IsNotFound(err) {
   return ctrl.Result{}, nil
  }
  return ctrl.Result{}, err
 }

 // Initialize conditions if empty
 if len(task.Status.Conditions) == 0 {
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Accepted",
   Status:  metav1.ConditionTrue,
   Reason:  "ValidationPassed",
   Message: "Task validated and accepted",
  })
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Running",
   Status:  metav1.ConditionFalse,
   Reason:  "Pending",
   Message: "Waiting for job to start",
  })
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Succeeded",
   Status:  metav1.ConditionFalse,
   Reason:  "InProgress",
   Message: "",
  })
  if err := r.Status().Update(ctx, task); err != nil {
   return ctrl.Result{}, err
  }
  return ctrl.Result{Requeue: true}, nil
 }

 // Check if job already exists
 if task.Status.JobName != "" {
  return r.reconcileJob(ctx, task)
 }

 // Create the job
 job := r.buildJob(task)
 if err := ctrl.SetControllerReference(task, job, r.Scheme); err != nil {
  return ctrl.Result{}, err
 }

 logger.Info("Creating job", "job", job.Name)
 if err := r.Create(ctx, job); err != nil {
  if errors.IsAlreadyExists(err) {
   return ctrl.Result{Requeue: true}, nil
  }
  return ctrl.Result{}, err
 }

 // Update status with job name
 task.Status.JobName = job.Name
 meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
  Type:    "Running",
  Status:  metav1.ConditionTrue,
  Reason:  "JobStarted",
  Message: fmt.Sprintf("Job %s started", job.Name),
 })
 if err := r.Status().Update(ctx, task); err != nil {
  return ctrl.Result{}, err
 }

 return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) reconcileJob(ctx context.Context, task *shepherdv1alpha1.AgentTask) (ctrl.Result, error) {
 job := &batchv1.Job{}
 if err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Status.JobName}, job); err != nil {
  if errors.IsNotFound(err) {
   // Job was deleted, mark as failed
   meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
    Type:    "Succeeded",
    Status:  metav1.ConditionFalse,
    Reason:  "JobDeleted",
    Message: "Job was deleted",
   })
   meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
    Type:    "Running",
    Status:  metav1.ConditionFalse,
    Reason:  "JobDeleted",
    Message: "Job was deleted",
   })
   return ctrl.Result{}, r.Status().Update(ctx, task)
  }
  return ctrl.Result{}, err
 }

 // Check job status
 if job.Status.Succeeded > 0 {
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Succeeded",
   Status:  metav1.ConditionTrue,
   Reason:  "JobCompleted",
   Message: "Job completed successfully",
  })
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Running",
   Status:  metav1.ConditionFalse,
   Reason:  "JobCompleted",
   Message: "Job completed successfully",
  })
  return ctrl.Result{}, r.Status().Update(ctx, task)
 }

 if job.Status.Failed > 0 {
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Succeeded",
   Status:  metav1.ConditionFalse,
   Reason:  "JobFailed",
   Message: "Job failed",
  })
  meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
   Type:    "Running",
   Status:  metav1.ConditionFalse,
   Reason:  "JobFailed",
   Message: "Job failed",
  })
  task.Status.Result.Error = "Job failed"
  return ctrl.Result{}, r.Status().Update(ctx, task)
 }

 return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) buildJob(task *shepherdv1alpha1.AgentTask) *batchv1.Job {
 jobName := fmt.Sprintf("%s-job", task.Name)

 return &batchv1.Job{
  ObjectMeta: metav1.ObjectMeta{
   Name:      jobName,
   Namespace: task.Namespace,
   Labels: map[string]string{
    "shepherd.io/task": task.Name,
   },
  },
  Spec: batchv1.JobSpec{
   Template: corev1.PodTemplateSpec{
    Spec: corev1.PodSpec{
     RestartPolicy: corev1.RestartPolicyNever,
     Containers: []corev1.Container{
      {
       Name:  "runner",
       Image: task.Spec.Runner.Image,
       Env: []corev1.EnvVar{
        {
         Name:  "SHEPHERD_TASK_ID",
         Value: task.Name,
        },
        {
         Name:  "SHEPHERD_REPO_URL",
         Value: task.Spec.Repo.URL,
        },
        {
         Name:  "SHEPHERD_TASK_DESCRIPTION",
         Value: task.Spec.Task.Description,
        },
       },
      },
     },
    },
   },
  },
 }
}

// SetupWithManager sets up the controller with the Manager
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
 return ctrl.NewControllerManagedBy(mgr).
  For(&shepherdv1alpha1.AgentTask{}).
  Owns(&batchv1.Job{}).
  Complete(r)
}
```

**Step 3: Wire operator into Shepherd**

Update `pkg/shepherd/shepherd.go` to add operator initialization:

```go
// pkg/shepherd/shepherd.go
package shepherd

import (
 "context"
 "fmt"

 "github.com/NissesSenap/shepherd/pkg/api"
 "github.com/NissesSenap/shepherd/pkg/operator"
 "golang.org/x/sync/errgroup"
)

// Module represents a runnable component
type Module interface {
 Name() string
 Run(ctx context.Context) error
}

// Shepherd orchestrates all modules
type Shepherd struct {
 cfg     Config
 modules []Module
}

// New creates a new Shepherd instance
func New(cfg Config) (*Shepherd, error) {
 s := &Shepherd{cfg: cfg}

 if err := s.initModules(); err != nil {
  return nil, fmt.Errorf("init modules: %w", err)
 }

 return s, nil
}

func (s *Shepherd) initModules() error {
 switch s.cfg.Target {
 case TargetAll:
  if err := s.initAPI(); err != nil {
   return err
  }
  if err := s.initOperator(); err != nil {
   return err
  }
  // TODO: Add github-adapter module
 case TargetAPI:
  if err := s.initAPI(); err != nil {
   return err
  }
 case TargetOperator:
  if err := s.initOperator(); err != nil {
   return err
  }
 case TargetGitHubAdapter:
  // TODO: Initialize GitHub Adapter module
 }
 return nil
}

func (s *Shepherd) initAPI() error {
 apiServer, err := api.NewServer(api.Config{
  ListenAddr: s.cfg.APIListenAddr,
 })
 if err != nil {
  return fmt.Errorf("create api server: %w", err)
 }
 s.modules = append(s.modules, apiServer)
 return nil
}

func (s *Shepherd) initOperator() error {
 op, err := operator.NewOperator(operator.Config{
  MetricsAddr:      s.cfg.MetricsAddr,
  HealthProbeAddr:  s.cfg.HealthProbeAddr,
  LeaderElection:   s.cfg.LeaderElection,
  LeaderElectionID: s.cfg.LeaderElectionID,
 })
 if err != nil {
  return fmt.Errorf("create operator: %w", err)
 }
 s.modules = append(s.modules, op)
 return nil
}

// Run starts all modules and blocks until context is cancelled
func (s *Shepherd) Run(ctx context.Context) error {
 if len(s.modules) == 0 {
  fmt.Println("No modules to run for target:", s.cfg.Target)
  <-ctx.Done()
  return nil
 }

 g, ctx := errgroup.WithContext(ctx)

 for _, m := range s.modules {
  m := m // capture for goroutine
  g.Go(func() error {
   fmt.Printf("Starting module: %s\n", m.Name())
   return m.Run(ctx)
  })
 }

 return g.Wait()
}
```

**Step 4: Verify it compiles**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go mod tidy && go build ./...`
Expected: No errors

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: implement operator module with controller-runtime

- Add AgentTaskReconciler that creates K8s Jobs for tasks
- Implement condition-based status tracking
- Set up owner references for garbage collection
- Wire operator into multi-target system

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 7: Add Operator Tests

**Files:**

- Create: `internal/controller/agenttask_controller_test.go`

**Step 1: Create controller test file**

```go
// internal/controller/agenttask_controller_test.go
package controller

import (
 "context"
 "testing"

 batchv1 "k8s.io/api/batch/v1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime"
 "k8s.io/apimachinery/pkg/types"
 clientgoscheme "k8s.io/client-go/kubernetes/scheme"
 ctrl "sigs.k8s.io/controller-runtime"
 "sigs.k8s.io/controller-runtime/pkg/client/fake"

 shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestAgentTaskReconciler_CreatesJob(t *testing.T) {
 scheme := runtime.NewScheme()
 _ = clientgoscheme.AddToScheme(scheme)
 _ = shepherdv1alpha1.AddToScheme(scheme)
 _ = batchv1.AddToScheme(scheme)

 task := &shepherdv1alpha1.AgentTask{
  ObjectMeta: metav1.ObjectMeta{
   Name:      "test-task",
   Namespace: "default",
  },
  Spec: shepherdv1alpha1.AgentTaskSpec{
   Repo: shepherdv1alpha1.RepoSpec{
    URL: "https://github.com/org/repo.git",
   },
   Task: shepherdv1alpha1.TaskSpec{
    Description: "Fix the bug",
   },
   Callback: shepherdv1alpha1.CallbackSpec{
    URL: "https://callback.example.com",
   },
   Runner: shepherdv1alpha1.RunnerSpec{
    Image: "shepherd-runner:latest",
   },
  },
 }

 client := fake.NewClientBuilder().
  WithScheme(scheme).
  WithObjects(task).
  WithStatusSubresource(task).
  Build()

 reconciler := &AgentTaskReconciler{
  Client: client,
  Scheme: scheme,
 }

 // First reconcile - initialize conditions
 _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
  NamespacedName: types.NamespacedName{
   Name:      "test-task",
   Namespace: "default",
  },
 })
 if err != nil {
  t.Fatalf("first reconcile failed: %v", err)
 }

 // Second reconcile - create job
 _, err = reconciler.Reconcile(context.Background(), ctrl.Request{
  NamespacedName: types.NamespacedName{
   Name:      "test-task",
   Namespace: "default",
  },
 })
 if err != nil {
  t.Fatalf("second reconcile failed: %v", err)
 }

 // Verify job was created
 job := &batchv1.Job{}
 err = client.Get(context.Background(), types.NamespacedName{
  Name:      "test-task-job",
  Namespace: "default",
 }, job)
 if err != nil {
  t.Fatalf("failed to get job: %v", err)
 }

 if job.Spec.Template.Spec.Containers[0].Image != "shepherd-runner:latest" {
  t.Errorf("expected image shepherd-runner:latest, got %s", job.Spec.Template.Spec.Containers[0].Image)
 }
}

func TestAgentTaskReconciler_NotFound(t *testing.T) {
 scheme := runtime.NewScheme()
 _ = clientgoscheme.AddToScheme(scheme)
 _ = shepherdv1alpha1.AddToScheme(scheme)

 client := fake.NewClientBuilder().
  WithScheme(scheme).
  Build()

 reconciler := &AgentTaskReconciler{
  Client: client,
  Scheme: scheme,
 }

 // Reconcile non-existent task
 result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
  NamespacedName: types.NamespacedName{
   Name:      "non-existent",
   Namespace: "default",
  },
 })
 if err != nil {
  t.Fatalf("reconcile failed: %v", err)
 }

 if result.Requeue {
  t.Error("should not requeue for non-existent task")
 }
}

func TestBuildJob(t *testing.T) {
 scheme := runtime.NewScheme()
 _ = shepherdv1alpha1.AddToScheme(scheme)

 reconciler := &AgentTaskReconciler{
  Scheme: scheme,
 }

 task := &shepherdv1alpha1.AgentTask{
  ObjectMeta: metav1.ObjectMeta{
   Name:      "my-task",
   Namespace: "shepherd",
  },
  Spec: shepherdv1alpha1.AgentTaskSpec{
   Repo: shepherdv1alpha1.RepoSpec{
    URL: "https://github.com/test/repo.git",
   },
   Task: shepherdv1alpha1.TaskSpec{
    Description: "Test description",
   },
   Runner: shepherdv1alpha1.RunnerSpec{
    Image: "custom-runner:v1",
   },
  },
 }

 job := reconciler.buildJob(task)

 if job.Name != "my-task-job" {
  t.Errorf("expected job name my-task-job, got %s", job.Name)
 }

 if job.Namespace != "shepherd" {
  t.Errorf("expected namespace shepherd, got %s", job.Namespace)
 }

 if len(job.Spec.Template.Spec.Containers) != 1 {
  t.Fatalf("expected 1 container, got %d", len(job.Spec.Template.Spec.Containers))
 }

 container := job.Spec.Template.Spec.Containers[0]
 if container.Image != "custom-runner:v1" {
  t.Errorf("expected image custom-runner:v1, got %s", container.Image)
 }

 // Check environment variables
 envMap := make(map[string]string)
 for _, env := range container.Env {
  envMap[env.Name] = env.Value
 }

 if envMap["SHEPHERD_TASK_ID"] != "my-task" {
  t.Errorf("expected SHEPHERD_TASK_ID=my-task, got %s", envMap["SHEPHERD_TASK_ID"])
 }

 if envMap["SHEPHERD_REPO_URL"] != "https://github.com/test/repo.git" {
  t.Errorf("expected correct SHEPHERD_REPO_URL, got %s", envMap["SHEPHERD_REPO_URL"])
 }
}
```

**Step 2: Run tests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go test ./internal/controller/... -v`
Expected: All tests pass

**Step 3: Run all tests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && make test`
Expected: All tests pass

**Step 4: Commit**

```bash
git add -A
git commit -m "test: add operator controller tests

- Test job creation from AgentTask
- Test handling of non-existent tasks
- Test buildJob helper function

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 4: GitHub Adapter

### Task 8: Create GitHub Adapter Module

**Files:**

- Create: `pkg/adapters/github/adapter.go`
- Create: `pkg/adapters/github/webhook.go`
- Create: `pkg/adapters/github/client.go`
- Modify: `pkg/shepherd/shepherd.go`

**Step 1: Add GitHub dependencies**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go get github.com/google/go-github/v68/github github.com/bradleyfalzon/ghinstallation/v2`
Expected: Dependencies added

**Step 2: Create pkg/adapters/github/client.go**

```go
// pkg/adapters/github/client.go
package github

import (
 "context"
 "fmt"
 "net/http"
 "os"

 "github.com/bradleyfalzon/ghinstallation/v2"
 "github.com/google/go-github/v68/github"
)

// Client wraps the GitHub API client
type Client struct {
 appID      int64
 privateKey []byte
}

// NewClient creates a new GitHub client
func NewClient(appID int64, privateKeyPath string) (*Client, error) {
 key, err := os.ReadFile(privateKeyPath)
 if err != nil {
  return nil, fmt.Errorf("read private key: %w", err)
 }

 return &Client{
  appID:      appID,
  privateKey: key,
 }, nil
}

// GetInstallationClient returns a client authenticated for a specific installation
func (c *Client) GetInstallationClient(installationID int64) (*github.Client, error) {
 itr, err := ghinstallation.New(
  http.DefaultTransport,
  c.appID,
  installationID,
  c.privateKey,
 )
 if err != nil {
  return nil, fmt.Errorf("create installation transport: %w", err)
 }

 return github.NewClient(&http.Client{Transport: itr}), nil
}

// PostComment posts a comment on an issue or PR
func (c *Client) PostComment(ctx context.Context, installationID int64, owner, repo string, number int, body string) error {
 client, err := c.GetInstallationClient(installationID)
 if err != nil {
  return err
 }

 _, _, err = client.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{
  Body: &body,
 })
 return err
}
```

**Step 3: Create pkg/adapters/github/webhook.go**

```go
// pkg/adapters/github/webhook.go
package github

import (
 "crypto/hmac"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "strings"
)

// WebhookHandler handles GitHub webhooks
type WebhookHandler struct {
 secret    string
 apiClient APIClient
}

// APIClient interface for creating tasks via the API
type APIClient interface {
 CreateTask(repoURL, description, context, callbackURL string) (string, error)
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(secret string, apiClient APIClient) *WebhookHandler {
 return &WebhookHandler{
  secret:    secret,
  apiClient: apiClient,
 }
}

// IssueCommentEvent represents a GitHub issue comment event
type IssueCommentEvent struct {
 Action  string `json:"action"`
 Issue   Issue  `json:"issue"`
 Comment struct {
  Body string `json:"body"`
  User struct {
   Login string `json:"login"`
  } `json:"user"`
 } `json:"comment"`
 Repository struct {
  FullName string `json:"full_name"`
  CloneURL string `json:"clone_url"`
 } `json:"repository"`
 Installation struct {
  ID int64 `json:"id"`
 } `json:"installation"`
}

// Issue represents a GitHub issue
type Issue struct {
 Number int    `json:"number"`
 Title  string `json:"title"`
 Body   string `json:"body"`
}

// HandleWebhook handles incoming GitHub webhooks
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
 // Verify signature
 body, err := io.ReadAll(r.Body)
 if err != nil {
  http.Error(w, "failed to read body", http.StatusBadRequest)
  return
 }

 signature := r.Header.Get("X-Hub-Signature-256")
 if !h.verifySignature(body, signature) {
  http.Error(w, "invalid signature", http.StatusUnauthorized)
  return
 }

 // Handle based on event type
 eventType := r.Header.Get("X-GitHub-Event")
 switch eventType {
 case "issue_comment":
  h.handleIssueComment(w, body)
 default:
  w.WriteHeader(http.StatusOK)
 }
}

func (h *WebhookHandler) handleIssueComment(w http.ResponseWriter, body []byte) {
 var event IssueCommentEvent
 if err := json.Unmarshal(body, &event); err != nil {
  http.Error(w, "invalid json", http.StatusBadRequest)
  return
 }

 // Only handle new comments
 if event.Action != "created" {
  w.WriteHeader(http.StatusOK)
  return
 }

 // Check for @shepherd mention
 if !strings.Contains(event.Comment.Body, "@shepherd") {
  w.WriteHeader(http.StatusOK)
  return
 }

 // Extract task description (everything after @shepherd)
 description := extractTaskDescription(event.Comment.Body)
 if description == "" {
  w.WriteHeader(http.StatusOK)
  return
 }

 // Build context from issue
 context := fmt.Sprintf("Issue #%d: %s\n\n%s",
  event.Issue.Number,
  event.Issue.Title,
  event.Issue.Body,
 )

 // Create task via API
 callbackURL := fmt.Sprintf("/callback/%s/%d",
  event.Repository.FullName,
  event.Issue.Number,
 )

 if h.apiClient != nil {
  _, err := h.apiClient.CreateTask(
   event.Repository.CloneURL,
   description,
   context,
   callbackURL,
  )
  if err != nil {
   http.Error(w, "failed to create task", http.StatusInternalServerError)
   return
  }
 }

 w.WriteHeader(http.StatusAccepted)
}

func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
 if h.secret == "" {
  return true // Skip verification if no secret configured
 }

 if !strings.HasPrefix(signature, "sha256=") {
  return false
 }

 mac := hmac.New(sha256.New, []byte(h.secret))
 mac.Write(body)
 expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

 return hmac.Equal([]byte(expected), []byte(signature))
}

func extractTaskDescription(body string) string {
 idx := strings.Index(body, "@shepherd")
 if idx == -1 {
  return ""
 }
 return strings.TrimSpace(body[idx+len("@shepherd"):])
}
```

**Step 4: Create pkg/adapters/github/adapter.go**

```go
// pkg/adapters/github/adapter.go
package github

import (
 "context"
 "fmt"
 "net/http"
 "time"

 "github.com/go-chi/chi/v5"
 "github.com/go-chi/chi/v5/middleware"
)

// Config holds GitHub adapter configuration
type Config struct {
 ListenAddr    string
 WebhookSecret string
 AppID         int64
 PrivateKey    string
}

// Adapter is the GitHub adapter module
type Adapter struct {
 cfg    Config
 server *http.Server
 client *Client
}

// NewAdapter creates a new GitHub adapter
func NewAdapter(cfg Config) (*Adapter, error) {
 var client *Client
 var err error

 if cfg.AppID != 0 && cfg.PrivateKey != "" {
  client, err = NewClient(cfg.AppID, cfg.PrivateKey)
  if err != nil {
   return nil, fmt.Errorf("create github client: %w", err)
  }
 }

 webhookHandler := NewWebhookHandler(cfg.WebhookSecret, nil) // TODO: inject API client

 r := chi.NewRouter()
 r.Use(middleware.Logger)
 r.Use(middleware.Recoverer)
 r.Use(middleware.Timeout(30 * time.Second))

 // Webhook endpoint
 r.Post("/webhook", webhookHandler.HandleWebhook)

 // Callback endpoint (receives status updates from API)
 r.Post("/callback/{owner}/{repo}/{number}", func(w http.ResponseWriter, r *http.Request) {
  // TODO: Post comment to GitHub issue
  w.WriteHeader(http.StatusOK)
 })

 // Health check
 r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  w.Write([]byte("ok"))
 })

 return &Adapter{
  cfg:    cfg,
  client: client,
  server: &http.Server{
   Addr:    cfg.ListenAddr,
   Handler: r,
  },
 }, nil
}

// Name returns the module name
func (a *Adapter) Name() string {
 return "github-adapter"
}

// Run starts the GitHub adapter
func (a *Adapter) Run(ctx context.Context) error {
 errCh := make(chan error, 1)

 go func() {
  fmt.Printf("GitHub adapter listening on %s\n", a.cfg.ListenAddr)
  if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
   errCh <- err
  }
 }()

 select {
 case <-ctx.Done():
  shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  return a.server.Shutdown(shutdownCtx)
 case err := <-errCh:
  return err
 }
}
```

**Step 5: Wire GitHub adapter into Shepherd**

Update `pkg/shepherd/shepherd.go`:

```go
// pkg/shepherd/shepherd.go
package shepherd

import (
 "context"
 "fmt"

 "github.com/NissesSenap/shepherd/pkg/adapters/github"
 "github.com/NissesSenap/shepherd/pkg/api"
 "github.com/NissesSenap/shepherd/pkg/operator"
 "golang.org/x/sync/errgroup"
)

// Module represents a runnable component
type Module interface {
 Name() string
 Run(ctx context.Context) error
}

// Shepherd orchestrates all modules
type Shepherd struct {
 cfg     Config
 modules []Module
}

// New creates a new Shepherd instance
func New(cfg Config) (*Shepherd, error) {
 s := &Shepherd{cfg: cfg}

 if err := s.initModules(); err != nil {
  return nil, fmt.Errorf("init modules: %w", err)
 }

 return s, nil
}

func (s *Shepherd) initModules() error {
 switch s.cfg.Target {
 case TargetAll:
  if err := s.initAPI(); err != nil {
   return err
  }
  if err := s.initOperator(); err != nil {
   return err
  }
  if err := s.initGitHubAdapter(); err != nil {
   return err
  }
 case TargetAPI:
  if err := s.initAPI(); err != nil {
   return err
  }
 case TargetOperator:
  if err := s.initOperator(); err != nil {
   return err
  }
 case TargetGitHubAdapter:
  if err := s.initGitHubAdapter(); err != nil {
   return err
  }
 }
 return nil
}

func (s *Shepherd) initAPI() error {
 apiServer, err := api.NewServer(api.Config{
  ListenAddr: s.cfg.APIListenAddr,
 })
 if err != nil {
  return fmt.Errorf("create api server: %w", err)
 }
 s.modules = append(s.modules, apiServer)
 return nil
}

func (s *Shepherd) initOperator() error {
 op, err := operator.NewOperator(operator.Config{
  MetricsAddr:      s.cfg.MetricsAddr,
  HealthProbeAddr:  s.cfg.HealthProbeAddr,
  LeaderElection:   s.cfg.LeaderElection,
  LeaderElectionID: s.cfg.LeaderElectionID,
 })
 if err != nil {
  return fmt.Errorf("create operator: %w", err)
 }
 s.modules = append(s.modules, op)
 return nil
}

func (s *Shepherd) initGitHubAdapter() error {
 adapter, err := github.NewAdapter(github.Config{
  ListenAddr:    s.cfg.GitHubAdapterAddr,
  WebhookSecret: s.cfg.GitHubWebhookSecret,
  AppID:         s.cfg.GitHubAppID,
  PrivateKey:    s.cfg.GitHubPrivateKey,
 })
 if err != nil {
  return fmt.Errorf("create github adapter: %w", err)
 }
 s.modules = append(s.modules, adapter)
 return nil
}

// Run starts all modules and blocks until context is cancelled
func (s *Shepherd) Run(ctx context.Context) error {
 if len(s.modules) == 0 {
  fmt.Println("No modules to run for target:", s.cfg.Target)
  <-ctx.Done()
  return nil
 }

 g, ctx := errgroup.WithContext(ctx)

 for _, m := range s.modules {
  m := m // capture for goroutine
  g.Go(func() error {
   fmt.Printf("Starting module: %s\n", m.Name())
   return m.Run(ctx)
  })
 }

 return g.Wait()
}
```

**Step 6: Add GitHubAdapterAddr to config**

Update `pkg/shepherd/config.go` to add the missing field:

```go
// pkg/shepherd/config.go
package shepherd

import (
 "errors"
 "flag"
)

// Target constants for single-binary multi-target pattern
const (
 TargetAll           = "all"
 TargetAPI           = "api"
 TargetOperator      = "operator"
 TargetGitHubAdapter = "github-adapter"
)

// Config holds all configuration for Shepherd
type Config struct {
 Target string

 // API configuration
 APIListenAddr string

 // Operator configuration
 MetricsAddr      string
 HealthProbeAddr  string
 LeaderElection   bool
 LeaderElectionID string

 // GitHub Adapter configuration
 GitHubAdapterAddr   string
 GitHubWebhookSecret string
 GitHubAppID         int64
 GitHubPrivateKey    string
}

// RegisterFlags registers configuration flags
func (c *Config) RegisterFlags(f *flag.FlagSet) {
 f.StringVar(&c.Target, "target", TargetAll, "Component to run: all, api, operator, github-adapter")
 f.StringVar(&c.APIListenAddr, "api.listen-addr", ":8080", "API server listen address")
 f.StringVar(&c.MetricsAddr, "metrics.addr", ":9090", "Metrics server address")
 f.StringVar(&c.HealthProbeAddr, "health.addr", ":8081", "Health probe address")
 f.BoolVar(&c.LeaderElection, "leader-election", false, "Enable leader election")
 f.StringVar(&c.LeaderElectionID, "leader-election-id", "shepherd-operator", "Leader election ID")
 f.StringVar(&c.GitHubAdapterAddr, "github.listen-addr", ":8082", "GitHub adapter listen address")
 f.StringVar(&c.GitHubWebhookSecret, "github.webhook-secret", "", "GitHub webhook secret")
 f.Int64Var(&c.GitHubAppID, "github.app-id", 0, "GitHub App ID")
 f.StringVar(&c.GitHubPrivateKey, "github.private-key", "", "Path to GitHub App private key")
}

// Validate validates the configuration
func (c *Config) Validate() error {
 switch c.Target {
 case TargetAll, TargetAPI, TargetOperator, TargetGitHubAdapter:
  // valid
 default:
  return errors.New("invalid target: must be one of all, api, operator, github-adapter")
 }
 return nil
}
```

**Step 7: Verify it compiles**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go mod tidy && go build ./...`
Expected: No errors

**Step 8: Commit**

```bash
git add -A
git commit -m "feat: implement GitHub adapter module

- Add webhook handler for issue_comment events
- Add HMAC signature verification
- Extract @shepherd mentions from comments
- Add GitHub App client with ghinstallation
- Wire adapter into multi-target system

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 9: Add GitHub Adapter Tests

**Files:**

- Create: `pkg/adapters/github/webhook_test.go`

**Step 1: Create webhook_test.go**

```go
// pkg/adapters/github/webhook_test.go
package github

import (
 "bytes"
 "crypto/hmac"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"
)

func TestExtractTaskDescription(t *testing.T) {
 tests := []struct {
  name     string
  body     string
  expected string
 }{
  {
   name:     "simple mention",
   body:     "@shepherd fix the null pointer",
   expected: "fix the null pointer",
  },
  {
   name:     "mention in middle",
   body:     "Hey team, @shepherd please fix this bug",
   expected: "please fix this bug",
  },
  {
   name:     "no mention",
   body:     "This is a regular comment",
   expected: "",
  },
  {
   name:     "mention at end",
   body:     "@shepherd",
   expected: "",
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   result := extractTaskDescription(tt.body)
   if result != tt.expected {
    t.Errorf("expected %q, got %q", tt.expected, result)
   }
  })
 }
}

func TestVerifySignature(t *testing.T) {
 secret := "test-secret"
 handler := NewWebhookHandler(secret, nil)

 body := []byte(`{"test": "data"}`)

 // Generate valid signature
 mac := hmac.New(sha256.New, []byte(secret))
 mac.Write(body)
 validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

 tests := []struct {
  name      string
  signature string
  valid     bool
 }{
  {
   name:      "valid signature",
   signature: validSig,
   valid:     true,
  },
  {
   name:      "invalid signature",
   signature: "sha256=invalid",
   valid:     false,
  },
  {
   name:      "missing prefix",
   signature: hex.EncodeToString(mac.Sum(nil)),
   valid:     false,
  },
  {
   name:      "empty signature",
   signature: "",
   valid:     false,
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   result := handler.verifySignature(body, tt.signature)
   if result != tt.valid {
    t.Errorf("expected %v, got %v", tt.valid, result)
   }
  })
 }
}

func TestHandleWebhook_IssueComment(t *testing.T) {
 secret := "test-secret"
 handler := NewWebhookHandler(secret, nil)

 event := IssueCommentEvent{
  Action: "created",
  Issue: Issue{
   Number: 123,
   Title:  "Bug report",
   Body:   "Something is broken",
  },
  Comment: struct {
   Body string `json:"body"`
   User struct {
    Login string `json:"login"`
   } `json:"user"`
  }{
   Body: "@shepherd fix this issue",
  },
  Repository: struct {
   FullName string `json:"full_name"`
   CloneURL string `json:"clone_url"`
  }{
   FullName: "org/repo",
   CloneURL: "https://github.com/org/repo.git",
  },
 }

 body, _ := json.Marshal(event)

 // Generate signature
 mac := hmac.New(sha256.New, []byte(secret))
 mac.Write(body)
 signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

 req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
 req.Header.Set("X-Hub-Signature-256", signature)
 req.Header.Set("X-GitHub-Event", "issue_comment")

 w := httptest.NewRecorder()
 handler.HandleWebhook(w, req)

 if w.Code != http.StatusAccepted {
  t.Errorf("expected status %d, got %d", http.StatusAccepted, w.Code)
 }
}

func TestHandleWebhook_InvalidSignature(t *testing.T) {
 handler := NewWebhookHandler("secret", nil)

 req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
 req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
 req.Header.Set("X-GitHub-Event", "issue_comment")

 w := httptest.NewRecorder()
 handler.HandleWebhook(w, req)

 if w.Code != http.StatusUnauthorized {
  t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
 }
}

func TestHandleWebhook_NoMention(t *testing.T) {
 handler := NewWebhookHandler("", nil) // No secret verification

 event := IssueCommentEvent{
  Action: "created",
  Comment: struct {
   Body string `json:"body"`
   User struct {
    Login string `json:"login"`
   } `json:"user"`
  }{
   Body: "This is a regular comment without mention",
  },
 }

 body, _ := json.Marshal(event)

 req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
 req.Header.Set("X-GitHub-Event", "issue_comment")

 w := httptest.NewRecorder()
 handler.HandleWebhook(w, req)

 // Should return OK but not create task
 if w.Code != http.StatusOK {
  t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
 }
}
```

**Step 2: Run tests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && go test ./pkg/adapters/github/... -v`
Expected: All tests pass

**Step 3: Run all tests**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && make test`
Expected: All tests pass

**Step 4: Commit**

```bash
git add -A
git commit -m "test: add GitHub adapter tests

- Test extractTaskDescription
- Test HMAC signature verification
- Test webhook handling for issue comments
- Test handling of comments without @shepherd mention

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 5: Integration and Deployment

### Task 10: Create Sample AgentTask YAML

**Files:**

- Create: `config/samples/shepherd_v1alpha1_agenttask.yaml`

**Step 1: Create sample YAML**

```yaml
# config/samples/shepherd_v1alpha1_agenttask.yaml
apiVersion: shepherd.io/v1alpha1
kind: AgentTask
metadata:
  name: sample-task
  namespace: shepherd
spec:
  repo:
    url: "https://github.com/example/repo.git"
  task:
    description: "Fix the null pointer exception in login.go"
    context: |
      Issue #123: Login fails intermittently

      Steps to reproduce:
      1. Open the app
      2. Click login
      3. Enter credentials

      Expected: Login succeeds
      Actual: NullPointerException
  callback:
    url: "https://github-adapter.shepherd.svc.cluster.local/callback/example/repo/123"
  runner:
    image: "shepherd-runner:latest"
    timeout: 30m
```

**Step 2: Commit**

```bash
git add -A
git commit -m "docs: add sample AgentTask YAML

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 11: Create Dockerfile

**Files:**

- Create: `Dockerfile`

**Step 1: Create Dockerfile**

```dockerfile
# Dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o shepherd ./cmd/shepherd

# Runtime image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/shepherd .

ENTRYPOINT ["./shepherd"]
```

**Step 2: Test build**

Run: `cd /home/edvin/go/src/github.com/NissesSenap/shepherd && docker build -t shepherd:dev .`
Expected: Image builds successfully

**Step 3: Commit**

```bash
git add -A
git commit -m "build: add Dockerfile for shepherd binary

- Multi-stage build with golang:1.22-alpine
- Minimal runtime image with alpine:3.19

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 12: Create Basic Helm Chart

**Files:**

- Create: `deploy/helm/shepherd/Chart.yaml`
- Create: `deploy/helm/shepherd/values.yaml`
- Create: `deploy/helm/shepherd/templates/deployment.yaml`
- Create: `deploy/helm/shepherd/templates/service.yaml`
- Create: `deploy/helm/shepherd/templates/serviceaccount.yaml`

**Step 1: Create Chart.yaml**

```yaml
# deploy/helm/shepherd/Chart.yaml
apiVersion: v2
name: shepherd
description: Background coding agent orchestrator
type: application
version: 0.1.0
appVersion: "0.1.0"
```

**Step 2: Create values.yaml**

```yaml
# deploy/helm/shepherd/values.yaml
replicaCount: 1

image:
  repository: shepherd
  tag: latest
  pullPolicy: IfNotPresent

target: all

api:
  port: 8080

operator:
  leaderElection: true

githubAdapter:
  port: 8082
  webhookSecret: ""
  appId: 0
  privateKeySecret: ""

resources:
  limits:
    cpu: 500m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

serviceAccount:
  create: true
  name: ""
```

**Step 3: Create templates/serviceaccount.yaml**

```yaml
# deploy/helm/shepherd/templates/serviceaccount.yaml
{{- if .Values.serviceAccount.create }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Values.serviceAccount.name | default (include "shepherd.fullname" .) }}
  labels:
    {{- include "shepherd.labels" . | nindent 4 }}
{{- end }}
```

**Step 4: Create templates/deployment.yaml**

```yaml
# deploy/helm/shepherd/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "shepherd.fullname" . }}
  labels:
    {{- include "shepherd.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      {{- include "shepherd.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "shepherd.selectorLabels" . | nindent 8 }}
    spec:
      serviceAccountName: {{ .Values.serviceAccount.name | default (include "shepherd.fullname" .) }}
      containers:
        - name: shepherd
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          args:
            - "-target={{ .Values.target }}"
            - "-api.listen-addr=:{{ .Values.api.port }}"
            - "-github.listen-addr=:{{ .Values.githubAdapter.port }}"
            {{- if .Values.operator.leaderElection }}
            - "-leader-election=true"
            {{- end }}
          ports:
            - name: api
              containerPort: {{ .Values.api.port }}
            - name: github
              containerPort: {{ .Values.githubAdapter.port }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: api
          readinessProbe:
            httpGet:
              path: /readyz
              port: api
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
```

**Step 5: Create templates/service.yaml**

```yaml
# deploy/helm/shepherd/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "shepherd.fullname" . }}
  labels:
    {{- include "shepherd.labels" . | nindent 4 }}
spec:
  type: ClusterIP
  ports:
    - port: {{ .Values.api.port }}
      targetPort: api
      name: api
    - port: {{ .Values.githubAdapter.port }}
      targetPort: github
      name: github
  selector:
    {{- include "shepherd.selectorLabels" . | nindent 4 }}
```

**Step 6: Create templates/_helpers.tpl**

```yaml
# deploy/helm/shepherd/templates/_helpers.tpl
{{/*
Expand the name of the chart.
*/}}
{{- define "shepherd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shepherd.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shepherd.labels" -}}
helm.sh/chart: {{ include "shepherd.name" . }}
{{ include "shepherd.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "shepherd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "shepherd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
```

**Step 7: Validate helm chart**

Run: `helm lint deploy/helm/shepherd`
Expected: No errors

**Step 8: Commit**

```bash
git add -A
git commit -m "build: add Helm chart for Shepherd deployment

- Add deployment, service, serviceaccount templates
- Configure target, ports, leader election via values
- Add health probes

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Summary

This plan creates a minimal viable Shepherd orchestrator with:

1. **Single-binary multi-target architecture** (Loki/Mimir pattern)
2. **AgentTask CRD** with full spec and status
3. **API server** for task creation and status updates
4. **Operator** that creates K8s Jobs from AgentTasks
5. **GitHub adapter** that handles webhooks and @shepherd mentions
6. **Tests** for all major components
7. **Dockerfile** and **Helm chart** for deployment

### Not Included (Future Work)

- Init container for GitHub token generation
- Runner image with Claude Code
- Full callback flow (API → Adapter → GitHub comment)
- RBAC manifests for operator permissions
- Integration tests with real K8s cluster
- Metrics and observability
