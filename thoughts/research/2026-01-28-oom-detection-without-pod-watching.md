---
date: 2026-01-28T18:13:33Z
researcher: claude
git_commit: e06c618f66a99ef6e3e2c86539742d3bad352535
branch: main
repository: shepherd
topic: "OOM detection alternatives to pod watching for Phase 5 failure handling"
tags: [research, kubernetes, oom, pod-failure-policy, job-status, operator, phase-5]
status: complete
last_updated: 2026-01-28
last_updated_by: claude
---

# Research: OOM Detection Without Pod Watching

**Date**: 2026-01-28T18:13:33Z
**Researcher**: claude
**Git Commit**: e06c618f66a99ef6e3e2c86539742d3bad352535
**Branch**: main
**Repository**: shepherd

## Research Question

Phase 5 of the operator implementation plan proposes watching pods to detect OOMKilled failures. Is there a way to detect OOM without watching all pods? Kubernetes version is 1.34, soon upgrading to 1.35.

## Summary

**Yes, there is a viable alternative.** Using Kubernetes `podFailurePolicy` (GA since K8s 1.31), the operator can configure Jobs to surface exit code 137 (OOM) directly in Job conditions. This eliminates the need for a pod watcher or pod listing in most cases. There is a small trade-off: exit code 137 means SIGKILL (which is _usually_ OOM when memory limits are set), not definitively OOMKilled. For Shepherd's use case, this is an acceptable heuristic.

### Three approaches compared

| Approach | OOM Detection | Pod RBAC | Memory Overhead | Complexity |
|----------|--------------|----------|-----------------|------------|
| **Pod watcher** (plan Phase 5) | Definitive (`Reason: OOMKilled`) | pods: get;list;watch | High (caches all pods) | Moderate |
| **Pod list on failure** (plan Phase 5 alternative) | Definitive | pods: get;list | Low (on-demand only) | Low |
| **podFailurePolicy** (no pods) | Heuristic (exit code 137) | None | None | Lowest |

## Detailed Findings

### 1. podFailurePolicy (GA in K8s 1.31+)

The `podFailurePolicy` field on Job spec allows declaring rules that match pod failures by exit code or pod condition. When a rule matches, Kubernetes writes a specific condition to the Job status.

**Key facts:**
- GA since K8s 1.31; feature gate removed in K8s 1.34
- Available unconditionally on K8s 1.34 and 1.35
- Rules are evaluated in order; first match wins

**How it works for OOM:**

```yaml
podFailurePolicy:
  rules:
  - action: FailJob
    onExitCodes:
      operator: In
      values: [137]        # SIGKILL / OOM exit code
  - action: Ignore
    onPodConditions:
    - type: DisruptionTarget  # Node drain, preemption, eviction
```

When exit code 137 triggers the FailJob rule, the Job gets:

```yaml
status:
  conditions:
  - type: FailureTarget      # Added immediately
    status: "True"
    reason: PodFailurePolicy
    message: "Container runner for pod shepherd/task-1-job-xyz failed
              with exit code 137 matching FailJob rule at index 0"
  - type: Failed              # Added after pod terminates
    status: "True"
    reason: PodFailurePolicy
    message: "Container runner for pod shepherd/task-1-job-xyz failed
              with exit code 137 matching FailJob rule at index 0"
```

**What the operator can infer:**
- `reason: PodFailurePolicy` + `message` containing `exit code 137` + matching rule index 0 = OOM (by convention)
- `reason: DeadlineExceeded` = timeout
- `reason: PodFailurePolicy` + `DisruptionTarget` rule match = infrastructure failure (eviction/preemption)
- `reason: BackoffLimitExceeded` = application failure (non-zero exit, not 137)

### 2. What podFailurePolicy does NOT tell you

The Job condition message includes the exit code but **not** the container's `Terminated.Reason` field (where "OOMKilled" lives). This means:

- Exit code 137 = SIGKILL, which is _usually_ OOM when the container has memory limits
- It could theoretically be a manual `kill -9` or a liveness probe failure
- In practice, for Shepherd's isolated runner containers with memory limits, 137 is reliably OOM

The proposed `ResourceExhausted` pod condition (KEP-3329) was **abandoned** due to:
- CRI-O with cgroupv2 doesn't standardize the OOMKilled reason field
- Ambiguity between container-limit OOM and node memory pressure OOM
- Race conditions with DisruptionTarget

### 3. DisruptionTarget for infrastructure failures

KEP-3329 added the `DisruptionTarget` pod condition (GA in K8s 1.31). Kubernetes sets this condition on pods that are evicted due to:
- Node drain / preemption
- Taint-based eviction
- API-initiated eviction

Using `action: Ignore` for `DisruptionTarget` means those pods don't count toward backoff limit. The Job controller creates a replacement pod automatically (or the operator can handle it).

**This replaces the "pod missing/evicted" detection** from Phase 5's plan without needing to list pods.

### 4. How other operators handle this

**Tekton** watches Pods directly and checks `pod.Status.ContainerStatuses[].State.Terminated.Reason == "OOMKilled"`. This gives definitive detection but requires pod RBAC and a pod informer.

**Argo Workflows** has had long-standing issues with OOM detection reliability (GitHub issues #8456, #13373, #8680), partly because of inconsistent OOMKilled reporting across container runtimes.

Both operators predate `podFailurePolicy` GA. The `podFailurePolicy` approach is newer and avoids the complexity they deal with.

### 5. Pod listing on failure (middle-ground option)

If the heuristic isn't acceptable, the operator could list pods **only when a Job fails**, rather than watching all pods:

```go
// Only called when Job has Failed condition
func (r *AgentTaskReconciler) listJobPods(ctx context.Context, job *batchv1.Job) ([]corev1.Pod, error) {
    var podList corev1.PodList
    if err := r.List(ctx, &podList,
        client.InNamespace(job.Namespace),
        client.MatchingLabels(job.Spec.Selector.MatchLabels),
    ); err != nil {
        return nil, err
    }
    return podList.Items, nil
}
```

This requires `pods: get;list` RBAC but no informer/cache overhead. It's a single API call per failure.

### 6. KEP-4443: Custom failure reasons (Alpha in K8s 1.31)

A future enhancement allows specifying a custom `Reason` field in podFailurePolicy rules:

```yaml
podFailurePolicy:
  rules:
  - action: FailJob
    reason: OutOfMemory  # Custom reason appears in Job condition
    onExitCodes:
      operator: In
      values: [137]
```

This would make the Job condition say `reason: OutOfMemory` instead of generic `reason: PodFailurePolicy`. Status of this feature for K8s 1.34/1.35 is unclear (may still be alpha/beta).

### 7. Memory overhead of pod watching

Adding `Owns(&corev1.Pod{})` or `Watches(&corev1.Pod{})` to the controller creates an informer that caches **all pods** (cluster-wide by default). This can consume significant memory:

- controller-runtime caches objects across all namespaces by default
- Pods are the most numerous and frequently-updated objects in most clusters
- Real-world reports: 2GB+ memory for controllers in large clusters due to caching

Mitigations exist (label selectors, namespace filtering, metadata-only caching) but add complexity.

## Proposed Approach for Phase 5

Use `podFailurePolicy` on Jobs created by the operator, combined with Job condition parsing:

**Job spec changes in `buildJob()`:**
```yaml
podFailurePolicy:
  rules:
  - action: FailJob           # Exit code 137 = OOM → fail immediately
    onExitCodes:
      operator: In
      values: [137]
  - action: Ignore             # Eviction/preemption → don't count, auto-replace
    onPodConditions:
    - type: DisruptionTarget
```

**Reconciler failure classification (replacing `classifyJobFailure`):**

| Job condition reason | Job condition message | Classification |
|---------------------|----------------------|----------------|
| `DeadlineExceeded` | (any) | Timeout |
| `PodFailurePolicy` | contains "exit code 137" | OOM |
| `PodFailurePolicy` | contains "DisruptionTarget" | Infrastructure (auto-retried by K8s) |
| `BackoffLimitExceeded` | (any) | Application failure |
| Other / unknown | (any) | Application failure |

**What this eliminates:**
- No pod watcher / pod informer
- No pod RBAC (no `pods: get;list;watch`)
- No `failure.go` with `classifyJobFailure` that inspects pods
- No `listJobPods` helper
- DisruptionTarget handling is automatic (K8s retries the pod)

**What this changes:**
- OOM detection is heuristic (exit code 137) rather than definitive (`Reason: OOMKilled`)
- Infrastructure failures (eviction) are handled by K8s via `action: Ignore`, not by the operator creating new Jobs

## Code References

- `internal/controller/agenttask_controller.go:139-158` - Current `reconcileJobStatus` that needs failure classification
- `internal/controller/job_builder.go:43-182` - `buildJob()` where `podFailurePolicy` would be added
- `thoughts/plans/2026-01-27-operator-implementation.md:1124-1337` - Phase 5 plan with pod-watching approach

## Architecture Documentation

The current operator uses `Owns(&batchv1.Job{})` in `SetupWithManager` (`agenttask_controller.go:196-201`), which watches Job events and maps them back to the owning AgentTask. Adding `podFailurePolicy` to the Job spec keeps this architecture intact — the operator still reacts to Job condition changes, but the conditions now carry richer failure information.

## Historical Context (from thoughts/)

- `thoughts/plans/2026-01-27-operator-implementation.md` - Phase 5 proposes pod listing (`listJobPods`) and failure classification (`classifyJobFailure`) that inspects pod container statuses. This research proposes replacing that approach.

## Open Questions

1. **KEP-4443 status**: Is the custom `Reason` field in podFailurePolicy available in K8s 1.34 or 1.35? If so, the operator could use `reason: OutOfMemory` for cleaner detection.
2. **Init container OOM**: If the init container (`github-auth`) is OOMKilled, does exit code 137 still propagate? The `onExitCodes` rule can optionally specify `containerName` — we may need rules for both containers.
3. **backoffLimit interaction**: With `backoffLimit: 0` and `podFailurePolicy`, what happens when `DisruptionTarget` fires with `action: Ignore`? The pod should be replaced, but with backoffLimit 0, does K8s create a new pod? (Answer: yes, `Ignore` means the failure doesn't count toward backoffLimit, so K8s replaces it.)
4. **Multiple rule matches**: If a pod is both evicted AND has exit code 137, which rule fires? (Answer: first matching rule wins, so order matters.)
