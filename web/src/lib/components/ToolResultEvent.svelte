<script lang="ts">
import type { components } from "$lib/api.js";
import { formatTimestamp } from "$lib/format.js";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	event: TaskEvent;
}

const { event }: Props = $props();

let expanded = $state(false);

const success = $derived(event.output?.success ?? false);
const resultSummary = $derived(event.output?.summary ?? event.summary);
const isLong = $derived(resultSummary.length > 120);

const displayText = $derived(
	isLong && !expanded ? `${resultSummary.slice(0, 120)}...` : resultSummary,
);
</script>

<div class="flex gap-3 py-1 pl-6">
	<div class="flex w-10 shrink-0 items-start justify-end text-xs text-fg-dim font-mono">
		{formatTimestamp(event.timestamp)}
	</div>
	<div class="min-w-0 flex-1">
		<div class="flex items-start gap-1.5">
			<span class="mt-0.5 text-xs {success ? 'text-success-fg' : 'text-danger-fg'}">
				{success ? "✓" : "✗"}
			</span>
			<pre class="whitespace-pre-wrap break-words text-xs text-fg-muted">{displayText}</pre>
		</div>
		{#if isLong}
			<button
				class="mt-0.5 text-xs text-accent-fg hover:underline"
				onclick={() => { expanded = !expanded; }}
			>
				{expanded ? "Collapse" : "Expand"}
			</button>
		{/if}
	</div>
</div>
