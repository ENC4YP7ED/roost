import { signal } from "../core/reactive.ts";
import type { ServerInfo, SearchCondition } from "../api/types.ts";

/** What the main content area is currently showing. */
export type Route =
  | { kind: "home" }
  | { kind: "sql"; database?: string }
  | { kind: "users" }
  | { kind: "database"; database: string }
  | { kind: "table"; database: string; table: string; filter?: SearchCondition[] };

export const store = {
  connected: signal(false),
  server: signal(""),
  user: signal(""),
  serverInfo: signal<ServerInfo | null>(null),
  route: signal<Route>({ kind: "home" }),
  /** Bumped whenever the sidebar tree should reload (e.g. after DDL). */
  treeVersion: signal(0),
};

export function navigate(route: Route): void {
  store.route.value = route;
}

export function refreshTree(): void {
  store.treeVersion.value++;
}
