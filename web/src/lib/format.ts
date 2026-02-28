/**
 * Format a duration between two timestamps as a human-readable string.
 * If endTime is omitted, uses the current time (for active tasks).
 */
export function formatDuration(
	startTime: string,
	endTime?: string | null,
): string {
	const start = new Date(startTime).getTime();
	const end = endTime ? new Date(endTime).getTime() : Date.now();
	const diffMs = Math.max(0, end - start);
	const totalSeconds = Math.floor(diffMs / 1000);

	if (totalSeconds < 60) {
		return `${totalSeconds}s`;
	}

	const hours = Math.floor(totalSeconds / 3600);
	const minutes = Math.floor((totalSeconds % 3600) / 60);
	const seconds = totalSeconds % 60;

	if (hours > 0) {
		return `${hours}h ${String(minutes).padStart(2, "0")}m`;
	}

	return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
}

/**
 * Format a timestamp as a relative time string (e.g. "2 minutes ago").
 */
export function formatRelativeTime(timestamp: string): string {
	const now = Date.now();
	const then = new Date(timestamp).getTime();
	const diffMs = now - then;

	if (diffMs < 0) return "just now";

	const seconds = Math.floor(diffMs / 1000);
	if (seconds < 60) return "just now";

	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;

	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;

	const days = Math.floor(hours / 24);
	return `${days}d ago`;
}

/**
 * Format a timestamp as a time string (e.g. "12:30:05").
 */
export function formatTimestamp(timestamp: string): string {
	const date = new Date(timestamp);
	return date.toLocaleTimeString("en-GB", {
		hour: "2-digit",
		minute: "2-digit",
		second: "2-digit",
		hour12: false,
	});
}

/**
 * Extract a short repo name from a full GitHub URL.
 * e.g. "https://github.com/org/repo" → "org/repo"
 */
export function extractRepoName(url: string): string {
	try {
		const parsed = new URL(url);
		// Remove leading slash and trailing .git
		return parsed.pathname.slice(1).replace(/\.git$/, "");
	} catch {
		return url;
	}
}
