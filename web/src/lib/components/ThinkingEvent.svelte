<script lang="ts">
import type { components } from "$lib/api.js";
import { formatTimestamp } from "$lib/format.js";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	event: TaskEvent;
}

const { event }: Props = $props();

const isLong = $derived(event.summary.length > 200);
let expanded = $state(false);

const displayText = $derived(
	isLong && !expanded ? `${event.summary.slice(0, 200)}...` : event.summary,
);
</script>

<div class="flex gap-3 py-1.5">
	<div class="flex w-16 shrink-0 items-start justify-end text-xs text-fg-dim font-mono">
		{formatTimestamp(event.timestamp)}
	</div>
	<div class="flex items-start gap-2 text-fg-muted">
		<span class="mt-0.5 text-xs">💭</span>
		<div class="min-w-0">
			<p class="text-sm text-fg-dim italic whitespace-pre-wrap">{displayText}</p>
			{#if isLong}
				<button
					class="mt-0.5 text-xs text-accent-fg hover:underline"
					onclick={() => { expanded = !expanded; }}
				>
					{expanded ? "Show less" : "Show more"}
				</button>
			{/if}
		</div>
	</div>
</div>
