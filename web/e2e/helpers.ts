const API_URL = process.env.API_URL ?? "http://localhost:30080";

/** Short random suffix to make task descriptions unique per test run. */
export function uniqueId(): string {
	return Math.random().toString(36).slice(2, 8);
}

export async function createTask(opts: {
	repo: string;
	description: string;
}): Promise<string> {
	const res = await fetch(`${API_URL}/api/v1/tasks`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			repo: { url: `https://github.com/${opts.repo}`, ref: "main" },
			task: {
				description: opts.description,
				context: "E2E Playwright test task",
			},
			callbackURL: "https://example.com/callback",
			runner: {
				sandboxTemplateName: "e2e-runner",
				timeout: "5m",
			},
		}),
	});

	if (!res.ok) {
		const body = await res.text();
		throw new Error(
			`Failed to create task (${res.status}): ${body}`,
		);
	}

	const data = (await res.json()) as { id: string };
	return data.id;
}

export async function waitForTaskPhase(
	taskId: string,
	phase: string,
	timeoutMs = 120_000,
): Promise<void> {
	const start = Date.now();
	while (Date.now() - start < timeoutMs) {
		const res = await fetch(`${API_URL}/api/v1/tasks/${taskId}`);
		if (res.ok) {
			const task = (await res.json()) as {
				status: { phase: string };
			};
			if (task.status.phase === phase) return;
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
	throw new Error(
		`Task ${taskId} did not reach phase ${phase} within ${timeoutMs}ms`,
	);
}

export async function waitForAPI(timeoutMs = 30_000): Promise<void> {
	const start = Date.now();
	while (Date.now() - start < timeoutMs) {
		try {
			const res = await fetch(`${API_URL}/healthz`);
			if (res.ok) return;
		} catch {
			// API not ready yet
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
	throw new Error(`API not ready within ${timeoutMs}ms`);
}
