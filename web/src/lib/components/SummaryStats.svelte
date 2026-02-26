<script lang="ts">
import type { components } from "$lib/api.js";

type TaskResponse = components["schemas"]["TaskResponse"];

interface Props {
	tasks: TaskResponse[];
}

const { tasks }: Props = $props();

const stats = $derived.by(() => {
	let active = 0;
	let pending = 0;
	let succeeded = 0;
	let failed = 0;

	for (const task of tasks) {
		const phase = task.status.phase;
		if (phase === "Running") active++;
		else if (phase === "Pending") pending++;
		else if (phase === "Succeeded") succeeded++;
		else if (phase === "Failed" || phase === "TimedOut") failed++;
	}

	return { active, pending, succeeded, failed, total: tasks.length };
});
</script>

<div class="flex gap-3 text-sm">
	<div class="rounded-md border border-border-muted bg-canvas-subtle px-3 py-1.5">
		<span class="text-fg-muted">Total</span>
		<span class="ml-1 font-mono font-medium text-fg-default">{stats.total}</span>
	</div>
	{#if stats.active > 0}
		<div class="rounded-md border border-border-muted bg-canvas-subtle px-3 py-1.5">
			<span class="text-info-fg">Active</span>
			<span class="ml-1 font-mono font-medium text-info-fg">{stats.active}</span>
		</div>
	{/if}
	{#if stats.pending > 0}
		<div class="rounded-md border border-border-muted bg-canvas-subtle px-3 py-1.5">
			<span class="text-attention-fg">Pending</span>
			<span class="ml-1 font-mono font-medium text-attention-fg">{stats.pending}</span>
		</div>
	{/if}
	{#if stats.succeeded > 0}
		<div class="rounded-md border border-border-muted bg-canvas-subtle px-3 py-1.5">
			<span class="text-success-fg">Succeeded</span>
			<span class="ml-1 font-mono font-medium text-success-fg">{stats.succeeded}</span>
		</div>
	{/if}
	{#if stats.failed > 0}
		<div class="rounded-md border border-border-muted bg-canvas-subtle px-3 py-1.5">
			<span class="text-danger-fg">Failed</span>
			<span class="ml-1 font-mono font-medium text-danger-fg">{stats.failed}</span>
		</div>
	{/if}
</div>
