import type { components } from "./api.js";
import {
	classifyMessage,
	type TaskCompleteData,
	type WSMessage,
} from "./stream-logic.js";
import { type ConnectionState, WSClient } from "./ws.js";

type TaskEvent = components["schemas"]["TaskEvent"];

export type StreamPhase =
	| "idle"
	| "connecting"
	| "streaming"
	| "completed"
	| "error";

/**
 * Reactive store for streaming task events over WebSocket.
 *
 * State machine:
 *   idle -> connecting -> streaming -> completed
 *   streaming -> connecting (on connection loss)
 *   connecting -> error (after max retries)
 */
export class TaskStream {
	events: TaskEvent[] = $state([]);
	lastSequence = $state(0);
	streamPhase: StreamPhase = $state("idle");
	connectionState: ConnectionState = $state("disconnected");
	completionData: TaskCompleteData | null = $state(null);

	private client: WSClient<WSMessage> | null = null;
	private gapReconnectCount = 0;
	private static readonly MAX_GAP_RECONNECTS = 3;

	connect(taskId: string): void {
		this.streamPhase = "connecting";
		this.events = [];
		this.lastSequence = 0;
		this.completionData = null;
		this.gapReconnectCount = 0;

		const wsProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
		const wsHost = import.meta.env.VITE_API_URL
			? new URL(import.meta.env.VITE_API_URL as string).host
			: window.location.host;
		const url = `${wsProtocol}//${wsHost}/api/v1/tasks/${taskId}/events`;

		this.client = new WSClient<WSMessage>({
			url,
			onMessage: (msg) => this.handleMessage(msg),
			onStateChange: (state) => this.handleStateChange(state),
			getReconnectAfter: () => this.lastSequence || undefined,
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
		const { action, newGapReconnectCount } = classifyMessage(
			msg,
			this.lastSequence,
			this.gapReconnectCount,
			TaskStream.MAX_GAP_RECONNECTS,
		);
		this.gapReconnectCount = newGapReconnectCount;

		switch (action.type) {
			case "append":
				this.events.push(action.event);
				this.lastSequence = action.event.sequence;
				break;
			case "reconnect":
				this.client?.reconnectFrom(action.fromSequence);
				break;
			case "complete":
				this.completionData = action.data;
				this.streamPhase = "completed";
				this.client?.disconnect();
				break;
			case "skip":
				break;
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
