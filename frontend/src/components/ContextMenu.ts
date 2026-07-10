import { buildMenuPanel, type MenuItem } from "./Menu.ts";
import { openPopover, type PopoverHandle } from "../core/popover.ts";

let current: PopoverHandle | null = null;

/** Open a context menu at a screen point (e.g. from a contextmenu event). */
export function openContextMenu(x: number, y: number, items: MenuItem[]): PopoverHandle {
  current?.close();
  let handle: PopoverHandle;
  const panel = buildMenuPanel(items, () => handle.close());
  handle = openPopover({
    anchor: { x, y },
    content: panel,
    placement: "bottom-start",
    onClose: () => { if (current === handle) current = null; },
  });
  current = handle;
  return handle;
}

/** Wire an element so right-clicking it opens a context menu. */
export function attachContextMenu(
  target: HTMLElement,
  items: (ev: MouseEvent) => MenuItem[],
): void {
  target.addEventListener("contextmenu", (e) => {
    const built = items(e);
    if (!built.length) return;
    e.preventDefault();
    e.stopPropagation();
    openContextMenu(e.clientX, e.clientY, built);
  });
}
