import { el, icon, clear } from "../core/dom.ts";
import { effect } from "../core/reactive.ts";
import { IconButton } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { Tree, type TreeNode } from "../components/Tree.ts";
import { Scroller } from "../components/Scroller.ts";
import { attachMenu, type MenuItem } from "../components/Menu.ts";
import { tooltip } from "../components/Tooltip.ts";
import { notify } from "../components/Toast.ts";
import { Spinner } from "../components/misc.ts";
import { api } from "../api/client.ts";
import { store, navigate, refreshTree, type Route } from "../state/store.ts";
import { formatNumber } from "../util/format.ts";
import { HomeView } from "./HomeView.ts";
import { DatabaseView } from "./DatabaseView.ts";
import { TableView } from "./TableView.ts";
import { SqlConsole } from "./SqlConsole.ts";
import { UsersView } from "./UsersView.ts";

/** The connected application: topbar + navigation sidebar + content router. */
export function AppShell(onDisconnect: () => void): HTMLElement {
  // ---- sidebar tree ----
  const tree = Tree();
  const treeScroller = Scroller(tree.el, { class: "gtma-sidebar__scroller" });
  let filterText = "";

  async function loadTree() {
    tree.el.replaceChildren(el("div.gtma-sidebar__loading", Spinner(14), el("span.faint", {}, "loading schemas…")));
    try {
      const dbs = await api.databases();
      const visible = dbs.filter((d) => !filterText || d.name.toLowerCase().includes(filterText));
      const roots: TreeNode[] = visible.map((d) => ({
        id: `db:${d.name}`,
        label: d.name,
        icon: "database",
        iconOpen: "database",
        badge: d.tables,
        level: 0,
        onSelect: () => navigate({ kind: "database", database: d.name }),
        contextMenu: () => dbMenu(d.name),
        loadChildren: async () => {
          const tables = await api.tables(d.name);
          return tables.map<TreeNode>((t) => ({
            id: `tbl:${d.name}.${t.name}`,
            label: t.name,
            icon: t.type === "VIEW" ? "eye" : "table",
            badge: t.rows ? formatNumber(t.rows) : undefined,
            level: 1,
            onSelect: () => navigate({ kind: "table", database: d.name, table: t.name }),
            contextMenu: () => tableMenu(d.name, t.name),
          }));
        },
      }));
      tree.setRoots(roots);
      if (!roots.length) {
        tree.el.appendChild(el("div.gtma-sidebar__empty.faint", filterText ? "No matches" : "No databases"));
      }
      treeScroller.refresh();
    } catch (err) {
      tree.el.replaceChildren(el("div.gtma-sidebar__error", String(err)));
    }
  }

  function dbMenu(name: string): MenuItem[] {
    return [
      { header: name },
      { label: "Open", icon: "folder-open", onSelect: () => navigate({ kind: "database", database: name }) },
      { label: "SQL console", icon: "terminal", onSelect: () => navigate({ kind: "sql", database: name }) },
      { separator: true },
      { label: "Copy name", icon: "copy", onSelect: () => copy(name) },
    ];
  }

  function tableMenu(db: string, table: string): MenuItem[] {
    return [
      { header: `${db}.${table}` },
      { label: "Browse", icon: "table-list", onSelect: () => navigate({ kind: "table", database: db, table }) },
      { label: "Structure", icon: "diagram-project", onSelect: () => navigate({ kind: "table", database: db, table }) },
      { separator: true },
      { label: "Copy name", icon: "copy", onSelect: () => copy(table) },
    ];
  }

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); notify.success("Copied"); }
    catch { notify.error("Clipboard unavailable"); }
  }

  const search = TextInput({
    placeholder: "Filter databases…",
    icon: "magnifying-glass",
    size: "sm",
    clearable: true,
    onInput: (v) => { filterText = v.trim().toLowerCase(); loadTree(); },
  });

  const refreshBtn = IconButton("rotate", { size: "sm", variant: "ghost", onClick: () => refreshTree() });
  tooltip(refreshBtn, "Refresh");
  const collapseBtn = IconButton("compress", { size: "sm", variant: "ghost", onClick: () => tree.collapseAll() });
  tooltip(collapseBtn, "Collapse all");

  const sidebar = el("aside.gtma-sidebar",
    el("div.gtma-sidebar__head",
      el("button.gtma-sidebar__home", { onclick: () => navigate({ kind: "home" }) },
        icon("server"), el("span.truncate", {}, store.server),
      ),
    ),
    el("div.gtma-sidebar__tools",
      el("div.grow", search.el),
      refreshBtn,
      collapseBtn,
    ),
    treeScroller.el,
  );

  // Reload tree on version bumps.
  effect(() => { store.treeVersion.value; loadTree(); });

  // ---- topbar ----
  const connMenu = el("button.gtma-topbar__conn", {},
    icon("circle", { class: "gtma-topbar__dot" }),
    el("span.mono.truncate", store.user),
    el("span.faint", {}, "@"),
    el("span.mono.truncate", store.server),
    icon("chevron-down", { class: "faint" }),
  );
  attachMenu(connMenu, () => [
    { header: "Connection" },
    { label: store.server.value, icon: "server", disabled: true },
    { label: `User: ${store.user.value}`, icon: "user", disabled: true },
    { separator: true },
    { label: "Users & privileges", icon: "users", onSelect: () => navigate({ kind: "users" }) },
    { label: "Refresh schemas", icon: "rotate", onSelect: () => refreshTree() },
    { label: "Disconnect", icon: "right-from-bracket", danger: true, onSelect: async () => { await api.disconnect(); onDisconnect(); } },
  ], "bottom-end");

  const topbar = el("header.gtma-topbar",
    el("a.gtma-topbar__action", { href: "/#/admin/overview", attrs: { title: "Back to the Roost panel" } },
      icon("arrow-left"), el("span", {}, "Panel"),
    ),
    el("div.gtma-topbar__brand", { onclick: () => navigate({ kind: "home" }) },
      el("div.gtma-topbar__logo", icon("database")),
      el("span.gtma-topbar__name", "Database Viewer"),
    ),
    Breadcrumb(),
    el("span.spacer"),
    el("button.gtma-topbar__action", { onclick: () => navigate({ kind: "users" }) }, icon("users"), el("span", {}, "Users")),
    el("button.gtma-topbar__action", { onclick: () => navigate({ kind: "sql" }) }, icon("terminal"), el("span", {}, "SQL")),
    connMenu,
  );

  // ---- content router ----
  const content = el("main.gtma-content.grow");
  effect(() => {
    const route = store.route.value;
    clear(content);
    content.appendChild(renderRoute(route));
  });

  return el("div.gtma-shell.col.grow", topbar, el("div.gtma-body.grow", sidebar, content));
}

function renderRoute(route: Route): HTMLElement {
  switch (route.kind) {
    case "home": return HomeView();
    case "database": return DatabaseView(route.database);
    case "table": return TableView(route.database, route.table, route.filter);
    case "sql": return SqlConsole(route.database ? { database: route.database } : {});
    case "users": return UsersView();
  }
}

/** Reactive breadcrumb reflecting the current route. */
function Breadcrumb(): HTMLElement {
  const crumb = el("nav.gtma-breadcrumb");
  effect(() => {
    const route = store.route.value;
    clear(crumb);
    const part = (label: string, ic: string, onClick?: () => void) =>
      el(onClick ? "button.gtma-breadcrumb__item" : "span.gtma-breadcrumb__item", { onclick: onClick },
        icon(ic, { class: "gtma-breadcrumb__icon" }), el("span", {}, label));
    const sep = () => icon("angle-right", { class: "gtma-breadcrumb__sep" });

    crumb.appendChild(part("Home", "house", () => navigate({ kind: "home" })));
    if (route.kind === "database" || route.kind === "table") {
      crumb.append(sep(), part(route.database, "database", () => navigate({ kind: "database", database: route.database })));
    }
    if (route.kind === "table") {
      crumb.append(sep(), part(route.table, "table"));
    }
    if (route.kind === "sql") {
      crumb.append(sep(), part(route.database ? `SQL · ${route.database}` : "SQL", "terminal"));
    }
    if (route.kind === "users") {
      crumb.append(sep(), part("Users & privileges", "users"));
    }
  });
  return crumb;
}
