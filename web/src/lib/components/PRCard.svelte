<script lang="ts">
import { extractRepoName } from "$lib/format.js";

interface Props {
	prURL: string;
	repoURL: string;
}

const { prURL, repoURL }: Props = $props();

const repoName = $derived(extractRepoName(repoURL));

const prNumber = $derived.by(() => {
	const match = prURL.match(/\/pull\/(\d+)/);
	return match ? `#${match[1]}` : "";
});
</script>

<div class="rounded-lg border border-success-fg/30 bg-success-fg/5 p-4">
	<div class="flex items-center gap-3">
		<div class="flex h-10 w-10 items-center justify-center rounded-full bg-success-fg/10">
			<svg class="h-5 w-5 text-success-fg" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
				<path stroke-linecap="round" stroke-linejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m9.386-3.04l4.5-4.5a4.5 4.5 0 00-6.364-6.364l-1.757 1.757" />
			</svg>
		</div>
		<div class="min-w-0 flex-1">
			<div class="text-sm font-medium text-fg-default">Pull Request {prNumber}</div>
			<div class="text-xs text-fg-muted">{repoName}</div>
		</div>
		<a
			href={prURL}
			target="_blank"
			rel="noopener noreferrer"
			class="rounded-md border border-border-default bg-canvas-subtle px-3 py-1.5 text-sm text-fg-default hover:border-border-default hover:bg-canvas-default"
		>
			Open in GitHub
		</a>
	</div>
</div>
