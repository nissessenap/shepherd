import type { components } from "./api.js";
import { api } from "./client.js";

type TaskResponse = components["schemas"]["TaskResponse"];

export interface TaskFilters {
	repo?: string;
	fleet?: string;
	active?: "true" | "false";
}

export class TasksStore {
	data: TaskResponse[] = $state([]);
	loading = $state(false);
	error: string | null = $state(null);

	async load(params?: TaskFilters): Promise<void> {
		this.loading = true;
		this.error = null;
		try {
			const { data, response } = await api.GET("/api/v1/tasks", {
				params: { query: params },
			});
			if (!response.ok) {
				this.error = `Failed to load tasks (${response.status})`;
				return;
			}
			this.data = data ?? [];
		} catch (e) {
			this.error = e instanceof Error ? e.message : "Network error";
		} finally {
			this.loading = false;
		}
	}
}
