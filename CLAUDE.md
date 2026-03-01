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

## Key patterns (Backend)

- Kong CLI commands delegate to `Run(Options)` in their respective packages
- chi router with middleware stack, signal handling, graceful shutdown
- Two GitHub Apps: Trigger App (adapter, webhooks/comments) and Runner App (API, token generation) — separate packages, separate credentials
- CRD types live in `api/v1alpha1/`, API request/response types in `pkg/api/types.go`
- HMAC-SHA256 signature verification on webhook and callback endpoints
- Interfaces for testability (e.g., `TokenProvider` in `pkg/api/github_token.go`)

## Key patterns (Frontend)

- Svelte 5 runes only — `$state`, `$derived`, `$effect`, `$props()`. No Svelte 4 patterns (no `export let`, no `$:`, no stores via `writable`)
- API types auto-generated from OpenAPI spec: `make web-gen-types` → `src/lib/api.d.ts`. Never hand-edit this file
- Type-safe API client via `openapi-fetch` in `src/lib/client.ts` — all REST calls go through `api.GET()`/`api.POST()`
- Reactive stores are classes with `$state` fields (`tasks.svelte.ts`, `task-detail.svelte.ts`, `task-stream.svelte.ts`)
- Pure logic extracted into plain `.ts` files (`filters.ts`, `format.ts`, `stream-logic.ts`) for easy unit testing
- WebSocket client (`ws.ts`) handles reconnection with exponential backoff + jitter; `task-stream.svelte.ts` adds sequence gap detection on top
- Dark-only theme using CSS custom properties in `app.css` (GitHub-inspired palette)
- SPA mode: `ssr: false`, `prerender: false`, `adapter-static` with `fallback: 'index.html'`
- Dev server proxies `/api` to `localhost:8080` (configured in `vite.config.ts`)

## Testing conventions (Backend)

- httptest servers for mocking HTTP dependencies
- `fake.NewClientBuilder()` for K8s client mocks
- Table-driven tests for validation and regex
- Test helpers use `t.Helper()` and live in `_test.go` files alongside production code

## Testing conventions (Frontend)

- Unit tests (Vitest): test files live alongside source as `*.test.ts`
- Unit tests use `@testing-library/svelte` for component rendering
- Pure logic functions are tested directly without component rendering
- Prefer `getByText`, `getByRole` over CSS class selectors in tests — Tailwind classes are implementation details
- E2E tests (Playwright) live in `web/e2e/` — two strategies:
  - Integration tests (`task-list.spec.ts`, `task-detail.spec.ts`): run against real API + Kind cluster
  - Behavior tests (`websocket-behavior.spec.ts`): mock API via `page.route()` and WebSocket via `page.routeWebSocket()`
- E2E helpers (`e2e/helpers.ts`): `createTask()` seeds data through the real API, `waitForTaskPhase()` polls until K8s reconciles

## Things that will bite you (Backend)

- golangci-lint is strict about unused code — don't scaffold functions before they're wired to routes
- `go mod tidy` removes packages not yet imported — add dependencies in the phase they're used
- ghinstallation v2: no `TokenWithOptions` method; use `Transport.InstallationTokenOptions` field + `Token(ctx)`
- The adapter and API share `SHEPHERD_GITHUB_APP_ID` env var name but use different GitHub Apps — check which component you're configuring

## Things that will bite you (Frontend)

- `src/lib/api.d.ts` is auto-generated — run `make web-gen-types` after changing `api/openapi.yaml`, never edit by hand
- Biome (not ESLint/Prettier) handles linting and formatting — run `make web-lint-fix` not `npx eslint`
- Svelte 5 `$effect` cleanup: always return a cleanup function when setting up intervals/subscriptions, or they leak
- The `void now` trick forces `$derived.by` to depend on a reactive timestamp for live duration updates — don't remove the seemingly no-op `void` statement
- E2E integration tests require a running Kind cluster with the full stack — they are NOT standalone; see `make test-e2e-all`
- E2E WebSocket behavior tests (`websocket-behavior.spec.ts`) mock everything and can run against just the frontend dev server
- `openapi-fetch` omits `undefined` query params automatically — setting a filter to `undefined` means "don't send it", not "send empty"
