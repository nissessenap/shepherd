# Shepherd

A background coding agent orchestrator that runs on Kubernetes. Shepherd receives requests (e.g., from GitHub issue comments), creates sandboxed agent tasks, and reports results back.

## Architecture

Shepherd has three components:

| Component | Command | Description |
|-----------|---------|-------------|
| **API Server** | `shepherd api` | REST API for creating and managing agent tasks. Runs on Kubernetes with access to CRDs. |
| **Operator** | `shepherd operator` | Kubernetes controller that reconciles `AgentTask` CRDs, manages sandbox lifecycles, and watches for status changes. |
| **GitHub Adapter** | `shepherd github` | Receives GitHub webhooks, creates tasks via the API, and posts status comments back to issues. |

### How it works

1. A user comments `@shepherd fix this bug` on a GitHub issue
2. The GitHub adapter receives the webhook, checks for duplicate tasks, and creates an `AgentTask` via the API
3. The API server creates an `AgentTask` CRD in Kubernetes
4. The operator picks up the CRD, provisions a sandbox, and starts a runner
5. The runner clones the repo, works on the task, creates a PR, and reports status back
6. The API sends a callback to the adapter, which posts a comment on the original issue with the PR link

## GitHub App Setup

Shepherd uses **two separate GitHub Apps** with different responsibilities:

### Trigger App (GitHub Adapter)

Used by the adapter to receive webhooks and post comments on issues.

**Permissions required:**

- **Issues**: Read & Write (to post comments)
- **Pull Requests**: Read (to read PR details)

**Webhook events to subscribe:**

- `Issue comments`

**Webhook configuration:**

- **URL**: `https://<your-adapter-url>/webhook/`
- **Content type**: `application/json`
- **Secret**: A random string (used as `SHEPHERD_GITHUB_WEBHOOK_SECRET`)

### Runner App (API Server)

Used by the API server to generate short-lived tokens for runners to clone repos and create PRs.

**Permissions required:**

- **Contents**: Read & Write (to clone and push)
- **Pull Requests**: Read & Write (to create PRs)

**No webhook configuration needed** for this app.

### Getting the credentials

For each app you'll need:

- **App ID**: Found on the app's settings page
- **Installation ID**: Found in the URL after installing the app on a repo/org (`https://github.com/settings/installations/<ID>`)
- **Private key**: Generate and download from the app's settings page. Save as a PEM file.

## Configuration

### API Server (`shepherd api`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--listen-addr` | `SHEPHERD_API_ADDR` | `:8080` | Public API listen address |
| `--internal-listen-addr` | `SHEPHERD_INTERNAL_API_ADDR` | `:8081` | Internal (runner) API listen address |
| `--namespace` | `SHEPHERD_NAMESPACE` | `shepherd` | Kubernetes namespace for tasks |
| `--callback-secret` | `SHEPHERD_CALLBACK_SECRET` | | HMAC secret for signing callbacks to adapters |
| `--github-app-id` | `SHEPHERD_GITHUB_APP_ID` | | Runner App ID |
| `--github-installation-id` | `SHEPHERD_GITHUB_INSTALLATION_ID` | | Runner App installation ID |
| `--github-private-key-path` | `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | | Path to Runner App private key PEM |

The three `--github-*` flags must all be provided together, or all omitted (token endpoint will return 503).

### GitHub Adapter (`shepherd github`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--listen-addr` | `SHEPHERD_GITHUB_ADDR` | `:8082` | Adapter listen address |
| `--webhook-secret` | `SHEPHERD_GITHUB_WEBHOOK_SECRET` | | **Required.** GitHub webhook secret |
| `--github-app-id` | `SHEPHERD_GITHUB_APP_ID` | | **Required.** Trigger App ID |
| `--github-installation-id` | `SHEPHERD_GITHUB_INSTALLATION_ID` | | **Required.** Trigger App installation ID |
| `--github-private-key-path` | `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` | | **Required.** Path to Trigger App private key PEM |
| `--api-url` | `SHEPHERD_API_URL` | | **Required.** Shepherd API URL (e.g., `http://shepherd-api:8080`) |
| `--callback-secret` | `SHEPHERD_CALLBACK_SECRET` | | Shared HMAC secret (must match the API server) |
| `--callback-url` | `SHEPHERD_CALLBACK_URL` | | URL the API calls back on (e.g., `http://github-adapter:8082/callback`) |
| `--default-sandbox-template` | | `default` | Default sandbox template name |

### Operator (`shepherd operator`)

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--metrics-addr` | `SHEPHERD_METRICS_ADDR` | `:9090` | Metrics address |
| `--health-addr` | `SHEPHERD_HEALTH_ADDR` | `:8082` | Health probe address |
| `--leader-election` | `SHEPHERD_LEADER_ELECTION` | `false` | Enable leader election |
| `--api-url` | `SHEPHERD_API_URL` | | **Required.** Internal API server URL |

### Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--log-level` | `0` | Log verbosity (0=info, 1=debug) |
| `--dev-mode` | `false` | Human-readable log output |

## Deploying to Kubernetes

### Prerequisites

- A Kubernetes cluster
- `kubectl` configured
- Two GitHub Apps created and installed (see above)
- Secrets created in the cluster:

```bash
# Create secret for the Trigger App (adapter)
kubectl create secret generic shepherd-github-trigger \
  --from-literal=app-id=<TRIGGER_APP_ID> \
  --from-literal=installation-id=<TRIGGER_INSTALLATION_ID> \
  --from-file=private-key=<path-to-trigger-private-key.pem> \
  --from-literal=webhook-secret=<WEBHOOK_SECRET>

# Create secret for the Runner App (API server)
kubectl create secret generic shepherd-github-runner \
  --from-literal=app-id=<RUNNER_APP_ID> \
  --from-literal=installation-id=<RUNNER_INSTALLATION_ID> \
  --from-file=private-key=<path-to-runner-private-key.pem>

# Create shared callback secret
kubectl create secret generic shepherd-callback \
  --from-literal=secret=<RANDOM_CALLBACK_SECRET>
```

### Install CRDs and deploy

```bash
make install   # Install CRDs
make deploy    # Deploy operator
```

The API server and GitHub adapter can be deployed using the manifests in `config/` or with custom Kubernetes Deployments referencing the env vars listed above.

### Building

```bash
make build          # Build binary to bin/shepherd
make test           # Run tests (requires envtest)
make lint-fix       # Run linter
make ko-build-local # Build container image with ko
```

## Local development

```bash
# Run the API server
shepherd api \
  --namespace=default \
  --github-app-id=123 \
  --github-installation-id=456 \
  --github-private-key-path=./runner-key.pem

# Run the GitHub adapter
shepherd github \
  --webhook-secret=my-webhook-secret \
  --github-app-id=789 \
  --github-installation-id=012 \
  --github-private-key-path=./trigger-key.pem \
  --api-url=http://localhost:8080 \
  --callback-url=http://localhost:8082/callback
```

Use a tool like [smee.io](https://smee.io/) or [ngrok](https://ngrok.com/) to forward GitHub webhooks to your local adapter.

## Inspiration

- Spotify code agent [part 1](https://engineering.atspotify.com/2025/11/spotifys-background-coding-agent-part-1)
- Ramp [background agent](https://builders.ramp.com/post/why-we-built-our-background-agent)
- Spotify code agent [part 2](https://engineering.atspotify.com/2025/11/context-engineering-background-coding-agents-part-2)
- Spotify code agent [part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3)
