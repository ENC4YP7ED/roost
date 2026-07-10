import { el, icon, type Child } from "../core/dom.ts";

export type BadgeTone = "neutral" | "success" | "danger" | "warning" | "info" | "outline";

/** Small pill label. */
export function Badge(text: Child, tone: BadgeTone = "neutral", opts: { icon?: string } = {}): HTMLElement {
  return el("span.rst-badge", { class: `rst-badge--${tone}` },
    opts.icon ? icon(opts.icon) : null,
    el("span", {}, text),
  );
}

/** Indeterminate spinner. */
export function Spinner(size = 16): HTMLElement {
  return el("span.rst-spinner", { style: { width: `${size}px`, height: `${size}px` }, attrs: { role: "status", "aria-label": "Loading" } });
}

/** Full-area empty / placeholder state. */
export function EmptyState(opts: { icon?: string; title: string; description?: string; action?: HTMLElement }): HTMLElement {
  return el("div.rst-empty", {},
    opts.icon ? el("div.rst-empty__icon", {}, icon(opts.icon)) : null,
    el("div.rst-empty__title", {}, opts.title),
    opts.description ? el("div.rst-empty__desc", {}, opts.description) : null,
    opts.action ?? null,
  );
}

/** Centered loading state. */
export function LoadingState(label = "Loading…"): HTMLElement {
  return el("div.rst-loading", {}, Spinner(20), el("span.muted", {}, label));
}

export interface SegmentedOption { value: string; label?: string; icon?: string; title?: string; }

/** Segmented control (button group acting like radio). */
export function Segmented(opts: { options: SegmentedOption[]; value?: string; onChange?: (v: string) => void }): HTMLElement {
  let value = opts.value ?? opts.options[0]?.value ?? "";
  const buttons = new Map<string, HTMLButtonElement>();

  const root = el("div.rst-segmented", { attrs: { role: "tablist" } },
    ...opts.options.map((o) => {
      const btn = el("button.rst-segmented__btn", {
        class: o.value === value ? "is-active" : "",
        attrs: { type: "button", title: o.title ?? null },
        onclick: () => {
          value = o.value;
          for (const [v, b] of buttons) b.classList.toggle("is-active", v === value);
          opts.onChange?.(value);
        },
      }, o.icon ? icon(o.icon) : null, o.label ? el("span", {}, o.label) : null) as HTMLButtonElement;
      buttons.set(o.value, btn);
      return btn;
    }),
  );
  return root;
}

/** A labelled key/value row used in detail panels. */
export function Field(label: string, value: Child): HTMLElement {
  return el("div.rst-field", {},
    el("div.rst-field__label", {}, label),
    el("div.rst-field__value", {}, value),
  );
}

/** A toggle switch. */
export function Switch(opts: { checked?: boolean; onChange?: (checked: boolean) => void; label?: string }): HTMLElement {
  let checked = opts.checked ?? false;
  const knob = el("span.rst-switch__track", { class: checked ? "is-on" : "" }, el("span.rst-switch__knob"));
  const root = el("button.rst-switch", {
    attrs: { type: "button", role: "switch", "aria-checked": String(checked) },
    onclick: () => {
      checked = !checked;
      knob.classList.toggle("is-on", checked);
      root.setAttribute("aria-checked", String(checked));
      opts.onChange?.(checked);
    },
  }, knob, opts.label ? el("span.rst-switch__label", {}, opts.label) : null);
  return root;
}
