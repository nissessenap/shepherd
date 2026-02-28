<script lang="ts">
import type { components } from "$lib/api.js";
import ErrorEvent from "./ErrorEvent.svelte";
import ThinkingEvent from "./ThinkingEvent.svelte";
import ToolCallEvent from "./ToolCallEvent.svelte";
import ToolResultEvent from "./ToolResultEvent.svelte";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	event: TaskEvent;
}

const { event }: Props = $props();
</script>

{#if event.type === "thinking"}
	<ThinkingEvent {event} />
{:else if event.type === "tool_call"}
	<ToolCallEvent {event} />
{:else if event.type === "tool_result"}
	<ToolResultEvent {event} />
{:else if event.type === "error"}
	<ErrorEvent {event} />
{/if}
