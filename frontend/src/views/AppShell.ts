import { el, icon, clear } from "../core/dom.ts";
import { effect } from "../core/reactive.ts";
import { attachMenu } from "../components/Menu.ts";
import { auth } from "../api/client.ts";
import { store, navigate, type Route } from "../state/store.ts";
import { DashboardView } from "./DashboardView.ts";
import { AccountView } from "./AccountView.ts";
import { ServerView } from "./ServerView.ts";
import { AdminView } from "./AdminView.ts";
import { BillingView } from "./BillingView.ts";

/** Topbar + routed content. Server & admin views bring their own sidebars. */
export function AppShell(onLogout: () => void): HTMLElement {
  const user = store.user.peek()!;

  const adminBtn = user.admin
    ? el("button.rst-topbar__action", { onclick: () => navigate({ kind: "admin", section: "overview" }) },
        icon("screwdriver-wrench"), el("span", {}, "Admin"))
    : null;

  const userMenu = el("button.rst-topbar__conn", {},
    icon("circle", { class: "rst-topbar__dot" }),
    el("span.mono.truncate", user.username),
    icon("chevron-down", { class: "faint" }),
  );
  attachMenu(userMenu, () => [
    { header: user.email },
    { label: "Account settings", icon: "user-gear", onSelect: () => navigate({ kind: "account" }) },
    { label: "API keys", icon: "key", onSelect: () => navigate({ kind: "account", tab: "api" }) },
    { separator: true },
    { label: "Sign out", icon: "right-from-bracket", danger: true, onSelect: async () => { await auth.logout(); onLogout(); } },
  ], "bottom-end");

  const billingBtn = el("button.rst-topbar__action", { onclick: () => navigate({ kind: "billing", tab: "shop" }) },
    icon("cart-shopping"), el("span", {}, "Billing"));

  const topbar = el("header.rst-topbar",
    el("button.rst-topbar__brand", { onclick: () => navigate({ kind: "dashboard" }) },
      el("div.rst-topbar__logo", icon("feather-pointed")),
      el("span.rst-topbar__name", store.appName),
    ),
    Breadcrumb(),
    el("span.spacer"),
    billingBtn,
    adminBtn,
    userMenu,
  );

  const content = el("main.rst-content.grow");
  effect(() => {
    const route = store.route.value;
    clear(content);
    content.appendChild(renderRoute(route));
  });

  return el("div.rst-shell.col.grow", topbar, el("div.rst-body.grow", content));
}

function renderRoute(route: Route): HTMLElement {
  switch (route.kind) {
    case "dashboard": return DashboardView();
    case "account": return AccountView(route.tab);
    case "billing": return BillingView(route.tab);
    case "server": return ServerView(route.id, route.tab);
    case "admin": return AdminView(route.section, route.id);
  }
}

function Breadcrumb(): HTMLElement {
  const crumb = el("nav.rst-breadcrumb");
  effect(() => {
    const route = store.route.value;
    clear(crumb);
    const part = (label: string, ic: string, onClick?: () => void) =>
      el(onClick ? "button.rst-breadcrumb__item" : "span.rst-breadcrumb__item", { onclick: onClick },
        icon(ic, { class: "rst-breadcrumb__icon" }), el("span", {}, label));
    const sep = () => icon("angle-right", { class: "rst-breadcrumb__sep" });

    crumb.appendChild(part("Servers", "house", () => navigate({ kind: "dashboard" })));
    if (route.kind === "server") {
      crumb.append(sep(), part(route.id, "server", () => navigate({ kind: "server", id: route.id, tab: "console" })));
      crumb.append(sep(), part(route.tab, "angles-right"));
    }
    if (route.kind === "account") crumb.append(sep(), part("Account", "user-gear"));
    if (route.kind === "billing") crumb.append(sep(), part("Billing", "cart-shopping"));
    if (route.kind === "admin") {
      crumb.append(sep(), part("Admin", "screwdriver-wrench", () => navigate({ kind: "admin", section: "overview" })));
      crumb.append(sep(), part(route.section, "angles-right"));
    }
  });
  return crumb;
}
