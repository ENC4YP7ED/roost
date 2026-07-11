import { el, icon } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { Select } from "../components/Select.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge, Switch } from "../components/misc.ts";
import { admin, unwrap } from "../api/client.ts";
import { navigate, store } from "../state/store.ts";
import { page, statTile, localTime } from "./shared.ts";
import { AdminNodes, AdminServers, AdminEggs } from "./AdminInfra.ts";
import { AdminBilling, AdminProducts, AdminOrders } from "./AdminBilling.ts";

const SECTIONS: Array<{ id: string; label: string; icon: string }> = [
  { id: "overview", label: "Overview", icon: "gauge" },
  { id: "settings", label: "Settings", icon: "sliders" },
  { id: "security", label: "Security", icon: "shield-halved" },
  { id: "billing", label: "Billing", icon: "money-bill" },
  { id: "products", label: "Plans", icon: "box" },
  { id: "orders", label: "Orders & invoices", icon: "receipt" },
  { id: "webhooks", label: "Webhooks", icon: "satellite-dish" },
  { id: "locations", label: "Locations", icon: "map-pin" },
  { id: "nodes", label: "Nodes", icon: "hard-drive" },
  { id: "servers", label: "Servers", icon: "server" },
  { id: "users", label: "Users", icon: "users" },
  { id: "eggs", label: "Nests & Eggs", icon: "egg" },
  { id: "databases", label: "Database Hosts", icon: "database" },
  { id: "mounts", label: "Mounts", icon: "folder-tree" },
  { id: "dbviewer", label: "Database Viewer", icon: "table-columns" },
];

export function AdminView(section: string, id?: number): HTMLElement {
  const nav = el("nav.rst-sidebar__nav");
  for (const s of SECTIONS) {
    // The database viewer is a separate SPA served by the same binary.
    const onclick = s.id === "dbviewer"
      ? () => { location.href = "/dbviewer/"; }
      : () => navigate({ kind: "admin", section: s.id });
    nav.appendChild(el("button.rst-navitem", {
      class: s.id === section ? "is-active" : "",
      onclick,
    }, icon(s.icon), el("span", {}, s.label),
      s.id === "dbviewer" ? icon("arrow-up-right-from-square", { class: "rst-navitem__badge faint" }) : null));
  }
  const sidebar = el("aside.rst-sidebar",
    el("div.rst-sidebar__head",
      el("button.rst-sidebar__home", { onclick: () => navigate({ kind: "dashboard" }) },
        icon("arrow-left"), el("span", {}, "Back to servers")),
    ),
    el("div.rst-sidebar__section", "Administration"),
    nav,
  );
  return el("div.row.grow", { style: { alignItems: "stretch", minHeight: "0", gap: "0" } },
    sidebar,
    el("div.rst-content.grow", renderSection(section, id)),
  );
}

function renderSection(section: string, id?: number): HTMLElement {
  switch (section) {
    case "settings": return AdminSettings();
    case "security": return AdminSecurity();
    case "billing": return AdminBilling();
    case "products": return AdminProducts();
    case "orders": return AdminOrders();
    case "webhooks": return AdminWebhooks();
    case "locations": return AdminLocations();
    case "nodes": return AdminNodes(id);
    case "servers": return AdminServers(id);
    case "users": return AdminUsers();
    case "eggs": return AdminEggs(id);
    case "databases": return AdminDatabaseHosts();
    case "mounts": return AdminMounts();
    default: return AdminOverview();
  }
}

// ---------------------------------------------------------------- overview

function AdminOverview(): HTMLElement {
  const stats = el("div.rst-stats", LoadingState());
  admin.overview().then((o: any) => {
    stats.replaceChildren(
      statTile("Users", String(o.users), "users"),
      statTile("Servers", String(o.servers), "server"),
      statTile("Nodes", String(o.nodes), "hard-drive"),
      statTile("Locations", String(o.locations), "map-pin"),
    );
  });
  return page("Overview", { icon: "gauge", sub: "Roost — one binary, one SQLite file, wings-compatible." },
    stats,
    el("div.rst-card",
      el("div.rst-card__title", icon("circle-info"), "About this panel"),
      el("p.faint", "Roost is a ground-up Go + TypeScript successor to Pterodactyl, Pelican and Pyrodactyl. It speaks the same client/application/remote APIs, so existing Wings daemons, billing panels and API integrations keep working — without PHP, MySQL, Redis or a queue worker."),
    ),
  );
}

// ---------------------------------------------------------------- settings

function AdminSettings(): HTMLElement {
  const body = el("div", LoadingState());
  admin.settings().then((s) => {
    const name = TextInput({ label: "Panel name", value: s["app:name"] ?? "Roost" });
    const url = TextInput({ label: "Panel URL", value: s["app:url"] ?? "", hint: "Public URL wings uses to reach this panel (also embedded in signed tokens)." });
    let registration = Boolean(s["registration:enabled"]);
    body.replaceChildren(el("div.rst-card",
      el("div.rst-card__title", icon("sliders"), "Branding & connectivity"),
      el("div.rst-form__row", name.el, url.el),
      el("div.rst-card__title", { style: { marginTop: "var(--sp-5)" } }, icon("user-plus"), "Registration"),
      Switch({ checked: registration, label: "Allow public self-registration (a “Create account” option on the sign-in page)", onChange: (v) => { registration = v; } }),
      el("div.row", { style: { marginTop: "var(--sp-4)" } }, Button({
        label: "Save settings", variant: "primary", icon: "check",
        onClick: async () => {
          await admin.saveSettings({ "app:name": name.value, "app:url": url.value, "registration:enabled": registration ? "1" : "0" });
          store.appName.value = name.value;
          notify.success("Settings saved");
        },
      })),
    ));
  });
  return page("Settings", { icon: "sliders" }, body);
}


// ---------------------------------------------------------------- security

interface CaptchaLayerCfg { provider: string; mode: string; site_key: string; secret: string; }

const CAPTCHA_PROVIDERS = [
  { value: "turnstile", label: "Cloudflare Turnstile" },
  { value: "recaptcha", label: "Google reCAPTCHA v2" },
  { value: "hcaptcha", label: "hCaptcha" },
];


/** Automatic HTTPS via Let's Encrypt: domain + contact email, then restart. */
function TLSCard(): HTMLElement {
  const card = el("div.rst-card", LoadingState());

  async function render() {
    const cfg = await admin.tls.get();
    const domain = TextInput({
      label: "Domain name", icon: "globe", value: cfg.domain,
      placeholder: "panel.example.com",
      hint: "Must already point at this machine's public IP (A/AAAA record).",
    });
    const email = TextInput({
      label: "Contact email", icon: "envelope", value: cfg.email,
      placeholder: "admin@example.com",
      hint: "Let's Encrypt uses this to warn you about expiry problems.",
    });
    let enabled = cfg.enabled;
    let staging = cfg.staging;

    const status = (() => {
      if (!cfg.enabled) return Badge("disabled", "outline");
      if (!cfg.active) return Badge("restart required", "warning");
      if (cfg.certificate_issued) {
        return Badge(`valid · ${cfg.days_remaining}d left`, cfg.days_remaining! < 15 ? "warning" : "success");
      }
      return Badge("no certificate yet", "warning");
    })();

    const requestBtn = Button({
      label: "Request certificate now", icon: "certificate", size: "sm",
      disabled: !cfg.active,
      onClick: async () => {
        notify.info("Contacting Let's Encrypt…");
        try {
          const res = await admin.tls.request();
          notify.success(`Certificate issued, valid until ${new Date(res.expires_at).toLocaleDateString()}`);
          render();
        } catch (err) { notify.error(String((err as Error).message)); }
      },
    });

    const saveBtn = Button({
      label: "Save HTTPS settings", icon: "check", size: "sm", variant: "primary",
      onClick: async () => {
        try {
          await admin.tls.save({ enabled, domain: domain.value, email: email.value, staging });
          notify.success(enabled
            ? "Saved — restart Roost (with ports 80 and 443 free) to obtain the certificate."
            : "Automatic HTTPS disabled — restart Roost to apply.");
          render();
        } catch (err) { notify.error(String((err as Error).message)); }
      },
    });

    const children: (HTMLElement | null)[] = [
      el("div.rst-section__head",
        el("div.rst-card__title", icon("lock"), "Automatic HTTPS (Let's Encrypt)"),
        status,
      ),
      el("p.faint", "Roost obtains and renews a free TLS certificate for your domain on its own — no certbot, no reverse proxy. Requires ports 80 and 443 to reach this machine from the internet."),
      Switch({ checked: enabled, label: "Enable automatic HTTPS", onChange: (v) => { enabled = v; } }),
      el("div.rst-form__row", domain.el, email.el),
      Switch({ checked: staging, label: "Staging mode (untrusted certs, high rate limits — use to rehearse)", onChange: (v) => { staging = v; } }),
      el("div.row", saveBtn, requestBtn),
    ];

    if (cfg.enabled && !cfg.active) {
      children.push(el("p.faint", "⚠ Settings are stored but the certificate manager is not running. Restart Roost to activate it."));
    }
    if (cfg.error) children.push(el("p", { style: { color: "var(--danger)", fontSize: "12px" } }, cfg.error));
    if (cfg.enabled) {
      children.push(el("p.faint", `Panel URL is pinned to https://${cfg.domain || "…"} so wings receives the correct address in signed tokens.`));
    }

    card.replaceChildren(...children.filter(Boolean) as HTMLElement[]);
  }

  render().catch((err) => card.replaceChildren(el("p", { style: { color: "var(--danger)" } }, String(err.message))));
  return card;
}

function AdminSecurity(): HTMLElement {
  const body = el("div", LoadingState());
  let layers: CaptchaLayerCfg[] = [];

  async function save() {
    try {
      await admin.captcha.save(layers);
      notify.success(layers.length ? `CAPTCHA active with ${layers.length} layer(s)` : "CAPTCHA disabled");
      render();
    } catch (err) { notify.error(String((err as Error).message)); }
  }

  function layerCard(layer: CaptchaLayerCfg, index: number): HTMLElement {
    const provider = Select({ options: CAPTCHA_PROVIDERS, value: layer.provider, onChange: (v) => { layer.provider = v; } });
    const mode = Select({
      options: [
        { value: "visible", label: "Visible widget", icon: "eye" },
        { value: "invisible", label: "Invisible (transition page)", icon: "eye-slash" },
      ],
      value: layer.mode || "visible",
      onChange: (v) => { layer.mode = v; },
    });
    const site = TextInput({ label: "Site key", value: layer.site_key, onInput: (v) => { layer.site_key = v; } });
    const secret = TextInput({ label: "Secret key", value: layer.secret, type: "password", onInput: (v) => { layer.secret = v; } });
    return el("div.rst-card",
      el("div.rst-section__head",
        el("div.rst-card__title", icon("layer-group"), `Layer ${index + 1}`),
        IconButton("trash", { size: "sm", variant: "ghost", title: "Remove layer", onClick: () => { layers.splice(index, 1); render(); } }),
      ),
      el("div.rst-form__row",
        el("div", el("label.rst-input__label", "Provider"), provider.el),
        el("div", el("label.rst-input__label", "Mode"), mode.el),
        site.el, secret.el,
      ),
      el("p.faint", "Invisible mode verifies the browser on a transition page before the login form; the provider only shows an interactive challenge to suspicious clients. For Turnstile, also set the widget to Invisible/Managed in the Cloudflare dashboard."),
    );
  }

  function render() {
    body.replaceChildren(
      el("div.rst-card",
        el("div.rst-card__title", icon("shield-halved"), "Login CAPTCHA"),
        el("p.faint", "Pick any provider — or stack several: every enabled layer must be solved on sign-in, on top of the built-in per-IP rate limiting. Leave empty to disable."),
        el("div.row",
          Button({ label: "Add layer", icon: "plus", size: "sm", onClick: () => { layers.push({ provider: "turnstile", mode: "invisible", site_key: "", secret: "" }); render(); } }),
          Button({ label: "Save configuration", icon: "check", size: "sm", variant: "primary", onClick: save }),
        ),
      ),
      ...layers.map(layerCard),
    );
    if (!layers.length) {
      body.appendChild(EmptyState({ icon: "shield-halved", title: "CAPTCHA disabled", description: "Sign-in is protected by rate limiting only." }));
    }
  }

  admin.captcha.list().then((res) => {
    layers = (res.data ?? []).map((l) => ({ provider: l.provider, mode: l.mode || "visible", site_key: l.site_key, secret: l.secret ?? "" }));
    render();
  });
  return page("Security", { icon: "shield-halved" }, TLSCard(), body);
}

// ---------------------------------------------------------------- webhooks

function AdminWebhooks(): HTMLElement {
  const body = el("div", LoadingState());
  let hooks: Array<{ url: string; events: string[] }> = [];

  function render() {
    const rows = hooks.map((h, i) => el("div.rst-activity__item",
      icon("satellite-dish", { class: "faint" }),
      el("span.mono.truncate", { style: { maxWidth: "380px" } }, h.url),
      el("span.faint.mono", h.events.join(", ")),
      el("span.rst-activity__meta"),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Remove", onClick: async () => {
        hooks.splice(i, 1);
        await admin.webhooks.save(hooks);
        render();
      } }),
    ));
    const url = TextInput({ label: "Endpoint URL", placeholder: "https://example.com/hooks/roost", icon: "link" });
    const events = TextInput({ label: "Event filters", value: "*", hint: "Comma-separated prefixes, e.g. server:power.*, auth:fail — * for everything." });
    body.replaceChildren(
      el("div.rst-card",
        el("div.rst-card__title", icon("plus"), "Add webhook"),
        el("p.faint", "Every panel event (the activity log) is POSTed as JSON to matching endpoints — the feature Pterodactyl never shipped."),
        el("div.rst-form__row", url.el, events.el),
        el("div.row", Button({
          label: "Add", variant: "primary", icon: "plus",
          onClick: async () => {
            if (!url.value.startsWith("http")) { url.setError("Must be an http(s) URL"); return; }
            hooks.push({ url: url.value, events: events.value.split(",").map((s) => s.trim()).filter(Boolean) });
            await admin.webhooks.save(hooks);
            notify.success("Webhook added");
            render();
          },
        })),
      ),
      hooks.length
        ? el("div.rst-card", el("div.rst-card__title", icon("satellite-dish"), `Endpoints (${hooks.length})`), el("div.rst-activity", ...rows))
        : EmptyState({ icon: "satellite-dish", title: "No webhooks configured" }),
    );
  }

  admin.webhooks.list().then((res) => { hooks = res.data ?? []; render(); });
  return page("Webhooks", { icon: "satellite-dish" }, body);
}

// ---------------------------------------------------------------- locations

function AdminLocations(): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const locations = unwrap<any>(await admin.locations.list());
    if (!locations.length) {
      body.replaceChildren(EmptyState({ icon: "map-pin", title: "No locations", description: "Locations group nodes (e.g. by datacenter)." }));
      return;
    }
    body.replaceChildren(el("div.rst-card", el("div.rst-activity", ...locations.map((l) => el("div.rst-activity__item",
      icon("map-pin", { class: "faint" }),
      el("span.strong.mono", l.short),
      el("span.faint", l.long || "—"),
      el("span.rst-activity__meta", localTime(l.created_at)),
      IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => modal(l) }),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
        if (await confirmModal({ title: "Delete location", message: `Delete "${l.short}"?`, danger: true })) {
          try { await admin.locations.remove(l.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
        }
      } }),
    )))));
  }

  function modal(existing: any | null) {
    const short = TextInput({ label: "Short code", value: existing?.short ?? "", autofocus: true });
    const long = TextInput({ label: "Description", value: existing?.long ?? "" });
    openModal({
      title: existing ? "Edit location" : "New location", icon: "map-pin",
      body: el("div.rst-form", short.el, long.el),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          try {
            if (existing) await admin.locations.update(existing.id, { short: short.value, long: long.value });
            else await admin.locations.create(short.value, long.value);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  load();
  return page("Locations", { icon: "map-pin", actions: [Button({ label: "New location", icon: "plus", variant: "primary", size: "sm", onClick: () => modal(null) })] }, body);
}

// ---------------------------------------------------------------- users

function AdminUsers(): HTMLElement {
  const body = el("div", LoadingState());
  const search = TextInput({ placeholder: "Search users…", icon: "magnifying-glass", size: "sm", clearable: true, onInput: () => load(search.value) });

  async function load(filter = "") {
    const users = unwrap<any>(await admin.users.list(filter));
    body.replaceChildren(el("div.rst-card", el("div.rst-activity", ...users.map((u) => el("div.rst-activity__item",
      icon(u.root_admin ? "user-shield" : "user", { class: "faint" }),
      el("span.strong", u.username),
      el("span.faint", u.email),
      u.root_admin ? Badge("admin", "info") : null,
      u["2fa"] ? Badge("2FA", "success") : null,
      el("span.rst-activity__meta", `#${u.id} · ${localTime(u.created_at)}`),
      IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => modal(u) }),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
        if (await confirmModal({ title: "Delete user", message: `Delete ${u.email}? Their servers must be removed or reassigned first.`, danger: true })) {
          try { await admin.users.remove(u.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
        }
      } }),
    )))));
  }

  function modal(existing: any | null) {
    const username = TextInput({ label: "Username", value: existing?.username ?? "" });
    const email = TextInput({ label: "Email", value: existing?.email ?? "", icon: "envelope" });
    const first = TextInput({ label: "First name", value: existing?.first_name ?? "" });
    const last = TextInput({ label: "Last name", value: existing?.last_name ?? "" });
    const password = TextInput({ label: existing ? "New password (leave empty to keep)" : "Password (empty = random)", type: "password" });
    let rootAdmin = existing?.root_admin ?? false;
    openModal({
      title: existing ? `Edit ${existing.username}` : "New user", icon: "user", width: 560,
      body: el("div.rst-form",
        el("div.rst-form__row", username.el, email.el),
        el("div.rst-form__row", first.el, last.el),
        password.el,
        Switch({ checked: rootAdmin, label: "Administrator (full panel access)", onChange: (v) => { rootAdmin = v; } }),
      ),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const payload: Record<string, unknown> = {
            username: username.value, email: email.value,
            first_name: first.value, last_name: last.value, root_admin: rootAdmin,
          };
          if (password.value) payload.password = password.value;
          try {
            if (existing) await admin.users.update(existing.id, payload);
            else await admin.users.create(payload);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  load();
  return page("Users", { icon: "users", actions: [search.el, Button({ label: "New user", icon: "plus", variant: "primary", size: "sm", onClick: () => modal(null) })] }, body);
}

// ---------------------------------------------------------------- database hosts

function AdminDatabaseHosts(): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const hosts = unwrap<any>(await admin.databaseHosts.list());
    if (!hosts.length) {
      body.replaceChildren(EmptyState({ icon: "database", title: "No database hosts", description: "Register a MySQL/MariaDB server that panel users can create databases on." }));
      return;
    }
    body.replaceChildren(el("div.rst-card", el("div.rst-activity", ...hosts.map((h) => el("div.rst-activity__item",
      icon("database", { class: "faint" }),
      el("span.strong", h.name),
      el("span.mono.faint", `${h.host}:${h.port}`),
      el("span.faint", h.username),
      el("span.rst-activity__meta"),
      IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => modal(h) }),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
        if (await confirmModal({ title: "Delete database host", message: `Delete "${h.name}"?`, danger: true })) {
          try { await admin.databaseHosts.remove(h.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
        }
      } }),
    )))));
  }

  function modal(existing: any | null) {
    const name = TextInput({ label: "Display name", value: existing?.name ?? "" });
    const host = TextInput({ label: "Host", value: existing?.host ?? "", icon: "server" });
    const port = TextInput({ label: "Port", value: String(existing?.port ?? 3306), type: "number" });
    const username = TextInput({ label: "Username", value: existing?.username ?? "" });
    const password = TextInput({ label: existing ? "Password (empty = keep)" : "Password", type: "password" });
    openModal({
      title: existing ? "Edit database host" : "New database host", icon: "database", width: 560,
      body: el("div.rst-form",
        name.el,
        el("div.rst-form__row", host.el, port.el),
        el("div.rst-form__row", username.el, password.el),
      ),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const payload = { name: name.value, host: host.value, port: Number(port.value) || 3306, username: username.value, password: password.value };
          try {
            if (existing) await admin.databaseHosts.update(existing.id, payload);
            else await admin.databaseHosts.create(payload);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  load();
  return page("Database Hosts", { icon: "database", actions: [Button({ label: "New host", icon: "plus", variant: "primary", size: "sm", onClick: () => modal(null) })] }, body);
}

// ---------------------------------------------------------------- mounts

function AdminMounts(): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const mounts = unwrap<any>(await admin.mounts.list());
    if (!mounts.length) {
      body.replaceChildren(EmptyState({ icon: "folder-tree", title: "No mounts", description: "Bind host directories into server containers." }));
      return;
    }
    body.replaceChildren(el("div.rst-card", el("div.rst-activity", ...mounts.map((m) => el("div.rst-activity__item",
      icon("folder-tree", { class: "faint" }),
      el("span.strong", m.name),
      el("span.mono.faint", `${m.source} → ${m.target}`),
      m.read_only ? Badge("read-only", "outline") : null,
      el("span.rst-activity__meta"),
      IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => modal(m) }),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
        if (await confirmModal({ title: "Delete mount", message: `Delete "${m.name}"?`, danger: true })) {
          await admin.mounts.remove(m.id); load();
        }
      } }),
    )))));
  }

  function modal(existing: any | null) {
    const name = TextInput({ label: "Name", value: existing?.name ?? "" });
    const source = TextInput({ label: "Source (host path)", value: existing?.source ?? "", icon: "folder" });
    const target = TextInput({ label: "Target (container path)", value: existing?.target ?? "", icon: "folder-open" });
    let readOnly = existing?.read_only ?? false;
    openModal({
      title: existing ? "Edit mount" : "New mount", icon: "folder-tree", width: 560,
      body: el("div.rst-form", name.el, el("div.rst-form__row", source.el, target.el),
        Switch({ checked: readOnly, label: "Read-only", onChange: (v) => { readOnly = v; } })),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const payload = { name: name.value, source: source.value, target: target.value, read_only: readOnly };
          try {
            if (existing) await admin.mounts.update(existing.id, payload);
            else await admin.mounts.create(payload);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  load();
  return page("Mounts", { icon: "folder-tree", actions: [Button({ label: "New mount", icon: "plus", variant: "primary", size: "sm", onClick: () => modal(null) })] }, body);
}
