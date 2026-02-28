import type { components } from "./api.js";

type TaskEvent = components["schemas"]["TaskEvent"];

export type WSMessage =
	| { type: "task_event"; data: TaskEvent }
	| { type: "task_complete"; data: TaskCompleteData };

export interface TaskCompleteData {
	taskID: string;
	status: string;
	prURL?: string;
	error?: string;
}

export type MessageAction =
	| { type: "append"; event: TaskEvent }
	| { type: "reconnect"; fromSequence: number }
	| { type: "complete"; data: TaskCompleteData }
	| { type: "skip" };

/**
 * Pure classification of an incoming WebSocket message.
 *
 * Returns the action to take and the updated gap reconnect counter.
 */
export function classifyMessage(
	msg: WSMessage,
	lastSequence: number,
	gapReconnectCount: number,
	maxGapReconnects: number,
): { action: MessageAction; newGapReconnectCount: number } {
	if (msg.type === "task_complete") {
		return {
			action: { type: "complete", data: msg.data },
			newGapReconnectCount: gapReconnectCount,
		};
	}

	if (msg.type !== "task_event") {
		return {
			action: { type: "skip" },
			newGapReconnectCount: gapReconnectCount,
		};
	}

	const event = msg.data;

	// Deduplicate: skip events we already have
	if (event.sequence <= lastSequence) {
		return {
			action: { type: "skip" },
			newGapReconnectCount: gapReconnectCount,
		};
	}

	// Sequence gap detection (only when we have a baseline)
	if (lastSequence > 0 && event.sequence > lastSequence + 1) {
		if (gapReconnectCount < maxGapReconnects) {
			return {
				action: { type: "reconnect", fromSequence: lastSequence },
				newGapReconnectCount: gapReconnectCount + 1,
			};
		}
		// Max gap reconnects exhausted — accept the gap and append
		return {
			action: { type: "append", event },
			newGapReconnectCount: gapReconnectCount,
		};
	}

	// Sequential event — reset gap counter
	return {
		action: { type: "append", event },
		newGapReconnectCount: 0,
	};
}
