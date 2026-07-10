import { el, icon, type Child } from "../core/dom.ts";

export type ButtonVariant = "primary" | "default" | "ghost" | "danger" | "subtle";
export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonOptions {
  label?: string;
  icon?: string;
  iconRight?: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
  title?: string;
  disabled?: boolean;
  loading?: boolean;
  block?: boolean;
  onClick?: (ev: MouseEvent) => void;
}

/** A custom button. Returns the element; flip `.loading`/`.disabled` via attrs. */
export function Button(opts: ButtonOptions): HTMLButtonElement {
  const {
    label,
    variant = "default",
    size = "md",
    block = false,
    disabled = false,
    loading = false,
  } = opts;

  const children: Child[] = [];
  if (loading) children.push(icon("spinner", { spin: true, class: "rst-btn-spin" }));
  else if (opts.icon) children.push(icon(opts.icon));
  if (label) children.push(el("span", { class: "rst-btn-label" }, label));
  if (opts.iconRight) children.push(icon(opts.iconRight));

  const btn = el("button.rst-btn", {
    class: `rst-btn--${variant} rst-btn--${size}${block ? " rst-btn--block" : ""}`,
    attrs: { type: "button", title: opts.title ?? null, "aria-busy": loading },
    disabled: disabled || loading,
    onclick: opts.onClick,
  }, ...children) as HTMLButtonElement;

  return btn;
}

/** Icon-only square button. */
export function IconButton(name: string, opts: Omit<ButtonOptions, "label" | "icon"> = {}): HTMLButtonElement {
  const btn = Button({ ...opts, icon: name });
  btn.classList.add("rst-btn--icon");
  return btn;
}
