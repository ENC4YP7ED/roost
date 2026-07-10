import { el, icon, clear } from "../core/dom.ts";
import { attachContextMenu } from "./ContextMenu.ts";
import { type MenuItem } from "./Menu.ts";
import { Spinner } from "./misc.ts";

export interface TreeNode {
  id: string;
  label: string;
  icon?: string;
  iconOpen?: string;
  badge?: string | number;
  level: number;
  /** Lazy children loader; resolve [] for an empty expandable, omit for leaf. */
  loadChildren?: () => Promise<TreeNode[]>;
  onSelect?: () => void;
  contextMenu?: () => MenuItem[];
  meta?: unknown;
}

export interface TreeHandle {
  el: HTMLElement;
  setRoots(nodes: TreeNode[]): void;
  select(id: string | null): void;
  collapseAll(): void;
}

/** Lazy, virtual-free navigation tree (databases → tables). */
export function Tree(): TreeHandle {
  const list = el("div.gtma-tree", { attrs: { role: "tree" } });
  let selectedId: string | null = null;
  const rowEls = new Map<string, HTMLElement>();
  const expanded = new Set<string>();

  function setRoots(nodes: TreeNode[]) {
    clear(list);
    rowEls.clear();
    for (const n of nodes) list.appendChild(renderNode(n));
  }

  function renderNode(node: TreeNode): HTMLElement {
    const expandable = !!node.loadChildren;
    const wrapper = el("div.gtma-tree__node");
    const childRows = el("div.gtma-tree__children");
    let loaded = false;
    let loading = false;

    const chevron = expandable
      ? icon("chevron-right", { class: "gtma-tree__chevron" })
      : el("span.gtma-tree__chevron-spacer");

    const row = el("div.gtma-tree__row", {
      attrs: { role: "treeitem", tabindex: 0, "aria-expanded": expandable ? "false" : null },
      style: { paddingLeft: `${node.level * 14 + 8}px` },
      onclick: () => {
        select(node.id);
        node.onSelect?.();
        if (expandable) toggle();
      },
      onkeydown: (e: KeyboardEvent) => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); row.click(); }
        if (e.key === "ArrowRight" && expandable && !expanded.has(node.id)) toggle();
        if (e.key === "ArrowLeft" && expanded.has(node.id)) toggle();
      },
    },
      chevron,
      icon(node.icon ?? "circle", { class: "gtma-tree__icon" }),
      el("span.gtma-tree__label.truncate", {}, node.label),
      node.badge != null ? el("span.gtma-tree__badge", {}, String(node.badge)) : null,
    );

    rowEls.set(node.id, row);
    if (node.contextMenu) attachContextMenu(row, () => node.contextMenu!());

    async function toggle() {
      const isOpen = expanded.has(node.id);
      if (isOpen) {
        expanded.delete(node.id);
        wrapper.classList.remove("is-open");
        row.setAttribute("aria-expanded", "false");
        return;
      }
      expanded.add(node.id);
      wrapper.classList.add("is-open");
      row.setAttribute("aria-expanded", "true");
      if (node.iconOpen) {
        const ic = row.querySelector(".gtma-tree__icon");
        ic?.replaceWith(icon(node.iconOpen, { class: "gtma-tree__icon" }));
      }
      if (!loaded && !loading) {
        loading = true;
        childRowsSetLoading(childRows);
        try {
          const kids = await node.loadChildren!();
          clear(childRows);
          if (!kids.length) {
            childRows.appendChild(el("div.gtma-tree__empty", { style: { paddingLeft: `${(node.level + 1) * 14 + 8}px` } }, "empty"));
          }
          for (const k of kids) childRows.appendChild(renderNode(k));
          loaded = true;
        } catch (err) {
          clear(childRows);
          childRows.appendChild(el("div.gtma-tree__error", { style: { paddingLeft: `${(node.level + 1) * 14 + 8}px` } }, String(err)));
        } finally {
          loading = false;
        }
      }
    }

    wrapper.append(row, childRows);
    return wrapper;
  }

  function childRowsSetLoading(childRows: HTMLElement) {
    clear(childRows);
    childRows.appendChild(el("div.gtma-tree__loading", {}, Spinner(13), el("span.faint", {}, "loading…")));
  }

  function select(id: string | null) {
    if (selectedId && rowEls.has(selectedId)) rowEls.get(selectedId)!.classList.remove("is-selected");
    selectedId = id;
    if (id && rowEls.has(id)) rowEls.get(id)!.classList.add("is-selected");
  }

  return {
    el: list,
    setRoots,
    select,
    collapseAll: () => {
      expanded.clear();
      list.querySelectorAll(".gtma-tree__node.is-open").forEach((n) => n.classList.remove("is-open"));
    },
  };
}
