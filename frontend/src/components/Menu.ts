import { el, icon } from "../core/dom.ts";
import { openPopover, type PopoverHandle, type Placement } from "../core/popover.ts";

export interface MenuItem {
  label?: string;
  icon?: string;
  shortcut?: string;
  danger?: boolean;
  disabled?: boolean;
  checked?: boolean;
  separator?: boolean;
  header?: string;
  onSelect?: () => void;
  submenu?: MenuItem[];
}

/** Build the menu panel DOM (shared by dropdown menus and context menus). */
export function buildMenuPanel(items: MenuItem[], close: () => void): HTMLElement {
  const panel = el("div.rst-menu", { attrs: { role: "menu" } });

  for (const item of items) {
    if (item.separator) {
      panel.appendChild(el("div.rst-menu__sep"));
      continue;
    }
    if (item.header) {
      panel.appendChild(el("div.rst-menu__header", {}, item.header));
      continue;
    }

    const row = el("button.rst-menu__item", {
      class: [item.danger ? "rst-menu__item--danger" : "", item.disabled ? "rst-menu__item--disabled" : ""].join(" "),
      attrs: { type: "button", role: "menuitem", disabled: item.disabled ?? null },
      onclick: (e: MouseEvent) => {
        e.stopPropagation();
        if (item.disabled) return;
        if (item.submenu) return;
        close();
        item.onSelect?.();
      },
    },
      el("span.rst-menu__icon", {}, item.checked ? icon("check") : item.icon ? icon(item.icon) : null),
      el("span.rst-menu__label", {}, item.label ?? ""),
      item.shortcut ? el("span.rst-menu__shortcut.mono", {}, item.shortcut) : null,
      item.submenu ? el("span.rst-menu__chevron", {}, icon("chevron-right")) : null,
    );

    if (item.submenu) {
      let subHandle: PopoverHandle | null = null;
      const openSub = () => {
        if (subHandle) return;
        subHandle = openPopover({
          anchor: row,
          placement: "right-start",
          content: buildMenuPanel(item.submenu!, () => {
            subHandle?.close();
            close();
          }),
          onClose: () => { subHandle = null; },
        });
      };
      row.addEventListener("mouseenter", openSub);
    }

    panel.appendChild(row);
  }
  return panel;
}

/** Open a dropdown menu anchored to a trigger element. */
export function openMenu(
  anchor: HTMLElement,
  items: MenuItem[],
  placement: Placement = "bottom-start",
  onClose?: () => void,
): PopoverHandle {
  let handle: PopoverHandle;
  const panel = buildMenuPanel(items, () => handle.close());
  handle = openPopover({ anchor, content: panel, placement, onClose });
  return handle;
}

/** Attach a dropdown menu to a trigger button, toggling on click. */
export function attachMenu(trigger: HTMLElement, items: () => MenuItem[], placement: Placement = "bottom-start"): void {
  let open: PopoverHandle | null = null;
  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    if (open) {
      open.close();
      return;
    }
    trigger.classList.add("is-open");
    open = openMenu(trigger, items(), placement, () => {
      open = null;
      trigger.classList.remove("is-open");
    });
  });
}
