/**
 * Register global keyboard shortcuts.
 * Returns a cleanup function that removes the event listener.
 *
 * Shortcuts are only active when no input/textarea/select is focused.
 */
export function registerKeyboardShortcuts(opts: {
	onNavigate?: (delta: number) => void;
	onSelect?: () => void;
	onBack?: () => void;
	onExpandAll?: () => void;
	onCollapseAll?: () => void;
	onScrollBottom?: () => void;
	onFocusSearch?: () => void;
}): () => void {
	function handler(e: KeyboardEvent) {
		const target = e.target as HTMLElement;
		if (
			target.tagName === "INPUT" ||
			target.tagName === "TEXTAREA" ||
			target.tagName === "SELECT" ||
			target.isContentEditable
		) {
			return;
		}

		switch (e.key) {
			case "j":
				opts.onNavigate?.(1);
				break;
			case "k":
				opts.onNavigate?.(-1);
				break;
			case "Enter":
				opts.onSelect?.();
				break;
			case "Escape":
			case "Backspace":
				e.preventDefault();
				opts.onBack?.();
				break;
			case "e":
				opts.onExpandAll?.();
				break;
			case "c":
				opts.onCollapseAll?.();
				break;
			case "G":
				opts.onScrollBottom?.();
				break;
			case "/":
				e.preventDefault();
				opts.onFocusSearch?.();
				break;
			default:
				return;
		}
	}

	window.addEventListener("keydown", handler);
	return () => window.removeEventListener("keydown", handler);
}
