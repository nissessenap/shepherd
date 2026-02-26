---
paths:
  - "web/**/*.svelte"
  - "web/**/*.svelte.ts"
  - "web/**/*.svelte.js"
---

# Svelte 5 Runes — Mandatory Syntax

This project uses Svelte 5 with runes mode. NEVER use Svelte 4 syntax.

## State: `$state`, NEVER plain `let`

```svelte
<!-- WRONG -->
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
  items.push('c') // works, no dummy reassignment needed
</script>
```

Use `$state.raw()` for large immutable data you only ever reassign (never mutate).

## Derived values: `$derived`, NEVER `$:`

```svelte
<!-- WRONG -->
$: doubled = count * 2

<!-- CORRECT -->
const doubled = $derived(count * 2)

<!-- Multi-line -->
const total = $derived.by(() => {
  let sum = 0
  for (const n of numbers) sum += n
  return sum
})
```

`$derived` must be pure — NEVER mutate `$state` inside it.

## Side effects: `$effect`, NEVER `$:` for side effects

```svelte
<!-- WRONG -->
$: console.log(count)
$: document.title = `Count: ${count}`

<!-- CORRECT -->
$effect(() => {
  console.log(count)
})

$effect(() => {
  document.title = `Count: ${count}`
})
```

Return a cleanup function when needed:

```svelte
$effect(() => {
  const interval = setInterval(() => tick(), 1000)
  return () => clearInterval(interval)
})
```

NEVER synchronize state in `$effect` — use `$derived` instead:

```svelte
<!-- WRONG -->
let doubled = $state(0)
$effect(() => { doubled = count * 2 })

<!-- CORRECT -->
const doubled = $derived(count * 2)
```

## Props: `$props()`, NEVER `export let`

```svelte
<!-- WRONG -->
<script lang="ts">
  export let name: string
  export let count = 0
</script>

<!-- CORRECT -->
<script lang="ts">
  interface Props {
    name: string
    count?: number
  }
  let { name, count = 0 }: Props = $props()
</script>
```

Rest props (replaces `$$restProps`):

```svelte
let { value, ...rest } = $props()
```

## Bindable: explicit `$bindable()`

Props are NOT bindable by default in Svelte 5:

```svelte
<script lang="ts">
  let { value = $bindable('') }: { value: string } = $props()
</script>
<input bind:value />
```

## Events: `onclick`, NEVER `on:click`

```svelte
<!-- WRONG -->
<button on:click={handler}>Click</button>
<button on:click|preventDefault={handler}>Submit</button>

<!-- CORRECT -->
<button onclick={handler}>Click</button>
<button onclick={(e) => { e.preventDefault(); handler(e) }}>Submit</button>
```

Event modifiers are removed. Handle manually:

```svelte
<!-- |stopPropagation -->
onclick={(e) => { e.stopPropagation(); handler(e) }}

<!-- |once -->
use a wrapper: function once(fn) { return function(e) { if (fn) fn(e); fn = null } }

<!-- |capture -->
onclickcapture={handler}
```

## Component events: callback props, NEVER `createEventDispatcher`

```svelte
<!-- WRONG -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte'
  const dispatch = createEventDispatcher()
  dispatch('submit', data)
</script>

<!-- CORRECT -->
<script lang="ts">
  let { onsubmit }: { onsubmit?: (data: FormData) => void } = $props()
  onsubmit?.(data)
</script>
```

## Content: snippets, NEVER slots

```svelte
<!-- WRONG -->
<div class="card"><slot /></div>

<!-- CORRECT -->
<script lang="ts">
  import type { Snippet } from 'svelte'
  let { children }: { children: Snippet } = $props()
</script>
<div class="card">{@render children()}</div>
```

Named snippets replace named slots:

```svelte
<!-- Component -->
<script lang="ts">
  import type { Snippet } from 'svelte'
  let { header, children }: { header?: Snippet; children: Snippet } = $props()
</script>
<header>{@render header?.()}</header>
<main>{@render children()}</main>

<!-- Usage -->
<Layout>
  {#snippet header()}<h1>Title</h1>{/snippet}
  <p>Main content</p>
</Layout>
```

Snippets with parameters (replaces slot props / `let:` directive):

```svelte
<!-- Component -->
<script lang="ts">
  import type { Snippet } from 'svelte'
  let { items, row }: { items: Item[]; row: Snippet<[Item]> } = $props()
</script>
{#each items as item}
  {@render row(item)}
{/each}

<!-- Usage -->
<List {items}>
  {#snippet row(item)}<span>{item.name}</span>{/snippet}
</List>
```

## Shared state: `$state` in `.svelte.ts`, NEVER writable/readable stores

```typescript
// WRONG
import { writable } from 'svelte/store'
export const count = writable(0)

// CORRECT — use .svelte.ts file extension
// counter.svelte.ts
export class CounterStore {
  count = $state(0)
  increment = () => { this.count++ }
}
```

Export objects or classes, not primitives (primitives can't be tracked across imports):

```typescript
// WRONG: primitive export
export let count = $state(0)

// CORRECT: object export
export const counter = $state({ count: 0 })
```

Use `setContext`/`getContext` for SSR-safe shared state.

## Reactive subscriptions without value usage: `void expr`

Use `void expr` to subscribe to a `$state` value for re-computation without using its value directly. This is idiomatic Svelte 5 — do NOT remove `void` reads, they are intentional reactive dependencies.

```svelte
<script lang="ts">
  let now = $state(Date.now())
  const duration = $derived.by(() => {
    if (isRunning) void now  // re-evaluate every tick
    return formatDuration(start, end)
  })
</script>
```

## `$derived` vs `$derived.by`

- `$derived(expr)` — single expression, no braces needed: `const doubled = $derived(count * 2)`
- `$derived.by(() => { ... })` — multi-statement, local variables, conditionals, loops

Use the simpler form when possible.
