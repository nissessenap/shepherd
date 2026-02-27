import { expect, test } from "@playwright/test";
import { createTask, waitForAPI, waitForTaskPhase } from "./helpers.ts";

test.beforeAll(async () => {
	await waitForAPI();
});

test("task list loads and displays tasks", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E task list display test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	await page.goto("/tasks");

	// Verify task appears with correct content
	await expect(page.getByText("E2E task list display test")).toBeVisible();
	await expect(page.getByText("test-org/test-repo")).toBeVisible();
	// Verify a status badge exists (Succeeded)
	await expect(page.getByText("Succeeded")).toBeVisible();
});

test("clicking a task navigates to detail view", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E click navigation test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	await page.goto("/tasks");

	// Click the task row link
	await page.getByText("E2E click navigation test").click();

	// Verify URL changes to task detail
	await expect(page).toHaveURL(new RegExp(`/tasks/${taskId}`));
	// Verify task detail page renders
	await expect(page.getByText("E2E click navigation test")).toBeVisible();
});

test("filtering tasks by search updates URL", async ({ page }) => {
	await createTask({
		repo: "test-org/search-repo",
		description: "E2E searchable unique description",
	});

	await page.goto("/tasks?active=false");

	// Type in search input
	const searchInput = page.getByPlaceholder("Search tasks...");
	await searchInput.fill("searchable unique");

	// Wait for debounced URL update
	await expect(page).toHaveURL(/q=searchable/);

	// Verify matching task is shown
	await expect(
		page.getByText("E2E searchable unique description"),
	).toBeVisible();
});

test("empty state shows helpful message", async ({ page }) => {
	await page.goto("/tasks?q=nonexistent-gibberish-xyz");
	await expect(
		page.getByText("No tasks match your filters."),
	).toBeVisible();
});

test("browser back button preserves filter state", async ({ page }) => {
	const taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E back button test",
	});
	await waitForTaskPhase(taskId, "Succeeded");

	// Navigate with filter
	await page.goto("/tasks?active=false");
	await expect(page.getByText("E2E back button test")).toBeVisible();

	// Click to navigate to detail
	await page.getByText("E2E back button test").click();
	await expect(page).toHaveURL(new RegExp(`/tasks/${taskId}`));

	// Go back
	await page.goBack();

	// Verify we're back on the filtered list
	await expect(page).toHaveURL(/active=false/);
});
