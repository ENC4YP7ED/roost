import { el, icon, type Child } from "../core/dom.ts";

export interface CollapseOptions {
  title: string;
  icon?: string;
  badge?: string | number;
  open?: boolean;
  trailing?: HTMLElement;
  children: Child[];
  onToggle?: (open: boolean) => void;
}

/** An animated collapsible section (accordion panel). */
export function Collapse(opts: CollapseOptions): HTMLElement {
  let open = opts.open ?? false;

  const body = el("div.rst-collapse__body", {}, el("div.rst-collapse__inner", {}, ...opts.children));
  const chevron = icon("chevron-right", { class: "rst-collapse__chevron" });

  const header = el("button.rst-collapse__header", {
    attrs: { type: "button", "aria-expanded": String(open) },
    onclick: () => setOpen(!open),
  },
    chevron,
    opts.icon ? icon(opts.icon, { class: "rst-collapse__icon" }) : null,
    el("span.rst-collapse__title", {}, opts.title),
    opts.badge != null ? el("span.rst-collapse__badge", {}, String(opts.badge)) : null,
    el("span.spacer"),
    opts.trailing ?? null,
  );

  const root = el("div.rst-collapse", {}, header, body);

  function setOpen(next: boolean) {
    open = next;
    root.classList.toggle("is-open", open);
    header.setAttribute("aria-expanded", String(open));
    if (open) {
      body.style.height = `${body.scrollHeight}px`;
      body.addEventListener("transitionend", function done() {
        if (open) body.style.height = "auto";
        body.removeEventListener("transitionend", done);
      });
    } else {
      body.style.height = `${body.scrollHeight}px`;
      void body.offsetHeight; // force reflow
      body.style.height = "0px";
    }
    opts.onToggle?.(open);
  }

  // Initial state without animation.
  if (open) {
    root.classList.add("is-open");
    body.style.height = "auto";
  } else {
    body.style.height = "0px";
  }

  return root;
}
