---
date: 2026-02-24T22:49:00+01:00
researcher: claude
git_commit: 5b3621d70eda7ab55683baf79aa95e37cacb94cc
branch: stripe_minons
repository: NissesSenap/shepherd_init
topic: "Svelte 5 Claude Code rules, TanStack Query evaluation for WebSocket-first app"
tags: [research, frontend, svelte, svelte5, runes, tanstack-query, websocket, claude-code-rules]
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

### Rule File Placement

Place rules in the frontend project's CLAUDE.md so they're automatically picked up when CC works in that directory:

```
web/
├── CLAUDE.md          ← Svelte 5 rules here
├── src/
├── package.json
└── svelte.config.js
```

### Proposed CLAUDE.md for Frontend

```markdown
# Shepherd Web UI

SvelteKit app with Svelte 5. TypeScript required for all files.

## Svelte 5 Rules — MANDATORY

This project uses Svelte 5 runes. NEVER use Svelte 4 syntax.
Every .svelte and .svelte.ts file uses runes mode.

### State: use `$state`, NEVER plain `let` for reactive variables

```svelte
<!-- WRONG: plain let is not reactive in runes mode -->
<script lang="ts">
  let count = 0
</script>

<!-- CORRECT -->
<script lang="ts">
  let count = $state(0)
</script>
```

Arrays and objects get deep reactivity — mutations work directly:

```svelte
<script lang="ts">
  let items = $state(['a', 'b'])
  // items.push('c') works — no dummy reassignment needed
</script>
```

### Derived values: use `$derived`, NEVER `$:`

```svelte
<!-- WRONG -->
$: doubled = count * 2

<!-- CORRECT -->
const doubled = $derived(count * 2)

<!-- For multi-line derivations -->
const total = $derived.by(() => {
  let sum = 0
  for (const n of numbers) sum += n
  return sum
})
```

### Side effects: use `$effect`, NEVER `$:` for side effects

```svelte
<!-- WRONG -->
$: console.log(count)

<!-- CORRECT -->
$effect(() => {
  console.log(count)
})
```

Return a cleanup function from `$effect` when needed:

```svelte
$effect(() => {
  const interval = setInterval(() => tick(), 1000)
  return () => clearInterval(interval)
})
```

NEVER update `$state` inside `$derived`. Use `$effect` for side effects, `$derived` for pure computations only.

### Props: use `$props()`, NEVER `export let`

```svelte
<!-- WRONG -->
<script lang="ts">
  export let name: string
  export let count = 0
</script>

<!-- CORRECT -->
<script lang="ts">
  let { name, count = 0 }: { name: string; count?: number } = $props()
</script>
```

For rest props (replaces `$$restProps`):

```svelte
<script lang="ts">
  let { value, ...rest } = $props()
</script>
<input {value} {...rest} />
```

### Events: use `onclick`, NEVER `on:click`

```svelte
<!-- WRONG: directive syntax -->
<button on:click={handler}>Click</button>
<button on:click|preventDefault={handler}>Submit</button>

<!-- CORRECT: property syntax -->
<button onclick={handler}>Click</button>
<button onclick={(e) => { e.preventDefault(); handler(e) }}>Submit</button>
```

Shorthand when function name matches event:

```svelte
<script lang="ts">
  function onclick() { /* ... */ }
</script>
<button {onclick}>Click</button>
```

### Component events: use callback props, NEVER `createEventDispatcher`

```svelte
<!-- WRONG -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte'
  const dispatch = createEventDispatcher()
</script>

<!-- CORRECT: pass callbacks as props -->
<script lang="ts">
  let { onsubmit }: { onsubmit?: (data: FormData) => void } = $props()
</script>
<button onclick={() => onsubmit?.(formData)}>Submit</button>
```

### Content: use snippets, NEVER slots

```svelte
<!-- WRONG: slot syntax -->
<div class="card">
  <slot />
</div>

<!-- CORRECT: children snippet -->
<script lang="ts">
  import type { Snippet } from 'svelte'
  let { children }: { children: Snippet } = $props()
</script>
<div class="card">
  {@render children()}
</div>
```

Named snippets replace named slots:

```svelte
<!-- Component definition -->
<script lang="ts">
  import type { Snippet } from 'svelte'
  let { header, children }: { header?: Snippet; children: Snippet } = $props()
</script>
<header>{@render header?.()}</header>
<main>{@render children()}</main>

<!-- Usage -->
<Layout>
  {#snippet header()}
    <h1>Title</h1>
  {/snippet}
  <p>Main content</p>
</Layout>
```

### SvelteKit: use `$app/state`, NEVER `$app/stores`

```svelte
<!-- WRONG -->
<script lang="ts">
  import { page } from '$app/stores'
</script>
<p>{$page.url.pathname}</p>

<!-- CORRECT -->
<script lang="ts">
  import { page } from '$app/state'
</script>
<p>{page.url.pathname}</p>
```

### Shared state: use `$state` in `.svelte.ts` files, avoid writable/readable stores

```typescript
// WRONG: Svelte 4 store pattern
import { writable } from 'svelte/store'
export const count = writable(0)

// CORRECT: Svelte 5 $state in .svelte.ts file
// counter.svelte.ts
export const counter = $state({ count: 0 })
export function increment() { counter.count++ }
```

For shared state across components, prefer class-based pattern:

```typescript
// task-store.svelte.ts
export class TaskStore {
  data: Task[] = $state([])
  loading = $state(false)
  error: string | null = $state(null)

  async load() { /* ... */ }
}
```

Use `setContext`/`getContext` for SSR-safe shared state (not module-level singletons).

### Bindable props: use `$bindable()` explicitly

```svelte
<!-- Props are NOT bindable by default in Svelte 5 -->
<script lang="ts">
  let { value = $bindable('') }: { value: string } = $props()
</script>
<input bind:value />
```

## Tech stack

- SvelteKit (Svelte 5), TypeScript, Vite
- No TanStack Query — use `$state` classes for data fetching
- WebSocket for real-time event streaming, plain fetch for REST

## Patterns

- All state management via `$state` classes in `.svelte.ts` files
- WebSocket connection managed via `$effect` cleanup in layout
- Context API (`setContext`/`getContext`) for sharing state down the component tree
- URL-driven filters via SvelteKit's `page.url.searchParams`
```

## Syntax Quick Reference

For embedding in the CLAUDE.md or as a separate reference:

| Svelte 4 (NEVER use) | Svelte 5 (ALWAYS use) |
|---|---|
| `let count = 0` | `let count = $state(0)` |
| `$: doubled = count * 2` | `const doubled = $derived(count * 2)` |
| `$: { sideEffect() }` | `$effect(() => { sideEffect() })` |
| `export let name` | `let { name } = $props()` |
| `$$restProps` | `let { ...rest } = $props()` |
| `on:click={fn}` | `onclick={fn}` |
| `on:click\|preventDefault` | `(e) => { e.preventDefault(); fn(e) }` |
| `createEventDispatcher()` | callback props via `$props()` |
| `<slot />` | `{@render children()}` |
| `<slot name="x" />` | `{@render x()}` with `{#snippet x()}...{/snippet}` |
| `beforeUpdate` / `afterUpdate` | `$effect.pre` / `$effect` |
| `import { page } from '$app/stores'` | `import { page } from '$app/state'` |
| `writable()` / `readable()` | `$state()` in `.svelte.ts` files |
| `items = items` (reactivity hack) | `items.push(x)` (deep reactivity) |

## Compiler Errors to Know

These fire when Svelte 4 syntax leaks into a runes-mode component:

| Error | Cause | Fix |
|---|---|---|
| `legacy_export_invalid` | `export let x` | `let { x } = $props()` |
| `legacy_reactive_statement_invalid` | `$: x = ...` | `$derived(...)` or `$effect(...)` |
| `legacy_props_invalid` | `$$props` | `$props()` |
| `legacy_rest_props_invalid` | `$$restProps` | `let { ...rest } = $props()` |

## Historical Context

- `thoughts/research/2026-02-19-shepherd-frontend-design.md` — Original frontend design document recommending React. The project owner has since leaned toward Svelte 5 for its no-virtual-DOM approach.
- `thoughts/research/2026-02-18-agent-visibility-streaming-architecture.md` — WebSocket streaming architecture design. The `TaskEvent` schema and WebSocket endpoint (`GET /api/v1/tasks/{taskID}/events`) defined there are what the frontend consumes.

## Open Questions

1. **SvelteKit vs plain Svelte + Vite**: SvelteKit provides file-based routing, SSR (which we'd disable), and `$app/state`. For a 2-page SPA, is SvelteKit overhead justified, or is plain Svelte + a lightweight router (like `svelte-spa-router`) sufficient?

2. **CSS approach**: Tailwind CSS, plain CSS with Svelte's scoped styles, or a component library? The dark-mode-first developer tool aesthetic from the design doc needs a decision.

3. **Monorepo structure**: Should the frontend live in `web/` at the repo root, or in a separate repo? If in-repo, the CLAUDE.md rules activate automatically when CC works in that directory.
