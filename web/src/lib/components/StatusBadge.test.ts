import { render } from "@testing-library/svelte";
import { describe, expect, it } from "vitest";
import StatusBadge from "./StatusBadge.svelte";

describe("StatusBadge", () => {
	it.each([
		["Pending", "Pending", "text-attention-fg"],
		["Running", "Running", "text-info-fg"],
		["Succeeded", "Succeeded", "text-success-fg"],
		["Failed", "Failed", "text-danger-fg"],
		["TimedOut", "Timed Out", "text-attention-fg"],
		["Cancelled", "Cancelled", "text-fg-muted"],
	])("renders %s status with label %s and color class %s", (status, expectedLabel, expectedColorClass) => {
		const { container } = render(StatusBadge, { props: { status } });
		const badge = container.querySelector("span");
		expect(badge).not.toBeNull();
		expect(badge?.textContent?.trim()).toBe(expectedLabel);
		expect(badge?.className).toContain(expectedColorClass);
	});

	it("renders unknown status with raw value as label", () => {
		const { container } = render(StatusBadge, {
			props: { status: "CustomStatus" },
		});
		const badge = container.querySelector("span");
		expect(badge?.textContent?.trim()).toBe("CustomStatus");
		expect(badge?.className).toContain("text-fg-muted");
	});

	it("handles null-like status string", () => {
		const { container } = render(StatusBadge, { props: { status: "null" } });
		const badge = container.querySelector("span");
		expect(badge?.textContent?.trim()).toBe("null");
		expect(badge?.className).toContain("text-fg-muted");
	});
});
