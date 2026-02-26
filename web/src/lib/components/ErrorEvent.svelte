<script lang="ts">
import type { components } from "$lib/api.js";
import { formatTimestamp } from "$lib/format.js";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	event: TaskEvent;
}

const { event }: Props = $props();
</script>

<div class="flex gap-3 rounded-md border border-danger-fg/20 bg-danger-fg/5 px-3 py-2">
	<div class="flex w-16 shrink-0 items-start justify-end text-xs text-fg-dim font-mono">
		{formatTimestamp(event.timestamp)}
	</div>
	<div class="min-w-0 flex-1">
		<div class="text-xs font-medium text-danger-fg">Error</div>
		<pre class="mt-1 whitespace-pre-wrap break-words text-xs text-fg-default">{event.summary}</pre>
	</div>
</div>
