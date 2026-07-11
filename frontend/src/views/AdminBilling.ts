import { el, icon } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { TextInput, TextArea } from "../components/TextInput.ts";
import { Select } from "../components/Select.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge, Switch, Field } from "../components/misc.ts";
import { admin, unwrap } from "../api/client.ts";
import { page, localTime } from "./shared.ts";

// ================================================================= settings

export function AdminBilling(): HTMLElement {
  const body = el("div", LoadingState());

  admin.billing.settings().then((cfg: any) => {
    const enabled = Switch({ checked: cfg.enabled, label: "Enable billing & the customer shop", onChange: (v) => { s.enabled = v; } });
    const currency = TextInput({ label: "Currency (ISO 4217)", value: cfg.currency ?? "EUR" });
    const vat = TextInput({ label: "VAT rate (%)", value: String((cfg.vat_rate ?? 0) / 100), type: "number", hint: "e.g. 19 for German VAT. Reverse charge is applied automatically for EU B2B." });
    const prefix = TextInput({ label: "Invoice prefix", value: cfg.invoice_prefix ?? "INV" });

    const sellerName = TextInput({ label: "Legal name", value: cfg.seller_name ?? "" });
    const sellerAddr = TextArea({ placeholder: "Street, postal code, city, country", rows: 2, value: cfg.seller_address ?? "" });
    const sellerCountry = TextInput({ label: "Country code", value: cfg.seller_country ?? "" });
    const sellerVat = TextInput({ label: "Your VAT ID", value: cfg.seller_vat_id ?? "" });
    const sellerEmail = TextInput({ label: "Billing email", value: cfg.seller_email ?? "" });

    // Providers — secrets are write-only; blank means "keep the stored value".
    const stripeEnabled = Switch({ checked: cfg.stripe_enabled, label: "Stripe", onChange: (v) => { s.stripe_enabled = v; } });
    const stripeSecret = TextInput({ label: "Stripe secret key (sk_…)", type: "password", placeholder: cfg.stripe_configured ? "•••••• (stored)" : "sk_live_…" });
    const stripeWebhook = TextInput({ label: "Stripe webhook signing secret (whsec_…)", type: "password", placeholder: cfg.stripe_configured ? "•••••• (stored)" : "whsec_…" });
    const stripePublish = TextInput({ label: "Stripe publishable key (optional)", value: cfg.stripe_publishable ?? "" });

    const revolutEnabled = Switch({ checked: cfg.revolut_enabled, label: "Revolut Business", onChange: (v) => { s.revolut_enabled = v; } });
    const revolutSandbox = Switch({ checked: cfg.revolut_sandbox, label: "Sandbox mode", onChange: (v) => { s.revolut_sandbox = v; } });
    const revolutSecret = TextInput({ label: "Revolut API secret key", type: "password", placeholder: cfg.revolut_configured ? "•••••• (stored)" : "sk_…" });
    const revolutWebhook = TextInput({ label: "Revolut webhook signing secret", type: "password", placeholder: cfg.revolut_configured ? "•••••• (stored)" : "wsk_…" });

    const s: any = { enabled: cfg.enabled, stripe_enabled: cfg.stripe_enabled, revolut_enabled: cfg.revolut_enabled, revolut_sandbox: cfg.revolut_sandbox };

    const base = admin && (window.location.origin);
    const stripeHook = `${base}/api/billing/webhook/stripe`;
    const revolutHook = `${base}/api/billing/webhook/revolut`;

    const save = Button({
      label: "Save billing settings", variant: "primary", icon: "check",
      onClick: async () => {
        const payload: any = {
          enabled: s.enabled,
          currency: currency.value,
          vat_rate: Math.round(parseFloat(vat.value || "0") * 100),
          invoice_prefix: prefix.value,
          seller_name: sellerName.value, seller_address: sellerAddr.value,
          seller_country: sellerCountry.value, seller_vat_id: sellerVat.value,
          seller_email: sellerEmail.value,
          stripe_enabled: s.stripe_enabled, stripe_publishable: stripePublish.value,
          revolut_enabled: s.revolut_enabled, revolut_sandbox: s.revolut_sandbox,
        };
        // Only send secrets that were actually typed (blank = keep stored).
        if (stripeSecret.value) payload.stripe_secret = stripeSecret.value;
        if (stripeWebhook.value) payload.stripe_webhook_secret = stripeWebhook.value;
        if (revolutSecret.value) payload.revolut_secret = revolutSecret.value;
        if (revolutWebhook.value) payload.revolut_webhook_secret = revolutWebhook.value;
        try {
          await admin.billing.saveSettings(payload);
          notify.success("Billing settings saved");
        } catch (err) { notify.error(String((err as Error).message)); }
      },
    });

    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } },
      el("div.rst-card",
        el("div.rst-card__title", icon("money-bill"), "Billing"),
        el("p.faint", "Sell game servers as plans. When a customer pays, the server is provisioned automatically and a compliant invoice is issued."),
        enabled,
        el("div.rst-form__row", currency.el, vat.el, prefix.el),
      ),
      el("div.rst-card",
        el("div.rst-card__title", icon("building"), "Seller details (for invoices)"),
        el("p.faint", "Required by EU invoicing rules. These appear on every invoice."),
        sellerName.el,
        Field("Address", sellerAddr),
        el("div.rst-form__row", sellerCountry.el, sellerVat.el, sellerEmail.el),
      ),
      el("div.rst-card",
        el("div.rst-card__title", icon("credit-card"), "Stripe ", cfg.stripe_configured ? Badge("configured", "success") : Badge("not set", "outline")),
        stripeEnabled,
        el("div.rst-form__row", stripeSecret.el, stripeWebhook.el),
        stripePublish.el,
        el("p.faint", "Webhook endpoint (add in the Stripe dashboard): "),
        el("div.rst-codeblock", stripeHook),
      ),
      el("div.rst-card",
        el("div.rst-card__title", icon("building-columns"), "Revolut Business ", cfg.revolut_configured ? Badge("configured", "success") : Badge("not set", "outline")),
        el("div.row", revolutEnabled, revolutSandbox),
        el("div.rst-form__row", revolutSecret.el, revolutWebhook.el),
        el("p.faint", "Webhook endpoint (add in the Revolut merchant dashboard): "),
        el("div.rst-codeblock", revolutHook),
      ),
      el("div.row", save),
    ));
  }).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));

  return page("Billing", { icon: "money-bill" }, body);
}

// ================================================================= products

export function AdminProducts(): HTMLElement {
  const body = el("div", LoadingState());
  let eggs: Array<{ value: string; label: string }> = [];
  let nodes: Array<{ value: string; label: string }> = [];
  let nestOpts: Array<{ value: string; label: string }> = [];

  async function loadRefs() {
    if (eggs.length) return;
    const nests = unwrap<any>(await admin.nests.list());
    nestOpts = [{ value: "", label: "— (none)" }];
    for (const nest of nests) {
      nestOpts.push({ value: String(nest.id), label: nest.name });
      const list = unwrap<any>(await admin.nests.eggs(nest.id));
      for (const e of list) eggs.push({ value: String(e.id), label: `${nest.name} / ${e.name}` });
    }
    nodes = [{ value: "", label: "Auto (any node with capacity)" }];
    for (const n of unwrap<any>(await admin.nodes.list())) nodes.push({ value: String(n.id), label: n.name });
  }

  async function load() {
    await loadRefs();
    const products = unwrap<any>(await admin.billing.products());
    if (!products.length) {
      body.replaceChildren(EmptyState({ icon: "box", title: "No plans yet", description: "Create a plan customers can buy." }));
      return;
    }
    const list = el("div.rst-activity");
    for (const p of products) {
      list.appendChild(el("div.rst-activity__item",
        icon("box", { class: "faint" }),
        el("span.strong", p.name),
        el("span", {}, `${p.price} ${p.interval_label}`),
        el("span.faint", `${p.limits.memory} MiB · ${p.limits.disk} MiB`),
        p.active ? Badge("active", "success") : Badge("hidden", "outline"),
        el("span.rst-activity__meta"),
        IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => modal(p) }),
        IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
          if (await confirmModal({ title: "Delete plan", message: `Delete "${p.name}"? Sold plans are hidden rather than removed.`, danger: true })) {
            await admin.billing.removeProduct(p.id); load();
          }
        } }),
      ));
    }
    body.replaceChildren(el("div.rst-card", list));
  }

  function modal(existing: any | null) {
    const name = TextInput({ label: "Plan name", value: existing?.name ?? "" });
    const price = TextInput({ label: "Price (major units)", value: existing ? String(existing.price_cents / 100) : "", type: "number", hint: "Net price, before VAT." });
    const currency = TextInput({ label: "Currency", value: existing?.currency ?? "EUR" });
    const interval = Select({ options: [
      { value: "month", label: "Monthly (subscription)" },
      { value: "year", label: "Yearly (subscription)" },
      { value: "one_time", label: "One-time payment" },
    ], value: existing?.billing_interval ?? "month" });
    const egg = Select({ options: eggs, value: String(existing?.egg_id ?? eggs[0]?.value ?? ""), searchable: true });
    const node = Select({ options: nodes, value: existing?.node_id != null ? String(existing.node_id) : "" });
    const memory = TextInput({ label: "Memory (MiB)", value: String(existing?.limits.memory ?? 2048), type: "number" });
    const disk = TextInput({ label: "Disk (MiB)", value: String(existing?.limits.disk ?? 10240), type: "number" });
    const cpu = TextInput({ label: "CPU (%)", value: String(existing?.limits.cpu ?? 0), type: "number", hint: "0 = shared" });
    const databases = TextInput({ label: "Databases", value: String(existing?.feature_limits.databases ?? 0), type: "number" });
    const allocations = TextInput({ label: "Allocations", value: String(existing?.feature_limits.allocations ?? 1), type: "number" });
    const backups = TextInput({ label: "Backups", value: String(existing?.feature_limits.backups ?? 0), type: "number" });
    const description = TextArea({ placeholder: "Shown on the plan card", rows: 2, value: existing?.description ?? "" });
    let active = existing?.active ?? true;

    // Configurable plan: customer picks RAM (priced base + per-GB) and a game
    // from the chosen nest. The fixed Memory/Egg fields above become defaults.
    let configurable = existing?.configurable ?? false;
    const pricePerGB = TextInput({ label: "Price per GB (major units)", value: existing ? String((existing.price_per_gb_cents ?? 0) / 100) : "0", type: "number", hint: "Added on top of the base price, per GiB of RAM." });
    const minMemory = TextInput({ label: "Min memory (MiB)", value: String(existing?.min_memory ?? 1024), type: "number" });
    const maxMemory = TextInput({ label: "Max memory (MiB)", value: String(existing?.max_memory ?? 16384), type: "number" });
    const nest = Select({ options: nestOpts, value: existing?.nest_id != null ? String(existing.nest_id) : "", searchable: true });
    const configFields = el("div.rst-form", { style: { display: configurable ? "" : "none", marginTop: "var(--sp-3)" } },
      el("div.rst-form__row", pricePerGB.el, minMemory.el, maxMemory.el),
      Field("Game category (nest customers can pick from)", nest.el),
    );

    openModal({
      title: existing ? `Edit ${existing.name}` : "New plan", icon: "box", width: 700,
      body: el("div.rst-form",
        el("div.rst-form__row", name.el, price.el, currency.el),
        el("div.rst-form__row", Field("Billing", interval.el), Field("Egg", egg.el), Field("Node", node.el)),
        el("div.rst-form__row", memory.el, disk.el, cpu.el),
        el("div.rst-form__row", databases.el, allocations.el, backups.el),
        Field("Description", description),
        Switch({ checked: active, label: "Active (visible in the shop)", onChange: (v) => { active = v; } }),
        Switch({ checked: configurable, label: "Configurable (customer chooses RAM + game)", onChange: (v) => { configurable = v; configFields.style.display = v ? "" : "none"; } }),
        configFields,
      ),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const payload = {
            name: name.value, description: description.value,
            price_cents: Math.round(parseFloat(price.value || "0") * 100),
            currency: currency.value, billing_interval: interval.value,
            egg_id: Number(egg.value), node_id: node.value ? Number(node.value) : null,
            memory: Number(memory.value), disk: Number(disk.value), cpu: Number(cpu.value),
            databases: Number(databases.value), allocations: Number(allocations.value), backups: Number(backups.value),
            active,
            configurable,
            price_per_gb_cents: Math.round(parseFloat(pricePerGB.value || "0") * 100),
            min_memory: Number(minMemory.value), max_memory: Number(maxMemory.value),
            nest_id: nest.value ? Number(nest.value) : null,
          };
          try {
            if (existing) await admin.billing.updateProduct(existing.id, payload);
            else await admin.billing.createProduct(payload);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  loadRefs().then(load).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  const newBtn = Button({ label: "New plan", icon: "plus", variant: "primary", size: "sm", onClick: () => modal(null) });
  return page("Plans", { icon: "box", actions: [newBtn] }, body);
}

// ================================================================= orders & invoices

export function AdminOrders(): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const [orders, invoices] = await Promise.all([
      unwrap<any>(await admin.billing.orders()),
      unwrap<any>(await admin.billing.invoices()),
    ]);

    const orderList = orders.length ? el("div.rst-activity", ...orders.map((o: any) => el("div.rst-activity__item",
      icon("receipt", { class: "faint" }),
      el("span.mono", o.uuid.slice(0, 8)),
      el("span.faint", `user #${o.user_id}`),
      el("span", {}, o.gross),
      el("span.faint", o.provider),
      Badge(o.status, o.status === "paid" ? "success" : o.status === "refunded" || o.status === "failed" ? "danger" : "warning"),
      o.server_id ? Badge(`server #${o.server_id}`, "info") : null,
      el("span.rst-activity__meta", localTime(o.created_at)),
    ))) : el("p.faint", "No orders yet.");

    const invoiceList = invoices.length ? el("div.rst-activity", ...invoices.map((v: any) => el("div.rst-activity__item",
      icon("file-invoice", { class: "faint" }),
      el("span.mono", v.number),
      el("span.faint", `user #${v.user_id}`),
      el("span", {}, v.gross),
      v.reverse_charge ? Badge("reverse charge", "outline") : null,
      Badge(v.status, v.status === "paid" ? "success" : "warning"),
      el("span.rst-activity__meta", localTime(v.issued_at)),
      Button({ label: "Open", icon: "arrow-up-right-from-square", size: "sm", variant: "ghost",
        onClick: () => window.open(`/api/client/billing/invoices/${encodeURIComponent(v.number)}/html`, "_blank") }),
    ))) : el("p.faint", "No invoices yet.");

    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } },
      el("div.rst-card", el("div.rst-card__title", icon("receipt"), `Orders (${orders.length})`), orderList),
      el("div.rst-card", el("div.rst-card__title", icon("file-invoice"), `Invoices (${invoices.length})`), invoiceList),
    ));
  }

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Orders & invoices", { icon: "receipt" }, body);
}
