// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { registerKeyboardShortcuts } from "./keyboard.js";

function fireKey(key: string, target?: Partial<HTMLElement>): KeyboardEvent {
	const event = new KeyboardEvent("keydown", {
		key,
		bubbles: true,
		cancelable: true,
	});

	if (target) {
		Object.defineProperty(event, "target", { value: target });
	}

	window.dispatchEvent(event);
	return event;
}

afterEach(() => {
	vi.restoreAllMocks();
});

describe("registerKeyboardShortcuts", () => {
	it('pressing "j" calls onNavigate(1)', () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		fireKey("j");
		expect(onNavigate).toHaveBeenCalledWith(1);

		cleanup();
	});

	it('pressing "k" calls onNavigate(-1)', () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		fireKey("k");
		expect(onNavigate).toHaveBeenCalledWith(-1);

		cleanup();
	});

	it('pressing "Enter" calls onSelect()', () => {
		const onSelect = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onSelect });

		fireKey("Enter");
		expect(onSelect).toHaveBeenCalledOnce();

		cleanup();
	});

	it('pressing "Escape" calls onBack() and calls preventDefault()', () => {
		const onBack = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onBack });

		const event = fireKey("Escape");
		expect(onBack).toHaveBeenCalledOnce();
		expect(event.defaultPrevented).toBe(true);

		cleanup();
	});

	it('pressing "Backspace" calls onBack() and calls preventDefault()', () => {
		const onBack = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onBack });

		const event = fireKey("Backspace");
		expect(onBack).toHaveBeenCalledOnce();
		expect(event.defaultPrevented).toBe(true);

		cleanup();
	});

	it('pressing "e" calls onExpandAll()', () => {
		const onExpandAll = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onExpandAll });

		fireKey("e");
		expect(onExpandAll).toHaveBeenCalledOnce();

		cleanup();
	});

	it('pressing "c" calls onCollapseAll()', () => {
		const onCollapseAll = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onCollapseAll });

		fireKey("c");
		expect(onCollapseAll).toHaveBeenCalledOnce();

		cleanup();
	});

	it('pressing "G" (uppercase) calls onScrollBottom()', () => {
		const onScrollBottom = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onScrollBottom });

		fireKey("G");
		expect(onScrollBottom).toHaveBeenCalledOnce();

		cleanup();
	});

	it('pressing "g" (lowercase) does NOT call onScrollBottom()', () => {
		const onScrollBottom = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onScrollBottom });

		fireKey("g");
		expect(onScrollBottom).not.toHaveBeenCalled();

		cleanup();
	});

	it('pressing "/" calls onFocusSearch() and calls preventDefault()', () => {
		const onFocusSearch = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onFocusSearch });

		const event = fireKey("/");
		expect(onFocusSearch).toHaveBeenCalledOnce();
		expect(event.defaultPrevented).toBe(true);

		cleanup();
	});

	it('pressing "j" does NOT call callback when <input> is focused', () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		fireKey("j", { tagName: "INPUT", isContentEditable: false });
		expect(onNavigate).not.toHaveBeenCalled();

		cleanup();
	});

	it('pressing "j" does NOT call callback when <textarea> is focused', () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		fireKey("j", { tagName: "TEXTAREA", isContentEditable: false });
		expect(onNavigate).not.toHaveBeenCalled();

		cleanup();
	});

	it('pressing "j" does NOT call callback when contentEditable element is focused', () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		fireKey("j", { tagName: "DIV", isContentEditable: true });
		expect(onNavigate).not.toHaveBeenCalled();

		cleanup();
	});

	it("returned cleanup function removes the listener", () => {
		const onNavigate = vi.fn();
		const cleanup = registerKeyboardShortcuts({ onNavigate });

		cleanup();

		fireKey("j");
		expect(onNavigate).not.toHaveBeenCalled();
	});

	it('pressing unregistered key (e.g., "a") calls no callbacks', () => {
		const onNavigate = vi.fn();
		const onSelect = vi.fn();
		const onBack = vi.fn();
		const cleanup = registerKeyboardShortcuts({
			onNavigate,
			onSelect,
			onBack,
		});

		fireKey("a");
		expect(onNavigate).not.toHaveBeenCalled();
		expect(onSelect).not.toHaveBeenCalled();
		expect(onBack).not.toHaveBeenCalled();

		cleanup();
	});

	it("omitting optional callback does not throw when corresponding key is pressed", () => {
		const cleanup = registerKeyboardShortcuts({});

		expect(() => {
			fireKey("j");
			fireKey("k");
			fireKey("Enter");
			fireKey("Escape");
			fireKey("Backspace");
			fireKey("e");
			fireKey("c");
			fireKey("G");
			fireKey("/");
		}).not.toThrow();

		cleanup();
	});
});
