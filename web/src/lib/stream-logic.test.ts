import { describe, expect, it } from "vitest";
import { classifyMessage, type WSMessage } from "./stream-logic.js";

const MAX_GAP = 3;

function taskEvent(sequence: number): WSMessage {
	return {
		type: "task_event",
		data: {
			sequence,
			timestamp: "2024-01-01T00:00:00Z",
			type: "tool_call",
			summary: `event-${sequence}`,
		},
	};
}

function taskComplete(status = "completed"): WSMessage {
	return {
		type: "task_complete",
		data: {
			taskID: "task-1",
			status,
			prURL: "https://github.com/org/repo/pull/1",
		},
	};
}

describe("classifyMessage", () => {
	it("event with sequence 1, lastSequence 0 -> append", () => {
		const { action, newGapReconnectCount } = classifyMessage(
			taskEvent(1),
			0,
			0,
			MAX_GAP,
		);
		expect(action).toEqual({
			type: "append",
			event: taskEvent(1).data,
		});
		expect(newGapReconnectCount).toBe(0);
	});

	it("event with sequence 1, lastSequence 1 -> skip (duplicate)", () => {
		const { action } = classifyMessage(taskEvent(1), 1, 0, MAX_GAP);
		expect(action).toEqual({ type: "skip" });
	});

	it("event with sequence 1, lastSequence 5 -> skip (older duplicate)", () => {
		const { action } = classifyMessage(taskEvent(1), 5, 0, MAX_GAP);
		expect(action).toEqual({ type: "skip" });
	});

	it("event with sequence 5, lastSequence 3, gapCount 0 -> reconnect from 3, gapCount becomes 1", () => {
		const { action, newGapReconnectCount } = classifyMessage(
			taskEvent(5),
			3,
			0,
			MAX_GAP,
		);
		expect(action).toEqual({ type: "reconnect", fromSequence: 3 });
		expect(newGapReconnectCount).toBe(1);
	});

	it("event with sequence 5, lastSequence 3, gapCount 3 (at max) -> append (gap accepted), gapCount stays 3", () => {
		const { action, newGapReconnectCount } = classifyMessage(
			taskEvent(5),
			3,
			3,
			MAX_GAP,
		);
		expect(action).toEqual({
			type: "append",
			event: taskEvent(5).data,
		});
		expect(newGapReconnectCount).toBe(3);
	});

	it("event with sequence 2, lastSequence 1 -> append (sequential), gapCount resets to 0", () => {
		const { action, newGapReconnectCount } = classifyMessage(
			taskEvent(2),
			1,
			2,
			MAX_GAP,
		);
		expect(action).toEqual({
			type: "append",
			event: taskEvent(2).data,
		});
		expect(newGapReconnectCount).toBe(0);
	});

	it("event with sequence 5, lastSequence 0 -> append (gap check skipped when lastSequence is 0)", () => {
		const { action, newGapReconnectCount } = classifyMessage(
			taskEvent(5),
			0,
			0,
			MAX_GAP,
		);
		expect(action).toEqual({
			type: "append",
			event: taskEvent(5).data,
		});
		expect(newGapReconnectCount).toBe(0);
	});

	it("task_complete message -> complete with data payload", () => {
		const msg = taskComplete("completed");
		const { action } = classifyMessage(msg, 5, 0, MAX_GAP);
		expect(action).toEqual({
			type: "complete",
			data: {
				taskID: "task-1",
				status: "completed",
				prURL: "https://github.com/org/repo/pull/1",
			},
		});
	});

	it("out-of-order sequence [1, 3, 2]: detects gap on 3, skips 2 as stale", () => {
		// Event 1 arrives normally
		const r1 = classifyMessage(taskEvent(1), 0, 0, MAX_GAP);
		expect(r1.action).toEqual({ type: "append", event: taskEvent(1).data });

		// Event 3 arrives before 2 — gap detected (lastSequence=1, got 3)
		const r2 = classifyMessage(taskEvent(3), 1, r1.newGapReconnectCount, MAX_GAP);
		expect(r2.action).toEqual({ type: "reconnect", fromSequence: 1 });
		expect(r2.newGapReconnectCount).toBe(1);

		// After reconnect fills the gap, lastSequence is now 3.
		// Late event 2 arrives — already seen, should be skipped
		const r3 = classifyMessage(taskEvent(2), 3, 0, MAX_GAP);
		expect(r3.action).toEqual({ type: "skip" });
	});

	it("out-of-order with exhausted retries: accepts gap and continues", () => {
		// Simulate repeated out-of-order where reconnects can't fill the gap
		// After MAX_GAP reconnect attempts, the gap is accepted
		const r1 = classifyMessage(taskEvent(1), 0, 0, MAX_GAP);
		expect(r1.action.type).toBe("append");

		// Event 5 arrives (gap of 3), exhaust all reconnect attempts
		let gapCount = 0;
		for (let i = 0; i < MAX_GAP; i++) {
			const r = classifyMessage(taskEvent(5), 1, gapCount, MAX_GAP);
			expect(r.action).toEqual({ type: "reconnect", fromSequence: 1 });
			gapCount = r.newGapReconnectCount;
		}
		expect(gapCount).toBe(MAX_GAP);

		// Next attempt with exhausted retries — accepts the gap
		const rFinal = classifyMessage(taskEvent(5), 1, gapCount, MAX_GAP);
		expect(rFinal.action).toEqual({ type: "append", event: taskEvent(5).data });
	});

	it("unknown message type -> skip", () => {
		const msg = { type: "unknown_type", data: {} } as unknown as WSMessage;
		const { action } = classifyMessage(msg, 0, 0, MAX_GAP);
		expect(action).toEqual({ type: "skip" });
	});
});
