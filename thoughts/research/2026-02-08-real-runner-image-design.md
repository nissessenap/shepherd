---
date: 2026-02-08T12:00:00+01:00
researcher: claude
git_commit: 2f30bdba8ec52a6c27e3b0c058428b4f22a9f923
branch: setup_github_app
repository: NissesSenap/shepherd
topic: "Real runner image design: Claude Code container for Go development tasks"
tags: [research, codebase, runner, container, claude-code, release, ci-cd]
status: complete
last_updated: 2026-02-08
last_updated_by: claude
---

# Research: Real Runner Image Design

**Date**: 2026-02-08T12:00:00+01:00
**Researcher**: claude
**Git Commit**: 2f30bdba8ec52a6c27e3b0c058428b4f22a9f923
**Branch**: setup_github_app
**Repository**: NissesSenap/shepherd

## Research Question

We currently have `cmd/shepherd-runner/main.go` as a stub for e2e testing. It's time to create a real runner image focused on Go development, with Claude Code installed and configured to run in headless/yolo mode. The runner needs an entrypoint listening on port 8888, and upon receiving a task assignment, it should clone the repo, invoke Claude Code with task context, and create a PR. Additionally, we need a release process for all images (none exists today). Should CLAUDE.md instructions be baked into the image?

## Summary

The current runner stub at `cmd/shepherd-runner/main.go` is a ~70-line Go program that listens on `:8888`, accepts a `POST /task` with `{taskID, apiURL}`, and exits. The real runner needs to replace this with a container that has a full Go development toolchain, Claude Code (npm package), git, gh CLI, and a Go entrypoint that orchestrates the workflow: receive assignment, fetch task data from API, get GitHub token, clone repo, invoke `claude -p --dangerously-skip-permissions`, and report status back.

For the release process, the shepherd and shepherd-runner Go images can use `ko` via a reusable GHA workflow (modeled on the `ko-release.yml` pattern from `gha-golang`). The new Claude Code runner image needs a Dockerfile-based build since it's not a pure Go binary.

## Detailed Findings

### 1. Current Runner Stub

**File**: `cmd/shepherd-runner/main.go`

The stub implements the contract the operator expects:
- Listens on `:8888`
- `GET /healthz` returns 200 OK
- `POST /task` accepts `TaskAssignment{TaskID, APIURL}` JSON
- Buffered channel (cap 1) ensures single-task-per-pod
- Returns 409 Conflict if task already assigned
- Exits after receiving assignment (line 39: "stub exiting")

Tests in `cmd/shepherd-runner/main_test.go` cover: health check, task acceptance, duplicate rejection, invalid JSON.

### 2. Task Assignment Protocol (Operator -> Runner)

**Operator sends** (`internal/controller/agenttask_controller.go:220-256`):
```
POST http://{sandbox.Status.ServiceFQDN}:8888/task
Content-Type: application/json

{"taskID": "my-task", "apiURL": "http://shepherd-api:8080"}
```

Port 8888 is hardcoded in `agenttask_controller.go:232`.

**Expected responses**:
- `200 OK` or `202 Accepted` - task accepted
- `409 Conflict` - already assigned (idempotent, treated as success)

### 3. Runner's Expected Workflow (from API types)

After receiving the assignment, the runner should:

1. **Fetch task data**: `GET {apiURL}/api/v1/tasks/{taskID}/data`
   - Returns `TaskDataResponse`: description, context (decompressed), sourceURL, repo (url + ref)
   - 404 = not found, 410 = terminal (skip)

2. **Fetch GitHub token**: `GET {apiURL}/api/v1/tasks/{taskID}/token`
   - Returns `TokenResponse`: token, expiresAt
   - One-time use (replay protection via `TokenIssued` status field)
   - 409 = already issued

3. **Execute the task**: Clone repo, run coding agent, create PR

4. **Report status**: `POST {apiURL}/api/v1/tasks/{taskID}/status`
   - `StatusUpdateRequest`: event (started/progress/completed/failed), message, details
   - For completed: `details.prURL` with the PR URL
   - For failed: `message` with error description

### 4. What the Real Runner Container Needs

#### Required Software
| Component | Purpose | Notes |
|-----------|---------|-------|
| Go 1.25+ | Compile, test, vet Go code | Must match project version |
| Node.js 18+ | Claude Code runtime | npm package requirement |
| Claude Code | AI coding agent | `npm install -g @anthropic-ai/claude-code` |
| git | Clone repos, create branches | Core workflow |
| gh CLI | Create pull requests | `gh pr create` |
| bash | Shell for Claude Code tools | Required by CC's Bash tool |
| ca-certificates | HTTPS connections | API calls, git clone |

#### Required Configuration
| Item | Location | Purpose |
|------|----------|---------|
| ANTHROPIC_API_KEY | K8s Secret -> env var | Claude Code auth |
| Git identity | `git config user.name/email` | Commit authorship |
| CLAUDE.md | Baked in image OR fetched at runtime | Coding instructions |
| Claude settings | `~/.claude/settings.json` | Permission config |

#### Container Image Base Options

| Base | Size | Pros | Cons |
|------|------|------|------|
| `golang:1.25-alpine` | ~300MB + deps | Small, has Go | musl libc (CGO issues) |
| `golang:1.25-bookworm` | ~800MB + deps | Full glibc, no compat issues | Larger |
| `debian:bookworm-slim` + Go | ~74MB + Go + deps | Most control over size | Must install Go manually |
| Multi-stage | Varies | Optimal size | More complex build |

**Recommendation**: `golang:1.25-bookworm` as base. The runner needs a full dev environment, not a minimal production image. CGO compatibility matters for some Go tools. Alpine's musl libc can cause subtle issues.

### 5. Claude Code Automation Configuration

#### CLI Flags for Headless Mode
```bash
claude -p "task description" \
  --dangerously-skip-permissions \
  --output-format json \
  --max-turns 50 \
  --max-budget-usd 10.00 \
  --no-session-persistence
```

Key flags:
- `-p` / `--print`: Headless mode, execute and exit
- `--dangerously-skip-permissions`: Skip all permission prompts ("yolo mode")
- `--output-format json`: Structured output for parsing
- `--max-turns N`: Safety limit on agentic turns
- `--max-budget-usd N`: Spending cap per task
- `--no-session-persistence`: Don't save session to disk

#### Environment Variables
```
ANTHROPIC_API_KEY=sk-ant-...    # Required
DISABLE_AUTOUPDATER=1           # Don't auto-update in container
CI=true                         # Signal non-interactive environment
```

#### Settings File (`~/.claude/settings.json`)
```json
{
  "permissions": {
    "allow": ["Bash(*)", "Read(*)", "Edit(*)", "Write(*)", "Glob(*)", "Grep(*)"],
    "defaultMode": "acceptEdits"
  }
}
```

### 6. CLAUDE.md: Bake In vs. Fetch at Runtime

**Bake in (recommended for v1)**:
- Pros: Consistent behavior, no network dependency, version-controlled with image
- Cons: Requires image rebuild to update instructions
- Implementation: Copy into image at build time, place at `~/.claude/CLAUDE.md`

**Fetch at runtime**:
- Pros: Dynamic updates without rebuild
- Cons: Adds network dependency, latency, failure mode
- Implementation: Entrypoint fetches from API or configmap before invoking CC

**Hybrid approach** (future):
- Bake base instructions in image
- Allow per-repo `.claude/CLAUDE.md` in the cloned repo to override/extend
- Claude Code already supports this hierarchy natively

### 7. Entrypoint Design

The real runner entrypoint needs to be a Go program (not just a shell script) to maintain the HTTP contract with the operator. It should:

1. **Listen on :8888** with `/healthz` and `POST /task` (same as stub)
2. **On task assignment**:
   a. Report `started` status to API
   b. Fetch task data from API
   c. Fetch GitHub token from API
   d. Configure git with token: `https://x-access-token:{token}@github.com`
   e. Clone repo at specified ref
   f. Create working branch
   g. Write task context to a file for CC consumption
   h. Invoke Claude Code in headless mode with task description
   i. If CC made changes: commit, push, create PR via `gh`
   j. Report `completed` with PR URL, or `failed` with error
3. **Exit** after task completion (pod lifecycle is single-task)

### 8. Release Process Design

#### Current State
- **Existing workflows**: Only `lint.yml` and `test.yml` on PRs
- **No release automation** for any image
- **ko builds**: Local only via Makefile targets

#### Images to Release

| Image | Build Tool | Source |
|-------|-----------|--------|
| `shepherd` | ko | `./cmd/shepherd/` (operator + API) |
| `shepherd-runner` | ko | `./cmd/shepherd-runner/` (stub) |
| `shepherd-runner-go` | Docker | New Dockerfile (CC + Go toolchain) |

#### Release Workflow Pattern (from reference repos)

The `gha-golang` ko-release workflow provides the pattern:
- Trigger: push tags `v*`
- Steps: checkout, setup-go, setup-ko, auth to registry, `ko build` with OCI labels, attest provenance
- Outputs: image ref, digest

For shepherd, we need a release workflow that:
1. Triggers on `v*` tags
2. Builds `shepherd` and `shepherd-runner` via ko (can use reusable workflow or inline)
3. Builds `shepherd-runner-go` via Docker build+push
4. Pushes all to a container registry (GHCR or GCP Artifact Registry)
5. Attests build provenance

#### Registry Choice

The reference repos use GCP Artifact Registry with Workload Identity Federation. For an open-source project, **GHCR (GitHub Container Registry)** is simpler:
- No external auth setup needed
- `GITHUB_TOKEN` provides push access
- Free for public repos

### 9. POC Sandbox Patterns (Reference)

The POC at `poc/sandbox/cmd/entrypoint/main.go` demonstrates the pattern the real runner should follow:
- HTTP server on `:8888` with `/healthz` and `POST /task`
- Buffered channel (cap 1) for single-task enforcement
- Shutdown HTTP server before starting work
- Signal handling for SIGTERM/SIGINT
- 5-second graceful shutdown timeout
- Exit 0 on success, exit 1 on failure

The SandboxTemplate at `poc/sandbox/manifests/sandbox-template.yaml` shows:
- Readiness probe: HTTP GET `/healthz:8888`, 2s initial delay, 5s period
- Security: run as UID 1000, non-root
- Resources: 64Mi-128Mi memory, 50m-200m CPU (will need more for CC)
- Restart policy: Never

## Code References

- `cmd/shepherd-runner/main.go` - Current stub runner implementation
- `cmd/shepherd-runner/main_test.go` - Stub runner tests
- `internal/controller/agenttask_controller.go:53-57` - TaskAssignment struct
- `internal/controller/agenttask_controller.go:220-256` - assignTask() HTTP POST to runner
- `pkg/api/types.go:79-84` - StatusUpdateRequest (runner -> API)
- `pkg/api/types.go:94-100` - TaskDataResponse (API -> runner)
- `pkg/api/types.go:102-106` - TokenResponse (API -> runner)
- `pkg/api/handler_data.go:33-72` - getTaskData handler
- `poc/sandbox/cmd/entrypoint/main.go` - POC entrypoint pattern
- `poc/sandbox/manifests/sandbox-template.yaml` - POC SandboxTemplate
- `.github/workflows/lint.yml` - Existing lint CI
- `.github/workflows/test.yml` - Existing test CI
- `Makefile:105-125` - ko build targets

## Architecture Documentation

### Current Image Build Flow
```
Makefile:ko-build-local     -> ko build ./cmd/shepherd/        -> local Docker image
Makefile:ko-build-runner-local -> ko build ./cmd/shepherd-runner/ -> local Docker image
Makefile:ko-build-kind      -> tag both + kind load docker-image
```

### Proposed Image Build Flow (Release)
```
git tag v* push -> GHA release.yaml triggers ->
  job 1: ko build shepherd       -> push to registry
  job 2: ko build shepherd-runner -> push to registry
  job 3: docker build shepherd-runner-go -> push to registry
```

### Runner Container Architecture
```
┌─────────────────────────────────────────────┐
│ shepherd-runner-go container                │
│                                             │
│ Base: golang:1.25-bookworm                  │
│ + Node.js 18 + npm                          │
│ + Claude Code (@anthropic-ai/claude-code)   │
│ + git + gh CLI + bash + ca-certificates     │
│                                             │
│ /root/.claude/settings.json  (permissions)  │
│ /root/.claude/CLAUDE.md      (instructions) │
│                                             │
│ Entrypoint: /ko-app/shepherd-runner-go      │
│  -> HTTP :8888 (/healthz, POST /task)       │
│  -> On task: fetch data, clone, run CC, PR  │
└─────────────────────────────────────────────┘
```

### Task Execution Flow
```
Operator                    Runner                      API Server
   │                          │                            │
   ├─POST /task {taskID,apiURL}─>│                         │
   │                          │──GET /tasks/{id}/data────>│
   │                          │<─{description,context,repo}│
   │                          │──GET /tasks/{id}/token───>│
   │                          │<─{token, expiresAt}───────│
   │                          │                            │
   │                          │──POST /tasks/{id}/status──>│
   │                          │  {event:"started"}         │
   │                          │                            │
   │                          │ [clone, branch, run CC]    │
   │                          │                            │
   │                          │──POST /tasks/{id}/status──>│
   │                          │  {event:"completed",       │
   │                          │   details:{prURL:"..."}}   │
   │                          │                            │
   │                          │ [exit 0]                   │
```

## Historical Context (from thoughts/)

- `thoughts/plans/2026-01-31-poc-sandbox-entrypoint.md` - Original POC design for sandbox entrypoint pattern
- `thoughts/plans/2026-02-05-agent-sandbox-lifecycle-migration.md` - Migration to agent-sandbox v0.1.1 lifecycle
- `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md` - Sandbox architecture decisions
- `thoughts/research/2026-01-31-poc-sandbox-learnings.md` - Learnings from POC implementation
- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` - Session management research

## Open Questions

1. **Registry choice**: GHCR (simple, free for public) vs GCP Artifact Registry (matches reference repos)?
2. **Multi-arch**: Build for `linux/amd64` only or also `linux/arm64`? CC + Go + Node.js on ARM64 should work but increases build time.
3. **Runner resource requirements**: Claude Code + Go toolchain needs significantly more than the POC's 128Mi/200m CPU. Likely 2-4Gi memory, 1-2 CPU minimum.
4. **Token scoping for gh CLI**: The GitHub token from API is an installation token. Can `gh` use it directly via `GH_TOKEN` env var, or does it need `gh auth login`?
5. **Working directory**: Should the runner clone into `/workspace` or `/tmp/work`? Needs to be writable by the container user.
6. **Image versioning**: Should the CC runner image version track the shepherd release version, or be versioned independently?
7. **Max turns / budget**: What are reasonable defaults for `--max-turns` and `--max-budget-usd` for Go tasks?
8. **Error recovery**: If Claude Code fails mid-task (OOM, API error), should the entrypoint retry or just report failure?
9. **Separate repo**: When moving the runner image to a separate repo, how should the entrypoint Go code reference shared types (TaskAssignment, API client)?
