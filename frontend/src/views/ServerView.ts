import { el, icon } from "../core/dom.ts";
import { LoadingState, EmptyState, Badge } from "../components/misc.ts";
import { client } from "../api/client.ts";
import { navigate } from "../state/store.ts";
import { ServerConsole } from "./ServerConsole.ts";
import { ServerFiles } from "./ServerFiles.ts";
import {
  DatabasesTab, SchedulesTab, SubusersTab, BackupsTab,
  NetworkTab, StartupTab, SettingsTab, ServerActivityTab,
} from "./ServerFeatures.ts";

export interface ServerCtx {
  id: string;                 // short identifier used in URLs
  attrs: any;                 // client server attributes
  permissions: string[];      // acting user's permissions ("*" for owner/admin)
  can(perm: string): boolean;
}

const NAV: Array<{ id: string; label: string; icon: string; perm?: string }> = [
  { id: "console", label: "Console", icon: "terminal" },
  { id: "files", label: "Files", icon: "folder-open", perm: "file.read" },
  { id: "databases", label: "Databases", icon: "database", perm: "database.read" },
  { id: "schedules", label: "Schedules", icon: "calendar-days", perm: "schedule.read" },
  { id: "users", label: "Users", icon: "users", perm: "user.read" },
  { id: "backups", label: "Backups", icon: "box-archive", perm: "backup.read" },
  { id: "network", label: "Network", icon: "network-wired", perm: "allocation.read" },
  { id: "startup", label: "Startup", icon: "rocket", perm: "startup.read" },
  { id: "settings", label: "Settings", icon: "gear" },
  { id: "activity", label: "Activity", icon: "clock-rotate-left", perm: "activity.read" },
];

export function ServerView(id: string, tab: string): HTMLElement {
  const root = el("div.row.grow", { style: { alignItems: "stretch", minHeight: "0", gap: "0" } }, LoadingState("Loading server…"));

  client.server(id).then((res) => {
    const attrs = res.attributes as any;
    const perms: string[] = res.meta?.user_permissions ?? [];
    const ctx: ServerCtx = {
      id, attrs, permissions: perms,
      can: (p) => perms.includes("*") || perms.includes(p),
    };

    const nav = el("nav.rst-sidebar__nav");
    for (const item of NAV) {
      if (item.perm && !ctx.can(item.perm)) continue;
      nav.appendChild(el("button.rst-navitem", {
        class: item.id === tab ? "is-active" : "",
        onclick: () => navigate({ kind: "server", id, tab: item.id }),
      }, icon(item.icon), el("span", {}, item.label)));
    }

    const statusBadge = attrs.status
      ? Badge(String(attrs.status).replace("_", " "), attrs.status === "suspended" ? "danger" : "warning")
      : null;

    const sidebar = el("aside.rst-sidebar",
      el("div.rst-sidebar__head",
        el("button.rst-sidebar__home", { onclick: () => navigate({ kind: "dashboard" }) },
          icon("arrow-left"), el("span.truncate", {}, attrs.name), statusBadge,
        ),
      ),
      el("div.rst-sidebar__section", "Manage"),
      nav,
    );

    root.replaceChildren(sidebar, el("div.rst-content.grow", renderTab(ctx, tab)));
  }).catch((err) => {
    root.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Server unavailable", description: String((err as Error).message) }));
  });

  return root;
}

function renderTab(ctx: ServerCtx, tab: string): HTMLElement {
  switch (tab) {
    case "files": return ServerFiles(ctx);
    case "databases": return DatabasesTab(ctx);
    case "schedules": return SchedulesTab(ctx);
    case "users": return SubusersTab(ctx);
    case "backups": return BackupsTab(ctx);
    case "network": return NetworkTab(ctx);
    case "startup": return StartupTab(ctx);
    case "settings": return SettingsTab(ctx);
    case "activity": return ServerActivityTab(ctx);
    default: return ServerConsole(ctx);
  }
}
