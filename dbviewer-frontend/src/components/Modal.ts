import { el, icon, type Child } from "../core/dom.ts";
import { Button, type ButtonVariant } from "./Button.ts";

export interface ModalAction {
  label: string;
  variant?: ButtonVariant;
  icon?: string;
  onClick?: () => void | Promise<void>;
  closeOnClick?: boolean;
}

export interface ModalOptions {
  title: string;
  icon?: string;
  body: Child;
  actions?: ModalAction[];
  width?: number;
  onClose?: () => void;
}

export interface ModalHandle {
  close(): void;
  el: HTMLElement;
}

/** Open a modal dialog. Returns a handle to programmatically close it. */
export function openModal(opts: ModalOptions): ModalHandle {
  const overlay = el("div.gtma-modal__overlay");

  const close = () => {
    overlay.classList.add("is-closing");
    setTimeout(() => { overlay.remove(); opts.onClose?.(); }, 140);
    document.removeEventListener("keydown", onKey, true);
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") { e.stopPropagation(); close(); }
  };
  document.addEventListener("keydown", onKey, true);

  const footer = opts.actions?.length
    ? el("div.gtma-modal__footer", {}, ...opts.actions.map((a) =>
        Button({
          label: a.label,
          icon: a.icon,
          variant: a.variant ?? "default",
          onClick: async () => {
            await a.onClick?.();
            if (a.closeOnClick !== false) close();
          },
        })))
    : null;

  const dialog = el("div.gtma-modal", {
    style: { width: opts.width ? `${opts.width}px` : "" },
    onclick: (e: MouseEvent) => e.stopPropagation(),
  },
    el("div.gtma-modal__header", {},
      opts.icon ? icon(opts.icon, { class: "gtma-modal__title-icon" }) : null,
      el("div.gtma-modal__title", {}, opts.title),
      el("button.gtma-modal__close", { attrs: { type: "button", "aria-label": "Close" }, onclick: close }, icon("xmark")),
    ),
    el("div.gtma-modal__body", {}, opts.body),
    footer,
  );

  overlay.appendChild(dialog);
  overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) close(); });
  document.body.appendChild(overlay);

  return { close, el: dialog };
}

/** Convenience confirm dialog returning a promise. */
export function confirmModal(opts: { title: string; message: Child; confirmLabel?: string; danger?: boolean; icon?: string }): Promise<boolean> {
  return new Promise((resolve) => {
    let result = false;
    openModal({
      title: opts.title,
      icon: opts.icon ?? (opts.danger ? "triangle-exclamation" : "circle-question"),
      body: el("div.gtma-confirm", {}, opts.message),
      actions: [
        { label: "Cancel", variant: "ghost", onClick: () => { result = false; } },
        {
          label: opts.confirmLabel ?? "Confirm",
          variant: opts.danger ? "danger" : "primary",
          onClick: () => { result = true; },
        },
      ],
      onClose: () => resolve(result),
    });
  });
}
