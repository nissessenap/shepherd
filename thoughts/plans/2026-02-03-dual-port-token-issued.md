# Dual-Port API + TokenIssued Implementation Plan

## Overview

Implement defense-in-depth security for the shepherd API by:
1. Splitting the API into two ports (public and internal/runner)
2. Adding a `TokenIssued` field to prevent replay attacks on token fetches

This implements the MVP approach from the token auth research document, providing network-layer isolation combined with application-layer one-time-fetch protection.

## Current State Analysis

**API Server (`pkg/api/server.go`):**
- Single port (`:8080`) serves all endpoints
- All routes share the same chi router
- No separation between public and runner-sensitive endpoints

**Token Handler (`pkg/api/handler_token.go`):**
- Checks `IsTerminal()` to reject completed tasks
- No authentication or replay protection
- TODO comment references Issue #22

**AgentTaskStatus (`api/v1alpha1/agenttask_types.go:108-123`):**
- Fields: `ObservedGeneration`, `StartTime`, `CompletionTime`, `Conditions`, `SandboxClaimName`, `Result`, `GraceDeadline`
- No `TokenIssued` field

### Key Discoveries:
- Status update conflict handling pattern exists in `handler_status.go:149-178` (re-fetch + retry)
- `IsTerminal()` helper already exists for lifecycle checks
- Config uses Kong CLI parser with env var support (`cmd/shepherd/api.go`)

## Desired End State

After implementation:
1. API serves on two ports:
   - Port 8080 (public): `POST /tasks`, `GET /tasks`, `GET /tasks/{taskID}`
   - Port 8081 (internal): `POST /tasks/{taskID}/status`, `GET /tasks/{taskID}/data`, `GET /tasks/{taskID}/token`
2. `TokenIssued` flag prevents multiple token fetches per task execution
3. Kubernetes Service exposes both ports with named port references

### Verification:
- Token endpoint returns 409 on second fetch attempt
- Public endpoints accessible on 8080, runner endpoints on 8081
- Health endpoints (`/healthz`, `/readyz`) available on both ports

## What We're NOT Doing

- **NetworkPolicy manifests** - handled separately by cluster admins
- **Retrigger logic** - future feature; document that `TokenIssued` should be reset if implemented
- **JWT-based authentication** - future enhancement for multi-tenant scenarios
- **Issue #22 bearer token approach** - superseded by this simpler design

## Implementation Approach

The work is split into three phases:
1. Add `TokenIssued` field and one-time-fetch logic (smallest testable unit)
2. Split API into dual-port architecture
3. Add Kubernetes Service manifest

Each phase is independently deployable and testable.

---

## Phase 1: Add TokenIssued Field

### Overview
Add `TokenIssued` boolean to `AgentTaskStatus` and enforce one-time token fetch in the handler.

### Changes Required:

#### 1. AgentTask Types
**File**: `api/v1alpha1/agenttask_types.go`
**Changes**: Add TokenIssued field to AgentTaskStatus

```go
type AgentTaskStatus struct {
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	StartTime          *metav1.Time `json:"startTime,omitempty"`
	CompletionTime     *metav1.Time `json:"completionTime,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
	SandboxClaimName string             `json:"sandboxClaimName,omitempty"`
	// +optional
	Result TaskResult `json:"result,omitzero"`
	// GraceDeadline tracks the deadline for the grace period when a sandbox
	// terminates while the task is still running.
	// +optional
	GraceDeadline *metav1.Time `json:"graceDeadline,omitempty"`
	// TokenIssued is set true when a GitHub token has been issued for this execution.
	// Prevents replay attacks by blocking subsequent token requests.
	// Should be reset if task retrigger functionality is implemented in the future.
	// +optional
	TokenIssued bool `json:"tokenIssued,omitempty"`
}
```

#### 2. Token Handler
**File**: `pkg/api/handler_token.go`
**Changes**: Add TokenIssued check with optimistic concurrency retry

```go
func (h *taskHandler) getTaskToken(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		var task toolkitv1alpha1.AgentTask
		key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
		if err := h.client.Get(r.Context(), key, &task); err != nil {
			if errors.IsNotFound(err) {
				writeError(w, http.StatusNotFound, "task not found", "")
				return
			}
			log.Error(err, "failed to get task", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to get task", "")
			return
		}

		if task.IsTerminal() {
			writeError(w, http.StatusGone, "task is terminal", "")
			return
		}

		// One-time fetch: block replay within same execution
		if task.Status.TokenIssued {
			writeError(w, http.StatusConflict, "token already issued for this execution", "")
			return
		}

		if h.githubKey == nil {
			writeError(w, http.StatusServiceUnavailable, "GitHub App not configured", "")
			return
		}

		// Mark TokenIssued BEFORE generating token (fail-safe: if token gen fails,
		// the flag is set but no token was leaked)
		task.Status.TokenIssued = true
		if err := h.client.Status().Update(r.Context(), &task); err != nil {
			if errors.IsConflict(err) {
				log.V(1).Info("conflict updating TokenIssued, retrying", "taskID", taskID, "attempt", attempt+1)
				continue // Retry with fresh task
			}
			log.Error(err, "failed to update TokenIssued", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to update task status", "")
			return
		}

		// Generate and return token
		jwtToken, err := createJWT(h.githubAppID, h.githubKey)
		if err != nil {
			log.Error(err, "failed to create JWT", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to generate token", "")
			return
		}

		repoName, err := parseRepoName(task.Spec.Repo.URL)
		if err != nil {
			log.Error(err, "failed to parse repo URL", "taskID", taskID, "url", task.Spec.Repo.URL)
			writeError(w, http.StatusInternalServerError, "failed to parse repo URL", "")
			return
		}

		httpClient := h.httpClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}

		token, expiresAt, err := exchangeToken(r.Context(), httpClient, h.githubAPIURL, h.githubInstallID, jwtToken, repoName)
		if err != nil {
			log.Error(err, "failed to exchange token", "taskID", taskID)
			writeError(w, http.StatusBadGateway, "failed to generate GitHub token", "")
			return
		}

		writeJSON(w, http.StatusOK, TokenResponse{
			Token:     token,
			ExpiresAt: expiresAt,
		})
		return
	}

	// Exhausted retries
	log.Error(nil, "exhausted retries updating TokenIssued", "taskID", taskID)
	writeError(w, http.StatusConflict, "concurrent update conflict", "")
}
```

#### 3. Regenerate CRD Manifests
**Command**: `make manifests`

This regenerates `config/crd/bases/toolkit.shepherd.io_agenttasks.yaml` with the new field.

#### 4. Token Handler Tests
**File**: `pkg/api/handler_token_test.go`
**Changes**: Add tests for TokenIssued behavior

```go
func TestGetTaskToken_SetsTokenIssued(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-issued-1",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-issued-1/token")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify TokenIssued was set
	var updatedTask toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-issued-1"}, &updatedTask)
	require.NoError(t, err)
	assert.True(t, updatedTask.Status.TokenIssued, "TokenIssued should be true after token fetch")
}

func TestGetTaskToken_RejectsSecondFetch(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-issued-2",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			TokenIssued: true, // Already issued
		},
	}

	h := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-issued-2/token")

	assert.Equal(t, http.StatusConflict, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "token already issued for this execution", errResp.Error)
}
```

### Success Criteria:

#### Automated Verification:
- [ ] CRD manifests regenerate cleanly: `make manifests`
- [ ] Unit tests pass: `make test`
- [ ] Linting passes: `make lint`
- [ ] New tests verify TokenIssued behavior

#### Manual Verification:
- [ ] First token fetch succeeds and sets `TokenIssued=true` in CRD status
- [ ] Second token fetch returns 409 Conflict

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to Phase 2.

---

## Phase 2: Dual-Port API Architecture

### Overview
Split the API into two ports: public (8080) and internal (8081). Health endpoints are available on both.

### Changes Required:

#### 1. API Options
**File**: `pkg/api/server.go`
**Changes**: Add InternalListenAddr to Options struct

```go
// Options configures the API server.
type Options struct {
	ListenAddr           string
	InternalListenAddr   string // Runner-only API port
	CallbackSecret       string
	Namespace            string
	GithubAppID          int64
	GithubInstallationID int64
	GithubAPIURL         string
	GithubPrivateKeyPath string
}
```

#### 2. Dual Router Setup
**File**: `pkg/api/server.go`
**Changes**: Replace single router with two routers and two HTTP servers

```go
func Run(opts Options) error {
	// ... existing client setup code unchanged until router creation ...

	// Health check handler (shared between both routers)
	healthzHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	readyzHandler := func(w http.ResponseWriter, _ *http.Request) {
		if !watcherHealthy.Load() || !cacheHealthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("watcher or cache unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}

	// Public router (port 8080) - external API for adapters/UI
	publicRouter := chi.NewRouter()
	publicRouter.Use(middleware.RequestID)
	publicRouter.Use(middleware.RealIP)
	publicRouter.Use(middleware.Recoverer)
	publicRouter.Get("/healthz", healthzHandler)
	publicRouter.Get("/readyz", readyzHandler)
	publicRouter.Route("/api/v1", func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/tasks", handler.createTask)
		r.Get("/tasks", handler.listTasks)
		r.Get("/tasks/{taskID}", handler.getTask)
	})

	// Internal router (port 8081) - runner-only API (NetworkPolicy protected)
	internalRouter := chi.NewRouter()
	internalRouter.Use(middleware.RequestID)
	internalRouter.Use(middleware.RealIP)
	internalRouter.Use(middleware.Recoverer)
	internalRouter.Get("/healthz", healthzHandler)
	internalRouter.Get("/readyz", readyzHandler)
	internalRouter.Route("/api/v1", func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/tasks/{taskID}/status", handler.updateTaskStatus)
		r.Get("/tasks/{taskID}/data", handler.getTaskData)
		r.Get("/tasks/{taskID}/token", handler.getTaskToken)
	})

	// Start public server
	publicSrv := &http.Server{
		Addr:         opts.ListenAddr,
		Handler:      publicRouter,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start internal server
	internalSrv := &http.Server{
		Addr:         opts.InternalListenAddr,
		Handler:      internalRouter,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting public API server", "addr", opts.ListenAddr)
		if err := publicSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("public server: %w", err)
		}
	}()
	go func() {
		log.Info("starting internal API server", "addr", opts.InternalListenAddr)
		if err := internalSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("internal server: %w", err)
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		log.Info("shutting down API servers")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		// Shutdown both servers
		var errs []error
		if err := publicSrv.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("public shutdown: %w", err))
		}
		if err := internalSrv.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("internal shutdown: %w", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("shutdown errors: %v", errs)
		}
		return nil
	case err := <-errCh:
		return err
	}
}
```

#### 3. CLI Configuration
**File**: `cmd/shepherd/api.go`
**Changes**: Add InternalListenAddr flag

```go
type APICmd struct {
	ListenAddr           string `help:"Public API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
	InternalListenAddr   string `help:"Internal (runner) API listen address" default:":8081" env:"SHEPHERD_INTERNAL_API_ADDR"`
	CallbackSecret       string `help:"HMAC secret for adapter callbacks" env:"SHEPHERD_CALLBACK_SECRET"`
	Namespace            string `help:"Namespace for task creation" default:"shepherd" env:"SHEPHERD_NAMESPACE"`
	GithubAppID          int64  `help:"GitHub Runner App ID" env:"SHEPHERD_GITHUB_APP_ID"`
	GithubInstallationID int64  `help:"GitHub Installation ID" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
	GithubAPIURL         string `help:"GitHub API URL" default:"https://api.github.com" env:"SHEPHERD_GITHUB_API_URL"`
	GithubPrivateKeyPath string `help:"Path to Runner App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY_PATH"`
}

func (c *APICmd) Run(_ *CLI) error {
	// ... existing validation ...

	return api.Run(api.Options{
		ListenAddr:           c.ListenAddr,
		InternalListenAddr:   c.InternalListenAddr,
		CallbackSecret:       c.CallbackSecret,
		Namespace:            c.Namespace,
		GithubAppID:          c.GithubAppID,
		GithubInstallationID: c.GithubInstallationID,
		GithubAPIURL:         c.GithubAPIURL,
		GithubPrivateKeyPath: c.GithubPrivateKeyPath,
	})
}
```

#### 4. Update Operator API URL
**Files**: `pkg/operator/operator.go`, `cmd/shepherd/operator.go`
**Changes**: Ensure operator uses internal port for runner communication

The operator POSTs task assignments to runners, which then call back to the API. The `APIURL` passed to runners should use the internal port (8081) since runners need access to `/tasks/{taskID}/status`, `/tasks/{taskID}/data`, and `/tasks/{taskID}/token`.

**Discovery**: Search for API URL configuration:
```bash
grep -r "8080\|APIURL\|ApiURL\|api.*url" pkg/operator/ cmd/shepherd/
```

**Locations to update**:
- `pkg/operator/operator.go:55` - `Options.APIURL` field passed to operator
- `cmd/shepherd/operator.go` - CLI flag/env var default value
- `internal/controller/agenttask_controller_test.go` - test fixtures use `http://shepherd-api.shepherd.svc.cluster.local:8080`

**Example change**:
```
Before: http://shepherd-api.shepherd.svc.cluster.local:8080
After:  http://shepherd-api.shepherd.svc.cluster.local:8081
```

**Note**: The CLI default and documentation should reference port 8081. Existing deployments will need to update their `SHEPHERD_API_URL` environment variable.

#### 5. Server Tests
**File**: `pkg/api/server_test.go` (new file or add to existing)
**Changes**: Add tests verifying route separation

```go
func TestDualPortRouting(t *testing.T) {
	// Test that public routes are not available on internal port
	// Test that internal routes are not available on public port
	// Test health endpoints available on both
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Unit tests pass: `make test`
- [ ] Linting passes: `make lint`
- [ ] Build succeeds: `make build`

#### Manual Verification:
- [ ] `curl localhost:8080/api/v1/tasks` returns task list (public endpoint works)
- [ ] `curl localhost:8080/api/v1/tasks/{id}/token` returns 404 (not routed on public port)
- [ ] `curl localhost:8081/api/v1/tasks/{id}/token` returns token (internal endpoint works)
- [ ] `curl localhost:8081/api/v1/tasks` returns 404 (not routed on internal port)
- [ ] Health endpoints work on both ports

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to Phase 3.

---

## Phase 3: Kubernetes Service Manifest

### Overview
Create a Service manifest that exposes both API ports with named port references.

### Changes Required:

#### 1. API Service Manifest
**File**: `config/api/service.yaml` (new file)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: shepherd-api
  labels:
    app.kubernetes.io/name: shepherd-api
    app.kubernetes.io/component: api
spec:
  selector:
    app.kubernetes.io/name: shepherd-api
  ports:
  - name: api
    port: 8080
    targetPort: 8080
    protocol: TCP
  - name: internal
    port: 8081
    targetPort: 8081
    protocol: TCP
```

#### 2. Kustomization
**File**: `config/api/kustomization.yaml` (new file)

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
- service.yaml
```

#### 3. Update Default Kustomization
**File**: `config/default/kustomization.yaml`
**Changes**: Add api resources

```yaml
resources:
# ... existing resources ...
- ../api
```

### Success Criteria:

#### Automated Verification:
- [ ] Kustomize build succeeds: `kustomize build config/default`
- [ ] Service manifest is valid YAML

#### Manual Verification:
- [ ] Service created in cluster with both ports
- [ ] Pods accessible via Service DNS on both ports

**Implementation Note**: After completing this phase, the full implementation is complete.

---

## Testing Strategy

### Unit Tests:
- TokenIssued flag set on first fetch
- TokenIssued flag blocks second fetch (409)
- Conflict retry logic works correctly
- Route separation between public/internal routers

### Integration Tests:
- End-to-end token fetch with TokenIssued persistence
- Verify correct endpoints available on each port

### Manual Testing Steps:
1. Deploy updated API to test cluster
2. Create an AgentTask via `POST :8080/api/v1/tasks`
3. Fetch token via `GET :8081/api/v1/tasks/{id}/token` - should succeed
4. Fetch token again - should return 409 Conflict
5. Verify TokenIssued=true in `kubectl get agenttask {id} -o yaml`

## Migration Notes

- **Backwards compatible**: Existing tasks without `TokenIssued` field will work (omitempty means false)
- **Runners must update API URL**: Runners currently using port 8080 for callbacks must switch to 8081
- **No data migration needed**: New field has zero value default

## Future Considerations

- **Retrigger support**: If implemented, must reset `TokenIssued=false` when resetting task execution state
- **JWT auth upgrade**: For multi-tenant scenarios, add operator-signed JWT as additional layer
- **Audit logging**: Consider logging token issuance events for compliance

## References

- Research document: `thoughts/research/2026-02-03-token-auth-alternatives.md`
- Issue #22: Original token auth proposal (superseded by this approach)
- Current API server: `pkg/api/server.go`
- AgentTask types: `api/v1alpha1/agenttask_types.go`

## Code Review Notes (2026-02-03)

Plan reviewed by code-reviewer and golang-pro agents. Key decisions:

### Accepted Feedback

- **Phase 2.4 specificity**: Added grep commands and specific file locations for operator API URL updates.

### Rejected Feedback (with rationale)

1. **Add `Running` state check before token issuance**: REJECTED
   - Creates race condition in happy path: runner receives task assignment before operator commits `Running` status to Kubernetes
   - Timeline: Operator POSTs to runner → Runner requests token → Operator updates status (commits later)
   - Both `Pending` and `Running` states have `Succeeded.Status=Unknown`, so `IsTerminal()` correctly allows both
   - `TokenIssued` flag provides the actual replay protection, not the `Running` state

2. **Add concurrent request test**: REJECTED
   - Controller-runtime fake client doesn't simulate real etcd conflicts
   - Better approach: mock client returning `IsConflict` to test retry logic
   - True concurrency testing requires integration tests with envtest

3. **Add `TokenIssuedAt` timestamp**: REJECTED
   - Scope creep for MVP; plan already lists audit logging as Future Consideration
   - Kubernetes resource metadata already tracks update times if needed

4. **Document NetworkPolicy in plan**: REJECTED
   - Plan explicitly scopes this out ("What We're NOT Doing")
   - Belongs in deployment documentation, not implementation plan

### Concurrency Pattern Confirmed Safe

The check-then-update pattern with retry loop is correct:
- Matches existing pattern in `handler_status.go:149-178`
- Kubernetes optimistic concurrency via resourceVersion handles TOCTOU
- If concurrent requests race: first to update wins, others retry and see `TokenIssued=true`
