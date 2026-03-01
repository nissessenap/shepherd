import { describe, expect, it } from "vitest";
import { backoffDelay, buildWSUrl } from "./ws.js";

describe("backoffDelay", () => {
	it("attempt 0 returns value in range [700, 1300]", () => {
		for (let i = 0; i < 50; i++) {
			const d = backoffDelay(0);
			expect(d).toBeGreaterThanOrEqual(700);
			expect(d).toBeLessThanOrEqual(1300);
		}
	});

	it("attempt 1 returns value in range [1400, 2600]", () => {
		for (let i = 0; i < 50; i++) {
			const d = backoffDelay(1);
			expect(d).toBeGreaterThanOrEqual(1400);
			expect(d).toBeLessThanOrEqual(2600);
		}
	});

	it("attempt 2 returns value in range [2800, 5200]", () => {
		for (let i = 0; i < 50; i++) {
			const d = backoffDelay(2);
			expect(d).toBeGreaterThanOrEqual(2800);
			expect(d).toBeLessThanOrEqual(5200);
		}
	});

	it("attempt 10+ is capped at MAX_DELAY_MS (30000) range [21000, 39000]", () => {
		for (let i = 0; i < 50; i++) {
			const d = backoffDelay(10);
			expect(d).toBeGreaterThanOrEqual(21000);
			expect(d).toBeLessThanOrEqual(39000);
		}
	});

	it("very large attempt (100) does not overflow to Infinity or NaN", () => {
		const d = backoffDelay(100);
		expect(Number.isFinite(d)).toBe(true);
		expect(Number.isNaN(d)).toBe(false);
	});

	it("always returns non-negative number", () => {
		for (let attempt = 0; attempt < 20; attempt++) {
			for (let i = 0; i < 10; i++) {
				expect(backoffDelay(attempt)).toBeGreaterThanOrEqual(0);
			}
		}
	});

	it("attempt -1 returns a value (does not throw)", () => {
		const d = backoffDelay(-1);
		expect(Number.isFinite(d)).toBe(true);
	});
});

describe("buildWSUrl", () => {
	it("returns base URL unchanged when after is undefined", () => {
		expect(buildWSUrl("ws://host/path")).toBe("ws://host/path");
	});

	it("returns base URL unchanged when after is 0", () => {
		expect(buildWSUrl("ws://host/path", 0)).toBe("ws://host/path");
	});

	it("returns base URL unchanged when after is negative", () => {
		expect(buildWSUrl("ws://host/path", -1)).toBe("ws://host/path");
	});

	it("appends ?after=5 when base has no query string", () => {
		expect(buildWSUrl("ws://host/path", 5)).toBe("ws://host/path?after=5");
	});

	it("appends &after=5 when base already has a query string", () => {
		expect(buildWSUrl("ws://host/path?foo=bar", 5)).toBe(
			"ws://host/path?foo=bar&after=5",
		);
	});

	it("handles after=1 (minimum positive value)", () => {
		expect(buildWSUrl("ws://host/path", 1)).toBe("ws://host/path?after=1");
	});

	it("handles empty base URL", () => {
		expect(buildWSUrl("", 5)).toBe("?after=5");
	});

	it("handles base URL that is just a question mark", () => {
		expect(buildWSUrl("?", 5)).toBe("?&after=5");
	});
});
