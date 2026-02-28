import type { components } from "./api.js";
import { api } from "./client.js";

type TaskResponse = components["schemas"]["TaskResponse"];

export class TaskDetailStore {
	task: TaskResponse | null = $state(null);
	loading = $state(false);
	error: string | null = $state(null);
	private controller: AbortController | null = null;

	async load(taskId: string): Promise<void> {
		this.controller?.abort();
		const controller = new AbortController();
		this.controller = controller;
		this.loading = true;
		this.error = null;
		try {
			const { data, response } = await api.GET("/api/v1/tasks/{taskID}", {
				params: { path: { taskID: taskId } },
				signal: controller.signal,
			});
			if (!response.ok) {
				this.error = `Failed to load task (${response.status})`;
				return;
			}
			this.task = data ?? null;
		} catch (e) {
			if (e instanceof DOMException && e.name === "AbortError") return;
			this.error = e instanceof Error ? e.message : "Network error";
		} finally {
			if (!controller.signal.aborted) {
				this.loading = false;
			}
		}
	}
}
