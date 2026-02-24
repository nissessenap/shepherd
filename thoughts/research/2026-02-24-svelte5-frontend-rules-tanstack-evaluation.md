---
date: 2026-02-24T22:49:00+01:00
researcher: claude
git_commit: 5b3621d70eda7ab55683baf79aa95e37cacb94cc
branch: stripe_minons
repository: NissesSenap/shepherd_init
topic: "Svelte 5 .claude/rules/ for monorepo, TanStack Query evaluation for WebSocket-first app"
tags: [research, frontend, svelte, svelte5, runes, tanstack-query, websocket, claude-rules]
status: complete
last_updated: 2026-02-24
last_updated_by: claude
---

# Research: Svelte 5 Claude Code Rules and TanStack Query Evaluation

**Date**: 2026-02-24T22:49:00+01:00
**Researcher**: claude
**Git Commit**: 5b3621d70eda7ab55683baf79aa95e37cacb94cc
**Branch**: stripe_minons
**Repository**: NissesSenap/shepherd_init

## Research Question

1. Do we actually need TanStack Query for a WebSocket-first project?
2. How should Claude Code rules be structured to enforce Svelte 5 runes syntax and prevent Svelte 4 patterns?

## Summary

**TanStack Query**: Not needed. Shepherd's frontend is WebSocket-first for real-time events and has a tiny REST surface (task list, task detail). TanStack Query's cache/dedup/stale-while-revalidate model is designed for REST request/response patterns and fights append-only WebSocket streams. Plain Svelte 5 `$state` classes handle both the WebSocket stream and the small REST surface with less code and no dependency. If the REST surface grows significantly later, TanStack Query v6 (which supports Svelte 5 runes natively) can be added incrementally.

**Svelte 5 rules**: A CLAUDE.md in the frontend directory with concrete before/after examples is the most effective approach. The rules cover the 8 syntax changes that matter: `$state` vs `let`, `$derived` vs `$:`, `$effect` vs `$:` side effects, `$props` vs `export let`, `onclick` vs `on:click`, snippets vs slots, callback props vs `createEventDispatcher`, and `$app/state` vs `$app/stores`.

## TanStack Query Evaluation

### What TanStack Query Provides

- **Caching with stale-while-revalidate**: Cache keyed by query key, serves stale data while refetching in background
- **Request deduplication**: 10 components mounting with same query key = 1 network request
- **Background refetching**: Auto-refetch on window focus, network reconnect, configurable intervals
- **Mutation lifecycle**: `isPending`/`isError`/`isSuccess` states, optimistic updates, cache invalidation on success
- **DevTools**: Visual cache inspector for debugging

### Why It Doesn't Fit Shepherd

Shepherd's frontend data flow:

```
WebSocket (primary):  Server pushes TaskEvents in real-time → append to event list
REST (secondary):     GET /tasks (initial load), GET /tasks/:id (detail), POST /tasks (rare)
```

| Concern | TanStack Query | Plain Svelte 5 `$state` |
|---------|---------------|------------------------|
| WebSocket event stream | Awkward — `setQueryData((old) => [...old, event])` on every message | Natural — `this.events.push(event)` (Svelte 5 proxies arrays) |
| Task list initial load | Good fit (cache, dedup, stale-while-revalidate) | ~15 lines of `$state` class with `load()` method |
| Task detail | Good fit (cache from list, instant navigation) | ~15 lines, no cross-page cache (acceptable for 2-page app) |
| Task creation mutation | Useful (`isPending` state + `invalidateQueries`) | ~10 lines manual |
| Bundle size | @tanstack/svelte-query + @tanstack/query-core (~40KB) | 0 |
| Svelte 5 compatibility | v6 required (v5 is "buggy and unreliable" with runes) | Native |

The honest trade-off: TanStack Query saves ~40 lines of loading/error state boilerplate across 2-3 REST endpoints. It costs a dependency, the v6 thunk API pattern, and awkward integration for the primary data flow (WebSocket). For a 2-page monitoring app where most data arrives via WebSocket, the dependency isn't justified.

### Recommended Pattern: `$state` Classes

```typescript
// lib/task-stream.svelte.ts — WebSocket event stream
export class TaskStream {
  events: TaskEvent[] = $state([])
  connected = $state(false)
  error: string | null = $state(null)
  private ws: WebSocket | null = null
  private lastSequence = 0

  connect(taskId: string) {
    const url = `wss://api/v1/tasks/${taskId}/events?after=${this.lastSequence}`
    this.ws = new WebSocket(url)
    this.ws.onopen = () => { this.connected = true }
    this.ws.onmessage = (e) => {
      const msg = JSON.parse(e.data)
      if (msg.type === 'task_event') {
        this.events.push(msg.data)
        this.lastSequence = msg.data.sequence
      }
    }
    this.ws.onclose = () => {
      this.connected = false
      // reconnect with lastSequence for replay
      setTimeout(() => this.connect(taskId), 1000 + Math.random() * 2000)
    }
  }

  disconnect() { this.ws?.close() }
}
```

```typescript
// lib/tasks.svelte.ts — REST task list
export class TasksStore {
  data: Task[] = $state([])
  loading = $state(false)
  error: string | null = $state(null)

  async load() {
    this.loading = true
    this.error = null
    try {
      const res = await fetch('/api/v1/tasks')
      if (!res.ok) throw new Error(res.statusText)
      this.data = await res.json()
    } catch (e) {
      this.error = (e as Error).message
    } finally {
      this.loading = false
    }
  }
}
```

Both classes are ~20 lines, zero dependencies, fully typed, and work natively with Svelte 5's reactivity system. The WebSocket class gets automatic deep reactivity on `events.push()` because Svelte 5 proxies `$state` arrays.

## Svelte 5 Claude Code Rules

### Why `.claude/rules/` Instead of CLAUDE.md

The project is a monorepo with Go backend and Svelte frontend in `web/`. The root `CLAUDE.md` contains Go-specific instructions (build commands, linting, testing patterns). Putting Svelte rules in the same file would pollute the Go context and vice versa.

Claude Code's `.claude/rules/` system solves this with `paths` frontmatter — rules scoped to `web/**/*.svelte` only activate when CC works on Svelte files. This keeps each rule file focused and avoids cross-contamination.

### Rule File Structure

```
.claude/
├── rules/
│   └── frontend/
│       ├── svelte5-runes.md    ← Svelte 5 syntax (scoped to web/**/*.svelte, web/**/*.svelte.ts)
│       └── sveltekit.md        ← SvelteKit conventions, data fetching (scoped to web/**/*.svelte, web/**/*.ts)
```

Rules use YAML frontmatter with `paths` to scope activation:

```yaml
---
paths:
  - "web/**/*.svelte"
  - "web/**/*.svelte.ts"
---
```

### What the Rules Cover

**`svelte5-runes.md`** — The 8 critical Svelte 4→5 syntax changes with wrong/correct examples:
1. `$state` vs plain `let`
2. `$derived` vs `$:`
3. `$effect` vs `$:` side effects
4. `$props()` vs `export let`
5. `$bindable()` for two-way binding
6. `onclick` vs `on:click` (including modifier replacements)
7. Callback props vs `createEventDispatcher`
8. Snippets/`{@render}` vs slots

**`sveltekit.md`** — SvelteKit and data fetching conventions:
1. `$app/state` vs `$app/stores`
2. TypeScript required
3. `$state` classes for data fetching (no TanStack Query)
4. WebSocket pattern with `$effect` cleanup
5. SvelteKit 2 error/redirect changes

## Code References

The rules files that implement the Svelte 5 conventions:
- `.claude/rules/frontend/svelte5-runes.md` — Svelte 5 runes syntax (scoped to `web/**/*.svelte`, `web/**/*.svelte.ts`)
- `.claude/rules/frontend/sveltekit.md` — SvelteKit conventions and data fetching patterns (scoped to `web/**/*.svelte`, `web/**/*.ts`)

## Historical Context

- `thoughts/research/2026-02-19-shepherd-frontend-design.md` — Original frontend design document recommending React. The project owner has since leaned toward Svelte 5 for its no-virtual-DOM approach.
- `thoughts/research/2026-02-18-agent-visibility-streaming-architecture.md` — WebSocket streaming architecture design. The `TaskEvent` schema and WebSocket endpoint (`GET /api/v1/tasks/{taskID}/events`) defined there are what the frontend consumes.

## Decisions

1. **SvelteKit with `adapter-static`**: SvelteKit is the official way to build Svelte apps. For a 2-page SPA the overhead is near zero — it's file conventions, not runtime bloat. We get `$app/state`, `$app/navigation`, file-based routing, and the conventional project structure out of the box. `adapter-static` disables SSR and produces a plain SPA. Going plain Svelte + Vite would mean giving up those built-ins and picking a third-party router for what SvelteKit provides for free.

2. **Svelte scoped styles with CSS custom properties for theming**: Zero dependencies. Svelte's `<style>` blocks are already scoped to the component — one of its headline features. CSS custom properties handle dark mode cleanly: define variables on `:root`, reference in components. The app is 2 pages with a developer-tool aesthetic (monospace, dense, dark) — straightforward CSS, not a design system problem. Tailwind adds a dependency and a different mental model for a Go team; for this surface area it doesn't pay back. Component libraries add opinions we'd fight for a custom monitoring UI.

3. **Monorepo**: Frontend lives in `web/` at the repo root. The `.claude/rules/frontend/` rules activate automatically when CC works on `web/**` files. Monorepo simplifies CI (contract tests run on the same PR) and keeps the Go API types and frontend TypeScript types in sync.
