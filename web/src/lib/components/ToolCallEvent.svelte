<script lang="ts">
import type { components } from "$lib/api.js";
import { formatTimestamp } from "$lib/format.js";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	event: TaskEvent;
}

const { event }: Props = $props();

let expanded = $state(false);

const toolColor = $derived.by(() => {
	switch (event.tool) {
		case "Read":
		case "Edit":
		case "Write":
		case "Glob":
		case "Grep":
			return "text-accent-fg bg-accent-fg/10";
		case "Bash":
			return "text-attention-fg bg-attention-fg/10";
		case "WebSearch":
		case "WebFetch":
			return "text-success-fg bg-success-fg/10";
		default:
			return "text-fg-muted bg-fg-muted/10";
	}
});
</script>

<div class="flex gap-3 py-1.5">
	<div class="flex w-16 shrink-0 items-start justify-end text-xs text-fg-dim font-mono">
		{formatTimestamp(event.timestamp)}
	</div>
	<div class="min-w-0 flex-1">
		<div class="flex items-center gap-2">
			<span class="rounded px-1.5 py-0.5 text-xs font-mono font-medium {toolColor}">
				{event.tool ?? "unknown"}
			</span>
			<span class="truncate text-sm text-fg-default">{event.summary}</span>
		</div>
		{#if event.input && Object.keys(event.input).length > 0}
			<button
				class="mt-1 text-xs text-fg-dim hover:text-fg-muted"
				onclick={() => { expanded = !expanded; }}
			>
				{expanded ? "Hide details" : "Show details"}
			</button>
			{#if expanded}
				<pre class="mt-1 overflow-x-auto rounded-md bg-canvas-inset p-2 font-mono text-xs text-fg-muted">{JSON.stringify(event.input, null, 2)}</pre>
			{/if}
		{/if}
	</div>
</div>
