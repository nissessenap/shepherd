---
date: 2026-03-01T15:13:33+01:00
researcher: claude
git_commit: f10345adf50ddc57bb3d277854658f234aeb207d
branch: app_sharing
repository: shepherd
topic: "Documentation Site Planning — Pages, Structure, and Content for Hugo-based GitHub Pages"
tags: [research, documentation, hugo, github-pages, github-apps, runner-api, architecture]
status: complete
last_updated: 2026-03-01
last_updated_by: claude
---

# Research: Documentation Site Planning

**Date**: 2026-03-01T15:13:33+01:00
**Researcher**: claude
**Git Commit**: f10345adf50ddc57bb3d277854658f234aeb207d
**Branch**: app_sharing
**Repository**: shepherd

## Research Question

What documentation pages are needed for a Hugo-based GitHub Pages site for Shepherd? The user wants: quickstart, architectural overview, configuration guides, a guide on building custom runners (Python/Node.js examples), and GitHub App setup using manifest flow. What other pages might be needed?

## Summary

Based on thorough codebase analysis, the documentation site should have **9 core pages** organized into 4 sections. The codebase has rich material for each: the architecture spans 4 components with clear interaction flows, the API has a complete OpenAPI spec suitable for runner development guides, the two GitHub Apps have distinct permission sets ideal for manifest-based setup guides, and the deployment uses Kustomize with well-documented configuration options.

## Recommended Documentation Pages

### Section 1: Getting Started

#### Page 1: Introduction / Home (`_index.md`)
- What Shepherd is: background coding agent orchestrator on Kubernetes
- The problem it solves: automated code changes triggered from GitHub issues
- High-level flow: `@shepherd` comment → task → sandbox → PR
- Key features: two-app GitHub architecture, sandbox isolation, real-time streaming, web dashboard
- Links to quickstart and architecture pages

#### Page 2: Quickstart (`quickstart.md`)
Content available from codebase:
- **Prerequisites**: Kind, kubectl, ko, Docker, Go 1.25+, Node.js 22+
- **Local development cluster setup**: `make kind-create` uses `test/e2e/kind-config.yaml` (single node, ports 30080/30081)
- **Build and load images**: `make ko-build-kind` builds 3 images (shepherd, shepherd-runner, shepherd-web) and loads into Kind
- **Install dependencies**: `make install-agent-sandbox` (agent-sandbox operator v0.1.1), `make install` (AgentTask CRD)
- **Deploy**: `make deploy-test` (test overlay with NodePort services, no GitHub App required)
- **Verify**: `kubectl get pods -n shepherd-system`, access web UI at `localhost:30081`, API at `localhost:30080`
- **Create a test task**: curl example against `POST /api/v1/tasks` with minimal payload
- **Watch it run**: web UI shows real-time streaming events
- **Frontend dev**: `make web-dev` proxies `/api` to `localhost:8080`
- Note: the test overlay removes GitHub App requirements, so token endpoint returns 503 — fine for exploring the system

### Section 2: Architecture & Concepts

#### Page 3: Architecture Overview (`architecture.md`)
Content available from codebase:
- **Component diagram**: 4 deployments (operator, API server, GitHub adapter, web frontend) + ephemeral runner sandboxes
- **Two-port API architecture**: public `:8080` (adapters, UI) and internal `:8081` (runners only, NetworkPolicy-protected)
- **Full task lifecycle flow** (12 steps documented in detail from codebase analysis):
  1. GitHub webhook → adapter
  2. `@shepherd` mention detection (regex: `(?i)(?:^|\s)@shepherd\b`)
  3. Deduplication check via `GET /api/v1/tasks?active=true`
  4. Context assembly (all issue comments, 1MB cap)
  5. Task creation via `POST /api/v1/tasks` → AgentTask CRD
  6. Operator reconciles → SandboxClaim creation
  7. Sandbox becomes ready → task assignment via `POST :8888/task`
  8. Runner executes (clone, branch, Claude Code, PR)
  9. Status update → `POST /api/v1/tasks/{id}/status`
  10. Callback → adapter → GitHub comment
- **CRD model**: AgentTask spec/status fields, conditions (Succeeded, Notified), terminal states
- **Sandbox lifecycle**: SandboxClaim → SandboxTemplate → Pod, grace period handling, timeout classification
- **EventHub**: in-memory pub/sub with 1000-event ring buffer, WebSocket fan-out, sequence-based reconnection
- **Status watcher**: backup callback mechanism with 5-minute TTL for stale CallbackPending conditions

#### Page 4: GitHub Apps Explained (`github-apps.md`)
Content available from codebase:
- **Why two apps**: separation of concerns — Trigger App reads webhooks/posts comments, Runner App generates repo-scoped tokens
- **Trigger App** (GitHub adapter):
  - Permissions: Issues (read/write)
  - Webhook events: `issue_comment`
  - Authentication: `ghinstallation.New()` → installation-level transport
  - Operations: `PostComment`, `ListIssueComments`
- **Runner App** (API server):
  - Permissions: Contents (read/write), Pull Requests (read/write)
  - No webhook subscriptions needed
  - Authentication: `ghinstallation.NewAppsTransport()` → app-level transport, per-request installation transports for repo-scoped tokens
  - One-time token issuance with `tokenIssued` anti-replay flag
- **Shared secret**: `SHEPHERD_CALLBACK_SECRET` for HMAC-SHA256 signed callbacks between API and adapter
- **Important gotcha**: both apps use `SHEPHERD_GITHUB_APP_ID` env var name but refer to different GitHub Apps

### Section 3: Setup & Configuration

#### Page 5: GitHub App Setup with Manifests (`github-app-setup.md`)
Content available from GitHub docs + codebase analysis:

**Trigger App Manifest**:
```json
{
  "name": "Shepherd Trigger",
  "url": "https://github.com/NissesSenap/shepherd",
  "hook_attributes": {
    "url": "https://<your-adapter-host>/webhook",
    "active": true
  },
  "redirect_url": "https://<your-setup-page>/callback",
  "public": false,
  "default_permissions": {
    "issues": "write"
  },
  "default_events": [
    "issue_comment"
  ]
}
```

**Runner App Manifest**:
```json
{
  "name": "Shepherd Runner",
  "url": "https://github.com/NissesSenap/shepherd",
  "public": false,
  "default_permissions": {
    "contents": "write",
    "pull_requests": "write"
  },
  "default_events": []
}
```

- **Manifest flow**: 3 steps (redirect to GitHub → GitHub redirects back with code → exchange code for credentials), all within 1 hour
- **Registration URLs**: `https://github.com/settings/apps/new` (personal) or `https://github.com/organizations/ORGANIZATION/settings/apps/new` (org)
- **What you get back**: `id`, `pem` (private key), `webhook_secret` — store these as K8s secrets
- **Manual setup alternative**: step-by-step for each app if not using manifests
- **K8s secret creation**: `shepherd-github-app` secret with keys `app-id`, `installation-id`, `private-key`
- **Installation**: install each app on the target repos/org

#### Page 6: Deployment & Configuration (`deployment.md`)
Content available from codebase:
- **Prerequisites**: Kubernetes cluster, cert-manager (optional), agent-sandbox operator
- **Kustomize structure**: `config/default/` composes CRD + RBAC + operator + API + web
- **Installing CRDs**: `make install` (includes AgentTask CRD, external sandbox CRDs for envtest only)
- **Installing agent-sandbox**: `make install-agent-sandbox` (v0.1.1)
- **Deploying Shepherd**: `make deploy` with image configuration
- **Environment variables reference** — all 3 components:
  - API: `SHEPHERD_API_ADDR`, `SHEPHERD_INTERNAL_API_ADDR`, `SHEPHERD_CALLBACK_SECRET`, `SHEPHERD_NAMESPACE`, GitHub App vars
  - Operator: `SHEPHERD_METRICS_ADDR`, `SHEPHERD_HEALTH_ADDR`, `SHEPHERD_LEADER_ELECTION`, `SHEPHERD_API_URL`
  - GitHub Adapter: `SHEPHERD_GITHUB_ADDR`, `SHEPHERD_GITHUB_WEBHOOK_SECRET`, GitHub App vars, `SHEPHERD_API_URL`, `SHEPHERD_CALLBACK_URL`, `SHEPHERD_DEFAULT_SANDBOX_TEMPLATE`
- **RBAC requirements**: operator ClusterRole (sandboxes, sandboxclaims, agenttasks), API Role (agenttasks, agenttasks/status)
- **SandboxTemplate creation**: example YAML from `config/samples/sandbox-template-runner.yaml`
- **Frontend**: nginx container proxies `/api/` to API service, serves SPA with fallback routing
- **Optional**: Prometheus ServiceMonitor, NetworkPolicy for metrics

#### Page 7: Configuration Reference (`configuration.md`)
Content available from codebase:
- **CLI flags and env vars**: complete table for each subcommand (`api`, `operator`, `github`)
- **AgentTask CRD spec reference**: all fields with types, validation rules, defaults
- **SandboxTemplate reference**: pod template, resource requirements, volumes
- **RunnerSpec options**: `sandboxTemplateName`, `timeout` (default 30m), `serviceAccountName`, `resources`
- **Callback configuration**: HMAC secret, URL requirements (no localhost/metadata IPs), signature format
- **Frontend configuration**: `VITE_API_URL` build-time var, nginx proxy config

### Section 4: Extending Shepherd

#### Page 8: Building Custom Runners (`custom-runners.md`)
Content available from codebase — this is the most detailed guide:

**Runner Protocol** (5-step contract):

1. **Expose `POST /task` on port 8888** — receive `{"taskID": "...", "apiURL": "..."}` from operator, respond 200 (or 409 if busy)
2. **Fetch task data** — `GET {apiURL}/api/v1/tasks/{taskID}/data` → `{description, context, sourceURL, repo: {url, ref}}`
3. **Fetch GitHub token** (optional, one-time) — `GET {apiURL}/api/v1/tasks/{taskID}/token` → `{token, expiresAt}`. Use as `git clone https://x-access-token:{token}@github.com/org/repo.git`
4. **Stream events** (optional) — `POST {apiURL}/api/v1/tasks/{taskID}/events` with `{events: [{sequence, timestamp, type, summary, ...}]}`. Types: `thinking`, `tool_call`, `tool_result`, `error`
5. **Report completion** — `POST {apiURL}/api/v1/tasks/{taskID}/status` with `{event: "completed"|"failed", message, details: {pr_url, error}}`

**Python example** (minimal runner):
```python
from flask import Flask, request, jsonify
import requests, subprocess, os

app = Flask(__name__)
task_queue = None

@app.route("/healthz")
def healthz():
    return "ok"

@app.route("/task", methods=["POST"])
def receive_task():
    global task_queue
    if task_queue is not None:
        return jsonify({"error": "task already assigned"}), 409
    task_queue = request.json
    return jsonify({"status": "accepted"})

def execute_task(task_id, api_url):
    # Step 1: Report started
    requests.post(f"{api_url}/api/v1/tasks/{task_id}/status",
        json={"event": "started", "message": "runner started"})

    # Step 2: Fetch task data
    resp = requests.get(f"{api_url}/api/v1/tasks/{task_id}/data")
    task_data = resp.json()

    # Step 3: Fetch token
    resp = requests.get(f"{api_url}/api/v1/tasks/{task_id}/token")
    token = resp.json()["token"]

    # Step 4: Clone and work
    repo_url = task_data["repo"]["url"]
    clone_url = repo_url.replace("https://", f"https://x-access-token:{token}@")
    subprocess.run(["git", "clone", clone_url, "/workspace/repo"], check=True)
    # ... do your work ...

    # Step 5: Report completion
    requests.post(f"{api_url}/api/v1/tasks/{task_id}/status",
        json={"event": "completed", "message": "done",
              "details": {"pr_url": "https://github.com/..."}})
```

**Node.js example** (minimal runner):
```javascript
import express from 'express';

const app = express();
app.use(express.json());
let currentTask = null;

app.get('/healthz', (req, res) => res.send('ok'));

app.post('/task', (req, res) => {
  if (currentTask) return res.status(409).json({ error: 'task already assigned' });
  currentTask = req.body;
  res.json({ status: 'accepted' });
  executeTask(currentTask.taskID, currentTask.apiURL);
});

async function executeTask(taskID, apiURL) {
  // Step 1: Report started
  await fetch(`${apiURL}/api/v1/tasks/${taskID}/status`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ event: 'started', message: 'runner started' }),
  });

  // Step 2: Fetch task data
  const dataResp = await fetch(`${apiURL}/api/v1/tasks/${taskID}/data`);
  const taskData = await dataResp.json();

  // Step 3: Fetch token (one-time!)
  const tokenResp = await fetch(`${apiURL}/api/v1/tasks/${taskID}/token`);
  const { token } = await tokenResp.json();

  // Step 4: Clone, branch, work, commit, push, open PR
  // ... your implementation ...

  // Step 5: Report completion
  await fetch(`${apiURL}/api/v1/tasks/${taskID}/status`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      event: 'completed',
      message: 'PR created',
      details: { pr_url: 'https://github.com/org/repo/pull/123' },
    }),
  });
}

app.listen(8888);
```

**SandboxTemplate for custom runners**: how to write a SandboxTemplate YAML pointing to a custom container image
**Event streaming protocol**: sequence numbers, event types, timing considerations
**Key constraints**: token is one-time use (409 on second call), task data returns 410 if task is already terminal, runner must handle its own timeout gracefully

#### Page 9: API Reference (`api-reference.md`)
Content available from codebase (OpenAPI spec + handler analysis):
- **Public endpoints** (port 8080): `GET /healthz`, `GET /readyz`, `POST /api/v1/tasks`, `GET /api/v1/tasks`, `GET /api/v1/tasks/{taskID}`, `GET /api/v1/tasks/{taskID}/events` (WebSocket)
- **Internal endpoints** (port 8081): `POST /api/v1/tasks/{taskID}/status`, `POST /api/v1/tasks/{taskID}/events`, `GET /api/v1/tasks/{taskID}/data`, `GET /api/v1/tasks/{taskID}/token`
- Full request/response schemas for each endpoint
- Query parameter documentation (repo filter normalization, active filter)
- WebSocket protocol (upgrade, `?after=N` reconnection, message envelope, completion)
- Error codes and their meanings (400, 404, 409, 410, 413, 415, 502, 503)
- Note: could be auto-generated from `api/openapi.yaml` using a Hugo shortcode or Swagger UI embed

### Additional Pages to Consider

#### Page 10: Contributing / Development Guide (`contributing.md`)
- `make build` / `make test` / `make lint-fix` workflow
- Frontend dev: `make web-dev` / `make web-test` / `make web-check` / `make web-lint-fix`
- CRD changes: edit `api/v1alpha1/` → `make manifests generate`
- API changes: edit `api/openapi.yaml` → `make web-gen-types`
- E2E testing: `make test-e2e-interactive` (keeps Kind cluster)
- Code conventions: testify, httptest, table-driven tests, Svelte 5 runes only

#### Page 11: Troubleshooting (`troubleshooting.md`)
- Common issues: `SHEPHERD_GITHUB_APP_ID` same env var name for different apps
- Token endpoint returns 503: GitHub App not configured
- Token endpoint returns 409: token already issued (one-time use)
- Task stuck in Pending: SandboxClaim not becoming Ready
- Callback not delivered: check `ConditionNotified` on the AgentTask CRD
- golangci-lint failures: don't scaffold unused functions
- `go mod tidy` removes unused packages

## Proposed Site Structure

```
docs/
  _index.md                          # Home / Introduction
  getting-started/
    _index.md                        # Section index
    quickstart.md                    # Local dev quickstart
  architecture/
    _index.md                        # Section index
    overview.md                      # Architecture overview
    github-apps.md                   # Two GitHub Apps explained
  setup/
    _index.md                        # Section index
    github-app-setup.md             # GitHub App setup with manifests
    deployment.md                    # Deployment & configuration
    configuration.md                 # Configuration reference
  extending/
    _index.md                        # Section index
    custom-runners.md               # Building custom runners
    api-reference.md                # API reference
  contributing.md                    # Contributing / dev guide
  troubleshooting.md                # Troubleshooting
```

## GitHub App Manifest Flow Details

From GitHub documentation research:

### Three-Step Registration Flow
1. **Redirect**: Send user to `https://github.com/settings/apps/new` with `manifest` JSON parameter
2. **Callback**: GitHub redirects to `redirect_url` with temporary `code` parameter
3. **Exchange**: `POST /app-manifests/{code}/conversions` returns `id`, `pem` (private key), `webhook_secret`

All three steps must complete within 1 hour.

### Manifest Parameters
| Parameter | Type | Required | Notes |
|---|---|---|---|
| `name` | string | No | App name (editable by user) |
| `url` | string | **Yes** | App homepage URL |
| `hook_attributes` | object | No | `{url, active}` — webhook endpoint |
| `redirect_url` | string | No | Where GitHub sends user after registration |
| `callback_urls` | array | No | Up to 10 OAuth callback URLs |
| `setup_url` | string | No | Post-installation setup redirect |
| `description` | string | No | App description |
| `public` | boolean | No | Public or private app |
| `default_events` | array | No | Webhook event subscriptions |
| `default_permissions` | object | No | Permission name → access level |
| `request_oauth_on_install` | boolean | No | Request user auth on install |
| `setup_on_update` | boolean | No | Redirect on updates |

### What the Exchange Returns
- `id` — GitHub App ID (use as `SHEPHERD_GITHUB_APP_ID`)
- `pem` — Private key (save to file, use as `SHEPHERD_GITHUB_PRIVATE_KEY_PATH`)
- `webhook_secret` — Generated secret (use as `SHEPHERD_GITHUB_WEBHOOK_SECRET`)
- `client_id`, `client_secret` — OAuth credentials (not needed for Shepherd)

The installation ID is obtained separately after installing the app on a repo/org.

## Code References

- CLI entry point: `cmd/shepherd/main.go:31-68`
- API server: `pkg/api/server.go:177-205` (two-port setup)
- Task handler: `pkg/api/handler_tasks.go:90-256`
- Token handler: `pkg/api/handler_token.go:34-105`
- Event handler: `pkg/api/handler_events.go:76-100`
- WebSocket handler: `pkg/api/handler_ws.go:63-150`
- GitHub adapter: `pkg/adapters/github/server.go:121-128`
- Webhook handler: `pkg/adapters/github/webhook.go:73-281`
- Callback handler: `pkg/adapters/github/callback.go:77-250`
- Operator reconciler: `internal/controller/agenttask_controller.go:68-216`
- Runner server: `pkg/runner/server.go:42-195`
- Runner client: `pkg/runner/client.go:35-258`
- GoRunner: `cmd/shepherd-runner/gorunner.go:131-241`
- Hook: `cmd/shepherd-runner/hook.go:36-146`
- CRD types: `api/v1alpha1/agenttask_types.go`
- OpenAPI spec: `api/openapi.yaml`
- Kustomize default: `config/default/kustomization.yaml`
- Kustomize test: `config/test/kustomization.yaml`
- Kind config: `test/e2e/kind-config.yaml`
- E2E test runner: `test/e2e/testrunner/main.go`
- Makefile: `Makefile`

## Open Questions

1. **Hugo theme choice**: The user mentioned "basic theme" — Docsy, Hextra, or Hugo Book are popular for project docs
2. **API reference generation**: Could embed Swagger UI or use `openapi-to-md` to generate from `api/openapi.yaml` instead of hand-writing
3. **Versioning**: Should docs be versioned per release?
4. **GitHub Pages deployment**: GitHub Actions workflow needed for Hugo build + deploy
5. **GitHub App manifest implementation**: Shepherd could potentially host a `/setup` endpoint that implements the manifest flow, but that's a code change, not a docs-only task
