/**
 * Lightweight floating-layer manager: positions a panel near an anchor (an
 * element or a point), keeps it inside the viewport, and wires up dismissal on
 * outside-click, Escape and scroll.
 */

export type Placement = "bottom-start" | "bottom-end" | "top-start" | "top-end" | "right-start";

export interface PopoverHandle {
  el: HTMLElement;
  close(): void;
}

interface OpenOptions {
  anchor: HTMLElement | { x: number; y: number };
  content: HTMLElement;
  placement?: Placement;
  matchWidth?: boolean;
  offset?: number;
  onClose?: () => void;
}

let openCount = 0;

/** True while any popover (menu, select dropdown, context menu) is open. */
export function anyPopoverOpen(): boolean {
  return openCount > 0;
}

export function openPopover(opts: OpenOptions): PopoverHandle {
  const { placement = "bottom-start", offset = 6 } = opts;

  const layer = document.createElement("div");
  layer.className = "rst-popover-layer";
  layer.appendChild(opts.content);
  document.body.appendChild(layer);
  openCount++;

  const position = () => {
    const panel = opts.content;
    const pw = panel.offsetWidth;
    const ph = panel.offsetHeight;
    const vw = window.innerWidth;
    const vh = window.innerHeight;

    let rect: DOMRect;
    if (opts.anchor instanceof HTMLElement) {
      rect = opts.anchor.getBoundingClientRect();
      if (opts.matchWidth) panel.style.minWidth = `${rect.width}px`;
    } else {
      rect = new DOMRect(opts.anchor.x, opts.anchor.y, 0, 0);
    }

    let top = 0;
    let left = 0;
    switch (placement) {
      case "bottom-start": top = rect.bottom + offset; left = rect.left; break;
      case "bottom-end": top = rect.bottom + offset; left = rect.right - pw; break;
      case "top-start": top = rect.top - ph - offset; left = rect.left; break;
      case "top-end": top = rect.top - ph - offset; left = rect.right - pw; break;
      case "right-start": top = rect.top; left = rect.right + offset; break;
    }

    // Flip / clamp to keep it on screen.
    if (left + pw > vw - 8) left = Math.max(8, vw - pw - 8);
    if (left < 8) left = 8;
    if (top + ph > vh - 8) {
      const above = rect.top - ph - offset;
      top = above > 8 ? above : Math.max(8, vh - ph - 8);
    }
    if (top < 8) top = 8;

    panel.style.position = "fixed";
    panel.style.top = `${top}px`;
    panel.style.left = `${left}px`;
  };

  // Measure off-screen first, then place.
  opts.content.style.position = "fixed";
  opts.content.style.visibility = "hidden";
  requestAnimationFrame(() => {
    position();
    opts.content.style.visibility = "visible";
  });

  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    document.removeEventListener("mousedown", onDocDown, true);
    document.removeEventListener("keydown", onKey, true);
    window.removeEventListener("resize", position);
    window.removeEventListener("scroll", onScroll, true);
    layer.remove();
    openCount--;
    opts.onClose?.();
  };

  const onDocDown = (e: MouseEvent) => {
    if (!layer.contains(e.target as Node) &&
        !(opts.anchor instanceof HTMLElement && opts.anchor.contains(e.target as Node))) {
      close();
    }
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      // Exclusive: Escape dismisses only the popover, never what's beneath.
      e.stopImmediatePropagation();
      e.stopPropagation();
      close();
    }
  };
  const onScroll = (e: Event) => {
    if (layer.contains(e.target as Node)) return;
    close();
  };

  // Defer listener attach so the opening click doesn't immediately close it.
  setTimeout(() => {
    document.addEventListener("mousedown", onDocDown, true);
    document.addEventListener("keydown", onKey, true);
    window.addEventListener("resize", position);
    window.addEventListener("scroll", onScroll, true);
  }, 0);

  return { el: layer, close };
}
