<script lang="ts">
import "../app.css";
import favicon from "$lib/assets/favicon.svg";

let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

<div class="min-h-screen bg-canvas-default text-fg-default font-sans">
	<header class="border-b border-border-default px-4 py-3">
		<div class="mx-auto flex max-w-[1200px] items-center gap-4">
			<a href="/tasks" class="font-mono text-sm font-bold tracking-wider text-fg-default">
				SHEPHERD
			</a>
			<nav class="flex gap-3 text-sm">
				<a href="/tasks" class="text-fg-muted hover:text-fg-default">Tasks</a>
			</nav>
		</div>
	</header>
	{@render children()}
</div>
