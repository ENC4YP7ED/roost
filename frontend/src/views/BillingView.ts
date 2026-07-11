import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { Tabs } from "../components/Tabs.ts";
import { openModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge } from "../components/misc.ts";
import { client, unwrap } from "../api/client.ts";
import { page, localTime } from "./shared.ts";

/** Customer billing area: shop, orders, invoices, subscriptions, profile. */
export function BillingView(tab?: string): HTMLElement {
  const tabs = Tabs([
    { id: "shop", label: "Shop", icon: "cart-shopping", render: ShopTab },
    { id: "orders", label: "Orders", icon: "receipt", render: OrdersTab },
    { id: "invoices", label: "Invoices", icon: "file-invoice", render: InvoicesTab },
    { id: "subscriptions", label: "Subscriptions", icon: "arrows-rotate", render: SubscriptionsTab },
    { id: "profile", label: "Billing profile", icon: "address-card", render: ProfileTab },
  ], { active: tab && ["shop", "orders", "invoices", "subscriptions", "profile"].includes(tab) ? tab : "shop" });

  return page("Billing", { icon: "cart-shopping" }, tabs.el);
}

function ShopTab(): HTMLElement {
  const body = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());

  async function load() {
    const products = unwrap<any>(await client.billing.products());
    if (!products.length) {
      body.replaceChildren(EmptyState({ icon: "store-slash", title: "The shop is empty", description: "No plans are available right now." }));
      return;
    }
    const grid = el("div.rst-shop");
    for (const p of products) grid.appendChild(productCard(p));
    body.replaceChildren(grid);
  }

  function productCard(p: any): HTMLElement {
    const buy = async (provider: string, config?: { memory: number; egg_id: number }) => {
      try {
        const res = await client.billing.checkout(p.id, provider, config);
        window.location.href = res.data.redirect_url;
      } catch (err) { notify.error(String((err as Error).message)); }
    };
    return el("div.rst-plan", { class: p.configurable ? "is-configurable" : "" },
      p.configurable ? el("div.rst-plan__badge", "Configurable") : null,
      el("div.rst-plan__name", p.name),
      el("div.rst-plan__price",
        p.configurable ? el("span.faint", "from ") : null,
        el("span.rst-plan__amount", p.price),
        p.configurable
          ? el("span.rst-plan__interval.faint", ` + ${p.price_per_gb}/GB`)
          : el("span.rst-plan__interval.faint", ` ${p.interval_label}`),
      ),
      p.description ? el("p.faint", p.description) : null,
      el("dl.rst-kv",
        el("dt", "Memory"), el("dd", p.configurable ? `${(p.min_memory / 1024) | 0}–${(p.max_memory / 1024) | 0} GB (you choose)` : `${p.limits.memory} MiB`),
        el("dt", "Disk"), el("dd", `${p.limits.disk} MiB`),
        el("dt", "CPU"), el("dd", p.limits.cpu ? `${p.limits.cpu}%` : "shared"),
        el("dt", "Databases"), el("dd", String(p.feature_limits.databases)),
        el("dt", "Backups"), el("dd", String(p.feature_limits.backups)),
      ),
      p.configurable
        ? el("div.rst-plan__buy", { onclick: () => configureModal(p, buy) },
            Button({ label: "Configure & order", icon: "sliders", variant: "primary", block: true }),
          )
        : el("div.rst-plan__buy", { onclick: () => providerChoice(p, (prov) => buy(prov)) },
            Button({ label: "Order now", icon: "credit-card", variant: "primary", block: true }),
          ),
    );
  }

  function configureModal(p: any, buy: (provider: string, config: { memory: number; egg_id: number }) => void) {
    const min = p.min_memory as number, max = p.max_memory as number;
    const games: { id: number; name: string }[] = p.game_options ?? [];
    // Snap the initial choice to the plan's minimum, in whole GiB steps.
    let memory = min;
    let eggID = games.length ? games[0].id : 0;

    const perGB = (p.price_per_gb_cents as number) / 100;
    const base = (p.price_cents as number) / 100;
    const cur = (p.currency as string) || "EUR";
    const fmt = (n: number) => new Intl.NumberFormat(undefined, { style: "currency", currency: cur }).format(n);
    const quote = () => base + perGB * Math.ceil(memory / 1024);

    const priceEl = el("div.rst-plan__amount", fmt(quote()));
    const ramLabel = el("strong", `${(memory / 1024) | 0} GB`);
    const slider = el("input.rst-range", {
      type: "range", min: String(min), max: String(max), step: "1024", value: String(memory),
      oninput: (e: Event) => {
        memory = Number((e.target as HTMLInputElement).value);
        ramLabel.textContent = `${(memory / 1024) | 0} GB`;
        priceEl.textContent = fmt(quote());
      },
    }) as HTMLInputElement;

    const gameSel = el("select.rst-select", {
      onchange: (e: Event) => { eggID = Number((e.target as HTMLSelectElement).value); },
    }, ...games.map((g) => el("option", { value: String(g.id) }, g.name))) as HTMLSelectElement;

    openModal({
      title: `Configure — ${p.name}`, icon: "sliders", width: 460,
      body: el("div.col", { style: { gap: "var(--sp-4)" } },
        el("div.col", { style: { gap: "var(--sp-2)" } },
          el("label.rst-field__label", el("span", "Memory"), ramLabel),
          slider,
        ),
        games.length
          ? el("div.col", { style: { gap: "var(--sp-2)" } }, el("label.rst-field__label", "Game"), gameSel)
          : null,
        el("div.rst-plan__price", { style: { marginTop: "var(--sp-2)" } },
          el("span.faint", "Total "), priceEl, el("span.faint", ` ${p.interval_label}`),
        ),
        el("div.col", { style: { gap: "var(--sp-2)", marginTop: "var(--sp-2)" } },
          Button({ label: "Pay with card (Stripe)", icon: "credit-card", block: true, onClick: () => buy("stripe", { memory, egg_id: eggID }) }),
          Button({ label: "Pay with Revolut", icon: "building-columns", block: true, onClick: () => buy("revolut", { memory, egg_id: eggID }) }),
        ),
      ),
      actions: [{ label: "Cancel" }],
    });
  }

  function providerChoice(p: any, buy: (provider: string) => void) {
    openModal({
      title: `Checkout — ${p.name}`, icon: "credit-card", width: 420,
      body: el("div.col", { style: { gap: "var(--sp-3)" } },
        el("p.faint", `You'll be redirected to a secure payment page to pay ${p.price} ${p.interval_label}.`),
        el("div.col", { style: { gap: "var(--sp-2)" } },
          Button({ label: "Pay with card (Stripe)", icon: "credit-card", block: true, onClick: () => buy("stripe") }),
          Button({ label: "Pay with Revolut", icon: "building-columns", block: true, onClick: () => buy("revolut") }),
        ),
      ),
      actions: [{ label: "Cancel" }],
    });
  }

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load the shop", description: String(err.message) })));
  return body;
}

function OrdersTab(): HTMLElement {
  const body = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());
  client.billing.orders().then((res) => {
    const orders = unwrap<any>(res);
    if (!orders.length) {
      body.replaceChildren(EmptyState({ icon: "receipt", title: "No orders yet" }));
      return;
    }
    const list = el("div.rst-activity");
    for (const o of orders) {
      list.appendChild(el("div.rst-activity__item",
        icon("receipt", { class: "faint" }),
        el("span.mono", o.uuid.slice(0, 8)),
        el("span", {}, o.gross),
        orderBadge(o.status),
        o.reverse_charge ? Badge("reverse charge", "outline") : null,
        el("span.rst-activity__meta", localTime(o.created_at)),
      ));
    }
    body.replaceChildren(el("div.rst-card", list));
  }).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return body;
}

function orderBadge(status: string) {
  const tone = status === "paid" ? "success" : status === "failed" || status === "refunded" ? "danger" : "warning";
  return Badge(status, tone as never);
}

function InvoicesTab(): HTMLElement {
  const body = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());
  client.billing.invoices().then((res) => {
    const invoices = unwrap<any>(res);
    if (!invoices.length) {
      body.replaceChildren(EmptyState({ icon: "file-invoice", title: "No invoices yet" }));
      return;
    }
    const list = el("div.rst-activity");
    for (const v of invoices) {
      list.appendChild(el("div.rst-activity__item",
        icon("file-invoice", { class: "faint" }),
        el("span.mono", v.number),
        el("span", {}, v.gross),
        Badge(v.status, v.status === "paid" ? "success" : "warning"),
        el("span.rst-activity__meta", localTime(v.issued_at)),
        Button({ label: "View / print", icon: "arrow-up-right-from-square", size: "sm", variant: "ghost",
          onClick: () => window.open(client.billing.invoiceURL(v.number), "_blank") }),
      ));
    }
    body.replaceChildren(el("div.rst-card", list));
  }).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return body;
}

function SubscriptionsTab(): HTMLElement {
  const body = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());
  client.billing.subscriptions().then((res) => {
    const subs = unwrap<any>(res);
    if (!subs.length) {
      body.replaceChildren(EmptyState({ icon: "arrows-rotate", title: "No subscriptions" }));
      return;
    }
    const list = el("div.rst-activity");
    for (const s of subs) {
      list.appendChild(el("div.rst-activity__item",
        icon("arrows-rotate", { class: "faint" }),
        el("span.mono", s.uuid.slice(0, 8)),
        el("span.faint", s.provider),
        Badge(s.status, s.status === "active" ? "success" : s.status === "canceled" ? "danger" : "warning"),
        el("span.rst-activity__meta", s.current_period_end ? `renews ${localTime(s.current_period_end)}` : ""),
      ));
    }
    body.replaceChildren(el("div.rst-card", list));
  }).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return body;
}

function ProfileTab(): HTMLElement {
  const body = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());
  client.billing.profile().then((res) => {
    const p = (res.attributes ?? {}) as any;
    const name = TextInput({ label: "Full name", value: p.name ?? "" });
    const company = TextInput({ label: "Company (optional)", value: p.company ?? "" });
    const address = TextInput({ label: "Address", value: p.address ?? "" });
    const city = TextInput({ label: "City", value: p.city ?? "" });
    const postal = TextInput({ label: "Postal code", value: p.postal_code ?? "" });
    const country = TextInput({ label: "Country (2-letter code)", value: p.country ?? "", hint: "e.g. DE, FR, US — determines VAT treatment." });
    const vat = TextInput({ label: "VAT ID (EU businesses)", value: p.vat_id ?? "", hint: "A valid EU VAT ID enables reverse-charge (no VAT charged)." });

    body.replaceChildren(el("div.rst-card",
      el("div.rst-card__title", icon("address-card"), "Billing details"),
      el("p.faint", "These appear on your invoices and set how VAT is calculated at checkout."),
      el("div.rst-form__row", name.el, company.el),
      address.el,
      el("div.rst-form__row", city.el, postal.el, country.el),
      vat.el,
      el("div.row", Button({
        label: "Save billing profile", variant: "primary", icon: "check",
        onClick: async () => {
          try {
            await client.billing.saveProfile({
              Name: name.value, Company: company.value, Address: address.value, City: city.value,
              PostalCode: postal.value, Country: country.value, VATID: vat.value,
            });
            notify.success("Billing profile saved");
          } catch (err) { notify.error(String((err as Error).message)); }
        },
      })),
    ));
  });
  return body;
}
