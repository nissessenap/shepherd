import { expect, test } from "@playwright/test";
import { createTask, waitForAPI, waitForTaskPhase } from "./helpers.ts";

let taskId: string;

test.beforeAll(async () => {
	await waitForAPI();
	taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E task list test",
	});
	await waitForTaskPhase(taskId, "Succeeded");
});

test("task list loads and displays completed tasks", async ({ page }) => {
	await page.goto("/tasks?active=false");

	await expect(page.getByText("E2E task list test").first()).toBeVisible({
		timeout: 10_000,
	});
	await expect(page.getByText("Succeeded").first()).toBeVisible();
});

test("clicking a task navigates to detail view", async ({ page }) => {
	await page.goto("/tasks?active=false");

	const taskLink = page.getByText("E2E task list test").first();
	await expect(taskLink).toBeVisible({ timeout: 10_000 });
	await taskLink.click();

	await expect(page).toHaveURL(new RegExp(`/tasks/`));
	await expect(page.getByText("E2E task list test")).toBeVisible();
});

test("empty state shows helpful message", async ({ page }) => {
	await page.goto("/tasks?q=nonexistent-gibberish-xyz");
	await expect(
		page.getByText("No tasks match your filters."),
	).toBeVisible();
});

test("active filter toggle works", async ({ page }) => {
	await page.goto("/tasks");
	await page.getByRole("button", { name: "All" }).click();
	await expect(page).toHaveURL(/active=false/);
});
