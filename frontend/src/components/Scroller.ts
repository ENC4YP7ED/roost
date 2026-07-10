import { el } from "../core/dom.ts";

export interface ScrollerHandle {
  el: HTMLElement;
  viewport: HTMLElement;
  scrollTo(opts: ScrollToOptions): void;
  refresh(): void;
}

/**
 * Custom scroll container with overlay scrollbars that only appear while
 * scrolling or hovering — keeps the OLED canvas clean. Wraps content in a
 * viewport and draws its own draggable thumbs for both axes.
 */
export function Scroller(content: HTMLElement, opts: { class?: string } = {}): ScrollerHandle {
  const viewport = el("div.rst-scroller__viewport", {}, content);
  const vThumb = el("div.rst-scroller__thumb.rst-scroller__thumb--v");
  const hThumb = el("div.rst-scroller__thumb.rst-scroller__thumb--h");
  const vTrack = el("div.rst-scroller__track.rst-scroller__track--v", {}, vThumb);
  const hTrack = el("div.rst-scroller__track.rst-scroller__track--h", {}, hThumb);

  const root = el("div.rst-scroller", { class: opts.class ?? "" }, viewport, vTrack, hTrack);

  let hideTimer = 0;
  const showBars = () => {
    root.classList.add("is-scrolling");
    clearTimeout(hideTimer);
    hideTimer = window.setTimeout(() => root.classList.remove("is-scrolling"), 900);
  };

  const refresh = () => {
    const { scrollHeight, clientHeight, scrollWidth, clientWidth, scrollTop, scrollLeft } = viewport;

    const vVisible = scrollHeight > clientHeight + 1;
    vTrack.classList.toggle("hidden", !vVisible);
    if (vVisible) {
      const thumbH = Math.max(24, (clientHeight / scrollHeight) * clientHeight);
      const maxTop = clientHeight - thumbH;
      const top = (scrollTop / (scrollHeight - clientHeight)) * maxTop;
      vThumb.style.height = `${thumbH}px`;
      vThumb.style.transform = `translateY(${top}px)`;
    }

    const hVisible = scrollWidth > clientWidth + 1;
    hTrack.classList.toggle("hidden", !hVisible);
    if (hVisible) {
      const thumbW = Math.max(24, (clientWidth / scrollWidth) * clientWidth);
      const maxLeft = clientWidth - thumbW;
      const left = (scrollLeft / (scrollWidth - clientWidth)) * maxLeft;
      hThumb.style.width = `${thumbW}px`;
      hThumb.style.transform = `translateX(${left}px)`;
    }
  };

  viewport.addEventListener("scroll", () => { refresh(); showBars(); }, { passive: true });
  const ro = new ResizeObserver(refresh);
  ro.observe(viewport);
  ro.observe(content);

  // Drag handling for both thumbs.
  const dragThumb = (thumb: HTMLElement, axis: "v" | "h") => {
    thumb.addEventListener("mousedown", (e) => {
      e.preventDefault();
      const startPos = axis === "v" ? e.clientY : e.clientX;
      const startScroll = axis === "v" ? viewport.scrollTop : viewport.scrollLeft;
      const scrollRange = axis === "v"
        ? viewport.scrollHeight - viewport.clientHeight
        : viewport.scrollWidth - viewport.clientWidth;
      const trackRange = axis === "v"
        ? viewport.clientHeight - thumb.offsetHeight
        : viewport.clientWidth - thumb.offsetWidth;

      const onMove = (me: MouseEvent) => {
        const delta = (axis === "v" ? me.clientY : me.clientX) - startPos;
        const ratio = trackRange > 0 ? delta / trackRange : 0;
        const next = startScroll + ratio * scrollRange;
        if (axis === "v") viewport.scrollTop = next;
        else viewport.scrollLeft = next;
      };
      const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        root.classList.remove("is-dragging");
      };
      root.classList.add("is-dragging");
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });
  };
  dragThumb(vThumb, "v");
  dragThumb(hThumb, "h");

  requestAnimationFrame(refresh);

  return {
    el: root,
    viewport,
    scrollTo: (o) => viewport.scrollTo(o),
    refresh,
  };
}
