import { el, icon, type Child } from "../core/dom.ts";
import { formatBytes } from "../util/format.ts";

/** Standard scrollable page scaffold with a title row. */
export function page(title: Child, opts: { sub?: string; actions?: Child[]; icon?: string } = {}, ...children: Child[]): HTMLElement {
  return el("div.rst-page",
    el("div.rst-page__inner",
      el("div.rst-page__head",
        el("div",
          el("h1.rst-page__title", opts.icon ? [icon(opts.icon, { class: "faint" }), " ", title] : title),
          opts.sub ? el("div.rst-page__sub", opts.sub) : null,
        ),
        opts.actions?.length ? el("div.row", ...opts.actions) : null,
      ),
      ...children,
    ),
  );
}

export function section(title: string, actions: Child[], ...children: Child[]): HTMLElement {
  return el("div.rst-section",
    el("div.rst-section__head",
      el("h2.rst-section__title", title),
      actions.length ? el("div.row", ...actions) : null,
    ),
    ...children,
  );
}

/** A resource meter with label, value and usage bar. */
export function meter(label: string, valueText: string, fraction: number): HTMLElement {
  const pct = Math.max(0, Math.min(1, fraction));
  const fill = el("div.rst-meter__fill", { style: { width: `${(pct * 100).toFixed(1)}%` } });
  if (pct > 0.9) fill.classList.add("is-crit");
  else if (pct > 0.7) fill.classList.add("is-hot");
  return el("div.rst-meter",
    el("div.rst-meter__row",
      el("span.rst-meter__label", label),
      el("span.rst-meter__value", valueText),
    ),
    el("div.rst-meter__bar", fill),
  );
}

export function statTile(label: string, value: Child, iconName: string): HTMLElement {
  return el("div.rst-stat",
    el("div.rst-stat__icon", icon(iconName)),
    el("div",
      el("div.rst-stat__value", value),
      el("div.rst-stat__label.faint", label),
    ),
  );
}

export function statusDot(state: string): HTMLElement {
  return el("span", { class: `rst-statusdot rst-statusdot--${state}` });
}

export function memText(bytes: number, limitMiB: number): string {
  return limitMiB > 0
    ? `${formatBytes(bytes)} / ${formatBytes(limitMiB * 1024 * 1024)}`
    : `${formatBytes(bytes)} / ∞`;
}

export function cpuText(pct: number, limit: number): string {
  return limit > 0 ? `${pct.toFixed(1)}% / ${limit}%` : `${pct.toFixed(1)}% / ∞`;
}

export function timeAgo(iso: string | null | undefined): string {
  if (!iso) return "never";
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms)) return String(iso);
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** ISO timestamp → local date+time string. */
export function localTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? String(iso) : d.toLocaleString();
}
