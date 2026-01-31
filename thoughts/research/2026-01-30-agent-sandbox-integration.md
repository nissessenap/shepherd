---
date: 2026-01-30T08:42:09+01:00
researcher: claude
git_commit: 8d4daca72879f7af97fcbfa29869ca4aa8165d62
branch: review_sandbox
repository: shepherd
topic: "How could kubernetes-sigs/agent-sandbox be used inside Shepherd?"
tags: [research, security, sandbox, agent-sandbox, kubernetes, isolation, gvisor, kata-containers, warm-pools, snapshots]
status: complete
last_updated: 2026-01-30
last_updated_by: claude
last_updated_note: "Revised analysis — deeper look at Option A (full integration) and how SandboxClaim/SandboxTemplate/WarmPool map to Shepherd's execution model"
---

# Research: How could kubernetes-sigs/agent-sandbox be used inside Shepherd?

**Date**: 2026-01-30T08:42:09+01:00
**Researcher**: claude
**Git Commit**: 8d4daca72879f7af97fcbfa29869ca4aa8165d62
**Branch**: review_sandbox
**Repository**: shepherd

## Research Question

How could kubernetes-sigs/agent-sandbox be integrated into Shepherd to improve security, performance, and environment management? Specifically, replacing K8s Jobs with Sandbox resources to gain gVisor/Kata isolation, warm pools, SandboxTemplate-based environment management, and future snapshot capabilities.

## Summary

Agent-Sandbox (v0.1.0, alpha, SIG Apps subproject) provides CRDs and controllers for managing isolated AI agent runtimes in Kubernetes. It offers gVisor/Kata isolation, warm pools for sub-second startup, and a template system for managing sandbox archetypes.

Shepherd's current coupling to the K8s Job API is **moderate** — the operator uses `backoffLimit: 0` (no K8s retries), handles all completion/failure logic itself, and the Job-specific features being used (activeDeadlineSeconds, podFailurePolicy) have straightforward equivalents when managing pods directly. This makes a full migration from Jobs to Sandbox resources feasible.

The integration maps well:
- **SandboxTemplate** → per-language runner environments (Go, Python, Node.js)
- **SandboxClaim** → ephemeral one-off task execution (create, use, cleanup)
- **SandboxWarmPool** → pre-warmed runners for sub-second task startup
- **Sandbox Router** → stable network endpoint for runner-to-API callbacks
- **Snapshots (future)** → base environments with repos pre-cloned and dependencies installed

## What is agent-sandbox?

### Project Overview

- **Repository**: https://github.com/kubernetes-sigs/agent-sandbox
- **API Group**: `agents.x-k8s.io/v1alpha1` (core), `extensions.agents.x-k8s.io/v1alpha1` (extensions)
- **Maturity**: Alpha (v0.1.0, released November 2025)
- **Governance**: Official Kubernetes SIG Apps subproject
- **License**: Apache-2.0
- **Stars**: 825+

### Core CRDs

| CRD | API Group | Purpose |
|-----|-----------|---------|
| Sandbox | `agents.x-k8s.io/v1alpha1` | Isolated, stateful singleton pod with stable identity |
| SandboxTemplate | `extensions.agents.x-k8s.io/v1alpha1` | Reusable blueprint for sandbox archetypes |
| SandboxClaim | `extensions.agents.x-k8s.io/v1alpha1` | Transactional request to provision a sandbox from a template |
| SandboxWarmPool | `extensions.agents.x-k8s.io/v1alpha1` | Pre-warmed pool of ready sandboxes for sub-second allocation |

### Key Capabilities

1. **Multi-backend isolation**: gVisor (userspace kernel) and Kata Containers (VM-level) — configured via `runtimeClassName` in pod template
2. **Warm pools**: Pre-warmed sandbox instances for sub-second startup (up to 90% latency reduction)
3. **Lifecycle management**: Create, pause, resume, scheduled shutdown
4. **Stable identity**: Consistent hostname and network presence via Sandbox Router
5. **Persistent storage**: Data survives pod restarts
6. **Ephemeral patterns**: Documentation describes orchestrating "thousands of sandboxes as ephemeral environments, rapidly creating and deleting them"

### Sandbox CRD Example

```yaml
apiVersion: agents.x-k8s.io/v1alpha1
kind: Sandbox
metadata:
  name: my-sandbox
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
      containers:
      - name: my-container
        image: registry.k8s.io/agent-sandbox/python-runtime-sandbox:v0.1.0
  shutdownTime: "2026-01-31T00:00:00Z"
```

### SandboxTemplate + SandboxClaim Pattern

```yaml
# Admin defines environment archetypes
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: go-runner-template
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
      containers:
      - name: runner
        image: shepherd-runner-go:latest
        ports:
        - containerPort: 8888
        readinessProbe:
          httpGet:
            path: "/"
            port: 8888

# Shepherd operator creates claims per task
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxClaim
metadata:
  name: task-abc123-sandbox
spec:
  templateName: go-runner-template
```

### Requirements

- Kubernetes cluster with CRD support (1.24+)
- gVisor or Kata Containers runtime installed and configured on nodes
- RuntimeClass resources configured for the chosen backend
- Agent-sandbox controller installed (`kubectl apply -f` from release manifests)

## Shepherd's Current Job Execution

### What Shepherd Uses from the Job API

Analysis of `internal/controller/agenttask_controller.go` and `internal/controller/job_builder.go` shows the coupling is moderate:

**Job features actively used:**

| Feature | How Used | Sandbox Equivalent |
|---------|----------|-------------------|
| `backoffLimit: 0` | Disable K8s retries (operator handles retries) | Not needed — Sandbox doesn't retry |
| `activeDeadlineSeconds` | Timeout enforcement | `shutdownTime` (absolute time) |
| `podFailurePolicy` (exit 137) | OOM detection | Inspect pod container status directly (terminated reason + exit code) |
| `podFailurePolicy` (DisruptionTarget) | Ignore eviction | Sandbox controller handles pod disruption |
| `JobComplete` condition | Success detection | Watch pod phase (Succeeded) or Sandbox status |
| `JobFailed` condition + reason | Failure classification | Watch pod phase (Failed) + container termination state |
| `RestartPolicy: Never` | No pod restart | Sandbox lifecycle management handles this |
| `.Owns(&batchv1.Job{})` | Watch owned Jobs | `.Owns(&Sandbox{})` or `.Owns(&SandboxClaim{})` |

**Job features NOT used:**
- `completions`, `parallelism` (single pod)
- `job.Status.Active/Succeeded/Failed` (count fields)
- `job.Status.StartTime/CompletionTime` (AgentTask tracks its own)
- `TTLSecondsAfterFinished`
- `CompletionMode`, `Suspend`, `ManualSelector`

### Current Pod Template (simplified)

```
Pod:
  securityContext:
    runAsNonRoot: true
    seccompProfile: RuntimeDefault
    fsGroup: 65532
  initContainers:
    - name: github-auth (shepherd-init image)
      volumes: github-creds (RW), runner-app-key (RO), task-files (RW)
  containers:
    - name: runner (operator-controlled image)
      volumes: github-creds (RO), task-files (RO)
      env: SHEPHERD_* vars
```

## Full Integration: Replacing Jobs with Sandbox (Option A)

### Architecture Overview

```text
                    AgentTask CRD created
                           |
                           v
                  Shepherd Operator
                           |
              +------------+------------+
              |                         |
              v                         v
    SandboxClaim created          (watches Sandbox status)
              |                         |
              v                         |
    Agent-Sandbox Controller            |
              |                         |
    +---------+---------+               |
    |                   |               |
    v                   v               |
  WarmPool          New Sandbox         |
  (claim ready)     (cold start)        |
    |                   |               |
    +----->  Sandbox Ready  <-----------+
                  |
                  v
         Shepherd injects task
         (via API call to runner or
          write to shared volume)
                  |
                  v
         Runner executes task
                  |
                  v
         Container exits → Sandbox
         status updated → Operator
         detects terminal state →
         AgentTask condition updated
```

### What Changes in Shepherd

#### 1. Job Builder → Sandbox/Claim Builder

`internal/controller/job_builder.go` becomes `sandbox_builder.go`. Instead of building a `batchv1.Job`, it builds a `SandboxClaim` (or direct `Sandbox`).

Key mappings:
- Job pod template → Sandbox pod template (same structure)
- `activeDeadlineSeconds` → `shutdownTime` (compute as `time.Now().Add(timeout)`)
- `backoffLimit: 0` → not needed (Sandbox doesn't retry)
- `podFailurePolicy` → not needed (operator inspects pod status directly)
- `RestartPolicy: Never` → handled by Sandbox controller
- Owner reference → same pattern, different resource type

#### 2. Reconciler Status Handling

`agenttask_controller.go`'s `reconcileJobStatus()` becomes `reconcileSandboxStatus()`. Instead of checking `batchv1.JobComplete/JobFailed` conditions, it checks Sandbox status and/or the underlying pod status:

**Success**: Pod phase `Succeeded` (container exited 0)
**Failure**: Pod phase `Failed` + inspect container termination:
- Exit code 137 → OOM
- Sandbox deleted by shutdownTime → Timeout
- Other non-zero exit → Application failure

#### 3. Controller Watch

```go
// Before:
Owns(&batchv1.Job{})

// After:
Owns(&sandboxv1alpha1.SandboxClaim{})
// or Owns(&sandboxv1alpha1.Sandbox{})
```

#### 4. RBAC Expansion

```yaml
- apiGroups: [agents.x-k8s.io]
  resources: [sandboxes, sandboxes/status]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [extensions.agents.x-k8s.io]
  resources: [sandboxclaims, sandboxclaims/status]
  verbs: [get, list, watch, create, update, patch, delete]
```

### SandboxTemplate for Environment Management

This is where the integration becomes particularly clean. Instead of a single operator-level `SHEPHERD_RUNNER_IMAGE` config, Shepherd can reference SandboxTemplates by name:

```yaml
# Cluster admin creates templates for approved environments
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: shepherd-go-runner
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
      containers:
      - name: runner
        image: shepherd-runner-go:latest
        resources:
          requests: {memory: "512Mi", cpu: "500m"}
          limits: {memory: "2Gi", cpu: "2000m"}
---
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: shepherd-python-runner
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
      containers:
      - name: runner
        image: shepherd-runner-python:latest
        resources:
          requests: {memory: "1Gi", cpu: "500m"}
          limits: {memory: "4Gi", cpu: "4000m"}
```

AgentTask CRD could reference a template:

```yaml
spec:
  runner:
    sandboxTemplateName: "shepherd-go-runner"  # instead of image field
    timeout: 30m
```

Benefits:
- Cluster admins control approved environments via SandboxTemplate (security boundary)
- Different resource profiles per language/environment
- Runtime class (gVisor/Kata) configured per template
- Adding a new language environment = creating a new SandboxTemplate (no operator changes)

### Warm Pools

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxWarmPool
metadata:
  name: go-runner-pool
spec:
  templateName: shepherd-go-runner
  minReady: 3    # Keep 3 warm sandboxes
  maxReady: 10   # Scale up to 10
```

When Shepherd creates a SandboxClaim referencing `shepherd-go-runner`, the agent-sandbox controller claims a ready sandbox from the pool → sub-second task startup.

### Init Container Pattern Rethinking

With warm pools, init containers run at pool-warming time (not per-task). Per-task initialization (GitHub tokens, task files) needs a different approach:

**Option 1: Runner pulls task from API**
- Runner container starts, reads `SHEPHERD_TASK_ID` from env
- Calls `GET /api/v1/tasks/{id}` to fetch task details
- API returns task description, context, and generates a GitHub token on-the-fly
- Runner uses token to clone repo

This is cleaner than the current init container pattern and works naturally with warm pools. The runner is already expected to call back to the API for status updates.

**Option 2: Operator writes task data via Sandbox Router**
- Sandbox is ready, operator sends task payload via HTTP to the runner through the Sandbox Router
- Runner receives task, fetches GitHub token from API, begins work

**Option 3: Init container without warm pools**
- Keep init containers for cold-start sandboxes (no warm pool)
- Accept that warm pool sandboxes skip init and use Option 1 instead
- Shepherd can support both paths

### Failure Detection Without podFailurePolicy

Currently Shepherd relies on Job's `podFailurePolicy` to classify failures via Job condition reasons. With Sandbox, the operator inspects the pod directly:

```go
func classifySandboxFailure(pod *corev1.Pod) failureClass {
    for _, cs := range pod.Status.ContainerStatuses {
        if cs.Name != "runner" {
            continue
        }
        if cs.State.Terminated == nil {
            continue
        }
        if cs.State.Terminated.ExitCode == 137 {
            if cs.State.Terminated.Reason == "OOMKilled" {
                return failureOOM
            }
            // Could also be SIGKILL from shutdownTime
            return failureOOM
        }
        if cs.State.Terminated.ExitCode != 0 {
            return failureApplication
        }
    }
    return failureUnknown
}
```

This is actually more direct than the current approach (which checks Job condition reasons that are string-based). Inspecting `ContainerStatus.State.Terminated.Reason == "OOMKilled"` is more reliable than parsing `"exit code 137"` from a Job condition message.

### Timeout Handling

```go
shutdownTime := metav1.NewTime(time.Now().Add(task.Spec.Runner.Timeout.Duration))
sandbox.Spec.ShutdownTime = &shutdownTime
```

When shutdownTime expires, the agent-sandbox controller terminates the sandbox. The operator detects the sandbox is gone (or in terminal state) and checks whether the runner completed successfully. If the runner didn't report completion before shutdown, it's a timeout.

## Snapshot Capabilities (Future)

### Current State

- **GKE Pod Snapshots**: In limited preview (early 2026). Works with agent-sandbox. Enables full checkpoint/restore of running pods.
- **Upstream agent-sandbox**: "Deep hibernation" is on the roadmap (saving state to persistent storage, archiving Sandbox object) but not yet implemented.
- **CRIU**: Kubernetes has alpha-level forensic container checkpointing. Agent-sandbox could leverage this.

### How Snapshots Would Benefit Shepherd

1. **Base environment snapshots**: Clone repo, install dependencies, set up tooling → snapshot. New tasks restore from snapshot instead of repeating setup. Minutes of setup → seconds of restore.

2. **Failed task debugging**: Snapshot a failed runner environment for post-mortem analysis without keeping the sandbox running.

3. **Warm pool efficiency**: Instead of keeping live pods warm (consuming resources), snapshot idle sandboxes and restore on demand. Lower cost, same startup speed.

4. **Resume interrupted tasks**: If a task is interrupted (node failure, timeout), restore from last snapshot and continue rather than starting over.

## CRD Changes for AgentTask

To support agent-sandbox integration, the AgentTask CRD would evolve:

```go
type RunnerSpec struct {
    // SandboxTemplateName references a SandboxTemplate for the runner environment.
    // Replaces the Image field — environment is defined by the template.
    // +kubebuilder:validation:Required
    SandboxTemplateName string `json:"sandboxTemplateName"`

    // Timeout is the maximum duration for task execution.
    // Translated to Sandbox shutdownTime.
    // +kubebuilder:default="30m"
    Timeout metav1.Duration `json:"timeout,omitempty"`

    // ServiceAccountName for the sandbox pod.
    ServiceAccountName string `json:"serviceAccountName,omitempty"`

    // Resources override for the runner container.
    // If empty, uses the template defaults.
    Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

type AgentTaskStatus struct {
    // ... existing fields ...
    SandboxName string `json:"sandboxName,omitempty"` // replaces JobName
}
```

## Comparison: Before and After

| Aspect | Current (Jobs) | With agent-sandbox |
|--------|---------------|-------------------|
| **Isolation** | seccomp + non-root + drop capabilities | gVisor userspace kernel or Kata VM |
| **Startup** | Cold start every time (image pull + init) | Sub-second from warm pool |
| **Environment mgmt** | Single operator-level image config | SandboxTemplate per language/runtime |
| **Failure detection** | podFailurePolicy → Job condition reasons | Direct pod container status inspection |
| **Timeout** | activeDeadlineSeconds (relative) | shutdownTime (absolute) |
| **Network** | Ephemeral pod IP | Stable identity via Sandbox Router |
| **Init pattern** | Init container writes files to emptyDir | Runner pulls task from API |
| **Future: snapshots** | Not available | Base env snapshots, checkpoint/restore |
| **Future: pause/resume** | Not available | Native Sandbox capability |
| **Dependencies** | K8s batch/v1 (built-in) | agent-sandbox CRDs + controller (alpha) |

## Risks and Mitigations

### Alpha API Stability

**Risk**: agent-sandbox is v0.1.0 alpha. API may change significantly.
**Mitigation**: Both projects are early. Designing for agent-sandbox now means Shepherd's abstractions align with the community direction. Wrapping agent-sandbox types behind an internal interface allows adapting to API changes without rewriting the operator.

### Two Controllers Managing Pods

**Risk**: Agent-sandbox controller manages Sandbox pods. Shepherd's operator needs to observe and react to status.
**Mitigation**: This is the standard Kubernetes pattern — Shepherd `.Owns()` the SandboxClaim/Sandbox, and watches for status changes. The agent-sandbox controller manages the underlying pod. No different from how Shepherd currently uses Jobs (Job controller manages pods, Shepherd watches Job status).

### Warm Pool + Per-Task Init

**Risk**: Warm pools pre-run pods. Init containers with per-task data (GitHub tokens) don't work with pre-warming.
**Mitigation**: Shift to a runner-pull model where the runner fetches task data from the API on startup. This is architecturally cleaner anyway — the runner already needs to call the API for status updates, so it can also pull task data. The init container's responsibilities (token generation, task file writing) move to the API server.

### gVisor Performance Overhead

**Risk**: gVisor intercepts syscalls via userspace kernel. Could slow down agent workloads.
**Mitigation**: AI agent workloads are primarily I/O-bound (API calls, git operations, file reads). gVisor's overhead is mainly on syscall-heavy compute workloads. Worth benchmarking, but likely negligible for Shepherd's use case.

### Cluster Requirements

**Risk**: Requires gVisor or Kata runtime on cluster nodes. Not all clusters have this.
**Mitigation**: SandboxTemplate can omit `runtimeClassName` for clusters without runtime isolation. Shepherd works with or without it — just with different security profiles.

## Implementation Approach

Given both projects are alpha, a phased approach:

### Phase 1: Abstraction Layer

Introduce an internal interface that abstracts the execution backend:

```go
type ExecutionBackend interface {
    Create(ctx context.Context, task *v1alpha1.AgentTask) error
    GetStatus(ctx context.Context, task *v1alpha1.AgentTask) (*ExecutionStatus, error)
    Delete(ctx context.Context, task *v1alpha1.AgentTask) error
}

type ExecutionStatus struct {
    Phase     ExecutionPhase // Pending, Running, Succeeded, Failed
    Reason    string         // OOM, TimedOut, Failed, Succeeded
    Message   string
    StartTime *metav1.Time
}
```

Implement `JobBackend` first (current behavior), then `SandboxBackend`.

### Phase 2: SandboxTemplate Integration

- Define SandboxTemplates for runner environments
- AgentTask references template name instead of image
- Operator creates SandboxClaim per task

### Phase 3: Runner Pull Model

- Runner container fetches task data from API on startup
- API generates GitHub tokens on behalf of the runner
- Init container no longer needed (or optional for cold-start compatibility)

### Phase 4: Warm Pools

- Create SandboxWarmPool per template
- SandboxClaim claims from warm pool
- Sub-second task startup

### Phase 5: Snapshots (when available)

- Base environment snapshots (repo cloned, deps installed)
- Restore from snapshot for new tasks
- Checkpoint/restore for interrupted tasks

## Code References

- `internal/controller/job_builder.go` — Current Job spec construction (to be replaced/abstracted)
- `internal/controller/agenttask_controller.go:172-204` — `reconcileJobStatus()` and `classifyJobFailure()` (to be adapted)
- `internal/controller/agenttask_controller.go:242-247` — `.Owns(&batchv1.Job{})` watch setup (to be changed)
- `api/v1alpha1/agenttask_types.go` — RunnerSpec and AgentTaskStatus (to be extended)
- `cmd/shepherd-init/` — Init container (responsibilities shift to API in runner-pull model)
- `config/rbac/role.yaml` — RBAC rules (to be expanded for agent-sandbox resources)

## Historical Context (from thoughts/)

- `thoughts/plans/2026-01-28-init-container.md` — Init container implementation (would be rethought in runner-pull model)
- `thoughts/plans/2026-01-27-operator-implementation.md` — Operator implementation with RBAC design
- `thoughts/plans/2026-01-28-phase5-failure-handling-podfailurepolicy.md` — Pod failure policy design (replaced by direct pod status inspection)
- `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md` — OOM detection research (direct container status is actually more reliable)
- `thoughts/reviews/2026-01-28-api-server-plan-review.md` — API server review with security considerations

## Links

- [agent-sandbox GitHub](https://github.com/kubernetes-sigs/agent-sandbox)
- [agent-sandbox docs](https://agent-sandbox.sigs.k8s.io/)
- [agent-sandbox guides](https://agent-sandbox.sigs.k8s.io/docs/guides/)
- [gVisor architecture](https://gvisor.dev/docs/architecture_guide/intro/)
- [gVisor security model](https://gvisor.dev/docs/architecture_guide/security/)
- [Kata Containers + agent-sandbox integration](https://katacontainers.io/blog/kata-containers-agent-sandbox-integration/)
- [GKE agent-sandbox docs](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox)
- [GKE Pod Snapshots + Agent Sandbox blog](https://cloud.google.com/blog/topics/developers-practitioners/agent-factory-recap-supercharging-agents-on-gke-with-agent-sandbox-and-pod-snapshots)
- [GKE Agent Sandbox zero trust security (Medium)](https://medium.com/google-cloud/gke-agent-sandbox-and-gke-pod-snapshots-zero-trust-security-for-ai-agents-at-scale-559261ee20b5)
- [InfoQ coverage](https://www.infoq.com/news/2025/12/agent-sandbox-kubernetes/)
- [Warm pool deep dive](https://pacoxu.wordpress.com/2025/12/02/agent-sandbox-pre-warming-pool-makes-secure-containers-cold-start-lightning-fast/)
- [Google Open Source blog: launch announcement](https://opensource.googleblog.com/2025/11/unleashing-autonomous-ai-agents-why-kubernetes-needs-a-new-standard-for-agent-execution.html)
- [Go SDK request (Issue #227)](https://github.com/kubernetes-sigs/agent-sandbox/issues/227)
- [Kubernetes forensic container checkpointing](https://kubernetes.io/blog/2022/12/05/forensic-container-checkpointing-alpha/)

## Open Questions

1. **Sandbox completion semantics**: What exactly happens to Sandbox status when the main container exits with code 0? The documentation says "automatically deleted after the program runs" — need to verify whether Shepherd can observe the terminal state before deletion.
2. **SandboxClaim owner references**: Can Shepherd's operator set itself as owner of a SandboxClaim for garbage collection? Need to test.
3. **Go SDK**: Issue #227 requests a Go client. Currently only Python SDK exists. Shepherd would need to use the K8s client-go directly to create Sandbox resources (standard for operators anyway).
4. **Warm pool + init containers**: Does agent-sandbox run init containers during pool warming, or only when a sandbox is claimed? This affects whether the runner-pull model is strictly required.
5. **Snapshot timeline**: When will upstream agent-sandbox (not GKE-specific) support checkpoint/restore? Worth tracking the project roadmap.
6. **Performance benchmarking**: gVisor overhead for AI agent workloads (git, API calls, file I/O) — needs measurement but likely negligible.
