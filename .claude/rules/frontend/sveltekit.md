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

Use `$state` classes in `.svelte.ts` files for data fetching:

```typescript
// lib/stores/tasks.svelte.ts
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

## WebSocket: `$state` class with `$effect` cleanup

```typescript
// lib/stores/task-stream.svelte.ts
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
  import { TaskStream } from '$lib/stores/task-stream.svelte'

  const stream = new TaskStream()
  setContext('task-stream', stream)

  $effect(() => {
    stream.connect('wss://...')
    return () => stream.disconnect()
  })
</script>
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
