---
title: Contributing
weight: 5
---

This guide covers the development workflow for contributing to Shepherd.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| **Go** | 1.25+ | Backend development |
| **Node.js** | 22+ | Frontend development |
| **Docker** | — | Container builds |
| **Kind** | — | Local Kubernetes cluster |
| **kubectl** | — | Kubernetes CLI |
| **ko** | — | Go container image builder |

## Backend Development

### Build and Test

```bash
make build         # Build bin/shepherd binary
make test          # Run all Go tests (unit + envtest)
make lint-fix      # Run golangci-lint with auto-fix
go vet ./...       # Quick static analysis check
```

`make test` automatically runs `fmt`, `vet`, `generate`, and `sync-external-crds` before testing.

### Code Conventions

- **Test framework**: [testify](https://github.com/stretchr/testify) for assertions, `httptest` for HTTP mocking
- **K8s testing**: `fake.NewClientBuilder()` for client mocks, envtest for integration tests
- **Test style**: table-driven tests for validation and regex patterns
- **Helpers**: use `t.Helper()` in test helper functions, keep them in `_test.go` files

### CRD Changes

When modifying the `AgentTask` CRD types in `api/v1alpha1/`:

```bash
# 1. Edit the types
vim api/v1alpha1/agenttask_types.go

# 2. Regenerate CRD manifests and DeepCopy methods
make manifests generate

# 3. Run tests to verify
make test
```

## Frontend Development

### Build and Test

```bash
make web-dev        # Start dev server with API proxy (localhost:5173)
make web-build      # Production build
make web-test       # Run Vitest unit tests
make web-check      # Run svelte-check type checking
make web-lint-fix   # Run Biome linter with auto-fix
```

### Code Conventions

- **Svelte 5 runes only** — use `$state`, `$derived`, `$effect`, `$props()`. No Svelte 4 patterns (`export let`, `$:`, `writable` stores)
- **Linter**: Biome (not ESLint/Prettier) — always use `make web-lint-fix`
- **API types**: auto-generated from OpenAPI spec — never hand-edit `src/lib/api.d.ts`
- **Test selectors**: prefer `getByText`, `getByRole` over CSS class selectors (Tailwind classes are implementation details)

### API Type Generation

When changing `api/openapi.yaml`:

```bash
# Regenerate TypeScript types
make web-gen-types

# Verify the types compile
make web-check
```

This generates `web/src/lib/api.d.ts` from the OpenAPI spec using `openapi-typescript`.

## E2E Testing

### Full Stack (Go + Playwright)

```bash
make test-e2e-all              # Create cluster, run all tests, tear down
make test-e2e-all-interactive  # Same but keep cluster alive for debugging
```

### Go E2E Only

```bash
make test-e2e-interactive  # Create/reuse cluster, run Go e2e tests
make test-e2e-existing     # Run against an already-running cluster
```

### Playwright Only

Playwright tests live in `web/e2e/` and use two strategies:

- **Integration tests** (`task-list.spec.ts`, `task-detail.spec.ts`): require a running Kind cluster with the full stack deployed
- **Behavior tests** (`websocket-behavior.spec.ts`): mock the API via `page.route()` and WebSocket via `page.routeWebSocket()` — these run against just the frontend dev server

```bash
# Install browsers (first time)
make web-e2e-install

# Run all Playwright tests
make web-e2e
```

## Project Structure

```
cmd/shepherd/         CLI entry point (Kong subcommands: api, operator, github)
pkg/api/              HTTP API server (chi router, CRD management, token generation)
pkg/adapters/github/  GitHub adapter (webhooks, comments, callbacks)
pkg/operator/         K8s controller (AgentTask reconciliation, sandbox lifecycle)
api/v1alpha1/         CRD types (AgentTask)
api/                  OpenAPI 3.0 spec (source of truth for API types)
config/               Kustomize manifests, CRDs, RBAC
web/                  Svelte 5 SPA (SvelteKit, Tailwind v4, openapi-fetch)
docs/                 Hugo documentation site (this site)
```

## Common Pitfalls

- **golangci-lint is strict** — don't scaffold functions before they're wired to routes. The linter rejects unused code.
- **`go mod tidy` removes packages** not yet imported in source files. Add dependencies in the phase they're used.
- **`src/lib/api.d.ts` is generated** — run `make web-gen-types` after changing `api/openapi.yaml`, never edit by hand.
- **Biome, not ESLint** — use `make web-lint-fix`, not `npx eslint`.
- **Svelte 5 `$effect` cleanup** — always return a cleanup function when setting up intervals/subscriptions to prevent leaks.

## Next Steps

- [Architecture Overview](../architecture/overview/) — understand how the components fit together
- [Troubleshooting](../troubleshooting/) — common development issues and solutions
