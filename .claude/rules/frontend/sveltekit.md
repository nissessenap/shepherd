---
paths:
  - "web/**/*.svelte"
  - "web/**/*.ts"
  - "web/**/*.svelte.ts"
---

# SvelteKit Conventions

## Use `$app/state`, NEVER `$app/stores`

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

`$app/state` properties are fine-grained reactive — `page.data` updates don't invalidate `page.url`.

## TypeScript required

All `<script>` blocks must use `lang="ts"`:

```svelte
<script lang="ts">
  // ...
</script>
```

All `.ts` and `.svelte.ts` files must have proper types. Avoid `any`.

## Data fetching: plain `$state` classes, no TanStack Query

Use `$state` classes in `.svelte.ts` files for data fetching with the typed `openapi-fetch` client:

```typescript
// lib/tasks.svelte.ts
import { api } from "$lib/client.js";
import type { components } from "$lib/api.js";

type TaskResponse = components["schemas"]["TaskResponse"];

export class TasksStore {
  data: TaskResponse[] = $state([])
  loading = $state(false)
  error: string | null = $state(null)

  async load(params?: { repo?: string }) {
    this.loading = true
    this.error = null
    try {
      const { data, response } = await api.GET("/api/v1/tasks", {
        params: { query: params },
      })
      if (!response.ok) throw new Error(`${response.status}`)
      this.data = data ?? []
    } catch (e) {
      this.error = (e as Error).message
    } finally {
      this.loading = false
    }
  }
}
```

## WebSocket: `$state` class with `$effect` cleanup

```typescript
// lib/task-stream.svelte.ts
export class TaskStream {
  events: TaskEvent[] = $state([])
  connected = $state(false)
  private ws: WebSocket | null = null

  connect(url: string) {
    this.ws = new WebSocket(url)
    this.ws.onopen = () => { this.connected = true }
    this.ws.onmessage = (e) => { this.events.push(JSON.parse(e.data)) }
    this.ws.onclose = () => { this.connected = false }
  }

  disconnect() { this.ws?.close() }
}
```

Connect via `$effect` in a layout:

```svelte
<script lang="ts">
  import { setContext } from 'svelte'
  import { TaskStream } from '$lib/task-stream.svelte'

  const stream = new TaskStream()
  setContext('task-stream', stream)

  $effect(() => {
    stream.connect('wss://...')
    return () => stream.disconnect()
  })
</script>
```

## Testability: extract logic from `.svelte.ts` into plain `.ts`

Complex logic in `$state` classes (state machines, parsers, classification) should be extracted to plain `.ts` files so Vitest can test them without Svelte compilation. Keep `.svelte.ts` files thin — they wire reactive state to pure functions, not contain the logic itself.

```typescript
// BAD: logic coupled to $state in task-stream.svelte.ts
export class TaskStream {
  handleMessage(msg: WSMessage): void {
    // 30 lines of sequence/gap/dedup logic...
  }
}

// GOOD: logic extracted to stream-logic.ts (pure, testable)
export function classifyMessage(msg: WSMessage, lastSeq: number): MessageAction { ... }

// task-stream.svelte.ts (thin reactive wrapper)
import { classifyMessage } from "./stream-logic.js";
export class TaskStream {
  handleMessage(msg: WSMessage): void {
    const result = classifyMessage(msg, this.lastSequence);
    // apply result to $state...
  }
}
```

## Data fetching: use `openapi-fetch` typed client

Use the typed `openapi-fetch` client from `$lib/client.js`, not raw `fetch`:

```typescript
// WRONG
const res = await fetch('/api/v1/tasks')
const data = await res.json()

// CORRECT
import { api } from "$lib/client.js";
const { data, response } = await api.GET("/api/v1/tasks", {
  params: { query: filters },
});
if (!response.ok) throw new Error(`${response.status}`);
```

For generated API types in components and stores:

```typescript
import type { components } from "$lib/api.js";
type TaskEvent = components["schemas"]["TaskEvent"];
```

## File organization

```
web/src/lib/
├── components/     Svelte components (.svelte)
├── *.svelte.ts     Reactive stores ($state classes)
├── *.ts            Pure logic, utilities, types
├── client.ts       openapi-fetch API client
└── api.d.ts        Generated types from OpenAPI spec (do not edit)
```

## Error/redirect: no `throw` needed (SvelteKit 2)

```typescript
// WRONG
throw error(404, 'Not found')
throw redirect(302, '/login')

// CORRECT
error(404, 'Not found')
redirect(302, '/login')
```
