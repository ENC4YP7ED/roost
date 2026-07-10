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
  if (loading) children.push(icon("spinner", { spin: true, class: "gtma-btn-spin" }));
  else if (opts.icon) children.push(icon(opts.icon));
  if (label) children.push(el("span", { class: "gtma-btn-label" }, label));
  if (opts.iconRight) children.push(icon(opts.iconRight));

  const btn = el("button.gtma-btn", {
    class: `gtma-btn--${variant} gtma-btn--${size}${block ? " gtma-btn--block" : ""}`,
    attrs: { type: "button", title: opts.title ?? null, "aria-busy": loading },
    disabled: disabled || loading,
    onclick: opts.onClick,
  }, ...children) as HTMLButtonElement;

  return btn;
}

/** Icon-only square button. */
export function IconButton(name: string, opts: Omit<ButtonOptions, "label" | "icon"> = {}): HTMLButtonElement {
  const btn = Button({ ...opts, icon: name });
  btn.classList.add("gtma-btn--icon");
  return btn;
}
