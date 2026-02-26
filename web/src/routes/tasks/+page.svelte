<script lang="ts">
import { page } from "$app/state";
import FilterBar from "$lib/components/FilterBar.svelte";
import SummaryStats from "$lib/components/SummaryStats.svelte";
import TaskCard from "$lib/components/TaskCard.svelte";
import { type TaskFilters, TasksStore } from "$lib/tasks.svelte.js";

const store = new TasksStore();

const filters = $derived.by((): TaskFilters => {
	const params = page.url.searchParams;
	const f: TaskFilters = {};
	const repo = params.get("repo");
	const fleet = params.get("fleet");
	const active = params.get("active");
	if (repo) f.repo = repo;
	if (fleet) f.fleet = fleet;
	f.active = active === "true" || active === null ? "true" : undefined;
	return f;
});

const searchQuery = $derived(
	page.url.searchParams.get("q")?.toLowerCase() ?? "",
);

const filteredTasks = $derived.by(() => {
	if (!searchQuery) return store.data;
	return store.data.filter(
		(task) =>
			task.task.description.toLowerCase().includes(searchQuery) ||
			task.repo.url.toLowerCase().includes(searchQuery) ||
			task.id.toLowerCase().includes(searchQuery),
	);
});

// Fetch on mount and when filters change
$effect(() => {
	store.load(filters);
});

// 30-second background poll
$effect(() => {
	const interval = setInterval(() => {
		store.load(filters);
	}, 30_000);
	return () => clearInterval(interval);
});
</script>

<main class="mx-auto max-w-[1200px] px-4 py-6">
	<div class="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
		<h1 class="text-xl font-bold text-fg-default">Tasks</h1>
		<SummaryStats tasks={store.data} />
	</div>

	<div class="mb-4">
		<FilterBar tasks={store.data} />
	</div>

	{#if store.loading && store.data.length === 0}
		<div class="py-12 text-center text-fg-muted">Loading tasks...</div>
	{:else if store.error}
		<div class="rounded-md border border-danger-fg/30 bg-danger-fg/5 px-4 py-3 text-sm text-danger-fg">
			{store.error}
		</div>
	{:else if filteredTasks.length === 0}
		<div class="py-12 text-center text-fg-muted">
			{#if searchQuery || page.url.searchParams.has("repo")}
				No tasks match your filters.
			{:else}
				No tasks yet.
			{/if}
		</div>
	{:else}
		<div class="flex flex-col gap-2">
			{#each filteredTasks as task (task.id)}
				<TaskCard {task} />
			{/each}
		</div>
	{/if}
</main>
