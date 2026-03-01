---
title: Quickstart
weight: 1
---

Get Shepherd running locally in minutes. Part 1 requires only Docker and a few CLI tools — no GitHub App needed. Part 2 walks through connecting to GitHub for full end-to-end testing.

## Part 1: Local-Only Testing

This section sets up a local Kind cluster with Shepherd fully deployed. You can create tasks via the API and watch them execute in sandboxed runners — all without a GitHub App.

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| [Docker](https://docs.docker.com/get-docker/) | Latest | — |
| [Kind](https://kind.sigs.k8s.io/) | v0.20+ | `go install sigs.k8s.io/kind@latest` |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | Latest | — |
| [ko](https://ko.build/) | v0.17+ | `go install github.com/google/ko@latest` |
| [Go](https://go.dev/dl/) | 1.25+ | — |
| [Node.js](https://nodejs.org/) | 22+ | — |

### Create a Kind Cluster

```bash
make kind-create
```

This creates a single-node Kind cluster named `shepherd` with two NodePort mappings:

- **Port 30080** — Public API
- **Port 30081** — Web UI

### Build and Load Images

```bash
make ko-build-kind
```

This builds three container images (`shepherd`, `shepherd-runner`, `shepherd-web`) and loads them into the Kind cluster.

### Install Dependencies

```bash
make install-agent-sandbox
make install
```

The first command installs the [agent-sandbox operator](https://agent-sandbox.sigs.k8s.io/docs/) (v0.1.1) which manages sandbox pods. The second installs the AgentTask CRD.

### Deploy the Test Overlay

```bash
make deploy-test
```

The test overlay deploys all Shepherd components with NodePort services and **no GitHub App requirement**. This is the fastest way to explore the system.

### Verify

```bash
kubectl get pods -n shepherd-system
```

You should see pods for the operator, API server, and web frontend all running.

### Access the Web UI and API

- **Web UI**: [http://localhost:30081](http://localhost:30081)
- **API**: [http://localhost:30080](http://localhost:30080)

### Create a Test Task

```bash
curl -s -X POST http://localhost:30080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "repo": {
      "url": "https://github.com/NissesSenap/shepherd"
    },
    "task": {
      "description": "Say hello world"
    },
    "callback": {
      "url": "https://example.com/callback"
    }
  }' | jq .
```

Watch the task appear in the web UI and move through `Pending` → `Running` → `Succeeded` or `Failed`.

### Frontend Development

For live-reloading frontend development:

```bash
make web-dev
```

This starts the SvelteKit dev server with an API proxy to `localhost:8080`.

{{< callout type="info" >}}
The test overlay does not configure a GitHub App. The token endpoint (`GET /api/v1/tasks/{taskID}/token`) returns **503** — this is expected. Everything else works normally.
{{< /callout >}}

---

## Part 2: Connecting to GitHub

Once you've verified the local setup, you can connect it to GitHub to receive real webhooks. This requires exposing your local cluster to the internet using ngrok or smee.io.

### Option A: ngrok (Recommended)

[ngrok](https://ngrok.com/) creates a public HTTPS tunnel to your local machine. You only need to expose the adapter port — GitHub sends webhooks to the adapter, and everything else stays local.

#### Setup

1. [Download ngrok](https://ngrok.com/download) and create a free account.

2. Authenticate:

   ```bash
   ngrok config add-authtoken <YOUR_TOKEN>
   ```

3. Start a tunnel to the GitHub adapter:

   ```bash
   ngrok http 8082
   ```

4. Note the `https://XXXX.ngrok-free.app` URL — you'll use this as the webhook URL when creating your GitHub App.

{{< callout type="warning" >}}
Only expose the adapter port (8082). The API server has no authentication — do **not** tunnel port 30080 to the internet.
{{< /callout >}}

#### Gotchas

- **HMAC signatures pass through unchanged** — webhook signature verification works correctly through ngrok.
- **The browser interstitial does not affect webhooks** — programmatic HTTP POSTs are not intercepted.
- **Inspection UI** — visit [http://127.0.0.1:4040](http://127.0.0.1:4040) to see all requests flowing through ngrok. Great for debugging.
- **Free tier caveat** — the random subdomain changes every time you restart ngrok. Either keep it running or update your GitHub App's webhook URL after restarting.

### Option B: smee.io (Simpler, Webhook-Only)

[smee.io](https://smee.io) is a webhook relay built by the Probot team. It gives you a permanent channel URL that survives restarts.

1. Install the client:

   ```bash
   npm install -g smee-client
   ```

2. Go to [https://smee.io](https://smee.io) and click **Start a new channel**. Copy the channel URL.

3. Use the channel URL as your GitHub App's webhook URL.

4. Forward webhooks to the local adapter:

   ```bash
   smee --url https://smee.io/YOUR_CHANNEL_ID --path /webhook --port 8082
   ```

{{< callout type="info" >}}
smee.io only relays webhooks, which is exactly what you need. The permanent channel URL is its main advantage over ngrok's free tier (where the subdomain changes on restart).
{{< /callout >}}

### Full End-to-End Flow

Once your tunnel is running:

1. **Create GitHub Apps** using the manifest flow described in the [GitHub App Setup](../../setup/github-app-setup/) guide. Use your ngrok/smee URL + `/webhook` as the webhook URL.

2. **Store credentials** as Kubernetes secrets:

   ```bash
   kubectl create secret generic shepherd-trigger-app \
     --namespace shepherd-system \
     --from-literal=app-id=<TRIGGER_APP_ID> \
     --from-literal=installation-id=<TRIGGER_INSTALLATION_ID> \
     --from-file=private-key=<TRIGGER_PRIVATE_KEY_FILE>

   kubectl create secret generic shepherd-runner-app \
     --namespace shepherd-system \
     --from-literal=app-id=<RUNNER_APP_ID> \
     --from-literal=installation-id=<RUNNER_INSTALLATION_ID> \
     --from-file=private-key=<RUNNER_PRIVATE_KEY_FILE>
   ```

3. **Redeploy** with GitHub App configuration (replace the test overlay with your production-like configuration):

   ```bash
   make deploy
   ```

4. **Install the GitHub Apps** on your target repository (or organization).

5. **Test it** — comment `@shepherd do something` on an issue in the target repo.

6. **Watch the task** appear in the web UI at [http://localhost:30081](http://localhost:30081) and progress through the lifecycle.

## Next Steps

- [Architecture Overview]({{< relref "../architecture/overview" >}}) — understand how the components fit together
- [GitHub App Setup](../../setup/github-app-setup/) — detailed guide for creating the two GitHub Apps
- [Deployment Guide](../../setup/deployment/) — production deployment with Kustomize
