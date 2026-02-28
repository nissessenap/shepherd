<script lang="ts">
import { getStatusConfig } from "$lib/filters.js";

interface Props {
	status: string;
}

const { status }: Props = $props();

const config = $derived(getStatusConfig(status));
</script>

<span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium {config.color}">
	{config.label}
</span>
