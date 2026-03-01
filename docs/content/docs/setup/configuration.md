---
title: Configuration Reference
weight: 3
---

Complete reference for all CLI flags, environment variables, and CRD fields in Shepherd.

## CLI Subcommands

Shepherd is a single binary (`shepherd`) with three subcommands:

```bash
shepherd api        # Run API server
shepherd operator   # Run K8s operator
shepherd github     # Run GitHub adapter
```

Global flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--log-level` | `0` | Log level (0=info, 1=debug) |
| `--dev-mode` | `false` | Enable development mode logging |

## API Server (`shepherd api`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--listen-addr` | `SHEPHERD_API_ADDR` | `:8080` | Public API listen address |
| `--internal-listen-addr` | `SHEPHERD_INTERNAL_API_ADDR` | `:8081` | Internal (runner) API listen address |
| `--callback-secret` | `SHEPHERD_CALLBACK_SECRET` | (empty) | HMAC secret for signing adapter callbacks |
| `--namespace` | `SHEPHERD_NAMESPACE` | `shepherd` | Namespace for AgentTask creation |
| `--github-app-id` | `SHEPHERD_GITHUB_APP_ID` | (none) | Runner App ID |
| `--github-installation-id` | `SHEPHERD_GITHUB_INSTALLATION_ID` | (none) | Runner App installation ID |
| `--github-private-key-path` | `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | (none) | Path to Runner App private key file |

The three GitHub flags are **all-or-nothing** — set all three or none. Without them, the API server starts but the token endpoint (`GET /api/v1/tasks/{taskID}/token`) returns **503 Service Unavailable**.

## Operator (`shepherd operator`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--metrics-addr` | `SHEPHERD_METRICS_ADDR` | `:9090` | Prometheus metrics address |
| `--health-addr` | `SHEPHERD_HEALTH_ADDR` | `:8082` | Health probe address |
| `--leader-election` | `SHEPHERD_LEADER_ELECTION` | `false` | Enable leader election for HA |
| `--apiurl` | `SHEPHERD_API_URL` | (required) | Internal API server URL |

The `--apiurl` must be a valid URL with scheme and host. In-cluster, this is typically:

```
http://shepherd-shepherd-api.shepherd-system.svc.cluster.local:8081
```

## GitHub Adapter (`shepherd github`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--listen-addr` | `SHEPHERD_GITHUB_ADDR` | `:8082` | Adapter listen address |
| `--webhook-secret` | `SHEPHERD_GITHUB_WEBHOOK_SECRET` | (required) | GitHub webhook signature secret |
| `--github-app-id` | `SHEPHERD_GITHUB_APP_ID` | (required) | Trigger App ID |
| `--github-installation-id` | `SHEPHERD_GITHUB_INSTALLATION_ID` | (required) | Trigger App installation ID |
| `--github-private-key-path` | `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | (required) | Path to Trigger App private key file |
| `--api-url` | `SHEPHERD_API_URL` | (required) | Shepherd API server URL |
| `--callback-secret` | `SHEPHERD_CALLBACK_SECRET` | (empty) | Shared secret for callback verification |
| `--callback-url` | `SHEPHERD_CALLBACK_URL` | (required) | URL where API sends completion callbacks |
| `--default-sandbox-template` | `SHEPHERD_DEFAULT_SANDBOX_TEMPLATE` | `default` | Default SandboxTemplate name for new tasks |

{{< callout type="warning" >}}
**Same env var, different apps**: The adapter and API server both use `SHEPHERD_GITHUB_APP_ID`, but they refer to **different GitHub Apps**. The adapter uses the Trigger App credentials; the API server uses the Runner App credentials. See [GitHub Apps Explained](../../architecture/github-apps/).
{{< /callout >}}

## AgentTask CRD

The `AgentTask` CRD (`toolkit.shepherd.io/v1alpha1`) is the core data model. Tasks are created by the API server and reconciled by the operator.

### Spec Fields

#### `spec.repo`

| Field | Type | Required | Validation | Description |
|-------|------|----------|------------|-------------|
| `url` | string | Yes | Must start with `https://` | Repository HTTPS URL |
| `ref` | string | No | — | Git ref (branch, tag, commit) |

The `repo` field is **immutable** — it cannot be changed after creation.

#### `spec.task`

| Field | Type | Required | Validation | Description |
|-------|------|----------|------------|-------------|
| `description` | string | Yes | MinLength=1 | What the runner should do |
| `context` | string | No | — | Additional context (gzip+base64 when `contextEncoding: gzip`) |
| `contextEncoding` | string | No | Enum: `""`, `"gzip"` | Encoding of the context field |
| `sourceURL` | string | No | — | Origin URL (e.g., GitHub issue URL) |
| `sourceType` | string | No | Enum: `""`, `"issue"`, `"pr"`, `"fleet"` | Trigger type |
| `sourceID` | string | No | — | Trigger instance ID (e.g., issue number) |

The `task` field is **immutable** — it cannot be changed after creation.

The API server accepts plain-text context and compresses it automatically (gzip + base64) for CRD storage.

#### `spec.callback`

| Field | Type | Required | Validation | Description |
|-------|------|----------|------------|-------------|
| `url` | string | Yes | Must start with `http://` or `https://` | Completion callback URL |

The callback URL is validated at creation time. Blocked hosts: `169.254.169.254`, `localhost`, `127.0.0.1`, `::1`, `0.0.0.0`.

#### `spec.runner`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `sandboxTemplateName` | string | Yes | — | Name of the SandboxTemplate to use |
| `timeout` | duration | No | `30m` | Maximum task execution duration |
| `serviceAccountName` | string | No | — | ServiceAccount for the sandbox pod |
| `resources` | ResourceRequirements | No | — | CPU/memory resource overrides |

### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `observedGeneration` | int64 | Last reconciled generation |
| `startTime` | Time | When the runner was assigned |
| `completionTime` | Time | When the task reached a terminal state |
| `sandboxClaimName` | string | Name of the associated SandboxClaim |
| `result.prURL` | string | Pull request URL (on success) |
| `result.error` | string | Error message (on failure) |
| `graceDeadline` | Time | Sandbox termination grace window end |
| `tokenIssued` | bool | Prevents token replay (one-time use) |

### Conditions

**`Succeeded`** — primary lifecycle condition:

| Reason | Status | Meaning |
|--------|--------|---------|
| `Pending` | Unknown | Waiting for sandbox |
| `Running` | Unknown | Runner is executing |
| `Succeeded` | True | Task completed successfully |
| `Failed` | False | Task failed |
| `TimedOut` | False | Sandbox expired or claim expired |
| `Cancelled` | False | Task was cancelled |

A task is **terminal** when the `Succeeded` condition has status `True` or `False` (not `Unknown`).

**`Notified`** — callback delivery tracking:

| Reason | Status | Meaning |
|--------|--------|---------|
| `CallbackPending` | Unknown | Callback queued |
| `CallbackSent` | True | Callback delivered |
| `CallbackFailed` | True | Callback delivery failed |

## SandboxTemplate

`SandboxTemplate` resources (`extensions.agents.x-k8s.io/v1alpha1`) define the runner environment. They are managed by the [agent-sandbox operator](https://agent-sandbox.sigs.k8s.io/docs/).

Key fields:

| Field | Description |
|-------|-------------|
| `spec.template.spec.containers` | Runner container image, ports, probes, env, resources |
| `spec.template.spec.securityContext` | Pod-level security context |
| `spec.template.spec.volumes` | Volumes available to the runner |
| `spec.template.spec.restartPolicy` | Should be `Never` for sandbox pods |

The runner container must:

- Expose port **8888** for task assignment (`POST /task`)
- Have a readiness probe on port 8888 (e.g., `GET /healthz`)

See the [Deployment Guide](../deployment/#step-5-create-a-sandboxtemplate) for a complete example.

## Callback Configuration

When a task reaches a terminal state, the API server sends a signed HTTP POST to the callback URL.

### Signature Format

If `SHEPHERD_CALLBACK_SECRET` is set, the callback includes:

```
X-Shepherd-Signature: sha256=<hex-encoded HMAC-SHA256>
```

The HMAC is computed over the JSON request body using the shared secret. The adapter verifies this signature before processing the callback.

### Callback Payload

```json
{
  "taskID": "task-name",
  "event": "completed",
  "message": "Task completed successfully",
  "details": {
    "pr_url": "https://github.com/org/repo/pull/123"
  }
}
```

The `event` field is either `"completed"` or `"failed"`. On success, `details.pr_url` contains the pull request URL.

## Frontend Configuration

The web frontend is a Svelte 5 SPA built with SvelteKit (adapter-static).

### Build-Time Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VITE_API_URL` | (empty) | API base URL; empty means same-origin |

In Kubernetes, the nginx container proxies `/api/` to the API server, so `VITE_API_URL` is typically left empty.

### Dev Server

For local development, the SvelteKit dev server proxies `/api` to `localhost:8080`:

```bash
make web-dev
```

This is configured in `web/vite.config.ts`.

## Next Steps

- [Architecture Overview](../../architecture/overview/) — how components interact
- [Custom Runners](../../extending/custom-runners/) — build your own runner with the 5-step protocol
- [Troubleshooting](../../troubleshooting/) — common configuration issues
