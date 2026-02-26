<script lang="ts">
interface Props {
	status: string;
}

const { status }: Props = $props();

const config = $derived.by(() => {
	switch (status) {
		case "Pending":
			return {
				color: "text-attention-fg bg-attention-fg/10",
				label: "Pending",
			};
		case "Running":
			return { color: "text-info-fg bg-info-fg/10", label: "Running" };
		case "Succeeded":
			return { color: "text-success-fg bg-success-fg/10", label: "Succeeded" };
		case "Failed":
			return { color: "text-danger-fg bg-danger-fg/10", label: "Failed" };
		case "TimedOut":
			return {
				color: "text-attention-fg bg-attention-fg/10",
				label: "Timed Out",
			};
		case "Cancelled":
			return { color: "text-fg-muted bg-fg-muted/10", label: "Cancelled" };
		default:
			return { color: "text-fg-muted bg-fg-muted/10", label: status };
	}
});
</script>

<span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium {config.color}">
	{config.label}
</span>
