# Operator & API: Jobs to Agent-Sandbox Migration

## Overview

Migrate the operator and API server from the Jobs-based architecture to the agent-sandbox-based architecture described in `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md`. This is a breaking change — no backwards compatibility with the Jobs-based implementation.

## Current State Analysis

**Operator** (`internal/controller/`): Creates K8s Jobs via `job_builder.go`, monitors Job conditions for completion/failure/OOM, uses PodFailurePolicy for exit code 137 detection. Timeout managed via Job `activeDeadlineSeconds`.

**API** (`pkg/api/`): REST endpoints for task CRUD and runner status callbacks. Context compressed via gzip+base64. CRD status watcher sends HMAC-signed callbacks to adapters. No runner-facing data or token endpoints.

**Init container** (`cmd/shepherd-init/`): Writes task files to shared volume, generates GitHub installation tokens via JWT. This logic moves to the API server.

**Agent-sandbox**: Module `sigs.k8s.io/agent-sandbox`. SandboxClaim in `extensions.agents.x-k8s.io/v1alpha1`, Sandbox in `agents.x-k8s.io/v1alpha1`. SandboxClaim references a SandboxTemplate, gets a Sandbox with `Status.ServiceFQDN` and `Ready` condition. Lifecycle/ShutdownTime exists on both CRD types. Uses controller-runtime v0.22.2 + K8s v0.34.1 (Shepherd uses v0.23.0 + v0.35.0).

### Key Discoveries:
- agent-sandbox API types import `sigs.k8s.io/controller-runtime/pkg/scheme` for registration — importing them pulls controller-runtime, but Go module resolution picks v0.23.0 (Shepherd's version wins)
- `decodeContext()` in `cmd/shepherd-init/taskfiles.go:83-114` has decompression bomb protection (10MiB limit) — reuse in API
- `createJWT()` and `exchangeToken()` in `cmd/shepherd-init/github.go` handle PKCS1/PKCS8 key formats and repo-scoped tokens — move to `pkg/api/`
- `github.com/golang-jwt/jwt/v5` is a dependency of `cmd/shepherd-init/` (separate go.mod in that directory) — needs to be added to root go.mod
- SandboxClaim `spec.sandboxTemplateRef.name` references a SandboxTemplate
- Sandbox `status.serviceFQDN` provides the FQDN for HTTP calls to the runner
- Sandbox `status.conditions` with type `Ready` tracks readiness
- SandboxClaim extension type has `SandboxStatus.Name` (note: JSON tag is `Name` with capital N)
- **SandboxClaim controller propagates the Sandbox Ready condition** to `SandboxClaim.Status.Conditions` (`extensions/controllers/sandboxclaim_controller.go:313-318`). This means the operator can use `Owns(&SandboxClaim{})` and read the claim's own Ready condition — no need to watch Sandbox resources directly or build a custom two-level owner mapper.
- **SandboxClaim does NOT expose `ServiceFQDN`** — only `Status.SandboxStatus.Name` (the Sandbox name). To get the FQDN for task assignment, the operator must GET the Sandbox by name at reconcile time. This is a one-time lookup during task assignment, not a watch concern.

## Desired End State

- Operator creates SandboxClaims instead of Jobs
- Operator watches SandboxClaim Ready condition for status tracking (SandboxClaim mirrors Sandbox's Ready condition — no direct Sandbox watching needed)
- Operator GETs Sandbox by name (from `SandboxClaim.Status.SandboxStatus.Name`) to read `ServiceFQDN` for task assignment
- Operator manages timeout via its own timer (not Job `activeDeadlineSeconds`)
- Operator POSTs task assignment to runner (taskID + apiURL, no auth token yet — see [#22](https://github.com/nissessenap/shepherd/issues/22))
- API serves task data and GitHub tokens to runners via new endpoints
- API validates compressed context size (413 if exceeds 1.4MB)
- API supports `fleet` query parameter on task listing
- Runner stub exists at `cmd/shepherd-runner/` with HTTP server on :8888
- Init container code (`cmd/shepherd-init/`) retired
- `ReasonOOM` removed from conditions (simplified failure model)
- CRD updated: `SandboxClaimName` replaces `JobName`, new `SourceType`/`SourceID`/`SourceURL` fields

### Verification:
- `make test` passes (all unit + envtest integration tests)
- `make lint` passes
- `make build` succeeds
- `make manifests && make generate` produces correct CRD YAML
- `make build-smoke` passes

## What We're NOT Doing

- GitHub adapter changes (stays as-is)
- Full runner implementation (stub only)
- Fleet CLI tool
- e2e tests (deferred to follow-up plan)
- Per-task bearer token generation and validation (deferred to [#22](https://github.com/nissessenap/shepherd/issues/22), rely on NetworkPolicy for now)
- Rate limiting on API endpoints (add TODO markers, implement as separate task)
- GitHub App creation or configuration
- SandboxTemplate/SandboxWarmPool manifests (deployment concern, not code)

---

## Phase 1: CRD Type Changes

### Overview
Update the AgentTask CRD types to reflect the sandbox-based architecture. This is the foundation all other phases depend on.

### Changes Required:

#### 1. AgentTask Types
**File**: `api/v1alpha1/agenttask_types.go`

**RunnerSpec changes** — remove `Image`, add `SandboxTemplateName`:
```go
type RunnerSpec struct {
	// SandboxTemplateName references a SandboxTemplate for the runner environment.
	// +kubebuilder:validation:Required
	SandboxTemplateName string `json:"sandboxTemplateName"`

	// Timeout is the maximum duration for task execution.
	// The operator enforces this via its own timer since agent-sandbox v0.1.0
	// does not support Lifecycle/ShutdownTime in released versions.
	// +kubebuilder:default="30m"
	// +optional
	Timeout metav1.Duration `json:"timeout,omitzero"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`
}
```

**TaskSpec changes** — rename `ContextURL` to `SourceURL`, add `SourceType`/`SourceID`:
```go
type TaskSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Context is additional context, gzip-compressed then base64-encoded.
	// The API accepts raw text, compresses for CRD storage.
	// +optional
	Context string `json:"context,omitempty"`

	// +kubebuilder:validation:Enum="";gzip
	ContextEncoding string `json:"contextEncoding,omitempty"`

	// SourceURL is the origin of the task (e.g., GitHub issue URL). Informational only.
	SourceURL string `json:"sourceURL,omitempty"`

	// SourceType identifies the trigger type: "issue", "pr", or "fleet".
	SourceType string `json:"sourceType,omitempty"`

	// SourceID identifies the specific trigger instance (e.g., issue number).
	SourceID string `json:"sourceID,omitempty"`
}
```

Note: `Context` changes from `+kubebuilder:validation:Required` + `MinLength=1` to `+optional`. The arch doc shows context as optional in the CRD (the API can accept tasks without context).

Note: `SourceType`, `SourceID`, and `SourceURL` are all immutable — covered by the existing `+kubebuilder:validation:XValidation:rule="self == oldSelf"` on `TaskSpec`. This is intentional: source metadata is set at creation and never changed.

**AgentTaskStatus changes** — replace `JobName` with `SandboxClaimName`:
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
}
```

**Printcolumn changes** — replace Job with Claim:
```go
// +kubebuilder:printcolumn:name="Claim",type=string,JSONPath=`.status.sandboxClaimName`
```
(Replace the line with `name="Job"`)

#### 2. Conditions
**File**: `api/v1alpha1/conditions.go`

Remove `ReasonOOM`:
```go
const (
	ConditionSucceeded = "Succeeded"

	ReasonPending   = "Pending"
	ReasonRunning   = "Running"
	ReasonSucceeded = "Succeeded"
	ReasonFailed    = "Failed"
	ReasonTimedOut  = "TimedOut"
	ReasonCancelled = "Cancelled"
	// ReasonOOM removed — simplified failure model, no Pod reads

	ConditionNotified = "Notified"

	ReasonCallbackPending = "CallbackPending"
	ReasonCallbackSent    = "CallbackSent"
	ReasonCallbackFailed  = "CallbackFailed"
)
```

#### 3. API Types
**File**: `pkg/api/types.go`

Update `TaskRequest` — rename `ContextURL` to `SourceURL`, add source fields:
```go
type TaskRequest struct {
	Description string `json:"description"`
	Context     string `json:"context,omitempty"`
	SourceURL   string `json:"sourceURL,omitempty"`
	SourceType  string `json:"sourceType,omitempty"`
	SourceID    string `json:"sourceID,omitempty"`
}
```

Update `TaskStatusSummary` — rename `JobName` to `SandboxClaimName`:
```go
type TaskStatusSummary struct {
	Phase            string `json:"phase"`
	Message          string `json:"message"`
	SandboxClaimName string `json:"sandboxClaimName,omitempty"`
	PRUrl            string `json:"prUrl,omitempty"`
	Error            string `json:"error,omitempty"`
}
```

#### 4. API Handlers
**File**: `pkg/api/handler_tasks.go`

- `createTask()`: Update `TaskSpec` field mapping (`ContextURL` → `SourceURL`, add `SourceType`/`SourceID`). Make context optional (remove the "context is required" validation). Add `shepherd.io/source-type` and `shepherd.io/source-id` labels when present.
- `taskToResponse()`: Update field mapping (`ContextURL` → `SourceURL` etc.)
- `extractStatus()`: Change `JobName` → `SandboxClaimName`

#### 5. Regenerate
- Run `make generate` (deepcopy)
- Run `make manifests` (CRD YAML)

#### 6. Fix All Compilation Errors
Every reference to `JobName`, `ContextURL`, `ReasonOOM`, `RunnerSpec.Image` across the codebase needs updating. Key files:
- `internal/controller/agenttask_controller.go` — `task.Status.JobName` → `task.Status.SandboxClaimName`
- `internal/controller/job_builder.go` — references to `RunnerSpec.Image` (will be fully replaced in Phase 2, but needs to compile)
- `pkg/api/handler_status.go` — any `JobName` references
- All test files referencing these fields

### Success Criteria:

#### Automated Verification:
- [x] `make generate` succeeds
- [x] `make manifests` succeeds — CRD YAML shows `sandboxClaimName` not `jobName`, shows `sandboxTemplateName` not `image`
- [x] `make build` compiles without errors
- [x] `make test` passes (update all tests referencing old field names)
- [x] `make lint` passes

**Implementation Note**: After completing this phase and all automated verification passes, pause for review before proceeding.

---

## Phase 2: Agent-Sandbox Dependency + Sandbox Builder

### Overview
Import agent-sandbox API types and create `sandbox_builder.go` that constructs SandboxClaim objects from AgentTask specs. This replaces `job_builder.go`.

### Changes Required:

#### 1. Add agent-sandbox dependency
**File**: `go.mod`

Add the upstream agent-sandbox module:
```go
require (
	sigs.k8s.io/agent-sandbox v0.1.0  // or latest released version
)
```

Run `go mod tidy` to resolve transitive dependencies. The agent-sandbox API types import `sigs.k8s.io/controller-runtime/pkg/scheme` — Go module resolution will use Shepherd's v0.23.0 (higher version wins).

Note: Do NOT use a `replace` directive. Use the published upstream package so CI and other developers can build without a local checkout.

#### 2. Register agent-sandbox schemes
**File**: `pkg/operator/operator.go`

Add scheme registration for both agent-sandbox API groups:
```go
import (
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(toolkitv1alpha1.AddToScheme(scheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(scheme))
	utilruntime.Must(sandboxextv1alpha1.AddToScheme(scheme))
}
```

Also register in `internal/controller/suite_test.go` for envtest.

#### 3. Create sandbox_builder.go
**File**: `internal/controller/sandbox_builder.go` (new)

Replace `job_builder.go`. The builder constructs a SandboxClaim from an AgentTask:

```go
type sandboxConfig struct {
	Scheme *runtime.Scheme
}

func buildSandboxClaim(task *toolkitv1alpha1.AgentTask, cfg sandboxConfig) (*sandboxextv1alpha1.SandboxClaim, error) {
	claimName := task.Name
	if len(claimName) > 63 {
		return nil, fmt.Errorf("task name %q exceeds 63-character limit", claimName)
	}

	if task.Spec.Runner.SandboxTemplateName == "" {
		return nil, fmt.Errorf("sandboxTemplateName is required")
	}

	claim := &sandboxextv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: task.Namespace,
			Labels: map[string]string{
				"shepherd.io/task": task.Name,
			},
		},
		Spec: sandboxextv1alpha1.SandboxClaimSpec{
			TemplateRef: sandboxextv1alpha1.SandboxTemplateRef{
				Name: task.Spec.Runner.SandboxTemplateName,
			},
		},
	}

	if err := controllerutil.SetControllerReference(task, claim, cfg.Scheme); err != nil {
		return nil, fmt.Errorf("setting owner reference: %w", err)
	}

	return claim, nil
}
```

Key differences from `buildJob()`:
- No init container, no volumes, no env vars — SandboxTemplate controls the pod spec
- No `activeDeadlineSeconds` — operator manages timeout (Phase 5)
- No PodFailurePolicy — simplified failure model
- Claim name = task name (no generation suffix needed — SandboxClaim is owned, deleted on recreation)
- No `AllowedRunnerImage`, `InitImage`, `RunnerSecretName`, or GitHub config needed

#### 4. Delete job_builder.go
**File**: `internal/controller/job_builder.go` — delete

#### 5. Tests
**File**: `internal/controller/sandbox_builder_test.go` (new)

Unit tests using testify (follow `job_builder_test.go` patterns):
- Claim name matches task name
- Labels set correctly (`shepherd.io/task`)
- SandboxTemplateRef.Name set from `spec.runner.sandboxTemplateName`
- Owner reference set (AgentTask as controller owner)
- Error on empty sandboxTemplateName
- Error on name exceeding 63 characters
- Namespace propagated from task

**File**: `internal/controller/job_builder_test.go` — delete

**File**: `internal/controller/failure_test.go` — delete (failure classification changes in Phase 5)

### Success Criteria:

#### Automated Verification:
- [x] `go mod tidy` succeeds without errors
- [x] `make build` compiles (may need temporary stubs in controller if it references old builder)
- [x] `make test` passes — new `sandbox_builder_test.go` passes, old Job tests removed
- [x] `make lint` passes

---

## Phase 3: Reconcile — SandboxClaim Creation + Status Tracking

### Overview
Rewrite the reconcile loop core. Create SandboxClaims instead of Jobs. Watch Sandbox status for Ready condition. Track state transitions: Pending → SandboxClaim created → Sandbox Ready → Running.

### Changes Required:

#### 1. Rewrite reconciler
**File**: `internal/controller/agenttask_controller.go`

Replace the entire reconciler. Key structural changes:

**Reconciler struct** — remove Job-related config, add API URL and HTTP client:
```go
type AgentTaskReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   events.EventRecorder // k8s.io/client-go/tools/events (NOT record.EventRecorder)
	APIURL     string               // Internal API URL for runner task assignment
	HTTPClient *http.Client         // Injectable for testing; defaults to http.DefaultClient
}
```

`AllowedRunnerImage`, `RunnerSecretName`, `InitImage`, `GithubAppID`, `GithubInstallationID`, `GithubAPIURL` all removed. The reconciler no longer builds pods directly or handles GitHub config.

`HTTPClient` is injected so tests can use `httptest.NewServer` to mock the runner endpoint. In production, set to `http.DefaultClient` (or a client with reasonable timeouts).

**Reconcile loop** (new logic):
```
1. Fetch AgentTask
2. If terminal → return (+ cleanup SandboxClaim if still exists)
3. If no Succeeded condition → set Pending, requeue
4. Look for existing SandboxClaim (name = task.Name)
5. If no SandboxClaim → create via buildSandboxClaim()
   - Record SandboxClaimName in status
   - Stay in Pending state
   - Requeue
6. Check SandboxClaim Ready condition (the SandboxClaim controller
   mirrors the underlying Sandbox's Ready condition, so we don't
   need to watch or GET the Sandbox for status tracking):
     a. Ready=True → set Running (task assignment deferred to Phase 4)
     b. Ready=False + was previously Running → mark Failed (Phase 5)
     c. Ready condition is nil, False, or Unknown AND task was never Running
        → still starting, requeue after 5s
   Note: When a SandboxClaim is first created, its conditions slice is
   empty (`FindStatusCondition` returns nil). Treat nil the same as
   "not ready yet" — the agent-sandbox controller hasn't reconciled yet.
```

For this phase, step 6a just transitions to Running without task assignment. Phase 4 adds the assignment step (which needs a Sandbox GET to read `ServiceFQDN`).

**Remove**: `reconcileJobStatus()`, `classifyJobFailure()`, `failureClass` type, all Job imports.

**SetupWithManager** — watch AgentTasks and owned SandboxClaims:
```go
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolkitv1alpha1.AgentTask{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&sandboxextv1alpha1.SandboxClaim{}).
		Complete(r)
}
```

This is simpler than the original plan assumed. The SandboxClaim controller
propagates the Sandbox Ready condition to `SandboxClaim.Status.Conditions`
(`agent-sandbox extensions/controllers/sandboxclaim_controller.go:313-318`).
When the SandboxClaim status changes, controller-runtime triggers reconciliation
of the owning AgentTask via the `Owns()` binding. No direct Sandbox watching
or custom two-level owner mapper is needed.

The operator still needs `get` permission on Sandbox resources (used in Phase 4
to read `ServiceFQDN` for the task assignment POST), but does not need
`list`/`watch`.

#### 2. Update RBAC markers (partial — completed in Phase 6)

For this phase, update markers enough to compile. Full RBAC update in Phase 6.

#### 3. Tests
**File**: `internal/controller/agenttask_controller_test.go`

Rewrite integration tests for the new flow:
- "should set Pending condition on first reconcile" (same as before)
- "should not reconcile a terminal task" (same pattern)
- "should create SandboxClaim on second reconcile"
  - Verify claim exists with correct template ref and owner ref
  - Verify `status.sandboxClaimName` set
- "should set Running when SandboxClaim Ready=True"
  - Manually set the SandboxClaim's Ready condition to True (simulating
    what the agent-sandbox SandboxClaim controller would do)
  - Reconcile and verify AgentTask condition transitions to Running
- "should requeue when SandboxClaim not yet ready"

Note: envtest won't have the agent-sandbox controllers running. Tests
simulate the SandboxClaim controller by manually setting conditions
on the SandboxClaim's status. This is sufficient since we only read
the SandboxClaim's Ready condition (not the Sandbox directly).

**File**: `internal/controller/suite_test.go`

Register agent-sandbox CRDs in envtest. The CRD YAMLs are at `k8s/crds/` within the `sigs.k8s.io/agent-sandbox` module.

Resolve the module path from the Go module cache at test time (e.g., via `go list -m -json sigs.k8s.io/agent-sandbox` or `runtime/debug.ReadBuildInfo`). Add the resolved CRD directory to envtest's `CRDDirectoryPaths`.

### Success Criteria:

#### Automated Verification:
- [x] `make build` compiles
- [x] `make test` passes — new reconciler tests cover Pending → SandboxClaim creation → Running transition
- [x] `make lint` passes
- [x] No references to `batchv1.Job` remain in `internal/controller/`

---

## Phase 4: Reconcile — Task Assignment

### Overview
When the Sandbox becomes Ready, the operator POSTs the task assignment to the runner's HTTP endpoint. The Running condition serves as the idempotency marker — no annotations or extra status fields needed.

Per-task bearer token authentication is deferred to [#22](https://github.com/nissessenap/shepherd/issues/22). Until then, runner endpoints are protected by NetworkPolicy only.

### Changes Required:

#### 1. Task assignment logic
**File**: `internal/controller/agenttask_controller.go`

Add `assignTask()` method:

```go
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

// assignTask POSTs a task assignment to the runner's HTTP endpoint.
// Returns nil on success (200 OK or 409 Conflict), error otherwise.
// The caller (reconcile loop) handles retries via controller-runtime's RequeueAfter.
func (r *AgentTaskReconciler) assignTask(ctx context.Context, sandboxFQDN string, assignment TaskAssignment) error {
	body, err := json.Marshal(assignment)
	if err != nil {
		return fmt.Errorf("marshaling assignment: %w", err)
	}

	url := fmt.Sprintf("http://%s:8888/task", sandboxFQDN)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting to runner: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		// Runner already has this task (idempotent retry after crash)
		return nil
	default:
		return fmt.Errorf("runner returned %d", resp.StatusCode)
	}
}
```

**Note**: This implementation uses single-attempt with controller-runtime's exponential backoff
requeue instead of an in-method retry loop. This is simpler and consistent with Kubernetes
controller patterns where transient errors are handled by requeueing the entire reconcile.

#### 2. Update reconcile loop

Modify the Ready=True branch in the reconcile loop:

```
6a. SandboxClaim Ready=True:
    - If task is already Running → nothing to do (assignment already succeeded)
    - If not Running:
      1. GET Sandbox by name (from claim.Status.SandboxStatus.Name) to read ServiceFQDN
      2. Call assignTask() with the ServiceFQDN
      - On success → set Running condition (this IS the idempotency marker)
      - On failure → requeue (transient) or mark Failed (after retries exhausted)
```

The Running condition is set AFTER successful task assignment. If the operator crashes
after POST but before setting Running, the next reconcile retries the POST — the runner
returns 409 (already assigned), which is treated as success, and Running is set.

The Sandbox GET is the only place the operator reads Sandbox resources directly.
This is needed because `SandboxClaim.Status` only exposes the Sandbox name,
not the ServiceFQDN. The operator needs `get` (but not `list`/`watch`) on
Sandboxes for this lookup.

#### 3. Tests
**File**: `internal/controller/agenttask_controller_test.go`

Add tests:
- "should POST assignment to runner when SandboxClaim Ready=True"
  - Use `httptest.NewServer` to simulate runner endpoint
  - Verify POST body contains taskID and apiURL
- "should set Running after successful assignment"
- "should not re-assign if already Running" (idempotency)
- "should treat 409 from runner as success" (crash recovery)
- "should requeue on transient assignment failure"

### Success Criteria:

#### Automated Verification:
- [x] `make build` compiles
- [x] `make test` passes — assignment tests verify HTTP POST and Running transition
- [x] `make lint` passes

---

## Phase 5: Reconcile — Failure Detection + Timeout Management

### Overview
Handle unhappy paths: Sandbox termination without success callback → Failed. Timeout exceeded → TimedOut. Terminal state → delete SandboxClaim.

### Changes Required:

#### 1. Failure detection
**File**: `internal/controller/agenttask_controller.go`

Replace `classifyJobFailure()` with SandboxClaim condition-based detection.
The SandboxClaim mirrors the Sandbox Ready condition, including the reason
(e.g., `SandboxExpired`, `ClaimExpired`, `SandboxNotReady`):

```go
func classifyClaimTermination(claim *sandboxextv1alpha1.SandboxClaim) (string, string) {
	readyCond := meta.FindStatusCondition(claim.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
	if readyCond == nil {
		return toolkitv1alpha1.ReasonFailed, "SandboxClaim status unavailable"
	}
	if readyCond.Reason == sandboxv1alpha1.SandboxReasonExpired ||
		readyCond.Reason == sandboxextv1alpha1.ClaimExpiredReason {
		return toolkitv1alpha1.ReasonTimedOut, "Sandbox expired"
	}
	return toolkitv1alpha1.ReasonFailed, fmt.Sprintf("Sandbox terminated: %s", readyCond.Message)
}
```

Update reconcile loop step 6b:
```
6b. SandboxClaim Ready=False + task has Running condition (assignment was successful):
    - Refetch the AgentTask to get latest status (the API may have updated it
      to terminal via the runner's success callback)
    - If task is now terminal (Succeeded=True or Succeeded=False):
      → just delete SandboxClaim, return
    - If task is still Running:
      → requeue after 30s grace period to give the API time to process
        the runner's callback (race: sandbox can shut down before API
        persists the runner's success report)
    - On the SECOND reconcile after grace period, if task is STILL Running:
      → classify termination from SandboxClaim conditions, mark Failed
      → delete SandboxClaim
```

**Why the grace period**: When a runner completes successfully, it POSTs to
the API and then exits. The sandbox shuts down (Ready→False) potentially
before the API persists `Succeeded=True`. Without the grace period, the
operator would see Ready=False + Running and immediately mark Failed,
destroying a successful result.

#### 2. Timeout management
**File**: `internal/controller/agenttask_controller.go`

Add timeout check in the reconcile loop between steps 6 and 7:

```
7. If task is Running (assigned):
   - If now > startTime + spec.runner.timeout → delete SandboxClaim, mark TimedOut
```

```go
func (r *AgentTaskReconciler) checkTimeout(task *toolkitv1alpha1.AgentTask) bool {
	if task.Status.StartTime == nil {
		return false
	}
	timeout := task.Spec.Runner.Timeout.Duration
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	return time.Now().After(task.Status.StartTime.Time.Add(timeout))
}
```

#### 3. Terminal cleanup
**File**: `internal/controller/agenttask_controller.go`

Add cleanup at the top of reconcile (after terminal check):
```
2. If terminal:
   - If SandboxClaim still exists → delete it
   - Return
```

This handles the case where the API updated the CRD to terminal (via runner callback) but the operator hasn't cleaned up the SandboxClaim yet.

#### 4. Tests

**File**: `internal/controller/agenttask_controller_test.go`

Add tests:
- "should requeue with grace period when SandboxClaim Ready=False and task Running"
- "should mark Failed after grace period if task still Running"
- "should not mark Failed if API set Succeeded during grace period"
- "should mark TimedOut when timeout exceeded"
- "should delete SandboxClaim on terminal state"
- "should delete SandboxClaim when task already succeeded via API callback"
- "should mark TimedOut when SandboxExpired reason on SandboxClaim"
- "should mark TimedOut when ClaimExpired reason on SandboxClaim"

**File**: `internal/controller/failure_test.go` (new, replaces deleted one)

Unit tests for `classifyClaimTermination()`:
- Ready condition nil → Failed
- SandboxExpired reason → TimedOut
- ClaimExpired reason → TimedOut
- Other reason → Failed with message

### Success Criteria:

#### Automated Verification:
- [ ] `make build` compiles
- [ ] `make test` passes — failure and timeout tests pass
- [ ] `make lint` passes

---

## Phase 6: Reconcile — RBAC + Wiring

### Overview
Update RBAC markers, wire the new reconciler into the operator binary, and update the CLI flags.

### Changes Required:

#### 1. RBAC markers
**File**: `internal/controller/agenttask_controller.go`

Replace all RBAC markers:
```go
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
```

Key changes from current:
- Remove `batch` group (no more Jobs)
- Remove `create`/`delete` from AgentTask verbs (operator doesn't create tasks)
- Add `extensions.agents.x-k8s.io/sandboxclaims` (create, delete, get, list, watch — `Owns()` requires list/watch)
- Add `agents.x-k8s.io/sandboxes` (`get` only — used once during task assignment to read ServiceFQDN from Sandbox; no list/watch needed since the operator watches SandboxClaim conditions, not Sandbox directly)
- Add `coordination.k8s.io/leases` (leader election)
- Secrets RBAC deferred to [#22](https://github.com/nissessenap/shepherd/issues/22) (token authentication)

#### 2. Operator Options
**File**: `pkg/operator/operator.go`

Update Options struct — remove Job-related fields, add APIURL:
```go
type Options struct {
	MetricsAddr    string
	HealthAddr     string
	LeaderElection bool
	APIURL         string // Internal API URL (e.g., http://shepherd-api.shepherd.svc.cluster.local:8080)
}
```

Update `Run()` — wire new reconciler:
```go
if err := (&controller.AgentTaskReconciler{
	Client:     mgr.GetClient(),
	Scheme:     mgr.GetScheme(),
	Recorder:   mgr.GetEventRecorder("shepherd-operator"),
	APIURL:     opts.APIURL,
	HTTPClient: &http.Client{Timeout: 30 * time.Second},
}).SetupWithManager(mgr); err != nil {
	return fmt.Errorf("setting up controller: %w", err)
}
```

#### 3. CLI flags
**File**: `cmd/shepherd/operator.go`

Update OperatorCmd — remove Job-related flags, add APIURL:
```go
type OperatorCmd struct {
	MetricsAddr    string `help:"Metrics address" default:":9090" env:"SHEPHERD_METRICS_ADDR"`
	HealthAddr     string `help:"Health probe address" default:":8081" env:"SHEPHERD_HEALTH_ADDR"`
	LeaderElection bool   `help:"Enable leader election" default:"false" env:"SHEPHERD_LEADER_ELECTION"`
	APIURL         string `help:"Internal API server URL" required:"" env:"SHEPHERD_API_URL"`
}
```

Remove: `AllowedRunnerImage`, `InitImage`, `GithubAppID`, `GithubInstallationID`, `GithubAPIURL`, `RunnerSecretName`.

#### 4. Regenerate RBAC manifests
Run `make manifests` to regenerate `config/rbac/role.yaml` from the new markers.

#### 5. Update config/rbac
Verify generated `config/rbac/role.yaml` no longer references `batch` group and includes new sandbox permissions.

### Success Criteria:

#### Automated Verification:
- [ ] `make manifests` regenerates RBAC — no `batch` group in role.yaml
- [ ] `make build` compiles
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] `make build-smoke` passes (kustomize renders correctly)

---

## Phase 7: API — New Runner Endpoints

### Overview
Add endpoints for runner-pull model: serve task data and generate GitHub tokens. Add context size validation. Add fleet query parameter support.

### Changes Required:

#### 1. Task data endpoint
**File**: `pkg/api/handler_data.go` (new)

```go
// getTaskData handles GET /api/v1/tasks/{taskID}/data
// Returns decompressed task description, context, repo info.
// TODO: Authenticate via per-task bearer token (see architecture doc "Runner Authentication")
func (h *taskHandler) getTaskData(w http.ResponseWriter, r *http.Request) {
	// 1. Extract taskID from URL
	// 2. Fetch AgentTask CRD
	// 3. Check task is not terminal (reject if completed/failed)
	// 4. Decompress context (reuse decodeContext logic from init container)
	// 5. Return TaskDataResponse
}
```

Response type in `types.go`:
```go
type TaskDataResponse struct {
	Description string      `json:"description"`
	Context     string      `json:"context"`
	SourceURL   string      `json:"sourceURL,omitempty"`
	Repo        RepoRequest `json:"repo"`
}
```

Move `decodeContext()` from `cmd/shepherd-init/taskfiles.go` to `pkg/api/decompress.go` (or extend existing `compress.go`). Keep the 10MiB decompression limit.

#### 2. Token generation endpoint
**File**: `pkg/api/handler_token.go` (new)

```go
// getTaskToken handles GET /api/v1/tasks/{taskID}/token
// Generates a short-lived GitHub installation token scoped to the task's repo.
// TODO: Authenticate via per-task bearer token (see architecture doc "Runner Authentication")
func (h *taskHandler) getTaskToken(w http.ResponseWriter, r *http.Request) {
	// 1. Extract taskID from URL
	// 2. Fetch AgentTask CRD
	// 3. Check task is not terminal
	// 4. Generate JWT using GitHub App private key
	// 5. Exchange JWT for installation token scoped to repo
	// 6. Return TokenResponse
}
```

Response type:
```go
type TokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}
```

Move token generation logic from `cmd/shepherd-init/github.go`:
- `readPrivateKey()` → `pkg/api/github_token.go`
- `createJWT()` → same file
- `parseRepoName()` → same file
- `exchangeToken()` → same file

The `taskHandler` struct needs new fields for GitHub App config:
```go
type taskHandler struct {
	client             client.Client
	namespace          string
	callback           *callbackSender
	githubAppID        int64
	githubInstallID    int64
	githubAPIURL       string
	githubPrivateKey   *rsa.PrivateKey // Loaded once at startup
}
```

Add `github.com/golang-jwt/jwt/v5` to root `go.mod`.

#### 3. Context size validation
**File**: `pkg/api/handler_tasks.go`

After compressing context in `createTask()`, check size:
```go
const maxCompressedContextSize = 1_400_000 // ~1.4MB, etcd limit minus overhead

compressed, encoding, err := compressContext(req.Task.Context)
if err != nil { ... }
if len(compressed) > maxCompressedContextSize {
	writeError(w, http.StatusRequestEntityTooLarge,
		"compressed context exceeds size limit",
		fmt.Sprintf("compressed size %d exceeds %d byte limit", len(compressed), maxCompressedContextSize))
	return
}
```

#### 4. Fleet query parameter
**File**: `pkg/api/handler_tasks.go`

Add `fleet` query parameter to `listTasks()`:
```go
if fleet := r.URL.Query().Get("fleet"); fleet != "" {
	labelSelector["shepherd.io/fleet"] = fleet
}
```

#### 5. Route registration
**File**: `pkg/api/server.go`

Add new routes:
```go
r.Get("/tasks/{taskID}/data", handler.getTaskData)
r.Get("/tasks/{taskID}/token", handler.getTaskToken)
```

Update `Options` and `APICmd`:
```go
type Options struct {
	ListenAddr           string
	CallbackSecret       string
	Namespace            string
	GithubAppID          int64
	GithubInstallationID int64
	GithubAPIURL         string
	GithubPrivateKeyPath string
}
```

**File**: `cmd/shepherd/api.go` — add GitHub App flags:
```go
type APICmd struct {
	ListenAddr           string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
	CallbackSecret       string `help:"HMAC secret for adapter callbacks" env:"SHEPHERD_CALLBACK_SECRET"`
	Namespace            string `help:"Namespace for task creation" default:"shepherd" env:"SHEPHERD_NAMESPACE"`
	GithubAppID          int64  `help:"GitHub Runner App ID" required:"" env:"SHEPHERD_GITHUB_APP_ID"`
	GithubInstallationID int64  `help:"GitHub Installation ID" required:"" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
	GithubAPIURL         string `help:"GitHub API URL" default:"https://api.github.com" env:"SHEPHERD_GITHUB_API_URL"`
	GithubPrivateKeyPath string `help:"Path to Runner App private key" required:"" env:"SHEPHERD_GITHUB_PRIVATE_KEY_PATH"`
}
```

#### 6. Tests

**File**: `pkg/api/handler_data_test.go` (new)
- Returns decompressed context for valid task
- Returns 404 for missing task
- Returns plaintext when context has no encoding
- Decompresses gzip+base64 context correctly
- Rejects requests for terminal tasks

**File**: `pkg/api/handler_token_test.go` (new)
- Note: Token generation requires a real private key and GitHub API. Use `httptest.NewServer` to mock the GitHub token exchange endpoint.
- Returns token for valid task
- Returns 404 for missing task
- Scopes token to repo from task spec
- Rejects requests for terminal tasks

**File**: `pkg/api/handler_tasks_test.go` — update existing tests:
- Add test for 413 response on oversized compressed context
- Add test for `fleet` query parameter
- Update tests that reference `ContextURL` → `SourceURL` (JSON tag now `sourceURL`)
- Update tests that reference `JobName` → `SandboxClaimName`

**File**: `pkg/api/decompress_test.go` (new, or extend `compress_test.go`)
- Roundtrip: compress then decompress
- Handles empty context
- Rejects invalid base64
- Rejects invalid gzip
- Enforces decompression size limit

### Success Criteria:

#### Automated Verification:
- [ ] `go mod tidy` succeeds (jwt dependency added)
- [ ] `make build` compiles
- [ ] `make test` passes — all new endpoint tests pass
- [ ] `make lint` passes

---

## Phase 8: Runner Stub + Cleanup

### Overview
Create a minimal runner stub binary and retire the init container.

### Changes Required:

#### 1. Runner stub
**File**: `cmd/shepherd-runner/main.go` (new)

Minimal HTTP server that accepts task assignment and exits:

```go
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var assigned chan TaskAssignment
	assigned = make(chan TaskAssignment, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
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

	srv := &http.Server{Addr: ":8888", Handler: mux}
	go func() {
		slog.Info("runner stub listening", "addr", ":8888")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for task assignment or shutdown
	select {
	case ta := <-assigned:
		slog.Info("task assigned, stub exiting", "taskID", ta.TaskID)
		// In full implementation: pull data, clone repo, run claude, report results
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	srv.Shutdown(context.Background())
}
```

This is a stub — it accepts the assignment and exits. The full runner implementation is a separate plan.

#### 2. Runner stub tests
**File**: `cmd/shepherd-runner/main_test.go` (new)

- Health endpoint returns 200
- POST /task accepts assignment and returns 200
- POST /task rejects second assignment (409)
- Invalid JSON returns 400

#### 3. Delete init container
Remove `cmd/shepherd-init/` directory entirely.

#### 4. Update Makefile

Remove init-container targets:
- Remove `ko-build-init`
- Remove `test-init`
- Remove `lint-init`
- Remove `vet-init`

Add runner stub target:
- `ko-build-runner` (or similar)

#### 5. Update .ko.yaml
Add entry for runner binary if needed.

### Success Criteria:

#### Automated Verification:
- [ ] `make build` compiles (both binaries)
- [ ] `make test` passes — runner stub tests pass
- [ ] `make lint` passes
- [ ] `cmd/shepherd-init/` directory does not exist
- [ ] `make build-smoke` passes

---

## Testing Strategy

### Unit Tests:
- Sandbox builder: construction, validation, owner refs (Phase 2)
- Failure classification: sandbox condition parsing (Phase 5)
- Decompression: roundtrip, limits, error cases (Phase 7)
- Token generation: JWT creation, exchange mocking (Phase 7)

### Integration Tests (envtest):
- Reconciler: full lifecycle Pending → Running → Succeeded/Failed (Phases 3-5)
- Task assignment: HTTP POST to runner, idempotency via Running condition (Phase 4)
- Timeout: operator-managed timeout detection (Phase 5)
- Grace period: sandbox termination does not immediately mark Failed (Phase 5)
- Agent-sandbox types registered in envtest scheme with CRD manifests

### HTTP Handler Tests:
- All API endpoints with fake K8s client (Phases 1, 7)

### NOT in this plan:
- e2e tests requiring real cluster with agent-sandbox controller
- GitHub App integration tests with real GitHub API

## Performance Considerations

- Decompression uses `io.LimitReader` to prevent decompression bombs (10MiB limit)
- SandboxClaim creation is idempotent via owner reference + name matching
- Task assignment is idempotent: Running condition is the marker, runner returns 409 on duplicate POST
- TODO: Rate limiting on API endpoints (deferred — see "What We're NOT Doing")

## Migration Notes

This is a **clean break** — no backwards compatibility:
1. Delete all existing AgentTask CRDs before deploying new version
2. Delete all existing Jobs created by old operator
3. The CRD schema changes are breaking (removed fields, renamed fields)
4. Operator CLI flags change (removed GitHub/image flags, added API URL)
5. API CLI flags change (added GitHub App flags)

## References

- Architecture: `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md`
- PoC learnings: `thoughts/research/2026-01-31-poc-sandbox-learnings.md`
- Agent-sandbox source: `/home/edvin/go/src/github.com/NissesSenap/agent-sandbox`
- Agent-sandbox API types:
  - Sandbox: `sigs.k8s.io/agent-sandbox/api/v1alpha1` (group: `agents.x-k8s.io`)
  - SandboxClaim: `sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1` (group: `extensions.agents.x-k8s.io`)
