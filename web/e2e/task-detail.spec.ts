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
const description = `E2E detail ${runId}`;

test.beforeAll(async () => {
	await waitForAPI();
	taskId = await createTask({
		repo: "test-org/test-repo",
		description,
	});
	await waitForTaskPhase(taskId, "Succeeded");
});

test("task completes and shows PR link", async ({ page }) => {
	await page.goto(`/tasks/${taskId}`);

	await expect(page.getByText("Succeeded")).toBeVisible({ timeout: API_WAIT });

	await expect(page.getByText("Pull Request #42")).toBeVisible({
		timeout: API_WAIT,
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

	await expect(page.getByText("Succeeded")).toBeVisible({ timeout: API_WAIT });
	await expect(
		page.getByText("test-org/test-repo").first(),
	).toBeVisible();
	await expect(page.getByText(description)).toBeVisible();
});
