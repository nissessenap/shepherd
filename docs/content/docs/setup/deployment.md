---
title: Deployment Guide
weight: 2
---

This guide covers deploying Shepherd to a Kubernetes cluster with Helm. For a quick local test, see the [Quickstart](../../getting-started/quickstart/). For development, see the [Contributing Guide](https://github.com/NissesSenap/shepherd/blob/main/CONTRIBUTING.md).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Kubernetes cluster** | v1.28+ recommended |
| **kubectl** | Configured to access your cluster |
| **Helm** | 3.10+ |
| **agent-sandbox operator** | Manages runner sandbox pods |
| **Two GitHub Apps** | See [GitHub App Setup](../github-app-setup/) |

## Step 1: Install agent-sandbox

The [agent-sandbox operator](https://github.com/kubernetes-sigs/agent-sandbox) manages ephemeral sandbox pods for task runners:

```bash
export AGENT_SANDBOX_VERSION="v0.1.1"
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml
```

Wait for the operator to be ready:

```bash
kubectl wait --for=condition=Available deployment/agent-sandbox-controller-manager \
  -n agent-sandbox-system --timeout=120s
```

See the [agent-sandbox getting started guide](https://agent-sandbox.sigs.k8s.io/docs/getting_started/) for more details.

## Step 2: Create Secrets

Shepherd uses two GitHub Apps — one for the adapter (webhooks/comments) and one for the API server (token generation). Create a secret for each:

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

The runner also needs an Anthropic API key for the sandbox containers:

```bash
kubectl create secret generic anthropic-credentials \
  --namespace shepherd-system \
  --from-literal=api-key=<YOUR_ANTHROPIC_API_KEY>
```

See [GitHub App Setup](../github-app-setup/) for creating the apps and obtaining these values.

## Step 3: Create a SandboxTemplate

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
The template name (`runner` in this example) is what you'll use as the `sandboxTemplateName` in task creation requests. The GitHub adapter uses `SHEPHERD_DEFAULT_SANDBOX_TEMPLATE` to set this automatically.
{{< /callout >}}

See the [agent-sandbox documentation](https://agent-sandbox.sigs.k8s.io/docs/) and [Custom Runners](../../extending/custom-runners/) for building your own runner container.

{{< callout type="info" >}}
For lower latency, agent-sandbox supports **warm sandboxes** — pre-provisioned pods that are ready before tasks arrive. See the [agent-sandbox warm pools documentation](https://agent-sandbox.sigs.k8s.io/docs/) for details on configuring warm agent pools.
{{< /callout >}}

## Step 4: Deploy with Helm

### Basic Install (without GitHub adapter)

```bash
helm install shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
  --version 0.1.0 \
  --create-namespace -n shepherd-system
```

### Full Install (with GitHub adapter)

```bash
helm install shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
  --version 0.1.0 \
  --set api.githubApp.enabled=true \
  --set api.githubApp.existingSecret=shepherd-runner-app \
  --set githubAdapter.enabled=true \
  --set githubAdapter.existingSecret=shepherd-trigger-app \
  --set githubAdapter.callbackURL=https://your-domain.com/callback \
  --create-namespace -n shepherd-system
```

### Using a Custom Values File

For more complex configurations, create a `values.yaml` and install with:

```bash
helm install shepherd oci://ghcr.io/nissessenap/helm-charts/shepherd \
  --version 0.1.0 \
  -f my-values.yaml \
  --create-namespace -n shepherd-system
```

See the [Helm Chart Values](../helm-values/) reference for all available options.

## Step 5: Verify

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

{{< callout type="warning" >}}
**Ingress / HTTPRoute** support is planned but not yet available in the chart. For production access, configure your own ingress controller, use `extraObjects` to add Ingress resources, or use `kubectl port-forward`.
{{< /callout >}}

## Next Steps

- [Helm Chart Values](../helm-values/) — full reference for all chart configuration options
- [Configuration Reference](../configuration/) — CLI flags, environment variables, and CRD fields
- [GitHub App Setup](../github-app-setup/) — if you haven't created the apps yet
- [Custom Runners](../../extending/custom-runners/) — build your own runner container
