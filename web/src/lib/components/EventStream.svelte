<script lang="ts">
import type { components } from "$lib/api.js";
import EventItem from "./EventItem.svelte";

type TaskEvent = components["schemas"]["TaskEvent"];

interface Props {
	events: TaskEvent[];
	isLive: boolean;
}

const { events, isLive }: Props = $props();

let container: HTMLDivElement | undefined = $state(undefined);
let userScrolledUp = $state(false);
let showAllEvents = $state(false);

const COLLAPSED_HEAD = 3;
const COLLAPSED_TAIL = 5;

const shouldCollapse = $derived(
	!isLive &&
		!showAllEvents &&
		events.length > COLLAPSED_HEAD + COLLAPSED_TAIL + 2,
);

const visibleEvents = $derived.by(() => {
	if (!shouldCollapse) return events;
	return [...events.slice(0, COLLAPSED_HEAD), ...events.slice(-COLLAPSED_TAIL)];
});

const hiddenCount = $derived(
	shouldCollapse ? events.length - COLLAPSED_HEAD - COLLAPSED_TAIL : 0,
);

function onScroll() {
	if (!container) return;
	const { scrollTop, scrollHeight, clientHeight } = container;
	// "Scrolled up" = not within 50px of bottom
	userScrolledUp = scrollHeight - scrollTop - clientHeight > 50;
}

// Auto-scroll to bottom when new events arrive (if user hasn't scrolled up)
$effect(() => {
	// Track events.length to trigger on new events
	void events.length;
	if (!container || userScrolledUp) return;
	// Use requestAnimationFrame to scroll after DOM update
	requestAnimationFrame(() => {
		if (container) {
			container.scrollTop = container.scrollHeight;
		}
	});
});

function scrollToBottom() {
	if (container) {
		container.scrollTop = container.scrollHeight;
		userScrolledUp = false;
	}
}
</script>

<div
	class="relative flex flex-col overflow-hidden rounded-md border border-border-muted"
>
	<div
		bind:this={container}
		onscroll={onScroll}
		class="flex-1 overflow-y-auto bg-canvas-inset p-3"
		style="max-height: 600px;"
		role="log"
		aria-live="polite"
	>
		{#if events.length === 0}
			<div class="py-8 text-center text-sm text-fg-dim">
				{isLive ? "Waiting for events..." : "No events recorded."}
			</div>
		{:else}
			<div class="flex flex-col gap-0.5">
				{#each visibleEvents as event, i (event.sequence)}
					{#if shouldCollapse && i === COLLAPSED_HEAD}
						<button
							class="my-2 text-center text-xs text-accent-fg hover:underline"
							onclick={() => { showAllEvents = true; }}
						>
							Show {hiddenCount} hidden events
						</button>
					{/if}
					<EventItem {event} />
				{/each}
			</div>
		{/if}
	</div>

	{#if isLive && userScrolledUp}
		<button
			class="absolute bottom-3 left-1/2 -translate-x-1/2 rounded-full bg-accent-fg px-3 py-1 text-xs font-medium text-canvas-default shadow-lg"
			onclick={scrollToBottom}
		>
			New events ↓
		</button>
	{/if}
</div>
