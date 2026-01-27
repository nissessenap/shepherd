# Shepherd Design Document

**Date:** 2026-01-27
**Status:** Draft
**Supersedes:** [2026-01-25 design](2026-01-25-shepherd-design.md)

## Overview

Shepherd is an open-source background coding agent orchestrator. It receives tasks from various triggers (GitHub, Slack, CLI), runs AI coding agents in isolated Kubernetes jobs, and reports results back.

Inspired by:

- [Spotify's Background Coding Agent](https://engineering.atspotify.com/2025/11/spotifys-background-coding-agent-part-1)
- [Ramp's Background Agent](https://builders.ramp.com/post/why-we-built-our-background-agent)

## Goals

### MVP (Priority Order)

1. **Issue-driven development** - Trigger agent from GitHub issue/PR to work on a single repo
2. **Scheduled SRE tasks** - Daily/weekly automated analysis and fixes

### Future

1. **Fleet migrations** - Running the same transformation across many repos
2. **Interactive sessions** - Long-running dev environments with remote access

## Architecture

### Design Principles

- **K8s-native**: Uses CRDs for state, Jobs for execution. No external database for MVP.
- **Provider-agnostic core**: API and operator know nothing about GitHub. Adapters handle translation.
- **Callback-based status**: Triggers include a callback URL. API POSTs status updates there.
- **Single binary, multiple targets**: Loki/Mimir pattern - one binary, scale components independently.

### Tech Stack

| Category | Choice |
| -------- | ------ |
| Language | Go 1.25+ |
| CRD/Operator | kubebuilder 4.11.0, controller-runtime |
| CLI | `github.com/alecthomas/kong` |
| HTTP Router | `github.com/go-chi/chi/v5` |
| Logging | zap via `sigs.k8s.io/controller-runtime/pkg/log/zap` |
| GitHub | `github.com/google/go-github`, `github.com/bradleyfalzon/ghinstallation/v2` |
| Unit Testing | `github.com/stretchr/testify` |
| Integration Testing | `github.com/onsi/gomega` for operator (envtest, `Eventually()`) |
| Metrics | `github.com/prometheus/client_golang` (via controller-runtime) |
| Container Builds | ko |
| Deployment | Helm (kubebuilder Kustomize for CRD generation only) |

### Components

Single binary with Kong subcommands (`shepherd api`, `shepherd operator`, `shepherd github`, `shepherd all`):

| Target | Role | Scaling |
| ------ | ---- | ------- |
| `api` | REST API, CRD creation, runner callbacks, watches CRD status, notifies adapters | Multiple replicas behind LB |
| `operator` | Watches CRDs, manages Jobs, updates CRD status. K8s-only, no external calls. | 1 active (leader election) |
| `github` | GitHub App webhooks, posts comments | Multiple replicas |
| `all` | All in one process | Dev/testing only |

### CLI Structure

```go
type CLI struct {
    API      APICmd      `cmd:"" help:"Run API server"`
    Operator OperatorCmd `cmd:"" help:"Run K8s operator"`
    GitHub   GitHubCmd   `cmd:"" help:"Run GitHub adapter"`
    All      AllCmd      `cmd:"" help:"Run all components"`

    LogLevel int  `help:"Log level (0=info, 1=debug)" default:"0"`
    DevMode  bool `help:"Enable development mode logging" default:"false"`
}

type APICmd struct {
    ListenAddr string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
}

type OperatorCmd struct {
    MetricsAddr    string `help:"Metrics address" default:":9090" env:"SHEPHERD_METRICS_ADDR"`
    HealthAddr     string `help:"Health probe address" default:":8081" env:"SHEPHERD_HEALTH_ADDR"`
    LeaderElection bool   `help:"Enable leader election" default:"false" env:"SHEPHERD_LEADER_ELECTION"`
}

type GitHubCmd struct {
    ListenAddr    string `help:"GitHub adapter listen address" default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
    WebhookSecret string `help:"GitHub webhook secret" env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
    AppID         int64  `help:"GitHub App ID" env:"SHEPHERD_GITHUB_APP_ID"`
    PrivateKey    string `help:"Path to GitHub App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY"`
}
```

Zap is configured programmatically from Kong flags (not via `flag.CommandLine`):

```go
logger := zap.New(
    zap.UseDevMode(cli.DevMode),
    zap.Level(zapcore.Level(-cli.LogLevel)),
)
log.SetLogger(logger)
```

### GitHub Apps

Two separate GitHub Apps with distinct responsibilities:

| App | Purpose | Permissions | Used By |
| --- | ------- | ----------- | ------- |
| Shepherd Trigger | Webhooks, read issues/PRs, write comments | Read issues, write comments | github-adapter |
| Shepherd Runner | Clone repos, push branches, create PRs | Read/write code, create PRs | K8s jobs (via init container) |

### Component Responsibilities

| Component | Does | Does NOT |
| --------- | ---- | -------- |
| **Operator** | Watch CRDs, create/monitor Jobs, update CRD status | Make external HTTP calls |
| **API** | Serve REST endpoints, create CRDs, receive runner callbacks, watch CRD status changes, notify adapters via callback URLs | Manage Jobs |
| **Adapter** | Handle GitHub webhooks, post GitHub comments, query API for deduplication | Read CRDs, talk to K8s |

The operator is purely K8s-internal. All external communication (adapter callbacks) flows through the API, which watches CRD status changes via an informer.

### Data Flow

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
   (no token - adapter doesn't have Runner credentials)
         |
         v
5. API validates request, gzip-compresses context, creates AgentTask CRD
         |
         v
6. Operator sees new CRD, creates K8s Job with:
   - Init container: generates GitHub token from Runner App key
   - Main container: runner image with Claude Code
   - Env vars: repo URL, task, API callback URL
         |
         v
7. Job starts:
   - Init container generates token, writes to shared volume
   - Main container clones repo, Claude Code works on task
   - Hooks POST progress updates to API
         |
         v
8. API receives runner callback, updates CRD status,
   reads callback URL from CRD spec, POSTs to adapter
   Adapter posts comment: "Working on it..."
         |
         v
9. Claude Code creates PR, job completes
         |
         v
10. Operator sees job done, updates CRD to Succeeded=True
         |
         v
11. API's CRD status watcher detects terminal state,
    reads callback URL from CRD spec, POSTs to adapter
    Adapter posts final comment with PR link
```

Step 11 ensures the adapter is notified even if the runner crashes without calling its completion hook. The API watches AgentTask status for terminal conditions (`Succeeded=True` or `Succeeded=False`) and fires the adapter callback.

## CRD Specification

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
    ref: "main"                          # optional: branch, tag, or SHA
  task:
    description: "Fix the null pointer exception in login.go"
    context: "<gzip+base64 encoded>"     # API compresses transparently
    contextEncoding: "gzip"              # "gzip" or empty for plaintext
    contextUrl: "https://github.com/org/repo/issues/123"
  callback:
    url: "https://github-adapter.example.com/callback"
    secretRef:
      name: webhook-secret
      key: token
  runner:
    image: "shepherd-runner:latest"
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
  startTime: "2026-01-27T10:00:00Z"
  completionTime: "2026-01-27T10:15:00Z"
  conditions:
    - type: Succeeded
      status: "True"
      reason: Succeeded
      message: "Pull request created"
      lastTransitionTime: "2026-01-27T10:15:00Z"
      observedGeneration: 1
  result:
    prUrl: "https://github.com/org/repo/pull/42"
    error: ""
  jobName: task-abc123-job
```

### CRD Type Definitions

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=`.status.result.prUrl`,priority=1
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentTask struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   AgentTaskSpec   `json:"spec,omitempty"`
    Status AgentTaskStatus `json:"status,omitempty"`
}

type AgentTaskSpec struct {
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
    Repo     RepoSpec     `json:"repo"`
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
    Task     TaskSpec     `json:"task"`
    Callback CallbackSpec `json:"callback"`
    Runner   RunnerSpec   `json:"runner,omitempty"`
}

type RepoSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^https://`
    URL string `json:"url"`
    Ref string `json:"ref,omitempty"`
}

type TaskSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Description     string `json:"description"`
    Context         string `json:"context,omitempty"`
    ContextEncoding string `json:"contextEncoding,omitempty"`
    ContextURL      string `json:"contextUrl,omitempty"`
}

type CallbackSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^https?://`
    URL       string                    `json:"url"`
    SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`
}

type RunnerSpec struct {
    // +kubebuilder:default="shepherd-runner:latest"
    Image              string                       `json:"image,omitempty"`
    // +kubebuilder:default="30m"
    Timeout            metav1.Duration              `json:"timeout,omitempty"`
    ServiceAccountName string                       `json:"serviceAccountName,omitempty"`
    Resources          corev1.ResourceRequirements  `json:"resources,omitempty"`
}

type AgentTaskStatus struct {
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
    StartTime          *metav1.Time       `json:"startTime,omitempty"`
    CompletionTime     *metav1.Time       `json:"completionTime,omitempty"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    JobName            string             `json:"jobName,omitempty"`
    Result             TaskResult         `json:"result,omitempty"`
}

type TaskResult struct {
    PRUrl string `json:"prUrl,omitempty"`
    Error string `json:"error,omitempty"`
}
```

### Condition State Machine

The primary condition is `Succeeded` (run-to-completion pattern, like Tekton TaskRun):

| Status | Reason | Meaning |
| ------ | ------ | ------- |
| Unknown | Pending | Waiting for job to start |
| Unknown | Running | Job is executing |
| True | Succeeded | Task completed, PR created |
| False | Failed | Task failed |
| False | TimedOut | Timeout exceeded |
| False | Cancelled | User cancelled |

Progress events use the K8s Events API (`r.Recorder.Event(...)`) rather than embedded status fields.

### Job Failure and Pod Termination

The operator watches Jobs via `Owns(&batchv1.Job{})`. This covers cases where the runner cannot report its own status:

- **Node loss / eviction**: The node is deleted or the pod is evicted. The pod object may be gone entirely. The operator detects the Job failure and looks up the pod: if the pod no longer exists (or was evicted), this is an infrastructure failure. The operator creates a new Job. The CRD stays at `Succeeded=Unknown` with reason `Running`.
- **Application failure**: The pod exists with a non-zero exit code. The operator updates the CRD to `Succeeded=False`. The API's CRD status watcher notifies the adapter. Users can retrigger via a new `@shepherd` comment.
- **OOM kill**: Pod exists with OOMKilled reason. Treated as application failure (`Succeeded=False`).
- **Timeout**: The Job's `activeDeadlineSeconds` (derived from `spec.runner.timeout`) causes K8s to terminate the Job. Operator sets `Succeeded=False` with reason `TimedOut`.

The Job is always created with `backoffLimit: 0` (K8s does not retry on its own). Retry logic lives in the operator, which distinguishes infrastructure failures (pod missing / evicted) from application failures (pod exists with exit code) and only retries the former.

### Spec Immutability

Enforced via CEL validation rules (`self == oldSelf`) on `repo` and `task` fields. No validating webhook needed.

### Context Compression

The API gzip-compresses the context field before creating the CRD, transparent to consumers. A 1.5 MiB etcd limit with gzip comfortably holds ~5-10 MiB of uncompressed text. Inspired by the [Grafana Operator's gzip pattern](https://grafana.github.io/grafana-operator/docs/examples/dashboard/#gzipjson).

### Task Deduplication

One active task per repo+issue. The adapter queries the API before creating a new task:

- If active (`Succeeded=Unknown`): post "already running" comment
- If completed (`Succeeded=True`): allow new task (user explicitly retriggered)
- If failed (`Succeeded=False`) and failure feedback posted: allow new task

Labels `shepherd.io/repo` and `shepherd.io/issue` enable lookup via `GET /api/v1/tasks?repo=...&issue=...&active=true`.

## Job Specification

### Init Container

Generates GitHub installation token without exposing private key to main container:

```yaml
initContainers:
  - name: github-auth
    image: shepherd-init:latest
    env:
      - name: REPO_URL
        value: "https://github.com/org/repo.git"
    volumeMounts:
      - name: github-creds
        mountPath: /creds
      - name: runner-app-key
        mountPath: /secrets/runner-app-key
        readOnly: true
```

### Main Container

```yaml
containers:
  - name: runner
    image: shepherd-runner:latest
    env:
      - name: SHEPHERD_CALLBACK_URL
        value: "http://shepherd-api.shepherd.svc.cluster.local/api/v1/status"
      - name: SHEPHERD_TASK_ID
        value: "task-abc123"
      - name: SHEPHERD_REPO_URL
        value: "https://github.com/org/repo.git"
      - name: SHEPHERD_TASK_DESCRIPTION
        valueFrom:
          configMapKeyRef: ...
    volumeMounts:
      - name: github-creds
        mountPath: /creds
        readOnly: true
```

### Runner Image Contents

- Claude Code CLI (pre-installed)
- Git, gh CLI
- Go runtime (single runtime for MVP)
- Shepherd hooks (shell scripts that POST to API)
- Base CLAUDE.md with conventions

### Claude Code Hooks

```
~/.claude/hooks/
├── on_start.sh      # POST /status {event: "started"}
├── on_tool_call.sh  # Optional: log tool usage
└── on_complete.sh   # POST /status {event: "completed", pr_url: "..."}
```

## Callback Contract

### Runner to API (progress updates)

The runner POSTs progress updates to the API via internal K8s networking. The API updates the CRD status and forwards to the adapter callback URL.

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

### API CRD Status Watcher (terminal state)

The API runs an informer on AgentTask resources. When the operator updates a CRD to a terminal condition (`Succeeded=True` or `Succeeded=False`), the API reads the callback URL from `spec.callback.url` and notifies the adapter. This handles the case where the runner crashes without calling its completion hook.

### API to Adapter

Both runner callbacks and CRD status watcher trigger adapter notifications. External callback with HMAC-SHA256 signature verification:

```json
POST {callback_url}
Content-Type: application/json
X-Shepherd-Signature: sha256=...

{
  "task_id": "task-abc123",
  "event": "started" | "progress" | "completed" | "failed",
  "message": "Working on your request...",
  "details": { ... },
  "context": { ... }
}
```

The API should deduplicate notifications to avoid sending duplicate "completed" or "failed" callbacks (e.g., if the runner reports completion AND the operator updates the CRD).

## Repository Structure

```
shepherd/
├── cmd/
│   └── shepherd/              # Kong CLI entrypoint
├── api/
│   └── v1alpha1/              # Kubebuilder-managed CRD types
├── internal/
│   └── controller/            # Kubebuilder-managed controllers
├── config/                    # Kubebuilder-managed manifests (Kustomize)
├── pkg/
│   ├── shepherd/              # Module orchestrator, errgroup lifecycle
│   ├── api/                   # REST API server (chi router)
│   ├── operator/              # Operator module wrapper
│   └── adapters/
│       └── github/            # GitHub webhook handler, App client
├── deploy/
│   └── helm/                  # Helm charts for deployment
├── images/
│   ├── runner/                # Runner Dockerfile
│   └── init/                  # Init container Dockerfile
├── PROJECT                    # Kubebuilder project file
├── Makefile                   # Extended kubebuilder Makefile + ko targets
├── .ko.yaml                   # ko build configuration
└── examples/
```

Kubebuilder manages `api/`, `internal/controller/`, and `config/`. Everything else is custom.

## Namespace Strategy

- AgentTask is namespace-scoped (supports future multi-tenancy)
- Default namespace: `shepherd`
- Operator uses ClusterRole/ClusterRoleBinding (kubebuilder default) to watch all namespaces

## Security Considerations

- **Runner App private key**: Only accessible to init container, never main container
- **Installation tokens**: Short-lived (1 hour), scoped to specific repos
- **Pre-approved runner images**: Users cannot specify arbitrary images; validated in controller
- **Internal callbacks**: Job to API uses cluster-internal networking
- **Signed callbacks**: API to adapter uses HMAC-SHA256 signature verification
- **Webhook verification**: GitHub webhooks verified via `X-Hub-Signature-256` with constant-time comparison
- **Prompt injection**: Runner environments must limit LLM access to prevent oversharing or malicious actions

## Dependencies (go.mod)

```go
require (
    // Core K8s
    sigs.k8s.io/controller-runtime
    k8s.io/apimachinery
    k8s.io/client-go

    // CLI
    github.com/alecthomas/kong

    // HTTP
    github.com/go-chi/chi/v5

    // GitHub
    github.com/google/go-github/v81
    github.com/bradleyfalzon/ghinstallation/v2

    // Concurrency
    golang.org/x/sync

    // Testing
    github.com/stretchr/testify
    github.com/onsi/gomega
)
```

## Future Considerations

- **Multiple runtime images**: Language-specific pre-approved images
- **GitLab/other adapters**: Provider-agnostic core enables this
- **Scheduled tasks**: Cron-based triggers for SRE automation
- **Fleet migrations**: Batch orchestration across multiple repos
- **Interactive sessions**: Long-running environments with VS Code remote access
- **OpenCode/SDK integration**: More control over agent execution and metrics

## Kubebuilder Scaffolding

```bash
kubebuilder init --domain shepherd.io
kubebuilder create api --group toolkit --version v1alpha1 --kind AgentTask
```

This produces `toolkit.shepherd.io/v1alpha1` as the API group (following Flux's `toolkit.fluxcd.io` precedent).

## Open Questions

1. **Runner image distribution** - ko builds the shepherd binary. The runner image (with Claude Code) needs a separate Dockerfile. Out of scope for MVP but needs planning.
2. **go-github version** - Pin to latest at implementation time (calendar versioning).
