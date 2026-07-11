import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { LoadingState } from "../components/misc.ts";
import { storefront, type Storefront } from "../api/client.ts";

/**
 * Public marketing landing page shown to logged-out visitors: a hero, the
 * catalogue of games we host, the hosting packages (fixed plans plus a
 * "build your own" configurator), and a call to action into sign-in / sign-up.
 */
export function LandingView(opts: { onSignIn: () => void; onRegister: () => void; onConfigure: (pkg: any) => void }): HTMLElement {
  const root = el("div.ptg-landing", LoadingState("Loading…"));

  storefront().then((data) => {
    root.replaceChildren(render(data, opts));
  }).catch(() => {
    // If the storefront can't load, fall straight to sign-in.
    opts.onSignIn();
  });

  return root;
}

function render(data: Storefront, opts: { onSignIn: () => void; onRegister: () => void; onConfigure: (pkg: any) => void }): HTMLElement {
  const nav = el("header.ptg-landing__nav",
    el("div.ptg-landing__brand", el("div.ptg-landing__logo", icon("feather-pointed")), el("span", {}, data.app_name)),
    el("div.row",
      data.registration_enabled ? Button({ label: "Create account", variant: "default", onClick: opts.onRegister }) : null,
      Button({ label: "Sign in", variant: "primary", icon: "right-to-bracket", onClick: opts.onSignIn }),
    ),
  );

  const hero = el("section.ptg-landing__hero",
    el("div.ptg-landing__grid"),
    el("div.ptg-landing__hero-inner",
      el("h1.ptg-landing__title", `Game servers, hosted in seconds.`),
      el("p.ptg-landing__sub", `Deploy ${data.games.length ? countGames(data) : "your favourite"} games on high-performance hardware. Instant setup, full control, pay only for what you need.`),
      el("div.row", { style: { justifyContent: "center", marginTop: "var(--sp-5)" } },
        Button({ label: "See plans", variant: "primary", icon: "arrow-down", size: "lg", onClick: () => document.querySelector(".ptg-landing__packages")?.scrollIntoView({ behavior: "smooth" }) }),
        Button({ label: "Browse games", variant: "default", size: "lg", onClick: () => document.querySelector(".ptg-landing__games")?.scrollIntoView({ behavior: "smooth" }) }),
      ),
    ),
  );

  const games = el("section.ptg-landing__games",
    sectionHead("Games we host", `${data.games.length} game platforms, ${totalEggs(data)} server types`),
    el("div.ptg-landing__game-grid",
      ...data.games.map((g: any) => el("div.ptg-landing__game",
        el("div.ptg-landing__game-icon", icon(gameIcon(g.name))),
        el("div.ptg-landing__game-name", g.name),
        el("div.ptg-landing__game-eggs.faint", (g.eggs ?? []).slice(0, 4).map((e: any) => e.name).join(" · ") || g.description),
      )),
    ),
  );

  const packages = el("section.ptg-landing__packages",
    sectionHead("Hosting packages", data.enabled ? "Pick a plan or build your own" : "Plans are coming soon"),
    data.enabled && data.packages.length
      ? el("div.ptg-landing__plan-grid", ...data.packages.map((p: any) => planCard(p, opts)))
      : el("p.faint", { style: { textAlign: "center" } }, "No packages are available yet — check back soon."),
  );

  const footer = el("footer.ptg-landing__footer.faint",
    icon("feather-pointed"), el("span", {}, `${data.app_name} · powered by Roost`),
  );

  return el("div.ptg-landing__page", nav, hero, games, packages, footer);
}

function planCard(p: any, opts: { onConfigure: (pkg: any) => void }): HTMLElement {
  const featured = p.configurable;
  return el("div.ptg-landing__plan", { class: featured ? "is-featured" : "" },
    featured ? el("div.ptg-landing__badge", "Configurable") : null,
    el("div.ptg-landing__plan-name", p.name),
    el("div.ptg-landing__plan-price",
      p.configurable
        ? [el("span.faint", "from "), el("span.ptg-landing__amount", p.price), el("span.faint", ` + ${p.price_per_gb}/GB`)]
        : [el("span.ptg-landing__amount", p.price), el("span.faint", ` ${p.interval_label}`)],
    ),
    p.description ? el("p.faint", p.description) : null,
    el("ul.ptg-landing__specs",
      p.configurable
        ? el("li", icon("sliders"), ` ${(p.min_memory/1024)|0}–${(p.max_memory/1024)|0} GB RAM, your choice`)
        : el("li", icon("memory"), ` ${(p.limits.memory/1024).toFixed(0)} GB RAM`),
      el("li", icon("hard-drive"), ` ${(p.limits.disk/1024).toFixed(0)} GB disk`),
      el("li", icon("gauge-high"), p.limits.cpu ? ` ${p.limits.cpu}% CPU` : " Shared CPU"),
      el("li", icon("database"), ` ${p.feature_limits.databases} databases`),
      el("li", icon("box-archive"), ` ${p.feature_limits.backups} backups`),
    ),
    Button({
      label: p.configurable ? "Configure & order" : "Order now",
      variant: featured ? "primary" : "default", icon: p.configurable ? "sliders" : "credit-card", block: true,
      onClick: () => opts.onConfigure(p),
    }),
  );
}

function sectionHead(title: string, sub: string): HTMLElement {
  return el("div.ptg-landing__section-head",
    el("h2.ptg-landing__section-title", title),
    el("p.faint", sub),
  );
}

function gameIcon(name: string): string {
  const n = name.toLowerCase();
  if (n.includes("minecraft")) return "cube";
  if (n.includes("rust")) return "gear";
  if (n.includes("source") || n.includes("counter")) return "crosshairs";
  if (n.includes("voice") || n.includes("mumble") || n.includes("teamspeak")) return "microphone";
  return "gamepad";
}

function countGames(data: Storefront): string {
  const names = data.games.map((g: any) => g.name).slice(0, 3).join(", ");
  return data.games.length > 3 ? `${names} and more` : names;
}

function totalEggs(data: Storefront): number {
  return data.games.reduce((n: number, g: any) => n + (g.eggs?.length ?? 0), 0);
}
