<script lang="ts">
import type { components } from "$lib/api.js";
import {
	extractRepoName,
	formatDuration,
	formatRelativeTime,
} from "$lib/format.js";
import { LiveTick } from "$lib/live-tick.svelte.js";
import StatusBadge from "./StatusBadge.svelte";

type TaskResponse = components["schemas"]["TaskResponse"];

interface Props {
	task: TaskResponse;
}

const { task }: Props = $props();

const repoName = $derived(extractRepoName(task.repo.url));
const isActive = $derived(
	task.status.phase === "Running" || task.status.phase === "Pending",
);

const tick = new LiveTick(() => isActive);

const duration = $derived.by(() => {
	// Force reactivity on tick.now for active tasks
	if (isActive) void tick.now;
	return formatDuration(task.createdAt, task.completionTime);
});

const description = $derived(
	task.task.description.length > 120
		? `${task.task.description.slice(0, 120)}...`
		: task.task.description,
);
</script>

<a
	href="/tasks/{task.id}"
	class="flex items-center gap-4 rounded-md border border-border-muted bg-canvas-subtle px-4 py-3 transition-colors hover:border-border-default hover:bg-canvas-default"
>
	<div class="w-24 shrink-0">
		<StatusBadge status={task.status.phase} />
	</div>

	<div class="min-w-0 flex-1">
		<div class="flex items-center gap-2">
			<span class="font-mono text-sm text-accent-fg">{repoName}</span>
			<span class="text-fg-dim">-</span>
			<span class="truncate text-sm text-fg-default">{description}</span>
		</div>
		{#if task.status.message}
			<p class="mt-0.5 truncate text-xs text-fg-muted">{task.status.message}</p>
		{/if}
	</div>

	<div class="shrink-0 text-right text-xs text-fg-muted">
		<div class="font-mono">{duration}</div>
		<div>{formatRelativeTime(task.createdAt)}</div>
	</div>
</a>
