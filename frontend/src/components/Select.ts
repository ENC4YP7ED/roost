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

  const trigger = el("button.rst-select", {
    class: `rst-select--${opts.size ?? "md"}`,
    attrs: { type: "button", "aria-haspopup": "listbox" },
    onclick: () => toggle(),
  },
    () => {
      const ic = iconOf(value.value);
      return el("span.rst-select__value", {},
        ic ? icon(ic, { class: "rst-select__value-icon" }) : null,
        el("span.truncate", {}, () => labelOf(value.value)),
      );
    },
    icon("chevron-down", { class: "rst-select__chevron" }),
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
    const list = el("div.rst-select__menu", { attrs: { role: "listbox" } });

    const render = () => {
      list.replaceChildren();
      const q = filter.value.toLowerCase();
      const matches = opts.options.filter((o) => o.label.toLowerCase().includes(q));
      if (!matches.length) {
        list.appendChild(el("div.rst-select__empty", {}, "No matches"));
        return;
      }
      for (const o of matches) {
        list.appendChild(el("button.rst-select__option", {
          class: o.value === value.value ? "is-selected" : "",
          attrs: { type: "button", role: "option" },
          onclick: () => {
            value.value = o.value;
            opts.onChange?.(o.value);
            popover?.close();
          },
        },
          el("span.rst-select__opt-icon", {}, o.icon ? icon(o.icon) : null),
          el("span.truncate", {}, o.label),
          o.value === value.value ? icon("check", { class: "rst-select__check" }) : null,
        ));
      }
    };

    const panel = el("div.rst-select__panel");
    if (opts.searchable) {
      const search = el("input.rst-select__search", {
        attrs: { type: "search", placeholder: "Filter…", spellcheck: false },
        oninput: (e: Event) => { filter.value = (e.target as HTMLInputElement).value; render(); },
      });
      panel.appendChild(el("div.rst-select__search-wrap", {}, icon("magnifying-glass"), search));
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
