# Shepherd Design Document

**Date:** 2026-01-25
**Status:** Draft

## Overview

Shepherd is an open-source background coding agent orchestrator. It receives tasks from various triggers (GitHub, Slack, CLI), runs AI coding agents in isolated Kubernetes jobs, and reports results back.

Inspired by:

- [Spotify's Background Coding Agent](https://engineering.atspotify.com/2025/11/spotifys-background-coding-agent-part-1)
- [Ramp's Background Agent](https://builders.ramp.com/post/why-we-built-our-background-agent)

## Goals

### MVP (Priority Order)

1. **Issue-driven development** - Trigger agent from GitHub issue/PR to work on a single repo
2. **Scheduled SRE tasks** - Daily/weekly automated analysis and fixes

### Future

3. **Fleet migrations** - Running the same transformation across many repos
2. **Interactive sessions** - Long-running dev environments with remote access

## Architecture

### Design Principles

- **K8s-native**: Uses CRDs for state, Jobs for execution. No external database for MVP.
- **Provider-agnostic core**: API and operator know nothing about GitHub. Adapters handle translation.
- **Callback-based status**: Triggers include a callback URL. API POSTs status updates there.
- **Single binary, multiple targets**: Loki/Mimir pattern - one binary, scale components independently.

### Components

Single binary with multiple targets:

| Target | Role | Scaling |
|--------|------|---------|
| `api` | REST API, CRD creation, job callbacks, adapter callbacks | Multiple replicas behind LB |
| `operator` | Watches CRDs, manages Jobs, updates status | 1 active (leader election) |
| `github-adapter` | GitHub App webhooks, posts comments | Multiple replicas |
| `all` | All in one process | Dev/testing only |

### GitHub Apps

Two separate GitHub Apps with distinct responsibilities:

| App | Purpose | Permissions | Used By |
|-----|---------|-------------|---------|
| Shepherd Trigger | Webhooks, read issues/PRs, write comments | Read issues, write comments | github-adapter |
| Shepherd Runner | Clone repos, push branches, create PRs | Read/write code, create PRs | K8s jobs (via init container) |

### Data Flow

```
1. Developer comments "@shepherd fix the null pointer"
         |
         v
2. GitHub webhook --> github-adapter (Trigger App)
         |
         v
3. Adapter extracts: repo_url, issue body, comments, author
   POST /api/v1/tasks to API with:
   - repo_url, task description, context
   - callback_url pointing back to adapter
   (no token - adapter doesn't have Runner credentials)
         |
         v
4. API validates request, creates AgentTask CRD
         |
         v
5. Operator sees new CRD, creates K8s Job with:
   - Init container: generates GitHub token from Runner App key
   - Main container: runner image with Claude Code
   - Env vars: repo URL, task, API callback URL
         |
         v
6. Job starts:
   - Init container generates token, writes to shared volume
   - Main container clones repo, Claude Code works on task
   - Hooks POST status updates to API
         |
         v
7. API updates CRD status, POSTs to adapter callback_url
   Adapter posts comment: "Working on it..."
         |
         v
8. Claude Code creates PR, job completes
         |
         v
9. Operator sees job done, updates CRD to completed
   API notifies adapter --> posts final comment with PR link
```

## CRD Specification

```yaml
apiVersion: shepherd.io/v1alpha1
kind: AgentTask
metadata:
  name: task-abc123
  namespace: shepherd
spec:
  repo:
    url: "https://github.com/org/repo.git"
  task:
    description: "Fix the null pointer exception in login.go"
    context: |
      Issue #123: Login fails intermittently
      User reported seeing NullPointerException...
  callback:
    url: "https://github-adapter.example.com/callback"
    secret: "webhook-secret-ref"
  runner:
    image: "shepherd-runner:latest"
    timeout: 30m
status:
  conditions:
    - type: Accepted
      status: "True"
      lastTransitionTime: "2026-01-25T10:00:00Z"
      reason: ValidationPassed
      message: "Task validated and accepted"
    - type: Running
      status: "True"
      lastTransitionTime: "2026-01-25T10:00:05Z"
      reason: JobStarted
      message: "Job task-abc123-job started"
    - type: Succeeded
      status: "False"
      lastTransitionTime: "2026-01-25T10:00:05Z"
      reason: InProgress
      message: ""
  result:
    prUrl: ""
    error: ""
  jobName: task-abc123-job
  events:
    - timestamp: "2026-01-25T10:00:05Z"
      message: "Cloning repository"
    - timestamp: "2026-01-25T10:01:00Z"
      message: "Working on task"
```

### Notes

- `spec` is immutable once created
- `status.conditions` follows K8s conventions
- `spec.task.context` may hit size limits with large issues (future: object storage)

## Job Specification

### Init Container

Generates GitHub installation token without exposing private key to main container:

```yaml
initContainers:
  - name: github-auth
    image: shepherd-init:latest
    env:
      - name: REPO_URL
        value: "https://github.com/org/repo.git"
    volumeMounts:
      - name: github-creds
        mountPath: /creds
      - name: runner-app-key
        mountPath: /secrets/runner-app-key
        readOnly: true
```

### Main Container

```yaml
containers:
  - name: runner
    image: shepherd-runner:latest
    env:
      - name: SHEPHERD_CALLBACK_URL
        value: "http://shepherd-api.shepherd.svc.cluster.local/api/v1/status"
      - name: SHEPHERD_TASK_ID
        value: "task-abc123"
      - name: SHEPHERD_REPO_URL
        value: "https://github.com/org/repo.git"
      - name: SHEPHERD_TASK_DESCRIPTION
        valueFrom:
          configMapKeyRef: ...
    volumeMounts:
      - name: github-creds
        mountPath: /creds
        readOnly: true
```

### Runner Image Contents

- Claude Code CLI (pre-installed)
- Git, gh CLI
- Go runtime (single runtime for MVP)
- Shepherd hooks (shell scripts that POST to API)
- Base CLAUDE.md with conventions

### Claude Code Hooks

```
~/.claude/hooks/
├── on_start.sh      # POST /status {event: "started"}
├── on_tool_call.sh  # Optional: log tool usage
└── on_complete.sh   # POST /status {event: "completed", pr_url: "..."}
```

## Callback Contract

### Job to API

Internal K8s networking, no auth required:

```
POST /api/v1/tasks/{task_id}/status
Content-Type: application/json

{
  "event": "started" | "progress" | "completed" | "failed",
  "message": "Cloning repository...",
  "details": {
    "pr_url": "https://github.com/org/repo/pull/123",
    "error": "Build failed: missing import"
  }
}
```

### API to Adapter

External callback with signature verification:

```
POST {callback_url}
Content-Type: application/json
X-Shepherd-Signature: sha256=...

{
  "task_id": "task-abc123",
  "event": "started" | "progress" | "completed" | "failed",
  "message": "Working on your request...",
  "details": { ... },
  "context": { ... }
}
```

## Repository Structure

```
shepherd/
├── cmd/
│   └── shepherd/           # single entrypoint
├── pkg/
│   ├── shepherd/           # target wiring, lifecycle
│   ├── api/                # REST API implementation
│   ├── operator/           # K8s operator logic
│   ├── adapters/
│   │   └── github/         # GitHub-specific logic
│   └── types/              # Shared types, interfaces
├── deploy/
│   └── helm/               # Helm charts for all components
├── images/
│   ├── runner/             # Runner Dockerfile
│   └── init/               # Init container Dockerfile
└── examples/
```

## Security Considerations

- **Runner App private key**: Only accessible to init container, never main container
- **Installation tokens**: Short-lived (1 hour), scoped to specific repos
- **Pre-approved runner images**: Users cannot specify arbitrary images
- **Internal callbacks**: Job to API uses cluster-internal networking
- **Signed callbacks**: API to adapter uses HMAC signature verification
- **Prompt injection**: How to make sure an LLM can't overshare based on prompt or perform melisous tasks.

## Future Considerations

- **Object storage for context**: Large issues may exceed CRD size limits
- **Multiple runtime images**: Language-specific pre-approved images
- **GitLab/other adapters**: Provider-agnostic core enables this
- **Scheduled tasks**: Cron-based triggers for SRE automation
- **Fleet migrations**: Batch orchestration across multiple repos
- **Interactive sessions**: Long-running environments with VS Code remote access
- **OpenCode/SDK integration**: More control over agent execution and metrics
