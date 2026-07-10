import { el, icon, clear } from "../core/dom.ts";

export interface TabDef {
  id: string;
  label: string;
  icon?: string;
  badge?: string | number;
  render: () => HTMLElement;
}

export interface TabsHandle {
  el: HTMLElement;
  select(id: string): void;
  get active(): string;
}

/** Tabbed container that lazily renders panels and caches them. */
export function Tabs(tabs: TabDef[], opts: { active?: string; onSelect?: (id: string) => void } = {}): TabsHandle {
  let active = opts.active ?? tabs[0]?.id ?? "";
  const cache = new Map<string, HTMLElement>();
  const tabList = el("div.gtma-tabs__list", { attrs: { role: "tablist" } });
  const panel = el("div.gtma-tabs__panel.grow");
  const buttons = new Map<string, HTMLButtonElement>();

  function select(id: string) {
    active = id;
    for (const [tid, btn] of buttons) btn.classList.toggle("is-active", tid === id);
    const tab = tabs.find((t) => t.id === id);
    if (!tab) return;
    let view = cache.get(id);
    if (!view) { view = tab.render(); cache.set(id, view); }
    clear(panel);
    panel.appendChild(view);
    opts.onSelect?.(id);
  }

  for (const tab of tabs) {
    const btn = el("button.gtma-tabs__tab", {
      attrs: { type: "button", role: "tab" },
      onclick: () => select(tab.id),
    },
      tab.icon ? icon(tab.icon) : null,
      el("span", {}, tab.label),
      tab.badge != null ? el("span.gtma-tabs__badge", {}, String(tab.badge)) : null,
    ) as HTMLButtonElement;
    buttons.set(tab.id, btn);
    tabList.appendChild(btn);
  }

  const root = el("div.gtma-tabs.col.grow", {}, tabList, panel);
  if (active) select(active);

  return { el: root, select, get active() { return active; } };
}
