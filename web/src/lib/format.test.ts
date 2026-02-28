import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	extractRepoName,
	formatDuration,
	formatRelativeTime,
	formatTimestamp,
} from "./format.js";

afterEach(() => {
	vi.restoreAllMocks();
});

describe("formatDuration", () => {
	const BASE = "2024-01-01T00:00:00Z";

	function endAfter(seconds: number): string {
		return new Date(new Date(BASE).getTime() + seconds * 1000).toISOString();
	}

	describe("happy paths", () => {
		it("returns seconds only when duration is under 60s", () => {
			expect(formatDuration(BASE, endAfter(42))).toBe("42s");
		});

		it("returns Xm YYs with zero-padded seconds", () => {
			expect(formatDuration(BASE, endAfter(5 * 60 + 3))).toBe("5m 03s");
		});

		it("returns Xh YYm with zero-padded minutes", () => {
			expect(formatDuration(BASE, endAfter(2 * 3600 + 5 * 60))).toBe("2h 05m");
		});

		it("falls back to Date.now() when endTime is null", () => {
			const fakeNow = new Date(BASE).getTime() + 30_000;
			vi.spyOn(Date, "now").mockReturnValue(fakeNow);

			expect(formatDuration(BASE, null)).toBe("30s");
		});

		it("falls back to Date.now() when endTime is undefined", () => {
			const fakeNow = new Date(BASE).getTime() + 30_000;
			vi.spyOn(Date, "now").mockReturnValue(fakeNow);

			expect(formatDuration(BASE)).toBe("30s");
		});
	});

	describe("edge cases", () => {
		it('returns "0s" for zero-second duration', () => {
			expect(formatDuration(BASE, BASE)).toBe("0s");
		});

		it('clamps negative duration to "0s" when end is before start', () => {
			const before = new Date(new Date(BASE).getTime() - 5000).toISOString();
			expect(formatDuration(BASE, before)).toBe("0s");
		});

		it('returns "59s" at the 59-second boundary', () => {
			expect(formatDuration(BASE, endAfter(59))).toBe("59s");
		});

		it('returns "1m 00s" at exactly 60 seconds', () => {
			expect(formatDuration(BASE, endAfter(60))).toBe("1m 00s");
		});

		it('returns "59m 59s" just below the hour boundary', () => {
			expect(formatDuration(BASE, endAfter(59 * 60 + 59))).toBe("59m 59s");
		});

		it('returns "1h 00m" at exactly 3600 seconds', () => {
			expect(formatDuration(BASE, endAfter(3600))).toBe("1h 00m");
		});

		it("returns fallback for invalid start date", () => {
			const result = formatDuration("not-a-date", "2024-01-01T00:00:00Z");
			expect(result).toBe("--");
		});
	});
});

describe("formatRelativeTime", () => {
	const NOW = new Date("2024-06-15T12:00:00Z").getTime();

	function ago(seconds: number): string {
		return new Date(NOW - seconds * 1000).toISOString();
	}

	function ahead(seconds: number): string {
		return new Date(NOW + seconds * 1000).toISOString();
	}

	beforeEach(() => {
		vi.spyOn(Date, "now").mockReturnValue(NOW);
	});

	describe("happy paths", () => {
		it('returns "just now" when less than 60s ago', () => {
			expect(formatRelativeTime(ago(30))).toBe("just now");
		});

		it('returns "Nm ago" for minutes', () => {
			expect(formatRelativeTime(ago(5 * 60))).toBe("5m ago");
		});

		it('returns "Nh ago" for hours', () => {
			expect(formatRelativeTime(ago(3 * 3600))).toBe("3h ago");
		});

		it('returns "Nd ago" for days', () => {
			expect(formatRelativeTime(ago(2 * 86400))).toBe("2d ago");
		});
	});

	describe("edge cases", () => {
		it('returns "just now" for a future timestamp', () => {
			expect(formatRelativeTime(ahead(60))).toBe("just now");
		});

		it('returns "1m ago" at exactly 60 seconds', () => {
			expect(formatRelativeTime(ago(60))).toBe("1m ago");
		});

		it('returns "1h ago" at exactly 60 minutes', () => {
			expect(formatRelativeTime(ago(3600))).toBe("1h ago");
		});

		it('returns "1d ago" at exactly 24 hours', () => {
			expect(formatRelativeTime(ago(86400))).toBe("1d ago");
		});

		it("returns fallback for invalid date input", () => {
			expect(formatRelativeTime("not-a-date")).toBe("--");
		});
	});
});

describe("formatTimestamp", () => {
	it("formats as HH:MM:SS pattern", () => {
		const result = formatTimestamp("2024-06-15T14:30:05Z");
		expect(result).toMatch(/^\d{2}:\d{2}:\d{2}$/);
	});

	it("formats midnight as HH:MM:SS pattern", () => {
		const result = formatTimestamp("2024-01-01T00:00:00Z");
		expect(result).toMatch(/^\d{2}:\d{2}:\d{2}$/);
	});

	it("returns 'Invalid Date' for garbage input", () => {
		const result = formatTimestamp("garbage");
		expect(result).toContain("Invalid");
	});

	it("returns 'Invalid Date' for empty string input", () => {
		const result = formatTimestamp("");
		expect(result).toContain("Invalid");
	});
});

describe("extractRepoName", () => {
	describe("happy paths", () => {
		it("extracts org/repo from a GitHub URL", () => {
			expect(extractRepoName("https://github.com/org/repo")).toBe("org/repo");
		});

		it("strips .git suffix", () => {
			expect(extractRepoName("https://github.com/org/repo.git")).toBe(
				"org/repo",
			);
		});
	});

	describe("edge cases", () => {
		it("returns raw input for an invalid URL", () => {
			expect(extractRepoName("not-a-url")).toBe("not-a-url");
		});

		it("returns empty string for empty input", () => {
			expect(extractRepoName("")).toBe("");
		});

		it("preserves trailing slash in the path", () => {
			expect(extractRepoName("https://github.com/org/repo/")).toBe("org/repo/");
		});

		it("preserves deep paths", () => {
			expect(extractRepoName("https://github.com/org/repo/tree/main/src")).toBe(
				"org/repo/tree/main/src",
			);
		});

		it("handles non-GitHub URLs the same way", () => {
			expect(extractRepoName("https://gitlab.com/team/project")).toBe(
				"team/project",
			);
		});
	});
});
