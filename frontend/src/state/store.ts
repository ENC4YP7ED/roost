import { signal, Signal } from "../core/reactive.ts";

/** Authenticated user, as returned by /api/client/account. */
export interface Account {
  id: number;
  uuid: string;
  admin: boolean;
  username: string;
  email: string;
  first_name: string;
  last_name: string;
  language: string;
  "2fa_enabled": boolean;
}

/** Hash-based routes: #/, #/server/abc123/console, #/account, #/admin/nodes… */
export type Route =
  | { kind: "dashboard" }
  | { kind: "account"; tab?: string }
  | { kind: "server"; id: string; tab: string }
  | { kind: "billing"; tab: string }
  | { kind: "admin"; section: string; id?: number };

function parseHash(): Route {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  switch (parts[0]) {
    case "server":
      if (parts[1]) return { kind: "server", id: parts[1], tab: parts[2] ?? "console" };
      break;
    case "account":
      return { kind: "account", tab: parts[1] };
    case "billing":
      return { kind: "billing", tab: parts[1] ?? "shop" };
    case "admin":
      return { kind: "admin", section: parts[1] ?? "overview", id: parts[2] ? Number(parts[2]) : undefined };
  }
  return { kind: "dashboard" };
}

export function routeHash(route: Route): string {
  switch (route.kind) {
    case "dashboard": return "#/";
    case "account": return `#/account${route.tab ? `/${route.tab}` : ""}`;
    case "billing": return `#/billing/${route.tab}`;
    case "server": return `#/server/${route.id}/${route.tab}`;
    case "admin": return `#/admin/${route.section}${route.id != null ? `/${route.id}` : ""}`;
  }
}

export function navigate(route: Route): void {
  const hash = routeHash(route);
  if (location.hash !== hash) location.hash = hash; // hashchange updates the signal
  else store.route.value = route;
}

/** User-tweakable preferences, persisted in localStorage (a Pelican-style
 *  nicety none of the PHP panels expose this cheaply). */
export interface Prefs {
  serverLayout: "grid" | "list";
  consoleFontSize: number;
}

function loadPrefs(): Prefs {
  try {
    return { serverLayout: "grid", consoleFontSize: 13, ...JSON.parse(localStorage.getItem("roost:prefs") ?? "{}") };
  } catch {
    return { serverLayout: "grid", consoleFontSize: 13 };
  }
}

export const store = {
  user: signal<Account | null>(null) as Signal<Account | null>,
  route: signal<Route>(parseHash()),
  prefs: signal<Prefs>(loadPrefs()),
  appName: signal<string>("Roost"),
};

export function savePrefs(update: Partial<Prefs>): void {
  store.prefs.value = { ...store.prefs.peek(), ...update };
  localStorage.setItem("roost:prefs", JSON.stringify(store.prefs.peek()));
}

window.addEventListener("hashchange", () => {
  store.route.value = parseHash();
});
