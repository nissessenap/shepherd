---
date: 2026-02-05T12:00:00+01:00
researcher: Claude
git_commit: bc862d3d4e340c90762afc9cbd7556d9d3fc1d53
branch: main
repository: shepherd
topic: "GitHub Adapter Implementation Research"
tags: [research, codebase, github, adapter, webhooks, api]
status: complete
last_updated: 2026-02-05
last_updated_by: Claude
---

# Research: GitHub Adapter Implementation

**Date**: 2026-02-05T12:00:00+01:00
**Researcher**: Claude
**Git Commit**: bc862d3d4e340c90762afc9cbd7556d9d3fc1d53
**Branch**: main
**Repository**: shepherd

## Research Question

Document the current state of the Shepherd codebase to support implementing the GitHub adapter (`shepherd github`) component. The API implementation has evolved since the original plan, so a fresh analysis is needed before creating the implementation plan.

## Summary

The Shepherd codebase has a fully implemented API server with dual-port architecture, a working operator using agent-sandbox, and a stub for the GitHub adapter command. The API already contains GitHub token generation for runners but lacks the webhook-receiving adapter component. Key findings:

1. **API Server is Complete**: Dual-port architecture (8080 public, 8081 internal), CRD watcher, callback sender with HMAC signing
2. **GitHub Token Generation Exists**: JWT signing and installation token exchange implemented in `pkg/api/github_token.go`
3. **GitHub Adapter is a Stub**: `shepherd github` command exists but returns "not implemented yet"
4. **No go-github Library**: Custom HTTP implementation for GitHub API calls; go-github/ghinstallation not in dependencies
5. **Callback Contract Defined**: HMAC-signed POST to adapter callback URLs with `CallbackPayload` structure
6. **CRD Has Source Fields**: `SourceType` (issue/pr/fleet), `SourceURL`, `SourceID` for tracking trigger origin

## Detailed Findings

### 1. Current CLI Structure

**Location**: `cmd/shepherd/`

The CLI uses Kong with three subcommands:

| File | Command | Status |
|------|---------|--------|
| `main.go` | Root CLI + `github` stub | Stub returns error |
| `api.go` | `shepherd api` | Fully implemented |
| `operator.go` | `shepherd operator` | Fully implemented |

**GitHubCmd Definition** (`main.go:38-47`):
```go
type GitHubCmd struct {
    ListenAddr    string `default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
    WebhookSecret string `env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
    AppID         int64  `env:"SHEPHERD_GITHUB_APP_ID"`
    PrivateKey    string `env:"SHEPHERD_GITHUB_PRIVATE_KEY"`
}
```

The Run method at line 45-47 returns `fmt.Errorf("github adapter not implemented yet")`.

### 2. API Server Architecture

**Location**: `pkg/api/`

**Files** (19 total):
- `server.go` - Main server with dual-port architecture
- `handler_tasks.go` - Task CRUD endpoints
- `handler_status.go` - Runner status updates
- `handler_token.go` - GitHub token endpoint for runners
- `handler_data.go` - Task data endpoint
- `callback.go` - HMAC-signed callback sender
- `watcher.go` - CRD status watcher for terminal notifications
- `github_token.go` - JWT creation and token exchange
- `compress.go` / `decompress.go` - Context compression
- `types.go` - API type definitions

**Dual-Port Design**:
- **Port 8080 (Public)**: `POST/GET /api/v1/tasks`, health endpoints
- **Port 8081 (Internal)**: `PUT /tasks/{id}/status`, `GET /tasks/{id}/token`, `GET /tasks/{id}/data`

**Options Struct** (`server.go:50-60`):
```go
type Options struct {
    ListenAddr           string  // ":8080"
    InternalListenAddr   string  // ":8081"
    CallbackSecret       string  // HMAC signing
    Namespace            string  // K8s namespace
    GithubAppID          int64   // Runner App ID
    GithubInstallationID int64   // Installation ID
    GithubAPIURL         string  // API base URL
    GithubPrivateKeyPath string  // RSA key path
}
```

### 3. Callback Contract (API to Adapter)

**CallbackPayload** (`types.go:87-92`):
```go
type CallbackPayload struct {
    TaskID  string         `json:"taskID"`
    Event   string         `json:"event"` // started, progress, completed, failed
    Message string         `json:"message"`
    Details map[string]any `json:"details,omitempty"`
}
```

**Event Constants** (`types.go:21-24`):
- `EventStarted = "started"`
- `EventProgress = "progress"`
- `EventCompleted = "completed"`
- `EventFailed = "failed"`

**HMAC Signing** (`callback.go:63-69`):
- Header: `X-Shepherd-Signature: sha256=<hex-encoded-hmac>`
- Algorithm: HMAC-SHA256
- Body: JSON-marshaled CallbackPayload
- Empty secret disables signing

**Callback Flow**:
1. Runner sends status update to API (internal port)
2. API updates CRD status
3. API POSTs to `task.Spec.Callback.URL` with HMAC signature
4. Watcher provides backup notification for terminal states

### 4. CRD Types (AgentTask)

**Location**: `api/v1alpha1/`

**AgentTaskSpec Fields**:
- `Repo.URL` - Repository URL (required, must start with `https://`)
- `Repo.Ref` - Git ref (optional)
- `Task.Description` - Task description (required)
- `Task.Context` - Compressed context (optional)
- `Task.SourceURL` - Origin URL (e.g., GitHub issue URL)
- `Task.SourceType` - Trigger type: `"issue"`, `"pr"`, or `"fleet"`
- `Task.SourceID` - Trigger ID (e.g., issue number)
- `Callback.URL` - Adapter callback URL (required)
- `Runner.SandboxTemplateName` - Sandbox template (required)
- `Runner.Timeout` - Default 30m

**CallbackSpec** (`agenttask_types.go:83-87`):
```go
type CallbackSpec struct {
    URL string `json:"url"` // Pattern: ^https?://
}
```
Note: **No SecretRef field** - the original design doc's `SecretRef` was removed.

**Condition Constants** (`conditions.go`):
- `ConditionSucceeded` with reasons: Pending, Running, Succeeded, Failed, TimedOut, Cancelled
- `ConditionNotified` with reasons: CallbackPending, CallbackSent, CallbackFailed

**TokenIssued Flag** (`agenttask_types.go:127`):
- Boolean in status for replay protection
- Set before token generation to prevent double-issuance

### 5. GitHub Token Generation (Existing)

**Location**: `pkg/api/github_token.go`, `pkg/api/handler_token.go`

This is for **runners** to get tokens, not for the adapter.

**Implementation**:
1. `readPrivateKey()` - Loads RSA key (PKCS1 or PKCS8)
2. `createJWT()` - Signs GitHub App JWT (RS256, 10min expiry)
3. `exchangeToken()` - POSTs to `/app/installations/{id}/access_tokens`
4. `getTaskToken()` handler - One-time token fetch with replay protection

**Key Pattern**: Token issued flag set BEFORE calling GitHub API (security-first).

### 6. Dependencies (go.mod)

**Go Version**: 1.25.3

**Relevant Direct Dependencies**:
- `github.com/go-chi/chi/v5 v5.2.4` - HTTP router
- `github.com/golang-jwt/jwt/v5 v5.3.1` - JWT library
- `github.com/alecthomas/kong v1.13.0` - CLI parser
- `sigs.k8s.io/controller-runtime v0.23.0` - K8s controller
- `sigs.k8s.io/agent-sandbox v0.1.0` - Sandbox library

**NOT Present**:
- `github.com/google/go-github` - GitHub API client
- `github.com/bradleyfalzon/ghinstallation` - GitHub App auth

The codebase uses custom HTTP implementation for GitHub API calls.

### 7. Design Document Expectations

From `thoughts/research/2026-01-27-shepherd-design.md`:

**Two GitHub Apps**:
| App | Purpose | Used By |
|-----|---------|---------|
| Shepherd Trigger | Webhooks, read issues/PRs, write comments | github-adapter |
| Shepherd Runner | Clone repos, push branches, create PRs | K8s jobs |

**Adapter Responsibilities**:
- Handle GitHub webhooks
- Post GitHub comments
- Query API for deduplication (check active tasks)
- Does NOT talk to K8s directly

**Data Flow** (design doc steps 2-4):
1. GitHub webhook → github-adapter
2. Adapter checks API for active tasks
3. Adapter POSTs to API with callback URL
4. API creates CRD, operator handles execution
5. API notifies adapter via callback
6. Adapter posts GitHub comment

### 8. Repository Structure

**Existing**:
```
shepherd/
├── cmd/shepherd/           # CLI (main.go, api.go, operator.go)
├── api/v1alpha1/          # CRD types
├── internal/controller/   # Operator controller
├── pkg/
│   ├── api/              # API server (19 files)
│   └── operator/         # Operator wrapper
├── config/               # Kustomize manifests
└── thoughts/             # Research and plans
```

**Planned but Missing**:
```
├── pkg/
│   └── adapters/
│       └── github/       # GitHub adapter (not created)
```

## Code References

### CLI Structure
- `cmd/shepherd/main.go:29-36` - CLI struct with subcommands
- `cmd/shepherd/main.go:38-47` - GitHubCmd struct and stub
- `cmd/shepherd/api.go:25-34` - APICmd struct

### API Server
- `pkg/api/server.go:50-60` - Options struct
- `pkg/api/server.go:80-261` - Run() function
- `pkg/api/server.go:181-206` - Route wiring (dual-port)

### Callback System
- `pkg/api/callback.go:33-36` - callbackSender struct
- `pkg/api/callback.go:51-86` - send() method with HMAC
- `pkg/api/types.go:87-92` - CallbackPayload struct

### CRD Types
- `api/v1alpha1/agenttask_types.go:42-50` - AgentTaskSpec
- `api/v1alpha1/agenttask_types.go:59-81` - TaskSpec with Source fields
- `api/v1alpha1/agenttask_types.go:83-87` - CallbackSpec (no SecretRef)
- `api/v1alpha1/conditions.go:19-38` - Condition constants

### GitHub Token (for runners)
- `pkg/api/github_token.go:68-78` - createJWT()
- `pkg/api/github_token.go:99-157` - exchangeToken()
- `pkg/api/handler_token.go:33-123` - getTaskToken() handler

## Architecture Documentation

### Existing Patterns to Follow

1. **Kong CLI Pattern**: Subcommand struct with Run(*CLI) error method
2. **Package Structure**: `pkg/{component}/` with `Run(Options)` function
3. **Chi Router**: Middleware stack (RequestID, RealIP, Recoverer)
4. **Controller-Runtime Logger**: `ctrl.Log.WithName("component")`
5. **Signal Handling**: `signal.NotifyContext` with SIGINT/SIGTERM
6. **Health Endpoints**: `/healthz` (liveness) and `/readyz` (readiness)

### API Contract for Adapter

**Task Creation** (`POST /api/v1/tasks`):
```json
{
  "repo": {"url": "https://github.com/org/repo", "ref": "main"},
  "task": {
    "description": "Fix the bug",
    "context": "Issue body and comments...",
    "sourceUrl": "https://github.com/org/repo/issues/123",
    "sourceType": "issue",
    "sourceId": "123"
  },
  "callbackUrl": "http://github-adapter:8082/callback",
  "labels": {"shepherd.io/repo": "org-repo", "shepherd.io/issue": "123"},
  "runner": {"sandboxTemplateName": "default"}
}
```

**Task Query** (`GET /api/v1/tasks?repo=org-repo&issue=123&active=true`):
- Returns active tasks for deduplication check

**Callback Reception** (adapter must implement):
```json
POST /callback
X-Shepherd-Signature: sha256=...
{
  "taskID": "task-abc123",
  "event": "completed",
  "message": "Pull request created",
  "details": {"pr_url": "https://github.com/org/repo/pull/42"}
}
```

## Historical Context (from thoughts/)

- `thoughts/research/2026-01-27-shepherd-design.md` - Original design with GitHub adapter spec
- `thoughts/plans/2026-01-28-api-server-implementation.md` - API implementation plan (outdated)
- `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md` - Current architecture
- `thoughts/research/2026-02-03-token-auth-alternatives.md` - Token authentication research

## Open Questions for Implementation Plan

1. **go-github vs Custom HTTP**: Should the adapter use go-github library or continue with custom HTTP? The API uses custom HTTP for simplicity.

2. **Webhook Verification**: GitHub sends `X-Hub-Signature-256` header. Need constant-time comparison.

3. **Comment Templates**: What should the adapter post for different events? (started, progress, completed, failed)

4. **Installation ID Resolution**: The Trigger App needs to get installation ID for each webhook. Options:
   - Store mapping in ConfigMap
   - Use GitHub API to look up installation by repo

5. **Deduplication Logic**: When should adapter allow new task vs post "already running"?
   - Active (`Succeeded=Unknown`): Block
   - Completed (`Succeeded=True`): Allow
   - Failed (`Succeeded=False`): Allow

6. **Callback Endpoint Path**: Design doc shows `/callback`, but need to define full contract.

7. **Error Handling**: What to post when API returns error? When callback verification fails?
