# Real Runner Image Implementation Plan

## Overview

Implement the production runner image (`shepherd-runner`) that replaces the e2e stub with a container capable of executing coding tasks via Claude Code. The runner receives task assignments from the operator, fetches task data and GitHub tokens from the API, clones the repo, invokes Claude Code in headless mode, and reports status back via both a CC Stop hook and a Go entrypoint fallback.

## Current State Analysis

- **Stub runner** at `cmd/shepherd-runner/main.go` (72 lines): HTTP server on `:8888`, accepts `POST /task`, exits immediately. Used for e2e testing only.
- **No `pkg/runner/` package** exists - all runner logic is inline in the stub.
- **No Dockerfiles** in the project - everything uses `ko` for image builds.
- **No release workflow** - only `lint.yml` and `test.yml` on PRs.
- **API types** in `pkg/api/types.go` define the contract: `TaskDataResponse`, `TokenResponse`, `StatusUpdateRequest`.
- **Operator** at `internal/controller/agenttask_controller.go` POSTs `{taskID, apiURL}` to `http://{sandboxFQDN}:8888/task`.

### Key Discoveries

- Sandbox builder (`internal/controller/sandbox_builder.go`) uses `SandboxTemplateName` from the task spec - the runner image is configured in the SandboxTemplate CRD, not the operator code
- Status handler (`pkg/api/handler_status.go:78-97`) already handles terminal event deduplication via `Notified` condition - safe for both hook and entrypoint to report
- Token endpoint (`pkg/api/handler_token.go:61-64`) is one-time-use with `TokenIssued` flag - runner must handle 409 Conflict gracefully on retry
- The API uses dual ports: 8080 (public/adapter) and 8081 (internal/runner). Runner talks to 8081

## Desired End State

A production-ready runner container image (`shepherd-runner`) that:

1. Listens on `:8888` with `/healthz` and `POST /task` endpoints
2. On task assignment: fetches task data, fetches GitHub token, clones repo, runs Claude Code
3. Reports `started` status before CC invocation (Go entrypoint)
4. Reports `completed`/`failed` status after CC finishes (CC Stop hook via `shepherd-runner hook` subcommand)
5. Reports `failed` status if CC crashes/exits non-zero (Go entrypoint fallback)
6. Exits after task completion (single-task pod lifecycle)

### Verification

- `make test` passes with new `pkg/runner/` and `cmd/shepherd-runner/` tests
- `make lint-fix` passes
- `docker build -f build/runner/Dockerfile .` produces a working image
- GHA release workflow triggers on `v*` tags and pushes to GHCR

## What We're NOT Doing

- **No real-time progress streaming** - CC Stop hook fires once at end, no `PostToolUse` hooks
- **No prompt engineering iteration** - hardcoded v1 prompt, iteration happens later
- **No ARM64 support** - linux/amd64 only
- **No independent versioning** - runner image tracks shepherd release version
- **No changes to the operator or API server** - runner implements the existing contract
- **No prompt injection sanitization** - task description is interpolated into CC prompt; trusted because it flows through our adapter/API first (known limitation)
- **No git credential hardening** - token embedded in clone URL, stored in `.git/config` as remote origin; acceptable for single-task pods that get deleted after completion (TODO: consider `GIT_ASKPASS` in future)
- **No token retry on 409** - if runner crashes after fetching token but before using it, the token is burned and the task fails permanently; operator would need to create a new AgentTask

## Implementation Approach

The work is split into 7 phases, each producing compilable, testable code:

0. **Move e2e stub** - Relocate `cmd/shepherd-runner/` to `test/e2e/testrunner/`
1. **`pkg/runner/`** - Shared HTTP server, API client, and interfaces
2. **`cmd/shepherd-runner/`** - CLI with `serve` and `hook` subcommands
3. **Go runner logic** - Clone, invoke CC, parse output
4. **Hook subcommand** - CC Stop hook handler that reports status
5. **Dockerfile + image config** - CLAUDE.md, settings.json, Dockerfile
6. **GHA release workflow** - Build and push both images to GHCR

---

## Phase 0: Move E2E Stub Runner

### Overview

The current `cmd/shepherd-runner/` is a minimal stub (72 lines) used only for e2e testing. It doesn't belong in `cmd/` since that directory is for production binaries. Move it to `test/e2e/testrunner/` to free up the `cmd/shepherd-runner/` path for the real production runner.

### Rationale

- `cmd/` is for production binaries that ship to users — the stub is test infrastructure
- Go projects don't suffix binaries with `-go` (YAGNI — no other language runners exist yet)
- If a Python/Rust runner is added later, image tags or repo names handle differentiation
- Follows Kubernetes project conventions (`test/e2e/` for e2e test binaries)

### Changes Required

#### 1. Move stub runner

```bash
git mv cmd/shepherd-runner test/e2e/testrunner
```

#### 2. Update references

- **Makefile targets** (all reference `./cmd/shepherd-runner/`):
  - `ko-build-runner-local` (line ~122): Change build path to `./test/e2e/testrunner/`
  - `build-smoke` (line ~127): Depends on `ko-build-runner-local` — no path change needed, just verify it still works
  - `ko-build-kind` (line ~132): Uses `$(RUNNER_IMG)` variable — no change needed
- **`.ko.yaml`** (if exists): Update import path
- **e2e tests**: Update any references to the old binary path
- **Operator code**: The operator doesn't reference the binary path directly (it uses the SandboxTemplate image) — no changes needed

#### 3. Verify

Ensure the stub is still buildable from its new location:

```bash
go build ./test/e2e/testrunner/
```

### Success Criteria

#### Automated Verification

- [ ] `go build ./test/e2e/testrunner/` compiles
- [ ] `make lint-fix` passes
- [ ] `make test` passes (all existing tests still pass)
- [ ] No references to `cmd/shepherd-runner` remain (except in git history)

---

## Phase 1: `pkg/runner/` - Shared Runner Package

### Overview

Create the reusable runner package with HTTP server, API client, and the `TaskRunner` interface. This package is shared across language-specific runners.

### Changes Required

#### 1. Runner types and interface

**File**: `pkg/runner/runner.go`

```go
package runner

import "context"

// TaskAssignment is the payload sent by the operator when assigning a task.
type TaskAssignment struct {
    TaskID string `json:"taskID"`
    APIURL string `json:"apiURL"`
}

// TaskData holds the fetched task information for the runner.
type TaskData struct {
    TaskID      string
    APIURL      string
    Description string
    Context     string
    SourceURL   string
    RepoURL     string
    RepoRef     string
}

// Result holds the outcome of a task execution.
type Result struct {
    Success bool
    PRURL   string
    Message string
}

// TaskRunner is implemented by language-specific runners.
type TaskRunner interface {
    Run(ctx context.Context, task TaskData, token string) (*Result, error)
}
```

#### 2. API client interface

**File**: `pkg/runner/client.go`

Define an `APIClient` interface for testability (follows the `TokenProvider` pattern from `pkg/api/github_token.go`):

```go
// APIClient communicates with the shepherd API server.
type APIClient interface {
    FetchTaskData(ctx context.Context, taskID string) (*TaskData, error)
    FetchToken(ctx context.Context, taskID string) (token string, expiresAt time.Time, err error)
    ReportStatus(ctx context.Context, taskID string, event, message string, details map[string]any) error
}
```

The concrete `Client` struct implements `APIClient`. Tests inject a mock.

#### 3. HTTP server

**File**: `pkg/runner/server.go`

Implements the `:8888` HTTP server with `/healthz` and `POST /task`. Follows the POC pattern: shut down HTTP server before starting work.

```go
// Server handles task assignment and delegates to a TaskRunner.
type Server struct {
    runner   TaskRunner
    client   APIClient  // interface, not concrete *Client
    addr     string
    logger   logr.Logger
}

// NewServer creates a runner server.
func NewServer(runner TaskRunner, client APIClient, opts ...ServerOption) *Server

// Serve starts the HTTP server and blocks until task is complete or context is cancelled.
func (s *Server) Serve(ctx context.Context) error
```

The `Serve` method:

1. Starts HTTP server on `:8888`
2. Waits for task assignment on channel (or context cancellation)
3. Shuts down HTTP server with 5s timeout (prevents second assignment)
4. Reports `started` status
5. Fetches task data
6. Fetches GitHub token (409 Conflict = fatal error, task cannot be retried — see "What We're NOT Doing")
7. Calls `runner.Run(ctx, taskData, token)`
8. If `runner.Run` returns error: reports `failed` status (fallback - hook may have already reported)
9. Exits

#### 4. Concrete API client

**File**: `pkg/runner/client.go` (same file as interface)

Concrete implementation of `APIClient` for the shepherd API (internal port 8081).

```go
// Client implements APIClient for the shepherd API server.
type Client struct {
    baseURL    string
    httpClient *http.Client
    logger     logr.Logger
}

// NewClient creates an API client.
func NewClient(baseURL string, opts ...ClientOption) *Client

// FetchTaskData retrieves task details from the API.
func (c *Client) FetchTaskData(ctx context.Context, taskID string) (*TaskData, error)

// FetchToken retrieves a GitHub installation token.
// Returns a fatal error on 409 Conflict (token already issued, non-retriable).
func (c *Client) FetchToken(ctx context.Context, taskID string) (token string, expiresAt time.Time, err error)

// ReportStatus sends a status update to the API.
func (c *Client) ReportStatus(ctx context.Context, taskID string, event, message string, details map[string]any) error
```

#### 5. Tests

**File**: `pkg/runner/server_test.go`

Tests for the HTTP endpoints:

- `TestHealthEndpoint` - GET /healthz returns 200
- `TestTaskAccepted` - POST /task with valid JSON returns 200
- `TestTaskRejectsSecond` - Second POST returns 409
- `TestTaskInvalidJSON` - Bad JSON returns 400

**File**: `pkg/runner/client_test.go`

Tests for the API client using `httptest.Server`:

- `TestFetchTaskData` - Happy path, 404, 410
- `TestFetchToken` - Happy path, 409 (already issued)
- `TestReportStatus` - Happy path, validates request body

### Success Criteria

#### Automated Verification

- [ ] `go build ./pkg/runner/` compiles
- [ ] `go test ./pkg/runner/` passes
- [ ] `make lint-fix` passes
- [ ] `make test` passes (all existing tests still pass)

---

## Phase 2: `cmd/shepherd-runner/` - CLI Entry Point

### Overview

Create the runner binary with Kong CLI, two subcommands (`serve` and `hook`), and wire them up.

### Changes Required

#### 1. Main entry point

**File**: `cmd/shepherd-runner/main.go`

```go
package main

import (
    "fmt"
    "os"

    "github.com/alecthomas/kong"
)

type CLI struct {
    Serve ServeCmd `cmd:"" default:"1" help:"Run the runner HTTP server (default)"`
    Hook  HookCmd  `cmd:"" help:"Handle Claude Code Stop hook"`
}

func main() {
    cli := CLI{}
    ctx := kong.Parse(&cli,
        kong.Name("shepherd-runner"),
        kong.Description("Shepherd runner for coding tasks"),
    )
    if err := ctx.Run(); err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
}
```

#### 2. Serve subcommand

**File**: `cmd/shepherd-runner/serve.go`

```go
type ServeCmd struct {
    Addr string `help:"Listen address" default:":8888" env:"SHEPHERD_RUNNER_ADDR"`
}

func (c *ServeCmd) Run() error {
    // Creates GoRunner, creates runner.Server, calls server.Serve(ctx)
}
```

#### 3. Hook subcommand (stub for now)

**File**: `cmd/shepherd-runner/hook.go`

```go
type HookCmd struct{}

func (c *HookCmd) Run() error {
    // Stub - implemented in Phase 4
    return fmt.Errorf("not implemented")
}
```

### Success Criteria

#### Automated Verification

- [ ] `go build ./cmd/shepherd-runner/` compiles
- [ ] `make lint-fix` passes
- [ ] `make test` passes

---

## Phase 3: Go Runner Logic

### Overview

Implement the `GoRunner` that clones the repo, configures git, invokes Claude Code, and parses the result.

### Changes Required

#### 1. Go runner implementation

**File**: `cmd/shepherd-runner/gorunner.go`

```go
// GoRunner implements runner.TaskRunner for Go development tasks.
type GoRunner struct {
    workDir    string // e.g., /workspace
    logger     logr.Logger
    execCmd    CommandExecutor // interface for testing
}

// ExecOptions configures command execution.
type ExecOptions struct {
    Dir   string     // Working directory
    Env   []string   // Environment variables (KEY=VALUE)
    Stdin io.Reader  // Standard input (nil = no stdin)
}

// ExecResult holds the outcome of a command execution.
type ExecResult struct {
    Stdout   []byte
    Stderr   []byte
    ExitCode int
}

// CommandExecutor abstracts os/exec for testing.
type CommandExecutor interface {
    // Run executes a command. Returns ExecResult for command outcomes (including non-zero exit).
    // Returns error only for non-command failures (context cancel, command not found).
    Run(ctx context.Context, name string, args []string, opts ExecOptions) (*ExecResult, error)
}

func (r *GoRunner) Run(ctx context.Context, task runner.TaskData, token string) (*runner.Result, error) {
    // 0. Copy baked-in CC config from /etc/shepherd/ to ~/.claude/ (emptyDir mount shadows baked-in files)
    //    - mkdir -p ~/.claude
    //    - copy /etc/shepherd/settings.json -> ~/.claude/settings.json
    //    - copy /etc/shepherd/CLAUDE.md -> ~/.claude/CLAUDE.md
    // 1. Clone repo using token in URL: git clone https://x-access-token:{token}@github.com/owner/repo.git
    //    (token in clone URL is stored in .git/config as remote — acceptable for single-task pods; TODO: GIT_ASKPASS)
    // 2. Create working branch: shepherd/{taskID}
    // 3. Write task context to workDir/task-context.md
    // 4. Set env vars for hook: SHEPHERD_API_URL, SHEPHERD_TASK_ID
    // 5. Build prompt from task data
    // 6. Invoke: claude -p "<prompt>" --dangerously-skip-permissions --output-format json --max-turns 50 --max-budget-usd 10.00
    // 7. Parse JSON output: log total_cost_usd, num_turns, session_id
    // 8. Return Result{} — the hook handles success/failure detection via artifact verification
}
```

Key design decisions:

- **`CommandExecutor` interface** allows testing without real `git`/`claude` binaries; returns `*ExecResult` to distinguish exit codes from execution failures
- **Prompt is hardcoded** in a `buildPrompt(task)` function for v1
- **Git credentials** use token embedded in clone URL (`https://x-access-token:{token}@github.com/...`) — standard GitHub approach, no extra scripts
- **Working branch** named `shepherd/{taskID}` for easy identification
- **CC environment**: `ANTHROPIC_API_KEY` from container env, `DISABLE_AUTOUPDATER=1`, `CI=true`
- **Config staging**: CC config files are baked into `/etc/shepherd/` in the image and copied to `~/.claude/` at startup, because the emptyDir volume mount at `/home/shepherd` shadows any files baked into the image

#### 2. Prompt builder

**File**: `cmd/shepherd-runner/prompt.go`

```go
func buildPrompt(task runner.TaskData) string {
    // Constructs the v1 prompt template from task data
    // References task-context.md file path
    // Includes source URL
    // Instructions: implement, create PR, stay focused
}
```

#### 3. Tests

**File**: `cmd/shepherd-runner/gorunner_test.go`

Tests using mock `CommandExecutor`:

- `TestRunCloneAndInvoke` - Happy path: verifies git clone, branch creation, CC invocation with correct args
- `TestRunCloneFailure` - Git clone fails, returns error
- `TestRunCCNonZeroExit` - CC exits non-zero, returns error
- `TestBuildPrompt` - Prompt contains description, source URL, context file path

### Success Criteria

#### Automated Verification

- [ ] `go build ./cmd/shepherd-runner/` compiles
- [ ] `go test ./cmd/shepherd-runner/` passes
- [ ] `make lint-fix` passes
- [ ] `make test` passes

---

## Phase 4: Hook Subcommand

### Overview

Implement the `hook` subcommand that handles CC's Stop hook. The hook reads the hook JSON from stdin, checks the infinite-loop guard, performs basic artifact verification (`git diff`, `gh pr list`), and reports `completed` or `failed` status to the shepherd API. The Go entrypoint fallback in `server.go` handles crash/error reporting if the hook fails to fire.

### Design Decision: Basic Artifact Verification

The CC Stop hook receives **only metadata** on stdin (session_id, transcript_path, cwd, etc.) — it does NOT receive any information about what CC accomplished. Since `claude -p` exits 0 even when Claude cannot complete the task, we need basic artifact checks to distinguish success from failure:

1. **`git diff --quiet HEAD`** — did CC make any changes?
2. **`gh pr list --head shepherd/{taskID}`** — was a PR created?

If no changes were made, report `failed`. If changes exist but no PR, report `failed` with "changes made but no PR created". Only report `completed` when a PR exists.

The hook uses `CommandExecutor` (same interface as GoRunner) for testability — git/gh commands are mockable in tests.

### Changes Required

#### 1. Hook implementation

**File**: `cmd/shepherd-runner/hook.go`

Replace the stub with:

```go
type HookCmd struct{}

func (c *HookCmd) Run() error {
    // 1. Read hook JSON from stdin (CC passes hook data on stdin)
    // 2. Check stop_hook_active field - if true, exit early (prevent infinite loop)
    // 3. Read SHEPHERD_API_URL and SHEPHERD_TASK_ID from env vars
    // 4. Verify artifacts:
    //    a. Run `git diff --quiet HEAD` in CWD — if exit 0, no changes were made
    //    b. If changes exist, run `gh pr list --head shepherd/{taskID} --json url --jq '.[0].url'`
    // 5. Report status to API via runner.Client:
    //    - No changes: report "failed" with message "No changes made"
    //    - Changes but no PR: report "failed" with message "Changes made but no PR created"
    //    - PR exists: report "completed" with pr_url in details
    // 6. On network errors reaching API: exit 0 silently (let Go entrypoint be the safety net)
}
```

The hook is configured in `~/.claude/settings.json` (staged at `/etc/shepherd/settings.json`, copied at startup):

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/shepherd-runner hook",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

#### 2. Hook input types

**File**: `cmd/shepherd-runner/hook.go` (same file)

```go
// HookInput is the JSON data CC passes to hooks on stdin.
// Note: CC does NOT pass result data — only metadata. Artifact verification
// must be done by inspecting git state and PR existence.
type HookInput struct {
    SessionID      string `json:"session_id"`
    TranscriptPath string `json:"transcript_path"`
    CWD            string `json:"cwd"`
    HookEventName  string `json:"hook_event_name"`
    StopHookActive bool   `json:"stop_hook_active"`
}
```

#### 3. Tests

**File**: `cmd/shepherd-runner/hook_test.go`

- `TestHookStopHookActive` - When `stop_hook_active=true`, exits without reporting
- `TestHookNoChanges` - `git diff --quiet` exits 0, reports `failed` with "No changes made"
- `TestHookChangesNoPR` - `git diff` shows changes, `gh pr list` returns empty, reports `failed`
- `TestHookPRCreated` - `git diff` shows changes, `gh pr list` returns URL, reports `completed` with pr_url
- `TestHookMissingEnvVars` - Missing `SHEPHERD_API_URL`, returns error
- `TestHookAPINetworkError` - API unreachable, exits 0 (silent, lets entrypoint handle it)

### Success Criteria

#### Automated Verification

- [ ] `go build ./cmd/shepherd-runner/` compiles
- [ ] `go test ./cmd/shepherd-runner/` passes
- [ ] `make lint-fix` passes
- [ ] `make test` passes

---

## Phase 5: Dockerfile + Image Configuration

### Overview

Create the Dockerfile for `shepherd-runner` and the baked-in configuration files (CLAUDE.md, settings.json).

### Changes Required

#### 1. Dockerfile

**File**: `build/runner/Dockerfile`

```dockerfile
FROM golang:1.25-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /shepherd-runner ./cmd/shepherd-runner/

FROM golang:1.25-bookworm

# Install tools (as root)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    bash \
    ca-certificates \
    make \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Install gh CLI (as root)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*

# Install Claude Code (native Bun binary, as root)
RUN curl -fsSL https://claude.ai/install.sh | bash

# Copy runner binary (root ownership is fine for system binaries)
COPY --from=builder /shepherd-runner /usr/local/bin/shepherd-runner

# Stage CC config files at /etc/shepherd/ (read-only in image).
# The Go entrypoint copies these to ~/.claude/ at startup because the
# emptyDir volume mount at /home/shepherd shadows any baked-in files.
COPY build/runner/CLAUDE.md /etc/shepherd/CLAUDE.md
COPY build/runner/settings.json /etc/shepherd/settings.json

# Create non-root user (after all root-requiring operations)
RUN useradd -m -u 1000 -s /bin/bash shepherd

# Create workspace directory (owned by shepherd)
RUN mkdir -p /workspace && chown shepherd:shepherd /workspace

USER shepherd
WORKDIR /home/shepherd

# Configure git identity (as shepherd user)
RUN git config --global user.name "Shepherd Bot" \
    && git config --global user.email "shepherd-bot@users.noreply.github.com"

EXPOSE 8888
ENTRYPOINT ["shepherd-runner"]
```

Design notes:
- **Config staging at `/etc/shepherd/`**: CC config (settings.json, CLAUDE.md) is baked into a read-only path. The Go entrypoint copies them to `~/.claude/` at startup. This is needed because the SandboxTemplate mounts an emptyDir at `/home/shepherd` for writability, which shadows any files baked into the image.
- **Binary in `/usr/local/bin/`**: COPY always runs as root regardless of USER directive, so ordering doesn't matter. Root ownership is correct for system binaries.
- **`/workspace` owned by shepherd**: Created as root, chowned to shepherd for git clone operations.

#### 2. CLAUDE.md for runner

**File**: `build/runner/CLAUDE.md`

```markdown
# Shepherd Runner

You are a coding agent running inside a Shepherd runner container. You have been assigned a task and should focus on completing it.

## Important

- You are running in headless mode with all permissions granted
- Create a feature branch, commit your changes, and create a pull request
- Stay focused on the assigned task - do not make unrelated changes
- If you cannot solve the task, explain why clearly in your output
- Run tests before committing to verify your changes work
```

#### 3. Claude Code settings

**File**: `build/runner/settings.json`

```json
{
  "permissions": {
    "allow": [
      "Bash(*)",
      "Read(*)",
      "Edit(*)",
      "Write(*)",
      "Glob(*)",
      "Grep(*)"
    ],
    "defaultMode": "acceptEdits"
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/shepherd-runner hook",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

#### 4. SandboxTemplate example

**File**: `config/samples/sandbox-template-runner.yaml`

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
          image: ghcr.io/nissessenap/shepherd-runner:latest
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

Note: The emptyDir mounts ensure `/home/shepherd` and `/workspace` are writable even with read-only root filesystem policies. The Go entrypoint copies CC config from `/etc/shepherd/` to `~/.claude/` at startup since the emptyDir shadows baked-in files.

### Success Criteria

#### Automated Verification

- [ ] `docker build -f build/runner/Dockerfile .` builds successfully
- [ ] `make lint-fix` passes
- [ ] `make test` passes

#### Manual Verification

- [ ] Container starts and responds to `GET /healthz` with 200
- [ ] `claude --version` works inside the container
- [ ] `go version` works inside the container
- [ ] `gh --version` works inside the container

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation from the human that the Docker image works correctly before proceeding to the next phase.

---

## Phase 6: GHA Release Workflow

### Overview

Create a GitHub Actions workflow that builds and pushes both `shepherd` and `shepherd-runner` images to GHCR on tag push.

### Changes Required

#### 1. Release workflow

**File**: `.github/workflows/release.yaml`

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: read
  packages: write
  id-token: write  # For provenance attestation

env:
  REGISTRY: ghcr.io
  SHEPHERD_IMAGE: ghcr.io/${{ github.repository_owner }}/shepherd
  RUNNER_IMAGE: ghcr.io/${{ github.repository_owner }}/shepherd-runner

jobs:
  shepherd:
    name: Build shepherd image
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - uses: ko-build/setup-ko@v0.9
      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Build and push
        env:
          KO_DOCKER_REPO: ${{ env.SHEPHERD_IMAGE }}
        run: |
          ko build --sbom=none --bare --platform=linux/amd64 \
            --tags=${{ github.ref_name }} \
            --tags=latest \
            ./cmd/shepherd/

  runner:
    name: Build shepherd-runner image
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3
      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          file: build/runner/Dockerfile
          push: true
          platforms: linux/amd64
          tags: |
            ${{ env.RUNNER_IMAGE }}:${{ github.ref_name }}
            ${{ env.RUNNER_IMAGE }}:latest
```

#### 2. Makefile updates

**File**: `Makefile`

Add new targets for building the runner image locally:

```makefile
RUNNER_IMG ?= shepherd-runner:latest

.PHONY: docker-build-runner
docker-build-runner: ## Build shepherd-runner Docker image locally.
 docker build -f build/runner/Dockerfile -t $(RUNNER_IMG) .
```

### Success Criteria

#### Automated Verification

- [ ] `make lint-fix` passes
- [ ] `make test` passes
- [ ] `make docker-build-runner` builds locally

#### Manual Verification

- [ ] Push a test tag to trigger the workflow (on a fork or with `workflow_dispatch`)
- [ ] Both images appear in GHCR with correct tags
- [ ] Images contain expected binaries (`shepherd`, `shepherd-runner`, `claude`, `go`, `gh`)

---

## Testing Strategy

### Unit Tests

- `pkg/runner/server_test.go` - HTTP endpoint tests (healthz, task assignment, conflict, invalid JSON)
- `pkg/runner/client_test.go` - API client tests with httptest mocks (fetch data, fetch token, report status)
- `cmd/shepherd-runner/gorunner_test.go` - GoRunner tests with mocked command executor
- `cmd/shepherd-runner/hook_test.go` - Hook subcommand tests (stdin parsing, stop_hook_active guard, artifact verification, status reporting)
- `cmd/shepherd-runner/prompt_test.go` - Prompt builder tests

### What We Don't Test

- Actual Claude Code invocation (subprocess, non-deterministic)
- Docker image building (tested via `docker build` in CI)
- GitHub token generation (API server's responsibility)
- Real git clone operations (mocked via CommandExecutor)

### Manual Testing Steps

1. Build Docker image locally: `make docker-build-runner`
2. Run container: `docker run -p 8888:8888 -e ANTHROPIC_API_KEY=... shepherd-runner:latest`
3. Verify health: `curl localhost:8888/healthz`
4. In a kind cluster: deploy SandboxTemplate, create AgentTask, verify runner picks up and executes

## Performance Considerations

- Runner pods need 4Gi memory for CC + Go compilation
- CC `--max-budget-usd 10.00` caps per-task API spend
- CC `--max-turns 50` prevents infinite loops
- Token has 1-hour expiry - long tasks may need token refresh (future)
- Docker image is ~1GB+ due to Go toolchain - acceptable for long-running pods

## File Summary

### New Files

```
pkg/runner/
  runner.go              - Types and TaskRunner interface
  server.go              - HTTP server (:8888, /healthz, POST /task)
  server_test.go         - Server endpoint tests
  client.go              - API client (fetch data, token, report status)
  client_test.go         - Client tests with httptest mocks

test/e2e/testrunner/
  main.go                - Stub runner moved from cmd/shepherd-runner/ (e2e test infrastructure)

cmd/shepherd-runner/
  main.go                - Kong CLI entry point (serve + hook subcommands)
  serve.go               - Serve subcommand (wires GoRunner + runner.Server)
  gorunner.go            - GoRunner implementation (clone, invoke CC)
  gorunner_test.go       - GoRunner tests with mock executor
  prompt.go              - Prompt builder
  prompt_test.go         - Prompt tests
  hook.go                - Hook subcommand (CC Stop hook handler)
  hook_test.go           - Hook tests

build/runner/
  Dockerfile             - Multi-stage Dockerfile
  CLAUDE.md              - Baked-in coding instructions
  settings.json          - CC permissions + hook config

config/samples/
  sandbox-template-runner.yaml  - Example SandboxTemplate

.github/workflows/
  release.yaml           - Build and push images on tag
```

### Modified Files

```
Makefile                 - Add docker-build-runner target
go.mod / go.sum          - New dependency: kong (already present)
```

## References

- Research: `thoughts/research/2026-02-08-real-runner-image-design.md`
- POC entrypoint: `poc/sandbox/cmd/entrypoint/main.go`
- Runner stub (moved): `test/e2e/testrunner/main.go` (originally `cmd/shepherd-runner/main.go`)
- API types: `pkg/api/types.go`
- Operator task assignment: `internal/controller/agenttask_controller.go:217-256`
- Status handler: `pkg/api/handler_status.go`
- Token handler: `pkg/api/handler_token.go`
