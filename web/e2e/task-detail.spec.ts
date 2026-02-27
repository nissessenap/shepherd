import { expect, test } from "@playwright/test";
import { createTask, waitForAPI, waitForTaskPhase } from "./helpers.ts";

test.beforeAll(async () => {
	await waitForAPI();
});

test("live events stream during running task", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E live stream test",
	});

	// Navigate immediately before stub runner finishes
	await page.goto(`/tasks/${taskId}`);

	// Wait for "LIVE" indicator to appear (stub runner must be running)
	await expect(page.getByText("LIVE")).toBeVisible({ timeout: 60_000 });

	// Wait for events to appear in the stream
	// The stub runner POSTs 8 events: thinking, tool_call, tool_result, etc.
	// Wait for at least one thinking event (italic text with brain emoji)
	await expect(page.getByText("Analyzing the codebase structure")).toBeVisible({
		timeout: 30_000,
	});

	// Verify at least one tool_call event (tool badge visible)
	await expect(page.getByText("Read")).toBeVisible();

	// Verify we see tool results
	await expect(page.getByText("Bash")).toBeVisible();
});

test("task completes and shows PR link", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E PR card test",
	});

	await page.goto(`/tasks/${taskId}`);

	// Wait for task to complete (stub runner takes ~4s for events + status)
	await expect(page.getByText("Succeeded")).toBeVisible({ timeout: 120_000 });

	// Verify PR card appears
	await expect(page.getByText("Pull Request #42")).toBeVisible();

	// Verify "Open in GitHub" link
	const githubLink = page.getByRole("link", { name: "Open in GitHub" });
	await expect(githubLink).toBeVisible();
	await expect(githubLink).toHaveAttribute(
		"href",
		"https://github.com/test-org/test-repo/pull/42",
	);
});

test("completed task shows event history", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E event history test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	// Navigate to task detail after completion
	await page.goto(`/tasks/${taskId}`);

	// Verify event log shows historical events from EventHub buffer
	await expect(page.getByText("Events")).toBeVisible();

	// Verify we see events that were streamed during the task
	await expect(page.getByText("Analyzing the codebase structure")).toBeVisible({
		timeout: 10_000,
	});
});

test("task detail shows metadata", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E metadata display test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	await page.goto(`/tasks/${taskId}`);

	// Verify status badge
	await expect(page.getByText("Succeeded")).toBeVisible();

	// Verify repo name
	await expect(page.getByText("test-org/test-repo")).toBeVisible();

	// Verify task description
	await expect(page.getByText("E2E metadata display test")).toBeVisible();
});

test("deep link to a specific task works", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E deep link test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	// Navigate directly via URL (no interaction with task list)
	await page.goto(`/tasks/${taskId}`);

	// Verify page loads correctly
	await expect(page.getByText("E2E deep link test")).toBeVisible();
	await expect(page.getByText("Succeeded")).toBeVisible();
});
