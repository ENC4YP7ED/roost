import { el } from "../core/dom.ts";

let tip: HTMLElement | null = null;
let hideTimer = 0;

/** Attach a hover tooltip to an element. */
export function tooltip(target: HTMLElement, text: string | (() => string), placement: "top" | "bottom" = "top"): void {
  target.addEventListener("mouseenter", () => {
    clearTimeout(hideTimer);
    show(target, typeof text === "function" ? text() : text, placement);
  });
  target.addEventListener("mouseleave", hide);
  target.addEventListener("mousedown", hide);
}

function show(target: HTMLElement, text: string, placement: "top" | "bottom") {
  if (!text) return;
  hide();
  tip = el("div.rst-tooltip", { class: `rst-tooltip--${placement}` }, text);
  document.body.appendChild(tip);

  const r = target.getBoundingClientRect();
  const tr = tip.getBoundingClientRect();
  let top = placement === "top" ? r.top - tr.height - 7 : r.bottom + 7;
  let left = r.left + r.width / 2 - tr.width / 2;
  left = Math.max(6, Math.min(left, window.innerWidth - tr.width - 6));
  if (top < 6) top = r.bottom + 7;
  tip.style.top = `${top}px`;
  tip.style.left = `${left}px`;
  requestAnimationFrame(() => tip?.classList.add("is-visible"));
}

function hide() {
  if (tip) {
    const t = tip;
    tip = null;
    t.classList.remove("is-visible");
    hideTimer = window.setTimeout(() => t.remove(), 120);
  }
}
