<script lang="ts">
import { page } from "$app/state";
import Breadcrumb from "$lib/components/Breadcrumb.svelte";
import ConnectionIndicator from "$lib/components/ConnectionIndicator.svelte";
import ErrorCallout from "$lib/components/ErrorCallout.svelte";
import EventStream from "$lib/components/EventStream.svelte";
import PRCard from "$lib/components/PRCard.svelte";
import StatusBadge from "$lib/components/StatusBadge.svelte";
import {
	extractRepoName,
	formatDuration,
	formatRelativeTime,
} from "$lib/format.js";
import { LiveTick } from "$lib/live-tick.svelte.js";
import { TaskDetailStore } from "$lib/task-detail.svelte.js";
import { TaskStream } from "$lib/task-stream.svelte.js";

const taskID = $derived(page.params.taskID ?? "");
const detail = new TaskDetailStore();
const stream = new TaskStream();

const task = $derived(detail.task);
const phase = $derived(task?.status.phase ?? "");
const isRunning = $derived(phase === "Running");
const isPending = $derived(phase === "Pending");
const isSucceeded = $derived(phase === "Succeeded");
const isFailed = $derived(phase === "Failed" || phase === "TimedOut");

// Live duration tick for running tasks
const tick = new LiveTick(() => isRunning);

const duration = $derived.by(() => {
	if (!task) return "";
	if (isRunning) void tick.now;
	return formatDuration(task.createdAt, task.completionTime);
});

// Load task metadata on mount / taskID change
$effect(() => {
	if (!taskID) return;
	detail.load(taskID);
});

// Connect WebSocket for running tasks, disconnect on cleanup
$effect(() => {
	if (isRunning && taskID) {
		stream.connect(taskID);
		return () => stream.disconnect();
	}
});

// Refetch task when stream completes (task_complete message received)
$effect(() => {
	if (stream.streamPhase === "completed" && taskID) {
		detail.load(taskID);
	}
});

// Find last tool_call for failed tasks
const lastToolAction = $derived.by(() => {
	const toolCalls = stream.events.filter((e) => e.type === "tool_call");
	return toolCalls.length > 0
		? toolCalls[toolCalls.length - 1].summary
		: undefined;
});
</script>

<svelte:head><title>{task?.task.description ?? "Task"} - Shepherd</title></svelte:head>

<main class="mx-auto max-w-[1200px] px-4 py-6">
	<div class="mb-4">
		<Breadcrumb {taskID} />
	</div>

	{#if detail.loading && !task}
		<div class="py-12 text-center text-fg-muted">Loading task...</div>
	{:else if detail.error}
		<div
			class="rounded-md border border-danger-fg/30 bg-danger-fg/5 px-4 py-3 text-sm text-danger-fg"
		>
			{detail.error}
		</div>
	{:else if task}
		<!-- Header: status + metadata -->
		<div class="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
			<div class="min-w-0 flex-1">
				<div class="flex items-center gap-3">
					<StatusBadge status={phase} />
					{#if isRunning}
						<ConnectionIndicator state={stream.connectionState} />
					{/if}
				</div>
				<h1 class="mt-2 text-lg font-medium text-fg-default">
					{task.task.description}
				</h1>
				<div class="mt-1 flex items-center gap-3 text-sm text-fg-muted">
					<span class="font-mono text-accent-fg">
						{extractRepoName(task.repo.url)}
					</span>
					<span class="text-fg-dim">·</span>
					<span class="font-mono">{duration}</span>
					<span class="text-fg-dim">·</span>
					<span>{formatRelativeTime(task.createdAt)}</span>
				</div>
			</div>
		</div>

		<!-- Terminal state banners -->
		{#if isSucceeded && task.status.prURL}
			<div class="mb-6">
				<PRCard prURL={task.status.prURL} repoURL={task.repo.url} />
			</div>
		{/if}

		{#if isFailed && task.status.error}
			<div class="mb-6">
				<ErrorCallout error={task.status.error} lastAction={lastToolAction} />
			</div>
		{/if}

		<!-- Pending state -->
		{#if isPending}
			<div
				class="rounded-md border border-border-muted bg-canvas-subtle px-4 py-8 text-center"
			>
				<div class="text-fg-muted">Waiting for sandbox...</div>
				<div class="mt-1 text-xs text-fg-dim">
					The task will start once a runner picks it up.
				</div>
			</div>
		{/if}

		<!-- Event stream -->
		{#if isRunning || stream.events.length > 0}
			<div class="mt-2">
				<div class="mb-2 flex items-center justify-between">
					<h2 class="text-sm font-medium text-fg-muted">
						Events ({stream.events.length})
					</h2>
				</div>
				<EventStream events={stream.events} isLive={isRunning} />
			</div>
		{/if}

		<!-- Status message -->
		{#if task.status.message && !isFailed}
			<div class="mt-4 text-sm text-fg-muted">
				{task.status.message}
			</div>
		{/if}
	{/if}
</main>
