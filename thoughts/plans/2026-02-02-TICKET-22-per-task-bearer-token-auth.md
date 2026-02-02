# Per-Task Bearer Token Authentication Implementation Plan

## Overview

Add defense-in-depth authentication for runner-facing API endpoints. When a sandbox becomes ready, the operator generates a cryptographically random bearer token, stores its SHA-256 hash in a K8s Secret, and sends the plaintext token to the runner via the `Authorization` header on the task assignment POST. The API server validates this token on all runner-facing endpoints (`/tasks/{id}/data`, `/tasks/{id}/token`, `/tasks/{id}/status`).

## Current State Analysis

**Operator** (`internal/controller/agenttask_controller.go`): Reconciles AgentTask → creates SandboxClaim → waits for Ready → POSTs `TaskAssignment{TaskID, APIURL}` to runner at `:8888/task`. No token generation or Secret management.

**API server** (`pkg/api/`): Three runner-facing endpoints exist with `TODO: Authenticate via per-task bearer token (see #22)` comments:
- `GET /api/v1/tasks/{taskID}/data` (`handler_data.go:33`)
- `GET /api/v1/tasks/{taskID}/token` (`handler_token.go:33`)
- `POST /api/v1/tasks/{taskID}/status` (`handler_status.go:35`)

**RBAC**: Operator has no Secret permissions (`config/rbac/role.yaml`). API server has no Secret permissions (`config/api-rbac/role.yaml`).

**Runner stub** (`cmd/shepherd-runner/main.go`): Accepts `TaskAssignment{TaskID, APIURL}` — no token field or Authorization header handling.

**Task names**: Generated via `fmt.Sprintf("task-%s", rand.String(8))` in `handler_tasks.go:152` — always 13 chars. But tasks can also be created directly via kubectl with arbitrary names up to 63 chars.

### Key Discoveries:
- `sandbox_builder.go:37` validates task name ≤ 63 chars, but no validation protects against names that would make Secret names (`{name}-token`) exceed 63 chars
- The `taskHandler` struct (`handler_tasks.go:58-67`) uses `client.Client` for K8s access — same pattern needed for Secret reads
- The operator's `assignTask()` at `agenttask_controller.go:242-281` constructs an HTTP POST with JSON body — the token will be sent as an `Authorization` header instead of in the body
- The test infrastructure (`agenttask_controller_test.go`) uses `rewriteTransport` to redirect HTTP requests to `httptest.Server` — tests can verify the Authorization header through this mechanism
- The `handler_status.go:35-192` handles status updates from runners. The issue only mentioned `/data` and `/token`, but per user decision, `/status` will also be protected

## Desired End State

- Operator generates a 64-char random token when assigning a task
- Operator stores SHA-256 hash in a K8s Secret named `{task-name}-token`, owned by AgentTask
- Operator sends plaintext token as `Authorization: Bearer <token>` header on the POST to runner
- On crash recovery (Secret already exists), operator deletes and recreates with new hash before POSTing
- API server validates `Authorization: Bearer <token>` on `/data`, `/token`, and `/status` endpoints
- API server looks up the Secret for the task, compares SHA-256(provided token) against stored hash
- API server returns 401 on missing/invalid token
- Operator RBAC includes `create`, `delete`, `get` on Secrets
- API server RBAC includes `get` on Secrets
- Runner stub accepts and stores the bearer token from the Assignment's Authorization header
- Task name validation ensures name + `-token` suffix ≤ 63 chars

### Verification:
- `make test` passes (all unit + envtest integration tests)
- `make lint` passes
- `make build` succeeds
- `make manifests && make generate` produces correct CRD and RBAC YAML
- `make build-smoke` passes

## What We're NOT Doing

- Token rotation (tokens are one-time per assignment; crash recovery creates new tokens)
- Token TTL/expiration (token is valid for the lifetime of the task; Secret is cleaned up via owner reference when AgentTask is deleted)
- Adapter-facing authentication (adapters use the existing HMAC callback mechanism)
- mTLS between operator and runner (NetworkPolicy remains the primary isolation layer)
- Per-task token for the adapter callback (`POST /api/v1/tasks`) — that endpoint is not runner-facing

## Implementation Approach

Three phases, each independently compilable and testable:

1. **Operator-side**: Token generation, Secret management, Authorization header on POST
2. **API-side**: Bearer token validation middleware for runner-facing endpoints
3. **Runner stub + RBAC**: Accept token from Assignment, update RBAC manifests

---

## Phase 1: Operator — Token Generation and Secret Management

### Overview
When the sandbox becomes Ready and the operator is about to assign the task, it first generates a bearer token, stores the hash in a Secret, then sends the token as an Authorization header to the runner.

### Changes Required:

#### 1. Token generation helper
**File**: `internal/controller/token.go` (new)

```go
package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const tokenLength = 64

// generateToken creates a cryptographically random bearer token of the specified length
// and returns both the plaintext token and its SHA-256 hex digest.
func generateToken() (plaintext string, hash string, err error) {
	b := make([]byte, tokenLength/2) // 32 bytes = 64 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating random bytes: %w", err)
	}
	plaintext = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return plaintext, hash, nil
}

// hashToken computes the SHA-256 hex digest of a plaintext token.
func hashToken(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// tokenSecretName returns the deterministic Secret name for a task's bearer token.
func tokenSecretName(taskName string) string {
	// Task names are validated to be ≤57 chars, so this is always ≤63 chars.
	return taskName + "-token"
}
```

#### 2. Task name length validation
**File**: `internal/controller/sandbox_builder.go`

Update the name length check from 63 to 57 to account for the `-token` suffix:

```go
// Change existing check:
if len(claimName) > 57 {
    return nil, fmt.Errorf("task name %q exceeds 57-character limit (must leave room for -token Secret suffix)", claimName)
}
```

Note: Since API-generated task names are always 13 chars (`task-{8random}`), this only affects kubectl-created tasks. The sandbox builder is the right place for this check because it's the first place the operator processes a new task's name, and it already validates name length.

#### 3. Secret creation in reconcile loop
**File**: `internal/controller/agenttask_controller.go`

Add token Secret management between the "SandboxClaim Ready=True" check and the `assignTask()` call. Insert a new method `ensureTokenSecret()`:

```go
// ensureTokenSecret creates or recreates the bearer token Secret for a task.
// Returns the plaintext token on success.
// On crash recovery (Secret already exists from a previous attempt), deletes
// and recreates with a new hash to ensure the new POST carries a matching token.
func (r *AgentTaskReconciler) ensureTokenSecret(
	ctx context.Context,
	task *toolkitv1alpha1.AgentTask,
) (string, error) {
	log := logf.FromContext(ctx)
	secretName := tokenSecretName(task.Name)

	// Check if Secret already exists (crash recovery)
	var existing corev1.Secret
	key := client.ObjectKey{Namespace: task.Namespace, Name: secretName}
	if err := r.Get(ctx, key, &existing); err == nil {
		// Secret exists — delete it (we'll create a new one with fresh token)
		log.Info("token Secret already exists, deleting for fresh token", "secret", secretName)
		if err := r.Delete(ctx, &existing); err != nil && !errors.IsNotFound(err) {
			return "", fmt.Errorf("deleting existing token secret: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return "", fmt.Errorf("checking for existing token secret: %w", err)
	}

	// Generate new token
	plaintext, hash, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}

	// Create Secret with hash, owned by AgentTask
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: task.Namespace,
			Labels: map[string]string{
				"shepherd.io/task":  task.Name,
				"shepherd.io/type":  "task-token",
			},
		},
		Data: map[string][]byte{
			"token-hash": []byte(hash),
		},
	}
	if err := controllerutil.SetControllerReference(task, secret, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on token secret: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		return "", fmt.Errorf("creating token secret: %w", err)
	}
	log.Info("created token Secret", "secret", secretName)

	return plaintext, nil
}
```

#### 4. Update reconcile loop to use token
**File**: `internal/controller/agenttask_controller.go`

In the Ready=True branch (around line 190), before calling `assignTask()`:

```go
// Generate token and create Secret
token, err := r.ensureTokenSecret(ctx, &task)
if err != nil {
    log.Error(err, "failed to ensure token secret")
    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// POST task assignment to the runner (with token in Authorization header)
assignment := TaskAssignment{
    TaskID: task.Name,
    APIURL: r.APIURL,
}
if err := r.assignTask(ctx, sandbox.Status.ServiceFQDN, assignment, token); err != nil {
    // ...existing error handling...
}
```

#### 5. Update `assignTask()` to send Authorization header
**File**: `internal/controller/agenttask_controller.go`

Change the `assignTask()` signature to accept a token parameter and set the Authorization header:

```go
func (r *AgentTaskReconciler) assignTask(ctx context.Context, sandboxFQDN string, assignment TaskAssignment, bearerToken string) error {
    // ... existing code ...
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+bearerToken)
    // ... rest unchanged ...
}
```

#### 6. Update RBAC markers
**File**: `internal/controller/agenttask_controller.go`

Add Secret permissions to the RBAC markers:

```go
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;delete
```

#### 7. Add imports
**File**: `internal/controller/agenttask_controller.go`

Add:
- `corev1 "k8s.io/api/core/v1"`
- `"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"`

#### 8. Cleanup: delete token Secret alongside SandboxClaim
**File**: `internal/controller/agenttask_controller.go`

The owner reference on the Secret means Kubernetes garbage collection will handle deletion when the AgentTask is deleted. No explicit cleanup needed in `cleanupSandboxClaim()` — the Secret lifecycle is tied to the AgentTask, not the SandboxClaim.

#### 9. Tests
**File**: `internal/controller/token_test.go` (new)

Unit tests:
- `generateToken()` returns 64-char hex plaintext
- `generateToken()` returns different tokens on successive calls
- `hashToken()` is consistent (same input → same output)
- `hashToken(plaintext)` matches the hash from `generateToken()`
- `tokenSecretName()` appends `-token` suffix

**File**: `internal/controller/agenttask_controller_test.go`

Update existing tests:
- `setupRunnerMock` helper should capture and verify the `Authorization` header
- "should POST assignment to runner when SandboxClaim Ready=True" — verify `Authorization: Bearer <token>` header is present and non-empty
- "should treat 409 from runner as success" — verify token Secret is created even when runner returns 409
- Add new test: "should create token Secret owned by AgentTask before assignment"
  - Verify Secret exists with name `{task-name}-token`
  - Verify Secret has `token-hash` data key
  - Verify Secret has owner reference to AgentTask
  - Verify Secret has `shepherd.io/task` and `shepherd.io/type` labels
- Add new test: "should delete and recreate token Secret on crash recovery"
  - Pre-create a Secret with `{task-name}-token` name
  - Reconcile → verify old Secret deleted and new one created with different hash
- Update `reconcileToRunning` helper to set up a runner mock that captures the Authorization header

**File**: `internal/controller/sandbox_builder_test.go`

Update existing test for name length validation:
- Change 63-char limit test to 57-char limit
- Add test: name exactly 57 chars should succeed
- Add test: name 58 chars should fail with message about `-token` suffix

### Success Criteria:

#### Automated Verification:
- [ ] `make build` compiles without errors
- [ ] `make test` passes — new token tests and updated assignment tests pass
- [ ] `make lint` passes
- [ ] `make manifests` regenerates RBAC — `config/rbac/role.yaml` includes Secrets `get`, `create`, `delete`

**Implementation Note**: After completing this phase and all automated verification passes, pause for review before proceeding.

---

## Phase 2: API Server — Bearer Token Validation

### Overview
Add middleware/helper to validate `Authorization: Bearer <token>` on runner-facing endpoints. The API server looks up the token Secret for the task, computes SHA-256 of the provided token, and compares against the stored hash.

### Changes Required:

#### 1. Token validation helper
**File**: `pkg/api/auth.go` (new)

```go
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateTaskToken checks the Authorization header against the token hash
// stored in the task's token Secret. Returns nil if valid, an error otherwise.
func validateTaskToken(ctx context.Context, k8sClient client.Client, namespace, taskID string, r *http.Request) error {
	// Extract bearer token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return fmt.Errorf("missing Authorization header")
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return fmt.Errorf("Authorization header must use Bearer scheme")
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return fmt.Errorf("empty bearer token")
	}

	// Look up the token Secret
	secretName := taskID + "-token"
	var secret corev1.Secret
	key := client.ObjectKey{Namespace: namespace, Name: secretName}
	if err := k8sClient.Get(ctx, key, &secret); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("token secret not found for task %s", taskID)
		}
		return fmt.Errorf("looking up token secret: %w", err)
	}

	storedHash, ok := secret.Data["token-hash"]
	if !ok {
		return fmt.Errorf("token secret missing token-hash key")
	}

	// Compare SHA-256 hash of provided token against stored hash
	h := sha256.Sum256([]byte(token))
	providedHash := hex.EncodeToString(h[:])

	if subtle.ConstantTimeCompare([]byte(providedHash), storedHash) != 1 {
		return fmt.Errorf("invalid bearer token")
	}

	return nil
}
```

Key design decisions:
- Uses `subtle.ConstantTimeCompare` to prevent timing attacks
- Returns generic errors to avoid leaking information to attackers
- Takes `client.Client` as parameter (same pattern as `taskHandler`)

#### 2. Add authentication to runner-facing handlers
**File**: `pkg/api/handler_data.go`

Add token validation at the start of `getTaskData()`, after extracting taskID:

```go
// Validate bearer token
if err := validateTaskToken(r.Context(), h.client, h.namespace, taskID, r); err != nil {
    log.V(1).Info("token validation failed", "taskID", taskID, "error", err)
    writeError(w, http.StatusUnauthorized, "unauthorized", "")
    return
}
```

Remove the `TODO: Authenticate via per-task bearer token` comment.

**File**: `pkg/api/handler_token.go`

Same pattern — add `validateTaskToken()` call after extracting taskID. Remove TODO.

**File**: `pkg/api/handler_status.go`

Same pattern — add `validateTaskToken()` call after extracting taskID. Remove TODO.

Note: The status handler currently has no TODO because the original issue only mentioned `/data` and `/token`, but per the user's decision, we're protecting `/status` too.

#### 3. Update API server RBAC
**File**: `config/api-rbac/role.yaml`

Add Secret `get` permission:

```yaml
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
```

#### 4. Tests
**File**: `pkg/api/auth_test.go` (new)

Unit tests using a fake K8s client:
- Valid token returns nil
- Missing Authorization header returns error
- Non-Bearer scheme returns error
- Empty bearer token returns error
- Wrong token returns error (hash mismatch)
- Missing Secret returns error
- Secret without `token-hash` key returns error
- Timing-safe comparison (verify `subtle.ConstantTimeCompare` is used — structural test)

**File**: `pkg/api/handler_data_test.go`

Update existing tests to include valid Authorization header. Add:
- Returns 401 when no Authorization header
- Returns 401 when wrong token
- Returns 200 with valid token (existing tests updated to pass token)

**File**: `pkg/api/handler_token_test.go`

Same pattern — update existing tests with valid Authorization header, add 401 tests.

**File**: `pkg/api/handler_status_test.go`

Same pattern — update existing tests with valid Authorization header, add 401 tests.

### Success Criteria:

#### Automated Verification:
- [ ] `make build` compiles without errors
- [ ] `make test` passes — auth tests and updated handler tests pass
- [ ] `make lint` passes

**Implementation Note**: After completing this phase and all automated verification passes, pause for review before proceeding.

---

## Phase 3: Runner Stub + Final RBAC

### Overview
Update the runner stub to accept and store the bearer token from the operator's Assignment. Update RBAC manifests. Regenerate CRD and RBAC.

### Changes Required:

#### 1. Update runner stub to store token
**File**: `cmd/shepherd-runner/main.go`

Update `TaskAssignment` struct to include the token received from the Authorization header:

```go
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
	Token  string `json:"-"` // Not from JSON body; extracted from Authorization header
}
```

Update the POST handler in `newMux()` to extract the Authorization header:

```go
mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
    r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
    var ta TaskAssignment
    if err := json.NewDecoder(r.Body).Decode(&ta); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }

    // Extract bearer token from Authorization header
    if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
        ta.Token = strings.TrimPrefix(auth, "Bearer ")
    }

    slog.Info("received task assignment", "taskID", ta.TaskID, "apiURL", ta.APIURL, "hasToken", ta.Token != "")
    select {
    case assigned <- ta:
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"status":"accepted"}`))
    default:
        http.Error(w, "task already assigned", http.StatusConflict)
    }
})
```

#### 2. Update runner stub tests
**File**: `cmd/shepherd-runner/main_test.go`

Add test:
- POST /task with Authorization header → token is extracted and available
- POST /task without Authorization header → token field is empty (backwards compatible)

#### 3. Regenerate manifests
Run `make manifests` to regenerate:
- `config/rbac/role.yaml` — should now include Secrets `get`, `create`, `delete`

Verify the generated RBAC is correct.

#### 4. Verify API RBAC manually
**File**: `config/api-rbac/role.yaml`

This was manually edited in Phase 2. Verify it includes:
```yaml
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
```

### Success Criteria:

#### Automated Verification:
- [ ] `make build` compiles without errors
- [ ] `make test` passes — all tests including runner stub pass
- [ ] `make lint` passes
- [ ] `make manifests` generates correct RBAC — `config/rbac/role.yaml` includes Secrets verbs
- [ ] `make build-smoke` passes (kustomize renders correctly)
- [ ] No `TODO.*#22` comments remain in the codebase

#### Manual Verification:
- [ ] `config/rbac/role.yaml` includes `secrets` resource with `get`, `create`, `delete`
- [ ] `config/api-rbac/role.yaml` includes `secrets` resource with `get`
- [ ] Runner stub logs `hasToken: true` when receiving assignment with token

---

## Testing Strategy

### Unit Tests:
- Token generation: randomness, length, consistency (`internal/controller/token_test.go`)
- Token validation: valid/invalid/missing scenarios (`pkg/api/auth_test.go`)
- Secret naming: deterministic naming from task name (`internal/controller/token_test.go`)
- Name length validation: sandbox builder rejects names > 57 chars (`internal/controller/sandbox_builder_test.go`)

### Integration Tests (envtest):
- Full lifecycle: operator creates Secret, sends token in header, Secret has correct hash (`internal/controller/agenttask_controller_test.go`)
- Crash recovery: pre-existing Secret is deleted and recreated (`internal/controller/agenttask_controller_test.go`)
- Owner reference: Secret is owned by AgentTask (GC-safe) (`internal/controller/agenttask_controller_test.go`)

### HTTP Handler Tests:
- Auth middleware rejects unauthenticated requests with 401 (`pkg/api/handler_*_test.go`)
- Auth middleware accepts valid tokens (`pkg/api/handler_*_test.go`)
- Auth middleware uses constant-time comparison (`pkg/api/auth_test.go`)

### NOT in this plan:
- e2e tests with real cluster and agent-sandbox controller
- Performance/load testing of token validation
- Token rotation or expiration

## Performance Considerations

- Token generation uses `crypto/rand` — this is crypto-safe and fast enough for per-task usage
- Token validation does one K8s Secret GET per request — this is a lightweight in-cluster call
- SHA-256 hashing is negligible cost
- `subtle.ConstantTimeCompare` prevents timing side-channels
- No caching of tokens on the API side — each request validates against the Secret (simplicity over premature optimization; the K8s API server caches reads)

## Migration Notes

- **No CRD schema changes** — the token is stored in a K8s Secret, not in the AgentTask status
- **Backwards compatible** — existing tasks without token Secrets will fail auth on runner endpoints (this is intentional; they should no longer be calling those endpoints)
- **RBAC update required** — both operator and API server need updated Roles before deploying
- **Task name length change** — the sandbox builder now rejects names > 57 chars (was 63). This only affects kubectl-created tasks; API-generated names are always 13 chars

## References

- Original issue: https://github.com/nissessenap/shepherd/issues/22
- Migration plan: `thoughts/plans/2026-02-01-operator-api-sandbox-migration.md`
- Architecture: `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md`
