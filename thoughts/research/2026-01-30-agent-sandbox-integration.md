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
last_updated_note: "Follow-up: init container rethinking, learnings from agent-sandbox codebase, 2026 roadmap, abstraction layer decision"
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

## Open Questions (Resolved)

Most open questions from the initial research have been answered by reading the agent-sandbox source code:

1. **Sandbox completion semantics**: **RESOLVED**. The Sandbox controller does NOT auto-delete when the container exits. The pod remains and the Sandbox transitions to `Ready=False` with reason `DependenciesNotReady`. Deletion only happens via `shutdownTime` + `ShutdownPolicy=Delete`, or explicit deletion of the SandboxClaim. Shepherd can observe the terminal state.

2. **SandboxClaim owner references**: **RESOLVED**. SandboxClaim sets itself as controller owner of the Sandbox via `controllerutil.SetControllerReference()`. Shepherd would own the SandboxClaim (via OwnerReference from AgentTask), giving a clean chain: AgentTask → SandboxClaim → Sandbox → Pod/Service.

3. **Go SDK**: **RESOLVED**. On the 2026 roadmap. For Shepherd's operator, client-go is the natural fit anyway (standard K8s operator pattern). No SDK dependency needed.

4. **Warm pool + init containers**: **RESOLVED**. Init containers ARE run during pool warming. The warm pool controller copies the entire PodSpec from the template verbatim, including initContainers. They complete before the pod is marked ready. When claimed by a SandboxClaim, init containers are already done. This means per-task init containers won't work with warm pools — confirming the runner-pull model is needed.

5. **Snapshot timeline**: **PARTIALLY RESOLVED**. The 2026 roadmap includes "Scale-down / Resume PVC based" — pause/resume preserving PVC only, which is a step toward full snapshots. Full memory snapshots remain GKE-specific for now.

6. **gVisor overhead**: Considered non-issue. Google runs gVisor in production at scale.

## Remaining Open Questions

1. **Sandbox status when container exits with code 0**: The Sandbox becomes `Ready=False` but is NOT deleted. Does the controller distinguish between "container exited cleanly" and "container crashed"? Shepherd would need to inspect pod container status directly (same approach as discussed in failure detection section).

2. **Warm pool pod readiness**: If the runner container in a warm pool pod is an HTTP server waiting for tasks, is the pod marked ready before it has a task? This would require a readiness probe on the runner (which agent-sandbox templates support).

---

## Follow-up Research 2026-01-30T09:30+01:00

### Init Container Pattern: What Needs to Move and Where

The current shepherd-init container has three responsibilities:

| Responsibility | Input | Output | Where it moves |
|---------------|-------|--------|---------------|
| Write task description | `TASK_DESCRIPTION` env var | `/task/description.txt` | API server → runner HTTP endpoint |
| Write task context | `TASK_CONTEXT` + `CONTEXT_ENCODING` env vars | `/task/context.txt` (gzip-decoded) | API server → runner HTTP endpoint |
| Generate GitHub token | Private key file + App ID + Installation ID + Repo URL | `/creds/token` | API server generates token on-demand |

#### Why the Init Container Doesn't Work With Warm Pools

Agent-sandbox warm pools create pods from the SandboxTemplate's PodSpec **verbatim**, including init containers. Init containers run during pool warming (before any task exists). This means:

- Init containers that write per-task data (description, context) have nothing to write yet
- Init containers that generate per-repo GitHub tokens don't know which repo yet
- The warm pod sits ready with completed init containers but no task-specific data

#### The Runner-Pull Model

Instead of pushing data into the pod via init containers, the runner **pulls** data from the API:

```text
                    Shepherd API
                    /           \
      POST /tasks              GET /tasks/{id}
      (adapter creates)        (runner fetches)
           |                        |
           v                        v
      AgentTask CRD            Runner container
           |                   (warm pool pod)
           v                        |
      Operator creates         1. Poll/wait for task assignment
      SandboxClaim             2. GET /tasks/{id} → description, context
           |                   3. GET /tasks/{id}/token → GitHub token
           v                   4. Clone repo, execute task
      Agent-sandbox            5. POST /tasks/{id}/status → progress
      claims warm pod          6. POST /tasks/{id}/status → completion
```

##### Step by step:

1. **Runner starts** (from warm pool or cold start). It exposes an HTTP API (like agent-sandbox's python-runtime-sandbox pattern: `/execute`, `/upload`, `/download`). The runner is idle, waiting.

2. **Task arrives** → Adapter calls API → API creates AgentTask → Operator creates SandboxClaim.

3. **Agent-sandbox claims a warm pod**. Shepherd operator detects the Sandbox is ready (watches SandboxClaim status for `Ready=True`).

4. **Operator notifies the runner** via HTTP through the Sandbox Router:
   - `POST http://{sandbox-name}.{namespace}.svc.cluster.local:8888/task` with task ID and API endpoint
   - Or: operator sets an annotation/label on the Sandbox that the runner watches

5. **Runner pulls task data from API**:
   - `GET /api/v1/tasks/{id}` → returns description, context (API decompresses gzip)
   - `GET /api/v1/tasks/{id}/token` → API generates GitHub installation token on-the-fly using the Runner App private key (the key stays in the API server, never in pods)

6. **Runner executes**: clones repo with token, runs Claude Code, POSTs status updates to API.

7. **Runner finishes**: container exits (or stays alive for the next task in a future multi-task model). Shepherd operator detects via Sandbox/pod status change.

##### Why This Is Better Than Init Containers (Even Without Warm Pools)

- **Private key never leaves the API server**. Today, the init container mounts the Runner App private key as a volume. In the pull model, the API server holds the key and generates tokens. The runner only ever sees short-lived installation tokens, never the private key.

- **Context decompression happens in the API**, not in the pod. The API already handles gzip compression when creating the CRD. It can decompress when serving to the runner.

- **Single source of truth**. The runner gets data from one place (the API) instead of from environment variables, mounted files, and volumes.

- **No emptyDir volume coordination**. The current pattern requires careful UID/fsGroup matching between init and runner containers. The pull model eliminates this.

- **Testability**. The API endpoint can be tested with standard HTTP tests. The init container requires integration testing with shared volumes.

### Learnings From agent-sandbox Implementation

#### 1. NetworkPolicy Per Sandbox (Default Deny)

Agent-sandbox creates a `NetworkPolicy` per SandboxClaim **before** the pod starts (`sandboxclaim_controller.go:192-197`). The policy uses the claim UID as pod selector, ensuring each sandbox has its own firewall:

```yaml
spec:
  podSelector:
    matchLabels:
      agents.x-k8s.io/claim-uid: {claim-uid}
  policyTypes: [Ingress, Egress]
  # Template defines allowed rules
```

**For Shepherd**: Each runner pod should have a NetworkPolicy that:
- Allows egress to the Shepherd API (for callbacks and task data)
- Allows egress to GitHub API (for cloning and PR creation)
- Allows egress to the AI provider API (Anthropic for Claude Code)
- Denies all other egress (prevents data exfiltration)
- Denies all ingress (runner doesn't need to receive connections)

This is defined in the SandboxTemplate's `networkPolicy` field. Shepherd-specific templates would include the right rules.

#### 2. AutomountServiceAccountToken Defaults to False

Agent-sandbox explicitly sets `AutomountServiceAccountToken: false` if not specified (`sandboxclaim_controller.go:446-452`). This prevents the runner from accessing the K8s API.

**For Shepherd**: Already good practice, and agent-sandbox enforces it by default.

#### 3. HTTP-Based Communication (Not Exec)

Agent-sandbox's runtime pattern uses HTTP APIs (`/execute`, `/upload`, `/download`) rather than `kubectl exec`. Each sandbox gets a headless service (`ClusterIP: None`) for DNS-based routing, and the Sandbox Router uses `X-Sandbox-ID` headers to proxy requests.

**For Shepherd**: The runner image should expose an HTTP API. Shepherd communicates via the Sandbox Router or directly via the headless service FQDN (`{sandbox-name}.{namespace}.svc.cluster.local`).

#### 4. ShutdownPolicy: Delete vs Retain

SandboxClaim supports `ShutdownPolicy`:
- `Delete`: claim + sandbox + pod all deleted when shutdownTime expires
- `Retain`: pod and sandbox deleted (save resources), but SandboxClaim kept (audit trail)

**For Shepherd**: Use `ShutdownPolicy: Retain` with `shutdownTime = now + timeout`. When the timeout fires, the pod is killed but the SandboxClaim stays. Shepherd can inspect it to determine timeout vs completion. For successful tasks, explicitly delete the SandboxClaim.

#### 5. Warm Pool Pod Adoption Pattern

When a SandboxClaim is created, it calls `tryAdoptPodFromPool()` which:
1. Lists pods matching the template hash label
2. Removes warm pool owner references
3. Adds sandbox labels and claim UID label
4. The Sandbox controller then adopts the pod

**For Shepherd**: This is transparent. Shepherd creates SandboxClaim → agent-sandbox handles adoption.

#### 6. Owner Reference Chain

```
SandboxClaim → owns → Sandbox → owns → Pod, Service, PVC
                                  ↕
                         SandboxClaim → owns → NetworkPolicy
```

**For Shepherd**: Add AgentTask to the chain:
```
AgentTask → owns → SandboxClaim → owns → Sandbox → owns → Pod, Service
```

Deleting an AgentTask cascades all the way down.

### 2026 Roadmap Highlights (from PR #259)

Key items relevant to Shepherd:

| Roadmap Item | Relevance to Shepherd |
|-------------|----------------------|
| **Implement Go Client (#227)** | Shepherd is Go — direct SDK usage |
| **Scale-down / Resume PVC based** | Pause/resume runners, preserve workspace. Step toward snapshots |
| **Auto-deletion of bursty sandboxes** | Directly useful for Shepherd's run-to-completion tasks |
| **Status Updates (#119)** | Better status reporting reduces need for Shepherd to inspect pods |
| **Startup Actions (#58)** | Could allow "start paused, resume when task assigned" pattern |
| **Creation Latency Metrics (#123)** | Observability for task startup time |
| **Decouple API from Runtime** | Full customization of runner environment |
| **API Support for Multi-Sandbox per Pod** | Not immediately relevant but interesting for sidecar patterns |
| **Falco configuration extension** | Security auditing for sandboxed runners |
| **Beta/GA versions** | API stability for production use |

The "Auto-deletion of bursty sandboxes" item is particularly interesting — it suggests the project is actively thinking about ephemeral/short-lived sandbox patterns, which is exactly Shepherd's use case.

"Startup Actions" (#58) could enable a pattern where warm pool sandboxes start paused and are resumed when a task is assigned, avoiding the idle HTTP server model.

### Should We Introduce an ExecutionBackend Interface?

**No. Build directly for agent-sandbox.**

The abstraction is overhead that doesn't serve Shepherd's goals:

1. **Both projects are greenfield alpha**. Designing an abstraction to support a backend you're about to remove (Jobs) adds code and complexity for no gain.

2. **The interface would be wrong anyway**. A good abstraction emerges from having two concrete implementations. Designing it upfront from one implementation (Jobs) will produce an interface shaped like Jobs, which won't fit Sandbox well.

3. **Reversibility is cheap**. If you later need to support Jobs (e.g., for clusters without agent-sandbox), adding a second backend to a Sandbox-native design is straightforward. The reconciler logic is ~200 lines.

4. **Agent-sandbox IS the abstraction**. SandboxTemplate already abstracts over runtime classes, resource profiles, and security configs. SandboxClaim abstracts over warm pool vs cold start. Adding another layer on top is premature.

**Recommendation**: Build the operator directly against agent-sandbox CRDs. If you ever need a Job fallback, extract the interface then. You'll have two concrete implementations to inform the design.

### Revised Data Flow With Agent-Sandbox

```text
1. Developer comments "@shepherd fix the null pointer"
         |
         v
2. GitHub webhook → github-adapter (Trigger App)
         |
         v
3. Adapter checks API for active tasks on this repo+issue
         |
         v
4. Adapter extracts context, POST /api/v1/tasks to API
         |
         v
5. API validates, gzip-compresses context, creates AgentTask CRD
         |
         v
6. Operator sees new AgentTask, creates SandboxClaim
   referencing SandboxTemplate (e.g. "shepherd-go-runner")
         |
         v
7. Agent-sandbox controller:
   - Claims warm pod from SandboxWarmPool (or creates new)
   - Creates headless Service
   - Creates NetworkPolicy (from template)
   - SandboxClaim status → Ready=True
         |
         v
8. Operator detects SandboxClaim ready, notifies runner:
   POST http://{sandbox}.{ns}.svc.cluster.local:8888/task
   with: { taskID, apiEndpoint }
         |
         v
9. Runner pulls task from API:
   GET /api/v1/tasks/{id} → description, context
   GET /api/v1/tasks/{id}/token → GitHub installation token
         |
         v
10. Runner clones repo, Claude Code works on task
    POST /api/v1/tasks/{id}/status → progress updates
    API watches CRD status, notifies adapter via callback
         |
         v
11. Claude Code creates PR, runner reports completion
    POST /api/v1/tasks/{id}/status → { event: completed, pr_url: ... }
         |
         v
12. Operator detects runner completion (pod status or API callback)
    Updates AgentTask to Succeeded=True
    Deletes SandboxClaim → cascades to Sandbox → Pod → Service
         |
         v
13. API's CRD status watcher detects terminal state
    POSTs to adapter callback → adapter posts final GitHub comment
```

### What Happens to shepherd-init?

The `cmd/shepherd-init/` module and its code would be retired. Its responsibilities redistribute:

| Current (init container) | New (API server) |
|-------------------------|------------------|
| Read TASK_DESCRIPTION env → write /task/description.txt | API serves `GET /tasks/{id}` returning description |
| Read TASK_CONTEXT env → gzip-decode → write /task/context.txt | API serves `GET /tasks/{id}` returning decoded context |
| Read private key → create JWT → exchange for GitHub token → write /creds/token | API serves `GET /tasks/{id}/token`, generates token using its own copy of the private key |

The `taskfiles.go` decompression logic moves to the API's task-serving endpoint. The `github.go` token generation logic moves to an API endpoint. The private key is mounted into the API server pod (not individual runner pods), which is a security improvement — one key location vs. N runner pods.

The runner image becomes simpler: it's just an HTTP server that receives task assignments and has Claude Code + git + language tools pre-installed.
