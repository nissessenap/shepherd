<script lang="ts">
interface Props {
	error: string;
	lastAction?: string;
}

const { error, lastAction }: Props = $props();

let copied = $state(false);

async function copyError() {
	await navigator.clipboard.writeText(error);
	copied = true;
	setTimeout(() => {
		copied = false;
	}, 2000);
}
</script>

<div class="rounded-lg border-l-4 border-danger-fg bg-danger-fg/5 p-4">
	<div class="flex items-start justify-between gap-3">
		<div class="min-w-0 flex-1">
			<div class="text-sm font-medium text-danger-fg">Task Failed</div>
			{#if lastAction}
				<div class="mt-1 text-xs text-fg-muted">Last action: {lastAction}</div>
			{/if}
			<pre class="mt-2 whitespace-pre-wrap break-words font-mono text-xs text-fg-default">{error}</pre>
		</div>
		<button
			onclick={copyError}
			class="shrink-0 rounded-md border border-border-default bg-canvas-subtle px-2 py-1 text-xs text-fg-muted hover:text-fg-default"
		>
			{copied ? "Copied" : "Copy"}
		</button>
	</div>
</div>
