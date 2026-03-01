/**
 * Reactive timestamp that updates every second while the condition is true.
 * Use inside $derived.by to force periodic recomputation for live durations.
 */
export class LiveTick {
	now = $state(Date.now());

	constructor(shouldTick: () => boolean) {
		$effect(() => {
			if (!shouldTick()) return;
			const interval = setInterval(() => {
				this.now = Date.now();
			}, 1000);
			return () => clearInterval(interval);
		});
	}
}
