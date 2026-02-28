import { expect, test } from "@playwright/test";
import { API_WAIT } from "./helpers.ts";

/**
 * These tests use Playwright's page.routeWebSocket() to test WebSocket
 * edge cases deterministically without depending on real server timing.
 */

test("reconnects after WebSocket disconnect", async ({ page }) => {
	let connectionCount = 0;

	// Intercept WebSocket connections to the events endpoint
	await page.routeWebSocket(/\/api\/v1\/tasks\/.*\/events/, (ws) => {
		connectionCount++;

		if (connectionCount === 1) {
			// First connection: send a few events then close abnormally
			ws.send(
				JSON.stringify({
					type: "task_event",
					data: {
						sequence: 1,
						timestamp: new Date().toISOString(),
						type: "thinking",
						summary: "First connection event",
					},
				}),
			);

			// Close abnormally after a short delay to trigger reconnection
			setTimeout(() => {
				ws.close({ code: 1006, reason: "Simulated disconnect" });
			}, 500);
		} else {
			// Reconnection: send more events
			ws.send(
				JSON.stringify({
					type: "task_event",
					data: {
						sequence: 2,
						timestamp: new Date().toISOString(),
						type: "tool_call",
						summary: "Reconnected event",
						tool: "Read",
					},
				}),
			);
		}
	});

	// Mock the task REST API to return a running task
	await page.route("**/api/v1/tasks/mock-task-reconnect", (route) => {
		route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				id: "mock-task-reconnect",
				namespace: "shepherd-system",
				repo: { url: "https://github.com/test-org/test-repo" },
				task: { description: "Reconnection test" },
				callbackURL: "https://example.com/callback",
				status: { phase: "Running", message: "Running" },
				createdAt: new Date().toISOString(),
			}),
		});
	});

	await page.goto("/tasks/mock-task-reconnect");

	// Verify first event appears
	await expect(page.getByText("First connection event")).toBeVisible({
		timeout: API_WAIT,
	});

	// Verify "Reconnecting..." indicator appears after disconnect
	await expect(page.getByText("Reconnecting...")).toBeVisible({
		timeout: API_WAIT,
	});

	// Verify reconnected event arrives
	await expect(page.getByText("Reconnected event")).toBeVisible({
		timeout: API_WAIT,
	});
});

test("shows disconnected state after max retries", async ({ page }) => {
	// Intercept and immediately close all WebSocket connections
	await page.routeWebSocket(/\/api\/v1\/tasks\/.*\/events/, (ws) => {
		ws.close({ code: 1006, reason: "Simulated failure" });
	});

	// Mock the task REST API to return a running task
	await page.route("**/api/v1/tasks/mock-task-maxretry", (route) => {
		route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				id: "mock-task-maxretry",
				namespace: "shepherd-system",
				repo: { url: "https://github.com/test-org/test-repo" },
				task: { description: "Max retry test" },
				callbackURL: "https://example.com/callback",
				status: { phase: "Running", message: "Running" },
				createdAt: new Date().toISOString(),
			}),
		});
	});

	await page.goto("/tasks/mock-task-maxretry");

	// After max retries (5 attempts with exponential backoff),
	// the connection indicator should show "Disconnected"
	await expect(page.getByText("Disconnected")).toBeVisible({
		timeout: 60_000,
	});
});

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
