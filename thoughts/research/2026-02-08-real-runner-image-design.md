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
last_updated_note: "Added CC exit code behavior, max-turns/budget details, prompt design section, artifact-based success detection"
---

# Research: Real Runner Image Design

**Date**: 2026-02-08T12:00:00+01:00
**Researcher**: claude
**Git Commit**: 2f30bdba8ec52a6c27e3b0c058428b4f22a9f923
**Branch**: setup_github_app
**Repository**: NissesSenap/shepherd

## Research Question

We currently have `cmd/shepherd-runner/main.go` as a stub for e2e testing. It's time to create a real runner image focused on Go development, with Claude Code installed and configured to run in headless/yolo mode. The runner needs an entrypoint listening on port 8888, and upon receiving a task assignment, it should clone the repo, invoke Claude Code with task context, and create a PR. Additionally, we need a release process for production images (none exists today). Should CLAUDE.md instructions be baked into the image?

## Summary

The current runner stub at `cmd/shepherd-runner/main.go` is a ~70-line Go program that listens on `:8888`, accepts a `POST /task` with `{taskID, apiURL}`, and exits. The real runner replaces this with a container that has a full Go development toolchain, Claude Code (native binary via Bun - no Node.js needed), git, gh CLI, and a Go entrypoint that orchestrates the workflow: receive assignment, fetch task data from API, get GitHub token, clone repo, invoke `claude -p --dangerously-skip-permissions`, and report status back.

The entrypoint logic (HTTP server, API client, task lifecycle) lives in a reusable `pkg/runner/` package so future language-specific runner images can share the same contract implementation.

For the release process, the `shepherd` image uses `ko` via GHA. The `shepherd-runner-go` image uses a Dockerfile-based build. Both push to GHCR. The e2e stub (`shepherd-runner`) is not released - it's only used locally.

### Decisions Made

- **Registry**: GHCR (GitHub Container Registry)
- **Resources**: 4Gi memory for runner pods
- **Multi-arch**: linux/amd64 only for now, ARM64 later
- **Image versioning**: Tracks shepherd release version for now, independent versioning is a future problem
- **CLAUDE.md**: Baked into image for v1
- **Node.js**: Not needed - Claude Code has a native installer (Bun single-file executable)
- **Reusable package**: Entrypoint logic goes in `pkg/runner/` for reuse across runner images

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

This stub is only used for e2e testing via `make ko-build-kind` and is **not released** as a production image.

### 2. Task Assignment Protocol (Operator -> Runner)

**Operator sends** (`internal/controller/agenttask_controller.go:220-256`):

```http
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

### 4. Claude Code Installation: Native Binary (No Node.js)

Claude Code ships as a **standalone Bun single-file executable** via a native installer. The npm installation method is deprecated.

**Installation** (~10MB vs 200MB+ for node_modules):

```bash
curl -fsSL https://claude.ai/install.sh | bash
```

**Key facts**:

- No Node.js, npm, or Bun runtime required on the system
- Self-contained binary with embedded Bun runtime
- Uses WebKit's JavaScriptCore engine (not Node V8)
- Anthropic acquired Bun to support this distribution model
- Binary supports: macOS 13.0+, Ubuntu 20.04+, Debian 10+, Alpine 3.19+

**Deprecated npm method** (not used):

```bash
npm install -g @anthropic-ai/claude-code  # DEPRECATED - requires Node.js 18+
```

**Sources**:

- [Claude Code Setup Docs](https://code.claude.com/docs/en/setup)
- [Native Installer Announcement](https://www.threads.com/@claudeai/post/DQe2GgUAK6m/)
- [Bun Single-File Executable](https://x.com/jarredsumner/status/1943492457506697482)

### 5. What the Real Runner Container Needs

#### Required Software

| Component | Purpose | Notes |
| --- | --- | --- |
| Go 1.25+ | Compile, test, vet Go code | Must match project version |
| Claude Code | AI coding agent | Native installer, ~10MB |
| git | Clone repos, create branches | Core workflow |
| gh CLI | Create pull requests | `gh pr create` |
| bash | Shell for Claude Code tools | Required by CC's Bash tool |
| ca-certificates | HTTPS connections | API calls, git clone |
| make | Build tool | Many Go projects use Makefiles |

Node.js is **not required** - Claude Code uses its native Bun-based binary.

#### Required Configuration

| Item | Location | Purpose |
| --- | --- | --- |
| ANTHROPIC_API_KEY | K8s Secret -> env var | Claude Code auth |
| Git identity | `git config user.name/email` | Commit authorship |
| CLAUDE.md | Baked in image at `~/.claude/CLAUDE.md` | Coding instructions |
| Claude settings | `~/.claude/settings.json` | Permission config |

#### Container Image Base Options

| Base | Size | Pros | Cons |
| --- | --- | --- | --- |
| `golang:1.25-alpine` | ~300MB + deps | Small, has Go | musl libc (CGO issues) |
| `golang:1.25-bookworm` | ~800MB + deps | Full glibc, no compat issues | Larger |
| `debian:bookworm-slim` + Go | ~74MB + Go + deps | Most control over size | Must install Go manually |

**Recommendation**: `golang:1.25-bookworm` as base. The runner needs a full dev environment, not a minimal production image. CGO compatibility matters for some Go tools. Alpine's musl libc can cause subtle issues. The image size is less important since these are long-running sandbox pods, not frequently pulled.

### 6. Claude Code Automation Configuration

#### CLI Flags for Headless Mode

```bash
claude -p "task description" \
  --dangerously-skip-permissions \
  --output-format json \
  --max-turns 50 \
  --max-budget-usd 10.00
```

Key flags:

- `-p` / `--print`: Headless mode, execute and exit
- `--dangerously-skip-permissions`: Skip all permission prompts ("yolo mode")
- `--output-format json`: Structured output for parsing
- `--max-turns N`: Safety limit on agentic turns (see below)
- `--max-budget-usd N`: Spending cap per task (see below)

Session persistence is **kept enabled** (the default). CC writes session data to `~/.claude/` which can be useful if CC needs to reference earlier reasoning during a long task. The container user's home directory must be writable.

These values are **hardcoded for v1** but should become fields on the AgentTask CRD or API request in the future.

#### What max-turns Means

A "turn" is one iteration of the agentic loop: Claude reasons, uses one or more tools, observes results, decides next action. **One turn can include multiple tool calls** (e.g., reading 3 files in parallel is one turn with 3 tool uses). So `--max-turns 50` allows far more than 50 tool calls.

Typical turn counts:

- Simple fix (typo, single-line change): 1-3 turns
- Bug fix with tests: 5-10 turns
- Complex feature implementation: 10-30+ turns

#### What max-budget-usd Means

Limits total API token spend for the session. With Sonnet 4.5 ($3/M input, $15/M output):

- **$10 budget ~= 1.1M combined tokens**, roughly 100-500 turns
- Average developer usage is ~$6/day, so $10 is generous for a single focused task

#### Exit Code Behavior (Critical)

**`claude -p` exits 0 even when Claude decides it cannot do the task.** Exit 0 means the process ran without errors, not that the task was accomplished. Claude saying "I can't solve this" is a successful process run.

JSON output fields:

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "total_cost_usd": 0.34,
  "num_turns": 4,
  "result": "Response text here...",
  "session_id": "abc-123"
}
```

Even `subtype: "success"` + `is_error: false` does NOT mean the task was accomplished. **There is no reliable way to distinguish "Claude succeeded" from "Claude gave up" via exit code or JSON fields alone.**

This means the **Go entrypoint must check for concrete artifacts** to determine success:

- Did `git diff` show changes were made?
- Was a PR created? (parse CC output for PR URL, or run `gh pr list --head <branch>`)
- Did the build/tests pass?

Non-zero exit codes indicate process-level failures:

| Exit Code | Meaning |
| --- | --- |
| 0 | Process ran (task may or may not be done) |
| 1 | General failure (config, auth, environment) |
| non-zero | Max turns or budget limit hit (varies) |

#### Environment Variables

```bash
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

### 7. Claude Code Hooks for Status Reporting

Claude Code has a hooks system that fires shell commands at lifecycle events. This is how the runner reports completion back to the shepherd API **from within CC itself**, rather than only relying on the Go entrypoint wrapper.

#### Relevant Hook Events

| Event | When It Fires | Use Case |
| --- | --- | --- |
| `Stop` | When CC finishes responding | Report `completed` or `failed` to API |
| `PostToolUse` | After a tool call succeeds | Report `progress` during execution |
| `Notification` | When CC sends a notification | Forward CC notifications to API |
| `SessionEnd` | When session terminates | Final cleanup, report failure if unexpected exit |

#### Hook Configuration Format

Hooks are configured in `~/.claude/settings.json` (baked into image) or `.claude/settings.json` (per-repo):

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/shepherd-hook-notify.sh",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

#### Hook Input Data

Every hook receives JSON on stdin with:

```json
{
  "session_id": "abc123",
  "transcript_path": "/path/to/transcript.jsonl",
  "cwd": "/workspace/repo",
  "hook_event_name": "Stop",
  "stop_hook_active": false
}
```

The `Stop` hook has a `stop_hook_active` field to prevent infinite loops - the hook script must check this and exit early if true.

#### Notification Strategy

Two complementary approaches work together:

1. **CC hooks** (during execution): A `Stop` hook script reads the transcript, determines success/failure, and POSTs status to the shepherd API. This fires when CC naturally finishes its work.

2. **Go entrypoint** (after CC exits): The Go process that invoked `claude -p` checks the exit code and output, then reports final status. This is the safety net if hooks fail or CC crashes.

The hook script (`shepherd-hook-notify.sh`) is baked into the image and uses environment variables set by the Go entrypoint (`SHEPHERD_API_URL`, `SHEPHERD_TASK_ID`) to know where to report.

#### Sources

- [Hooks reference](https://code.claude.com/docs/en/hooks)
- [Automate workflows with hooks](https://code.claude.com/docs/en/hooks-guide)

### 8. CLAUDE.md: Baked Into Image

**Decision**: Bake CLAUDE.md into the image for v1.

- Consistent behavior across all tasks
- No network dependency at runtime
- Version-controlled with the image
- Per-repo `.claude/CLAUDE.md` in cloned repos can extend/override natively

Claude Code's configuration hierarchy (highest to lowest precedence):

1. Command-line arguments
2. Local project settings (`.claude/settings.local.json`)
3. Shared project settings (`.claude/settings.json`)
4. User settings (`~/.claude/settings.json`)

This means per-repo CLAUDE.md in cloned repos automatically layers on top of the baked-in global CLAUDE.md.

### 8. Container User and Filesystem

The container runs as a **non-root user** (UID 1000) with a writable home directory. Claude Code needs write access to `~/.claude/` for session data, settings cache, and hook state.

**Dockerfile user setup**:

```dockerfile
RUN useradd -m -u 1000 -s /bin/bash shepherd
USER shepherd
WORKDIR /home/shepherd
```

**Writable paths needed**:

| Path | Purpose |
| --- | --- |
| `/home/shepherd/.claude/` | CC session data, settings, hooks |
| `/home/shepherd/.config/gh/` | gh CLI auth state |
| `/home/shepherd/.gitconfig` | Git identity |
| `/workspace/` | Clone target directory |

The SandboxTemplate can mount a tmpfs or emptyDir at `/home/shepherd` if ephemeral storage is preferred. Since pods are single-task and destroyed after, persistent storage is not needed.

### 9. Reusable Entrypoint Package (`pkg/runner/`)

The HTTP server logic (listen `:8888`, `/healthz`, `POST /task`, single-task channel, signal handling, graceful shutdown) and API client logic (fetch data, fetch token, report status) are the same contract every runner image must implement regardless of language toolchain.

**Proposed package structure**:

```text
pkg/runner/
  server.go    - HTTP server, health check, task assignment handler
  client.go    - API client (fetch task data, fetch token, report status)
  runner.go    - TaskRunner interface that language-specific runners implement
```

**Interface design**:

```go
// TaskRunner is implemented by language-specific runners (Go, Python, etc.)
type TaskRunner interface {
    Run(ctx context.Context, task TaskData, token string) (*Result, error)
}

type TaskData struct {
    TaskID      string
    Description string
    Context     string
    SourceURL   string
    Repo        RepoInfo
}

type Result struct {
    PRURL string
}
```

Then `cmd/shepherd-runner-go/main.go` imports `pkg/runner` and provides a `GoRunner` that clones the repo and invokes Claude Code. When the runner moves to a separate repo, this package can be extracted as a Go module or vendored.

### 10. Entrypoint Design

The real runner entrypoint is a Go program using `pkg/runner`:

1. **Listen on :8888** with `/healthz` and `POST /task` (via `pkg/runner.Server`)
2. **On task assignment**:
   a. Report `started` status to API
   b. Fetch task data from API
   c. Fetch GitHub token from API
   d. Configure git with token: `https://x-access-token:{token}@github.com`
   e. Set `SHEPHERD_API_URL` and `SHEPHERD_TASK_ID` env vars (for CC hooks)
   f. Clone repo at specified ref into `/workspace/`
   g. Create working branch
   h. Write task context to a file for CC consumption
   i. Build the prompt (see section 11)
   j. Invoke `claude -p --dangerously-skip-permissions --output-format json` with the prompt
   k. **CC hooks** may report progress during execution (see section 7)
   l. Parse JSON output: check `is_error`, `subtype`, `total_cost_usd`, `num_turns`
   m. **Verify concrete artifacts**: `git diff --stat`, check for PR URL in output, `gh pr list --head <branch>`
   n. Report `completed` with PR URL if artifacts exist, or `failed` with CC's result text
3. **Exit** after task completion (pod lifecycle is single-task)

**Completion detection**: Since `claude -p` exits 0 even when Claude gives up, the Go entrypoint verifies success by checking for concrete artifacts (git changes, PR created) rather than trusting the exit code. The JSON output's `total_cost_usd` and `num_turns` are logged for observability.

**Dual notification**: CC's `Stop` hook can report to the API during execution. The Go entrypoint then checks whether the API already received a terminal status before reporting again, avoiding duplicate updates. If CC crashes (non-zero exit without hook firing), the Go entrypoint reports `failed`.

### 11. Prompt Design (Hardcoded for v1)

The prompt passed to `claude -p` is the most critical piece - it determines whether CC does useful work. For v1, the prompt is hardcoded in the Go runner and constructed from task data fetched from the API.

**Prompt components**:

1. **Task description** (from `TaskDataResponse.Description`) - what to do
2. **Task context** (from `TaskDataResponse.Context`) - background information, written to a file
3. **Source URL** (from `TaskDataResponse.SourceURL`) - link to the originating issue/PR
4. **Repo context** - the cloned repo with its own CLAUDE.md

**What the prompt should NOT do**:

- Tell CC how to use git (it knows)
- Tell CC how to create PRs (it knows)
- Repeat information already in the repo's CLAUDE.md

**What the prompt SHOULD do**:

- Clearly state the objective
- Reference the context file path
- Reference the source URL for additional context
- Set expectations: create a PR if you can solve it, report back if you can't
- Be explicit about scope: don't touch unrelated code

**Rough v1 prompt template** (needs iteration):

```text
You are working on a task assigned by Shepherd. Read the context file at
/workspace/task-context.md for background information.

Source: {sourceURL}

Task: {description}

Instructions:
- Review the task and context carefully
- If you believe you can solve this, implement a solution
- Create a feature branch, commit your changes, and create a pull request
- If you cannot solve this or the task is unclear, explain why in your output
- Stay focused on the task - do not make unrelated changes
```

**Future improvements** (not for v1):

- Per-source-type prompt templates (issue vs PR review vs fleet task)
- Prompt engineering based on task success/failure data
- User-customizable prompt templates via API or CRD field
- Chain-of-thought preamble to improve reasoning quality

### 12. Release Process Design

#### Current State

- **Existing workflows**: Only `lint.yml` and `test.yml` on PRs
- **No release automation** for any image
- **ko builds**: Local only via Makefile targets

#### Images to Release

| Image | Build Tool | Source |
| --- | --- | --- |
| `shepherd` | ko | `./cmd/shepherd/` (operator + API) |
| `shepherd-runner-go` | Docker | New Dockerfile (CC + Go toolchain) |

The e2e stub `shepherd-runner` is **not released** - it's only built locally via `make ko-build-kind`.

#### Release Workflow Design

**Trigger**: Push tags `v*`

**Registry**: GHCR (`ghcr.io/nissessenap/shepherd`)

**Architecture**: `linux/amd64` only (ARM64 added later)

**Job 1 - shepherd image (ko)**:

Follows the pattern from `gha-golang` ko-release workflow:

1. Checkout, setup-go (version from go.mod), setup-ko
2. Login to GHCR via `docker/login-action` with `GITHUB_TOKEN`
3. `ko build` with OCI labels, tag from git ref
4. Attest build provenance

**Job 2 - shepherd-runner-go image (Docker)**:

1. Checkout
2. Login to GHCR
3. `docker/build-push-action` with Dockerfile, OCI labels, tag from git ref
4. Attest build provenance

Both jobs run in parallel since they're independent.

### 13. POC Sandbox Patterns (Reference)

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
- Resources: 64Mi-128Mi memory, 50m-200m CPU (real runner needs 4Gi memory)
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

```text
Makefile:ko-build-local        -> ko build ./cmd/shepherd/        -> local Docker image
Makefile:ko-build-runner-local -> ko build ./cmd/shepherd-runner/ -> local Docker image
Makefile:ko-build-kind         -> tag both + kind load docker-image
```

### Proposed Image Build Flow (Release)

```text
git tag v* push -> GHA release.yaml triggers ->
  job 1: ko build shepherd          -> push to ghcr.io
  job 2: docker build runner-go     -> push to ghcr.io
```

### Runner Container Architecture

```text
┌───────────────────────────────────────────────────────┐
│ shepherd-runner-go container (USER: shepherd, UID 1000)│
│                                                       │
│ Base: golang:1.25-bookworm                            │
│ + Claude Code (native binary via Bun, ~10MB)          │
│ + git + gh CLI + bash + ca-certificates + make        │
│                                                       │
│ /home/shepherd/.claude/settings.json  (permissions)   │
│ /home/shepherd/.claude/CLAUDE.md      (instructions)  │
│ /usr/local/bin/shepherd-hook-notify.sh (Stop hook)    │
│                                                       │
│ Entrypoint: Go binary from pkg/runner/                │
│  -> HTTP :8888 (/healthz, POST /task)                 │
│  -> On task: fetch data, clone, run CC                │
│  -> CC Stop hook reports completion to API             │
│  -> Go entrypoint is safety net if hooks fail         │
└───────────────────────────────────────────────────────┘
```

### Reusable Package Architecture

```text
pkg/runner/
  server.go   ─── HTTP :8888, /healthz, POST /task, signal handling
  client.go   ─── API client: GET /data, GET /token, POST /status
  runner.go   ─── TaskRunner interface

cmd/shepherd-runner-go/
  main.go     ─── GoRunner: clone, invoke CC, create PR
                   imports pkg/runner

cmd/shepherd-runner/
  main.go     ─── E2e stub (unchanged, not released)
```

### Task Execution Flow

```text
Operator                    Runner (Go entrypoint)      API Server
   │                          │                            │
   ├─POST /task {taskID,apiURL}─>│                         │
   │                          │──POST /tasks/{id}/status──>│
   │                          │  {event:"started"}         │
   │                          │                            │
   │                          │──GET /tasks/{id}/data────>│
   │                          │<─{description,context,repo}│
   │                          │──GET /tasks/{id}/token───>│
   │                          │<─{token, expiresAt}───────│
   │                          │                            │
   │                          │ [clone, set env vars]      │
   │                          │ [invoke: claude -p ...]    │
   │                          │                            │
   │                          │   ┌─CC Stop hook─────────>│
   │                          │   │ POST /tasks/{id}/status│
   │                          │   │ {event:"completed",    │
   │                          │   │  details:{prURL:"..."}}│
   │                          │   └────────────────────────│
   │                          │                            │
   │                          │ [CC exits]                 │
   │                          │ [Go checks exit code]      │
   │                          │ [reports if hook missed]   │
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

1. **Token scoping for gh CLI**: The GitHub token from API is an installation token. Can `gh` use it directly via `GH_TOKEN` env var, or does it need `gh auth login`?
2. **Max turns / budget defaults**: Candidates: `--max-turns 30 --max-budget-usd 5.00` for simple tasks, `--max-turns 100 --max-budget-usd 15.00` for complex. Need real-world data. These should become CRD/API fields in the future.
3. **Error recovery**: If Claude Code fails mid-task (OOM, API error), should the entrypoint retry or just report failure? Leaning toward report failure - retries are a future feature.
4. **Separate repo**: When moving the runner image to a separate repo, how should the entrypoint Go code reference shared types (TaskAssignment, API client)? Likely extract `pkg/runner/` as a Go module.
5. **Auto-updater**: Claude Code has an auto-updater. Disabled via `DISABLE_AUTOUPDATER=1`, but should we pin a specific version in the Dockerfile for reproducibility?
6. **Hook idempotency**: The `Stop` hook and Go entrypoint both report status. The API's status handler must handle duplicate terminal status updates gracefully (idempotent transitions).
7. **Prompt engineering**: The v1 prompt template (section 11) is a starting point. It needs iteration based on real task outcomes. How do we measure prompt effectiveness? Should we log CC's `result` text for analysis?
8. **Success detection heuristics**: Beyond `git diff` and PR creation, what else should the Go entrypoint check? Test results? Lint pass? This determines the boundary between "CC made changes" and "CC made good changes."
