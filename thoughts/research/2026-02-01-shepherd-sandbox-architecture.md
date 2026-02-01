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
last_updated_note: "Review round: token→Secret, simplified failure model (no Pod reads), rate limiting, context size limits, branch naming, RBAC section"
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
3. **Runner-pull model** — the runner pulls its configuration from the API instead of receiving it via environment variables and volumes, authenticated via per-task bearer tokens

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
- agent-sandbox v0.1.0 has no `Lifecycle` field — operator manages timeout and cleanup. Note: `Lifecycle` with `ShutdownTime` support exists on unreleased main (v0.1.0+46 commits). When a release including Lifecycle ships, timeout management can be delegated to agent-sandbox.
- Multiple concurrent sandboxes work without port conflicts

## Goals

### MVP (Part 1)

1. **Issue-driven development** — Trigger agent from GitHub issue/PR comment to work on a single repo

### MVP (Part 2)

2. **Fleet migrations** — Run the same transformation across many repos (Spotify pattern). Implemented via CLI + API endpoints + shared labels (no FleetTask CRD). See [Fleet Migrations](#fleet-migrations-mvp-part-2) section.

### Future

3. **Scheduled SRE tasks** — Daily/weekly automated analysis and fixes
4. **Interactive sessions** — Long-running dev environments with remote access (see `thoughts/research/2026-01-31-background-agents-session-management-learnings.md`)
5. **FleetTask CRD** — Server-side fleet orchestration with concurrency limiting, aggregate status, and cascade deletion. Upgrade from the CLI-based approach when server-side coordination proves necessary.

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
| **Operator** | Watch AgentTask CRDs, create SandboxClaims, watch Sandbox status, POST task assignment to runner, update AgentTask conditions, delete SandboxClaim on completion, manage timeout, create runner token Secrets | Generate GitHub tokens, decompress context, talk to GitHub, read Pod resources, make external HTTP calls (except runner task assignment within cluster) |
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
5. API validates request:
   - Accepts plaintext context in POST body
   - Compresses via gzip, then base64-encodes for CRD storage
   - Rejects with HTTP 413 if compressed size exceeds ~1.4MB
   - Creates AgentTask CRD with encoded context in spec.task.context
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
   Generates per-task bearer token, stores hash in K8s Secret (owned by AgentTask)
   POSTs to http://{sandbox-fqdn}:8888/task
   Body: { "taskID": "task-abc123", "apiURL": "http://shepherd-api:8080", "token": "..." }
         |
         v
9. Runner entrypoint receives POST, begins work:
   GET {apiURL}/api/v1/tasks/{taskID}/data    --> description, context (with Bearer token)
   GET {apiURL}/api/v1/tasks/{taskID}/token   --> GitHub installation token (with Bearer token)
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
11. Operator detects completion via two signals:
    a. Runner reported success via API --> API updated CRD --> AgentTask Succeeded=True
    b. Sandbox Ready=False without prior success callback --> AgentTask Failed
    Deletes SandboxClaim (cascades cleanup)
         |
         v
12. API's CRD status watcher detects terminal state
    POSTs to adapter callback --> adapter posts final GitHub comment
```

Step 12 ensures the adapter is notified even if the runner crashes without calling its completion hook.

### Data Flow (Fleet Migration — MVP Part 2)

```text
1. User runs CLI tool:
   shepherd fleet --repos repo1,repo2,repo3 \
     --description "Update Go deps" \
     --context "Run go get -u ./..." \
     --concurrency 5

   The CLI may use an LLM to customize the prompt per-repo
   (like Spotify's approach) before submitting.
         |
         v
2. CLI creates N AgentTasks via POST /api/v1/tasks:
   - One per repo
   - All share a label: shepherd.io/fleet={fleet-name}
   - CLI manages concurrency (submit 5, wait for one to finish, submit next)
         |
         v
3. Each AgentTask follows the single-task flow (steps 6-12 above)
         |
         v
4. CLI polls GET /api/v1/tasks?fleet={fleet-name} for aggregate status
   Displays progress: "7/10 completed, 1 failed, 2 in-progress"
         |
         v
5. Fleet complete when all tasks reach terminal state
```

No FleetTask CRD is needed for MVP. The CLI is the orchestration layer. Users don't need K8s access — they use the CLI which talks to the API. Aggregate visibility comes from the shared fleet label + API query.

Cancellation: `kubectl delete agenttask -l shepherd.io/fleet={fleet-name}` or a future `DELETE /api/v1/fleet/{fleet-name}` endpoint.

### Owner Reference Chain

```text
AgentTask --> owns --> SandboxClaim
         |                |
         |          agent-sandbox creates:
         |                +--> Sandbox --> Pod, Service
         |
         +--> owns --> Secret ({task-name}-token)
```

Deleting an AgentTask cascades: SandboxClaim deletion triggers agent-sandbox cleanup of Sandbox, Pod, and Service. The token Secret is also deleted via owner reference.

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
    shepherd.io/source-type: issue
    shepherd.io/source-id: "123"
spec:
  repo:
    url: "https://github.com/org/repo.git"
    ref: "main"
  task:
    description: "Fix the null pointer exception in login.go"
    context: "<gzip+base64 encoded, max ~1.4MB compressed — see Context Size Limits>"
    contextEncoding: "gzip"
    sourceUrl: "https://github.com/org/repo/issues/123"
    sourceType: "issue"
    sourceID: "123"
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

type TaskSpec struct {
    // Description is the task description sent to the agent.
    // +kubebuilder:validation:Required
    Description string `json:"description"`

    // Context is additional context for the task, gzip-compressed then base64-encoded.
    // The API accepts raw text, compresses via gzip, then base64-encodes for CRD storage.
    // Maximum compressed size is ~1.4MB (etcd 1.5MB object limit minus CRD overhead).
    // The API returns HTTP 413 if the compressed context exceeds this limit.
    Context string `json:"context,omitempty"`

    // ContextEncoding indicates the encoding of the context field.
    // Currently only "gzip" is supported (gzip + base64).
    ContextEncoding string `json:"contextEncoding,omitempty"`

    // SourceURL is the origin of the task (e.g., GitHub issue URL). Informational only.
    SourceURL string `json:"sourceUrl,omitempty"`

    // SourceType identifies the trigger type: "issue", "pr", or "fleet".
    // Used for deduplication and branch naming.
    SourceType string `json:"sourceType,omitempty"`

    // SourceID identifies the specific trigger instance (e.g., issue number, fleet name).
    // Used for deduplication and branch naming.
    SourceID string `json:"sourceID,omitempty"`
}

type RunnerSpec struct {
    // SandboxTemplateName references a SandboxTemplate for the runner environment.
    // +kubebuilder:validation:Required
    SandboxTemplateName string `json:"sandboxTemplateName"`

    // Timeout is the maximum duration for task execution.
    // The operator enforces this via its own timer since agent-sandbox v0.1.0
    // does not support Lifecycle/ShutdownTime. When a future agent-sandbox release
    // includes Lifecycle support, this can be delegated to the SandboxClaim's
    // ShutdownTime field instead.
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
- `TaskSpec.ContextURL` renamed to `TaskSpec.SourceURL` — clarifies this is the origin of the task (e.g., GitHub issue URL), not a URL to download context from
- `TaskSpec.SourceType` added — identifies trigger type ("issue", "pr", "fleet") for deduplication and branch naming
- `TaskSpec.SourceID` added — identifies specific trigger instance (issue number, fleet name)

**Labels vs spec fields**: `sourceType` and `sourceID` exist in both `spec.task` (canonical data) and as labels (`shepherd.io/source-type`, `shepherd.io/source-id`). The spec fields are the source of truth. The labels mirror the spec to enable efficient K8s label selector queries for deduplication (e.g., "is there an active task for this repo + issue?") and fleet filtering without fetching every CRD. The API server sets both when creating the AgentTask.

### Condition State Machine

Primary condition is `Succeeded` (run-to-completion pattern):

| Status | Reason | Meaning |
| ------ | ------ | ------- |
| Unknown | Pending | Waiting for sandbox to start |
| Unknown | Running | Sandbox running, task assigned |
| True | Succeeded | Task completed, PR created |
| False | Failed | Task failed (runner crashed or reported failure) |
| False | TimedOut | Timeout exceeded (operator-enforced) |
| False | Cancelled | User cancelled |

Note: The operator does not distinguish between OOM kills, crashes, and other failures. The runner is the authoritative source for success — it reports completion via the API callback. Any sandbox termination without a prior success callback is classified as `Failed`. This simplification avoids requiring Pod read RBAC on the operator (see [Failure Detection](#failure-detection)).

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
| GET | `/api/v1/tasks/{taskID}/data` | Serve task description + decompressed context to runner (authenticated via per-task bearer token) |
| GET | `/api/v1/tasks/{taskID}/token` | Generate GitHub installation token for runner (authenticated via per-task bearer token) |

The existing `GET /api/v1/tasks` endpoint gains a `fleet` query parameter for fleet label filtering: `GET /api/v1/tasks?fleet={fleet-name}`.

#### GET /api/v1/tasks/{taskID}/data

Returns task description and decompressed context. The API transparently decompresses the context: the CRD stores gzip-compressed, base64-encoded data. The `/data` endpoint decodes base64, decompresses gzip, and returns plaintext.

```json
{
  "description": "Fix the null pointer exception in login.go",
  "context": "<plaintext context, decompressed>",
  "sourceUrl": "https://github.com/org/repo/issues/123",
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

### Context Size Limits

The API accepts raw plaintext context in `POST /api/v1/tasks`, compresses it via gzip, then base64-encodes the result for storage in the AgentTask CRD's `spec.task.context` field.

etcd enforces a 1.5MB maximum object size. After accounting for CRD metadata, spec fields, and status (~100KB overhead), the effective limit for compressed+encoded context is **~1.4MB**. Given typical gzip compression ratios of 4-7x on text, this supports roughly 5-10MB of plaintext context.

The API validates the compressed size before creating the CRD:
- If compressed+encoded context exceeds 1.4MB: return HTTP `413 Request Entity Too Large`
- The error response includes the compressed size and the limit, so callers can adjust

For contexts exceeding this limit (rare — most issue threads and code snippets are well under 1MB), callers should truncate or summarize the context before submission.

### Rate Limiting

API endpoints are rate-limited to prevent abuse, especially while runner endpoints are unauthenticated in MVP Part 1.

| Endpoint | Limit | Scope | Reasoning |
| -------- | ----- | ----- | --------- |
| `POST /api/v1/tasks` | 10 req/min | Per source IP | Prevent task creation floods |
| `GET /api/v1/tasks/{id}/data` | 10 req/min | Per task ID | Runner calls once, maybe retries |
| `GET /api/v1/tasks/{id}/token` | 5 req/min | Per task ID | Tokens last 1 hour, single call at start |
| `POST /api/v1/tasks/{id}/status` | 30 req/min | Per task ID | Runner sends frequent progress updates |
| `GET /api/v1/tasks` | 60 req/min | Per source IP | CLI polling for fleet status |

Implementation: `golang.org/x/time/rate` with in-memory limiters keyed by task ID or source IP. For multi-replica API deployments in production, consider Redis-backed limiters.

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
     b. Ready=False + task was previously Running --> mark Failed
        (runner did not report success before sandbox terminated)
     c. Ready=False (never was Running) --> still starting, requeue
7. If task assigned + running --> check for timeout:
   - If now > startTime + timeout --> delete SandboxClaim, mark TimedOut
8. On terminal state --> delete SandboxClaim (cascades cleanup)
```

Note: The operator does NOT read Pod resources. It relies on Sandbox Ready condition transitions
and the runner's status callbacks via the API to determine task outcome. This avoids requiring
Pod read RBAC permissions.

### Task Assignment

When the operator detects `Sandbox Ready=True`, it POSTs to the runner:

```go
func (r *AgentTaskReconciler) assignTask(ctx context.Context, task *v1alpha1.AgentTask, sandbox *sandboxv1alpha1.Sandbox) error {
    url := fmt.Sprintf("http://%s:8888/task", sandbox.Status.ServiceFQDN)
    token := generateRunnerToken() // random 32-byte hex string
    body := TaskAssignment{
        TaskID: task.Name,
        APIURL: r.Config.APIURL,
        Token:  token,
    }
    // POST with timeout and retry (up to 5 attempts, 2s between)
    // Create K8s Secret "{task-name}-token" with owner reference to AgentTask:
    //   data: { "hash": SHA-256(token) }
    // Mark task as Running on success
    // Track assignment in annotation to prevent duplicate POSTs
}
```

The operator tracks assignment state via an annotation (`shepherd.io/task-assigned: "true"`) to ensure idempotency across reconcile loops. The runner token SHA-256 hash is stored in a K8s Secret named `{task-name}-token` with an owner reference to the AgentTask (cascade deletion). The API validates runner requests by reading the Secret via an informer cache.

### Failure Detection

The operator uses a simplified failure model that does **not** require Pod read RBAC. Instead of inspecting pod container status directly, it relies on two signals:

1. **Runner success callback**: The runner reports completion via `POST /api/v1/tasks/{id}/status` with `event: completed`. The API updates the AgentTask CRD status. The operator sees `Succeeded=True` and proceeds to cleanup.

2. **Sandbox Ready=False transition**: If the Sandbox transitions to `Ready=False` after the task was assigned (Running state), and no success callback was received, the operator marks the task as `Failed`.

```go
func classifySandboxTermination(task *v1alpha1.AgentTask, sandbox *sandboxv1alpha1.Sandbox) (string, string) {
    // If the runner already reported success via API callback, the CRD
    // is already in terminal state — this function is only called for
    // unexpected sandbox termination.

    readyCond := meta.FindStatusCondition(sandbox.Status.Conditions, "Ready")
    if readyCond == nil {
        return ReasonFailed, "Sandbox status unavailable"
    }

    if readyCond.Reason == "SandboxExpired" {
        return ReasonTimedOut, "Sandbox expired"
    }

    // Generic failure — could be OOM, crash, or any other pod termination.
    // The operator does not distinguish because it does not read Pod resources.
    return ReasonFailed, fmt.Sprintf("Sandbox terminated: %s", readyCond.Message)
}
```

**Trade-off**: This model does not distinguish between OOM kills, crashes, and other failures. The `Failed` reason covers all non-success outcomes. Granular failure classification (OOM vs crash) would require either Pod read RBAC or a future agent-sandbox enhancement that propagates termination details to Sandbox status (see [Open Questions](#open-questions)).

**Security benefit**: The operator does not need `get`/`list`/`watch` permissions on Pod resources, reducing its RBAC surface. It only needs permissions on AgentTask CRDs, SandboxClaim CRDs, Sandbox CRDs, and Secrets.

### Timeout Management

Since agent-sandbox v0.1.0 (the latest released version) does not support `Lifecycle.ShutdownTime`, the operator manages timeout:

1. Record `status.startTime` when SandboxClaim is created
2. On each reconcile, check if `now > startTime + spec.runner.timeout`
3. If timed out: delete SandboxClaim, mark AgentTask as `Succeeded=False`, reason `TimedOut`

**Future**: `Lifecycle` with `ShutdownTime` and `ShutdownPolicy` support exists on agent-sandbox's unreleased main branch (post-v0.1.0). When a release including Lifecycle ships, the operator can set `ShutdownTime` on the SandboxClaim and react to the `SandboxExpired` condition instead of managing its own timer. This would also give the sandbox time to gracefully shut down via `ShutdownPolicy`.

## Fleet Migrations (MVP Part 2)

Fleet migrations are orchestrated by a CLI tool, not a CRD. The CLI creates individual AgentTasks with a shared label and manages concurrency client-side.

### CLI Responsibilities

- Accept a list of repos (explicit URLs or from a file)
- Optionally use an LLM to customize the prompt per-repo (Spotify pattern)
- Create AgentTasks via `POST /api/v1/tasks` with a shared `shepherd.io/fleet` label
- Manage concurrency (submit N, wait for one to finish, submit next)
- Poll `GET /api/v1/tasks?fleet={fleet-name}` for aggregate status
- Display progress and final results

### API Support

- `GET /api/v1/tasks?fleet={fleet-name}` — filter tasks by fleet label
- No new CRD or controller needed
- Tasks are independent AgentTasks — the operator handles each one normally

### Future: FleetTask CRD

If the CLI-based approach proves insufficient (e.g., CLI dies mid-fleet, need server-side concurrency, need atomic cancellation), a FleetTask CRD can be introduced. It would own AgentTask resources via owner references and manage concurrency + aggregate status in a dedicated controller. This is documented as a future goal, not part of MVP.

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

Phase 2: Pull task data from API (authenticated via bearer token from assignment)
  - GET {apiURL}/api/v1/tasks/{taskID}/data --> description, context, repo info
  - GET {apiURL}/api/v1/tasks/{taskID}/token --> GitHub installation token
  - All requests include Authorization: Bearer {token} header

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

### Branch Naming Convention

The runner creates a branch for each task using a deterministic naming scheme that prevents collisions across concurrent tasks, retries, and fleet migrations:

```text
shepherd/{source-type}-{source-id}/{task-name}

Examples:
- shepherd/issue-123/task-abc123       (GitHub issue trigger)
- shepherd/pr-456/task-def456          (GitHub PR trigger)
- shepherd/fleet-update-deps/task-ghi789  (fleet migration)
```

The branch name is derived from `spec.task.sourceType`, `spec.task.sourceID`, and the AgentTask name. Since AgentTask names are unique within a namespace, branch collisions are impossible.

This scheme also groups related branches visually — all attempts for issue #123 appear under `shepherd/issue-123/`.

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
│   └── v1alpha1/              # Kubebuilder-managed CRD types (AgentTask)
├── internal/
│   └── controller/            # Kubebuilder-managed controllers
│       ├── agenttask_controller.go   # AgentTask reconciler (sandbox-based)
│       └── sandbox_builder.go        # SandboxClaim construction
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
| Job-based failure classification | Replaced | Sandbox Ready condition + runner callback model (no Pod reads) |
| `RunnerSpec.Image` field | Removed | Controlled by SandboxTemplate |
| `AgentTaskStatus.JobName` | Replaced | `AgentTaskStatus.SandboxClaimName` |

## Security Considerations

### Runner Authentication (Per-Task Bearer Token)

The runner-pull model requires the runner to authenticate when calling API endpoints (`/tasks/{id}/data`, `/tasks/{id}/token`, `/tasks/{id}/status`). Without authentication, any pod in the cluster could call these endpoints with a guessed task ID.

**Approach:** The operator generates a random bearer token per task, includes it in the task assignment POST to the runner, and stores the SHA-256 hash in a K8s Secret. The runner includes this token as an `Authorization: Bearer {token}` header on all API calls. The API validates the token by reading the Secret via an informer cache.

```text
Operator                          Runner                          API
   |                                |                              |
   |-- Create Secret {task}-token   |                              |
   |   { hash: SHA256(token) }      |                              |
   |                                |                              |
   |-- POST :8888/task ------------>|                              |
   |   { taskID, apiURL, token }    |                              |
   |                                |-- GET /tasks/{id}/data ----->|
   |                                |   Authorization: Bearer {t}  |
   |                                |<---- 200 task data ----------|
   |                                |                              |
   |                                |-- GET /tasks/{id}/token ---->|
   |                                |   Authorization: Bearer {t}  |
   |                                |<---- 200 GitHub token -------|
```

**Token lifecycle:**
- Generated by operator when assigning task (random 32-byte hex string)
- SHA-256 hash stored in K8s Secret `{task-name}-token` with owner reference to AgentTask
- Owner reference ensures cascade deletion when the AgentTask is deleted
- Sent to runner in plaintext in the task assignment POST (cluster-internal networking)
- Validated by API on every runner request (hash the presented token, compare to Secret)
- Implicitly expires when the task reaches terminal state (API rejects requests for terminal tasks)

**Why a Secret, not a CRD annotation:**
- Secrets can be encrypted at rest via etcd encryption (annotations cannot)
- Secret access is tracked separately in K8s audit logs
- RBAC for Secrets is independent of CRD read access — follows least privilege
- Owner reference provides automatic cleanup

**MVP scope:** This authentication mechanism is documented but deferred from MVP Part 1. For initial development, the API endpoints are unauthenticated (cluster-internal networking + NetworkPolicy provides the security boundary). The per-task token should be implemented before production use.

### General Security

- **Runner App private key**: Mounted once into API server pod, not into runner pods. Tokens generated on-demand per runner request. This expands the API server's privilege surface (it holds the GitHub App private key), but since the API already has `create` on AgentTask CRDs and `update` on status subresources, it's already a privileged component. The private key is mounted as a volume, not accessed via K8s RBAC. Single-namespace deployment keeps the blast radius contained.
- **Installation tokens**: Short-lived (1 hour), scoped to specific repos via GitHub App installation.
- **Pre-approved runner images**: Controlled by SandboxTemplate, not by API callers.
- **Internal communication**: Runner to API uses cluster-internal networking.
- **Signed callbacks**: API to adapter uses HMAC-SHA256 signature verification.
- **Webhook verification**: GitHub webhooks verified via `X-Hub-Signature-256` with constant-time comparison.
- **Pod security**: Non-root containers, seccomp RuntimeDefault profile.
- **Network isolation**: SandboxTemplate can specify NetworkPolicy (default-deny with explicit egress for DNS, Shepherd API, GitHub API, Anthropic API). This limits the blast radius even without per-task auth — runner pods can only reach the Shepherd API on port 8080.

## Namespace Strategy

- AgentTask is namespace-scoped (supports future multi-tenancy)
- Default namespace: `shepherd`
- SandboxTemplate and SandboxWarmPool deployed in same namespace
- Operator uses ClusterRole/ClusterRoleBinding to watch all namespaces

## RBAC Requirements

Each component requires specific K8s RBAC permissions. These are namespace-scoped unless noted.

### Operator

```yaml
rules:
  # AgentTask CRDs — full lifecycle management
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks/status"]
    verbs: ["update", "patch"]

  # SandboxClaim — create and delete for task execution
  - apiGroups: ["extensions.agents.x-k8s.io"]
    resources: ["sandboxclaims"]
    verbs: ["get", "list", "watch", "create", "delete"]

  # Sandbox — watch status for Ready condition transitions
  - apiGroups: ["agents.x-k8s.io"]
    resources: ["sandboxes"]
    verbs: ["get", "list", "watch"]

  # Secrets — create runner token secrets (owner ref for cleanup)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]

  # Events — emit K8s events for observability
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]

  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

**Notable exclusion**: The operator does NOT need `get`/`list`/`watch` on Pods. Failure detection uses Sandbox Ready condition transitions, not pod container status inspection.

### API Server

```yaml
rules:
  # AgentTask CRDs — create and watch
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks"]
    verbs: ["get", "list", "watch", "create"]
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks/status"]
    verbs: ["update", "patch"]

  # Secrets — read runner token for validation
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch"]
```

### GitHub Adapter

No K8s RBAC needed — communicates exclusively via the REST API.

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
2. **`internal/controller/agenttask_controller.go`** — the `assignTask()`, `classifySandboxTermination()`, and timeout management functions. These would move behind an `ExecutionBackend` interface.

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

## Known Limitations (MVP)

1. **No retry mechanism**: MVP Part 1 has no automatic retry for failed tasks. If a task fails (runner crash, network error, OOM), the caller must resubmit manually. The adapter can re-trigger from a new GitHub comment, and the CLI can resubmit fleet tasks. Automatic retries (with backoff and retryable reason filtering) are a post-MVP feature that would require new CRD fields (`retryPolicy`, `attempts` counter).

2. **Dangling branches on runner crash**: If a runner crashes after pushing a branch but before reporting completion, an orphaned branch remains in the repository. The adapter posts a failure comment (via the CRD watcher fallback, step 12), but branch cleanup is manual. This is especially relevant for fleet migrations where multiple tasks may crash, leaving partial branches across repos. Post-MVP: a cleanup job that lists `shepherd/*` branches, checks corresponding AgentTask terminal state, and deletes stale branches.

3. **No granular failure classification**: The operator classifies all non-success sandbox terminations as `Failed` without distinguishing OOM, crash, or error. This is a deliberate trade-off to avoid Pod read RBAC. If agent-sandbox adds termination info to Sandbox status in a future release, granular classification can be added without RBAC changes. An upstream issue should be filed for this.

## Open Questions

1. **Agent conversation log capture**: Claude Code produces conversation logs that could be valuable for debugging and auditing. Pod logs are accessible while the pod object exists, but are lost when the SandboxClaim is deleted. The operator should capture or persist these before cleanup. Options include object storage (S3/GCS) or a PVC, but conversation logs may contain sensitive internal information that makes a general logging solution (Loki, etc.) inappropriate. Left open for now — needs design when approaching production readiness.

2. **agent-sandbox termination info upstream**: The Sandbox CRD status does not expose pod exit codes or termination reasons (OOMKilled, etc.). Filing an upstream issue to add a `TerminationInfo` field to Sandbox status would allow Shepherd to provide granular failure reasons without Pod read RBAC. Until then, all failures are classified as generic `Failed`.

## Resolved Decisions

These were previously open questions, now settled:

- **agent-sandbox API version alignment**: Shepherd imports only the API types (schema) from agent-sandbox, not the full controller. No version conflict — API types are plain Go structs with no controller-runtime dependency.
- **Warm pool sizing**: User-configurable via SandboxWarmPool CRD. Start with `minReady: 1`. Each deployment adjusts based on their usage patterns.
- **Fleet repo discovery**: Not a Shepherd feature. The user/CLI provides the explicit list of repos. Repo discovery is organization-specific and out of scope.
- **Warm pool idle timeout**: Not Shepherd's concern. The agent-sandbox warm pool controller manages pod lifecycle. The runner entrypoint simply waits for a task assignment until the pod is terminated by the pool controller.
- **Migration from Jobs-based architecture**: Clean break, no backwards compatibility. The `v1alpha1` API is pre-GA with no stability guarantees. All Jobs-based code (`cmd/shepherd-init/`, `job_builder.go`, etc.) is retired without migration tooling. Existing AgentTask resources should be deleted before deploying the new version.
- **SandboxTemplate per language**: Per-language templates. The project provides one Go-based template as an example. Each organization creates their own templates for their language/build tool combinations (e.g., Java+Gradle, Java+Maven, Python+Poetry). Documentation should cover how to create a SandboxTemplate and what the runner container image needs to contain.

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
- `thoughts/plans/2026-01-28-phase5-failure-handling-podfailurepolicy.md` — Pod failure policy (replaced by Sandbox condition-based detection, no Pod reads)
