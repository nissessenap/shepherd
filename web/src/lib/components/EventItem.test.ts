// @vitest-environment jsdom
import { render } from "@testing-library/svelte";
import { describe, expect, it } from "vitest";
import EventItem from "./EventItem.svelte";

function makeEvent(
	type: "thinking" | "tool_call" | "tool_result" | "error",
	overrides: Record<string, unknown> = {},
) {
	return {
		sequence: 1,
		timestamp: "2024-01-01T12:30:05Z",
		type,
		summary: "Test summary",
		...overrides,
	};
}

describe("EventItem", () => {
	it("renders ThinkingEvent for thinking type", () => {
		const { container } = render(EventItem, {
			props: { event: makeEvent("thinking", { summary: "Analyzing code..." }) },
		});
		// ThinkingEvent renders italic text
		const italic = container.querySelector(".italic");
		expect(italic).not.toBeNull();
		expect(italic?.textContent).toContain("Analyzing code...");
	});

	it("renders ToolCallEvent for tool_call type with tool badge", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "Reading file",
					tool: "Read",
				}),
			},
		});
		// ToolCallEvent renders a tool name badge with font-mono
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge).not.toBeNull();
		expect(badge?.textContent?.trim()).toBe("Read");
	});

	it("renders ToolCallEvent with correct color for Bash tool", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "Running command",
					tool: "Bash",
				}),
			},
		});
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge?.className).toContain("text-attention-fg");
	});

	it("renders ToolCallEvent with correct color for file operation tools", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", { summary: "Reading", tool: "Read" }),
			},
		});
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge?.className).toContain("text-accent-fg");
	});

	it("renders ToolResultEvent for tool_result type with success indicator", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_result", {
					summary: "File contents",
					output: { success: true, summary: "OK" },
				}),
			},
		});
		const successIndicator = container.querySelector(".text-success-fg");
		expect(successIndicator).not.toBeNull();
		expect(successIndicator?.textContent?.trim()).toBe("✓");
	});

	it("renders ToolResultEvent with failure indicator", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_result", {
					summary: "Command failed",
					output: { success: false, summary: "exit code 1" },
				}),
			},
		});
		const failIndicator = container.querySelector(".text-danger-fg");
		expect(failIndicator).not.toBeNull();
		expect(failIndicator?.textContent?.trim()).toBe("✗");
	});

	it("renders ErrorEvent for error type with error styling", () => {
		const { container } = render(EventItem, {
			props: { event: makeEvent("error", { summary: "Something went wrong" }) },
		});
		// ErrorEvent renders with danger border and "Error" label
		const errorBorder = container.querySelector(".border-danger-fg\\/20");
		expect(errorBorder).not.toBeNull();
		const errorLabel = container.querySelector(".text-danger-fg");
		expect(errorLabel).not.toBeNull();
		expect(errorLabel?.textContent?.trim()).toBe("Error");
	});

	it("renders nothing for unknown event type", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("thinking", {
					type: "unknown_type" as "thinking",
					summary: "hello",
				}),
			},
		});
		// No sub-component rendered — container should have no meaningful content
		const inner = container.querySelector("div");
		// EventItem renders no children if none of the #if branches match
		expect(inner).toBeNull();
	});
});
