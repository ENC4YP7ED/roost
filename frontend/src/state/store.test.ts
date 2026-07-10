import { describe, it, expect, beforeEach, vi } from "vitest";
import { routeHash, type Route } from "./store.ts";

describe("routeHash", () => {
  const cases: Array<[Route, string]> = [
    [{ kind: "dashboard" }, "#/"],
    [{ kind: "account" }, "#/account"],
    [{ kind: "account", tab: "api" }, "#/account/api"],
    [{ kind: "server", id: "abc12345", tab: "console" }, "#/server/abc12345/console"],
    [{ kind: "server", id: "abc12345", tab: "files" }, "#/server/abc12345/files"],
    [{ kind: "admin", section: "nodes" }, "#/admin/nodes"],
    [{ kind: "admin", section: "nodes", id: 3 }, "#/admin/nodes/3"],
  ];

  it.each(cases)("serialises %o", (route, want) => {
    expect(routeHash(route)).toBe(want);
  });
});

// parseHash is module-private, but it runs on import and on hashchange, so we
// exercise it through the exported store by driving location.hash.
describe("hash parsing", () => {
  beforeEach(() => {
    location.hash = "";
  });

  async function parse(hash: string) {
    // parseHash() runs at import time, so reset the module registry and
    // re-import against the hash we just set.
    location.hash = hash;
    vi.resetModules();
    const mod = await import("./store.ts");
    return mod.store.route.peek();
  }

  it("defaults to the dashboard", async () => {
    expect(await parse("")).toEqual({ kind: "dashboard" });
    expect(await parse("#/")).toEqual({ kind: "dashboard" });
  });

  it("parses server routes and defaults the tab", async () => {
    expect(await parse("#/server/abc12345/files")).toEqual({
      kind: "server", id: "abc12345", tab: "files",
    });
    expect(await parse("#/server/abc12345")).toEqual({
      kind: "server", id: "abc12345", tab: "console",
    });
  });

  it("falls back to the dashboard for a server route without an id", async () => {
    expect(await parse("#/server")).toEqual({ kind: "dashboard" });
  });

  it("parses account routes", async () => {
    expect(await parse("#/account")).toEqual({ kind: "account", tab: undefined });
    expect(await parse("#/account/ssh")).toEqual({ kind: "account", tab: "ssh" });
  });

  it("parses admin routes with an optional numeric id", async () => {
    expect(await parse("#/admin")).toEqual({ kind: "admin", section: "overview", id: undefined });
    expect(await parse("#/admin/nodes")).toEqual({ kind: "admin", section: "nodes", id: undefined });
    expect(await parse("#/admin/nodes/12")).toEqual({ kind: "admin", section: "nodes", id: 12 });
  });

  it("ignores unknown prefixes", async () => {
    expect(await parse("#/totally-unknown")).toEqual({ kind: "dashboard" });
  });

  it("round-trips every route through routeHash", async () => {
    const routes: Route[] = [
      { kind: "dashboard" },
      { kind: "account", tab: "api" },
      { kind: "server", id: "xyz", tab: "backups" },
      { kind: "admin", section: "eggs", id: 4 },
    ];
    for (const route of routes) {
      expect(await parse(routeHash(route))).toEqual(route);
    }
  });
});
