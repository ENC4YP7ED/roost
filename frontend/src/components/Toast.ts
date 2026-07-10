import { el, icon } from "../core/dom.ts";

export type ToastKind = "success" | "error" | "info" | "warning";

let container: HTMLElement | null = null;

function host(): HTMLElement {
  if (!container) {
    container = el("div.rst-toasts");
    document.body.appendChild(container);
  }
  return container;
}

const ICONS: Record<ToastKind, string> = {
  success: "circle-check",
  error: "circle-xmark",
  info: "circle-info",
  warning: "triangle-exclamation",
};

/** Show a transient toast notification. */
export function toast(message: string, kind: ToastKind = "info", opts: { duration?: number; title?: string } = {}): void {
  const duration = opts.duration ?? (kind === "error" ? 6000 : 3500);

  const node = el("div.rst-toast", { class: `rst-toast--${kind}` },
    icon(ICONS[kind], { class: "rst-toast__icon" }),
    el("div.rst-toast__content", {},
      opts.title ? el("div.rst-toast__title", {}, opts.title) : null,
      el("div.rst-toast__msg", {}, message),
    ),
    el("button.rst-toast__close", { attrs: { type: "button", "aria-label": "Dismiss" }, onclick: () => remove() }, icon("xmark")),
  );

  let timer = 0;
  const remove = () => {
    clearTimeout(timer);
    node.classList.add("is-leaving");
    node.addEventListener("animationend", () => node.remove(), { once: true });
  };
  timer = window.setTimeout(remove, duration);
  node.addEventListener("mouseenter", () => clearTimeout(timer));
  node.addEventListener("mouseleave", () => { timer = window.setTimeout(remove, 1200); });

  host().appendChild(node);
}

export const notify = {
  success: (m: string, t?: string) => toast(m, "success", t ? { title: t } : {}),
  error: (m: string, t?: string) => toast(m, "error", t ? { title: t } : {}),
  info: (m: string, t?: string) => toast(m, "info", t ? { title: t } : {}),
  warning: (m: string, t?: string) => toast(m, "warning", t ? { title: t } : {}),
};
