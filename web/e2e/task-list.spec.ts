import { expect, test } from "@playwright/test";
import {
	API_WAIT,
	createTask,
	uniqueId,
	waitForAPI,
	waitForTaskPhase,
} from "./helpers.ts";

const runId = uniqueId();
let taskId: string;
const description = `E2E task-list ${runId}`;

test.beforeAll(async () => {
	await waitForAPI();
	taskId = await createTask({
		repo: "test-org/test-repo",
		description,
	});
	await waitForTaskPhase(taskId, "Succeeded");
});

test("task list loads and displays completed tasks", async ({ page }) => {
	await page.goto("/tasks?active=false");

	await expect(page.getByText(description)).toBeVisible({
		timeout: API_WAIT,
	});
	await expect(page.getByText("Succeeded").first()).toBeVisible();
});

test("clicking a task navigates to detail view", async ({ page }) => {
	await page.goto("/tasks?active=false");

	const taskLink = page.getByText(description);
	await expect(taskLink).toBeVisible({ timeout: API_WAIT });
	await taskLink.click();

	await expect(page).toHaveURL(new RegExp(`/tasks/${taskId}`));
	await expect(page.getByText(description)).toBeVisible();
});

test("empty state shows helpful message", async ({ page }) => {
	await page.goto("/tasks?q=nonexistent-gibberish-xyz");
	await expect(
		page.getByText("No tasks match your filters."),
	).toBeVisible();
});

test("active filter toggle works", async ({ page }) => {
	await page.goto("/tasks");

	// Active view should not show our completed task
	await expect(page.getByText(description)).not.toBeVisible({ timeout: API_WAIT });

	// Click "All" to see all tasks including completed
	await page.getByRole("button", { name: "All" }).click();
	await expect(page).toHaveURL(/active=false/);
	await expect(page.getByText(description)).toBeVisible({ timeout: API_WAIT });
});
