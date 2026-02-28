import { expect, test } from "@playwright/test";
import { createTask, waitForAPI, waitForTaskPhase } from "./helpers.ts";

// One shared task for all detail tests — created once, reused by all
let taskId: string;

test.beforeAll(async () => {
	await waitForAPI();
	taskId = await createTask({
		repo: "test-org/test-repo",
		description: "E2E detail test task",
	});
	await waitForTaskPhase(taskId, "Succeeded");
});

test("task completes and shows PR link", async ({ page }) => {
	await page.goto(`/tasks/${taskId}`);

	await expect(page.getByText("Succeeded")).toBeVisible({ timeout: 10_000 });

	// Verify PR card
	await expect(page.getByText("Pull Request #42")).toBeVisible({
		timeout: 10_000,
	});

	const githubLink = page.getByRole("link", { name: "Open in GitHub" });
	await expect(githubLink).toBeVisible();
	await expect(githubLink).toHaveAttribute(
		"href",
		"https://github.com/test-org/test-repo/pull/42",
	);
});

test("task detail shows metadata", async ({ page }) => {
	await page.goto(`/tasks/${taskId}`);

	await expect(page.getByText("Succeeded")).toBeVisible({ timeout: 10_000 });
	await expect(
		page.getByText("test-org/test-repo").first(),
	).toBeVisible();
	await expect(page.getByText("E2E detail test task")).toBeVisible();
});

test("deep link to a specific task works", async ({ page }) => {
	await page.goto(`/tasks/${taskId}`);

	await expect(page.getByText("E2E detail test task")).toBeVisible({
		timeout: 10_000,
	});
	await expect(page.getByText("Succeeded")).toBeVisible();
});
