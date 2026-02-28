import { expect, test } from "@playwright/test";

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
		timeout: 10_000,
	});

	// Verify "Reconnecting..." indicator appears after disconnect
	await expect(page.getByText("Reconnecting...")).toBeVisible({
		timeout: 10_000,
	});

	// Verify reconnected event arrives
	await expect(page.getByText("Reconnected event")).toBeVisible({
		timeout: 10_000,
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
