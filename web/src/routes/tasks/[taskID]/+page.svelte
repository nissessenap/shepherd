<script lang="ts">
import { page } from "$app/state";

// Task detail page — placeholder for Phase 5
const taskID = $derived(page.params.taskID);
</script>

<main class="mx-auto max-w-[1200px] px-4 py-8">
	<h1 class="text-2xl font-bold text-fg-default">Task {taskID}</h1>
	<p class="mt-2 text-fg-muted">Task detail will be implemented in Phase 5.</p>
</main>
