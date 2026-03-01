---
title: Building Custom Runners
weight: 1
---

Shepherd runners are containers that execute tasks inside ephemeral sandboxes. The default runner ships with Claude Code, but you can build your own runner in any language — just implement the 5-step protocol.

## Runner Protocol

Every runner must implement a simple HTTP-based contract. The operator starts your container, waits for it to be ready, then sends it a task via HTTP.

### Step 1: Expose `POST /task` on Port 8888

Your runner must listen on port **8888** and accept task assignments:

```
POST /task
Content-Type: application/json

{
  "taskID": "my-task-abc123",
  "apiURL": "http://shepherd-shepherd-api.shepherd-system.svc.cluster.local:8081"
}
```

Respond with:

- **200** — task accepted
- **409** — already processing a task (one task per container)

The `apiURL` points to the **internal** API server (port 8081), which is only accessible from within the cluster.

You should also expose a health endpoint (e.g., `GET /healthz`) for the readiness probe. The operator waits for the readiness probe to pass before sending the task.

### Step 2: Fetch Task Data

Retrieve the full task details from the API:

```
GET {apiURL}/api/v1/tasks/{taskID}/data
```

Response:

```json
{
  "description": "Fix the login bug in auth.go",
  "context": "The user reported that...",
  "sourceURL": "https://github.com/org/repo/issues/42",
  "repo": {
    "url": "https://github.com/org/repo",
    "ref": "main"
  }
}
```

{{< callout type="warning" >}}
If the task is already in a terminal state, this endpoint returns **410 Gone**. Your runner should handle this gracefully and exit.
{{< /callout >}}

### Step 3: Fetch GitHub Token (Optional, One-Time)

If your runner needs to clone a private repo or push changes, request a scoped installation token:

```
GET {apiURL}/api/v1/tasks/{taskID}/token
```

Response:

```json
{
  "token": "ghs_xxxxxxxxxxxxxxxxxxxx",
  "expiresAt": "2026-03-01T12:00:00Z"
}
```

Use the token for Git operations:

```bash
git clone https://x-access-token:{token}@github.com/org/repo.git
```

{{< callout type="error" >}}
**One-time use only.** The token endpoint returns **409 Conflict** on the second call. Store the token when you first retrieve it. This prevents token replay attacks.
{{< /callout >}}

### Step 4: Stream Events (Optional)

Keep the web UI updated with real-time progress by streaming events:

```
POST {apiURL}/api/v1/tasks/{taskID}/events
Content-Type: application/json

{
  "events": [
    {
      "sequence": 1,
      "timestamp": "2026-03-01T10:00:01Z",
      "type": "thinking",
      "summary": "Analyzing the codebase structure"
    },
    {
      "sequence": 2,
      "timestamp": "2026-03-01T10:00:05Z",
      "type": "tool_call",
      "summary": "Reading auth.go",
      "tool": "read_file",
      "input": {"path": "pkg/auth/auth.go"}
    }
  ]
}
```

Event types:

| Type | Description |
|------|-------------|
| `thinking` | Runner is reasoning about the task |
| `tool_call` | Runner is invoking a tool (file read, shell command, etc.) |
| `tool_result` | Result of a tool invocation |
| `error` | Non-fatal error during execution |

**Sequence numbers** must be positive integers starting from 1, increasing monotonically. The API uses these for WebSocket fan-out ordering and reconnection (`?after=N`).

### Step 5: Report Completion

When the task is done (or fails), report the final status:

```
POST {apiURL}/api/v1/tasks/{taskID}/status
Content-Type: application/json

{
  "event": "completed",
  "message": "PR created successfully",
  "details": {
    "pr_url": "https://github.com/org/repo/pull/123"
  }
}
```

The `event` field must be one of:

| Event | Meaning |
|-------|---------|
| `started` | Runner has begun work (optional progress update) |
| `progress` | Intermediate progress update |
| `completed` | Task finished successfully |
| `failed` | Task failed |

On `completed`, include `details.pr_url` if a pull request was created. On `failed`, include `details.error` with the error message.

## Complete Examples

### Python Runner (Flask)

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

### Node.js Runner (Express)

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

## SandboxTemplate for Custom Runners

To use your custom runner, create a `SandboxTemplate` that points to your container image:

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: my-custom-runner
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
          image: ghcr.io/your-org/your-runner:latest
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
            - name: MY_API_KEY
              valueFrom:
                secretKeyRef:
                  name: my-runner-secrets
                  key: api-key
          resources:
            requests:
              memory: "1Gi"
              cpu: "500m"
            limits:
              memory: "2Gi"
              cpu: "1000m"
          volumeMounts:
            - name: workspace
              mountPath: /workspace
      volumes:
        - name: workspace
          emptyDir: {}
      restartPolicy: Never
```

Apply it:

```bash
kubectl apply -f my-sandbox-template.yaml -n shepherd-system
```

Then reference `my-custom-runner` as the `sandboxTemplateName` when creating tasks, or set it as the adapter's default via `SHEPHERD_DEFAULT_SANDBOX_TEMPLATE`.

## Key Constraints

| Constraint | Behavior |
|-----------|----------|
| **One task per container** | Return 409 if already processing a task. Each sandbox pod handles exactly one task. |
| **One-time token** | The token endpoint returns 409 on the second call. Store it on first use. |
| **Terminal task data** | `GET /data` returns 410 if the task is already completed or failed. |
| **Timeout** | The sandbox has a configurable timeout (default 30m). If your runner doesn't report completion in time, the task is marked as timed out and the pod is deleted. |
| **No outbound restrictions** | By default, runner pods can reach the internet. Use NetworkPolicies if you need to restrict this. |

## Next Steps

- [API Reference](../api-reference/) — full endpoint documentation with Swagger UI
- [Configuration Reference](../../setup/configuration/) — SandboxTemplate and RunnerSpec fields
- [Architecture Overview](../../architecture/overview/) — how runners fit into the task lifecycle
