export type ConnectionState =
	| "connecting"
	| "connected"
	| "reconnecting"
	| "disconnected";

export interface WSClientOptions<T> {
	url: string;
	onMessage: (msg: T) => void;
	onStateChange: (state: ConnectionState) => void;
	getReconnectAfter?: () => number | undefined;
	maxRetries?: number;
	/** @internal Test seam — override WebSocket constructor */
	createWebSocket?: (url: string) => WebSocket;
}

const BASE_DELAY_MS = 1000;
const MAX_DELAY_MS = 30_000;
const JITTER_FACTOR = 0.3;

/**
 * Calculate exponential backoff delay with jitter.
 */
export function backoffDelay(attempt: number): number {
	const delay = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
	const jitter = delay * JITTER_FACTOR * (Math.random() * 2 - 1);
	return Math.max(0, delay + jitter);
}

/**
 * Build a WebSocket URL with an optional `?after=N` parameter for reconnection.
 */
export function buildWSUrl(base: string, after?: number): string {
	if (after === undefined || after <= 0) return base;
	const sep = base.includes("?") ? "&" : "?";
	return `${base}${sep}after=${after}`;
}

/**
 * Minimal WebSocket client with reconnection and typed JSON messages.
 *
 * The client is write-only from the server's perspective — no messages
 * are sent from the client to the server.
 */
export class WSClient<T> {
	private ws: WebSocket | null = null;
	private retryCount = 0;
	private retryTimer: ReturnType<typeof setTimeout> | undefined;
	private closed = false;
	private readonly opts: Required<WSClientOptions<T>>;

	constructor(opts: WSClientOptions<T>) {
		this.opts = {
			maxRetries: 5,
			getReconnectAfter: () => undefined,
			createWebSocket: (url) => new WebSocket(url),
			...opts,
		};
	}

	connect(afterSequence?: number): void {
		this.closed = false;
		this.retryCount = 0;
		this.open(afterSequence);
	}

	disconnect(): void {
		this.closed = true;
		clearTimeout(this.retryTimer);
		if (this.ws) {
			this.ws.onclose = null;
			this.ws.close();
			this.ws = null;
		}
		this.opts.onStateChange("disconnected");
	}

	private open(afterSequence?: number): void {
		if (this.closed) return;

		const url = buildWSUrl(this.opts.url, afterSequence);
		const state = this.retryCount === 0 ? "connecting" : "reconnecting";
		this.opts.onStateChange(state);

		this.ws = this.opts.createWebSocket(url);

		this.ws.onopen = () => {
			this.retryCount = 0;
			this.opts.onStateChange("connected");
		};

		this.ws.onmessage = (ev) => {
			try {
				const msg = JSON.parse(ev.data as string) as T;
				this.opts.onMessage(msg);
			} catch (e) {
				if (import.meta.env.DEV) {
					console.warn("[WSClient] Failed to parse message:", e);
				}
			}
		};

		this.ws.onclose = (ev) => {
			if (this.closed) return;
			// Normal closure (1000) means server intentionally closed — don't reconnect
			if (ev.code === 1000) {
				this.opts.onStateChange("disconnected");
				return;
			}
			this.scheduleReconnect();
		};

		this.ws.onerror = () => {
			// onclose will fire after onerror — reconnection handled there
		};
	}

	private scheduleReconnect(): void {
		if (this.closed) return;
		if (this.retryCount >= this.opts.maxRetries) {
			this.opts.onStateChange("disconnected");
			return;
		}
		this.opts.onStateChange("reconnecting");
		const delay = backoffDelay(this.retryCount);
		this.retryCount++;
		this.retryTimer = setTimeout(() => {
			this.open(this.opts.getReconnectAfter());
		}, delay);
	}

	/**
	 * Reconnect with a new `after` parameter (e.g. after sequence gap detection).
	 */
	reconnectFrom(afterSequence: number): void {
		if (this.ws) {
			this.ws.onclose = null;
			this.ws.close();
			this.ws = null;
		}
		this.retryCount = 0;
		this.open(afterSequence);
	}
}
