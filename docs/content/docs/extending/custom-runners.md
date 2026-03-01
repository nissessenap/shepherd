---
title: Building Custom Runners
weight: 1
---

Shepherd runners are containers that execute tasks inside ephemeral sandboxes. The default runner ships with Claude Code, but you can customize it or build your own.

There are two approaches, from easiest to most flexible:

1. **Extend the default runner** — copy the existing `shepherd-runner` Go binary into a new container image with your tools. No code to write.
2. **Build from scratch** — implement the 5-step runner protocol in any language.

## Approach 1: Extend the Default Runner (Recommended)

The easiest way to create a custom runner is to reuse the existing `shepherd-runner` binary. It's a statically-compiled Go binary that handles the entire protocol — task acceptance, API communication, Git cloning, Claude Code invocation, event streaming, and status reporting. Since it's Go, you don't need a Go runtime in your final image — just copy the binary.

All you need is a Dockerfile that starts from whatever base image has your tools and copies the runner binary in.

### Example: Python + Runner

```dockerfile
FROM ghcr.io/nissessenap/shepherd-runner:latest AS runner

FROM python:3.13-slim

# Install system tools the runner needs
RUN apt-get update && apt-get install -y --no-install-recommends \
    git bash ca-certificates jq curl make \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code native binary (no Node.js required)
RUN curl -fsSL https://claude.ai/install.sh | bash

# Copy the runner binary from the official image
COPY --from=runner /usr/local/bin/shepherd-runner /usr/local/bin/shepherd-runner

# Install your Python dependencies
COPY requirements.txt /tmp/
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Set up non-root user
RUN useradd -m -u 1000 shepherd
USER shepherd
WORKDIR /workspace

ENTRYPOINT ["shepherd-runner"]
```

### Example: Node.js + Runner

```dockerfile
FROM ghcr.io/nissessenap/shepherd-runner:latest AS runner

FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git bash ca-certificates jq curl make \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://claude.ai/install.sh | bash

COPY --from=runner /usr/local/bin/shepherd-runner /usr/local/bin/shepherd-runner

# Install your Node.js dependencies
COPY package*.json /tmp/
RUN cd /tmp && npm ci --production && mkdir -p /app && mv node_modules /app/

RUN useradd -m -u 1000 shepherd
USER shepherd
WORKDIR /workspace

ENTRYPOINT ["shepherd-runner"]
```

### Why This Works

The `shepherd-runner` binary is self-contained. It:

- Listens on port 8888 and accepts tasks from the operator
- Fetches task data and GitHub tokens from the API
- Clones the repo and creates a feature branch
- Invokes Claude Code in headless mode with the task description
- Streams events to the web UI in real time
- Reports completion or failure back to the API

By placing it in a container that also has Python, Node.js, or any other runtime, Claude Code can use those tools when working on the task. You get the full Shepherd integration for free — no protocol reimplementation needed.

### Building and Using

Build your image and push it:

```bash
docker build -t ghcr.io/your-org/shepherd-runner-python:latest .
docker push ghcr.io/your-org/shepherd-runner-python:latest
```

Then create a SandboxTemplate that references it (see [SandboxTemplate for Custom Runners](#sandboxtemplate-for-custom-runners) below).

---

## Approach 2: Build From Scratch

If you need full control over the runner behavior — for example, to use a different AI model, skip Claude Code entirely, or implement custom task logic — you can build a runner from scratch in any language by implementing the 5-step protocol.

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

### Complete Examples

#### Python Runner (Flask)

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

#### Node.js Runner (Express)

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
