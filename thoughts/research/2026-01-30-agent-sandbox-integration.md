---
date: 2026-01-30T08:42:09+01:00
researcher: claude
git_commit: 06ec7c36db501c8906417ef6e028dd9e921479cd
branch: review_sandbox
repository: shepherd
topic: "Shepherd + agent-sandbox: settled architecture"
tags: [research, security, sandbox, agent-sandbox, kubernetes, isolation, gvisor, warm-pools, architecture, settled]
status: complete
last_updated: 2026-01-31
last_updated_by: claude
last_updated_note: "Final consolidation — settled architecture after multiple rounds of discussion"
---

# Shepherd + agent-sandbox: Settled Architecture

**Date**: 2026-01-30 (initial), 2026-01-31 (settled)
**Git Commit**: 06ec7c36db501c8906417ef6e028dd9e921479cd
**Branch**: review_sandbox
**Repository**: shepherd

## Decisions Made

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Execution substrate | agent-sandbox (replace Jobs entirely) | Gains gVisor/Kata isolation, warm pools, SandboxTemplate environment management. Job coupling is moderate — migration is feasible. |
| Warm pools | From day one | Marginal complexity over cold-start (one HTTP endpoint on entrypoint). Real UX benefit for issue-driven workflows (~2s vs ~60s feedback). |
| Init container | Retired. Responsibilities move to API server. | Init containers are incompatible with warm pools (run during warming, before any task exists). API-based token generation is a security improvement. |
| Runner entrypoint | Thin Go binary: HTTP server on :8888, waits for task, then pulls data + runs Claude Code | Claude Code is a CLI tool. It doesn't pull data or expose APIs. A thin wrapper handles plumbing, invokes `claude -p` like running it from a terminal. |
| Abstraction layer | No. Build directly for agent-sandbox. | Both projects are alpha. Designing an interface from one backend (Jobs) produces the wrong shape. Agent-sandbox IS the abstraction (SandboxTemplate, SandboxClaim). |
| NetworkPolicy | Per-sandbox default-deny via SandboxTemplate | Agent-sandbox creates NetworkPolicy per claim before pod starts. Template defines allowed egress (API, GitHub, Anthropic). |

## Architecture

### Data Flow

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
   referencing SandboxTemplate (e.g. "shepherd-claude-runner")
   with shutdownTime = now + timeout
         |
         v
7. Agent-sandbox controller:
   - Claims warm pod from SandboxWarmPool (or creates new)
   - Creates headless Service ({claim-name}.{ns}.svc.cluster.local)
   - Creates NetworkPolicy (from template, default-deny)
   - SandboxClaim status → Ready=True
         |
         v
8. Operator detects SandboxClaim ready via status watch
   POSTs to http://{sandbox}.{ns}.svc.cluster.local:8888/task
   Body: { "taskID": "task-abc123", "apiURL": "http://shepherd-api:8080" }
         |
         v
9. Runner entrypoint receives POST, begins work:
   GET {apiURL}/api/v1/tasks/{taskID}        → description, context
   GET {apiURL}/api/v1/tasks/{taskID}/token   → GitHub installation token
   git clone with token
   write TASK.md to repo
   claude -p "Read TASK.md and implement what it describes"
   POST {apiURL}/api/v1/tasks/{taskID}/status → progress updates
         |
         v
10. Claude Code creates PR, entrypoint reports completion
    POST {apiURL}/api/v1/tasks/{taskID}/status → { event: completed, pr_url: ... }
    Container exits
         |
         v
11. Operator detects completion:
    Sandbox Ready=False + pod container exit code 0 → AgentTask Succeeded=True
    Deletes SandboxClaim → cascades to Sandbox → Pod → Service
         |
         v
12. API's CRD status watcher detects terminal state
    POSTs to adapter callback → adapter posts final GitHub comment
```

### Component Responsibilities

| Component | Does | Does NOT |
|-----------|------|----------|
| **Operator** | Watch AgentTask CRDs, create SandboxClaims, watch Sandbox status, POST task assignment to runner, update AgentTask conditions, delete SandboxClaim on completion | Generate GitHub tokens, decompress context, talk to GitHub |
| **API** | Serve REST endpoints, create AgentTask CRDs, serve task data to runners (`GET /tasks/{id}`), generate GitHub tokens on-demand (`GET /tasks/{id}/token`), receive runner status callbacks, watch CRD status, notify adapters | Manage sandboxes, talk to K8s batch API |
| **Runner entrypoint** | Listen on :8888 for task assignment, pull task data + token from API, clone repo, write TASK.md, invoke `claude -p`, report status/completion to API | Know about agent-sandbox, manage its own lifecycle, expose generic /execute endpoints |
| **Adapter** | Handle GitHub webhooks, post GitHub comments, query API for deduplication | Read CRDs, talk to K8s |
| **Agent-sandbox** | Manage Sandbox lifecycle, warm pools, pod adoption, headless services, NetworkPolicies | Know about Shepherd tasks |

### Owner Reference Chain

```text
AgentTask → owns → SandboxClaim → owns → Sandbox → owns → Pod, Service
                                    ↕
                           SandboxClaim → owns → NetworkPolicy
```

Deleting an AgentTask cascades through the entire chain.

## Runner Entrypoint

The runner container image contains:
- Claude Code CLI (pre-installed)
- Git, gh CLI
- Language tools (Go for MVP, more via additional SandboxTemplates later)
- `shepherd-runner` binary (thin Go entrypoint, ~100 lines)

### Entrypoint Binary (conceptual)

```go
// cmd/shepherd-runner/main.go
func main() {
    apiURL := os.Getenv("SHEPHERD_API_URL")

    // Phase 1: Wait for task assignment (warm pool phase)
    // HTTP server blocks until POST /task is received
    mux := http.NewServeMux()
    taskCh := make(chan TaskAssignment, 1)

    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
        var req TaskAssignment
        json.NewDecoder(r.Body).Decode(&req)
        taskCh <- req
        w.WriteHeader(http.StatusAccepted)
    })

    srv := &http.Server{Addr: ":8888", Handler: mux}
    go srv.ListenAndServe()

    // Block until task arrives
    assignment := <-taskCh
    srv.Shutdown(context.Background())

    // Phase 2: Pull task data from API
    task := fetchTask(assignment.APIURL, assignment.TaskID)
    token := fetchToken(assignment.APIURL, assignment.TaskID)

    // Phase 3: Setup workspace
    setupGitCredentials(token, task.RepoURL)
    cloneRepo(task.RepoURL, task.Ref, "/workspace")
    writeTaskFile("/workspace/TASK.md", task.Description, task.Context)

    // Phase 4: Run Claude Code
    cmd := exec.Command("claude", "-p",
        "Read TASK.md and implement what it describes. "+
        "Create a branch and PR when done.")
    cmd.Dir = "/workspace"
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    err := cmd.Run()

    // Phase 5: Report results
    if err != nil {
        reportFailure(assignment.APIURL, assignment.TaskID, err)
        os.Exit(1)
    }
    reportCompletion(assignment.APIURL, assignment.TaskID)
}
```

### Readiness Probe

The SandboxTemplate includes a readiness probe so the warm pool knows when pods are ready to receive work:

```yaml
readinessProbe:
  httpGet:
    path: /healthz
    port: 8888
  initialDelaySeconds: 2
  periodSeconds: 5
```

Pod becomes ready when the entrypoint's HTTP server is listening. The warm pool controller tracks `ReadyReplicas` based on this.

## SandboxTemplate

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: shepherd-claude-runner
  namespace: shepherd
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        runAsNonRoot: true
      containers:
      - name: runner
        image: shepherd-runner:latest
        ports:
        - containerPort: 8888
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8888
          initialDelaySeconds: 2
          periodSeconds: 5
        env:
        - name: SHEPHERD_API_URL
          value: "http://shepherd-api.shepherd.svc.cluster.local:8080"
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2000m"

  networkPolicy:
    egress:
    # DNS resolution
    - ports:
      - protocol: UDP
        port: 53
      - protocol: TCP
        port: 53
    # Shepherd API (cluster-internal)
    - to:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: shepherd
        podSelector:
          matchLabels:
            app: shepherd-api
      ports:
      - protocol: TCP
        port: 8080
    # GitHub API + Anthropic API (external HTTPS)
    - to:
      - ipBlock:
          cidr: 0.0.0.0/0
      ports:
      - protocol: TCP
        port: 443
```

## SandboxWarmPool

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxWarmPool
metadata:
  name: claude-runner-pool
  namespace: shepherd
spec:
  templateName: shepherd-claude-runner
  minReady: 1    # Start with 1 warm pod, increase as usage grows
  maxReady: 5
```

## SandboxClaim (created by operator per AgentTask)

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxClaim
metadata:
  name: task-abc123
  namespace: shepherd
  ownerReferences:
  - apiVersion: toolkit.shepherd.io/v1alpha1
    kind: AgentTask
    name: task-abc123
    controller: true
spec:
  sandboxTemplateRef:
    name: shepherd-claude-runner
  lifecycle:
    shutdownTime: "2026-01-31T11:30:00Z"  # now + spec.runner.timeout
    shutdownPolicy: Retain                  # keep claim for status inspection
```

## CRD Changes for AgentTask

```go
type RunnerSpec struct {
    // SandboxTemplateName references a SandboxTemplate for the runner environment.
    // +kubebuilder:validation:Required
    SandboxTemplateName string `json:"sandboxTemplateName"`

    // Timeout is the maximum duration for task execution.
    // Translated to SandboxClaim shutdownTime (now + timeout).
    // +kubebuilder:default="30m"
    Timeout metav1.Duration `json:"timeout,omitempty"`

    // ServiceAccountName for the sandbox pod.
    ServiceAccountName string `json:"serviceAccountName,omitempty"`

    // Resources override for the runner container.
    // If empty, uses the template defaults.
    Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

type AgentTaskStatus struct {
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
    StartTime          *metav1.Time       `json:"startTime,omitempty"`
    CompletionTime     *metav1.Time       `json:"completionTime,omitempty"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    SandboxClaimName   string             `json:"sandboxClaimName,omitempty"`
    Result             TaskResult         `json:"result,omitempty"`
}
```

## Operator Reconciliation

### Reconcile Loop

```text
1. Fetch AgentTask
2. If terminal → return (prevent re-reconciliation)
3. Initialize condition → Succeeded=Unknown, Reason=Pending
4. Look for existing SandboxClaim (name = task name)
5. If no SandboxClaim → create one:
   - Set ownerReference (AgentTask)
   - Set sandboxTemplateRef from task.Spec.Runner.SandboxTemplateName
   - Set shutdownTime from timeout
   - Set shutdownPolicy: Retain
   - Record SandboxClaimName in AgentTask status
6. Check SandboxClaim status:
   - Ready=True → POST /task to runner (if not already done)
     Mark AgentTask as Running
   - Ready=False + reason=SandboxExpired → timeout
     Mark AgentTask as Failed/TimedOut
   - Ready=False + reason=ClaimExpired → timeout
     Mark AgentTask as Failed/TimedOut
   - Ready=False (other) → still starting, requeue
7. If task already Running → check pod container status:
   - Container running → requeue
   - Container terminated, exit 0 → mark Succeeded
   - Container terminated, exit 137 + OOMKilled → mark Failed/OOM
   - Container terminated, other exit → mark Failed
8. On terminal state → delete SandboxClaim (cascades cleanup)
```

### Task Assignment (operator → runner)

When the operator detects `SandboxClaim Ready=True`, it needs to POST the task assignment to the runner. The sandbox's FQDN comes from `Sandbox.Status.ServiceFQDN`:

```go
func (r *AgentTaskReconciler) assignTask(ctx context.Context, task *v1alpha1.AgentTask, sandbox *sandboxv1alpha1.Sandbox) error {
    url := fmt.Sprintf("http://%s:8888/task", sandbox.Status.ServiceFQDN)
    body := TaskAssignment{
        TaskID: task.Name,
        APIURL: r.Config.APIURL,
    }
    // POST with timeout and retry
    // Mark task as Running on success
}
```

The operator needs to handle:
- Sandbox ready but runner not yet serving (retry with backoff)
- Assignment already sent (idempotency — track in AgentTask status or annotation)

### Failure Detection

```go
func classifyFailure(sandbox *sandboxv1alpha1.Sandbox, pod *corev1.Pod) (string, string) {
    // Check if sandbox expired (timeout)
    for _, c := range sandbox.Status.Conditions {
        if c.Type == "Ready" && c.Reason == "SandboxExpired" {
            return ReasonTimedOut, "Task exceeded timeout"
        }
    }

    // Check pod container status
    if pod == nil {
        return ReasonFailed, "Pod not found"
    }
    for _, cs := range pod.Status.ContainerStatuses {
        if cs.Name != "runner" {
            continue
        }
        if cs.State.Terminated == nil {
            continue
        }
        if cs.State.Terminated.Reason == "OOMKilled" {
            return ReasonOOM, "Container killed: OOM"
        }
        if cs.State.Terminated.ExitCode == 137 {
            return ReasonOOM, "Container killed: exit code 137"
        }
        if cs.State.Terminated.ExitCode != 0 {
            return ReasonFailed, fmt.Sprintf("Container exited with code %d", cs.State.Terminated.ExitCode)
        }
    }
    return ReasonFailed, "Unknown failure"
}
```

Direct pod inspection is more reliable than the current Job-based approach (parsing string messages from Job conditions).

## API Server Changes

### New Endpoints

The API server gains two endpoints that replace the init container:

**`GET /api/v1/tasks/{taskID}`** — Serves task data to the runner.
- Returns: description, context (decompressed from CRD), repo URL, ref
- Auth: internal cluster networking (no external access)
- The gzip decompression logic from `cmd/shepherd-init/taskfiles.go` moves here

**`GET /api/v1/tasks/{taskID}/token`** — Generates GitHub installation token.
- Returns: short-lived GitHub installation token scoped to the task's repo
- Auth: internal cluster networking
- The JWT generation and token exchange logic from `cmd/shepherd-init/github.go` moves here
- The Runner App private key is mounted into the API server pod (not runner pods)

### Security Improvement

| Concern | Before (init container) | After (API server) |
|---------|------------------------|-------------------|
| Private key location | Mounted into every runner pod via K8s Secret | Mounted once into API server pod |
| Token scope | Generated per-pod with repo scope | Generated per-request with repo scope |
| Key exposure surface | N runner pods | 1 API server |
| Network path | Init container → GitHub API (external) | API server → GitHub API (external) |

## What Gets Retired

### `cmd/shepherd-init/` (entire module)

The separate Go module for the init container is retired. Its logic redistributes:

| File | Logic | Moves to |
|------|-------|----------|
| `taskfiles.go` | `decodeContext()` gzip decompression | `pkg/api/` — task-serving endpoint |
| `taskfiles.go` | `writeTaskFiles()` file writing | Runner entrypoint writes after pulling from API |
| `github.go` | `createJWT()` JWT generation | `pkg/api/` — token endpoint |
| `github.go` | `exchangeToken()` GitHub API call | `pkg/api/` — token endpoint |
| `github.go` | `readPrivateKey()` PEM parsing | `pkg/api/` — server startup |

### Job-related operator code

| File | What changes |
|------|-------------|
| `internal/controller/job_builder.go` | Replaced by sandbox claim builder |
| `internal/controller/agenttask_controller.go` | `reconcileJobStatus()` → `reconcileSandboxStatus()`, `classifyJobFailure()` → `classifyFailure()` using pod status |
| `internal/controller/failure_test.go` | Rewritten for pod-based failure classification |
| `internal/controller/agenttask_controller_test.go` | Updated for SandboxClaim-based lifecycle |

## agent-sandbox Learnings Applied

### From the Codebase

1. **NetworkPolicy per sandbox with default-deny** — SandboxTemplate includes `networkPolicy` field. Policy created before pod starts, uses claim UID for pod targeting. Applied in template above.

2. **AutomountServiceAccountToken defaults to false** — Agent-sandbox controller enforces this. Runners shouldn't access the K8s API.

3. **ShutdownPolicy: Retain** — Pod/sandbox deleted on timeout but SandboxClaim stays for inspection. Shepherd can distinguish timeout from completion.

4. **Headless service per sandbox** — Stable DNS identity `{name}.{ns}.svc.cluster.local`. Operator uses this to POST task assignment.

5. **Warm pool adoption is transparent** — Shepherd creates SandboxClaim → agent-sandbox handles whether it comes from warm pool or cold start. No Shepherd code change needed.

### From the 2026 Roadmap (PR #259)

| Roadmap Item | Impact on Shepherd |
|-------------|-------------------|
| **Go Client (#227)** | Useful but not blocking — operator uses client-go directly |
| **Scale-down / Resume PVC based** | Enables pause/resume runners preserving workspace. Step toward snapshots for interactive sessions |
| **Auto-deletion of bursty sandboxes** | Relevant for Shepherd's ephemeral task pattern |
| **Status Updates (#119)** | Better status reduces need for pod-level inspection |
| **Startup Actions (#58)** | Future alternative to HTTP entrypoint: start paused, resume on task assignment |
| **Creation Latency Metrics (#123)** | Observability for task startup time |
| **Beta/GA versions** | API stability |

## Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| agent-sandbox alpha API changes | Medium | Both projects are alpha. Wrapping agent-sandbox types in internal structs allows adapting without full rewrite. |
| Operator needs to POST to runner (extra HTTP call) | Low | Standard reconciliation pattern. Retry with backoff. Track assignment status to ensure idempotency. |
| Runner entrypoint HTTP server adds code | Low | ~100 lines of Go. One endpoint. readiness probe on /healthz. |
| Private key in API server is single point of failure | Medium | Same as any centralized secret. Standard K8s Secret management + rotation applies. |
| Agent-sandbox controller must be installed | Low | Required dependency. Document in installation guide. |

## Remaining Open Questions

1. **Pod status visibility from SandboxClaim**: Can the operator get pod container status through the Sandbox/SandboxClaim status, or does it need to watch pods directly? Need to test with envtest.

2. **Warm pool sizing**: What's the right `minReady` for different usage patterns? Start with 1, observe, adjust.

3. **Task assignment idempotency**: If the operator reconciles multiple times after Ready=True, it should not POST /task twice. Track "assigned" state in AgentTask status or annotation.

4. **Runner HTTP server timeout**: If the warm pool pod sits for a long time without receiving a task (e.g., pool is larger than demand), it should eventually self-terminate. This may be handled by the warm pool controller's own cleanup, or by a timeout in the entrypoint.

## Links

- [agent-sandbox GitHub](https://github.com/kubernetes-sigs/agent-sandbox)
- [agent-sandbox docs](https://agent-sandbox.sigs.k8s.io/)
- [agent-sandbox 2026 roadmap PR](https://github.com/kubernetes-sigs/agent-sandbox/pull/259)
- [gVisor architecture](https://gvisor.dev/docs/architecture_guide/intro/)
- [Kata Containers + agent-sandbox](https://katacontainers.io/blog/kata-containers-agent-sandbox-integration/)
- [GKE agent-sandbox docs](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox)
- [GKE Pod Snapshots + Agent Sandbox](https://cloud.google.com/blog/topics/developers-practitioners/agent-factory-recap-supercharging-agents-on-gke-with-agent-sandbox-and-pod-snapshots)
- [Warm pool deep dive](https://pacoxu.wordpress.com/2025/12/02/agent-sandbox-pre-warming-pool-makes-secure-containers-cold-start-lightning-fast/)
- [Go SDK request (Issue #227)](https://github.com/kubernetes-sigs/agent-sandbox/issues/227)

## Historical Context (from thoughts/)

- `thoughts/plans/2026-01-28-init-container.md` — Init container implementation (retired in this architecture)
- `thoughts/plans/2026-01-27-operator-implementation.md` — Operator implementation (reconciler to be adapted for SandboxClaim)
- `thoughts/plans/2026-01-28-phase5-failure-handling-podfailurepolicy.md` — Pod failure policy (replaced by direct pod status inspection)
- `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md` — OOM detection (direct container status is more reliable)
- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` — Session management patterns for future interactive mode
- `thoughts/reviews/2026-01-28-api-server-plan-review.md` — API server review
