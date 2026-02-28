// @vitest-environment jsdom
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	type Mock,
	vi,
} from "vitest";
import type { ConnectionState } from "./ws.js";
import { WSClient } from "./ws.js";

// ---------------------------------------------------------------------------
// MockWebSocket
// ---------------------------------------------------------------------------

class MockWebSocket {
	url: string;
	onopen: ((ev: Event) => void) | null = null;
	onmessage: ((ev: MessageEvent) => void) | null = null;
	onclose: ((ev: CloseEvent) => void) | null = null;
	onerror: ((ev: Event) => void) | null = null;
	readyState = 0; // CONNECTING

	constructor(url: string) {
		this.url = url;
	}

	close() {
		/* no-op */
	}

	// Test helpers
	simulateOpen() {
		this.readyState = 1;
		this.onopen?.(new Event("open"));
	}

	simulateMessage(data: unknown) {
		this.onmessage?.(
			new MessageEvent("message", { data: JSON.stringify(data) }),
		);
	}

	simulateClose(code = 1006, reason = "") {
		this.readyState = 3;
		this.onclose?.(new CloseEvent("close", { code, reason }));
	}

	simulateError() {
		this.onerror?.(new Event("error"));
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

interface TestMsg {
	seq: number;
	text: string;
}

interface Harness {
	client: WSClient<TestMsg>;
	messages: TestMsg[];
	states: ConnectionState[];
	sockets: MockWebSocket[];
	onMessage: Mock;
	onStateChange: Mock;
	factory: (url: string) => WebSocket;
	getReconnectAfter: Mock;
}

function createHarness(overrides?: {
	maxRetries?: number;
	getReconnectAfter?: () => number | undefined;
}): Harness {
	const messages: TestMsg[] = [];
	const states: ConnectionState[] = [];
	const sockets: MockWebSocket[] = [];

	const onMessage = vi.fn((msg: TestMsg) => messages.push(msg));
	const onStateChange = vi.fn((s: ConnectionState) => states.push(s));
	const getReconnectAfter = vi.fn(
		overrides?.getReconnectAfter ?? (() => undefined),
	);

	const factory = (url: string): WebSocket => {
		const sock = new MockWebSocket(url);
		sockets.push(sock);
		return sock as unknown as WebSocket;
	};

	const client = new WSClient<TestMsg>({
		url: "ws://test/stream",
		onMessage,
		onStateChange,
		getReconnectAfter,
		maxRetries: overrides?.maxRetries ?? 5,
		createWebSocket: factory,
	});

	return {
		client,
		messages,
		states,
		sockets,
		onMessage,
		onStateChange,
		factory,
		getReconnectAfter,
	};
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("WSClient", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it("connect() then onopen transitions state to connected", () => {
		const { client, states, sockets } = createHarness();

		client.connect();

		expect(sockets).toHaveLength(1);
		expect(states).toEqual(["connecting"]);

		sockets[0].simulateOpen();

		expect(states).toEqual(["connecting", "connected"]);
	});

	it("onmessage with valid JSON calls onMessage callback", () => {
		const { client, messages, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();
		sockets[0].simulateMessage({ seq: 1, text: "hello" });

		expect(messages).toEqual([{ seq: 1, text: "hello" }]);
	});

	it("onmessage with invalid JSON does not throw", () => {
		const { client, messages, sockets, onMessage } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		// Send raw non-JSON string via onmessage directly
		sockets[0].onmessage?.(
			new MessageEvent("message", { data: "not json{{{" }),
		);

		expect(onMessage).not.toHaveBeenCalled();
		expect(messages).toHaveLength(0);
	});

	it("abnormal close (1006) triggers reconnection", () => {
		const { client, states, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();
		sockets[0].simulateClose(1006);

		expect(states).toContain("reconnecting");

		// Advance timer to trigger reconnection
		vi.advanceTimersByTime(60_000);

		// A second socket should have been created
		expect(sockets).toHaveLength(2);
	});

	it("normal close (1000) does not reconnect", () => {
		const { client, states, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();
		sockets[0].simulateClose(1000);

		expect(states).toEqual(["connecting", "connected", "disconnected"]);

		// Advance timers — no new socket should appear
		vi.advanceTimersByTime(60_000);

		expect(sockets).toHaveLength(1);
	});

	it("max retries exhausted transitions to disconnected", () => {
		const { client, states, sockets } = createHarness({ maxRetries: 2 });

		client.connect();
		sockets[0].simulateOpen();

		// Abnormal close #1 — schedules retry
		sockets[0].simulateClose(1006);
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(2);

		// Abnormal close #2 — schedules retry
		sockets[1].simulateClose(1006);
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(3);

		// Abnormal close #3 — maxRetries (2) exhausted, no more retries
		sockets[2].simulateClose(1006);
		vi.advanceTimersByTime(60_000);

		// No 4th socket created
		expect(sockets).toHaveLength(3);
		expect(states[states.length - 1]).toBe("disconnected");
	});

	it("reconnection uses getReconnectAfter() for the after parameter", () => {
		const { client, sockets } = createHarness({
			getReconnectAfter: () => 42,
		});

		client.connect();
		sockets[0].simulateOpen();
		sockets[0].simulateClose(1006);

		// Advance past the backoff delay
		vi.advanceTimersByTime(60_000);

		expect(sockets).toHaveLength(2);
		expect(sockets[1].url).toBe("ws://test/stream?after=42");
	});

	it("disconnect() closes WebSocket and sets state to disconnected", () => {
		const { client, states, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		const closeSpy = vi.spyOn(sockets[0], "close");
		client.disconnect();

		expect(closeSpy).toHaveBeenCalled();
		expect(states[states.length - 1]).toBe("disconnected");
	});

	it("disconnect() prevents further reconnection attempts", () => {
		const { client, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		// Trigger abnormal close, then immediately disconnect
		sockets[0].simulateClose(1006);
		client.disconnect();

		// Advance timers — no new socket should be created after disconnect
		vi.advanceTimersByTime(60_000);

		// Only the original socket should exist
		expect(sockets).toHaveLength(1);
	});

	it("reconnectFrom(N) closes current connection and opens new one with after=N", () => {
		const { client, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		const closeSpy = vi.spyOn(sockets[0], "close");
		client.reconnectFrom(99);

		expect(closeSpy).toHaveBeenCalled();
		expect(sockets).toHaveLength(2);
		expect(sockets[1].url).toBe("ws://test/stream?after=99");
	});

	it("reconnectFrom() after disconnect() does not create new connections", () => {
		const { client, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();
		client.disconnect();

		// reconnectFrom after disconnect — open() bails because closed=true
		client.reconnectFrom(10);
		expect(sockets).toHaveLength(1);
	});

	it("multiple rapid disconnect() calls do not throw", () => {
		const { client, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		expect(() => {
			client.disconnect();
			client.disconnect();
			client.disconnect();
		}).not.toThrow();
	});

	it("error event alone does not create duplicate reconnections", () => {
		const { client, sockets } = createHarness();

		client.connect();
		sockets[0].simulateOpen();

		// Fire error then close (as browsers do)
		sockets[0].simulateError();
		sockets[0].simulateClose(1006);

		// Should only schedule one reconnection, not two
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(2);
	});

	it("retryCount resets to 0 on successful reconnection", () => {
		const { client, states, sockets } = createHarness({ maxRetries: 2 });

		// Initial connection
		client.connect();
		sockets[0].simulateOpen();

		// Abnormal close triggers reconnect (retryCount goes to 1)
		sockets[0].simulateClose(1006);
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(2);

		// Successful reconnection — retryCount should reset to 0
		sockets[1].simulateOpen();
		expect(states).toContain("connected");

		// Now close abnormally again — should get full maxRetries again
		sockets[1].simulateClose(1006);
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(3);

		sockets[2].simulateClose(1006);
		vi.advanceTimersByTime(60_000);
		expect(sockets).toHaveLength(4);

		// This close exceeds maxRetries (2) from the second round
		sockets[3].simulateClose(1006);
		vi.advanceTimersByTime(60_000);

		// No 5th socket — retries exhausted again
		expect(sockets).toHaveLength(4);
		expect(states[states.length - 1]).toBe("disconnected");
	});
});
