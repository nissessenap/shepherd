---
date: 2026-02-01T13:46:04+01:00
researcher: claude
git_commit: cdecc15e35021cd984372ce323c0c02148d63b5e
branch: arch_new
repository: shepherd
topic: "Shepherd Architecture v2: agent-sandbox integration, retiring Jobs and init container"
tags: [research, architecture, agent-sandbox, operator, api, runner, migration, fleet, design]
status: complete
last_updated: 2026-02-01
last_updated_by: claude
---

# Shepherd Architecture v2: Agent-Sandbox

**Date**: 2026-02-01
**Supersedes**: [2026-01-27 design](2026-01-27-shepherd-design.md)
**Status**: Draft

## Overview

Shepherd is an open-source background coding agent orchestrator. It receives tasks from various triggers (GitHub, Slack, CLI), runs AI coding agents in isolated Kubernetes sandboxes, and reports results back.

This document replaces the Jobs-based architecture from the [2026-01-27 design](2026-01-27-shepherd-design.md) with an agent-sandbox-based architecture. The key changes are:

1. **Replace K8s Jobs with agent-sandbox SandboxClaims** for workload execution
2. **Retire the init container** — token generation and task data serving move to the API server
3. **Runner-pull model** — the runner pulls its configuration from the API instead of receiving it via environment variables and volumes
4. **Fleet migrations** — support running the same task across multiple repositories

### What Stays the Same

- Single binary, multiple targets (Kong CLI subcommands)
- `AgentTask` CRD as the unit of work
- API server creates CRDs, receives callbacks, watches terminal states
- Operator reconciles CRDs, manages execution lifecycle
- GitHub adapter handles webhooks, posts comments
- Provider-agnostic core (adapter pattern)
- HMAC-signed adapter callbacks

### What Changed From PoC Learnings

Based on `thoughts/research/2026-01-31-poc-sandbox-learnings.md`:

- ~8 second cold start latency (warm pools important for production)
- Headless services work for in-cluster connectivity (use `Sandbox.Status.ServiceFQDN` + port)
- `restartPolicy: Never` honored — no restart loops
- Resources persist after pod exits — operator must explicitly delete SandboxClaim
- agent-sandbox v0.1.0 has no `Lifecycle` field — operator manages timeout and cleanup
- Multiple concurrent sandboxes work without port conflicts

## Goals

### MVP

1. **Issue-driven development** — Trigger agent from GitHub issue/PR comment to work on a single repo
2. **Fleet migrations** — Run the same transformation across many repos (Spotify pattern)

### Future

3. **Scheduled SRE tasks** — Daily/weekly automated analysis and fixes
4. **Interactive sessions** — Long-running dev environments with remote access (see `thoughts/research/2026-01-31-background-agents-session-management-learnings.md`)

### Future Backend Extensibility

The architecture should be structured so that the operator's sandbox management logic can be extracted into a backend interface when a second execution backend materializes (e.g., Modal, Cloudflare Workers). For now, build directly for agent-sandbox without premature abstraction. Document the boundary clearly so the future refactoring surface is obvious.

## Architecture

### Design Principles

- **K8s-native**: Uses CRDs for state, agent-sandbox for execution. No external database for MVP.
- **Provider-agnostic core**: API and operator know nothing about GitHub. Adapters handle translation.
- **Callback-based status**: Triggers include a callback URL. API POSTs status updates there.
- **Single binary, multiple targets**: Loki/Mimir pattern — one binary, scale components independently.
- **Runner-pull model**: Runner pulls task data and tokens from the API, not from volumes or env vars.
- **Concrete first**: Build for agent-sandbox directly. Extract interfaces when a second backend appears.

### Tech Stack

| Category | Choice |
| -------- | ------ |
| Language | Go 1.25+ |
| CRD/Operator | kubebuilder, controller-runtime |
| CLI | `github.com/alecthomas/kong` |
| HTTP Router | `github.com/go-chi/chi/v5` |
| Logging | zap via `sigs.k8s.io/controller-runtime/pkg/log/zap` |
| GitHub | `github.com/google/go-github`, `github.com/bradleyfalzon/ghinstallation/v2` |
| Sandbox | `sigs.k8s.io/agent-sandbox` v0.1.0 |
| Unit Testing | `github.com/stretchr/testify` |
| Integration Testing | `github.com/onsi/gomega` (envtest) |
| Metrics | `github.com/prometheus/client_golang` (via controller-runtime) |
| Container Builds | ko |
| Deployment | Helm (kubebuilder Kustomize for CRD generation only) |

### Components

Single binary with Kong subcommands (`shepherd api`, `shepherd operator`, `shepherd github`):

| Target | Role | Scaling |
| ------ | ---- | ------- |
| `api` | REST API, CRD creation, runner data/token serving, runner callbacks, CRD status watcher, adapter notifications | Multiple replicas behind LB |
| `operator` | Watches AgentTask CRDs, creates SandboxClaims, watches Sandbox status, POSTs task assignment to runner, updates CRD status, deletes SandboxClaim on completion | 1 active (leader election) |
| `github` | GitHub App webhooks, posts comments | Multiple replicas |

### GitHub Apps

Two separate GitHub Apps with distinct responsibilities:

| App | Purpose | Permissions | Used By |
| --- | ------- | ----------- | ------- |
| Shepherd Trigger | Webhooks, read issues/PRs, write comments | Read issues, write comments | github-adapter |
| Shepherd Runner | Clone repos, push branches, create PRs | Read/write code, create PRs | API server (generates tokens on behalf of runners) |

### Component Responsibilities

| Component | Does | Does NOT |
| --------- | ---- | -------- |
| **Operator** | Watch AgentTask CRDs, create SandboxClaims, watch Sandbox status, POST task assignment to runner, update AgentTask conditions, delete SandboxClaim on completion, manage timeout | Generate GitHub tokens, decompress context, talk to GitHub, make external HTTP calls (except runner task assignment within cluster) |
| **API** | Serve REST endpoints, create AgentTask CRDs, serve task data to runners (`GET /tasks/{id}`), generate GitHub tokens on-demand (`GET /tasks/{id}/token`), receive runner status callbacks, watch CRD status, notify adapters | Manage sandboxes, talk to K8s batch API |
| **Runner entrypoint** | Listen on :8888 for task assignment, pull task data + token from API, clone repo, write TASK.md, invoke `claude -p`, report status/completion to API | Know about agent-sandbox, manage its own lifecycle |
| **Adapter** | Handle GitHub webhooks, post GitHub comments, query API for deduplication | Read CRDs, talk to K8s |
| **Agent-sandbox** | Manage Sandbox lifecycle, warm pools, pod creation, headless services, NetworkPolicies | Know about Shepherd tasks |

### Data Flow (Single Task)

```text
1. Developer comments "@shepherd fix the null pointer"
         |
         v
2. GitHub webhook --> github-adapter (Trigger App)
         |
         v
3. Adapter checks API for active tasks on this repo+issue
   - If active: posts "already running" comment, stops
   - If none or last failed: continues
         |
         v
4. Adapter extracts: repo_url, issue body, comments, author
   POST /api/v1/tasks to API with:
   - repo_url, task description, context
   - callback_url pointing back to adapter
   - labels for deduplication (shepherd.io/repo, shepherd.io/issue)
         |
         v
5. API validates request, gzip-compresses context, creates AgentTask CRD
         |
         v
6. Operator sees new AgentTask, creates SandboxClaim
   referencing SandboxTemplate (e.g. "shepherd-claude-runner")
         |
         v
7. Agent-sandbox controller:
   - Claims warm pod from SandboxWarmPool (or creates new)
   - Creates headless Service ({claim-name}.{ns}.svc.cluster.local)
   - SandboxClaim status --> Ready=True
         |
         v
8. Operator detects SandboxClaim ready via Sandbox status watch
   POSTs to http://{sandbox-fqdn}:8888/task
   Body: { "taskID": "task-abc123", "apiURL": "http://shepherd-api:8080" }
         |
         v
9. Runner entrypoint receives POST, begins work:
   GET {apiURL}/api/v1/tasks/{taskID}        --> description, context
   GET {apiURL}/api/v1/tasks/{taskID}/token   --> GitHub installation token
   git clone with token
   write TASK.md to repo
   claude -p "Read TASK.md and implement what it describes"
   POST {apiURL}/api/v1/tasks/{taskID}/status --> progress updates
         |
         v
10. Claude Code creates PR, entrypoint reports completion
    POST {apiURL}/api/v1/tasks/{taskID}/status --> { event: completed, pr_url: ... }
    Container exits
         |
         v
11. Operator detects completion:
    Sandbox Ready=False + pod exit code 0 --> AgentTask Succeeded=True
    Deletes SandboxClaim (cascades cleanup)
         |
         v
12. API's CRD status watcher detects terminal state
    POSTs to adapter callback --> adapter posts final GitHub comment
```

Step 12 ensures the adapter is notified even if the runner crashes without calling its completion hook.

### Data Flow (Fleet Migration)

```text
1. User creates a FleetTask CRD:
   - pattern: "github.com/org/*" or explicit repo list
   - task description and context
   - concurrency limit (e.g. 5 at a time)
         |
         v
2. Operator expands FleetTask into individual AgentTask CRDs:
   - One AgentTask per matching repo
   - Labels: shepherd.io/fleet-task={fleet-name}
   - Owner reference: FleetTask
         |
         v
3. Each AgentTask follows the single-task flow (steps 6-12 above)
   Operator respects concurrency limit
         |
         v
4. Operator tracks aggregate status on FleetTask:
   - Total tasks, completed, failed, in-progress
   - Terminal when all child tasks are terminal
         |
         v
5. API reports fleet-level status to adapter callback
```

### Owner Reference Chain

```text
AgentTask --> owns --> SandboxClaim
                          |
                    agent-sandbox creates:
                          +--> Sandbox --> Pod, Service
```

Deleting an AgentTask cascades: SandboxClaim deletion triggers agent-sandbox cleanup of Sandbox, Pod, and Service.

For fleet migrations:
```text
FleetTask --> owns --> AgentTask[] --> owns --> SandboxClaim[]
```

## CRD Specification

### AgentTask (updated)

```yaml
apiVersion: toolkit.shepherd.io/v1alpha1
kind: AgentTask
metadata:
  name: task-abc123
  namespace: shepherd
  labels:
    shepherd.io/repo: org-repo
    shepherd.io/issue: "123"
spec:
  repo:
    url: "https://github.com/org/repo.git"
    ref: "main"
  task:
    description: "Fix the null pointer exception in login.go"
    context: "<gzip+base64 encoded>"
    contextEncoding: "gzip"
    contextUrl: "https://github.com/org/repo/issues/123"
  callback:
    url: "https://github-adapter.example.com/callback"
  runner:
    sandboxTemplateName: "shepherd-claude-runner"
    timeout: 30m
    serviceAccountName: shepherd-agent
    resources:
      requests:
        memory: "512Mi"
        cpu: "500m"
      limits:
        memory: "2Gi"
        cpu: "2000m"
status:
  observedGeneration: 1
  startTime: "2026-02-01T10:00:00Z"
  completionTime: "2026-02-01T10:15:00Z"
  conditions:
    - type: Succeeded
      status: "True"
      reason: Succeeded
      message: "Pull request created"
      lastTransitionTime: "2026-02-01T10:15:00Z"
      observedGeneration: 1
  result:
    prUrl: "https://github.com/org/repo/pull/42"
    error: ""
  sandboxClaimName: task-abc123
```

### AgentTask Type Definitions (updated)

```go
type AgentTaskSpec struct {
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
    Repo     RepoSpec     `json:"repo"`
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
    Task     TaskSpec     `json:"task"`
    Callback CallbackSpec `json:"callback"`
    Runner   RunnerSpec   `json:"runner,omitempty"`
}

type RunnerSpec struct {
    // SandboxTemplateName references a SandboxTemplate for the runner environment.
    // +kubebuilder:validation:Required
    SandboxTemplateName string `json:"sandboxTemplateName"`

    // Timeout is the maximum duration for task execution.
    // The operator enforces this via its own timer since agent-sandbox v0.1.0
    // does not support Lifecycle/ShutdownTime.
    // +kubebuilder:default="30m"
    Timeout metav1.Duration `json:"timeout,omitempty"`

    // ServiceAccountName for the sandbox pod (if overriding template default).
    ServiceAccountName string `json:"serviceAccountName,omitempty"`

    // Resources override for the runner container.
    // If empty, uses the SandboxTemplate defaults.
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

**Changes from v1:**
- `RunnerSpec.Image` removed — the SandboxTemplate controls the image
- `RunnerSpec.SandboxTemplateName` added — references the SandboxTemplate to use
- `AgentTaskStatus.JobName` replaced by `AgentTaskStatus.SandboxClaimName`

### FleetTask (new CRD)

```yaml
apiVersion: toolkit.shepherd.io/v1alpha1
kind: FleetTask
metadata:
  name: fleet-update-deps
  namespace: shepherd
spec:
  repos:
    # Option A: explicit list
    urls:
      - "https://github.com/org/repo1.git"
      - "https://github.com/org/repo2.git"
    # Option B: pattern (future — requires GitHub API listing)
    # pattern: "github.com/org/*"
  task:
    description: "Update all Go dependencies to latest"
    context: "Run go get -u ./... && go mod tidy"
  callback:
    url: "https://github-adapter.example.com/fleet-callback"
  runner:
    sandboxTemplateName: "shepherd-go-runner"
    timeout: 30m
  concurrency: 5  # max parallel tasks
status:
  total: 10
  completed: 7
  failed: 1
  inProgress: 2
  conditions:
    - type: Succeeded
      status: "Unknown"
      reason: Running
      message: "7/10 tasks completed, 1 failed"
```

### Condition State Machine

Primary condition is `Succeeded` (run-to-completion pattern):

| Status | Reason | Meaning |
| ------ | ------ | ------- |
| Unknown | Pending | Waiting for sandbox to start |
| Unknown | Running | Sandbox running, task assigned |
| True | Succeeded | Task completed, PR created |
| False | Failed | Task failed |
| False | TimedOut | Timeout exceeded (operator-enforced) |
| False | OOM | Container killed: OOMKilled or exit 137 |
| False | Cancelled | User cancelled |

Secondary condition `Notified` (managed by API server):

| Status | Reason | Meaning |
| ------ | ------ | ------- |
| True | CallbackSent | Adapter callback sent successfully |
| True | CallbackFailed | Callback failed (logged, won't retry) |

## API Server

### Endpoints

#### Existing (unchanged)

| Method | Path | Purpose |
| ------ | ---- | ------- |
| POST | `/api/v1/tasks` | Create AgentTask CRD |
| GET | `/api/v1/tasks` | List tasks with filters |
| GET | `/api/v1/tasks/{taskID}` | Get task details |
| POST | `/api/v1/tasks/{taskID}/status` | Runner progress callbacks |
| GET | `/healthz` | Health check |
| GET | `/readyz` | Ready check |

#### New Endpoints

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET | `/api/v1/tasks/{taskID}/data` | Serve task description + decompressed context to runner |
| GET | `/api/v1/tasks/{taskID}/token` | Generate GitHub installation token for runner |
| POST | `/api/v1/fleet-tasks` | Create FleetTask CRD (future) |
| GET | `/api/v1/fleet-tasks` | List fleet tasks (future) |
| GET | `/api/v1/fleet-tasks/{fleetTaskID}` | Get fleet task details (future) |

#### GET /api/v1/tasks/{taskID}/data

Returns task description and decompressed context. The API transparently decompresses gzip+base64 context from the CRD.

```json
{
  "description": "Fix the null pointer exception in login.go",
  "context": "<plaintext context, decompressed>",
  "contextUrl": "https://github.com/org/repo/issues/123",
  "repo": {
    "url": "https://github.com/org/repo.git",
    "ref": "main"
  }
}
```

The decompression logic currently in `cmd/shepherd-init/taskfiles.go` (`decodeContext()`) moves here.

#### GET /api/v1/tasks/{taskID}/token

Generates a short-lived GitHub installation token scoped to the task's repository. The Runner App private key is mounted into the API server pod (not runner pods).

```json
{
  "token": "ghs_xxxxxxxxxxxxxxxxxxxx",
  "expiresAt": "2026-02-01T11:00:00Z"
}
```

The JWT generation and token exchange logic currently in `cmd/shepherd-init/github.go` (`createJWT()`, `exchangeToken()`) moves here.

**Security improvement**: The Runner App private key is mounted once into the API server, instead of into every runner pod via K8s Secret.

### CRD Status Watcher

The API runs a standalone controller-runtime cache that watches AgentTask resources. When the operator updates a CRD to a terminal condition (`Succeeded=True` or `Succeeded=False`), the API reads the callback URL from `spec.callback.url` and notifies the adapter. The `Notified` condition prevents duplicate callbacks.

This is unchanged from the current implementation.

## Operator Reconciliation

### Reconcile Loop (AgentTask)

```text
1. Fetch AgentTask
2. If terminal --> return (skip re-reconciliation)
3. If no Succeeded condition --> set Pending, requeue
4. Look for existing SandboxClaim (name = task name)
5. If no SandboxClaim --> create one:
   - Set ownerReference (AgentTask --> SandboxClaim)
   - Set sandboxTemplateRef from task.Spec.Runner.SandboxTemplateName
   - Record SandboxClaimName in AgentTask status
   - Start timeout timer (operator-managed, not agent-sandbox Lifecycle)
6. Get Sandbox resource (same name as claim):
   - Not found --> still creating, requeue
   - Found, check Ready condition:
     a. Ready=True --> assign task (if not already assigned)
     b. Ready=False + pod terminated --> check exit code, mark terminal
     c. Ready=False (other) --> still starting, requeue
7. If task assigned + running --> check for timeout:
   - If now > startTime + timeout --> delete SandboxClaim, mark TimedOut
8. On terminal state --> delete SandboxClaim (cascades cleanup)
```

### Task Assignment

When the operator detects `Sandbox Ready=True`, it POSTs to the runner:

```go
func (r *AgentTaskReconciler) assignTask(ctx context.Context, task *v1alpha1.AgentTask, sandbox *sandboxv1alpha1.Sandbox) error {
    url := fmt.Sprintf("http://%s:8888/task", sandbox.Status.ServiceFQDN)
    body := TaskAssignment{
        TaskID: task.Name,
        APIURL: r.Config.APIURL,
    }
    // POST with timeout and retry (up to 5 attempts, 2s between)
    // Mark task as Running on success
    // Track assignment in annotation to prevent duplicate POSTs
}
```

The operator tracks assignment state via an annotation (`shepherd.io/task-assigned: "true"`) to ensure idempotency across reconcile loops.

### Failure Detection

The operator inspects pod container status directly (more reliable than Job condition message parsing):

```go
func classifySandboxFailure(sandbox *sandboxv1alpha1.Sandbox, pod *corev1.Pod) (string, string) {
    // Check pod container status for the "runner" container
    for _, cs := range pod.Status.ContainerStatuses {
        if cs.Name != "runner" { continue }
        if cs.State.Terminated == nil { continue }

        if cs.State.Terminated.Reason == "OOMKilled" {
            return ReasonOOM, "Container killed: OOM"
        }
        if cs.State.Terminated.ExitCode == 137 {
            return ReasonOOM, "Container killed: exit code 137"
        }
        if cs.State.Terminated.ExitCode == 0 {
            return ReasonSucceeded, "Task completed"
        }
        return ReasonFailed, fmt.Sprintf("Exit code %d", cs.State.Terminated.ExitCode)
    }
    return ReasonFailed, "Unknown failure"
}
```

### Timeout Management

Since agent-sandbox v0.1.0 does not support `Lifecycle.ShutdownTime`, the operator manages timeout:

1. Record `status.startTime` when SandboxClaim is created
2. On each reconcile, check if `now > startTime + spec.runner.timeout`
3. If timed out: delete SandboxClaim, mark AgentTask as `Succeeded=False`, reason `TimedOut`

When agent-sandbox adds Lifecycle support, this can be simplified by setting `shutdownTime` on the SandboxClaim and reacting to the sandbox expiry condition.

### Reconcile Loop (FleetTask)

```text
1. Fetch FleetTask
2. If terminal --> return
3. Expand repo list into AgentTask CRDs (if not already created):
   - One AgentTask per repo
   - OwnerReference: FleetTask --> AgentTask
   - Labels: shepherd.io/fleet-task={fleet-name}
4. Count child AgentTask states (completed, failed, in-progress)
5. If in-progress < concurrency limit AND pending tasks exist:
   - Create next AgentTask (it will be picked up by the single-task reconciler)
6. Update FleetTask status with aggregate counts
7. If all tasks terminal --> mark FleetTask terminal
```

## Runner Entrypoint

The runner container image contains:
- Claude Code CLI (pre-installed)
- Git, gh CLI
- Language tools (Go for MVP, more via additional SandboxTemplates)
- `shepherd-runner` binary (thin Go entrypoint)

### Entrypoint Lifecycle

```text
Phase 1: HTTP server on :8888
  - GET /healthz (readiness probe)
  - POST /task (receives assignment from operator)
  - Blocks until task assignment arrives

Phase 2: Pull task data from API
  - GET {apiURL}/api/v1/tasks/{taskID}/data --> description, context, repo info
  - GET {apiURL}/api/v1/tasks/{taskID}/token --> GitHub installation token

Phase 3: Setup workspace
  - Configure git credentials with token
  - git clone {repoURL} --branch {ref} /workspace
  - Write TASK.md with description + context

Phase 4: Run Claude Code
  - claude -p "Read TASK.md and implement what it describes. Create a branch and PR when done."
  - Working directory: /workspace

Phase 5: Report results
  - POST {apiURL}/api/v1/tasks/{taskID}/status --> { event: completed, pr_url: "..." }
  - Exit 0 on success, exit 1 on failure
```

### Readiness Probe

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
      restartPolicy: Never
```

Additional templates for different languages/runtimes (e.g., `shepherd-python-runner`, `shepherd-node-runner`) can be created by changing the runner image and resource limits.

## SandboxWarmPool

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxWarmPool
metadata:
  name: claude-runner-pool
  namespace: shepherd
spec:
  templateName: shepherd-claude-runner
  minReady: 1
  maxReady: 5
```

Warm pools reduce startup latency from ~8 seconds (cold start observed in PoC) to near-instant.

## Callback Contract

### Runner to API (progress updates)

```json
POST /api/v1/tasks/{task_id}/status
Content-Type: application/json

{
  "event": "started" | "progress" | "completed" | "failed",
  "message": "Cloning repository...",
  "details": {
    "pr_url": "https://github.com/org/repo/pull/123",
    "error": "Build failed: missing import"
  }
}
```

### API to Adapter (HMAC-signed callback)

```json
POST {callback_url}
Content-Type: application/json
X-Shepherd-Signature: sha256=...

{
  "task_id": "task-abc123",
  "event": "started" | "progress" | "completed" | "failed",
  "message": "Working on your request...",
  "details": { ... }
}
```

## Repository Structure (target)

```
shepherd/
├── cmd/
│   ├── shepherd/              # Kong CLI entrypoint (api, operator, github)
│   └── shepherd-runner/       # Runner entrypoint binary
├── api/
│   └── v1alpha1/              # Kubebuilder-managed CRD types (AgentTask, FleetTask)
├── internal/
│   └── controller/            # Kubebuilder-managed controllers
│       ├── agenttask_controller.go   # AgentTask reconciler (sandbox-based)
│       ├── fleettask_controller.go   # FleetTask reconciler
│       ├── sandbox_builder.go        # SandboxClaim construction
│       └── failure.go                # Pod-based failure classification
├── config/                    # Kubebuilder-managed manifests (Kustomize)
├── pkg/
│   ├── operator/              # Module orchestrator, errgroup lifecycle
│   ├── api/                   # REST API server (chi router)
│   │   ├── server.go
│   │   ├── handler_tasks.go   # Task CRUD
│   │   ├── handler_status.go  # Runner callbacks
│   │   ├── handler_data.go    # Task data serving (NEW)
│   │   ├── handler_token.go   # Token generation (NEW)
│   │   ├── watcher.go         # CRD status watcher
│   │   ├── callback.go        # HMAC-signed adapter callbacks
│   │   └── compress.go        # Gzip compression
│   └── adapters/
│       └── github/            # GitHub webhook handler, App client
├── deploy/
│   ├── helm/                  # Helm charts for deployment
│   └── manifests/             # SandboxTemplate, SandboxWarmPool YAMLs
├── PROJECT                    # Kubebuilder project file
├── Makefile
├── .ko.yaml
└── examples/
```

### What Gets Retired

| Current | Status | Logic Moves To |
| ------- | ------ | -------------- |
| `cmd/shepherd-init/` | Retired | API server (`handler_data.go`, `handler_token.go`) |
| `internal/controller/job_builder.go` | Replaced | `internal/controller/sandbox_builder.go` |
| Job-based failure classification | Replaced | Pod-based failure classification via direct container status |
| `RunnerSpec.Image` field | Removed | Controlled by SandboxTemplate |
| `AgentTaskStatus.JobName` | Replaced | `AgentTaskStatus.SandboxClaimName` |

## Security Considerations

- **Runner App private key**: Mounted once into API server pod, not into runner pods. Tokens generated on-demand per runner request.
- **Installation tokens**: Short-lived (1 hour), scoped to specific repos via GitHub App installation.
- **Pre-approved runner images**: Controlled by SandboxTemplate, not by API callers.
- **Internal communication**: Runner to API uses cluster-internal networking.
- **Signed callbacks**: API to adapter uses HMAC-SHA256 signature verification.
- **Webhook verification**: GitHub webhooks verified via `X-Hub-Signature-256` with constant-time comparison.
- **Pod security**: Non-root containers, seccomp RuntimeDefault profile.
- **Network isolation**: SandboxTemplate can specify NetworkPolicy (default-deny with explicit egress for DNS, Shepherd API, GitHub API, Anthropic API).
- **Token endpoint access**: `GET /tasks/{id}/token` is cluster-internal only. Consider adding per-task bearer tokens for defense-in-depth.

## Namespace Strategy

- AgentTask and FleetTask are namespace-scoped (supports future multi-tenancy)
- Default namespace: `shepherd`
- SandboxTemplate and SandboxWarmPool deployed in same namespace
- Operator uses ClusterRole/ClusterRoleBinding to watch all namespaces

## Dependencies (go.mod additions)

```go
require (
    // Existing deps unchanged...

    // Sandbox (NEW)
    sigs.k8s.io/agent-sandbox v0.1.0
)
```

Note: agent-sandbox v0.1.0 uses controller-runtime v0.22.2 and K8s v0.34.1. The main shepherd module uses controller-runtime v0.23.0 and K8s v0.35.0. This version mismatch needs to be resolved — likely by importing only the API types (`api/v1alpha1`) from agent-sandbox and using the main module's controller-runtime for all operations.

## Future Backend Extensibility Notes

When a second execution backend (Modal, Cloudflare, etc.) becomes necessary, the refactoring surface is:

1. **`internal/controller/sandbox_builder.go`** — builds SandboxClaims. Would become one implementation of a `SandboxBuilder` interface.
2. **`internal/controller/agenttask_controller.go`** — the `assignTask()`, `classifySandboxFailure()`, and timeout management functions. These would move behind an `ExecutionBackend` interface.
3. **`internal/controller/failure.go`** — pod-based failure classification. Different backends would have different failure detection mechanisms.

The API server and adapter are already backend-agnostic — they only talk to CRDs and HTTP endpoints. No changes needed there.

A natural interface shape (not to implement now, just to document the boundary):

```go
// Future — not for implementation now
type ExecutionBackend interface {
    // Create starts execution for the given task.
    Create(ctx context.Context, task *v1alpha1.AgentTask) (claimName string, err error)

    // Status returns the current execution state.
    Status(ctx context.Context, task *v1alpha1.AgentTask) (ExecutionStatus, error)

    // AssignTask sends the task to the running sandbox/container.
    AssignTask(ctx context.Context, task *v1alpha1.AgentTask) error

    // Cleanup removes the execution environment.
    Cleanup(ctx context.Context, task *v1alpha1.AgentTask) error
}
```

## Open Questions

1. **agent-sandbox API version alignment**: agent-sandbox v0.1.0 depends on K8s v0.34.1 / controller-runtime v0.22.2. Shepherd uses K8s v0.35.0 / controller-runtime v0.23.0. Can we import only the API types without pulling in the full controller? Needs testing.

2. **Warm pool sizing**: What's the right `minReady` for different usage patterns? Start with 1 for development, adjust based on usage data.

3. **Fleet task repo discovery**: MVP uses explicit repo lists. Pattern-based discovery (e.g., "all repos in org") requires GitHub API access from the adapter or API server. Deferred to post-MVP.

4. **Runner HTTP server idle timeout**: Warm pool pods sitting idle should eventually self-terminate. This may be handled by agent-sandbox's warm pool controller, or the entrypoint could implement an idle timeout (e.g., 30 minutes without receiving a task).

5. **Log capture before cleanup**: Pod logs are accessible while the pod exists. The operator should capture logs (or at least confirm the runner reported its results via API callback) before deleting the SandboxClaim. What's the right mechanism?

6. **SandboxTemplate per runner vs per language**: Should we have one template per language runtime (Go, Python, Node) or one universal template with all runtimes? Templates are cheap to create but the runner images get large. Recommend per-language templates.

## Historical Context (from thoughts/)

- `thoughts/research/2026-01-25-shepherd-intial-arch.md` — Original architecture, Jobs-based
- `thoughts/research/2026-01-27-shepherd-design.md` — Refined design, Jobs-based, tech stack decisions
- `thoughts/plans/2026-01-27-operator-implementation.md` — Operator implementation plan (Jobs-based)
- `thoughts/plans/2026-01-28-api-server-implementation.md` — API server implementation plan
- `thoughts/plans/2026-01-28-init-container.md` — Init container implementation plan (retired in this architecture)
- `thoughts/research/2026-01-30-agent-sandbox-integration.md` — Settled agent-sandbox architecture
- `thoughts/research/2026-01-31-poc-sandbox-learnings.md` — PoC findings (latency, cleanup, connectivity)
- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` — Session management patterns for future interactive mode
- `thoughts/research/2026-01-28-oom-detection-without-pod-watching.md` — OOM detection research
- `thoughts/reviews/2026-01-28-api-server-plan-review.md` — API server plan review
- `thoughts/plans/2026-01-28-phase5-failure-handling-podfailurepolicy.md` — Pod failure policy (replaced by direct pod status inspection)
