import { el, icon } from "../core/dom.ts";
import { openPopover, type PopoverHandle } from "../core/popover.ts";
import { signal } from "../core/reactive.ts";

export interface SelectOption {
  value: string;
  label: string;
  icon?: string;
}

export interface SelectOptions {
  options: SelectOption[];
  value?: string;
  placeholder?: string;
  size?: "sm" | "md";
  searchable?: boolean;
  onChange?: (value: string) => void;
}

export interface SelectHandle {
  el: HTMLElement;
  get value(): string;
  set value(v: string);
}

/** A fully custom select / combobox built on the popover layer. */
export function Select(opts: SelectOptions): SelectHandle {
  const value = signal(opts.value ?? opts.options[0]?.value ?? "");
  let popover: PopoverHandle | null = null;

  const labelOf = (v: string) => opts.options.find((o) => o.value === v)?.label ?? opts.placeholder ?? "Select…";
  const iconOf = (v: string) => opts.options.find((o) => o.value === v)?.icon;

  const trigger = el("button.gtma-select", {
    class: `gtma-select--${opts.size ?? "md"}`,
    attrs: { type: "button", "aria-haspopup": "listbox" },
    onclick: () => toggle(),
  },
    () => {
      const ic = iconOf(value.value);
      return el("span.gtma-select__value", {},
        ic ? icon(ic, { class: "gtma-select__value-icon" }) : null,
        el("span.truncate", {}, () => labelOf(value.value)),
      );
    },
    icon("chevron-down", { class: "gtma-select__chevron" }),
  );

  const toggle = () => {
    if (popover) { popover.close(); return; }
    trigger.classList.add("is-open");
    popover = openPopover({
      anchor: trigger,
      matchWidth: true,
      content: buildList(),
      onClose: () => { popover = null; trigger.classList.remove("is-open"); },
    });
  };

  function buildList(): HTMLElement {
    const filter = signal("");
    const list = el("div.gtma-select__menu", { attrs: { role: "listbox" } });

    const render = () => {
      list.replaceChildren();
      const q = filter.value.toLowerCase();
      const matches = opts.options.filter((o) => o.label.toLowerCase().includes(q));
      if (!matches.length) {
        list.appendChild(el("div.gtma-select__empty", {}, "No matches"));
        return;
      }
      for (const o of matches) {
        list.appendChild(el("button.gtma-select__option", {
          class: o.value === value.value ? "is-selected" : "",
          attrs: { type: "button", role: "option" },
          onclick: () => {
            value.value = o.value;
            opts.onChange?.(o.value);
            popover?.close();
          },
        },
          el("span.gtma-select__opt-icon", {}, o.icon ? icon(o.icon) : null),
          el("span.truncate", {}, o.label),
          o.value === value.value ? icon("check", { class: "gtma-select__check" }) : null,
        ));
      }
    };

    const panel = el("div.gtma-select__panel");
    if (opts.searchable) {
      const search = el("input.gtma-select__search", {
        attrs: { type: "search", placeholder: "Filter…", spellcheck: false },
        oninput: (e: Event) => { filter.value = (e.target as HTMLInputElement).value; render(); },
      });
      panel.appendChild(el("div.gtma-select__search-wrap", {}, icon("magnifying-glass"), search));
      queueMicrotask(() => search.focus());
    }
    panel.appendChild(list);
    render();
    return panel;
  }

  return {
    el: trigger,
    get value() { return value.value; },
    set value(v: string) { value.value = v; },
  };
}
