---
title: GitHub Apps Explained
weight: 2
---

Shepherd uses **two separate GitHub Apps** — one for the adapter (webhooks and comments) and one for the API server (repository access tokens). This page explains why, what each app does, and how they authenticate.

## Why Two Apps?

Separation of concerns:

- The **Trigger App** needs to read webhooks and write comments on issues. It runs in the adapter and never touches repository contents.
- The **Runner App** needs to read and write repository contents and pull requests. It runs in the API server and generates tokens that runners use to clone repos and push code.

By splitting these into separate apps, each component gets the minimum permissions it needs. A compromised adapter cannot access repository contents; a compromised runner token cannot post comments or read other issues.

## Trigger App (GitHub Adapter)

The Trigger App runs inside the GitHub adapter component.

### Permissions

| Permission | Access | Purpose |
|------------|--------|---------|
| Issues | Read & Write | Read issue bodies, post completion/failure comments |

### Webhook Events

| Event | Purpose |
|-------|---------|
| `issue_comment` | Detects `@shepherd` mentions in issue comments |

### Authentication Flow

The adapter authenticates using [GitHub App installation authentication](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation):

1. On startup, the adapter creates an **installation transport** using `ghinstallation.New()` with the app ID, installation ID, and private key.
2. The transport automatically handles JWT generation and installation token refresh.
3. All GitHub API calls (posting comments, listing comments) go through this transport.

### What It Does

1. **Receives webhooks** — verifies the `X-Hub-Signature-256` HMAC-SHA256 signature using `SHEPHERD_GITHUB_WEBHOOK_SECRET`.
2. **Detects mentions** — scans comment bodies with the regex `(?i)(?:^|\s)@shepherd\b`.
3. **Deduplicates** — checks for active tasks on the same repo/issue before creating a new one.
4. **Assembles context** — collects all issue comments (up to 1 MB) as task context.
5. **Creates tasks** — calls the API server to create an `AgentTask` CRD.
6. **Posts results** — when it receives a signed callback, posts a comment with the task outcome (including a PR link on success).

### Configuration

| Environment Variable | Description |
|---------------------|-------------|
| `SHEPHERD_GITHUB_APP_ID` | Trigger App ID |
| `SHEPHERD_GITHUB_INSTALLATION_ID` | Trigger App installation ID |
| `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | Path to Trigger App private key |
| `SHEPHERD_GITHUB_WEBHOOK_SECRET` | Webhook signature secret |
| `SHEPHERD_GITHUB_ADDR` | Listen address (default `:8082`) |

## Runner App (API Server)

The Runner App runs inside the API server component.

### Permissions

| Permission | Access | Purpose |
|------------|--------|---------|
| Contents | Read & Write | Clone repos, push branches |
| Pull Requests | Read & Write | Create and update PRs |

### Webhook Events

None — the Runner App does not receive webhooks.

### Authentication Flow

The API server uses a more sophisticated authentication approach because it needs to generate **per-repository, per-request tokens**:

1. On startup, the API server creates an **app-level transport** using `ghinstallation.NewAppsTransport()` with the app ID and private key.
2. When a runner requests a token for a specific task, the API server:
   - Reads the task's repository URL from the CRD.
   - Creates a fresh **installation transport** via `ghinstallation.NewFromAppsTransport()` — this is cheap (no network call).
   - Sets `InstallationTokenOptions.Repositories` to scope the token to just that repository.
   - Calls `Token(ctx)` to generate a short-lived installation token.
3. The token is returned to the runner via `GET /api/v1/tasks/{taskID}/token`.

### One-Time Token Issuance

Tokens are **one-time use** per task. The API server tracks issuance with a `tokenIssued` flag on the `AgentTask` status:

- **First request**: returns the token, sets `tokenIssued = true`.
- **Second request**: returns HTTP **409 Conflict**.

This prevents token replay attacks — if a runner or attacker tries to fetch the token again, they get an error.

### Configuration

| Environment Variable | Description |
|---------------------|-------------|
| `SHEPHERD_GITHUB_APP_ID` | Runner App ID |
| `SHEPHERD_GITHUB_INSTALLATION_ID` | Runner App installation ID |
| `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | Path to Runner App private key |

{{< callout type="warning" >}}
**Important**: Both apps use the same environment variable name `SHEPHERD_GITHUB_APP_ID` but refer to **different GitHub Apps**. The Trigger App ID goes in the adapter deployment, and the Runner App ID goes in the API server deployment. Double-check which component you're configuring.
{{< /callout >}}

## Shared Callback Secret

When a task completes or fails, the API server sends a callback to the adapter. This callback is signed with HMAC-SHA256 using `SHEPHERD_CALLBACK_SECRET`, which must match on both sides:

- **API server** — signs the callback payload with the secret.
- **Adapter** — verifies the `X-Shepherd-Signature` header on incoming callbacks.

This ensures that only the API server (or someone with the shared secret) can trigger result comments on GitHub issues.

## Summary

| | Trigger App | Runner App |
|---|---|---|
| **Component** | GitHub Adapter | API Server |
| **Port** | :8082 | :8080 / :8081 |
| **Permissions** | Issues (read/write) | Contents (read/write), Pull Requests (read/write) |
| **Webhook events** | `issue_comment` | None |
| **Authentication** | Installation transport | App transport → per-request installation tokens |
| **Token model** | N/A | One-time per task (409 on replay) |

## Next Steps

- [GitHub App Setup](../setup/github-app-setup/) — step-by-step guide to creating both apps
- [Architecture Overview]({{< relref "overview" >}}) — how all components fit together
- [Troubleshooting](../troubleshooting/) — common issues with GitHub App configuration
