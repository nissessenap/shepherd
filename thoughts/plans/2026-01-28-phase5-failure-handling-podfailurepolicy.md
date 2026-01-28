# Phase 5: Failure Handling via podFailurePolicy

## Overview

Implement failure classification and retry logic for the Shepherd operator using Kubernetes `podFailurePolicy` (GA since K8s 1.31). Instead of watching or listing pods to detect OOM/eviction, the operator configures Jobs with `podFailurePolicy` rules that surface failure information directly in Job status conditions. The reconciler parses these conditions to classify failures as OOM, timeout, or application errors, and retries infrastructure failures (eviction/preemption) automatically via Kubernetes.

## Current State Analysis

The reconciler at `internal/controller/agenttask_controller.go:139-158` handles Job failures with a single generic path:

```go
case batchv1.JobFailed:
    if c.Status == corev1.ConditionTrue {
        // Phase 5 will add infrastructure vs application failure distinction
        return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, c.Message)
    }
```

All failures use `ReasonFailed` regardless of cause. No OOM detection, no timeout distinction, no infrastructure retry.

### Key Discoveries:
- `backoffLimit: 0` is already set in `job_builder.go:62` — K8s won't retry on its own
- `ActiveDeadlineSeconds` is already configured (`job_builder.go:65-69`)
- `ReasonTimedOut` constant exists in `api/v1alpha1/conditions.go:28` but is unused
- The existing failure test (`agenttask_controller_test.go:325-367`) simulates Job failure by setting `JobFailed` condition with message `"BackoffLimitExceeded"`
- envtest does NOT run the Job controller, so `podFailurePolicy` rules won't be evaluated automatically — tests must manually set Job status conditions

## Desired End State

A reconciler that:
1. Creates Jobs with `podFailurePolicy` rules (exit code 137 → FailJob, DisruptionTarget → Ignore)
2. Classifies Job failures from condition `Reason` and `Message` fields:
   - `DeadlineExceeded` → timeout
   - `PodFailurePolicy` + `exit code 137` → OOM
   - `BackoffLimitExceeded` / other → application failure
3. Uses distinct condition reasons: `ReasonFailed`, `ReasonTimedOut`, `ReasonOOM`
4. Does NOT need pod RBAC — no `pods: get;list;watch`
5. Does NOT watch pods — no pod informer/cache overhead

### How to Verify

- `make test` passes all unit and envtest tests
- `make manifests` generates RBAC without pod permissions
- `go vet ./...` passes
- Unit tests cover all failure classification branches
- envtest tests cover: OOM → Failed, timeout → TimedOut, application failure → Failed, with correct reasons and messages

## What We're NOT Doing

- Pod watching or pod listing (replaced by podFailurePolicy)
- Infrastructure failure retry with new Jobs (DisruptionTarget → Ignore lets K8s auto-replace the pod; with `backoffLimit: 0` this requires `backoffLimitPerIndex` or adjusting backoffLimit — see design decision below)
- Retry annotations on AgentTask (no longer needed since K8s handles infrastructure retries)
- `failure.go` with `classifyJobFailure` that inspects pods (original Phase 5 plan — replaced)
- `listJobPods` helper (not needed)
- Custom failure reason field (KEP-4443 — alpha, not stable enough)

## Design Decisions

### backoffLimit interaction with podFailurePolicy

The `DisruptionTarget` rule with `action: Ignore` means eviction/preemption failures don't count toward `backoffLimit`. With `backoffLimit: 0`, Kubernetes will still create a replacement pod when an ignored failure occurs because the failure count remains at 0. This is the desired behavior — infrastructure failures get automatic retries without operator intervention.

However, there's a subtlety: with `backoffLimit: 0` and `action: FailJob` for exit code 137, the Job fails immediately on OOM (exit code 137 matches the FailJob rule before backoffLimit is checked). For non-137 exit codes, the pod fails, it counts toward backoffLimit, and since backoffLimit is 0, the Job fails with `BackoffLimitExceeded`. This is correct behavior.

### Failure classification approach

Parse Job condition fields only — no pod inspection:

| Job `Failed` condition `Reason` | Message pattern | Classification | AgentTask Reason |
|--------------------------------|-----------------|----------------|-----------------|
| `DeadlineExceeded` | (any) | Timeout | `TimedOut` |
| `PodFailurePolicy` | contains `exit code 137` | OOM | `OOM` |
| `PodFailurePolicy` | other | Application failure | `Failed` |
| `BackoffLimitExceeded` | (any) | Application failure | `Failed` |
| Other / unknown | (any) | Application failure | `Failed` |

### What about init container OOM?

The `podFailurePolicy` `onExitCodes` rule matches **any container** in the pod by default (when `containerName` is not specified). If the `github-auth` init container is OOMKilled with exit code 137, the same FailJob rule fires. This is correct — an OOMKilled init container should also be reported as OOM.

## Implementation Approach

Three focused changes: (1) add `podFailurePolicy` to Job spec, (2) implement failure classification in the reconciler, (3) add condition constant for OOM.

---

## Phase 5a: Add podFailurePolicy to Job Builder

### Overview

Add `podFailurePolicy` rules to the Job spec in `buildJob()`. This tells Kubernetes to surface exit code 137 as a specific Job condition and to auto-retry pods killed by infrastructure events.

### Changes Required

#### 1. Job Builder Update

**File**: `internal/controller/job_builder.go`

Add `PodFailurePolicy` field to the Job spec, after `ActiveDeadlineSeconds`:

```go
PodFailurePolicy: &batchv1.PodFailurePolicy{
    Rules: []batchv1.PodFailurePolicyRule{
        {
            Action: batchv1.PodFailurePolicyActionFailJob,
            OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
                Operator: batchv1.PodFailurePolicyOnExitCodesOpIn,
                Values:   []int32{137},
            },
        },
        {
            Action: batchv1.PodFailurePolicyActionIgnore,
            OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
                {
                    Type:   corev1.DisruptionTarget,
                    Status: corev1.ConditionTrue,
                },
            },
        },
    },
},
```

Rule ordering matters — first match wins:
1. Exit code 137 → FailJob (OOM before infrastructure check)
2. DisruptionTarget → Ignore (eviction/preemption auto-retries)

#### 2. Job Builder Test Updates

**File**: `internal/controller/job_builder_test.go`

Add test:

```go
func TestBuildJob_PodFailurePolicy(t *testing.T) {
    job, err := buildJob(baseTask(), baseCfg())
    require.NoError(t, err)

    require.NotNil(t, job.Spec.PodFailurePolicy)
    rules := job.Spec.PodFailurePolicy.Rules
    require.Len(t, rules, 2)

    // Rule 0: exit code 137 → FailJob
    assert.Equal(t, batchv1.PodFailurePolicyActionFailJob, rules[0].Action)
    require.NotNil(t, rules[0].OnExitCodes)
    assert.Equal(t, batchv1.PodFailurePolicyOnExitCodesOpIn, rules[0].OnExitCodes.Operator)
    assert.Equal(t, []int32{137}, rules[0].OnExitCodes.Values)

    // Rule 1: DisruptionTarget → Ignore
    assert.Equal(t, batchv1.PodFailurePolicyActionIgnore, rules[1].Action)
    require.Len(t, rules[1].OnPodConditions, 1)
    assert.Equal(t, corev1.DisruptionTarget, rules[1].OnPodConditions[0].Type)
    assert.Equal(t, corev1.ConditionTrue, rules[1].OnPodConditions[0].Status)
}
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes — including new `TestBuildJob_PodFailurePolicy`
- [x] `go vet ./...` clean

#### Manual Verification:
- [ ] Review that rule ordering is correct (exit code 137 before DisruptionTarget)

---

## Phase 5b: Implement Failure Classification in Reconciler

### Overview

Replace the generic failure handling in `reconcileJobStatus` with condition-based classification. Add `ReasonOOM` constant. No new files needed — changes go into existing controller and conditions files.

### Changes Required

#### 1. Add OOM Condition Constant

**File**: `api/v1alpha1/conditions.go`

Add `ReasonOOM` constant:

```go
const (
    ConditionSucceeded = "Succeeded"

    ReasonPending   = "Pending"
    ReasonRunning   = "Running"
    ReasonSucceeded = "Succeeded"
    ReasonFailed    = "Failed"
    ReasonTimedOut  = "TimedOut"
    ReasonOOM       = "OOM"
    ReasonCancelled = "Cancelled"
)
```

#### 2. Add Failure Classification Function

**File**: `internal/controller/agenttask_controller.go`

Add a `classifyJobFailure` function that examines the `JobFailed` condition:

```go
// failureClass represents the type of Job failure.
type failureClass int

const (
    failureApplication failureClass = iota // Non-zero exit — permanent
    failureOOM                              // Exit code 137 (SIGKILL/OOM) — permanent
    failureTimeout                          // ActiveDeadlineSeconds exceeded — permanent
)

// classifyJobFailure examines a Job's Failed condition to determine the failure type.
// It relies on podFailurePolicy surfacing exit code 137 as reason "PodFailurePolicy"
// with a message containing "exit code 137".
func classifyJobFailure(cond batchv1.JobCondition) failureClass {
    switch cond.Reason {
    case "DeadlineExceeded":
        return failureTimeout
    case "PodFailurePolicy":
        if strings.Contains(cond.Message, "exit code 137") {
            return failureOOM
        }
        return failureApplication
    default:
        return failureApplication
    }
}
```

#### 3. Update reconcileJobStatus

**File**: `internal/controller/agenttask_controller.go`

Replace the current `reconcileJobStatus` (lines 139-158):

```go
func (r *AgentTaskReconciler) reconcileJobStatus(ctx context.Context, task *toolkitv1alpha1.AgentTask, job *batchv1.Job) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    for _, c := range job.Status.Conditions {
        switch c.Type {
        case batchv1.JobComplete:
            if c.Status == corev1.ConditionTrue {
                return r.markSucceeded(ctx, task, "Job completed successfully")
            }
        case batchv1.JobFailed:
            if c.Status == corev1.ConditionTrue {
                fc := classifyJobFailure(c)
                switch fc {
                case failureOOM:
                    return r.markFailed(ctx, task, toolkitv1alpha1.ReasonOOM,
                        "Container killed: exit code 137 (OOM)")
                case failureTimeout:
                    return r.markFailed(ctx, task, toolkitv1alpha1.ReasonTimedOut,
                        "Job exceeded timeout")
                default:
                    return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, c.Message)
                }
            }
        }
    }

    log.V(1).Info("job still running", "job", job.Name)
    return ctrl.Result{}, nil
}
```

#### 4. Add `strings` Import

Add `"strings"` to the import block in `agenttask_controller.go`.

### Success Criteria

#### Automated Verification:
- [x] `make test` passes
- [x] `go vet ./...` clean
- [x] `make generate && make manifests` succeeds (no RBAC changes needed — no pod permissions)

#### Manual Verification:
- [ ] Review that RBAC in `config/rbac/role.yaml` does NOT include `pods` resource

---

## Phase 5c: Add Unit and Integration Tests for Failure Classification

### Overview

Add unit tests for `classifyJobFailure` and update envtest integration tests to cover OOM and timeout scenarios.

### Changes Required

#### 1. Unit Tests for classifyJobFailure

**File**: `internal/controller/agenttask_controller_test.go` (or a new `failure_test.go` — either is fine, but keeping it in the existing test file matches the pattern of testing controller logic there)

Since `classifyJobFailure` is a pure function, use table-driven tests with testify:

**File**: `internal/controller/failure_test.go` (new file — keeps failure classification tests separate from integration tests)

```go
package controller

import (
    "testing"

    "github.com/stretchr/testify/assert"
    batchv1 "k8s.io/api/batch/v1"
)

func TestClassifyJobFailure(t *testing.T) {
    tests := []struct {
        name     string
        cond     batchv1.JobCondition
        expected failureClass
    }{
        {
            name: "DeadlineExceeded → timeout",
            cond: batchv1.JobCondition{
                Type:   batchv1.JobFailed,
                Reason: "DeadlineExceeded",
            },
            expected: failureTimeout,
        },
        {
            name: "PodFailurePolicy with exit code 137 → OOM",
            cond: batchv1.JobCondition{
                Type:    batchv1.JobFailed,
                Reason:  "PodFailurePolicy",
                Message: "Container runner for pod default/my-task-1-job-xyz failed with exit code 137 matching FailJob rule at index 0",
            },
            expected: failureOOM,
        },
        {
            name: "PodFailurePolicy without exit code 137 → application",
            cond: batchv1.JobCondition{
                Type:    batchv1.JobFailed,
                Reason:  "PodFailurePolicy",
                Message: "Container runner for pod default/my-task-1-job-xyz failed with exit code 1 matching FailJob rule at index 0",
            },
            expected: failureApplication,
        },
        {
            name: "BackoffLimitExceeded → application",
            cond: batchv1.JobCondition{
                Type:   batchv1.JobFailed,
                Reason: "BackoffLimitExceeded",
            },
            expected: failureApplication,
        },
        {
            name: "unknown reason → application",
            cond: batchv1.JobCondition{
                Type:   batchv1.JobFailed,
                Reason: "SomeUnknownReason",
            },
            expected: failureApplication,
        },
        {
            name: "empty reason → application",
            cond: batchv1.JobCondition{
                Type: batchv1.JobFailed,
            },
            expected: failureApplication,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            assert.Equal(t, tt.expected, classifyJobFailure(tt.cond))
        })
    }
}
```

#### 2. Integration Tests for OOM and Timeout

**File**: `internal/controller/agenttask_controller_test.go`

Add two new tests in the "When Job lifecycle is managed" context:

```go
It("should set OOM reason when Job fails with PodFailurePolicy exit code 137", func() {
    createAgentTask(taskName, resourceNamespace)
    reconcileToPending()
    jobName := reconcileToRunning()

    By("Simulating PodFailurePolicy OOM failure")
    var job batchv1.Job
    Expect(k8sClient.Get(ctx, client.ObjectKey{
        Namespace: resourceNamespace,
        Name:      jobName,
    }, &job)).To(Succeed())

    now := metav1.Now()
    job.Status.StartTime = &now
    job.Status.Conditions = append(job.Status.Conditions,
        batchv1.JobCondition{
            Type:   batchv1.JobFailureTarget,
            Status: corev1.ConditionTrue,
        },
        batchv1.JobCondition{
            Type:    batchv1.JobFailed,
            Status:  corev1.ConditionTrue,
            Reason:  "PodFailurePolicy",
            Message: "Container runner for pod default/test-pod failed with exit code 137 matching FailJob rule at index 0",
        },
    )
    Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

    By("Reconciling after OOM failure")
    result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
    Expect(err).NotTo(HaveOccurred())
    Expect(result.RequeueAfter).To(BeZero())

    By("Verifying task has OOM reason")
    var task toolkitv1alpha1.AgentTask
    Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

    cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    Expect(cond).NotTo(BeNil())
    Expect(cond.Status).To(Equal(metav1.ConditionFalse))
    Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonOOM))
    Expect(cond.Message).To(ContainSubstring("exit code 137"))
    Expect(task.Status.CompletionTime).NotTo(BeNil())
    Expect(task.Status.Result.Error).To(ContainSubstring("exit code 137"))
})

It("should set TimedOut reason when Job fails with DeadlineExceeded", func() {
    createAgentTask(taskName, resourceNamespace)
    reconcileToPending()
    jobName := reconcileToRunning()

    By("Simulating timeout failure")
    var job batchv1.Job
    Expect(k8sClient.Get(ctx, client.ObjectKey{
        Namespace: resourceNamespace,
        Name:      jobName,
    }, &job)).To(Succeed())

    now := metav1.Now()
    job.Status.StartTime = &now
    job.Status.Conditions = append(job.Status.Conditions,
        batchv1.JobCondition{
            Type:    batchv1.JobFailed,
            Status:  corev1.ConditionTrue,
            Reason:  "DeadlineExceeded",
            Message: "Job was active longer than specified deadline",
        },
    )
    Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

    By("Reconciling after timeout")
    result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
    Expect(err).NotTo(HaveOccurred())
    Expect(result.RequeueAfter).To(BeZero())

    By("Verifying task has TimedOut reason")
    var task toolkitv1alpha1.AgentTask
    Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

    cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    Expect(cond).NotTo(BeNil())
    Expect(cond.Status).To(Equal(metav1.ConditionFalse))
    Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonTimedOut))
    Expect(cond.Message).To(ContainSubstring("timeout"))
    Expect(task.Status.CompletionTime).NotTo(BeNil())
})
```

#### 3. Update Existing Failure Test

**File**: `internal/controller/agenttask_controller_test.go`

The existing "should set Failed when Job fails" test (line 325) uses a `BackoffLimitExceeded` message but no `Reason` field on the condition. Update it to include `Reason: "BackoffLimitExceeded"` to be more realistic, and verify that it still maps to `ReasonFailed`:

```go
// Update the existing condition setup in the failure test:
batchv1.JobCondition{
    Type:    batchv1.JobFailed,
    Status:  corev1.ConditionTrue,
    Reason:  "BackoffLimitExceeded",
    Message: "Job has reached the specified backoff limit",
},
```

Also update the assertion to match the new message:
```go
Expect(task.Status.Result.Error).To(Equal("Job has reached the specified backoff limit"))
```

#### 4. Integration Test for podFailurePolicy on Created Job

**File**: `internal/controller/agenttask_controller_test.go`

Add assertion in the existing "should create a Job on second reconcile" test to verify `podFailurePolicy` is present on the created Job:

```go
By("Verifying podFailurePolicy is configured")
Expect(job.Spec.PodFailurePolicy).NotTo(BeNil())
Expect(job.Spec.PodFailurePolicy.Rules).To(HaveLen(2))
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes all tests
- [x] `go vet ./...` clean
- [x] Unit tests cover: timeout, OOM, application failure (3+ reasons), unknown reason, empty reason
- [x] envtest: OOM failure → `ReasonOOM` condition
- [x] envtest: timeout failure → `ReasonTimedOut` condition
- [x] envtest: application failure → `ReasonFailed` condition
- [x] envtest: created Job has `podFailurePolicy` with 2 rules

#### Manual Verification:
- [ ] Review that failure classification matches the table in Design Decisions
- [ ] Verify no pod RBAC in generated `config/rbac/role.yaml`

**After all automated verification passes, pause for manual review.**

---

## Testing Strategy

### Unit Tests (testify)
- `classifyJobFailure`: all failure types correctly identified (6 cases)
- `buildJob`: podFailurePolicy rules present and correct (2 rules)

### Integration Tests (envtest + gomega)
- OOM lifecycle: AgentTask → Running → OOM failure → `ReasonOOM`
- Timeout lifecycle: AgentTask → Running → timeout → `ReasonTimedOut`
- Application failure lifecycle: AgentTask → Running → application failure → `ReasonFailed` (existing test, updated)
- Job creation: created Job includes `podFailurePolicy`

### Why NOT e2e

envtest runs a real kube-apiserver that accepts and validates `podFailurePolicy` fields. The Job controller does not run in envtest, but that's acceptable because:
1. `podFailurePolicy` is GA in K8s 1.31+ — we trust Kubernetes evaluates rules correctly
2. What we test is our operator's reaction to Job conditions, which we simulate by manually setting Job status
3. The Kubernetes Job controller's message format (`"Container %s for pod %s/%s failed with exit code %v matching %v rule at index %d"`) is stable and documented

## Performance Considerations

- No pod informer/cache — zero additional memory overhead
- No pod RBAC — reduced attack surface
- `DisruptionTarget → Ignore` handled by Kubernetes automatically — no operator reconciliation needed for infrastructure retries
- `podFailurePolicy` evaluation happens in the Job controller, not in our operator

## References

- Research: `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md`
- Original plan: `thoughts/plans/2026-01-27-operator-implementation.md` (Phase 5, lines 1124-1337)
- Current controller: `internal/controller/agenttask_controller.go:139-158`
- Current job builder: `internal/controller/job_builder.go:111-113`
- Condition constants: `api/v1alpha1/conditions.go`
- K8s podFailurePolicy docs: https://kubernetes.io/docs/tasks/job/pod-failure-policy/
- KEP-3329 (podFailurePolicy GA): https://github.com/kubernetes/enhancements/tree/master/keps/sig-apps/3329-retriable-and-non-retriable-failures
