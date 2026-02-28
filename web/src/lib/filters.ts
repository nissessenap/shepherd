import type { components } from "./api.js";
import type { TaskFilters } from "./tasks.svelte.js";

type TaskResponse = components["schemas"]["TaskResponse"];

/**
 * Build API query filters from URL search params.
 * Defaults to active-only when no explicit active param is set.
 */
export function buildFilters(searchParams: URLSearchParams): TaskFilters {
	const f: TaskFilters = {};
	const repo = searchParams.get("repo");
	const fleet = searchParams.get("fleet");
	const active = searchParams.get("active");
	if (repo) f.repo = repo;
	if (fleet) f.fleet = fleet;
	f.active = active === "true" || active === null ? "true" : undefined;
	return f;
}

/**
 * Convert a repo URL or path to a Kubernetes label-compatible value.
 * Strips URL scheme/host, removes trailing .git, and replaces slashes with dashes.
 *
 * Examples:
 *   "https://github.com/org/repo"     → "org-repo"
 *   "https://github.com/org/repo.git" → "org-repo"
 *   "org/repo"                        → "org-repo"
 *   "org-repo"                        → "org-repo"
 */
export function repoUrlToLabel(url: string): string {
	let value: string;
	try {
		const parsed = new URL(url);
		value = parsed.pathname.replace(/^\//, "").replace(/\.git$/, "");
	} catch {
		value = url;
	}
	return value.replace(/\//g, "-");
}

/**
 * Client-side search filter over tasks by description, repo URL, or ID.
 */
export function filterTasks(
	tasks: TaskResponse[],
	query: string,
): TaskResponse[] {
	if (!query) return tasks;
	const q = query.toLowerCase();
	return tasks.filter(
		(task) =>
			task.task.description.toLowerCase().includes(q) ||
			task.repo.url.toLowerCase().includes(q) ||
			task.id.toLowerCase().includes(q),
	);
}

export interface StatusConfig {
	color: string;
	label: string;
}

/**
 * Map a task phase string to display color and label.
 */
export function getStatusConfig(status: string): StatusConfig {
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
}

export interface TaskStats {
	active: number;
	pending: number;
	succeeded: number;
	failed: number;
	total: number;
}

/**
 * Compute summary statistics from a list of tasks.
 */
export function computeStats(tasks: TaskResponse[]): TaskStats {
	let active = 0;
	let pending = 0;
	let succeeded = 0;
	let failed = 0;

	for (const task of tasks) {
		const phase = task.status.phase;
		if (phase === "Running") active++;
		else if (phase === "Pending") pending++;
		else if (phase === "Succeeded") succeeded++;
		else if (phase === "Failed" || phase === "TimedOut") failed++;
	}

	return { active, pending, succeeded, failed, total: tasks.length };
}
