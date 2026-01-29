# Shepherd Init Container Implementation Plan

## Overview

Implement the `shepherd-init` binary — the init container that runs before the main runner container in every AgentTask Job. It performs two tasks:

1. **Task file preparation**: Reads task description and context from env vars, decompresses gzip+base64-encoded context if needed, writes files for the runner container.
2. **GitHub token generation**: Reads the Runner App private key, generates a short-lived GitHub installation access token (1 hour, non-configurable by GitHub), scopes it to the target repo, and writes it to a shared volume.

The init container is a separate Go module with minimal dependencies (stdlib + `golang-jwt/jwt/v5`), built with ko.

## Current State Analysis

The operator's [job_builder.go](internal/controller/job_builder.go) already defines the init container contract:

- **Image**: Configurable via `SHEPHERD_INIT_IMAGE` (default `shepherd-init:latest`)
- **Env vars**: `REPO_URL`, `TASK_DESCRIPTION`, `REPO_REF` (optional), `TASK_CONTEXT` (optional), `CONTEXT_ENCODING` (optional)
- **Volume mounts**:
  - `/creds` (EmptyDir, writable) — for GitHub token output
  - `/secrets/runner-app-key` (Secret, readonly) — Runner App private key
  - `/task` (EmptyDir, writable, 10Mi limit) — for task files
- **Runner reads**: `/task/description.txt`, `/task/context.txt`, `/creds/` (all readonly)

The `images/` directory does not exist. No init container code exists.

### Key Discoveries:
- GitHub App installation tokens are valid for exactly **1 hour** (non-configurable by GitHub)
- The JWT used to request the token has a max lifetime of **10 minutes**
- The `ghinstallation` library is designed for ongoing HTTP transport use (auto-refresh); we only need a one-shot token, so stdlib + `golang-jwt` is sufficient
- The operator already passes `REPO_URL` — we can parse this to scope the token to one repo

## Desired End State

A working `shepherd-init` binary that:
- Reads env vars and writes `/task/description.txt` and `/task/context.txt` (decompressing gzip if needed)
- Reads the Runner App private key from `/secrets/runner-app-key/private-key.pem`
- Generates a JWT, exchanges it for an installation token scoped to the target repo
- Writes the token to `/creds/token`
- Is built as a separate Go module with minimal dependencies
- Has comprehensive unit tests
- Builds via `make ko-build-init`

### How to Verify

- `cd cmd/shepherd-init && go test ./...` passes all tests
- `make ko-build-init` builds the init container image
- `make build-smoke` includes init container build
- The init container runs in a Job, writes expected files, and the runner container can read them

## What We're NOT Doing

- Runner image implementation (separate plan)
- `gh` CLI installation in the init container (runner image responsibility)
- Git credential helper configuration (runner image entrypoint responsibility)
- GitHub Enterprise support beyond a configurable base URL
- Token refresh (token is generated once; the Job should complete within 1 hour)
- Configurable secret key names (convention: `private-key.pem`)

## Implementation Approach

Separate Go module at `cmd/shepherd-init/` with its own `go.mod`. The main shepherd binary has heavy K8s dependencies (controller-runtime, client-go, etc.). The init container only needs stdlib + one JWT library. Separate modules keep the init container image small and the dependency surface minimal.

Build with ko alongside the operator. The operator passes GitHub App config (App ID, Installation ID) as env vars to the init container.

---

## Phase 1: Module Scaffold + Task File Writing

### Overview

Create the separate Go module, implement task file preparation (description + context decompression). This is pure stdlib — no external dependencies yet.

### Changes Required

#### 1. Module Scaffold

**File**: `cmd/shepherd-init/go.mod`

```go
module github.com/NissesSenap/shepherd/cmd/shepherd-init

go 1.25.3
```

**File**: `cmd/shepherd-init/main.go`

```go
package main

import (
    "fmt"
    "log/slog"
    "os"
)

func main() {
    if err := run(); err != nil {
        slog.Error("shepherd-init failed", "error", err)
        os.Exit(1)
    }
}

func run() error {
    slog.Info("shepherd-init starting")

    if err := writeTaskFiles(); err != nil {
        return fmt.Errorf("writing task files: %w", err)
    }

    if err := generateGitHubToken(); err != nil {
        return fmt.Errorf("generating github token: %w", err)
    }

    slog.Info("shepherd-init completed successfully")
    return nil
}
```

#### 2. Task File Writing

**File**: `cmd/shepherd-init/taskfiles.go`

Responsibilities:
- Read `TASK_DESCRIPTION` env var → write to `/task/description.txt`
- Read `TASK_CONTEXT` env var → if `CONTEXT_ENCODING` is `"gzip"`, base64-decode then gzip-decompress → write to `/task/context.txt`
- If `TASK_CONTEXT` is empty, write empty file (runner checks file existence)

```go
package main

import (
    "bytes"
    "compress/gzip"
    "encoding/base64"
    "fmt"
    "io"
    "log/slog"
    "os"
    "path/filepath"
)

const (
    taskDir             = "/task"
    descriptionFilename = "description.txt"
    contextFilename     = "context.txt"
)

func writeTaskFiles() error {
    desc := os.Getenv("TASK_DESCRIPTION")
    if desc == "" {
        return fmt.Errorf("TASK_DESCRIPTION is required")
    }

    descPath := filepath.Join(taskDir, descriptionFilename)
    descData := []byte(desc)
    if err := writeFile(descPath, descData, 0644); err != nil {
        return fmt.Errorf("writing description: %w", err)
    }
    slog.Info("wrote task file", "path", descPath, "bytes", len(descData))

    context := os.Getenv("TASK_CONTEXT")
    if context == "" {
        // Write empty file so runner doesn't need to check existence
        contextPath := filepath.Join(taskDir, contextFilename)
        if err := writeFile(contextPath, nil, 0644); err != nil {
            return fmt.Errorf("writing empty context: %w", err)
        }
        slog.Info("wrote task file", "path", contextPath, "bytes", 0)
        return nil
    }

    encoding := os.Getenv("CONTEXT_ENCODING")
    data, err := decodeContext(context, encoding)
    if err != nil {
        return fmt.Errorf("decoding context: %w", err)
    }

    contextPath := filepath.Join(taskDir, contextFilename)
    if err := writeFile(contextPath, data, 0644); err != nil {
        return fmt.Errorf("writing context: %w", err)
    }
    slog.Info("wrote task file", "path", contextPath, "bytes", len(data))

    return nil
}

func decodeContext(raw, encoding string) ([]byte, error) {
    if encoding != "gzip" {
        // Plaintext — return as-is
        return []byte(raw), nil
    }

    // base64-decode, then gzip-decompress
    compressed, err := base64.StdEncoding.DecodeString(raw)
    if err != nil {
        return nil, fmt.Errorf("base64 decode: %w", err)
    }

    gr, err := gzip.NewReader(bytes.NewReader(compressed))
    if err != nil {
        return nil, fmt.Errorf("gzip reader: %w", err)
    }
    defer gr.Close()

    decompressed, err := io.ReadAll(gr)
    if err != nil {
        return nil, fmt.Errorf("gzip decompress: %w", err)
    }

    return decompressed, nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
    return os.WriteFile(path, data, perm)
}
```

#### 3. Unit Tests

**File**: `cmd/shepherd-init/taskfiles_test.go`

```go
// Test: TASK_DESCRIPTION is written to description.txt with correct permissions
// Test: Empty TASK_DESCRIPTION returns error
// Test: Empty TASK_CONTEXT writes empty context.txt
// Test: Plaintext TASK_CONTEXT (no encoding) written as-is
// Test: Gzip-encoded TASK_CONTEXT is base64-decoded then gzip-decompressed
// Test: Invalid base64 returns error
// Test: Invalid gzip data returns error
// Test: decodeContext with empty encoding returns raw bytes
// Test: decodeContext with "gzip" encoding decompresses correctly
// Test: writeFile respects permission parameter (0644 for task files, 0600 for token)
```

Use `t.TempDir()` to avoid needing real `/task` mount — make `taskDir` configurable via a parameter or test helper.

### Success Criteria

#### Automated Verification:
- [x] `cd cmd/shepherd-init && go build .` compiles
- [x] `cd cmd/shepherd-init && go test ./...` passes all tests
- [x] `cd cmd/shepherd-init && go vet ./...` clean

#### Manual Verification:
- [ ] Review that gzip decompression matches what the API will produce (base64(gzip(plaintext)))

**Pause for manual review before Phase 2.**

---

## Phase 2: GitHub Token Generation

### Overview

Implement GitHub App installation token generation. Read private key, sign JWT, exchange for installation token scoped to the target repo.

### Background: GitHub App Token Flow

1. Read RSA private key (PEM format) from mounted Secret volume
2. Create JWT with claims: `iss` = App ID, `iat` = now-60s, `exp` = now+10m
3. Sign JWT with RS256
4. POST to `https://api.github.com/app/installations/{id}/access_tokens` with `Authorization: Bearer {jwt}`
5. Optionally restrict to specific repositories via request body
6. Parse response for `token` field
7. Write token to `/creds/token`

Token is valid for **1 hour** (GitHub hardcoded, not configurable).

### Changes Required

#### 1. Add JWT Dependency

```bash
cd cmd/shepherd-init
go get github.com/golang-jwt/jwt/v5
```

#### 2. GitHub Token Generator

**File**: `cmd/shepherd-init/github.go`

```go
package main

import (
    "bytes"
    "crypto/rsa"
    "crypto/x509"
    "encoding/json"
    "encoding/pem"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/golang-jwt/jwt/v5"
)

const (
    credsDir       = "/creds"
    tokenFilename  = "token"
    secretDir      = "/secrets/runner-app-key"
    keyFilename    = "private-key.pem"
    defaultBaseURL = "https://api.github.com"
)

// githubConfig holds all configuration needed for token generation.
type githubConfig struct {
    AppID          int64
    InstallationID int64
    BaseURL        string
    PrivateKeyPath string
    RepoURL        string // For token scoping
    TokenPath      string // Output path
}

func githubConfigFromEnv() (githubConfig, error) {
    appID, err := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
    if err != nil {
        return githubConfig{}, fmt.Errorf("GITHUB_APP_ID invalid or missing: %w", err)
    }

    installID, err := strconv.ParseInt(os.Getenv("GITHUB_INSTALLATION_ID"), 10, 64)
    if err != nil {
        return githubConfig{}, fmt.Errorf("GITHUB_INSTALLATION_ID invalid or missing: %w", err)
    }

    baseURL := os.Getenv("GITHUB_API_URL")
    if baseURL == "" {
        baseURL = defaultBaseURL
    }

    return githubConfig{
        AppID:          appID,
        InstallationID: installID,
        BaseURL:        baseURL,
        PrivateKeyPath: filepath.Join(secretDir, keyFilename),
        RepoURL:        os.Getenv("REPO_URL"),
        TokenPath:      filepath.Join(credsDir, tokenFilename),
    }, nil
}

func generateGitHubToken() error {
    slog.Info("generating GitHub installation token")

    cfg, err := githubConfigFromEnv()
    if err != nil {
        return err
    }

    key, err := readPrivateKey(cfg.PrivateKeyPath)
    if err != nil {
        return fmt.Errorf("reading private key: %w", err)
    }

    jwtToken, err := createJWT(cfg.AppID, key)
    if err != nil {
        return fmt.Errorf("creating JWT: %w", err)
    }

    repoName, err := parseRepoName(cfg.RepoURL)
    if err != nil {
        return fmt.Errorf("parsing repo URL: %w", err)
    }

    token, err := exchangeToken(cfg.BaseURL, cfg.InstallationID, jwtToken, repoName)
    if err != nil {
        return fmt.Errorf("exchanging token: %w", err)
    }

    if err := writeFile(cfg.TokenPath, []byte(token), 0600); err != nil {
        return fmt.Errorf("writing token: %w", err)
    }

    slog.Info("GitHub installation token generated successfully")
    return nil
}

// Note: The token is written with mode 0600 (owner read-write only). The Job spec must ensure
// both init and runner containers run as the same UID (via securityContext.runAsUser), or use
// group-readable permissions (0640) with a shared fsGroup in the pod security context. The
// operator's job_builder.go should enforce this to ensure the runner container can read the token.

func readPrivateKey(path string) (*rsa.PrivateKey, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("reading key file: %w", err)
    }

    block, _ := pem.Decode(data)
    if block == nil {
        return nil, fmt.Errorf("no PEM block found in %s", path)
    }

    key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
    if err != nil {
        // Try PKCS8 format as fallback
        parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
        if err2 != nil {
            return nil, fmt.Errorf("parsing private key (tried PKCS1 and PKCS8): %w", err)
        }
        rsaKey, ok := parsed.(*rsa.PrivateKey)
        if !ok {
            return nil, fmt.Errorf("private key is not RSA")
        }
        return rsaKey, nil
    }
    return key, nil
}

func createJWT(appID int64, key *rsa.PrivateKey) (string, error) {
    now := time.Now()
    claims := jwt.RegisteredClaims{
        Issuer:    strconv.FormatInt(appID, 10),
        IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // Clock drift tolerance
        ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),  // GitHub max: 10 min
    }

    token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
    return token.SignedString(key)
}

// parseRepoName extracts "repo" from "https://github.com/org/repo.git" or "https://github.com/org/repo".
// Returns error if repoURL is non-empty but malformed.
func parseRepoName(repoURL string) (string, error) {
    if repoURL == "" {
        return "", nil
    }
    u, err := url.Parse(repoURL)
    if err != nil {
        return "", fmt.Errorf("invalid repo URL: %w", err)
    }
    parts := strings.Split(strings.Trim(u.Path, "/"), "/")
    if len(parts) < 2 {
        return "", fmt.Errorf("repo URL must contain owner/repo: %s", repoURL)
    }
    name := parts[1]
    return strings.TrimSuffix(name, ".git"), nil
}

// exchangeToken calls GitHub API to exchange JWT for an installation access token.
// If repoName is non-empty, the token is scoped to that repository only.
func exchangeToken(baseURL string, installationID int64, jwtToken, repoName string) (string, error) {
    endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)

    // Build request body — scope to repo if provided
    var bodyReader io.Reader
    if repoName != "" {
        body := map[string]interface{}{
            "repositories": []string{repoName},
        }
        bodyBytes, err := json.Marshal(body)
        if err != nil {
            return "", fmt.Errorf("marshaling request body: %w", err)
        }
        bodyReader = bytes.NewReader(bodyBytes)
    }

    req, err := http.NewRequest("POST", endpoint, bodyReader)
    if err != nil {
        return "", fmt.Errorf("creating request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+jwtToken)
    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("User-Agent", "shepherd-init")
    if bodyReader != nil {
        req.Header.Set("Content-Type", "application/json")
    }

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", fmt.Errorf("POST %s: %w", endpoint, err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("reading response: %w", err)
    }

    if resp.StatusCode != http.StatusCreated {
        return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
    }

    var result struct {
        Token string `json:"token"`
    }
    if err := json.Unmarshal(respBody, &result); err != nil {
        return "", fmt.Errorf("parsing response: %w", err)
    }
    if result.Token == "" {
        return "", fmt.Errorf("empty token in response")
    }

    return result.Token, nil
}
```

#### 3. Unit Tests

**File**: `cmd/shepherd-init/github_test.go`

```go
// Test: parseRepoName("https://github.com/org/repo.git") → ("repo", nil)
// Test: parseRepoName("https://github.com/org/repo") → ("repo", nil)
// Test: parseRepoName("") → ("", nil)
// Test: parseRepoName("https://github.com/org") → ("", error) (malformed)
// Test: parseRepoName("not-a-url") → ("", error) (invalid URL)
// Test: readPrivateKey reads PKCS1 PEM key
// Test: readPrivateKey reads PKCS8 PEM key
// Test: readPrivateKey returns error for non-PEM file
// Test: readPrivateKey returns error for non-RSA key
// Test: createJWT produces a valid RS256 JWT with correct claims
// Test: createJWT sets iat 60 seconds in the past
// Test: createJWT sets exp 10 minutes in the future
// Test: exchangeToken sends correct Authorization header
// Test: exchangeToken sends repositories scope when repoName is provided
// Test: exchangeToken sends no body when repoName is empty
// Test: exchangeToken returns error on non-201 response
// Test: exchangeToken returns error on empty token in response
// Test: exchangeToken respects 30-second timeout
// Test: githubConfigFromEnv reads all env vars correctly
// Test: githubConfigFromEnv defaults BaseURL to api.github.com
// Test: githubConfigFromEnv returns error for missing GITHUB_APP_ID
// Test: githubConfigFromEnv returns error for missing GITHUB_INSTALLATION_ID
```

Use `httptest.NewServer` for exchange token tests — mock the GitHub API endpoint. Use `crypto/rsa` `GenerateKey` to create test keys.

**Note on testability**: The HTTP client with 30-second timeout is created inline in `exchangeToken`. For more advanced testing (e.g., timeout scenarios with custom test servers), consider making the `*http.Client` injectable via the `githubConfig` struct in future iterations.

### Success Criteria

#### Automated Verification:
- [x] `cd cmd/shepherd-init && go build .` compiles
- [x] `cd cmd/shepherd-init && go test ./...` passes all tests
- [x] `cd cmd/shepherd-init && go vet ./...` clean
- [x] JWT claims verified in tests (iss, iat, exp)
- [x] Token scoping (repositories field) verified in tests

#### Manual Verification:
- [ ] Review JWT creation matches GitHub's requirements (RS256, correct claims)
- [ ] Review token scoping logic is correct

**Pause for manual review before Phase 3.**

---

## Phase 3: Operator Integration

### Overview

Update the operator to pass GitHub App configuration to the init container as env vars. Add new operator flags and update the job builder.

### Changes Required

#### 1. Operator Command Flags

**File**: `cmd/shepherd/operator.go`

Add two new flags:

```go
type OperatorCmd struct {
    // ... existing fields ...
    GithubAppID          int64  `help:"GitHub Runner App ID" required:"" env:"SHEPHERD_GITHUB_APP_ID"`
    GithubInstallationID int64  `help:"GitHub Runner App installation ID" required:"" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
    GithubAPIURL         string `help:"GitHub API base URL" default:"https://api.github.com" env:"SHEPHERD_GITHUB_API_URL"`
}
```

Pass to operator options:

```go
func (c *OperatorCmd) Run(_ *CLI) error {
    return operator.Run(operator.Options{
        // ... existing fields ...
        GithubAppID:          c.GithubAppID,
        GithubInstallationID: c.GithubInstallationID,
        GithubAPIURL:         c.GithubAPIURL,
    })
}
```

#### 2. Operator Options

**File**: `pkg/operator/operator.go`

Add fields to `Options` struct and pass to reconciler:

```go
type Options struct {
    // ... existing fields ...
    GithubAppID          int64
    GithubInstallationID int64
    GithubAPIURL         string
}
```

Pass to `AgentTaskReconciler`:

```go
&controller.AgentTaskReconciler{
    // ... existing fields ...
    GithubAppID:          opts.GithubAppID,
    GithubInstallationID: opts.GithubInstallationID,
    GithubAPIURL:         opts.GithubAPIURL,
}
```

#### 3. Reconciler Fields

**File**: `internal/controller/agenttask_controller.go`

Add fields:

```go
type AgentTaskReconciler struct {
    // ... existing fields ...
    GithubAppID          int64
    GithubInstallationID int64
    GithubAPIURL         string
}
```

#### 4. Job Builder Config + Env Vars

**File**: `internal/controller/job_builder.go`

Add to `jobConfig`:

```go
type jobConfig struct {
    // ... existing fields ...
    GithubAppID          int64
    GithubInstallationID int64
    GithubAPIURL         string
}
```

Add env vars to init container:

```go
initEnv := []corev1.EnvVar{
    {Name: "REPO_URL", Value: task.Spec.Repo.URL},
    {Name: "TASK_DESCRIPTION", Value: task.Spec.Task.Description},
    {Name: "GITHUB_APP_ID", Value: strconv.FormatInt(cfg.GithubAppID, 10)},
    {Name: "GITHUB_INSTALLATION_ID", Value: strconv.FormatInt(cfg.GithubInstallationID, 10)},
    {Name: "GITHUB_API_URL", Value: cfg.GithubAPIURL},
}
```

#### 5. Update Tests

**File**: `internal/controller/job_builder_test.go`

- Verify init container env includes `GITHUB_APP_ID`, `GITHUB_INSTALLATION_ID`, and `GITHUB_API_URL`
- Verify values match the jobConfig

**File**: `internal/controller/agenttask_controller_test.go`

- Update test reconciler setup to include new fields

### Success Criteria

#### Automated Verification:
- [x] `make test` passes all tests
- [x] `make build` compiles
- [x] `go vet ./...` clean
- [x] Job builder tests verify GITHUB_APP_ID, GITHUB_INSTALLATION_ID, and GITHUB_API_URL env vars
- [x] Existing envtest integration tests still pass

#### Manual Verification:
- [ ] `./bin/shepherd operator --help` shows new flags
- [ ] Flags are marked as required

**Pause for manual review before Phase 4.**

---

## Phase 4: Ko Build + Makefile Integration

### Overview

Add Makefile targets to build the init container image with ko. Update `build-smoke` to include both images.

### Changes Required

#### 1. Makefile Targets

**File**: `Makefile`

Add targets:

```makefile
.PHONY: ko-build-init
ko-build-init: ko ## Build init container image locally with ko.
	cd cmd/shepherd-init && KO_DOCKER_REPO=$(KO_DOCKER_REPO) "$(KO)" build --sbom=none --bare .

.PHONY: test-init
test-init: ## Run init container tests.
	cd cmd/shepherd-init && go test ./... -coverprofile cover-init.out

.PHONY: lint-init
lint-init: golangci-lint ## Lint init container code.
	cd cmd/shepherd-init && "$(GOLANGCI_LINT)" run

.PHONY: vet-init
vet-init: ## Vet init container code.
	cd cmd/shepherd-init && go vet ./...
```

Update existing targets:

```makefile
# Update build-smoke to include init container
.PHONY: build-smoke
build-smoke: ko-build-local ko-build-init manifests kustomize ## Verify ko builds + kustomize render.
	"$(KUSTOMIZE)" build config/default > /dev/null
	@echo "Build smoke test passed: ko images built, kustomize renders cleanly"

# Update test to also run init container tests
.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run all tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out
	cd cmd/shepherd-init && go test ./... -coverprofile cover-init.out
```

#### 2. Makefile run target

**File**: `Makefile`

Update the `run` target to include the new required flags:

```makefile
.PHONY: run
run: manifests generate fmt vet ## Run the operator from your host.
	SHEPHERD_RUNNER_IMAGE=shepherd-runner:latest \
	SHEPHERD_GITHUB_APP_ID=12345 \
	SHEPHERD_GITHUB_INSTALLATION_ID=67890 \
	SHEPHERD_GITHUB_API_URL=https://api.github.com \
	go run ./cmd/shepherd/ operator --leader-election=false
```

### Success Criteria

#### Automated Verification:
- [x] `make test-init` passes
- [x] `make ko-build-init` builds successfully
- [x] `make build-smoke` includes init container build
- [x] `make lint-init` passes
- [x] `make vet-init` passes

#### Manual Verification:
- [ ] `make build-smoke` exits 0

---

## Testing Strategy

### Unit Tests (cmd/shepherd-init/)

**Task files (`taskfiles_test.go`):**
- Description writing (happy path + missing env var)
- Context: plaintext passthrough, gzip decompression, empty context
- Error cases: invalid base64, invalid gzip

**GitHub token (`github_test.go`):**
- URL parsing (various formats, edge cases)
- Private key reading (PKCS1, PKCS8, error cases)
- JWT creation (correct claims, signing algorithm)
- Token exchange (mock HTTP server: success, scoped repos, error responses)
- Config from env (all vars, defaults, missing required vars)

### Operator Tests (internal/controller/)

**Job builder (`job_builder_test.go`):**

- Init container has GITHUB_APP_ID, GITHUB_INSTALLATION_ID, and GITHUB_API_URL env vars
- Values correctly propagated from jobConfig

### Test Patterns

- Use `t.TempDir()` for file operations
- Use `t.Setenv()` for environment variable tests
- Use `httptest.NewServer` for GitHub API mock
- Use `crypto/rsa.GenerateKey` for test keys
- Table-driven tests with testify assertions

## Dependency Summary

### Init container module (`cmd/shepherd-init/go.mod`)

```
github.com/golang-jwt/jwt/v5
```

That's it. Everything else is stdlib:
- `compress/gzip` — decompression
- `encoding/base64` — base64 decoding
- `encoding/json` — API request/response
- `encoding/pem` — key parsing
- `crypto/rsa`, `crypto/x509` — key parsing
- `net/http` — one API call
- `log/slog` — structured logging (Go 1.21+)
- `os`, `io`, `fmt`, `path/filepath`, `strings`, `strconv`, `time`, `bytes` — basics

### Operator module (unchanged `go.mod`)

No new dependencies — just `strconv` import in job_builder.go.

## Environment Variable Contract

### Init Container Env Vars (set by operator in job_builder.go)

| Variable | Source | Required | Description |
|----------|--------|----------|-------------|
| `REPO_URL` | `spec.repo.url` | Yes | Repository URL |
| `REPO_REF` | `spec.repo.ref` | No | Branch/tag/SHA |
| `TASK_DESCRIPTION` | `spec.task.description` | Yes | Task description text |
| `TASK_CONTEXT` | `spec.task.context` | No | Context (possibly gzip+base64) |
| `CONTEXT_ENCODING` | `spec.task.contextEncoding` | No | `"gzip"` or empty |
| `GITHUB_APP_ID` | Operator config | Yes | Runner App ID |
| `GITHUB_INSTALLATION_ID` | Operator config | Yes | Runner App installation ID |
| `GITHUB_API_URL` | Operator config | No | GitHub API base URL (default: `https://api.github.com`) |

### Output Files

| Path | Description | Read by |
|------|-------------|---------|
| `/task/description.txt` | Task description (plaintext) | Runner |
| `/task/context.txt` | Context (decompressed plaintext, may be empty) | Runner |
| `/creds/token` | GitHub installation access token (valid 1 hour) | Runner |

### Secret Convention

The Kubernetes Secret (name from `SHEPHERD_RUNNER_SECRET`, default `shepherd-runner-app-key`) must contain:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: shepherd-runner-app-key
type: Opaque
data:
  private-key.pem: <base64-encoded PEM private key>
```

## Security Considerations

- **Private key isolation**: The private key is only accessible to the init container, never the runner. The runner only receives the short-lived token.
- **Token scoping**: Installation token is restricted to the specific target repository via the `repositories` parameter.
- **Token lifetime**: 1 hour (GitHub hardcoded). The Job's `activeDeadlineSeconds` (default 30m) ensures the Job completes before the token expires.
- **File permissions**: Token file written with 0600 (owner read-write only) for security. Task files written with 0644 (readable by all pod containers).

## References

- Design doc: `docs/research/2026-01-27-shepherd-design.md`
- Operator plan: `thoughts/plans/2026-01-27-operator-implementation.md`
- Job builder: `internal/controller/job_builder.go`
- GitHub App token docs: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app
- JWT generation docs: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
- `golang-jwt/jwt`: https://github.com/golang-jwt/jwt
