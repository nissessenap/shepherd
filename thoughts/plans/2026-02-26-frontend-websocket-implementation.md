# Shepherd Frontend + WebSocket Streaming Implementation Plan

## Overview

Implement the Shepherd web UI and real-time WebSocket streaming infrastructure. This spans three domains: a Svelte 5 frontend for task monitoring, Go backend additions for event streaming, and runner modifications for real-time event extraction from Claude Code.

The frontend is a 2-page SvelteKit SPA (task list + task detail) with dark-mode developer tool aesthetic, WebSocket-powered live streaming, and URL-driven filtering. The Go backend adds an in-memory EventHub, a WebSocket endpoint on port 8080, and an event ingestion endpoint on port 8081. The runner switches from `--output-format json` to `--output-format stream-json` and POSTs agent-agnostic events to the API.

## Current State Analysis

**Frontend**: Zero implementation code exists. `.claude/rules/frontend/` contains Svelte 5 coding rules. Three research documents define the design.

**Go API**: 5 public routes (8080) and 3 internal routes (8081). Dual-port chi router. No WebSocket, no event streaming, no event buffering. Types in `pkg/api/types.go`, handlers in `pkg/api/handler_*.go`.

**Runner**: Go binary in `cmd/shepherd-runner/`. Uses `--output-format json` (single JSON blob after completion). No stream-json parsing. Stop hook (`shepherd-runner hook`) handles terminal status via `gh pr list` artifact verification. All stdout buffered in `strings.Builder` — no line-by-line reading.

**E2E Infrastructure**: kind cluster with NodePort 30080, ko for image builds, kustomize test overlay, Ginkgo test suite. Stub test runner in `test/e2e/testrunner/`.

### Key Discoveries:
- Runner currently buffers ALL CC stdout in memory (`gorunner.go:56-59`) — needs refactoring to line-by-line streaming
- `taskHandler` struct (`handler_tasks.go:57-62`) will need an `eventHub` field for WebSocket fan-out
- Test patterns use `newTestHandler()` with `fake.NewClientBuilder()` and `testRouter()` helper — new EventHub tests follow the same pattern
- E2E uses `ko-build-kind` for image loading — frontend image follows identical pattern but uses `docker build` instead of `ko`
- The `config/test/kustomization.yaml` `imagePullPolicy` patch targets all Deployments generically — a frontend Deployment gets patched automatically

## Desired End State

After this plan is complete:

1. A Svelte 5 SPA at `web/` serves a task list page and task detail page with dark-mode UI
2. Running tasks show real-time events streaming via WebSocket
3. Completed tasks show event history, PR link (if succeeded), or error details (if failed)
4. The runner extracts turn-level events from Claude Code and POSTs them to the API
5. An OpenAPI spec at `api/openapi.yaml` is validated against both Go handlers and TypeScript types
6. Frontend is containerized (nginx) and deployable to kind for E2E testing
7. Vitest unit/component tests cover WebSocket reconnection, event parsing, and component rendering
8. Playwright E2E tests run against the full stack in kind

### Verification:
- `make test` passes (Go unit + envtest, includes kin-openapi contract validation)
- `make lint-fix` passes
- `make web-test` passes (Vitest unit + component tests)
- `make web-build` produces a production build
- `make test-e2e` deploys everything to kind and Playwright tests pass

## What We're NOT Doing

- **Authentication/authorization** — No auth for MVP. Port-forward access only.
- **Task creation from UI** — Read-only monitoring. Task creation stays API/GitHub only.
- **Multi-replica EventHub** — Single API replica for MVP. No Redis/NATS pub/sub.
- **Event persistence** — Events are in-memory only, lost on API restart.
- **Cost/token display** — `total_cost_usd` from CC is logged but not shown in UI.
- **Interactive sessions** — WebSocket is write-only (server→client). No bidirectional messages.
- **Light mode** — Dark mode only for MVP. CSS custom properties make light mode easy to add later.
- **oapi-codegen server stubs** — Using kin-openapi for test-time validation only. No generated Go server interface.
- **SSR/SSG** — Pure SPA with `adapter-static`. No server-side rendering.

## Implementation Approach

**OpenAPI-first contract**: Write `api/openapi.yaml` manually, generate TypeScript types with `openapi-typescript`, validate Go handler responses with `kin-openapi` in tests. Single source of truth, compile-time safety on frontend, test-time safety on backend.

**Additive backend changes**: The EventHub and WebSocket endpoint are purely additive. Existing status/callback system is unchanged. The runner adds event POSTing alongside its existing status reporting.

**Frontend architecture**: SvelteKit with `adapter-static`, `$state` classes for data (no TanStack Query), Tailwind v4 for styling, `openapi-fetch` for typed API calls.

---

## Phase 1: OpenAPI Spec & Contract Validation

### Overview
Write the OpenAPI 3.0 spec for existing REST endpoints, add kin-openapi to Go dependencies, and create a contract validation test helper that proves the spec matches the running handlers. This establishes the contract foundation before any frontend code exists.

### Changes Required:

#### 1. OpenAPI Specification
**File**: `api/openapi.yaml` (new)
**Changes**: Complete OpenAPI 3.0.3 spec covering all existing public and internal endpoints.

Endpoints to document:
- `GET /healthz` — 200 with `"ok"` body
- `GET /readyz` — 200 with `"ok"` body
- `POST /api/v1/tasks` — CreateTaskRequest → 201 TaskResponse | 400/413/415 ErrorResponse
- `GET /api/v1/tasks` — query params (repo, issue, fleet, active) → 200 TaskResponse[]
- `GET /api/v1/tasks/{taskID}` — 200 TaskResponse | 404 ErrorResponse
- `POST /api/v1/tasks/{taskID}/status` — StatusUpdateRequest → 200 | 400/404/409/410 ErrorResponse
- `GET /api/v1/tasks/{taskID}/data` — 200 TaskDataResponse | 404/410 ErrorResponse
- `GET /api/v1/tasks/{taskID}/token` — 200 TokenResponse | 404/409/410 ErrorResponse

Schema types to define (mirroring `pkg/api/types.go`):
- `CreateTaskRequest`, `RepoRequest`, `TaskRequest`, `RunnerConfig`
- `TaskResponse`, `TaskStatusSummary`
- `StatusUpdateRequest`
- `TaskDataResponse`, `TokenResponse`
- `ErrorResponse`

#### 2. kin-openapi Dependency
**File**: `go.mod`
**Changes**: `go get github.com/getkin/kin-openapi`

#### 3. Contract Validation Test Helper
**File**: `pkg/api/contract_test.go` (new)
**Changes**: Test helper that loads `api/openapi.yaml` and validates handler responses against it.

```go
// loadSpec loads and validates the OpenAPI spec once per test binary.
func loadSpec(t *testing.T) *openapi3.T { ... }

// validateResponse checks that an httptest.ResponseRecorder's output
// matches the OpenAPI spec for the given request.
func validateResponse(t *testing.T, doc *openapi3.T, req *http.Request, rec *httptest.ResponseRecorder) { ... }
```

#### 4. Add Contract Validation to Existing Handler Tests
**Files**: `pkg/api/handler_tasks_test.go`, `pkg/api/handler_status_test.go`, `pkg/api/handler_data_test.go`, `pkg/api/handler_token_test.go`
**Changes**: Call `validateResponse()` in key test functions (happy path tests) to verify responses match the spec. This is additive — existing assertions remain.

Example integration point in `TestCreateTask_Valid` (`handler_tasks_test.go:111`):
```go
func TestCreateTask_Valid(t *testing.T) {
    h := newTestHandler()
    router := testRouter(h)
    w := postCreateTask(t, router, validCreateRequest())
    assert.Equal(t, http.StatusCreated, w.Code)

    // NEW: validate response matches OpenAPI spec
    doc := loadSpec(t)
    req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", /* ... */)
    validateResponse(t, doc, req, w)

    // existing assertions continue...
}
```

#### 5. Makefile Target
**File**: `Makefile`
**Changes**: No separate target needed — contract validation runs as part of `make test`.

### Success Criteria:

#### Automated Verification:
- [x] `api/openapi.yaml` is valid OpenAPI 3.0.3: `go test ./pkg/api/ -run TestOpenAPISpecIsValid`
- [x] `make test` passes — contract validation confirms all happy-path responses match the spec
- [x] `make lint-fix` passes

#### Manual Verification:
- [ ] Review `api/openapi.yaml` for completeness against the actual API routes in `pkg/api/server.go:175-200`

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding.

---

## Phase 2: Frontend Scaffolding

### Overview
Scaffold the SvelteKit project in `web/`, configure Tailwind v4, generate TypeScript types from the OpenAPI spec, and add Makefile targets. No UI components yet — just the build pipeline.

### Changes Required:

#### 1. SvelteKit Project Scaffolding
**Directory**: `web/` (new)

Initialize with:
```bash
cd web && npx sv create . --template minimal --types ts
npx sv add tailwindcss
```

Key files created:
- `web/package.json` — dependencies: svelte, sveltekit, tailwindcss, @tailwindcss/vite
- `web/svelte.config.js` — adapter-static, SPA mode
- `web/vite.config.ts` — tailwindcss plugin + sveltekit plugin + API proxy for dev
- `web/tsconfig.json` — strict TypeScript
- `web/src/app.html` — shell HTML
- `web/src/app.css` — Tailwind imports + theme tokens

#### 2. SvelteKit Configuration
**File**: `web/svelte.config.js`
```javascript
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({ fallback: 'index.html' }),  // SPA mode
  },
};
```

**File**: `web/vite.config.ts`
```typescript
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',  // proxy to Go API during dev
    },
  },
});
```

#### 3. Tailwind v4 Theme
**File**: `web/src/app.css`

```css
@import "tailwindcss";

@custom-variant dark (&:where(.dark, .dark *));

@theme {
  --font-mono: ui-monospace, 'JetBrains Mono', 'Fira Code', monospace;
  --font-sans: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;

  --color-canvas-default:  #0d1117;
  --color-canvas-subtle:   #161b22;
  --color-canvas-inset:    #010409;
  --color-border-default:  #30363d;
  --color-border-muted:    #21262d;
  --color-fg-default:      #e6edf3;
  --color-fg-muted:        #8b949e;
  --color-fg-dim:          #6e7681;
  --color-accent-fg:       #58a6ff;
  --color-success-fg:      #3fb950;
  --color-danger-fg:       #f85149;
  --color-attention-fg:    #d29922;
  --color-info-fg:         #58a6ff;
}
```

#### 4. TypeScript Type Generation
**File**: `web/package.json` — add dev dependencies:
- `openapi-typescript` — generates `.d.ts` from OpenAPI spec
- `openapi-fetch` — typed fetch wrapper (production dependency)

**Script**: `web/package.json` scripts:
```json
{
  "scripts": {
    "dev": "vite dev",
    "build": "vite build",
    "preview": "vite preview",
    "check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json",
    "gen:api": "openapi-typescript ../api/openapi.yaml -o src/lib/api.d.ts"
  }
}
```

**File**: `web/src/lib/api.d.ts` (generated, committed)
**File**: `web/src/lib/client.ts` — typed API client:
```typescript
import createClient from 'openapi-fetch';
import type { paths } from './api.d.ts';

export const api = createClient<paths>({
  baseUrl: import.meta.env.VITE_API_URL ?? '',
});
```

#### 5. SvelteKit Route Structure
```
web/src/routes/
├── +layout.svelte       ← App shell (header, dark mode class on <html>)
├── +page.ts             ← Redirect / → /tasks
├── tasks/
│   ├── +page.svelte     ← Task list (placeholder)
│   └── [taskID]/
│       └── +page.svelte ← Task detail (placeholder)
```

#### 6. Biome Configuration
**File**: `web/biome.json` (new)

```json
{
  "$schema": "https://biomejs.dev/schemas/2.0/schema.json",
  "organizeImports": { "enabled": true },
  "linter": { "enabled": true },
  "formatter": { "enabled": true, "indentStyle": "tab" }
}
```

#### 7. Makefile Targets
**File**: `Makefile`
**Changes**: Add frontend section:

```makefile
##@ Frontend

.PHONY: web-install
web-install: ## Install frontend dependencies.
	npm ci --prefix web

.PHONY: web-dev
web-dev: ## Start frontend dev server with API proxy.
	npm run dev --prefix web

.PHONY: web-build
web-build: web-install ## Build frontend for production.
	npm run build --prefix web

.PHONY: web-check
web-check: ## Run svelte-check type checking.
	npm run check --prefix web

.PHONY: web-gen-types
web-gen-types: ## Generate TypeScript types from OpenAPI spec.
	npm run gen:api --prefix web

.PHONY: web-lint
web-lint: ## Run Biome linter on frontend code.
	npx --prefix web biome check web/src/

.PHONY: web-lint-fix
web-lint-fix: ## Run Biome linter with auto-fix.
	npx --prefix web biome check --write web/src/
```

#### 8. .gitignore Updates
**File**: `web/.gitignore` (new)
```
node_modules/
.svelte-kit/
build/
```

### Success Criteria:

#### Automated Verification:
- [x] `make web-install` completes without errors
- [x] `make web-build` produces files in `web/build/`
- [x] `make web-check` passes TypeScript checking
- [x] `make web-gen-types` generates `web/src/lib/api.d.ts` from the OpenAPI spec
- [x] `make web-lint` passes
- [ ] `make web-dev` starts dev server and proxies `/api` to `localhost:8080`

#### Manual Verification:
- [ ] Dev server shows a placeholder page at `http://localhost:5173/tasks`
- [ ] Tailwind dark theme renders correctly (dark background, light text)

**Implementation Note**: After completing this phase, pause for manual verification.

---

## Phase 3: Go Backend — EventHub & WebSocket

### Overview
Add the in-memory EventHub for per-task event fan-out, the internal event ingestion endpoint (`POST /api/v1/tasks/{taskID}/events` on port 8081), and the public WebSocket endpoint (`GET /api/v1/tasks/{taskID}/events` on port 8080). Update the OpenAPI spec.

### Changes Required:

#### 1. coder/websocket Dependency
**File**: `go.mod`
**Changes**: `go get github.com/coder/websocket`

#### 2. TaskEvent Types
**File**: `pkg/api/types.go`
**Changes**: Add the agent-agnostic event schema types as defined in the streaming architecture research:

```go
// TaskEvent types
type TaskEventType string

const (
    EventTypeThinking  TaskEventType = "thinking"
    EventTypeToolCall  TaskEventType = "tool_call"
    EventTypeToolResult TaskEventType = "tool_result"
    EventTypeError     TaskEventType = "error"
)

type TaskEvent struct {
    Sequence  int64            `json:"sequence"`
    Timestamp time.Time        `json:"timestamp"`
    Type      TaskEventType    `json:"type"`
    Summary   string           `json:"summary"`
    Tool      string           `json:"tool,omitempty"`
    Input     map[string]any   `json:"input,omitempty"`
    Output    *TaskEventOutput `json:"output,omitempty"`
    Metadata  map[string]any   `json:"metadata,omitempty"`
}

type TaskEventOutput struct {
    Success bool   `json:"success"`
    Summary string `json:"summary,omitempty"`
}

// WebSocket message types (server → client)
type WSMessage struct {
    Type string `json:"type"` // "task_event" or "task_complete"
    Data any    `json:"data"`
}

type TaskCompleteData struct {
    TaskID string `json:"taskID"`
    Status string `json:"status"`
    PRURL  string `json:"prURL,omitempty"`
    Error  string `json:"error,omitempty"`
}

// Event ingestion request (runner → API)
type PostEventRequest struct {
    Events []TaskEvent `json:"events"`
}
```

#### 3. EventHub Implementation
**File**: `pkg/api/eventhub.go` (new)

Core structure:
```go
type EventHub struct {
    mu    sync.RWMutex
    tasks map[string]*taskStream
}

type taskStream struct {
    mu          sync.RWMutex
    events      []TaskEvent        // ring buffer, max 1000 events
    subscribers map[string]chan TaskEvent
    done        bool
}
```

Key methods:
- `NewEventHub() *EventHub`
- `Publish(taskID string, events []TaskEvent)` — append to ring buffer, fan out to subscribers
- `Subscribe(taskID string, after int64) (events []TaskEvent, ch <-chan TaskEvent, unsubscribe func())`
  - Returns historical events with sequence > `after`, plus a channel for live events
- `Complete(taskID string)` — marks stream as done, closes subscriber channels
- `Cleanup(taskID string)` — removes task stream entirely (called after TTL)

Ring buffer: cap at 1000 events per task. When full, oldest events are dropped. The `after` parameter on Subscribe allows reconnection without missing events (as long as the buffer hasn't wrapped).

#### 4. Event Ingestion Handler (Internal Port 8081)
**File**: `pkg/api/handler_events.go` (new)

```go
// POST /api/v1/tasks/{taskID}/events
func (h *taskHandler) postEvents(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "taskID")
    // Validate task exists and is not terminal
    // Decode PostEventRequest body
    // Validate each event has required fields (sequence, type, summary)
    // Publish events to EventHub
    // Return 200 OK
}
```

#### 5. WebSocket Handler (Public Port 8080)
**File**: `pkg/api/handler_ws.go` (new)

```go
// GET /api/v1/tasks/{taskID}/events (WebSocket upgrade)
func (h *taskHandler) streamEvents(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "taskID")
    afterParam := r.URL.Query().Get("after")

    // Validate task exists
    // Accept WebSocket upgrade via websocket.Accept(w, r, nil)
    // Subscribe to EventHub with after parameter
    // Send historical events (replay)
    // Stream live events until task completes or client disconnects
    // On task_complete: send TaskCompleteData, close with StatusNormalClosure
    // Use c.CloseRead(ctx) for write-only mode
}
```

#### 6. Wire EventHub into Server
**File**: `pkg/api/server.go`
**Changes**:
- Add `eventHub *EventHub` field to `taskHandler` struct
- Initialize `NewEventHub()` in `Run()`
- Add route: public router gets `GET /api/v1/tasks/{taskID}/events` → `handler.streamEvents`
- Add route: internal router gets `POST /api/v1/tasks/{taskID}/events` → `handler.postEvents`

#### 7. EventHub Cleanup on Task Completion
**File**: `pkg/api/handler_status.go`
**Changes**: When a terminal status event is received (`completed` or `failed`), call `eventHub.Complete(taskID)` to notify WebSocket subscribers. Schedule cleanup after 5-minute TTL.

#### 8. Update OpenAPI Spec
**File**: `api/openapi.yaml`
**Changes**: Add:
- `POST /api/v1/tasks/{taskID}/events` — PostEventRequest → 200 | 400/404/410
- `GET /api/v1/tasks/{taskID}/events` — documented as WebSocket upgrade with `?after` query parameter. Note: OpenAPI 3.0 doesn't natively describe WebSocket, but document the upgrade handshake and message format in the description.

#### 9. Tests
**File**: `pkg/api/eventhub_test.go` (new)
- Test publish and subscribe
- Test `after` parameter replays only newer events
- Test ring buffer overflow (oldest events dropped)
- Test Complete() closes subscriber channels
- Test concurrent publish/subscribe safety

**File**: `pkg/api/handler_events_test.go` (new)
- Test POST events — valid request, task not found, task already terminal
- Test event validation (missing required fields)
- Follow existing test patterns: `newTestHandler()`, `testRouter()`, `postJSON()`
- Add kin-openapi contract validation for responses

**File**: `pkg/api/handler_ws_test.go` (new)
- Test WebSocket upgrade and event streaming
- Test `?after` reconnection parameter
- Test connection closes on task completion
- Use `httptest.NewServer` + `coder/websocket.Dial` for WebSocket tests

**File**: `pkg/api/server_test.go`
- Update `buildTestRouters()` and `testRouter()` to include new routes
- Add dual-port routing tests for new endpoints

### Success Criteria:

#### Automated Verification:
- [x] `make test` passes — all new tests plus existing tests with contract validation
- [x] `make lint-fix` passes
- [x] New routes appear in dual-port routing tests

#### Manual Verification:
- [ ] Can POST events to `localhost:8081/api/v1/tasks/{id}/events` and receive them via WebSocket at `localhost:8080/api/v1/tasks/{id}/events` using `websocat` or similar tool

**Implementation Note**: After completing this phase, pause for manual WebSocket verification before proceeding.

---

## Phase 4: Frontend — Task List Page

### Overview
Build the task list page with filtering, summary stats, and status badges. This is the first user-facing page.

### Changes Required:

#### 1. API Data Store
**File**: `web/src/lib/tasks.svelte.ts` (new)

```typescript
export class TasksStore {
    data: TaskResponse[] = $state([]);
    loading = $state(false);
    error: string | null = $state(null);

    async load(params?: { repo?: string; fleet?: string; active?: string }) {
        this.loading = true;
        this.error = null;
        try {
            const { data, error } = await api.GET('/api/v1/tasks', { params: { query: params } });
            if (error) throw new Error(error.error);
            this.data = data ?? [];
        } catch (e) {
            this.error = (e as Error).message;
        } finally {
            this.loading = false;
        }
    }
}
```

#### 2. Task List Page
**File**: `web/src/routes/tasks/+page.svelte`
- Read filter state from URL search params (`$page.url.searchParams`)
- Create `TasksStore` instance, call `load()` on mount and when filters change
- Render summary stats bar, filter bar, and task rows
- 30-second background poll via `setInterval` in `$effect`
- "Load more" pagination

#### 3. Shared Components

**`web/src/lib/components/StatusBadge.svelte`**
- Props: `status: string` (Pending, Running, Succeeded, Failed, TimedOut, Cancelled)
- Renders colored pill with icon + text label
- Color mapping: Pending=amber, Running=blue, Succeeded=green, Failed=red, TimedOut=orange, Cancelled=gray

**`web/src/lib/components/SummaryStats.svelte`**
- Props: `tasks: TaskResponse[]`
- Derives counts: active, pending, completed today, failed
- Renders horizontal bar of stat cards

**`web/src/lib/components/FilterBar.svelte`**
- URL-driven: reads from and writes to `$page.url.searchParams`
- Filters: status toggle (Active/All), repo dropdown, fleet dropdown, search text
- Debounced search input (300ms)

**`web/src/lib/components/TaskCard.svelte`**
- Props: `task: TaskResponse`
- Renders one row: status badge, repo name, task description (truncated), duration, fleet
- Click navigates to `/tasks/{taskID}`
- Duration: elapsed time for active tasks (updated every second via client-side timer), total for completed

**`web/src/lib/components/Header.svelte`**
- App header: "SHEPHERD" title, "Tasks" nav link, cluster indicator

#### 4. Layout
**File**: `web/src/routes/+layout.svelte`
- Sets `dark` class on `<html>` element (dark mode only for MVP)
- Renders `<Header>` + `<slot>`
- Max width 1200px for content

#### 5. Time Formatting Utility
**File**: `web/src/lib/format.ts` (new)
- `formatDuration(startTime: string, endTime?: string): string` — "4m 12s", "12m 03s"
- `formatRelativeTime(timestamp: string): string` — "2 minutes ago"
- `formatTimestamp(timestamp: string): string` — "12:30:05"

### Success Criteria:

#### Automated Verification:
- [x] `make web-build` succeeds
- [x] `make web-check` passes TypeScript checking
- [x] `make web-lint` passes

#### Manual Verification:
- [ ] Task list displays tasks from a running Go API (via `make web-dev` with `make run` in another terminal)
- [ ] Filters update URL and re-fetch data
- [ ] Status badges show correct colors
- [ ] Duration updates live for running tasks

**Implementation Note**: After completing this phase, pause for manual verification.

---

## Phase 5: Frontend — Task Detail + Live Streaming

### Overview
Build the task detail page with WebSocket-powered live event streaming for running tasks, event history for completed tasks, and PR card / error callout for terminal states.

### Changes Required:

#### 1. WebSocket Client
**File**: `web/src/lib/ws.ts` (new, ~60-80 lines)

Generic typed WebSocket wrapper:
- `connect(url: string)` — opens WebSocket, handles `onopen`/`onmessage`/`onclose`/`onerror`
- Reconnection with exponential backoff: 1s, 2s, 4s, 8s, 16s (max 30s) + jitter
- Max 5 retry attempts, then manual retry only
- JSON message parsing with type discrimination
- Callbacks: `onMessage(msg)`, `onStateChange(state)`

#### 2. Task Stream Store
**File**: `web/src/lib/task-stream.svelte.ts` (new)

```typescript
export class TaskStream {
    events: TaskEvent[] = $state([]);
    lastSequence = $state(0);
    streamPhase: 'idle' | 'connecting' | 'streaming' | 'completed' | 'error' = $state('idle');
    connectionState: 'connecting' | 'connected' | 'reconnecting' | 'disconnected' = $state('disconnected');

    connect(taskId: string) { ... }
    disconnect() { ... }
}
```

State transitions per the streaming architecture research (section 7):
- idle → connecting → streaming → completed
- streaming → connecting (on connection loss, reconnect with `?after=lastSequence`)
- connecting → error (after max retries)

Sequence gap detection: if incoming `event.sequence > lastSequence + 1`, reconnect with `?after=lastSequence`.

#### 3. Task Detail Store
**File**: `web/src/lib/task-detail.svelte.ts` (new)

```typescript
export class TaskDetailStore {
    task: TaskResponse | null = $state(null);
    loading = $state(false);
    error: string | null = $state(null);

    async load(taskId: string) { ... }  // GET /api/v1/tasks/{taskId}
}
```

#### 4. Task Detail Page
**File**: `web/src/routes/tasks/[taskID]/+page.svelte`

Layout: 70/30 split for running tasks (event stream left, metadata right), full width for completed.

Logic:
1. Load task metadata via REST on mount
2. If phase is "Running": connect TaskStream WebSocket
3. Render based on phase:
   - **Running**: LIVE indicator + event stream + metadata panel (sticky)
   - **Succeeded**: PR card (hero) + collapsed event log (first/last events shown)
   - **Failed**: Error callout + event log (last events shown, failing tool call expanded)
   - **Pending**: "Waiting for sandbox" message
4. On `task_complete` WebSocket message: refetch task via REST, update UI

#### 5. Event Rendering Components

**`web/src/lib/components/EventStream.svelte`**
- Props: `events: TaskEvent[]`, `isLive: boolean`, `taskPhase: string`
- Scrollable container with auto-scroll when at bottom
- "N new events" pill when user has scrolled up
- For completed tasks: collapse middle events, show "Show all N events" expander

**`web/src/lib/components/EventItem.svelte`**
- Props: `event: TaskEvent`
- Dispatches to sub-components based on `event.type`:

**`web/src/lib/components/ThinkingEvent.svelte`**
- Muted text, brain icon, dimmer color
- Collapsed if text > 3 lines

**`web/src/lib/components/ToolCallEvent.svelte`**
- Tool name badge (color-coded: file ops=blue, shell=amber, search=green)
- Input summary one-liner
- Expandable input/output sections

**`web/src/lib/components/ToolResultEvent.svelte`**
- Nested under tool call visually (indented)
- Success ✓ or failure ✗ indicator
- Output preview (collapsed by default, click to expand)

**`web/src/lib/components/ErrorEvent.svelte`**
- Red background tint, red border
- Always expanded

**`web/src/lib/components/PRCard.svelte`**
- Props: `prURL: string`, `repo: string`
- Hero element for succeeded tasks
- Shows PR number, title (parsed from URL), diff stats placeholder, "Open in GitHub" button

**`web/src/lib/components/ErrorCallout.svelte`**
- Props: `error: string`, `lastAction?: string`
- Red left border, light red background
- "Last agent action" line
- Copy button (copies error text to clipboard)

**`web/src/lib/components/ConnectionIndicator.svelte`**
- Props: `state: ConnectionState`
- Green dot = connected, yellow = reconnecting, red = disconnected
- Shows attempt count during reconnection

**`web/src/lib/components/Breadcrumb.svelte`**
- "Tasks / {taskID}" with link back to task list

#### 6. Keyboard Shortcuts
**File**: `web/src/lib/keyboard.ts` (new)

Register on `window.keydown`:
- `j`/`k` — move between tasks in list (track focused index)
- `Enter` — open focused task
- `Escape`/`Backspace` — return to task list
- `e` — expand all events in detail view
- `c` — collapse all events
- `G` — scroll to bottom / resume auto-scroll
- `/` — focus search input

Only active when no input/textarea is focused.

### Success Criteria:

#### Automated Verification:
- [x] `make web-build` succeeds
- [x] `make web-check` passes TypeScript checking
- [x] `make web-lint` passes

#### Manual Verification:
- [ ] Navigate to a running task and see live events streaming
- [ ] WebSocket reconnects after network interruption (can simulate by stopping/restarting API)
- [ ] Completed task shows PR card with working GitHub link
- [ ] Failed task shows error callout with last agent action
- [ ] Keyboard shortcuts work (j/k navigation, Enter to open, Escape to go back)

**Implementation Note**: After completing this phase, pause for manual WebSocket verification.

---

## Phase 6: Runner — Event Extraction

### Overview
Modify the runner to switch from `--output-format json` to `--output-format stream-json`, read CC stdout line-by-line, parse NDJSON into agent-agnostic `TaskEvent` objects, and POST them to the API.

### Changes Required:

#### 1. Switch to stream-json Output Format
**File**: `cmd/shepherd-runner/gorunner.go`
**Changes**: Change `--output-format` from `"json"` to `"stream-json"` at line 149.

#### 2. Line-by-Line Stdout Reading
**File**: `cmd/shepherd-runner/gorunner.go`
**Changes**: Replace `strings.Builder` stdout buffering with a pipe + scanner pattern:

The current `osExecutor.Run()` at `gorunner.go:44-77` buffers all stdout in a `strings.Builder`. Replace this with:
- Create `io.Pipe()` for stdout
- Set `cmd.Stdout` to the pipe writer
- Run a goroutine that reads the pipe with `bufio.Scanner` line by line
- Each line is passed to the event parser
- Stderr remains buffered (for git clone error messages etc.)

This change is specific to the `claude` invocation, not all subprocess calls. Add an `ExecOptions.StreamStdout` callback field that, when set, receives each line instead of buffering.

#### 3. stream-json Parser
**File**: `cmd/shepherd-runner/streamparser.go` (new)

```go
// StreamParser translates Claude Code stream-json NDJSON lines into TaskEvents.
type StreamParser struct {
    toolMap  map[string]string  // tool_use_id → tool_name
    sequence int64
}

// ParseLine processes one NDJSON line and returns zero or more TaskEvents.
func (p *StreamParser) ParseLine(line []byte) []TaskEvent { ... }
```

Mapping (from streaming architecture research section 6):
- `assistant` message with `text` content → `EventTypeThinking` (first 200 chars)
- `assistant` message with `tool_use` content → `EventTypeToolCall` (record tool_use_id → name)
- `user` message with `tool_result` content → `EventTypeToolResult` (look up tool name, first 200 chars)
- `result` message → no TaskEvent (terminal status goes through existing status endpoint)
- Parse errors → `EventTypeError`

Truncation policy:
- `Bash` command input: first 500 chars
- `Read` file content: first 200 chars + "... (truncated)"
- `Edit` input: file path + lengths only
- Thinking text: first 200 chars

#### 4. Event POSTing to API
**File**: `cmd/shepherd-runner/gorunner.go`
**Changes**: In the `Run()` method, after parsing each line into events, POST them to the API:

```go
// POST to internal API
client.PostEvents(ctx, taskID, events)
```

**File**: `pkg/runner/client.go`
**Changes**: Add `PostEvents(ctx, taskID string, events []TaskEvent) error` method.
- POST to `{baseURL}/api/v1/tasks/{taskID}/events`
- Fire-and-forget: log errors but don't fail the task if event POST fails
- The event stream is best-effort; the status POST remains the source of truth

#### 5. Preserve ccOutput Parsing
**File**: `cmd/shepherd-runner/gorunner.go`
**Changes**: The `result` message in stream-json replaces the old single JSON blob. Parse it for `total_cost_usd`, `num_turns`, `session_id` metrics logging. The `ccOutput` struct remains but is populated from the `result` line instead of the entire stdout.

#### 6. Tests
**File**: `cmd/shepherd-runner/streamparser_test.go` (new)
- Test each CC message type → TaskEvent mapping
- Test tool_use_id → tool_name correlation
- Test truncation logic
- Test malformed JSON handling (should not panic)
- Test sequence number monotonic increase

**File**: `pkg/runner/client_test.go`
- Test PostEvents() HTTP call (existing httptest patterns)

### Success Criteria:

#### Automated Verification:
- [ ] `make test` passes — new stream parser tests + existing runner tests
- [ ] `make lint-fix` passes
- [ ] `make build` succeeds

#### Manual Verification:
- [ ] Run the runner locally against a real task and observe events being POSTed to the API
- [ ] Verify events appear in WebSocket stream on the frontend

**Implementation Note**: After completing this phase, pause for manual end-to-end verification of the full pipeline (runner → API → WebSocket → frontend).

---

## Phase 7: Frontend Unit & Component Tests

### Overview
Set up Vitest for frontend testing and write unit tests for the most critical logic: WebSocket reconnection, event parsing, URL construction, time formatting, and component rendering.

### Changes Required:

#### 1. Vitest Setup
**File**: `web/package.json`
**Changes**: Add dev dependencies: `vitest`, `@testing-library/svelte`, `jsdom`

**File**: `web/vite.config.ts`
**Changes**: Add vitest config:
```typescript
import { defineConfig } from 'vitest/config';

export default defineConfig({
  // ... existing config
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
});
```

**File**: `web/package.json` scripts:
```json
{ "test": "vitest run", "test:watch": "vitest" }
```

#### 2. Unit Tests (Highest Priority)

**`web/src/lib/ws.test.ts`** — WebSocket reconnection logic:
- Backoff timer calculation (1s, 2s, 4s, 8s, 16s, capped at 30s)
- Retry counter increment and max retry check
- `?after=N` parameter construction from lastSequence
- Jitter calculation stays within bounds

**`web/src/lib/task-stream.test.ts`** — Event stream state machine:
- Sequence gap detection (incoming seq > lastSequence + 1)
- Event deduplication (incoming seq <= lastSequence)
- State transitions: idle→connecting→streaming→completed
- Events are appended in order

**`web/src/lib/format.test.ts`** — Time formatting:
- `formatDuration()` edge cases: 0s, <1m, >1h, missing endTime (uses now)
- `formatTimestamp()` with various timezones
- `formatRelativeTime()` thresholds

**`web/src/lib/tasks.svelte.test.ts`** — TasksStore:
- load() sets loading/data/error correctly
- Error handling (network error, non-200 response)

#### 3. Component Tests

**`web/src/lib/components/StatusBadge.test.ts`**
- Renders correct text and applies correct CSS class for each status

**`web/src/lib/components/EventItem.test.ts`**
- Dispatches to correct sub-component based on event type
- ThinkingEvent collapses long text
- ToolCallEvent shows tool badge with correct color category
- ErrorEvent is always expanded

#### 4. Makefile Target
**File**: `Makefile`
```makefile
.PHONY: web-test
web-test: web-install ## Run frontend unit and component tests.
	npm test --prefix web
```

### Success Criteria:

#### Automated Verification:
- [ ] `make web-test` passes all tests
- [ ] Test coverage for ws.ts, task-stream.svelte.ts, and format.ts > 80%
- [ ] `make web-build` still succeeds

#### Manual Verification:
- [ ] None required — this phase is fully automated

---

## Phase 8: Frontend Container & Kind Deployment

### Overview
Create the frontend Docker image (multi-stage build with nginx), add kustomize manifests, and integrate into the kind deployment pipeline.

### Changes Required:

#### 1. Frontend Dockerfile
**File**: `build/web/Dockerfile` (new)

```dockerfile
# Stage 1: Build
FROM node:22-alpine AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Serve
FROM nginx:1-alpine
COPY build/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/build /usr/share/nginx/html
EXPOSE 8080
```

#### 2. nginx Configuration
**File**: `build/web/nginx.conf` (new)

```nginx
server {
    listen 8080;
    root /usr/share/nginx/html;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

#### 3. Kustomize Manifests
**File**: `config/web/deployment.yaml` (new)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shepherd-web
  namespace: shepherd-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: shepherd-web
  template:
    metadata:
      labels:
        app: shepherd-web
    spec:
      containers:
        - name: web
          image: shepherd-web:latest
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet:
              path: /
              port: 8080
          resources:
            requests: { memory: "32Mi", cpu: "10m" }
            limits: { memory: "64Mi", cpu: "100m" }
```

**File**: `config/web/service.yaml` (new)
```yaml
apiVersion: v1
kind: Service
metadata:
  name: shepherd-web
  namespace: shepherd-system
spec:
  selector:
    app: shepherd-web
  ports:
    - port: 8080
      targetPort: 8080
```

**File**: `config/web/kustomization.yaml` (new)
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
  - service.yaml
```

#### 4. Integrate into Config Overlays
**File**: `config/default/kustomization.yaml`
**Changes**: Add `../web` to resources list.

**File**: `config/test/kustomization.yaml`
**Changes**: Add NodePort patch for shepherd-web service (port 30081 for frontend access from host):
```yaml
- patch: |-
    - op: add
      path: /spec/type
      value: NodePort
    - op: add
      path: /spec/ports/0/nodePort
      value: 30081
  target:
    kind: Service
    name: shepherd-web
```

#### 5. Kind Config Update
**File**: `test/e2e/kind-config.yaml`
**Changes**: Add port mapping for frontend:
```yaml
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: 30080
    protocol: TCP
  - containerPort: 30081
    hostPort: 30081
    protocol: TCP
```

#### 6. Makefile Targets
**File**: `Makefile`
**Changes**:
```makefile
FRONTEND_IMG ?= shepherd-web:latest

.PHONY: docker-build-web
docker-build-web: web-build ## Build frontend Docker image.
	docker build -f build/web/Dockerfile -t $(FRONTEND_IMG) .

.PHONY: ko-build-kind
ko-build-kind: ko-build-local ko-build-runner-local docker-build-web
	docker tag "$(IMG)" shepherd:latest
	docker tag "$(RUNNER_IMG)" shepherd-runner:latest
	kind load docker-image shepherd:latest --name "$(KIND_CLUSTER_NAME)"
	kind load docker-image shepherd-runner:latest --name "$(KIND_CLUSTER_NAME)"
	kind load docker-image $(FRONTEND_IMG) --name "$(KIND_CLUSTER_NAME)"
```

#### 7. Frontend VITE_API_URL Configuration
**File**: `web/src/lib/client.ts`
**Changes**: In kind, the frontend container needs to know the API URL. Options:
- The Vite dev proxy handles this in development
- In production (nginx), the frontend calls relative URLs (`/api/v1/tasks`) and nginx proxies to the API service

**File**: `build/web/nginx.conf`
**Changes**: Add API proxy:
```nginx
location /api/ {
    proxy_pass http://shepherd-shepherd-api:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

This proxies `/api/*` requests from the browser to the Go API service, including WebSocket upgrades.

### Success Criteria:

#### Automated Verification:
- [ ] `make docker-build-web` builds the frontend image successfully
- [ ] `make ko-build-kind` loads all three images into kind
- [ ] `kubectl get pods -n shepherd-system` shows shepherd-web pod running after `make deploy-test`
- [ ] `curl http://localhost:30081/` returns the SPA HTML

#### Manual Verification:
- [ ] Open `http://localhost:30081/tasks` in a browser — task list renders
- [ ] API calls from the frontend reach the Go API via nginx proxy
- [ ] WebSocket connection works through nginx proxy

**Implementation Note**: After completing this phase, pause for manual verification of the full kind deployment.

---

## Phase 9: E2E Tests (Playwright)

### Overview
Update the E2E stub runner to POST fake events (so the full WebSocket pipeline is exercised), set up Playwright for browser-based E2E tests, and run them against the full stack deployed in kind.

The current stub runner at `test/e2e/testrunner/main.go` fetches task data, sleeps 5 seconds, and reports `"completed"`. It posts **zero events**, which means the WebSocket streaming path is never tested end-to-end. The stub runner must be updated to POST a realistic sequence of fake events during its work simulation so Playwright can verify the full pipeline: stub runner → `POST /events` → EventHub → WebSocket → browser.

### Changes Required:

#### 1. Update Stub Runner to POST Events
**File**: `test/e2e/testrunner/main.go`
**Changes**: Add event posting during the `executeTask()` function. Replace the 5-second sleep with a scripted sequence of events interspersed with short delays:

```go
func executeTask(ctx context.Context, ta TaskAssignment) error {
    client := &http.Client{Timeout: 30 * time.Second}

    // 1. Fetch task data (existing)
    // ...

    // 2. Report started status (existing)
    if err := reportStatus(ctx, ta, "started", "cloning repository"); err != nil {
        return err
    }

    // 3. POST a realistic sequence of fake events
    events := []event{
        {Seq: 1, Type: "thinking",    Summary: "Analyzing the codebase structure to understand the project layout..."},
        {Seq: 2, Type: "tool_call",   Summary: "Reading src/main.go", Tool: "Read", Input: map[string]any{"file_path": "src/main.go"}},
        {Seq: 3, Type: "tool_result", Summary: "package main\n\nfunc main() {...}", Tool: "Read", Success: true},
        {Seq: 4, Type: "tool_call",   Summary: "Editing src/main.go", Tool: "Edit", Input: map[string]any{"file_path": "src/main.go"}},
        {Seq: 5, Type: "tool_result", Summary: "Modified lines 10-15 (+3/-1)", Tool: "Edit", Success: true},
        {Seq: 6, Type: "thinking",    Summary: "Running tests to verify the changes..."},
        {Seq: 7, Type: "tool_call",   Summary: "go test ./...", Tool: "Bash", Input: map[string]any{"command": "go test ./..."}},
        {Seq: 8, Type: "tool_result", Summary: "PASS\nok  \tproject/pkg\t0.42s", Tool: "Bash", Success: true},
    }

    for _, e := range events {
        if err := postEvent(ctx, client, ta, e); err != nil {
            slog.Warn("failed to post event", "seq", e.Seq, "error", err)
            // Best-effort — don't fail the task if event posting fails
        }
        // Small delay between events so Playwright can observe them arriving
        select {
        case <-time.After(500 * time.Millisecond):
        case <-ctx.Done():
            return ctx.Err()
        }
    }

    // 4. Report completed status with a fake PR URL
    return reportStatus(ctx, ta, "completed", "stub runner completed successfully",
        map[string]any{"pr_url": "https://github.com/test-org/test-repo/pull/42"})
}

func postEvent(ctx context.Context, client *http.Client, ta TaskAssignment, e event) error {
    eventsURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/events"
    payload, _ := json.Marshal(map[string]any{
        "events": []map[string]any{{
            "sequence":  e.Seq,
            "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
            "type":      e.Type,
            "summary":   e.Summary,
            "tool":      e.Tool,
            "input":     e.Input,
            "output":    &map[string]any{"success": e.Success, "summary": e.Summary},
        }},
    })
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsURL, bytes.NewReader(payload))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("event POST returned %d: %s", resp.StatusCode, body)
    }
    return nil
}
```

This gives Playwright a realistic event stream: thinking events (rendered muted), tool calls with different tool types (Read=blue, Edit=blue, Bash=amber), tool results with success indicators, and a completed status with a PR URL.

The total time is ~4 seconds (8 events × 500ms), replacing the old 5-second sleep. The existing Go E2E tests that `Eventually` poll for the Running→Succeeded transition continue to work because the timing is similar.

#### 2. Update reportStatus to Support Details
**File**: `test/e2e/testrunner/main.go`
**Changes**: The existing `reportStatus()` only sends `event` and `message`. Update it to accept an optional `details` map so the completed event can include `pr_url`:

```go
func reportStatus(ctx context.Context, ta TaskAssignment, event, message string, details ...map[string]any) error {
    payload := map[string]any{
        "event":   event,
        "message": message,
    }
    if len(details) > 0 {
        payload["details"] = details[0]
    }
    // ... rest unchanged
}
```

#### 3. Playwright Setup
**File**: `web/package.json`
**Changes**: Add dev dependency: `@playwright/test`

**File**: `web/playwright.config.ts` (new)
```typescript
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  baseURL: process.env.BASE_URL ?? 'http://localhost:30081',
  use: {
    colorScheme: 'dark',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  retries: 1,
  reporter: [['html', { open: 'never' }]],
});
```

#### 4. Test Helper for API Seeding
**File**: `web/e2e/helpers.ts` (new)

```typescript
const API_URL = process.env.API_URL ?? 'http://localhost:30080';

export async function createTask(opts: { repo: string; description: string }): Promise<string> {
  const res = await fetch(`${API_URL}/api/v1/tasks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      repo: { url: `https://github.com/${opts.repo}` },
      task: { description: opts.description },
      callbackURL: 'https://example.com/callback',
      runner: { sandboxTemplateName: 'e2e-runner' },
    }),
  });
  const data = await res.json();
  return data.id;
}

export async function waitForTaskPhase(taskId: string, phase: string, timeoutMs = 120_000): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const res = await fetch(`${API_URL}/api/v1/tasks/${taskId}`);
    const task = await res.json();
    if (task.status.phase === phase) return;
    await new Promise(r => setTimeout(r, 1000));
  }
  throw new Error(`Task ${taskId} did not reach phase ${phase} within ${timeoutMs}ms`);
}
```

#### 5. E2E Test Scenarios — Task List
**File**: `web/e2e/task-list.spec.ts`

```typescript
test('task list loads and displays tasks', async ({ page }) => {
  // Seed: create a task via API, wait for it to complete
  // Navigate to /tasks
  // Verify task appears with correct status badge, repo, description
});

test('clicking a task navigates to detail view', async ({ page }) => {
  // Seed: create completed task
  // Click the task row
  // Verify URL changes to /tasks/{taskID}
  // Verify task detail page renders
});

test('filtering tasks by repository updates URL', async ({ page }) => {
  // Seed: create tasks with different repos
  // Apply repo filter
  // Verify URL contains ?repo= parameter
  // Verify only matching tasks shown
});

test('empty state shows helpful message', async ({ page }) => {
  // Navigate to /tasks with a filter that matches nothing
  // Verify "No tasks match your filters" message
});

test('browser back button preserves filter state', async ({ page }) => {
  // Apply filter → navigate to detail → press back
  // Verify filter is still applied in URL and UI
});
```

#### 6. E2E Test Scenarios — Task Detail (Full Stack WebSocket)
**File**: `web/e2e/task-detail.spec.ts`

These tests exercise the **real** WebSocket pipeline end-to-end (stub runner → API EventHub → WebSocket → browser). No mocking.

```typescript
test('live events stream during running task', async ({ page }) => {
  // 1. Create task via API
  // 2. Navigate to /tasks/{taskID} immediately (before stub runner finishes)
  // 3. Verify "LIVE" indicator appears
  // 4. Wait for events to appear in the event stream
  // 5. Verify at least one thinking event (muted text)
  // 6. Verify at least one tool_call event (tool badge visible)
  // 7. Verify at least one tool_result event (success indicator)
});

test('task completes and shows PR link', async ({ page }) => {
  // 1. Create task via API
  // 2. Navigate to task detail
  // 3. Wait for task to reach Succeeded phase
  // 4. Verify PR card appears with "Open in GitHub" link
  // 5. Verify PR URL matches the fake URL from stub runner
});

test('completed task shows event history', async ({ page }) => {
  // 1. Create task, wait for completion
  // 2. Navigate to task detail
  // 3. Verify event log shows historical events (from EventHub buffer)
  // 4. Verify events are in sequence order
});

test('task detail shows metadata', async ({ page }) => {
  // Verify: status badge, repo name, task description, duration
});

test('deep link to a specific task works', async ({ page }) => {
  // Navigate directly to /tasks/{taskID} via URL
  // Verify page loads correctly
});
```

#### 7. E2E Test Scenarios — WebSocket Behavior (Mocked for Determinism)
**File**: `web/e2e/websocket-behavior.spec.ts`

These tests use Playwright's `page.routeWebSocket()` to test WebSocket edge cases deterministically (reconnection, disconnection) without depending on real server timing:

```typescript
test('reconnects after WebSocket disconnect', async ({ page }) => {
  // Use page.routeWebSocket() to intercept WS connection
  // Send a few events, then close the connection abnormally
  // Verify "Reconnecting..." indicator appears
  // Accept the reconnection, send more events
  // Verify events continue streaming without gaps
});

test('shows disconnected state after max retries', async ({ page }) => {
  // Intercept WS, reject all reconnection attempts
  // Verify "Disconnected" indicator appears after max retries
});
```

#### 8. Makefile Targets
**File**: `Makefile`
```makefile
.PHONY: web-e2e
web-e2e: ## Run Playwright E2E tests against deployed stack.
	npx --prefix web playwright test

.PHONY: web-e2e-install
web-e2e-install: ## Install Playwright browsers.
	npx --prefix web playwright install chromium
```

#### 9. Full E2E Target (Kind + Playwright)
**File**: `Makefile`

For CI, a single target that stands up the full stack and runs all E2E tests:
```makefile
.PHONY: test-e2e-frontend
test-e2e-frontend: kind-create ko-build-kind install-agent-sandbox install deploy-test deploy-e2e-fixtures web-e2e-install ## Full frontend E2E: kind cluster + Playwright.
	npx --prefix web playwright test
	$(MAKE) kind-delete
```

The flow:
1. `kind-create` — creates kind cluster with port mappings (30080 API, 30081 frontend)
2. `ko-build-kind` — builds all 3 images (API, stub runner, frontend), loads into kind
3. `install-agent-sandbox` + `install` + `deploy-test` — deploys the full stack
4. `deploy-e2e-fixtures` — deploys the SandboxTemplate (stub runner uses `e2e-runner` template)
5. Playwright tests create tasks via the API, the stub runner picks them up, POSTs events, and Playwright observes the results in the browser

### Success Criteria:

#### Automated Verification:
- [ ] `make test-e2e` still passes — existing Go E2E tests unbroken by stub runner changes
- [ ] `make web-e2e` passes all Playwright tests against the kind-deployed stack
- [ ] The "live events stream" test observes real events flowing through the full pipeline
- [ ] Test report generated at `web/playwright-report/`
- [ ] Tests complete within 5 minutes

#### Manual Verification:
- [ ] Review Playwright test report for any flaky tests
- [ ] Verify test traces capture meaningful screenshots on failure

---

## Testing Strategy Summary

| Layer | Tool | Location | What It Tests | Run Command |
|-------|------|----------|---------------|-------------|
| Go unit + envtest | `go test` | `pkg/api/*_test.go` | Handlers, EventHub, contract validation | `make test` |
| Go E2E | Ginkgo | `test/e2e/` | Full K8s lifecycle (existing) | `make test-e2e` |
| Frontend unit | Vitest | `web/src/**/*.test.ts` | WS reconnection, event parsing, formatting, components | `make web-test` |
| Frontend E2E | Playwright | `web/e2e/*.spec.ts` | Browser flows against kind stack | `make web-e2e` |

## Performance Considerations

- **EventHub ring buffer**: 1000 events per task, ~500 bytes per event = ~500KB per active task. With 100 concurrent tasks: ~50MB. Acceptable for single-replica MVP.
- **WebSocket fan-out**: Each subscriber gets its own channel. With 10 viewers per task, fan-out is O(subscribers). No broadcast optimization needed at this scale.
- **Frontend bundle**: Svelte 5 (~2KB runtime) + Tailwind v4 (~8KB gzipped) + openapi-fetch (~6KB) + Shiki (lazy-loaded). Total initial load < 50KB gzipped.
- **Event stream rendering**: Events are appended to a `$state` array. Svelte 5's fine-grained reactivity means only new events trigger DOM updates, not re-renders of existing events.

## References

- Research: `thoughts/research/2026-02-19-shepherd-frontend-design.md` — Frontend design specification
- Research: `thoughts/research/2026-02-24-svelte5-frontend-rules-tanstack-evaluation.md` — Svelte 5 rules, TanStack evaluation
- Research: `thoughts/research/2026-02-18-agent-visibility-streaming-architecture.md` — WebSocket architecture, EventHub, event schema
- Rules: `.claude/rules/frontend/svelte5-runes.md` — Svelte 5 syntax rules
- Rules: `.claude/rules/frontend/sveltekit.md` — SvelteKit conventions
- Existing tests: `pkg/api/handler_tasks_test.go` — Test patterns to follow
- Existing E2E: `test/e2e/` — Kind cluster E2E infrastructure
- Runner: `cmd/shepherd-runner/gorunner.go` — Current runner implementation
