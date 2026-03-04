---
title: Quickstart
weight: 1
---

Get Shepherd running locally in minutes. The first section requires only Docker and a few CLI tools — no GitHub App needed. The second section walks through connecting to GitHub for full end-to-end testing.

## Local-Only Testing (No GitHub)

This section sets up a local Kind cluster with Shepherd fully deployed. You can create tasks via the API and watch them execute in sandboxed runners — all without a GitHub App.

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| [Docker](https://docs.docker.com/get-docker/) | Latest | — |
| [Kind](https://kind.sigs.k8s.io/) | v0.20+ | `go install sigs.k8s.io/kind@latest` |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | Latest | — |
| [Helm](https://helm.sh/docs/intro/install/) | 3.10+ | — |

### Create a Kind Cluster

If you have the repo cloned:

```bash
make kind-create
```

Or create the cluster directly:

```bash
cat <<EOF | kind create cluster --name shepherd --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: 30080
    protocol: TCP
  - containerPort: 30081
    hostPort: 30081
    protocol: TCP
  - containerPort: 30082
    hostPort: 30082
    protocol: TCP
EOF
```

This creates a single-node Kind cluster named `shepherd` with three NodePort mappings:

- **Port 30080** — Public API
- **Port 30081** — Web UI
- **Port 30082** — GitHub Adapter (webhooks)

### Install agent-sandbox

The [agent-sandbox operator](https://github.com/kubernetes-sigs/agent-sandbox) manages ephemeral sandbox pods for task runners:

```bash
export AGENT_SANDBOX_VERSION="v0.1.1"
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/$AGENT_SANDBOX_VERSION/manifest.yaml
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/$AGENT_SANDBOX_VERSION/extensions.yaml
```

Wait for the operator to be ready:

```bash
kubectl wait --for=condition=Available deployment/agent-sandbox-controller-manager \
  -n agent-sandbox-system --timeout=120s
```

See the [agent-sandbox getting started guide](https://agent-sandbox.sigs.k8s.io/docs/getting_started/) for more details.

### Create a SandboxTemplate

Runners need a `SandboxTemplate` that defines the container image, resources, and volumes. Apply the sample template:

```bash
kubectl apply -n shepherd-system -f - <<'EOF'
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: runner
spec:
  template:
    spec:
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        runAsNonRoot: true
      containers:
        - name: runner
          image: ghcr.io/nissessenap/shepherd-runner:v0.1.0
          ports:
            - containerPort: 8888
              protocol: TCP
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8888
            initialDelaySeconds: 2
            periodSeconds: 5
          env:
            - name: ANTHROPIC_API_KEY
              valueFrom:
                secretKeyRef:
                  name: anthropic-credentials
                  key: api-key
          resources:
            requests:
              memory: "2Gi"
              cpu: "500m"
            limits:
              memory: "4Gi"
              cpu: "2000m"
          volumeMounts:
            - name: home
              mountPath: /home/shepherd
            - name: workspace
              mountPath: /workspace
      volumes:
        - name: home
          emptyDir: {}
        - name: workspace
          emptyDir: {}
      restartPolicy: Never
EOF
```

{{< callout type="info" >}}
The template name (`runner`) is what you'll reference as the `sandboxTemplateName` when creating tasks. The runner container requires an `ANTHROPIC_API_KEY` secret — create it with:

```bash
kubectl create secret generic anthropic-credentials \
  --namespace shepherd-system \
  --from-literal=api-key=<YOUR_ANTHROPIC_API_KEY>
```

{{< /callout >}}

See the [agent-sandbox documentation](https://agent-sandbox.sigs.k8s.io/docs/) for the full `SandboxTemplate` reference.

### Deploy with Helm

Download the quickstart values file from the repo (or use it directly if you have the repo cloned):

```bash
curl -sLO https://raw.githubusercontent.com/NissesSenap/shepherd/main/charts/shepherd/values-quickstart.yaml
```

Install Shepherd without the GitHub adapter:

```bash
helm install shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
  --version 0.1.0 \
  -f values-quickstart.yaml \
  --set githubAdapter.enabled=false \
  --create-namespace -n shepherd-system
```

Or install with the GitHub adapter (requires secrets — see [Connecting to GitHub](#connecting-to-github) below):

```bash
helm install shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
  --version 0.1.0 \
  -f values-quickstart.yaml \
  --set api.githubApp.enabled=true \
  --set api.githubApp.existingSecret=shepherd-runner-app \
  --set githubAdapter.callbackURL=https://XXXX.ngrok-free.app/callback \
  --create-namespace -n shepherd-system
```

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

{{< callout type="info" >}}
Without a GitHub App configured, the token endpoint (`GET /api/v1/tasks/{taskID}/token`) returns **503** — this is expected. Everything else works normally.
{{< /callout >}}

{{< callout type="warning" >}}
**Ingress / HTTPRoute** support is planned but not yet available in the chart. For production access beyond NodePorts, configure your own ingress controller or use `kubectl port-forward`.
{{< /callout >}}

---

## Connecting to GitHub

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
   ngrok http 30082
   ```

4. Note the `https://XXXX.ngrok-free.app` URL — you'll use this as the webhook URL when creating your GitHub App.

{{< callout type="warning" >}}
Only expose the adapter port (30082). The API server has no authentication — do **not** tunnel port 30080 to the internet.
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
   smee --url https://smee.io/YOUR_CHANNEL_ID --path /webhook --port 30082
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

3. **Upgrade the Helm release** with GitHub App configuration:

   ```bash
   helm upgrade shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
     --version 0.1.0 \
     -f values-quickstart.yaml \
     -n shepherd-system \
     --set api.githubApp.enabled=true \
     --set api.githubApp.existingSecret=shepherd-runner-app \
     --set githubAdapter.callbackURL=https://XXXX.ngrok-free.app/callback
   ```

4. **Install the GitHub Apps** on your target repository (or organization).

5. **Test it** — comment `@shepherd do something` on an issue in the target repo.

6. **Watch the task** appear in the web UI at [http://localhost:30081](http://localhost:30081) and progress through the lifecycle.

## Next Steps

- [Architecture Overview]({{< relref "../architecture/overview" >}}) — understand how the components fit together
- [Helm Chart Values](../../setup/helm-values/) — full reference for all chart configuration options
- [Deployment Guide](../../setup/deployment/) — production deployment with Helm
- [GitHub App Setup](../../setup/github-app-setup/) — detailed guide for creating the two GitHub Apps
