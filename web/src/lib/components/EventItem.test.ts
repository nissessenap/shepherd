import { fireEvent, render } from "@testing-library/svelte";
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

	it("renders ToolCallEvent with correct color for WebSearch tool", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "Searching the web",
					tool: "WebSearch",
				}),
			},
		});
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge?.className).toContain("text-success-fg");
	});

	it("renders ToolCallEvent with muted color for unknown tool", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "Doing something",
					tool: "SomeUnknownTool",
				}),
			},
		});
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge?.className).toContain("text-fg-muted");
	});

	it("renders ToolCallEvent with 'unknown' when tool field is missing", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "No tool specified",
				}),
			},
		});
		const badge = container.querySelector(".font-mono.font-medium");
		expect(badge?.textContent?.trim()).toBe("unknown");
	});

	it("renders ToolResultEvent as failure when output is missing", () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_result", {
					summary: "Something happened",
				}),
			},
		});
		const failIndicator = container.querySelector(".text-danger-fg");
		expect(failIndicator).not.toBeNull();
		expect(failIndicator?.textContent?.trim()).toBe("✗");
	});

	it("ThinkingEvent truncates long text and toggles with Show more/less", async () => {
		const longText = "A".repeat(250);
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("thinking", { summary: longText }),
			},
		});

		// Text should be truncated initially
		const italic = container.querySelector(".italic");
		expect(italic?.textContent).toContain("...");
		expect(italic?.textContent?.length).toBeLessThan(250);

		// "Show more" button should exist
		const showMoreBtn = container.querySelector<HTMLButtonElement>("button");
		expect(showMoreBtn).not.toBeNull();
		expect(showMoreBtn?.textContent).toBe("Show more");

		// Click to expand
		if (showMoreBtn) await fireEvent.click(showMoreBtn);
		const expandedText = container.querySelector(".italic");
		expect(expandedText?.textContent).toBe(longText);

		const showLessBtn = container.querySelector<HTMLButtonElement>("button");
		expect(showLessBtn?.textContent).toBe("Show less");

		// Click to collapse again
		if (showLessBtn) await fireEvent.click(showLessBtn);
		const collapsedText = container.querySelector(".italic");
		expect(collapsedText?.textContent).toContain("...");
	});

	it("ToolCallEvent toggles details on Show/Hide details click", async () => {
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_call", {
					summary: "Reading file",
					tool: "Read",
					input: { file_path: "src/main.go" },
				}),
			},
		});

		// "Show details" button should exist
		const showBtn = container.querySelector<HTMLButtonElement>("button");
		expect(showBtn).not.toBeNull();
		expect(showBtn?.textContent).toBe("Show details");

		// No <pre> visible initially
		expect(container.querySelector("pre")).toBeNull();

		// Click to expand
		if (showBtn) await fireEvent.click(showBtn);
		const pre = container.querySelector("pre");
		expect(pre).not.toBeNull();
		expect(pre?.textContent).toContain("src/main.go");

		const hideBtn = container.querySelector<HTMLButtonElement>("button");
		expect(hideBtn?.textContent).toBe("Hide details");

		// Click to collapse
		if (hideBtn) await fireEvent.click(hideBtn);
		expect(container.querySelector("pre")).toBeNull();
	});

	it("ToolResultEvent truncates long output and toggles with Expand/Collapse", async () => {
		const longOutput = "B".repeat(150);
		const { container } = render(EventItem, {
			props: {
				event: makeEvent("tool_result", {
					summary: "Result",
					output: { success: true, summary: longOutput },
				}),
			},
		});

		// Text should be truncated initially
		const pre = container.querySelector("pre");
		expect(pre?.textContent).toContain("...");
		expect(pre?.textContent?.length).toBeLessThan(150);

		// "Expand" button should exist
		const expandBtn = container.querySelector<HTMLButtonElement>("button");
		expect(expandBtn).not.toBeNull();
		expect(expandBtn?.textContent).toBe("Expand");

		// Click to expand
		if (expandBtn) await fireEvent.click(expandBtn);
		const expandedPre = container.querySelector("pre");
		expect(expandedPre?.textContent).toBe(longOutput);

		const collapseBtn = container.querySelector<HTMLButtonElement>("button");
		expect(collapseBtn?.textContent).toBe("Collapse");

		// Click to collapse
		if (collapseBtn) await fireEvent.click(collapseBtn);
		const collapsedPre = container.querySelector("pre");
		expect(collapsedPre?.textContent).toContain("...");
	});
});
