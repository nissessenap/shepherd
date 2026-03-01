import { expect, test } from "@playwright/test";
import { API_WAIT } from "./helpers.ts";

/**
 * These tests mock the REST API (and WebSocket where needed) to verify
 * UI states for various task phases and error conditions without a real backend.
 */

test("failed task shows error callout", async ({ page }) => {
	await page.route("**/api/v1/tasks/mock-task-failed", (route) => {
		route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				id: "mock-task-failed",
				namespace: "shepherd-system",
				repo: { url: "https://github.com/test-org/test-repo" },
				task: { description: "Failed task test" },
				callbackURL: "https://example.com/callback",
				status: {
					phase: "Failed",
					message: "Task failed",
					error: "Process exited with code 1: something went wrong",
				},
				createdAt: new Date().toISOString(),
				completionTime: new Date().toISOString(),
			}),
		});
	});

	// Prevent stray WebSocket connection attempts
	await page.routeWebSocket(/\/api\/v1\/tasks\/.*\/events/, (ws) => {
		ws.close({ code: 1000, reason: "Not applicable" });
	});

	await page.goto("/tasks/mock-task-failed");

	await expect(page.getByText("Task Failed")).toBeVisible({ timeout: API_WAIT });
	await expect(
		page.getByText("Process exited with code 1: something went wrong"),
	).toBeVisible();
	await expect(page.getByRole("button", { name: "Copy" })).toBeVisible();
	await expect(page.getByText("Failed", { exact: true })).toBeVisible();
});

test("pending task shows waiting message", async ({ page }) => {
	await page.route("**/api/v1/tasks/mock-task-pending", (route) => {
		route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				id: "mock-task-pending",
				namespace: "shepherd-system",
				repo: { url: "https://github.com/test-org/test-repo" },
				task: { description: "Pending task test" },
				callbackURL: "https://example.com/callback",
				status: { phase: "Pending", message: "Queued" },
				createdAt: new Date().toISOString(),
			}),
		});
	});

	// Prevent stray WebSocket connection attempts
	await page.routeWebSocket(/\/api\/v1\/tasks\/.*\/events/, (ws) => {
		ws.close({ code: 1000, reason: "Not applicable" });
	});

	await page.goto("/tasks/mock-task-pending");

	await expect(page.getByText("Waiting for sandbox...")).toBeVisible({
		timeout: API_WAIT,
	});
	await expect(page.getByText("Pending", { exact: true })).toBeVisible();
});

test("task list shows error and retry button on API failure", async ({ page }) => {
	await page.route("**/api/v1/tasks", (route) => {
		route.fulfill({
			status: 500,
			contentType: "application/json",
			body: JSON.stringify({ error: "Internal server error" }),
		});
	});

	await page.goto("/tasks?active=false");

	await expect(page.getByText("Failed to load tasks (500)")).toBeVisible({
		timeout: API_WAIT,
	});
	await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
});
