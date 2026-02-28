# Shepherd

Background coding agent orchestrator on Kubernetes. Users trigger tasks via GitHub issue comments (`@shepherd`), which flow through: GitHub Adapter → API Server → Operator → Runner sandbox → PR back to GitHub.

## Project structure

```
cmd/shepherd/       CLI entry point (Kong). Subcommands: api, operator, github
pkg/api/            HTTP API server (chi router, CRD management, token generation)
pkg/adapters/github/ GitHub adapter (webhooks, comments, callbacks)
pkg/operator/       K8s controller (AgentTask reconciliation, sandbox lifecycle)
api/v1alpha1/       CRD types (AgentTask)
api/                OpenAPI 3.0 spec (source of truth for API types)
config/             Kustomize manifests, CRDs, RBAC
web/                Svelte 5 SPA (SvelteKit, Tailwind v4, openapi-fetch)
```

## Tech stack

- Go 1.25, controller-runtime, chi router, Kong CLI, ghinstallation/v2, go-github/v75
- Frontend: Svelte 5, SvelteKit (adapter-static), Tailwind v4, openapi-fetch
- Go tests: testify + httptest + envtest
- Frontend tests: Vitest (unit) + Playwright (E2E)
- Go linter: golangci-lint v2 via `make lint-fix`
- Frontend linter: Biome via `make web-lint-fix`

## Build and verify

```bash
make build       # bin/shepherd
make test        # unit + envtest (runs fmt/vet/generate first)
make lint-fix    # golangci-lint with auto-fix
go vet ./...     # quick check

# Frontend
make web-build     # production build
make web-test      # vitest unit tests
make web-check     # svelte-check type checking
make web-lint-fix  # biome auto-fix
make web-gen-types # generate TS types from OpenAPI spec
```

Always run `make lint-fix` and `make test` before considering Go work done.
Always run `make web-lint-fix`, `make web-test`, and `make web-check` before considering frontend work done.

## Key patterns

- Kong CLI commands delegate to `Run(Options)` in their respective packages
- chi router with middleware stack, signal handling, graceful shutdown
- Two GitHub Apps: Trigger App (adapter, webhooks/comments) and Runner App (API, token generation) — separate packages, separate credentials
- CRD types live in `api/v1alpha1/`, API request/response types in `pkg/api/types.go`
- HMAC-SHA256 signature verification on webhook and callback endpoints
- Interfaces for testability (e.g., `TokenProvider` in `pkg/api/github_token.go`)

## Testing conventions

- httptest servers for mocking HTTP dependencies
- `fake.NewClientBuilder()` for K8s client mocks
- Table-driven tests for validation and regex
- Test helpers use `t.Helper()` and live in `_test.go` files alongside production code

## Things that will bite you

- golangci-lint is strict about unused code — don't scaffold functions before they're wired to routes
- `go mod tidy` removes packages not yet imported — add dependencies in the phase they're used
- ghinstallation v2: no `TokenWithOptions` method; use `Transport.InstallationTokenOptions` field + `Token(ctx)`
- The adapter and API share `SHEPHERD_GITHUB_APP_ID` env var name but use different GitHub Apps — check which component you're configuring
