---
title: Deployment Guide
weight: 2
---

This guide covers deploying Shepherd to a Kubernetes cluster. If you're looking to run locally for development, see the [Quickstart](../../getting-started/quickstart/) instead.

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Kubernetes cluster** | v1.28+ recommended |
| **kubectl** | Configured to access your cluster |
| **Kustomize** | Bundled with kubectl or install separately |
| **agent-sandbox operator** | Manages runner sandbox pods |
| **Two GitHub Apps** | See [GitHub App Setup](../github-app-setup/) |
| **cert-manager** (optional) | For webhook TLS certificates |

## Architecture Recap

Shepherd deploys four components into a single namespace (`shepherd-system`):

| Component | Deployment | Purpose |
|-----------|------------|---------|
| **Operator** | `shepherd-controller-manager` | Reconciles AgentTask CRDs, manages sandbox lifecycle |
| **API Server** | `shepherd-shepherd-api` | Public + internal HTTP API, token generation |
| **GitHub Adapter** | (not in default overlay) | Webhook receiver, comment poster |
| **Web Frontend** | `shepherd-shepherd-web` | Svelte SPA served by nginx |

The default Kustomize overlay (`config/default/`) deploys the operator, API server, and web frontend. The GitHub adapter is deployed separately or added to your own overlay.

## Step 1: Install CRDs

Install the AgentTask CRD into your cluster:

```bash
make install
```

This runs `kustomize build config/crd | kubectl apply -f -` to install the `AgentTask` custom resource definition.

## Step 2: Install agent-sandbox

The [agent-sandbox operator](https://agent-sandbox.sigs.k8s.io/docs/) manages ephemeral sandbox pods for task runners:

```bash
make install-agent-sandbox
```

This installs the agent-sandbox operator (v0.1.1) and waits for it to be ready.

## Step 3: Create Secrets

Create the GitHub App secret for the API server (the Runner App):

```bash
kubectl create secret generic shepherd-github-app \
  --namespace shepherd-system \
  --from-literal=app-id=<RUNNER_APP_ID> \
  --from-literal=installation-id=<RUNNER_INSTALLATION_ID> \
  --from-file=private-key=<RUNNER_PRIVATE_KEY_FILE>
```

See [GitHub App Setup](../github-app-setup/) for creating the apps and obtaining these values.

## Step 4: Deploy Shepherd

```bash
make deploy
```

This builds and applies the default Kustomize overlay, which includes:

- CRDs (`config/crd/`)
- Operator RBAC + deployment (`config/rbac/`, `config/manager/`)
- API server RBAC + deployment (`config/api-rbac/`, `config/api/`)
- Web frontend deployment (`config/web/`)
- Metrics service (`config/default/metrics_service.yaml`)

### Customizing the Image

By default, the overlay uses `shepherd:latest`. To use a custom image:

```bash
IMG=ghcr.io/your-org/shepherd:v1.0.0 make deploy
```

Or edit `config/default/kustomization.yaml` directly:

```yaml
images:
- name: controller
  newName: ghcr.io/your-org/shepherd
  newTag: v1.0.0
```

## Step 5: Create a SandboxTemplate

Runners need a `SandboxTemplate` that defines the container image, resources, and volumes. Here's the default template from `config/samples/sandbox-template-runner.yaml`:

```yaml
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
```

Apply it:

```bash
kubectl apply -f config/samples/sandbox-template-runner.yaml -n shepherd-system
```

{{< callout type="info" >}}
The template name (`runner` in this example) is what you'll use as the `sandboxTemplateName` in task creation requests. The GitHub adapter uses `SHEPHERD_DEFAULT_SANDBOX_TEMPLATE` to set this automatically.
{{< /callout >}}

## Step 6: Verify

```bash
kubectl get pods -n shepherd-system
```

You should see:

```
NAME                                            READY   STATUS    RESTARTS   AGE
shepherd-controller-manager-xxxxx               1/1     Running   0          1m
shepherd-shepherd-api-xxxxx                     1/1     Running   0          1m
shepherd-shepherd-web-xxxxx                     1/1     Running   0          1m
```

Check the API server health:

```bash
kubectl port-forward -n shepherd-system svc/shepherd-shepherd-api 8080:8080
curl http://localhost:8080/healthz
# ok
```

## Kustomize Structure

The `config/` directory is organized as follows:

```
config/
├── crd/                  # AgentTask CRD + external CRDs (for envtest)
├── rbac/                 # Operator ClusterRole, ServiceAccount, leader election
├── api-rbac/             # API server Role + RoleBinding
├── manager/              # Operator Deployment (controller-manager)
├── api/                  # API server Deployment + Service
├── web/                  # Web frontend Deployment + Service
├── default/              # Production overlay (composes all of the above)
├── test/                 # Test overlay (NodePorts, no GitHub secrets)
├── network-policy/       # Optional NetworkPolicy
├── prometheus/           # Optional ServiceMonitor
└── samples/              # Example CRs (AgentTask, SandboxTemplate)
```

The `config/default/kustomization.yaml` composes: `crd` + `rbac` + `manager` + `api-rbac` + `api` + `web` + `metrics_service.yaml`.

## RBAC

### Operator (ClusterRole)

The operator runs with a `ClusterRole` because it needs cluster-wide access to sandbox resources:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| `toolkit.shepherd.io` | `agenttasks`, `agenttasks/status`, `agenttasks/finalizers` | get, list, watch, patch, update |
| `extensions.agents.x-k8s.io` | `sandboxclaims` | create, delete, get, list, watch |
| `agents.x-k8s.io` | `sandboxes` | get, list, watch |
| `coordination.k8s.io` | `leases` | create, delete, get, list, patch, update, watch |
| `""`, `events.k8s.io` | `events` | create, patch |

### API Server (Role)

The API server uses a namespace-scoped `Role`:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| `toolkit.shepherd.io` | `agenttasks` | get, list, watch, create |
| `toolkit.shepherd.io` | `agenttasks/status` | get, update, patch |

## Web Frontend

The web frontend is a Svelte 5 SPA served by nginx. The nginx container:

- Serves the built static files
- Proxies `/api/` requests to the API server service
- Provides SPA fallback routing (all unknown paths serve `index.html`)

The frontend image (`shepherd-web`) is built separately:

```bash
make docker-build-web
```

### Frontend Configuration

The frontend uses a build-time environment variable:

| Variable | Default | Description |
|----------|---------|-------------|
| `VITE_API_URL` | (empty) | API base URL; empty means same-origin (proxied by nginx) |

In most deployments, you don't need to set this — the nginx proxy handles API routing.

## Optional: Prometheus Monitoring

Enable Prometheus monitoring by uncommenting the prometheus resources in your kustomization:

```yaml
resources:
- ../prometheus
```

This creates a `ServiceMonitor` that scrapes the operator's metrics endpoint on port 9090.

## Optional: Network Policy

For additional security, enable the NetworkPolicy to restrict access to the metrics endpoint:

```yaml
resources:
- ../network-policy
```

## Next Steps

- [Configuration Reference](../configuration/) — all CLI flags, environment variables, and CRD fields
- [GitHub App Setup](../github-app-setup/) — if you haven't created the apps yet
- [Custom Runners](../../extending/custom-runners/) — build your own runner container
