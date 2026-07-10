import { el, icon } from "../core/dom.ts";

export interface TextInputOptions {
  value?: string;
  placeholder?: string;
  type?: "text" | "password" | "number" | "search" | "email";
  inputMode?: "text" | "numeric" | "decimal" | "tel" | "email" | "url" | "search";
  icon?: string;
  label?: string;
  hint?: string;
  size?: "sm" | "md";
  disabled?: boolean;
  autofocus?: boolean;
  clearable?: boolean;
  onInput?: (value: string) => void;
  onEnter?: (value: string) => void;
  onChange?: (value: string) => void;
}

export interface TextInputHandle {
  el: HTMLElement;
  input: HTMLInputElement;
  get value(): string;
  set value(v: string);
  focus(): void;
  setError(message: string | null): void;
}

/** Custom text input with optional leading icon, clear button and error state. */
export function TextInput(opts: TextInputOptions): TextInputHandle {
  let toggleVisible: HTMLElement | null = null;
  const input = el("input.gtma-input__field", {
    attrs: {
      type: opts.type ?? "text",
      inputmode: opts.inputMode ?? null,
      placeholder: opts.placeholder ?? "",
      spellcheck: false,
      autocomplete: opts.type === "password" ? "current-password" : "off",
    },
    value: opts.value ?? "",
    disabled: opts.disabled ?? false,
  }) as HTMLInputElement;

  const clearBtn = el("button.gtma-input__clear", {
    attrs: { type: "button", tabindex: -1, "aria-label": "Clear" },
    class: opts.clearable ? "" : "hidden",
    onclick: () => {
      input.value = "";
      opts.onInput?.("");
      input.focus();
      updateClear();
    },
  }, icon("xmark"));

  function updateClear() {
    if (opts.clearable) clearBtn.classList.toggle("hidden", input.value.length === 0);
  }

  input.addEventListener("input", () => {
    opts.onInput?.(input.value);
    updateClear();
  });
  input.addEventListener("change", () => opts.onChange?.(input.value));
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") opts.onEnter?.(input.value);
  });

  const wrap = el("div.gtma-input__wrap", {
    class: `gtma-input--${opts.size ?? "md"}`,
  },
    opts.icon ? icon(opts.icon, { class: "gtma-input__icon" }) : null,
    input,
    opts.clearable ? clearBtn : null,
    opts.type === "password" ? (toggleVisible = el("button.gtma-input__toggle", {
      attrs: { type: "button", tabindex: -1, "aria-label": "Toggle visibility" },
      onclick: () => {
        const showing = input.getAttribute("type") === "text";
        input.setAttribute("type", showing ? "password" : "text");
        toggleVisible!.replaceChildren(icon(showing ? "eye" : "eye-slash"));
      },
    }, icon("eye"))) : null,
  );

  const errorEl = el("div.gtma-input__error.hidden");
  const root = el("div.gtma-input", {},
    opts.label ? el("label.gtma-input__label", {}, opts.label) : null,
    wrap,
    opts.hint ? el("div.gtma-input__hint", {}, opts.hint) : null,
    errorEl,
  );

  if (opts.autofocus) queueMicrotask(() => input.focus());
  updateClear();

  return {
    el: root,
    input,
    get value() {
      return input.value;
    },
    set value(v: string) {
      input.value = v;
      updateClear();
    },
    focus: () => input.focus(),
    setError(message: string | null) {
      wrap.classList.toggle("gtma-input--error", !!message);
      errorEl.textContent = message ?? "";
      errorEl.classList.toggle("hidden", !message);
    },
  };
}

/** Multi-line variant. */
export function TextArea(opts: { value?: string; placeholder?: string; rows?: number; mono?: boolean; onInput?: (v: string) => void }): HTMLTextAreaElement {
  const ta = el("textarea.gtma-textarea", {
    class: opts.mono ? "mono" : "",
    attrs: { rows: opts.rows ?? 4, placeholder: opts.placeholder ?? "", spellcheck: false },
    value: opts.value ?? "",
    oninput: (e: Event) => opts.onInput?.((e.target as HTMLTextAreaElement).value),
  }) as HTMLTextAreaElement;
  return ta;
}
