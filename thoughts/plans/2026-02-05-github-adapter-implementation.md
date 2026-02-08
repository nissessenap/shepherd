# GitHub Adapter Implementation Plan

## Overview

Implement the GitHub adapter (`shepherd github`) - the component that receives GitHub webhooks, creates tasks via the Shepherd API, and posts GitHub comments. The adapter uses the "Trigger App" GitHub App for reading issues/PRs and posting comments.

**Key decisions from user**:
1. Use `go-github` + `ghinstallation` libraries (replace custom HTTP implementation)
2. Simple feedback: acknowledge request on GitHub, then wait for PR completion
3. Static installation ID configuration
4. Package location: `pkg/adapters/github/` for future GitLab support

## Current State Analysis

**What exists**:
- `cmd/shepherd/main.go:38-47` - GitHubCmd stub that returns "not implemented yet"
- `pkg/api/github_token.go` - Custom JWT/token exchange (to be refactored)
- `pkg/api/callback.go` - HMAC signing for outbound callbacks (adapter will verify these)
- API endpoints for task creation and querying

**What's missing**:
- `pkg/adapters/github/` package
- Webhook signature verification
- GitHub App client using ghinstallation
- Comment posting logic
- Callback receiver endpoint

### Key Discoveries:
- API callback uses `X-Shepherd-Signature: sha256=<hex>` header (`pkg/api/callback.go:68`)
- Task deduplication via `GET /api/v1/tasks?repo=X&issue=Y&active=true`
- Source tracking fields: `sourceType`, `sourceURL`, `sourceID` in task spec
- Labels for filtering: `shepherd.io/repo`, `shepherd.io/issue`

## Desired End State

A working GitHub adapter that:
- Listens for GitHub webhooks on port 8082 (configurable)
- Verifies webhook signatures using the webhook secret
- Triggers on `issue_comment` events with `@shepherd` mentions
- Checks API for active tasks (deduplication)
- Posts acknowledgment comment on GitHub
- Creates task via Shepherd API with callback URL
- Receives callbacks from API
- Posts final comment with PR link or failure message

### How to Verify

- `make test` passes all unit tests
- `make build` compiles successfully
- `shepherd github --help` shows correct flags
- Webhook signature verification rejects invalid signatures
- Deduplication prevents duplicate tasks
- Comments are posted to GitHub issues
- Callbacks trigger appropriate GitHub comments

## What We're NOT Doing

- PR review triggers (issue_comment only for MVP)
- Multiple GitHub App installations (static installation ID)
- Retry logic for GitHub API failures (rely on API's retry)
- GitLab adapter (future, but package structure supports it)
- Authentication on callback endpoint (HMAC signature is sufficient)
- **Interface abstraction for adapters** - MVP implements GitHub directly; interfaces can emerge later when we have a second adapter (GitLab) to learn from

## Architecture: Two GitHub Apps

The system uses two separate GitHub Apps with different responsibilities:

| GitHub App | Location | Purpose |
|------------|----------|---------|
| **Trigger App** | `pkg/adapters/github/` | Receives webhooks, reads issues/PRs, posts comments |
| **Runner App** | `pkg/api/github_token.go` | API generates tokens for runners to clone repos, push branches, create PRs |

**Why separate locations?**

- The **Trigger App** is adapter-specific - only the GitHub adapter needs it
- The **Runner App** token generation is API server responsibility - runners call the API to get tokens regardless of which adapter triggered the task
- Phase 6 refactors `pkg/api/github_token.go` to use ghinstallation (same library as adapter) but keeps it in `pkg/api/` because it serves the API, not the adapter

**Why no interface abstraction?**

- Interfaces should emerge from real implementations, not be designed upfront
- We don't know GitLab's webhook/API model well enough to abstract yet
- The package structure (`pkg/adapters/github/`, future `pkg/adapters/gitlab/`) provides organizational separation
- Adding a common interface later is a small refactor once we understand both implementations

## Implementation Approach

Follow existing patterns from `pkg/api/`:
- Kong CLI command delegates to package `Run(Options)` function
- Chi router with middleware stack
- Signal handling with graceful shutdown
- Health endpoints at `/healthz` and `/readyz`
- Testify for unit tests with httptest

---

## Phase 1: Dependencies + Package Scaffold

### Overview

Add go-github and ghinstallation dependencies, create the `pkg/adapters/github/` package structure, and wire the GitHubCmd to call the new package.

### Changes Required

#### 1. Add Dependencies

```bash
go get github.com/google/go-github/v68
go get github.com/bradleyfalzon/ghinstallation/v2
go get github.com/go-chi/httprate
```

Note: Use latest go-github version at implementation time.

#### 2. Create Package Structure

**Directory**: `pkg/adapters/github/`

**File**: `pkg/adapters/github/server.go`

```go
package github

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "strings"
    "sync/atomic"
    "syscall"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/go-chi/httprate"
    ctrl "sigs.k8s.io/controller-runtime"
)

// Options configures the GitHub adapter.
type Options struct {
    ListenAddr     string // ":8082"
    WebhookSecret  string // GitHub webhook secret
    AppID          int64  // GitHub App ID
    InstallationID int64  // GitHub Installation ID
    PrivateKeyPath string // Path to private key PEM file
    APIURL         string // Shepherd API URL (e.g., "http://shepherd-api:8080")
    CallbackSecret string // Shared secret for callback HMAC verification
    CallbackURL    string // URL for API to call back (e.g., "http://github-adapter:8082/callback")
}

// requireJSON validates Content-Type on POST/PUT/PATCH requests.
func requireJSON(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
            ct := r.Header.Get("Content-Type")
            if !strings.HasPrefix(ct, "application/json") {
                http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
                return
            }
        }
        next.ServeHTTP(w, r)
    })
}

// Run starts the GitHub adapter server.
func Run(opts Options) error {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    log := ctrl.Log.WithName("github-adapter")

    // TODO: Phase 2 - Create GitHub client
    // TODO: Phase 4 - Create API client

    // Health tracking
    var healthy atomic.Bool
    healthy.Store(true)

    // Build router
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Recoverer)

    // Health endpoints
    r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })
    r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
        if !healthy.Load() {
            w.WriteHeader(http.StatusServiceUnavailable)
            _, _ = w.Write([]byte("unhealthy"))
            return
        }
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

    // TODO: Phase 3 - Webhook endpoint (rate-limited, with requireJSON)
    // r.Route("/webhook", func(r chi.Router) {
    //     r.Use(httprate.LimitByIP(100, time.Minute))
    //     r.Use(requireJSON)
    //     r.Post("/", webhookHandler.ServeHTTP)
    // })
    // TODO: Phase 5 - Callback endpoint (with requireJSON)
    // r.With(requireJSON).Post("/callback", callbackHandler.ServeHTTP)

    srv := &http.Server{
        Addr:         opts.ListenAddr,
        Handler:      r,
        ReadTimeout:  30 * time.Second,
        WriteTimeout: 60 * time.Second,
        IdleTimeout:  120 * time.Second,
    }

    errCh := make(chan error, 1)
    go func() {
        log.Info("starting GitHub adapter", "addr", opts.ListenAddr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            errCh <- fmt.Errorf("server error: %w", err)
        }
    }()

    select {
    case <-ctx.Done():
        log.Info("shutting down GitHub adapter")
        shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer shutdownCancel()
        return srv.Shutdown(shutdownCtx)
    case err := <-errCh:
        return err
    }
}
```

#### 3. Wire CLI Command

**File**: `cmd/shepherd/main.go`

Replace the existing `GitHubCmd` struct entirely. Use consistent naming with `APICmd`:
- `--github-app-id`, `--github-installation-id`, `--github-private-key-path` match the API's pattern
- Adapter-specific flags: `--webhook-secret`, `--api-url`, `--callback-secret`, `--callback-url`
- Env vars use the same names as APICmd where concepts overlap (they run in separate processes)

**Breaking change**: `SHEPHERD_GITHUB_PRIVATE_KEY` becomes `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` for consistency.

```go
type GitHubCmd struct {
    ListenAddr           string `help:"GitHub adapter listen address" default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
    WebhookSecret        string `help:"GitHub webhook secret" env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
    GithubAppID          int64  `help:"GitHub App ID" env:"SHEPHERD_GITHUB_APP_ID"`
    GithubInstallationID int64  `help:"GitHub Installation ID" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
    GithubPrivateKeyPath string `help:"Path to GitHub App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY_PATH"`
    APIURL               string `help:"Shepherd API URL" required:"" env:"SHEPHERD_API_URL"`
    CallbackSecret       string `help:"Shared secret for callback verification" env:"SHEPHERD_CALLBACK_SECRET"`
    CallbackURL          string `help:"Callback URL for API to call back" env:"SHEPHERD_CALLBACK_URL"`
}

func (c *GitHubCmd) Run(_ *CLI) error {
    // Validate required fields
    if c.WebhookSecret == "" {
        return fmt.Errorf("webhook-secret is required")
    }
    if c.GithubAppID == 0 {
        return fmt.Errorf("github-app-id is required")
    }
    if c.GithubInstallationID == 0 {
        return fmt.Errorf("github-installation-id is required")
    }
    if c.GithubPrivateKeyPath == "" {
        return fmt.Errorf("github-private-key-path is required")
    }

    return github.Run(github.Options{
        ListenAddr:     c.ListenAddr,
        WebhookSecret:  c.WebhookSecret,
        AppID:          c.GithubAppID,
        InstallationID: c.GithubInstallationID,
        PrivateKeyPath: c.GithubPrivateKeyPath,
        APIURL:         c.APIURL,
        CallbackSecret: c.CallbackSecret,
        CallbackURL:    c.CallbackURL,
    })
}
```

Add import for the github package at top of file.

### Success Criteria

#### Automated Verification:
- [x] `go get` adds dependencies to go.mod/go.sum
- [x] `make build` compiles successfully
- [x] `go vet ./...` clean
- [x] `make lint-fix` passes
- [x] `shepherd github --help` shows all flags

#### Manual Verification:
- [ ] `shepherd github` with required flags starts and responds to `/healthz`

**Pause for review before Phase 2.**

---

## Phase 2: GitHub App Client

### Overview

Implement the GitHub App client using ghinstallation for authentication. This client will be used to post comments to issues/PRs.

### Changes Required

#### 1. GitHub Client Wrapper

**File**: `pkg/adapters/github/client.go`

```go
package github

import (
    "context"
    "fmt"
    "net/http"
    "os"

    "github.com/bradleyfalzon/ghinstallation/v2"
    "github.com/google/go-github/v68/github"
)

// Client wraps the GitHub API client with app authentication.
type Client struct {
    gh             *github.Client
    installationID int64
}

// NewClient creates a new GitHub client authenticated as a GitHub App installation.
func NewClient(appID, installationID int64, privateKeyPath string) (*Client, error) {
    keyData, err := os.ReadFile(privateKeyPath)
    if err != nil {
        return nil, fmt.Errorf("reading private key: %w", err)
    }

    transport, err := ghinstallation.New(http.DefaultTransport, appID, installationID, keyData)
    if err != nil {
        return nil, fmt.Errorf("creating installation transport: %w", err)
    }

    return &Client{
        gh:             github.NewClient(&http.Client{Transport: transport}),
        installationID: installationID,
    }, nil
}

// PostComment posts a comment to an issue or pull request.
func (c *Client) PostComment(ctx context.Context, owner, repo string, number int, body string) error {
    comment := &github.IssueComment{Body: github.Ptr(body)}
    _, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number, comment)
    if err != nil {
        return fmt.Errorf("creating comment: %w", err)
    }
    return nil
}

// GetIssue retrieves an issue or PR by number.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (*github.Issue, error) {
    issue, _, err := c.gh.Issues.Get(ctx, owner, repo, number)
    if err != nil {
        return nil, fmt.Errorf("getting issue: %w", err)
    }
    return issue, nil
}

// ListIssueComments retrieves all comments on an issue.
func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, number int) ([]*github.IssueComment, error) {
    var allComments []*github.IssueComment
    opts := &github.IssueListCommentsOptions{
        ListOptions: github.ListOptions{PerPage: 100},
    }
    for {
        comments, resp, err := c.gh.Issues.ListComments(ctx, owner, repo, number, opts)
        if err != nil {
            return nil, fmt.Errorf("listing comments: %w", err)
        }
        allComments = append(allComments, comments...)
        if resp.NextPage == 0 {
            break
        }
        opts.Page = resp.NextPage
    }
    return allComments, nil
}
```

#### 2. Comment Templates

**File**: `pkg/adapters/github/comments.go`

```go
package github

import "fmt"

// Comment templates for different events
const (
    CommentAcknowledge = `Shepherd is working on your request.

Task ID: %s

I'll update this issue when I'm done.`

    CommentAlreadyRunning = `A Shepherd task is already running for this issue.

Task ID: %s
Status: %s

Please wait for it to complete before triggering a new one.`

    CommentCompleted = `Shepherd has completed the task.

Pull Request: %s

Please review the changes.`

    CommentFailed = `Shepherd was unable to complete the task.

Error: %s

You can trigger a new attempt by commenting with @shepherd again.`
)

func formatAcknowledge(taskID string) string {
    return fmt.Sprintf(CommentAcknowledge, taskID)
}

func formatAlreadyRunning(taskID, status string) string {
    return fmt.Sprintf(CommentAlreadyRunning, taskID, status)
}

func formatCompleted(prURL string) string {
    return fmt.Sprintf(CommentCompleted, prURL)
}

func formatFailed(errorMsg string) string {
    if errorMsg == "" {
        errorMsg = "Unknown error"
    }
    return fmt.Sprintf(CommentFailed, errorMsg)
}
```

#### 3. Unit Tests

**File**: `pkg/adapters/github/client_test.go`

```go
package github

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestClient_PostComment(t *testing.T) {
    var receivedBody map[string]string
    var receivedPath string

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        receivedPath = r.URL.Path
        _ = json.NewDecoder(r.Body).Decode(&receivedBody)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusCreated)
        _, _ = w.Write([]byte(`{"id": 1, "body": "test"}`))
    }))
    defer srv.Close()

    // Create client with mock server
    // Note: For real tests, we'd mock the transport or use a test key
    // This is a simplified example
    t.Skip("Requires mock GitHub client setup - implement with interface")
}

func TestCommentTemplates(t *testing.T) {
    t.Run("acknowledge", func(t *testing.T) {
        result := formatAcknowledge("task-abc123")
        assert.Contains(t, result, "task-abc123")
        assert.Contains(t, result, "working on your request")
    })

    t.Run("already running", func(t *testing.T) {
        result := formatAlreadyRunning("task-xyz", "Running")
        assert.Contains(t, result, "task-xyz")
        assert.Contains(t, result, "Running")
        assert.Contains(t, result, "already running")
    })

    t.Run("completed", func(t *testing.T) {
        result := formatCompleted("https://github.com/org/repo/pull/42")
        assert.Contains(t, result, "https://github.com/org/repo/pull/42")
        assert.Contains(t, result, "completed")
    })

    t.Run("failed with message", func(t *testing.T) {
        result := formatFailed("Build failed")
        assert.Contains(t, result, "Build failed")
    })

    t.Run("failed empty message", func(t *testing.T) {
        result := formatFailed("")
        assert.Contains(t, result, "Unknown error")
    })
}
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes (new tests in pkg/adapters/github/)
- [x] `go vet ./...` clean
- [x] `make lint-fix` passes

#### Manual Verification:
- [ ] Client can authenticate with a real GitHub App (integration test)

**Pause for review before Phase 3.**

---

## Phase 3: Webhook Handler

### Overview

Implement the webhook endpoint that receives GitHub webhooks, verifies signatures, and processes `issue_comment` events with `@shepherd` mentions.

### Changes Required

#### 1. Webhook Handler

**File**: `pkg/adapters/github/webhook.go`

```go
package github

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "regexp"
    "strings"

    "github.com/go-logr/logr"
    gh "github.com/google/go-github/v68/github"
)

// Matches @shepherd mentions but not email-style patterns (e.g., user@shepherd.io).
// Requires start-of-string or whitespace before the @.
var shepherdMentionRegex = regexp.MustCompile(`(?i)(?:^|\s)@shepherd\b`)

// WebhookHandler handles incoming GitHub webhooks.
type WebhookHandler struct {
    secret          string
    ghClient        *Client
    apiClient       *APIClient       // Added in Phase 4
    callbackHandler *CallbackHandler // Added in Phase 4
    callbackURL     string           // Added in Phase 4
    log             logr.Logger
}

// NewWebhookHandler creates a new webhook handler.
// apiClient, callbackHandler, and callbackURL are nil/"" in Phase 3 and wired in Phase 4.
func NewWebhookHandler(secret string, ghClient *Client, apiClient *APIClient, callbackHandler *CallbackHandler, callbackURL string, log logr.Logger) *WebhookHandler {
    return &WebhookHandler{
        secret:          secret,
        ghClient:        ghClient,
        apiClient:       apiClient,
        callbackHandler: callbackHandler,
        callbackURL:     callbackURL,
        log:             log,
    }
}

// ServeHTTP handles webhook requests.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Read body
    body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
    if err != nil {
        h.log.Error(err, "failed to read webhook body")
        http.Error(w, "failed to read body", http.StatusBadRequest)
        return
    }

    // Verify signature
    signature := r.Header.Get("X-Hub-Signature-256")
    if !h.verifySignature(body, signature) {
        h.log.Info("webhook signature verification failed")
        http.Error(w, "invalid signature", http.StatusUnauthorized)
        return
    }

    // Parse event type
    eventType := r.Header.Get("X-GitHub-Event")
    h.log.V(1).Info("received webhook", "event", eventType)

    switch eventType {
    case "issue_comment":
        h.handleIssueComment(r.Context(), body)
    case "ping":
        h.log.Info("received ping webhook")
    default:
        h.log.V(1).Info("ignoring event type", "event", eventType)
    }

    w.WriteHeader(http.StatusOK)
}

// verifySignature verifies the GitHub webhook signature using HMAC-SHA256.
func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
    if h.secret == "" {
        return true // No verification if no secret configured
    }

    if !strings.HasPrefix(signature, "sha256=") {
        return false
    }

    expectedMAC := hmac.New(sha256.New, []byte(h.secret))
    expectedMAC.Write(body)
    expected := "sha256=" + hex.EncodeToString(expectedMAC.Sum(nil))

    return hmac.Equal([]byte(expected), []byte(signature))
}

// handleIssueComment processes issue_comment events.
func (h *WebhookHandler) handleIssueComment(ctx context.Context, body []byte) {
    var event gh.IssueCommentEvent
    if err := json.Unmarshal(body, &event); err != nil {
        h.log.Error(err, "failed to parse issue_comment event")
        return
    }

    // Only process new comments (not edits or deletes)
    if event.GetAction() != "created" {
        return
    }

    // Check for @shepherd mention
    commentBody := event.GetComment().GetBody()
    if !shepherdMentionRegex.MatchString(commentBody) {
        return
    }

    // Extract task description from comment
    // Remove the @shepherd mention and use the rest as description
    description := strings.TrimSpace(shepherdMentionRegex.ReplaceAllString(commentBody, ""))
    if description == "" {
        description = "Work on this issue"
    }

    h.log.Info("processing @shepherd mention",
        "repo", event.GetRepo().GetFullName(),
        "issue", event.GetIssue().GetNumber(),
        "user", event.GetComment().GetUser().GetLogin(),
    )

    // TODO: Phase 4 - Check for active tasks and create new task
    h.processTask(ctx, &event, description)
}

// processTask handles the task creation workflow.
func (h *WebhookHandler) processTask(ctx context.Context, event *gh.IssueCommentEvent, description string) {
    owner := event.GetRepo().GetOwner().GetLogin()
    repo := event.GetRepo().GetName()
    issueNumber := event.GetIssue().GetNumber()
    repoFullName := event.GetRepo().GetFullName()

    // Format repo label value (replace / with -)
    repoLabel := strings.ReplaceAll(repoFullName, "/", "-")
    issueLabel := fmt.Sprintf("%d", issueNumber)

    h.log.V(1).Info("would process task",
        "owner", owner,
        "repo", repo,
        "issue", issueNumber,
        "repoLabel", repoLabel,
        "description", description,
    )

    // TODO: Phase 4 implementation
    // 1. Check for active tasks via API
    // 2. If active, post "already running" comment
    // 3. If none active, create task and post acknowledgment
}
```

#### 2. Wire Webhook Handler to Server

**File**: `pkg/adapters/github/server.go`

Add after router setup:

```go
// Create GitHub client
ghClient, err := NewClient(opts.AppID, opts.InstallationID, opts.PrivateKeyPath)
if err != nil {
    return fmt.Errorf("creating github client: %w", err)
}

// Webhook handler (apiClient and callbackHandler added in Phase 4)
webhookHandler := NewWebhookHandler(opts.WebhookSecret, ghClient, nil, nil, "", log)

// Mount webhook endpoint with rate limiting + content-type validation
r.Route("/webhook", func(r chi.Router) {
    r.Use(httprate.LimitByIP(100, time.Minute))
    r.Use(requireJSON)
    r.Post("/", webhookHandler.ServeHTTP)
})
```

#### 3. Unit Tests

**File**: `pkg/adapters/github/webhook_test.go`

```go
package github

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    ctrl "sigs.k8s.io/controller-runtime"
)

func TestWebhookHandler_SignatureVerification(t *testing.T) {
    secret := "test-secret"
    handler := NewWebhookHandler(secret, nil, nil, nil, "", ctrl.Log.WithName("test"))

    t.Run("valid signature", func(t *testing.T) {
        body := []byte(`{"action":"created"}`)
        mac := hmac.New(sha256.New, []byte(secret))
        mac.Write(body)
        sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

        assert.True(t, handler.verifySignature(body, sig))
    })

    t.Run("invalid signature", func(t *testing.T) {
        body := []byte(`{"action":"created"}`)
        assert.False(t, handler.verifySignature(body, "sha256=invalid"))
    })

    t.Run("missing prefix", func(t *testing.T) {
        body := []byte(`{"action":"created"}`)
        assert.False(t, handler.verifySignature(body, "invalid"))
    })

    t.Run("empty secret allows all", func(t *testing.T) {
        h := NewWebhookHandler("", nil, nil, nil, "", ctrl.Log.WithName("test"))
        assert.True(t, h.verifySignature([]byte(`{}`), ""))
    })
}

func TestWebhookHandler_ServeHTTP(t *testing.T) {
    secret := "test-secret"
    handler := NewWebhookHandler(secret, nil, nil, nil, "", ctrl.Log.WithName("test"))

    t.Run("rejects GET requests", func(t *testing.T) {
        req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, req)
        assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
    })

    t.Run("rejects invalid signature", func(t *testing.T) {
        body := []byte(`{"action":"created"}`)
        req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
        req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
        req.Header.Set("X-GitHub-Event", "ping")
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, req)
        assert.Equal(t, http.StatusUnauthorized, w.Code)
    })

    t.Run("accepts valid ping", func(t *testing.T) {
        body := []byte(`{"zen":"test"}`)
        req := signedRequest(t, secret, body, "ping")
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
    })
}

func TestShepherdMentionRegex(t *testing.T) {
    tests := []struct {
        input string
        match bool
    }{
        {"@shepherd fix this bug", true},
        {"@SHEPHERD fix this bug", true},
        {"@Shepherd fix this bug", true},
        {"Hey @shepherd can you help?", true},
        {"@shepherd", true},
        {"\n@shepherd fix it", true},
        {"@shepherding", false},
        {"no mention here", false},
        {"email@shepherd.io", false},
        {"user@shepherd", false},
        {"test@shepherd.com stuff", false},
    }

    for _, tc := range tests {
        t.Run(tc.input, func(t *testing.T) {
            assert.Equal(t, tc.match, shepherdMentionRegex.MatchString(tc.input))
        })
    }
}

func signedRequest(t *testing.T, secret string, body []byte, event string) *http.Request {
    t.Helper()
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
    req.Header.Set("X-Hub-Signature-256", sig)
    req.Header.Set("X-GitHub-Event", event)
    return req
}
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes
- [ ] Signature verification tests pass
- [ ] @shepherd regex correctly matches mentions

#### Manual Verification:
- [ ] Webhook endpoint accepts valid GitHub webhooks
- [ ] Invalid signatures are rejected with 401

**Pause for review before Phase 4.**

---

## Phase 4: API Client + Task Creation

### Overview

Create an HTTP client for the Shepherd API to check for active tasks (deduplication) and create new tasks.

### Changes Required

#### 1. API Client

**File**: `pkg/adapters/github/api_client.go`

**Key design decision**: Import types directly from `pkg/api` instead of redeclaring them locally.
Both packages are in the same Go module and there is no circular dependency risk
(`pkg/api` does not import `pkg/adapters/*`). This eliminates type drift - any field
changes in `pkg/api/types.go` are automatically picked up by the adapter.

```go
package github

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "time"

    "github.com/NissesSenap/shepherd/pkg/api"
)

// APIClient communicates with the Shepherd API.
type APIClient struct {
    baseURL    string
    httpClient *http.Client
}

// NewAPIClient creates a new API client.
func NewAPIClient(baseURL string) *APIClient {
    return &APIClient{
        baseURL: baseURL,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
    }
}

// GetActiveTasks queries for active tasks matching the given labels.
func (c *APIClient) GetActiveTasks(ctx context.Context, repoLabel, issueLabel string) ([]api.TaskResponse, error) {
    u, err := url.Parse(c.baseURL + "/api/v1/tasks")
    if err != nil {
        return nil, fmt.Errorf("parsing URL: %w", err)
    }

    q := u.Query()
    q.Set("repo", repoLabel)
    q.Set("issue", issueLabel)
    q.Set("active", "true")
    u.RawQuery = q.Encode()

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
    if err != nil {
        return nil, fmt.Errorf("creating request: %w", err)
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("executing request: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
    if err != nil {
        return nil, fmt.Errorf("reading response: %w", err)
    }

    if resp.StatusCode != http.StatusOK {
        var errResp api.ErrorResponse
        _ = json.Unmarshal(body, &errResp)
        return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
    }

    var tasks []api.TaskResponse
    if err := json.Unmarshal(body, &tasks); err != nil {
        return nil, fmt.Errorf("parsing response: %w", err)
    }

    return tasks, nil
}

// GetTask fetches a single task by ID. Used by CallbackHandler to resolve
// task metadata for callbacks received after a restart (stateless recovery).
func (c *APIClient) GetTask(ctx context.Context, taskID string) (*api.TaskResponse, error) {
    reqURL := c.baseURL + "/api/v1/tasks/" + taskID
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
    if err != nil {
        return nil, fmt.Errorf("creating request: %w", err)
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("executing request: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
    if err != nil {
        return nil, fmt.Errorf("reading response: %w", err)
    }

    if resp.StatusCode != http.StatusOK {
        var errResp api.ErrorResponse
        _ = json.Unmarshal(body, &errResp)
        return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
    }

    var task api.TaskResponse
    if err := json.Unmarshal(body, &task); err != nil {
        return nil, fmt.Errorf("parsing response: %w", err)
    }

    return &task, nil
}

// CreateTask creates a new task via the API.
func (c *APIClient) CreateTask(ctx context.Context, createReq api.CreateTaskRequest) (*api.TaskResponse, error) {
    body, err := json.Marshal(createReq)
    if err != nil {
        return nil, fmt.Errorf("marshaling request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/tasks", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("creating request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("executing request: %w", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
    if err != nil {
        return nil, fmt.Errorf("reading response: %w", err)
    }

    if resp.StatusCode != http.StatusCreated {
        var errResp api.ErrorResponse
        _ = json.Unmarshal(respBody, &errResp)
        return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
    }

    var taskResp api.TaskResponse
    if err := json.Unmarshal(respBody, &taskResp); err != nil {
        return nil, fmt.Errorf("parsing response: %w", err)
    }

    return &taskResp, nil
}
```

#### 2. Complete processTask Implementation

**File**: `pkg/adapters/github/webhook.go`

Update processTask to use `api.*` types (imported from `pkg/api`):

```go
import "github.com/NissesSenap/shepherd/pkg/api"

// processTask handles the task creation workflow.
func (h *WebhookHandler) processTask(ctx context.Context, event *gh.IssueCommentEvent, description string) {
    owner := event.GetRepo().GetOwner().GetLogin()
    repo := event.GetRepo().GetName()
    issueNumber := event.GetIssue().GetNumber()
    repoFullName := event.GetRepo().GetFullName()
    issueURL := event.GetIssue().GetHTMLURL()
    repoURL := event.GetRepo().GetCloneURL()

    // Format label values
    repoLabel := strings.ReplaceAll(repoFullName, "/", "-")
    issueLabel := fmt.Sprintf("%d", issueNumber)

    // Check for active tasks (deduplication)
    activeTasks, err := h.apiClient.GetActiveTasks(ctx, repoLabel, issueLabel)
    if err != nil {
        h.log.Error(err, "failed to check for active tasks")
        // Continue anyway - better to potentially create duplicate than fail silently
    }

    if len(activeTasks) > 0 {
        task := activeTasks[0]
        h.log.Info("task already running", "taskID", task.ID, "status", task.Status.Phase)

        if err := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
            formatAlreadyRunning(task.ID, task.Status.Phase)); err != nil {
            h.log.Error(err, "failed to post already-running comment")
        }
        return
    }

    // Build context from issue body and comments
    issueBody := event.GetIssue().GetBody()
    taskContext := h.buildContext(ctx, owner, repo, issueNumber, issueBody)

    // Create task (uses api.* types from pkg/api)
    createReq := api.CreateTaskRequest{
        Repo: api.RepoRequest{
            URL: repoURL,
        },
        Task: api.TaskRequest{
            Description: description,
            Context:     taskContext,
            SourceURL:   issueURL,
            SourceType:  "issue",
            SourceID:    issueLabel,
        },
        Callback: h.callbackURL,
        Runner: &api.RunnerConfig{
            SandboxTemplateName: "default", // TODO: Make configurable
        },
        Labels: map[string]string{
            "shepherd.io/repo":  repoLabel,
            "shepherd.io/issue": issueLabel,
        },
    }

    taskResp, err := h.apiClient.CreateTask(ctx, createReq)
    if err != nil {
        h.log.Error(err, "failed to create task")
        if err := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
            formatFailed("Failed to create task: "+err.Error())); err != nil {
            h.log.Error(err, "failed to post error comment")
        }
        return
    }

    h.log.Info("created task", "taskID", taskResp.ID)

    // Register task metadata for callback handling
    h.callbackHandler.RegisterTask(taskResp.ID, TaskMetadata{
        Owner:       owner,
        Repo:        repo,
        IssueNumber: issueNumber,
    })

    // Post acknowledgment comment
    if err := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
        formatAcknowledge(taskResp.ID)); err != nil {
        h.log.Error(err, "failed to post acknowledgment comment")
    }
}

// maxContextSize is the soft limit for context passed to the API.
// The API's etcd limit is ~1.4MB compressed; 1MB uncompressed provides
// safe headroom since gzip typically achieves 3-5x compression on text.
const maxContextSize = 1_000_000 // 1MB

// buildContext assembles the context string from issue body and comments.
// Truncates if the total context exceeds maxContextSize.
func (h *WebhookHandler) buildContext(ctx context.Context, owner, repo string, issueNumber int, issueBody string) string {
    var sb strings.Builder
    sb.WriteString("## Issue Description\n\n")
    sb.WriteString(issueBody)
    sb.WriteString("\n\n")

    // Fetch comments
    comments, err := h.ghClient.ListIssueComments(ctx, owner, repo, issueNumber)
    if err != nil {
        h.log.Error(err, "failed to fetch issue comments")
        return sb.String()
    }

    if len(comments) > 0 {
        sb.WriteString("## Comments\n\n")
        for _, c := range comments {
            entry := fmt.Sprintf("**%s** wrote:\n\n%s\n\n---\n\n", c.GetUser().GetLogin(), c.GetBody())
            if sb.Len()+len(entry) > maxContextSize {
                sb.WriteString("\n\n--- Context truncated due to size limit ---\n")
                h.log.Info("context truncated", "issue", issueNumber, "size", sb.Len())
                break
            }
            sb.WriteString(entry)
        }
    }

    return sb.String()
}
```

#### 3. Update WebhookHandler struct

Add `callbackURL` and `callbackHandler` fields:

```go
type WebhookHandler struct {
    secret          string
    ghClient        *Client
    apiClient       *APIClient
    callbackHandler *CallbackHandler
    callbackURL     string
    log             logr.Logger
}

func NewWebhookHandler(secret string, ghClient *Client, apiClient *APIClient, callbackHandler *CallbackHandler, callbackURL string, log logr.Logger) *WebhookHandler {
    return &WebhookHandler{
        secret:          secret,
        ghClient:        ghClient,
        apiClient:       apiClient,
        callbackHandler: callbackHandler,
        callbackURL:     callbackURL,
        log:             log,
    }
}
```

#### 4. Update Server to Create API Client

**File**: `pkg/adapters/github/server.go`

```go
// Create API client
apiClient := NewAPIClient(opts.APIURL)

// Create callback handler (Phase 5 adds callback endpoint)
callbackHandler := NewCallbackHandler(opts.CallbackSecret, ghClient, apiClient, log)

// Webhook handler
webhookHandler := NewWebhookHandler(opts.WebhookSecret, ghClient, apiClient, callbackHandler, opts.CallbackURL, log)

// Mount webhook endpoint with rate limiting + content-type validation
r.Route("/webhook", func(r chi.Router) {
    r.Use(httprate.LimitByIP(100, time.Minute))
    r.Use(requireJSON)
    r.Post("/", webhookHandler.ServeHTTP)
})
```

#### 5. Unit Tests

**File**: `pkg/adapters/github/api_client_test.go`

```go
package github

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/NissesSenap/shepherd/pkg/api"
)

func TestAPIClient_GetActiveTasks(t *testing.T) {
    t.Run("returns active tasks", func(t *testing.T) {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            assert.Equal(t, "/api/v1/tasks", r.URL.Path)
            assert.Equal(t, "org-repo", r.URL.Query().Get("repo"))
            assert.Equal(t, "123", r.URL.Query().Get("issue"))
            assert.Equal(t, "true", r.URL.Query().Get("active"))

            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusOK)
            _, _ = w.Write([]byte(`[{"id":"task-abc","status":{"phase":"Running"}}]`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        tasks, err := client.GetActiveTasks(context.Background(), "org-repo", "123")
        require.NoError(t, err)
        require.Len(t, tasks, 1)
        assert.Equal(t, "task-abc", tasks[0].ID)
        assert.Equal(t, "Running", tasks[0].Status.Phase)
    })

    t.Run("returns empty array", func(t *testing.T) {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusOK)
            _, _ = w.Write([]byte(`[]`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        tasks, err := client.GetActiveTasks(context.Background(), "org-repo", "123")
        require.NoError(t, err)
        assert.Empty(t, tasks)
    })
}

func TestAPIClient_GetTask(t *testing.T) {
    t.Run("returns task", func(t *testing.T) {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            assert.Equal(t, "/api/v1/tasks/task-abc", r.URL.Path)
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusOK)
            _, _ = w.Write([]byte(`{"id":"task-abc","status":{"phase":"Running"},"task":{"sourceURL":"https://github.com/org/repo/issues/42"}}`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        task, err := client.GetTask(context.Background(), "task-abc")
        require.NoError(t, err)
        assert.Equal(t, "task-abc", task.ID)
    })

    t.Run("handles 404", func(t *testing.T) {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusNotFound)
            _, _ = w.Write([]byte(`{"error":"task not found"}`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        _, err := client.GetTask(context.Background(), "nonexistent")
        require.Error(t, err)
        assert.Contains(t, err.Error(), "task not found")
    })
}

func TestAPIClient_CreateTask(t *testing.T) {
    t.Run("creates task successfully", func(t *testing.T) {
        var receivedReq api.CreateTaskRequest
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            assert.Equal(t, "/api/v1/tasks", r.URL.Path)
            assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

            _ = json.NewDecoder(r.Body).Decode(&receivedReq)

            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusCreated)
            _, _ = w.Write([]byte(`{"id":"task-xyz","status":{"phase":"Pending"}}`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        resp, err := client.CreateTask(context.Background(), api.CreateTaskRequest{
            Repo:     api.RepoRequest{URL: "https://github.com/org/repo"},
            Task:     api.TaskRequest{Description: "Fix bug"},
            Callback: "http://adapter/callback",
        })
        require.NoError(t, err)
        assert.Equal(t, "task-xyz", resp.ID)
        assert.Equal(t, "https://github.com/org/repo", receivedReq.Repo.URL)
    })

    t.Run("handles API error", func(t *testing.T) {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadRequest)
            _, _ = w.Write([]byte(`{"error":"repo.url is required"}`))
        }))
        defer srv.Close()

        client := NewAPIClient(srv.URL)
        _, err := client.CreateTask(context.Background(), api.CreateTaskRequest{})
        require.Error(t, err)
        assert.Contains(t, err.Error(), "repo.url is required")
    })
}
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes all tests
- [x] `go vet ./...` clean
- [x] `make lint-fix` passes
- [x] API client tests pass

#### Manual Verification:
- [ ] Adapter creates tasks via API
- [ ] Deduplication works (second mention posts "already running")

**Pause for review before Phase 5.**

---

## Phase 5: Callback Handler

### Overview

Implement the callback endpoint that receives notifications from the Shepherd API and posts appropriate GitHub comments.

### Changes Required

#### 1. Callback Handler

**File**: `pkg/adapters/github/callback.go`

**Key design decision**: The callback handler is **stateless-safe**. It maintains an
in-memory cache for fast lookup, but if a callback arrives for an unknown task (e.g.,
after a pod restart), it queries the Shepherd API to recover the task metadata from
`sourceURL` and labels. This means no callbacks are silently dropped on restart.

Uses `api.CallbackPayload` imported from `pkg/api` — no local type duplication.

```go
package github

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "sync"

    "github.com/go-logr/logr"

    "github.com/NissesSenap/shepherd/pkg/api"
)

// TaskMetadata stores information about active tasks for callback handling.
type TaskMetadata struct {
    Owner       string
    Repo        string
    IssueNumber int
}

// CallbackHandler handles callbacks from the Shepherd API.
type CallbackHandler struct {
    secret    string
    ghClient  *Client
    apiClient *APIClient
    log       logr.Logger

    // In-memory cache for fast lookup; API fallback handles restarts
    tasksMu sync.RWMutex
    tasks   map[string]TaskMetadata
}

// NewCallbackHandler creates a new callback handler.
func NewCallbackHandler(secret string, ghClient *Client, apiClient *APIClient, log logr.Logger) *CallbackHandler {
    return &CallbackHandler{
        secret:    secret,
        ghClient:  ghClient,
        apiClient: apiClient,
        log:       log,
        tasks:     make(map[string]TaskMetadata),
    }
}

// RegisterTask stores metadata about a task for later callback handling.
func (h *CallbackHandler) RegisterTask(taskID string, meta TaskMetadata) {
    h.tasksMu.Lock()
    defer h.tasksMu.Unlock()
    h.tasks[taskID] = meta
}

// ServeHTTP handles callback requests from the API.
func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Read body
    body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
    if err != nil {
        h.log.Error(err, "failed to read callback body")
        http.Error(w, "failed to read body", http.StatusBadRequest)
        return
    }

    // Verify HMAC signature
    signature := r.Header.Get("X-Shepherd-Signature")
    if !h.verifySignature(body, signature) {
        h.log.Info("callback signature verification failed")
        http.Error(w, "invalid signature", http.StatusUnauthorized)
        return
    }

    // Parse payload (uses api.CallbackPayload from pkg/api)
    var payload api.CallbackPayload
    if err := json.Unmarshal(body, &payload); err != nil {
        h.log.Error(err, "failed to parse callback payload")
        http.Error(w, "invalid payload", http.StatusBadRequest)
        return
    }

    h.log.Info("received callback", "taskID", payload.TaskID, "event", payload.Event)

    // Handle the callback
    h.handleCallback(r.Context(), &payload)

    w.WriteHeader(http.StatusOK)
}

// verifySignature verifies the HMAC-SHA256 signature from the API.
func (h *CallbackHandler) verifySignature(body []byte, signature string) bool {
    if h.secret == "" {
        return true // No verification if no secret
    }

    if !strings.HasPrefix(signature, "sha256=") {
        return false
    }

    expectedMAC := hmac.New(sha256.New, []byte(h.secret))
    expectedMAC.Write(body)
    expected := "sha256=" + hex.EncodeToString(expectedMAC.Sum(nil))

    return hmac.Equal([]byte(expected), []byte(signature))
}

// resolveTaskMetadata looks up task metadata from cache, falling back to
// the Shepherd API if not found (e.g., after a restart).
func (h *CallbackHandler) resolveTaskMetadata(ctx context.Context, taskID string) (TaskMetadata, bool) {
    // Check in-memory cache first
    h.tasksMu.RLock()
    meta, ok := h.tasks[taskID]
    h.tasksMu.RUnlock()
    if ok {
        return meta, true
    }

    // Fallback: query the Shepherd API for task details
    task, err := h.apiClient.GetTask(ctx, taskID)
    if err != nil {
        h.log.Error(err, "failed to fetch task from API for callback", "taskID", taskID)
        return TaskMetadata{}, false
    }

    // Parse owner/repo/issue from sourceURL (e.g., "https://github.com/org/repo/issues/42")
    meta, err = parseSourceURL(task.Task.SourceURL)
    if err != nil {
        h.log.Error(err, "failed to parse sourceURL from task", "taskID", taskID, "sourceURL", task.Task.SourceURL)
        return TaskMetadata{}, false
    }

    // Cache for future callbacks on the same task
    h.RegisterTask(taskID, meta)
    h.log.Info("recovered task metadata from API", "taskID", taskID, "owner", meta.Owner, "repo", meta.Repo, "issue", meta.IssueNumber)
    return meta, true
}

// parseSourceURL extracts owner, repo, and issue number from a GitHub issue URL.
// Expected format: https://github.com/{owner}/{repo}/issues/{number}
func parseSourceURL(sourceURL string) (TaskMetadata, error) {
    if sourceURL == "" {
        return TaskMetadata{}, fmt.Errorf("empty sourceURL")
    }
    u, err := url.Parse(sourceURL)
    if err != nil {
        return TaskMetadata{}, fmt.Errorf("invalid sourceURL: %w", err)
    }
    // Path: /owner/repo/issues/42
    parts := strings.Split(strings.Trim(u.Path, "/"), "/")
    if len(parts) < 4 || parts[2] != "issues" {
        return TaskMetadata{}, fmt.Errorf("unexpected sourceURL format: %s", sourceURL)
    }
    issueNumber, err := strconv.Atoi(parts[3])
    if err != nil {
        return TaskMetadata{}, fmt.Errorf("invalid issue number in sourceURL: %w", err)
    }
    return TaskMetadata{
        Owner:       parts[0],
        Repo:        parts[1],
        IssueNumber: issueNumber,
    }, nil
}

// handleCallback processes the callback and posts appropriate GitHub comments.
func (h *CallbackHandler) handleCallback(ctx context.Context, payload *api.CallbackPayload) {
    // Look up task metadata (cache + API fallback)
    meta, ok := h.resolveTaskMetadata(ctx, payload.TaskID)
    if !ok {
        h.log.Info("unable to resolve task metadata, cannot post comment", "taskID", payload.TaskID)
        return
    }

    var comment string
    switch payload.Event {
    case "completed":
        prURL := ""
        if v, ok := payload.Details["prURL"].(string); ok {
            prURL = v
        }
        if prURL != "" {
            comment = formatCompleted(prURL)
        } else {
            comment = "Shepherd completed the task successfully."
        }

        // Clean up task metadata
        h.tasksMu.Lock()
        delete(h.tasks, payload.TaskID)
        h.tasksMu.Unlock()

    case "failed":
        errorMsg := payload.Message
        if v, ok := payload.Details["error"].(string); ok && v != "" {
            errorMsg = v
        }
        comment = formatFailed(errorMsg)

        // Clean up task metadata
        h.tasksMu.Lock()
        delete(h.tasks, payload.TaskID)
        h.tasksMu.Unlock()

    case "started", "progress":
        // Don't post comments for intermediate events per user requirement
        h.log.V(1).Info("ignoring intermediate event", "event", payload.Event)
        return

    default:
        h.log.Info("unknown event type", "event", payload.Event)
        return
    }

    if err := h.ghClient.PostComment(ctx, meta.Owner, meta.Repo, meta.IssueNumber, comment); err != nil {
        h.log.Error(err, "failed to post callback comment",
            "taskID", payload.TaskID,
            "event", payload.Event,
        )
    }
}
```

#### 2. Integrate CallbackHandler into Server

**File**: `pkg/adapters/github/server.go`

The WebhookHandler already has `callbackHandler` field (wired in Phase 4).
The `processTask` method already calls `RegisterTask` (added in Phase 4).
Phase 5 just adds the callback endpoint route:

```go
// Mount callback endpoint with content-type validation
r.With(requireJSON).Post("/callback", callbackHandler.ServeHTTP)
```

#### 3. Unit Tests

**File**: `pkg/adapters/github/callback_test.go`

```go
package github

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    ctrl "sigs.k8s.io/controller-runtime"
)

func TestCallbackHandler_SignatureVerification(t *testing.T) {
    secret := "callback-secret"
    handler := NewCallbackHandler(secret, nil, nil, ctrl.Log.WithName("test"))

    t.Run("valid signature", func(t *testing.T) {
        body := []byte(`{"taskID":"abc","event":"completed"}`)
        mac := hmac.New(sha256.New, []byte(secret))
        mac.Write(body)
        sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

        assert.True(t, handler.verifySignature(body, sig))
    })

    t.Run("invalid signature", func(t *testing.T) {
        body := []byte(`{"taskID":"abc","event":"completed"}`)
        assert.False(t, handler.verifySignature(body, "sha256=invalid"))
    })
}

func TestCallbackHandler_ServeHTTP(t *testing.T) {
    secret := "callback-secret"

    t.Run("rejects invalid signature", func(t *testing.T) {
        handler := NewCallbackHandler(secret, nil, nil, ctrl.Log.WithName("test"))

        body := []byte(`{"taskID":"abc","event":"completed"}`)
        req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
        req.Header.Set("X-Shepherd-Signature", "sha256=invalid")
        w := httptest.NewRecorder()

        handler.ServeHTTP(w, req)
        assert.Equal(t, http.StatusUnauthorized, w.Code)
    })
}

func TestCallbackHandler_TaskMetadata(t *testing.T) {
    handler := NewCallbackHandler("", nil, nil, ctrl.Log.WithName("test"))

    // Register a task
    handler.RegisterTask("task-123", TaskMetadata{
        Owner:       "test-org",
        Repo:        "test-repo",
        IssueNumber: 42,
    })

    // Verify it's stored
    handler.tasksMu.RLock()
    meta, ok := handler.tasks["task-123"]
    handler.tasksMu.RUnlock()

    assert.True(t, ok)
    assert.Equal(t, "test-org", meta.Owner)
    assert.Equal(t, "test-repo", meta.Repo)
    assert.Equal(t, 42, meta.IssueNumber)
}

func TestParseSourceURL(t *testing.T) {
    t.Run("valid issue URL", func(t *testing.T) {
        meta, err := parseSourceURL("https://github.com/myorg/myrepo/issues/42")
        require.NoError(t, err)
        assert.Equal(t, "myorg", meta.Owner)
        assert.Equal(t, "myrepo", meta.Repo)
        assert.Equal(t, 42, meta.IssueNumber)
    })

    t.Run("empty URL", func(t *testing.T) {
        _, err := parseSourceURL("")
        require.Error(t, err)
    })

    t.Run("non-issue URL", func(t *testing.T) {
        _, err := parseSourceURL("https://github.com/myorg/myrepo/pull/42")
        require.Error(t, err)
    })

    t.Run("invalid issue number", func(t *testing.T) {
        _, err := parseSourceURL("https://github.com/myorg/myrepo/issues/abc")
        require.Error(t, err)
    })
}
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes all tests
- [x] `go vet ./...` clean
- [x] `make lint-fix` passes
- [x] Callback signature verification tests pass

#### Manual Verification:
- [ ] Callback endpoint accepts valid callbacks from API
- [ ] Completed events post PR link comment
- [ ] Failed events post error comment
- [ ] started/progress events are ignored (no comment)

**Pause for review before Phase 6.**

---

## Phase 6: Refactor pkg/api/github_token.go (Runner App)

### Overview

Replace the custom GitHub token implementation in the API server with ghinstallation library. This code stays in `pkg/api/` because it serves the **Runner App** - the API generates tokens for runners to clone repos and create PRs, regardless of which adapter triggered the task.

**Note**: This is separate from the Trigger App client in `pkg/adapters/github/` which handles webhooks and comments.

**What ghinstallation replaces**: The current code in `pkg/api/github_token.go` has three
functions that manually implement the GitHub App authentication flow:
1. `readPrivateKey()` - reads and parses RSA PEM files (PKCS1/PKCS8)
2. `createJWT()` - creates a GitHub App JWT using `golang-jwt/jwt/v5`
3. `exchangeToken()` - POSTs the JWT to GitHub's API to get an installation access token

The `ghinstallation` library does **all three** of these internally. You create a
`ghinstallation.Transport` with the private key bytes, and its `Token(ctx)` /
`TokenWithOptions(ctx, opts)` methods handle JWT creation, exchange, and token caching
transparently. This means all three functions are deleted and `golang-jwt/jwt/v5`
becomes unused.

### Changes Required

#### 1. Update pkg/api/github_token.go

Replace the custom implementation with ghinstallation:

```go
package api

import (
    "context"
    "fmt"
    "net/http"
    "net/url"
    "os"
    "strings"
    "time"

    "github.com/bradleyfalzon/ghinstallation/v2"
)

// GitHubClient wraps GitHub API operations using ghinstallation.
type GitHubClient struct {
    transport *ghinstallation.Transport
}

// NewGitHubClient creates a new GitHub client from app credentials.
func NewGitHubClient(appID, installationID int64, privateKeyPath string) (*GitHubClient, error) {
    keyData, err := os.ReadFile(privateKeyPath)
    if err != nil {
        return nil, fmt.Errorf("reading private key: %w", err)
    }

    transport, err := ghinstallation.New(http.DefaultTransport, appID, installationID, keyData)
    if err != nil {
        return nil, fmt.Errorf("creating installation transport: %w", err)
    }

    return &GitHubClient{transport: transport}, nil
}

// GetToken returns a token for the installation, optionally scoped to a repository.
func (c *GitHubClient) GetToken(ctx context.Context, repoURL string) (string, time.Time, error) {
    // Optionally scope to repository
    if repoURL != "" {
        repoName, err := parseRepoName(repoURL)
        if err != nil {
            return "", time.Time{}, err
        }
        opts := &ghinstallation.IssueAccessTokenOptions{
            Repositories: []string{repoName},
        }
        token, err := c.transport.TokenWithOptions(ctx, opts)
        if err != nil {
            return "", time.Time{}, fmt.Errorf("getting scoped token: %w", err)
        }
        // ghinstallation tokens are valid for 1 hour
        return token, time.Now().Add(time.Hour), nil
    }

    token, err := c.transport.Token(ctx)
    if err != nil {
        return "", time.Time{}, fmt.Errorf("getting token: %w", err)
    }
    return token, time.Now().Add(time.Hour), nil
}

// parseRepoName extracts "repo" from "https://github.com/org/repo.git".
func parseRepoName(repoURL string) (string, error) {
    if repoURL == "" {
        return "", fmt.Errorf("repo URL is required")
    }
    u, err := url.Parse(repoURL)
    if err != nil {
        return "", fmt.Errorf("invalid repo URL: %w", err)
    }
    parts := strings.Split(strings.Trim(u.Path, "/"), "/")
    if len(parts) != 2 {
        return "", fmt.Errorf("repo URL must be owner/repo format: %s", repoURL)
    }
    return strings.TrimSuffix(parts[1], ".git"), nil
}
```

#### 2. Update handler_token.go

Update to use the new GitHubClient:

```go
// In taskHandler struct, replace:
// githubKey       *rsa.PrivateKey
// With:
githubClient *GitHubClient

// In getTaskToken handler:
token, expiresAt, err := h.githubClient.GetToken(r.Context(), task.Spec.Repo.URL)
if err != nil {
    writeError(w, http.StatusBadGateway, "failed to get GitHub token", "")
    return
}

writeJSON(w, http.StatusOK, TokenResponse{
    Token:     token,
    ExpiresAt: expiresAt.Format(time.RFC3339),
})
```

#### 3. Update server.go

Update client initialization:

```go
// Replace:
// githubKey, err := readPrivateKey(opts.GithubPrivateKeyPath)

// With:
var githubClient *GitHubClient
if opts.GithubPrivateKeyPath != "" {
    var err error
    githubClient, err = NewGitHubClient(opts.GithubAppID, opts.GithubInstallationID, opts.GithubPrivateKeyPath)
    if err != nil {
        return fmt.Errorf("creating github client: %w", err)
    }
}

// Update handler creation:
handler := &taskHandler{
    client:       k8sClient,
    namespace:    opts.Namespace,
    callback:     cb,
    githubClient: githubClient,
    httpClient:   &http.Client{Timeout: 30 * time.Second},
}
```

#### 4. Remove Old Functions

Remove from `pkg/api/github_token.go`:
- `readPrivateKey()` - replaced by `os.ReadFile()` + passing bytes to ghinstallation
- `createJWT()` - replaced by ghinstallation's internal JWT creation
- `exchangeToken()` - replaced by `transport.Token()` / `transport.TokenWithOptions()`

Keep `parseRepoName()` — still needed to extract repo name for token scoping.

#### 5. Clean Up Dependencies

```bash
go mod tidy
```

This removes `github.com/golang-jwt/jwt/v5` from `go.mod` since it is no longer
directly used (ghinstallation handles JWT internally).

#### 6. Update Tests

Update `pkg/api/handler_token_test.go` to use the new client pattern.

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests (including updated token tests)
- [ ] `go vet ./...` clean
- [ ] `make lint-fix` passes

#### Manual Verification:
- [ ] Token endpoint still works with real GitHub App
- [ ] Tokens are correctly scoped to repositories

---

## Testing Strategy

### Unit Tests (testify + httptest)

**Webhook Handler**:
- Signature verification (valid, invalid, missing prefix, empty secret)
- Event type routing (issue_comment, ping, unknown)
- @shepherd mention regex matching
- Comment body parsing

**API Client**:
- GetActiveTasks request formatting and response parsing
- CreateTask request/response handling
- Error response handling

**Callback Handler**:
- Signature verification
- Event handling (completed, failed, started, progress)
- Task metadata registration and cleanup

**GitHub Client**:
- Comment posting (mock server)
- Issue fetching
- Comment listing with pagination

### Integration Tests

- Full webhook → API → callback → comment flow
- Deduplication prevents duplicate tasks
- Concurrent webhooks handled correctly

### Test Patterns to Follow

From `pkg/api/` tests:
- `httptest.NewServer` for mock servers
- `fake.NewClientBuilder()` for K8s client
- Table-driven tests for regex and validation
- `atomic.Bool` for concurrent testing
- `t.Helper()` in test helpers

## Performance Considerations

- HTTP client timeouts (30s for API, 10s for GitHub)
- Connection pooling via http.DefaultTransport
- Pagination for comment fetching
- In-memory task metadata cache with API fallback for stateless recovery
- Context truncation at 1MB to stay within API's compressed context limit
- IP-based rate limiting (100 req/min) on webhook endpoint via httprate

## Security Considerations

- Webhook signature verification (X-Hub-Signature-256)
- Callback signature verification (X-Shepherd-Signature)
- Constant-time comparison for HMAC (hmac.Equal)
- Private key loaded from file, not environment variable
- No secrets logged
- Content-Type validation on POST endpoints (requireJSON middleware)
- IP-based rate limiting on public webhook endpoint to mitigate DoS
- @shepherd regex excludes email-style patterns to prevent false triggers

## References

- Research doc: `thoughts/research/2026-02-05-github-adapter-research.md`
- Design doc: `thoughts/research/2026-01-27-shepherd-design.md`
- API patterns: `pkg/api/server.go`
- go-github: https://github.com/google/go-github
- ghinstallation: https://github.com/bradleyfalzon/ghinstallation
