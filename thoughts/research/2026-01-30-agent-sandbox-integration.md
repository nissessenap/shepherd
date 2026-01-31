---
date: 2026-01-30T08:42:09+01:00
researcher: claude
git_commit: 8d4daca72879f7af97fcbfa29869ca4aa8165d62
branch: review_sandbox
repository: shepherd
topic: "How could kubernetes-sigs/agent-sandbox be used inside Shepherd?"
tags: [research, security, sandbox, agent-sandbox, kubernetes, isolation, gvisor, kata-containers]
status: complete
last_updated: 2026-01-30
last_updated_by: claude
---

# Research: How could kubernetes-sigs/agent-sandbox be used inside Shepherd?

**Date**: 2026-01-30T08:42:09+01:00
**Researcher**: claude
**Git Commit**: 8d4daca72879f7af97fcbfa29869ca4aa8165d62
**Branch**: review_sandbox
**Repository**: shepherd

## Research Question

The user found https://github.com/kubernetes-sigs/agent-sandbox and wants to understand how it could be integrated into Shepherd for improved security. The project is acknowledged to be early (v0.1.0 alpha) and may require specific features, but the concept is interesting.

## Summary

Agent-Sandbox is a Kubernetes SIG Apps subproject (v0.1.0, alpha) that provides CRDs and controllers for managing isolated, stateful, singleton workloads — specifically designed for AI agent runtimes. It offers gVisor and Kata Containers backends for sandbox isolation, warm pools for sub-second startup, and a declarative Kubernetes-native API.

Shepherd currently creates bare K8s Jobs with basic pod security (non-root, seccomp runtime/default, drop all capabilities). Agent-Sandbox could replace or augment Shepherd's Job execution layer to provide stronger workload isolation via gVisor or Kata Containers, particularly for the runner container that executes untrusted LLM-generated code.

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
| SandboxClaim | `extensions.agents.x-k8s.io/v1alpha1` | Request to provision a sandbox from a template |
| SandboxWarmPool | `extensions.agents.x-k8s.io/v1alpha1` | Pre-warmed pool of ready sandboxes for sub-second allocation |

### Key Capabilities

1. **Multi-backend isolation**: gVisor (userspace kernel) and Kata Containers (VM-level) — configured via `runtimeClassName` in pod template
2. **Warm pools**: Pre-warmed sandbox instances for sub-second startup (up to 90% latency reduction)
3. **Lifecycle management**: Create, pause, resume, scheduled shutdown
4. **Stable identity**: Consistent hostname and network presence
5. **Persistent storage**: Data survives pod restarts
6. **Sandbox Router**: Central traffic entry point (ClusterIP service on port 8080)

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

### Requirements

- Kubernetes cluster with CRD support (1.24+)
- gVisor or Kata Containers runtime installed and configured on nodes
- RuntimeClass resources configured for the chosen backend
- Installation via `kubectl apply -f` from release manifests

## Shepherd's Current Job Execution

Shepherd currently creates K8s Jobs via the operator controller. The relevant code:

### Job Builder (`internal/controller/job_builder.go`)

- Creates a K8s `batch/v1` Job with init container + runner container
- Init container: generates GitHub token, writes task files
- Runner container: executes AI agent (Claude Code) with task
- Pod security: `runAsNonRoot: true`, `seccompProfile: RuntimeDefault`, `drop ALL` capabilities
- `backoffLimit: 0` — no K8s retries, operator handles retry logic
- `podFailurePolicy` for OOM detection (exit code 137)
- Owner reference links Job to AgentTask (garbage collection)

### Pod Template (simplified)

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

### Security Boundaries

- Runner image is operator-controlled (not user-specified) — admin allowlist
- GitHub private key never exposed to runner container
- Token auto-expires after 1 hour, scoped to single repo
- CRD spec immutability prevents post-creation injection

## Integration Possibilities

### Option A: Replace Job with Sandbox (Full Integration)

Instead of creating a `batch/v1.Job`, the operator would create a `Sandbox` (or `SandboxClaim`) resource. The agent-sandbox controller would then manage the pod lifecycle.

**What changes**:
- `internal/controller/job_builder.go` → builds a `Sandbox` or `SandboxClaim` instead of a `Job`
- Operator watches `Sandbox` status instead of `Job` status
- `api/v1alpha1/agenttask_types.go` → `RunnerSpec` gains a `runtimeClassName` field
- RBAC expanded to manage `agents.x-k8s.io` resources

**What Shepherd gains**:
- gVisor/Kata isolation for runner containers (defense-in-depth beyond seccomp)
- Warm pool support for faster job startup
- Pause/resume capability (could be useful for interactive sessions in the future)
- Stable network identity via Sandbox Router

**Challenges**:
- **Run-to-completion semantics**: Sandbox is designed for long-running singletons, not batch Jobs. Shepherd needs Jobs (backoffLimit, completions, activeDeadlineSeconds, podFailurePolicy). Sandbox doesn't have built-in completion semantics — Shepherd would need to handle termination differently.
- **Init container pattern**: Shepherd uses init containers for GitHub auth. Sandbox's podTemplate supports init containers, but the interaction with warm pools is unclear — warm pools likely pre-run pods, so init containers that generate per-task tokens wouldn't work with pre-warming.
- **Owner references and garbage collection**: Job → AgentTask ownership is well-understood. Sandbox ownership semantics may differ.
- **Failure detection**: Shepherd uses `podFailurePolicy` (K8s Job feature) for OOM detection. This doesn't exist for Sandbox-managed pods. Shepherd would need alternative failure classification.
- **Two controllers managing the same pods**: Agent-sandbox controller manages the Sandbox pod, but Shepherd's operator needs to observe and react to status changes. Coordination needed.

**Verdict**: Significant impedance mismatch between Sandbox (long-running singleton) and Shepherd's Job (run-to-completion batch workload). Not a natural fit without upstream changes to agent-sandbox.

### Option B: Use RuntimeClass Only (Lightweight Integration)

Keep the existing Job-based architecture but add `runtimeClassName` to the pod template, leveraging gVisor or Kata Containers directly without the agent-sandbox controller.

**What changes**:
- `internal/controller/job_builder.go` → add `runtimeClassName` to pod spec
- `api/v1alpha1/agenttask_types.go` → `RunnerSpec` gains a `runtimeClassName` field (or operator-level config)
- Cluster needs gVisor/Kata runtime installed (same requirement as agent-sandbox)

**What Shepherd gains**:
- gVisor/Kata isolation for runner containers
- No new CRD dependency
- All existing Job semantics preserved (backoffLimit, podFailurePolicy, activeDeadlineSeconds)
- Simplest path to stronger isolation

**What Shepherd doesn't gain**:
- No warm pools (cold start overhead)
- No pause/resume
- No Sandbox Router
- Must manage RuntimeClass configuration independently

**Verdict**: Pragmatic choice for MVP. Gets 80% of the security benefit with minimal changes.

### Option C: Hybrid — SandboxTemplate for Configuration, Job for Execution

Use `SandboxTemplate` as a configuration source (defining the security profile, runtime class, resource limits) but continue using Jobs for actual execution.

**What changes**:
- Operator reads `SandboxTemplate` to extract runtime configuration
- Applies that configuration to the Job's pod template
- RBAC expanded to read `extensions.agents.x-k8s.io` resources

**What Shepherd gains**:
- Standardized sandbox configuration via a community CRD
- Cluster admins manage security profiles through SandboxTemplate
- Easy upgrade path if agent-sandbox adds batch workload support later

**Challenges**:
- Using SandboxTemplate as just a config source is not its intended purpose
- Adds a CRD dependency without using the controller
- Configuration drift if admin expectations don't match Shepherd's usage

**Verdict**: Over-engineered for current needs. The configuration can be expressed more simply through Shepherd's own operator config or CRD fields.

### Option D: Warm Pool for Runner Pre-warming (Future)

When agent-sandbox matures and potentially supports batch/ephemeral workloads, use `SandboxWarmPool` to maintain pre-warmed runner environments.

**What changes** (future):
- `SandboxWarmPool` keeps N runner pods ready with gVisor isolation
- On new AgentTask, claim a warm sandbox instead of creating a cold Job
- Init container logic moves to a "claim and configure" pattern

**What Shepherd gains**:
- Sub-second task startup (major UX improvement)
- gVisor/Kata isolation
- Kubernetes-native warm pool management

**Challenges**:
- Warm pools pre-run pods. Per-task init (GitHub tokens, task files) can't be pre-warmed.
- Would need a sidecar or post-start hook pattern instead of init containers
- Agent-sandbox doesn't currently support run-to-completion workloads

**Verdict**: Interesting future direction once agent-sandbox matures and potentially adds ephemeral/batch workload patterns. Worth tracking upstream.

## Recommended Approach

For the current Shepherd MVP:

1. **Short term (now)**: Option B — add `runtimeClassName` support to the Job builder. This is a small, additive change (~10 lines in `job_builder.go` + a config field). Clusters with gVisor/Kata get isolation; clusters without it continue working.

2. **Medium term**: Track agent-sandbox development. The project is alpha and under active development. If they add batch/ephemeral workload support (a reasonable evolution given AI agent use cases), Option A becomes viable.

3. **Long term**: Option D — warm pools would be a significant performance improvement for Shepherd. This depends on agent-sandbox supporting the init-then-run pattern that Shepherd needs.

### Minimal Change for Option B

In `RunnerSpec` (or operator config):

```go
type RunnerSpec struct {
    Image              string                       `json:"image,omitempty"`
    Timeout            metav1.Duration              `json:"timeout,omitempty"`
    ServiceAccountName string                       `json:"serviceAccountName,omitempty"`
    Resources          corev1.ResourceRequirements  `json:"resources,omitempty"`
    RuntimeClassName   *string                      `json:"runtimeClassName,omitempty"`  // NEW
}
```

In `job_builder.go`, when constructing the pod template:

```go
if task.Spec.Runner.RuntimeClassName != nil {
    pod.Spec.RuntimeClassName = task.Spec.Runner.RuntimeClassName
}
```

This is the smallest useful change and doesn't depend on agent-sandbox at all — it directly uses the Kubernetes `runtimeClassName` field that gVisor and Kata Containers expose.

## Key Differences: Sandbox vs Job

| Aspect | Kubernetes Job | Agent Sandbox |
|--------|---------------|---------------|
| **Workload type** | Run-to-completion (batch) | Long-running singleton |
| **Completion** | Built-in (completions, backoffLimit) | No built-in completion |
| **Failure policy** | podFailurePolicy (exit codes, conditions) | Not available |
| **Timeout** | activeDeadlineSeconds | shutdownTime (absolute) |
| **Retry** | backoffLimit + operator logic | Not applicable |
| **Init containers** | Native support | Supported in podTemplate |
| **Warm pools** | Not available | SandboxWarmPool |
| **Pause/Resume** | Not available | Native |
| **Network identity** | Ephemeral pod IP | Stable via Router |
| **Isolation** | Pod security + seccomp | gVisor/Kata via runtimeClassName |

## Code References

- `internal/controller/job_builder.go` — Job spec construction, where `runtimeClassName` would be added
- `internal/controller/agenttask_controller.go` — Reconciler that watches Jobs (would change if using Sandbox)
- `api/v1alpha1/agenttask_types.go` — CRD types where `RuntimeClassName` field would be added to `RunnerSpec`
- `config/rbac/role.yaml` — RBAC rules that would need expansion for agent-sandbox CRDs

## Architecture Documentation

Shepherd's current architecture (operator creates Jobs, watches status, updates CRD conditions) maps cleanly to Kubernetes batch workload patterns. The Job abstraction provides exactly the semantics Shepherd needs: run-to-completion, failure classification, timeout enforcement, and garbage collection via owner references.

Agent-sandbox targets a different workload profile (long-running, stateful, singleton) that doesn't naturally align with Shepherd's batch execution model. The security isolation benefits (gVisor, Kata) are valuable but are a Kubernetes runtime feature, not specific to agent-sandbox.

## Historical Context (from thoughts/)

- `thoughts/plans/2026-01-28-init-container.md` — Init container implementation planning, including securityContext considerations
- `thoughts/plans/2026-01-27-operator-implementation.md` — Operator implementation with RBAC and privilege design
- `thoughts/plans/2026-01-28-phase5-failure-handling-podfailurepolicy.md` — Pod failure policy design using Job-specific features (exit codes, DisruptionTarget)
- `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md` — Research on OOM detection leveraging Job's podFailurePolicy

## Links

- [agent-sandbox GitHub](https://github.com/kubernetes-sigs/agent-sandbox)
- [agent-sandbox docs](https://agent-sandbox.sigs.k8s.io/)
- [agent-sandbox guides](https://agent-sandbox.sigs.k8s.io/docs/guides/)
- [gVisor architecture](https://gvisor.dev/docs/architecture_guide/intro/)
- [Kata Containers integration](https://katacontainers.io/blog/kata-containers-agent-sandbox-integration/)
- [Google Cloud agent-sandbox on GKE](https://docs.google.com/kubernetes-engine/docs/how-to/agent-sandbox)
- [Agent Factory blog post](https://cloud.google.com/blog/topics/developers-practitioners/agent-factory-recap-supercharging-agents-on-gke-with-agent-sandbox-and-pod-snapshots)
- [InfoQ coverage](https://www.infoq.com/news/2025/12/agent-sandbox-kubernetes/)

## Open Questions

1. **Will agent-sandbox add batch/ephemeral workload support?** — The current singleton model doesn't fit Shepherd's run-to-completion pattern. Tracking upstream issues would clarify this.
2. **Warm pool + per-task init**: Can warm pools work with per-task initialization (GitHub tokens, task files)? Would require a sidecar pattern rather than init containers.
3. **RuntimeClass availability**: What percentage of Shepherd's target clusters will have gVisor or Kata configured? This determines how broadly useful runtime isolation is.
4. **Performance impact**: gVisor adds syscall overhead (userspace kernel interception). For AI agent workloads that are mostly I/O and API-call bound, this may be negligible — but worth benchmarking.
