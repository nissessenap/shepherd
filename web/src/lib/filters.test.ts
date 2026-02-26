import { describe, expect, it } from "vitest";
import type { components } from "./api.js";
import {
	buildFilters,
	computeStats,
	filterTasks,
	getStatusConfig,
} from "./filters.js";

type TaskResponse = components["schemas"]["TaskResponse"];

/** Create a minimal TaskResponse with sensible defaults and override support. */
function makeTask(overrides: Partial<TaskResponse> = {}): TaskResponse {
	return {
		id: "task-001",
		namespace: "default",
		repo: { url: "https://github.com/org/repo" },
		task: { description: "Fix the widget" },
		callbackURL: "https://example.com/callback",
		status: { phase: "Pending", message: "" },
		createdAt: "2026-01-15T10:00:00Z",
		...overrides,
	};
}

// ---------------------------------------------------------------------------
// buildFilters
// ---------------------------------------------------------------------------
describe("buildFilters", () => {
	it("defaults to active=true when no params are set", () => {
		const params = new URLSearchParams();
		expect(buildFilters(params)).toEqual({ active: "true" });
	});

	it("includes repo and fleet along with active default", () => {
		const params = new URLSearchParams("repo=foo&fleet=bar");
		expect(buildFilters(params)).toEqual({
			repo: "foo",
			fleet: "bar",
			active: "true",
		});
	});

	it("explicitly sets active=true", () => {
		const params = new URLSearchParams("active=true");
		expect(buildFilters(params)).toEqual({ active: "true" });
	});

	it("yields active=undefined when active=false (show all tasks)", () => {
		const params = new URLSearchParams("active=false");
		expect(buildFilters(params)).toEqual({ active: undefined });
	});

	it("yields active=undefined for garbage active value", () => {
		const params = new URLSearchParams("active=garbage");
		expect(buildFilters(params)).toEqual({ active: undefined });
	});

	it("does not include repo key when repo is empty string", () => {
		const params = new URLSearchParams("repo=");
		const result = buildFilters(params);
		expect(result).not.toHaveProperty("repo");
	});

	it("does not include fleet key when fleet is empty string", () => {
		const params = new URLSearchParams("fleet=");
		const result = buildFilters(params);
		expect(result).not.toHaveProperty("fleet");
	});

	it("defaults to active=true when active param is absent (null)", () => {
		// URLSearchParams.get returns null for absent keys
		const params = new URLSearchParams("repo=myrepo");
		expect(buildFilters(params).active).toBe("true");
	});
});

// ---------------------------------------------------------------------------
// filterTasks
// ---------------------------------------------------------------------------
describe("filterTasks", () => {
	const tasks: TaskResponse[] = [
		makeTask({
			id: "task-aaa",
			task: { description: "Refactor auth module" },
			repo: { url: "https://github.com/acme/backend" },
		}),
		makeTask({
			id: "task-bbb",
			task: { description: "Add login page" },
			repo: { url: "https://github.com/acme/frontend" },
		}),
		makeTask({
			id: "task-ccc",
			task: { description: "Update CI pipeline" },
			repo: { url: "https://github.com/acme/infra" },
		}),
	];

	it("returns all tasks when query is empty", () => {
		expect(filterTasks(tasks, "")).toEqual(tasks);
	});

	it("matches on task description", () => {
		const result = filterTasks(tasks, "auth");
		expect(result).toHaveLength(1);
		expect(result[0].id).toBe("task-aaa");
	});

	it("matches on repo URL", () => {
		const result = filterTasks(tasks, "frontend");
		expect(result).toHaveLength(1);
		expect(result[0].id).toBe("task-bbb");
	});

	it("matches on task ID", () => {
		const result = filterTasks(tasks, "task-ccc");
		expect(result).toHaveLength(1);
		expect(result[0].id).toBe("task-ccc");
	});

	it("performs case-insensitive matching", () => {
		const result = filterTasks(tasks, "REFACTOR");
		expect(result).toHaveLength(1);
		expect(result[0].id).toBe("task-aaa");
	});

	it("returns empty array when no matches", () => {
		expect(filterTasks(tasks, "nonexistent")).toEqual([]);
	});

	it("returns empty array when task list is empty", () => {
		expect(filterTasks([], "anything")).toEqual([]);
	});

	it("returns all matching tasks when query matches multiple", () => {
		const result = filterTasks(tasks, "acme");
		expect(result).toHaveLength(3);
	});
});

// ---------------------------------------------------------------------------
// getStatusConfig
// ---------------------------------------------------------------------------
describe("getStatusConfig", () => {
	it.each([
		["Pending", "Pending"],
		["Running", "Running"],
		["Succeeded", "Succeeded"],
		["Failed", "Failed"],
		["TimedOut", "Timed Out"],
		["Cancelled", "Cancelled"],
	])("maps %s to label %s", (status, expectedLabel) => {
		expect(getStatusConfig(status).label).toBe(expectedLabel);
	});

	it("returns 'Timed Out' with a space for TimedOut", () => {
		const config = getStatusConfig("TimedOut");
		expect(config.label).toBe("Timed Out");
	});

	it("returns the raw status as label for unknown statuses", () => {
		const config = getStatusConfig("SomeUnknown");
		expect(config.label).toBe("SomeUnknown");
		expect(config.color).toContain("muted");
	});

	it("handles empty string as unknown status", () => {
		const config = getStatusConfig("");
		expect(config.label).toBe("");
		expect(config.color).toContain("muted");
	});

	it("uses the same attention color for Pending and TimedOut", () => {
		const pending = getStatusConfig("Pending");
		const timedOut = getStatusConfig("TimedOut");
		expect(pending.color).toBe(timedOut.color);
	});

	it("assigns distinct colors to Running, Succeeded, and Failed", () => {
		const running = getStatusConfig("Running");
		const succeeded = getStatusConfig("Succeeded");
		const failed = getStatusConfig("Failed");
		const colors = new Set([running.color, succeeded.color, failed.color]);
		expect(colors.size).toBe(3);
	});
});

// ---------------------------------------------------------------------------
// computeStats
// ---------------------------------------------------------------------------
describe("computeStats", () => {
	it("returns all zeros for an empty array", () => {
		expect(computeStats([])).toEqual({
			active: 0,
			pending: 0,
			succeeded: 0,
			failed: 0,
			total: 0,
		});
	});

	it("counts mixed phases correctly", () => {
		const tasks: TaskResponse[] = [
			makeTask({ status: { phase: "Running", message: "" } }),
			makeTask({ status: { phase: "Running", message: "" } }),
			makeTask({ status: { phase: "Pending", message: "" } }),
			makeTask({ status: { phase: "Succeeded", message: "" } }),
			makeTask({ status: { phase: "Succeeded", message: "" } }),
			makeTask({ status: { phase: "Succeeded", message: "" } }),
			makeTask({ status: { phase: "Failed", message: "" } }),
		];

		expect(computeStats(tasks)).toEqual({
			active: 2,
			pending: 1,
			succeeded: 3,
			failed: 1,
			total: 7,
		});
	});

	it("counts TimedOut as failed", () => {
		const tasks: TaskResponse[] = [
			makeTask({ status: { phase: "TimedOut", message: "" } }),
			makeTask({ status: { phase: "Failed", message: "" } }),
		];

		const stats = computeStats(tasks);
		expect(stats.failed).toBe(2);
		expect(stats.total).toBe(2);
	});

	it("includes unknown phases in total but not in any category", () => {
		const tasks: TaskResponse[] = [
			makeTask({ status: { phase: "Running", message: "" } }),
			makeTask({ status: { phase: "SomethingElse", message: "" } }),
		];

		const stats = computeStats(tasks);
		expect(stats.active).toBe(1);
		expect(stats.pending).toBe(0);
		expect(stats.succeeded).toBe(0);
		expect(stats.failed).toBe(0);
		expect(stats.total).toBe(2);
	});
});
