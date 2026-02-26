<script lang="ts">
import "../app.css";
import favicon from "$lib/assets/favicon.svg";
import Header from "$lib/components/Header.svelte";

let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

<div class="min-h-screen bg-canvas-default text-fg-default font-sans">
	<Header />
	{@render children()}
</div>
