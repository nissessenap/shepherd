<script lang="ts">
import type { ConnectionState } from "$lib/ws.js";

interface Props {
	state: ConnectionState;
}

const { state }: Props = $props();

const config = $derived.by(() => {
	switch (state) {
		case "connected":
			return { color: "bg-success-fg", label: "LIVE", pulse: true };
		case "connecting":
			return { color: "bg-attention-fg", label: "Connecting...", pulse: true };
		case "reconnecting":
			return {
				color: "bg-attention-fg",
				label: "Reconnecting...",
				pulse: true,
			};
		case "disconnected":
			return { color: "bg-fg-dim", label: "Disconnected", pulse: false };
	}
});
</script>

<span class="inline-flex items-center gap-1.5 text-xs font-medium">
	<span class="relative flex h-2 w-2">
		{#if config.pulse}
			<span class="absolute inline-flex h-full w-full animate-ping rounded-full {config.color} opacity-75"></span>
		{/if}
		<span class="relative inline-flex h-2 w-2 rounded-full {config.color}"></span>
	</span>
	<span class="text-fg-muted">{config.label}</span>
</span>
