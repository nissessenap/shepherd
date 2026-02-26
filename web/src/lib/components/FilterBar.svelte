<script lang="ts">
import { goto } from "$app/navigation";
import { page } from "$app/state";
import type { components } from "$lib/api.js";

type TaskResponse = components["schemas"]["TaskResponse"];

interface Props {
	tasks: TaskResponse[];
}

const { tasks }: Props = $props();

const activeFilter = $derived(page.url.searchParams.get("active") ?? "true");
const repoFilter = $derived(page.url.searchParams.get("repo") ?? "");
const searchFilter = $derived(page.url.searchParams.get("q") ?? "");

let searchInput = $state("");
let debounceTimer: ReturnType<typeof setTimeout> | undefined =
	$state(undefined);

// Sync search input with URL when it changes externally (e.g., back button)
$effect(() => {
	searchInput = searchFilter;
});

const repos = $derived.by(() => {
	const set = new Set<string>();
	for (const task of tasks) {
		if (task.repo.url) set.add(task.repo.url);
	}
	return [...set].sort();
});

function updateFilter(key: string, value: string) {
	const params = new URLSearchParams(page.url.searchParams);
	if (value) {
		params.set(key, value);
	} else {
		params.delete(key);
	}
	goto(`?${params.toString()}`, { replaceState: true, keepFocus: true });
}

function onSearchInput(e: Event) {
	const value = (e.target as HTMLInputElement).value;
	searchInput = value;
	clearTimeout(debounceTimer);
	debounceTimer = setTimeout(() => {
		updateFilter("q", value);
	}, 300);
}

function onActiveToggle(value: string) {
	updateFilter("active", value);
}

function onRepoChange(e: Event) {
	updateFilter("repo", (e.target as HTMLSelectElement).value);
}
</script>

<div class="flex flex-wrap items-center gap-3">
	<div class="flex rounded-md border border-border-default text-sm">
		<button
			class="px-3 py-1.5 {activeFilter === 'true'
				? 'bg-canvas-subtle text-fg-default'
				: 'text-fg-muted hover:text-fg-default'}"
			onclick={() => onActiveToggle("true")}
		>
			Active
		</button>
		<button
			class="border-l border-border-default px-3 py-1.5 {activeFilter !== 'true'
				? 'bg-canvas-subtle text-fg-default'
				: 'text-fg-muted hover:text-fg-default'}"
			onclick={() => onActiveToggle("false")}
		>
			All
		</button>
	</div>

	{#if repos.length > 1}
		<select
			class="rounded-md border border-border-default bg-canvas-default px-3 py-1.5 text-sm text-fg-default"
			onchange={onRepoChange}
			value={repoFilter}
		>
			<option value="">All repos</option>
			{#each repos as repo}
				<option value={repo}>{repo.replace(/^https:\/\/github\.com\//, "")}</option>
			{/each}
		</select>
	{/if}

	<input
		type="text"
		placeholder="Search tasks..."
		class="rounded-md border border-border-default bg-canvas-default px-3 py-1.5 text-sm text-fg-default placeholder:text-fg-dim focus:border-accent-fg focus:outline-none"
		value={searchInput}
		oninput={onSearchInput}
	/>
</div>
