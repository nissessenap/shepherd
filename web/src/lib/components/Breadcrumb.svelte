<script lang="ts">
interface Props {
	taskID: string;
}

const { taskID }: Props = $props();
</script>

<nav class="flex items-center gap-1.5 text-sm">
	<a href="/tasks" class="text-fg-muted hover:text-fg-default">Tasks</a>
	<span class="text-fg-dim">/</span>
	<span class="font-mono text-fg-default">{taskID.slice(0, 8)}</span>
</nav>
