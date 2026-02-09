---
date: 2026-02-08T20:32:10+01:00
researcher: Claude
git_commit: e01b65ff003c57d72856f7092deed7d5c788f437
branch: e2e
repository: nissessenap/shepherd
topic: "E2E testing approaches without mocking GitHub"
tags: [research, e2e, testing, github, kind, chainsaw, go-vcr]
status: complete
last_updated: 2026-02-08
last_updated_by: Claude
last_updated_note: "Added follow-up research: resolved open questions, framework choice, integration test status"
---

# Research: E2E Testing Approaches Without Mocking GitHub

**Date**: 2026-02-08T20:32:10+01:00
**Researcher**: Claude
**Git Commit**: e01b65ff003c57d72856f7092deed7d5c788f437
**Branch**: e2e
**Repository**: nissessenap/shepherd

## Research Question
How can we set up e2e tests, preferably without having to create a mock implementation of GitHub?

## Summary

Shepherd currently has a basic e2e test suite (`test/e2e/`) that validates controller startup and metrics endpoint availability in a Kind cluster. The `config/test` kustomize overlay already strips GitHub App credentials, allowing the API server to start without GitHub configuration (returning 503 on token requests). This means e2e tests can validate the full Kubernetes-side lifecycle (AgentTask creation, SandboxClaim provisioning, runner assignment, status updates, callbacks) without GitHub at all.

For testing the GitHub token flow specifically, three practical approaches exist without building a custom mock: **go-vcr record/replay** (captures real GitHub API responses to YAML cassettes), **httptest.Server** (already used in unit tests - can be deployed as a sidecar or in-cluster service), and **Chainsaw/KUTTL** declarative frameworks (for testing K8s resource lifecycle with YAML assertions).

The key insight is that Shepherd's architecture cleanly separates GitHub from core functionality: the controller never touches GitHub, and the API server's `githubAPIURL` is injectable. This means a simple `httptest.Server` running as a K8s Service in the Kind cluster can replace `api.github.com` for e2e tests.

## Detailed Findings

### Current E2E Test Infrastructure

#### Test Suite Structure
- [test/e2e/e2e_suite_test.go](test/e2e/e2e_suite_test.go) - Suite setup with cert-manager management
- [test/e2e/e2e_test.go](test/e2e/e2e_test.go) - Test specs using Ginkgo/Gomega
- [test/utils/utils.go](test/utils/utils.go) - Shared utilities (Run, LoadImageToKindCluster, etc.)

#### What the E2E Tests Currently Validate
1. **Controller startup**: Exactly 1 pod with label `control-plane=controller-manager` runs in `shepherd-system` namespace
2. **Metrics endpoint**: Authenticates with ServiceAccount token, curls `https://<metrics-service>:8443/metrics`, expects HTTP 200

#### What Is NOT Tested Yet
- AgentTask CR creation and reconciliation lifecycle
- SandboxClaim creation and readiness
- Runner task assignment flow
- Status update and callback delivery
- GitHub token generation
- Context compression/decompression round-trip
- Error handling (sandbox timeout, runner failure)

#### Makefile Targets
- `make test-e2e`: Full cycle - `kind-create` -> `ko-build-kind` -> `install` -> `deploy-test` -> run tests -> `kind-delete`
- `make test-e2e-existing`: Run tests against already-running cluster (line 92)

#### Config/Test Overlay (`config/test/kustomization.yaml`)
The test overlay modifies the default deployment:
1. Sets `imagePullPolicy: IfNotPresent` on all Deployments (Kind loads images locally)
2. **Strips all GitHub App configuration** from the API deployment:
   - Removes `SHEPHERD_GITHUB_APP_ID`, `SHEPHERD_GITHUB_INSTALLATION_ID`, `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` env vars
   - Removes private key volume and volume mount
   - Keeps only `SHEPHERD_NAMESPACE`
3. Uses `shepherd:latest` image tag (matches `ko-build-kind` output)

The API server starts fine without GitHub config due to all-or-nothing flag group validation. Token requests return 503.

### GitHub Integration Architecture

GitHub integration is **isolated to the API server** (`pkg/api/handler_token.go`). The controller has zero GitHub code.

**Token request flow**: Runner calls `GET /api/v1/tasks/{taskID}/token` on internal port 8081 -> API creates JWT (RS256, 10min) -> API exchanges JWT at `{githubAPIURL}/app/installations/{id}/access_tokens` -> Returns `ghs_*` token to runner.

**Key injection point**: `githubAPIURL` in `taskHandler` struct (`pkg/api/server.go:115`), configurable via `--github-api-url` / `SHEPHERD_GITHUB_API_URL` flag (`cmd/shepherd/api.go:32`). Defaults to `https://api.github.com`.

This means pointing `SHEPHERD_GITHUB_API_URL` to a test server is the natural way to test token generation without real GitHub.

### Existing Test Patterns for GitHub

Unit tests already mock GitHub using `httptest.Server`:

**`pkg/api/handler_token_test.go:51-77`**: `mockGitHubTokenServer()` creates a test server that:
- Validates the request path matches `/app/installations/*/access_tokens`
- Validates `Authorization: Bearer` header is present
- Returns `{"token":"ghs_test_token_123","expires_at":"2026-02-02T12:00:00Z"}`

**`pkg/api/handler_token_test.go:79-102`**: `newTokenTestHandler()` injects the mock server URL:
```go
return &taskHandler{
    githubAPIURL:    ghServer.URL,    // Test server URL instead of api.github.com
    githubKey:       testKey(t),       // In-memory RSA key
    httpClient:      ghServer.Client(),
}
```

This same pattern can be used in e2e tests by deploying a similar HTTP server as a K8s Service in the Kind cluster.

### Approaches for E2E Testing Without Custom GitHub Mocks

#### Approach 1: Test the K8s Lifecycle Without GitHub (Lowest Effort)

Since `config/test` already strips GitHub config, the entire operator lifecycle can be tested end-to-end without GitHub:

1. Create AgentTask CR with `callbackURL` pointing to a test receiver pod
2. Verify SandboxClaim is created
3. Simulate sandbox readiness (create SandboxClaim with Ready=True condition)
4. Verify runner receives task assignment
5. Runner calls `POST /tasks/{id}/status` with `completed` event
6. Verify callback is delivered to receiver pod
7. Verify AgentTask reaches terminal state

Token requests would return 503 (expected when GitHub is not configured). This tests ~90% of the system.

#### Approach 2: In-Cluster httptest Server as GitHub Stub (Medium Effort)

Deploy a minimal Go HTTP server in the Kind cluster that responds to the GitHub App token exchange endpoint:

```
POST /app/installations/{id}/access_tokens -> 201 {"token":"ghs_test","expires_at":"..."}
```

Configure the API deployment with `SHEPHERD_GITHUB_API_URL` pointing to this service. This reuses the exact same pattern from unit tests but at the cluster level.

**What's needed**:
- A small Go binary (~50 lines) that implements the GitHub token endpoint
- A Dockerfile/ko build for this binary
- K8s manifests: Deployment + Service
- Additional kustomize patch in `config/test` to set `SHEPHERD_GITHUB_API_URL`

**Advantage**: Tests the actual token flow (JWT creation, HTTP exchange, token scoping) without any external dependency.

#### Approach 3: go-vcr Record/Replay (Medium Effort, Higher Realism)

Use [dnaeon/go-vcr](https://github.com/dnaeon/go-vcr) to record real GitHub API responses and replay them in tests.

**How it works**:
1. Run tests once with real GitHub credentials -> interactions recorded to YAML cassettes
2. Subsequent runs replay from cassettes (no network needed)
3. Cassettes committed to repo

**What's needed**:
- `go get github.com/dnaeon/go-vcr`
- Modify API server to accept custom `http.Transport` for the GitHub HTTP client
- Record cassettes against real GitHub App
- Add hook to scrub sensitive data from cassettes

**Trade-offs**:
- Higher realism (real GitHub responses)
- Requires initial real GitHub access for recording
- Cassettes can go stale if GitHub API changes
- More complex setup than stub server

#### Approach 4: Chainsaw Declarative E2E Framework (Complementary)

Use [kyverno/chainsaw](https://github.com/kyverno/chainsaw) for testing the Kubernetes resource lifecycle declaratively:

```yaml
apiVersion: chainsaw.kyverno.io/v1alpha1
kind: Test
metadata:
  name: task-lifecycle
spec:
  steps:
  - name: Create AgentTask
    try:
    - apply:
        file: agenttask.yaml
    - assert:
        file: expected-sandboxclaim.yaml
```

**Trade-offs**:
- Declarative YAML tests (easy to write and maintain)
- No GitHub API mocking built-in (combine with Approach 1 or 2)
- Modern replacement for KUTTL
- Better error reporting

#### Approach 5: Gitea as GitHub-Compatible Backend (High Effort, Partial Compatibility)

Deploy Gitea in Kind cluster as a GitHub-like API.

**NOT recommended** because:
- Gitea's webhook payloads differ from GitHub's in at least a dozen ways
- GitHub Apps are not fully supported
- The `POST /app/installations/{id}/access_tokens` endpoint doesn't exist in Gitea
- Significant setup complexity (needs database, persistent storage)
- Testing against Gitea != testing against GitHub

### Existing Unit Test Patterns (Reference)

| Pattern | Use Case | Key Files |
|---------|----------|-----------|
| Envtest suite | Controller integration tests | `internal/controller/suite_test.go` |
| Fake client | API server unit tests | `pkg/api/handler_*_test.go` |
| httptest.Server | Mock GitHub API, callbacks | `pkg/api/handler_token_test.go`, `callback_test.go` |
| Transport rewriting | Controller HTTP mocks | `internal/controller/agenttask_controller_test.go` |
| Interceptor funcs | Simulate K8s API errors | `pkg/api/handler_tasks_test.go`, `handler_token_test.go` |

## Code References

- `test/e2e/e2e_test.go:48-281` - Current e2e test specs
- `test/e2e/e2e_suite_test.go:45-101` - Suite setup with cert-manager
- `test/utils/utils.go:43-174` - Test utilities
- `config/test/kustomization.yaml` - Test overlay stripping GitHub config
- `pkg/api/handler_token.go:33-123` - Token request handler (target for e2e testing)
- `pkg/api/github_token.go:67-157` - JWT creation and token exchange
- `pkg/api/handler_token_test.go:51-77` - Existing GitHub mock server pattern
- `pkg/api/server.go:109-118` - taskHandler struct with injectable githubAPIURL
- `cmd/shepherd/api.go:30-42` - GitHub CLI flags with all-or-nothing validation
- `internal/controller/agenttask_controller.go:77-214` - Reconciliation flow (no GitHub)
- `Makefile:83-92` - E2E testing targets

## Architecture Documentation

### Test Layers in Shepherd

```
Layer 1: Unit Tests (envtest + fake clients + httptest)
  Controller reconciliation: envtest with real etcd/apiserver
  API handlers: fake.NewClientBuilder() with interceptors
  GitHub token: httptest.Server as mock GitHub API
  Callbacks: httptest.Server as mock adapter
  Speed: seconds | Coverage: logic paths, error handling

Layer 2: E2E Tests (Kind cluster + deployed operator)
  Controller deployment and startup
  Metrics endpoint availability
  [Missing] AgentTask lifecycle
  [Missing] SandboxClaim provisioning
  [Missing] Runner assignment and status flow
  [Missing] Callback delivery
  Speed: minutes | Coverage: deployment, integration, networking

Layer 3: [Not implemented] CI Validation (Real GitHub)
  Real GitHub App token generation
  Real repository cloning
  Real PR creation
  Speed: minutes | Coverage: external service integration
```

### GitHub Integration Isolation

```
Controller (internal/controller/)
  Manages SandboxClaim lifecycle
  Assigns tasks to runners via HTTP
  ZERO GitHub code

API Server (pkg/api/)
  Public port 8080: Task CRUD (no GitHub)
  Internal port 8081:
    Status updates (no GitHub)
    Task data (no GitHub)
    Token generation (ONLY GitHub touchpoint)
      Injectable via githubAPIURL field
```

## Historical Context (from thoughts/)

- `thoughts/plans/2026-02-01-operator-api-sandbox-migration.md` - Explicitly defers e2e tests to a follow-up plan
- `thoughts/research/2026-01-31-poc-sandbox-learnings.md` - Documents Kind cluster testing with agent-sandbox, ko image loading, imagePullPolicy patterns
- `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md` - Confirms envtest for integration testing
- `thoughts/research/2026-01-27-shepherd-design.md` - Original design specifying testify for unit tests, gomega/envtest for integration

## Related Research

- `thoughts/research/2026-02-03-token-auth-alternatives.md` - Token authentication alternatives research
- `thoughts/research/2026-01-31-poc-sandbox-learnings.md` - Kind cluster + ko testing patterns

## External References

- [dnaeon/go-vcr](https://github.com/dnaeon/go-vcr) - Record/replay HTTP interactions
- [migueleliasweb/go-github-mock](https://github.com/migueleliasweb/go-github-mock) - GitHub API mocking for go-github
- [kyverno/chainsaw](https://github.com/kyverno/chainsaw) - Declarative K8s e2e testing framework
- [kudobuilder/kuttl](https://github.com/kudobuilder/kuttl) - K8s test harness (used by ArgoCD Operator)
- [WireMock Kubernetes](https://wiremock.org/docs/solutions/kubernetes/) - In-cluster HTTP mocking

## Open Questions

1. **Agent-sandbox in e2e tests**: Both options are viable - agent-sandbox can be deployed to Kind, and CRDs can also be created manually to simulate sandbox readiness. Decision depends on what the specific test needs to validate.
2. ~~**Runner binary**~~: Resolved - see follow-up below.
3. ~~**Callback receiver**~~: Resolved - see follow-up below.
4. ~~**CI integration**~~: Resolved - see follow-up below.

## Follow-up Research 2026-02-08T20:45+01:00

### Resolved: E2E Test Framework Choice

The existing e2e tests already use **Ginkgo** (`test/e2e/e2e_test.go` uses `Describe`/`It`/`BeforeAll`/`Eventually`). The controller integration tests also use Ginkgo. The API unit tests use plain `testing` + testify.

Sticking with **Ginkgo for e2e** is the path of least resistance:

- Already set up with suite infrastructure in `test/e2e/e2e_suite_test.go`
- `Eventually` is natural for waiting on K8s resources to reach desired states
- Consistent with controller integration tests
- No reason to switch or introduce a new framework

### Resolved: Runner Strategy for E2E

**Decision**: Use a lightweight e2e stub runner, not the real runner.

The real runner will invoke Claude (or another AI agent), which costs tokens and is slow/non-deterministic. For e2e, a stub runner that simulates the runner's API interactions is needed:

1. Receive task assignment on `:8888/task`
2. Fetch task data via `GET /api/v1/tasks/{id}/data`
3. Optionally fetch token via `GET /api/v1/tasks/{id}/token`
4. Post terminal status via `POST /api/v1/tasks/{id}/status`

This exercises the full API flow without AI costs. The current `cmd/shepherd-runner/` stub already accepts task assignments and exits - it needs extending to call the data/status endpoints.

When the GitHub App webhook adapter is built, it becomes another entry point for e2e tests (create task via webhook -> verify lifecycle), but the core e2e flow doesn't depend on it. The webhook adapter mostly receives events and responds with 2xx, so it's not a complex mock target.

### Resolved: Callback Verification Strategy

**Decision**: Check the CRD condition only. No echo-server needed.

The API sets `Notified=CallbackSent` or `Notified=CallbackFailed` on the AgentTask status after sending a callback. For e2e tests, asserting this condition on the AgentTask CR is sufficient.

Callback payload structure and HMAC signing are already well-covered in unit tests:

- `pkg/api/callback_test.go` - Tests HMAC signature generation, payload structure, error handling
- `pkg/api/handler_status_test.go` - Tests callback delivery on terminal events, deduplication

No need to duplicate that verification at the e2e layer.

For the `callbackURL` in e2e AgentTask specs, it can point to a non-existent URL or a simple in-cluster endpoint. The test asserts the CRD condition, not the actual HTTP delivery.

### Resolved: CI Integration

**Decision**: No real GitHub token testing in CI. E2e tests run without GitHub credentials.

### Current Integration Test Status

**Controller tests** (`internal/controller/`): Envtest-based with Ginkgo. Tests reconciliation through multiple phases:

- Creates AgentTask, verifies SandboxClaim creation
- Simulates sandbox readiness (manually setting conditions on SandboxClaim)
- Verifies task assignment via httptest mock runner (using `rewriteTransport`)
- Tests sandbox termination and grace period handling
- Tests failure classification (timeout vs failure reasons)
- Tests sandbox builder output (SandboxClaim spec correctness)
- Tests helper functions (IsTerminal, condition checking)

**API tests** (`pkg/api/`): Testify-based with fake K8s client. Cover all endpoints:

- Task CRUD: creation validation, listing with filters, retrieval
- Status updates: terminal/non-terminal events, callback delivery, deduplication
- Context: compression/decompression round-trip
- Token generation: GitHub mock server, replay protection, retry on conflict, repo scoping
- Watcher: terminal transition detection, callback claiming
- Callbacks: HMAC signing, error handling
- Server: content-type validation, health endpoints
- Error paths via client interceptors (conflicts, API server errors)

**Runner tests** (`cmd/shepherd-runner/`): Basic tests for the stub runner.

**Key gap**: No test exercises the controller and API server together. Controller tests mock the runner HTTP endpoint. API tests use a fake K8s client. Neither proves the full flow where the controller creates a real AgentTask that the API server can serve to a runner. This is exactly what the e2e tests should cover.
