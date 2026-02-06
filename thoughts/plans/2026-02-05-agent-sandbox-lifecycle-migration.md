# Agent-Sandbox v0.1.1 Lifecycle Migration Plan

**Status**: Complete

## Overview

Migrate from operator-managed timeout to agent-sandbox's built-in `Lifecycle.ShutdownTime` and `ShutdownPolicy` features, now available in v0.1.1.

## Current State Analysis

### What exists now

1. **SandboxClaim creation** (`internal/controller/sandbox_builder.go`):
   - Creates claims WITHOUT `Lifecycle` field
   - No shutdown time or policy configured

2. **Manual timeout management** (`internal/controller/agenttask_controller.go`):
   - `checkTimeout()` function compares `now > startTime + timeout`
   - `taskTimeout()` returns configured timeout or default 30m
   - Timeout is checked in two places during reconciliation (lines 160-171, 228-235)
   - Operator manually deletes SandboxClaim and marks task as `TimedOut`

3. **go.mod dependency**: Uses `sigs.k8s.io/agent-sandbox v0.1.0`

### What agent-sandbox v0.1.1 provides

From `extensions/api/v1alpha1/sandboxclaim_types.go`:

```go
type Lifecycle struct {
    // ShutdownTime is the absolute time when the SandboxClaim expires.
    // +kubebuilder:validation:Format="date-time"
    ShutdownTime *metav1.Time `json:"shutdownTime,omitempty"`

    // ShutdownPolicy determines the behavior when the SandboxClaim expires.
    // +kubebuilder:default=Retain
    ShutdownPolicy ShutdownPolicy `json:"shutdownPolicy,omitempty"`
}

const (
    ShutdownPolicyDelete ShutdownPolicy = "Delete"  // Deletes everything
    ShutdownPolicyRetain ShutdownPolicy = "Retain"  // Keeps claim, deletes underlying resources
)
```

When a claim expires, agent-sandbox:
1. Sets `Ready=False` with reason `ClaimExpired`
2. Deletes underlying resources (Sandbox, Pod, Service)
3. With `Delete` policy: also deletes the SandboxClaim
4. With `Retain` policy: keeps SandboxClaim for status inspection

## Desired End State

After this plan is complete:

1. **SandboxClaims** are created with:
   - `Lifecycle.ShutdownTime` = creation time + `task.Spec.Runner.Timeout`
   - `Lifecycle.ShutdownPolicy` = `Retain` (so operator can inspect status before cleanup)

2. **Timeout enforcement** is delegated to agent-sandbox:
   - Operator no longer runs periodic timeout checks
   - When claim expires, operator detects `Ready=False` + `ClaimExpired` reason
   - Existing `classifyClaimTermination()` already handles this correctly

3. **Tests** verify:
   - Lifecycle field is set correctly on SandboxClaim creation
   - Timeout scenarios work via `ClaimExpired` reason (not manual checks)

### Verification

```bash
# Automated verification
make test                    # All unit tests pass
make lint                    # No linting errors

# Manual verification
# 1. Create an AgentTask with a short timeout (e.g., 2m)
# 2. Observe SandboxClaim is created with correct Lifecycle.ShutdownTime
# 3. After timeout, confirm Ready=False with reason=ClaimExpired
# 4. Confirm task is marked TimedOut
```

## What We're NOT Doing

- **NOT changing ShutdownPolicy to Delete**: We use `Retain` so the operator can inspect the claim status before cleanup. The claim is still deleted by the operator after marking the task terminal.
- **NOT removing grace period logic**: The grace period for API callback processing remains unchanged.
- **NOT changing the API spec**: `RunnerSpec.Timeout` field remains; we're just delegating enforcement.

## Implementation Approach

The migration is straightforward because:
1. The operator already handles `ClaimExpired` reason correctly in `classifyClaimTermination()`
2. We just need to set the Lifecycle field and remove the redundant manual checks
3. Tests already cover the `ClaimExpired` scenario

## Phase 1: Upgrade agent-sandbox dependency

### Overview
Update go.mod to use agent-sandbox v0.1.1 which includes the Lifecycle feature.

### Changes Required:

#### 1. Update go.mod
**File**: `go.mod`
**Changes**: Update agent-sandbox version from v0.1.0 to v0.1.1

```diff
-	sigs.k8s.io/agent-sandbox v0.1.0
+	sigs.k8s.io/agent-sandbox v0.1.1
```

Run `go mod tidy` to update dependencies.

### Success Criteria:

#### Automated Verification:
- [ ] `go mod tidy` completes without errors
- [ ] `make build` compiles successfully
- [ ] `make test` passes (existing tests still work)

---

## Phase 2: Add Lifecycle to SandboxClaim creation

### Overview
Modify `buildSandboxClaim()` to set the `Lifecycle` field with `ShutdownTime` and `ShutdownPolicy`.

### Changes Required:

#### 1. sandbox_builder.go
**File**: `internal/controller/sandbox_builder.go`
**Changes**: Add Lifecycle configuration to SandboxClaim spec

```go
import (
    "time"
    // ... existing imports
)

func buildSandboxClaim(task *toolkitv1alpha1.AgentTask, cfg sandboxConfig) (*sandboxextv1alpha1.SandboxClaim, error) {
    // ... existing validation ...

    // Calculate shutdown time from task timeout
    timeout := task.Spec.Runner.Timeout.Duration
    if timeout == 0 {
        timeout = 30 * time.Minute // Default timeout
    }
    shutdownTime := metav1.NewTime(time.Now().Add(timeout))
    shutdownPolicy := sandboxextv1alpha1.ShutdownPolicyRetain

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
            Lifecycle: &sandboxextv1alpha1.Lifecycle{
                ShutdownTime:   &shutdownTime,
                ShutdownPolicy: shutdownPolicy,
            },
        },
    }

    // ... existing owner reference setup ...
}
```

#### 2. sandbox_builder_test.go
**File**: `internal/controller/sandbox_builder_test.go`
**Changes**: Add tests for Lifecycle field

```go
func TestBuildSandboxClaim_Lifecycle_DefaultTimeout(t *testing.T) {
    task := baseTask()
    // No timeout set, should use default 30m

    beforeBuild := time.Now()
    claim, err := buildSandboxClaim(task, baseSandboxCfg())
    afterBuild := time.Now()
    require.NoError(t, err)

    require.NotNil(t, claim.Spec.Lifecycle, "Lifecycle should be set")
    require.NotNil(t, claim.Spec.Lifecycle.ShutdownTime, "ShutdownTime should be set")

    // ShutdownTime should be ~30 minutes from now
    expectedMin := beforeBuild.Add(30 * time.Minute)
    expectedMax := afterBuild.Add(30 * time.Minute)
    shutdownTime := claim.Spec.Lifecycle.ShutdownTime.Time
    assert.True(t, shutdownTime.After(expectedMin) || shutdownTime.Equal(expectedMin),
        "ShutdownTime should be at least 30m from build start")
    assert.True(t, shutdownTime.Before(expectedMax) || shutdownTime.Equal(expectedMax),
        "ShutdownTime should be at most 30m from build end")

    assert.Equal(t, sandboxextv1alpha1.ShutdownPolicyRetain, claim.Spec.Lifecycle.ShutdownPolicy)
}

func TestBuildSandboxClaim_Lifecycle_CustomTimeout(t *testing.T) {
    task := baseTask()
    task.Spec.Runner.Timeout = metav1.Duration{Duration: 15 * time.Minute}

    beforeBuild := time.Now()
    claim, err := buildSandboxClaim(task, baseSandboxCfg())
    afterBuild := time.Now()
    require.NoError(t, err)

    require.NotNil(t, claim.Spec.Lifecycle)
    require.NotNil(t, claim.Spec.Lifecycle.ShutdownTime)

    // ShutdownTime should be ~15 minutes from now
    expectedMin := beforeBuild.Add(15 * time.Minute)
    expectedMax := afterBuild.Add(15 * time.Minute)
    shutdownTime := claim.Spec.Lifecycle.ShutdownTime.Time
    assert.True(t, shutdownTime.After(expectedMin) || shutdownTime.Equal(expectedMin))
    assert.True(t, shutdownTime.Before(expectedMax) || shutdownTime.Equal(expectedMax))
}
```

### Success Criteria:

#### Automated Verification:
- [ ] `make test` passes with new Lifecycle tests
- [ ] `make lint` passes

#### Manual Verification:
- [ ] Create an AgentTask and verify SandboxClaim has Lifecycle field set
- [ ] Confirm ShutdownTime is approximately now + task timeout

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation from the human that the manual testing was successful before proceeding to the next phase.

---

## Phase 3: Remove manual timeout enforcement

### Overview
Remove the redundant manual timeout checking from the controller. The timeout is now enforced by agent-sandbox via the Lifecycle field.

### Changes Required:

#### 1. agenttask_controller.go
**File**: `internal/controller/agenttask_controller.go`
**Changes**: Remove manual timeout checks while keeping the termination classification logic

Remove/modify:
1. Remove `checkTimeout()` calls from the reconcile loop (lines 160-171, 228-235)
2. Keep `classifyClaimTermination()` - it already handles `ClaimExpired` correctly
3. Keep `taskTimeout()` - still needed for SandboxClaim Lifecycle calculation
4. Remove the `checkTimeout()` function itself (lines 452-457)
5. Remove timeout-based requeue scheduling (the operator no longer needs to wake up at timeout deadline)

The key insight: when agent-sandbox expires the claim, it sets `Ready=False` with reason `ClaimExpired`. The existing `handleSandboxTermination()` flow will be triggered by the status change, and `classifyClaimTermination()` already maps `ClaimExpired` to `ReasonTimedOut`.

**Before** (simplified):
```go
// In reconcile when Ready=True and isRunning
if checkTimeout(&task) {
    // Manual timeout handling
    return r.markFailed(ctx, &task, ReasonTimedOut, ...)
}
// Requeue at timeout deadline
return ctrl.Result{RequeueAfter: remaining}, nil
```

**After** (simplified):
```go
// In reconcile when Ready=True and isRunning
// No manual timeout check - agent-sandbox will expire the claim
// Requeue for health monitoring (optional, can be longer interval)
return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
```

#### 2. agenttask_controller_test.go
**File**: `internal/controller/agenttask_controller_test.go`
**Changes**: Update test "should mark TimedOut when timeout exceeded"

The test currently manipulates `StartTime` to simulate timeout. After this change, timeout is detected via `ClaimExpired` reason, which is already tested in "should mark TimedOut when ClaimExpired reason on SandboxClaim".

Remove the test "should mark TimedOut when timeout exceeded" (lines 704-741) since:
1. Manual timeout checking is removed
2. Timeout behavior is now tested via the ClaimExpired test
3. The Lifecycle field test in Phase 2 verifies the timeout is configured correctly

### Success Criteria:

#### Automated Verification:
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] No compilation errors

#### Manual Verification:
- [ ] Create an AgentTask with a short timeout (2m)
- [ ] Wait for timeout to expire
- [ ] Confirm SandboxClaim shows Ready=False with reason=ClaimExpired
- [ ] Confirm AgentTask is marked with reason=TimedOut
- [ ] Confirm SandboxClaim is deleted after task becomes terminal

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation from the human that the manual testing was successful before proceeding to the next phase.

---

## Phase 4: Update documentation

### Overview
Update comments and documentation to reflect the new Lifecycle-based timeout.

### Changes Required:

#### 1. agenttask_types.go
**File**: `api/v1alpha1/agenttask_types.go`
**Changes**: Update comment on Timeout field

```go
type RunnerSpec struct {
    // ...

    // Timeout is the maximum duration for task execution.
    // This is translated to SandboxClaim.Lifecycle.ShutdownTime when
    // creating the sandbox. Agent-sandbox enforces the timeout by
    // expiring the claim, which triggers Ready=False with reason=ClaimExpired.
    // +kubebuilder:default="30m"
    // +optional
    Timeout metav1.Duration `json:"timeout,omitzero"`

    // ...
}
```

#### 2. Architecture document
**File**: `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md`
**Changes**: Update the timeout management section and references to agent-sandbox version

Update sections:
- "What Changed From PoC Learnings" - update the note about Lifecycle support
- "Timeout Management" section - rewrite to describe Lifecycle-based approach
- "Tech Stack" - update agent-sandbox version from v0.1.0 to v0.1.1
- Remove any "Future" notes about Lifecycle since it's now implemented

### Success Criteria:

#### Automated Verification:
- [ ] `make lint` passes (no issues with Go comments)
- [ ] Documentation is internally consistent

---

## Testing Strategy

### Unit Tests:
- `TestBuildSandboxClaim_Lifecycle_DefaultTimeout` - verifies 30m default
- `TestBuildSandboxClaim_Lifecycle_CustomTimeout` - verifies custom timeout
- Existing `ClaimExpired` tests cover timeout behavior

### Integration Tests:
- The existing envtest-based controller tests verify the full flow
- `should mark TimedOut when ClaimExpired reason on SandboxClaim` test validates the timeout path

### Manual Testing Steps:
1. Deploy updated operator to a test cluster with agent-sandbox v0.1.1
2. Create an AgentTask with `spec.runner.timeout: 2m`
3. Verify SandboxClaim is created with `lifecycle.shutdownTime` ~2m in future
4. Wait for timeout to expire
5. Verify SandboxClaim status shows `Ready=False`, reason=`ClaimExpired`
6. Verify AgentTask shows `Succeeded=False`, reason=`TimedOut`
7. Verify SandboxClaim is deleted after task becomes terminal

## Migration Notes

- **No CRD changes required**: The AgentTask CRD is unchanged
- **Backwards compatible**: Existing AgentTask resources will work with the new logic
- **Requires agent-sandbox v0.1.1**: The cluster must have agent-sandbox v0.1.1 installed

## References

- agent-sandbox v0.1.1 release: https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v0.1.1
- SandboxClaim Lifecycle types: `~/go/src/github.com/NissesSenap/agent-sandbox/extensions/api/v1alpha1/sandboxclaim_types.go`
- Original TODO in architecture doc: `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md:586`
- Current timeout implementation: `internal/controller/agenttask_controller.go:452-457`
