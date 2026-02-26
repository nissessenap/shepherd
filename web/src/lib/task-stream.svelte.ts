import type { components } from "./api.js";
import { type ConnectionState, WSClient } from "./ws.js";

type TaskEvent = components["schemas"]["TaskEvent"];

export type StreamPhase =
	| "idle"
	| "connecting"
	| "streaming"
	| "completed"
	| "error";

interface WSMessage {
	type: "task_event" | "task_complete";
	data: TaskEvent | TaskCompleteData;
}

interface TaskCompleteData {
	taskID: string;
	status: string;
	prURL?: string;
	error?: string;
}

/**
 * Reactive store for streaming task events over WebSocket.
 *
 * State machine:
 *   idle → connecting → streaming → completed
 *   streaming → connecting (on connection loss)
 *   connecting → error (after max retries)
 */
export class TaskStream {
	events: TaskEvent[] = $state([]);
	lastSequence = $state(0);
	streamPhase: StreamPhase = $state("idle");
	connectionState: ConnectionState = $state("disconnected");
	completionData: TaskCompleteData | null = $state(null);

	private client: WSClient<WSMessage> | null = null;

	connect(taskId: string): void {
		this.streamPhase = "connecting";
		this.events = [];
		this.lastSequence = 0;
		this.completionData = null;

		const wsProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
		const wsHost = import.meta.env.VITE_API_URL
			? new URL(import.meta.env.VITE_API_URL as string).host
			: window.location.host;
		const url = `${wsProtocol}//${wsHost}/api/v1/tasks/${taskId}/events`;

		this.client = new WSClient<WSMessage>({
			url,
			onMessage: (msg) => this.handleMessage(msg),
			onStateChange: (state) => this.handleStateChange(state),
		});

		this.client.connect();
	}

	disconnect(): void {
		this.client?.disconnect();
		this.client = null;
		if (this.streamPhase !== "completed") {
			this.streamPhase = "idle";
		}
	}

	private handleMessage(msg: WSMessage): void {
		if (msg.type === "task_event") {
			const event = msg.data as TaskEvent;
			// Deduplicate: skip events we already have
			if (event.sequence <= this.lastSequence) return;
			// Sequence gap detection: if incoming > lastSequence + 1, reconnect
			if (this.lastSequence > 0 && event.sequence > this.lastSequence + 1) {
				this.client?.reconnectFrom(this.lastSequence);
				return;
			}
			this.events.push(event);
			this.lastSequence = event.sequence;
		} else if (msg.type === "task_complete") {
			this.completionData = msg.data as TaskCompleteData;
			this.streamPhase = "completed";
			this.client?.disconnect();
		}
	}

	private handleStateChange(state: ConnectionState): void {
		this.connectionState = state;
		if (state === "connected") {
			this.streamPhase = "streaming";
		} else if (state === "disconnected" && this.streamPhase !== "completed") {
			this.streamPhase = "error";
		}
	}
}
