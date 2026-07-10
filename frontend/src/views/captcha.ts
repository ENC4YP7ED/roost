import { el, icon } from "../core/dom.ts";
import { Spinner } from "../components/misc.ts";
import { http } from "../api/client.ts";
import { store } from "../state/store.ts";

/**
 * Multi-layer CAPTCHA support. Each layer is a provider + mode:
 *
 *  - visible   — classic checkbox widget the user clicks
 *  - invisible — executes automatically behind a Cloudflare-style
 *                "checking your browser" transition page; the provider only
 *                surfaces an interactive challenge if it distrusts the client
 *
 * All configured layers must produce a token before the login form appears
 * (invisible) or submits (visible).
 */

export interface CaptchaLayer {
  id: number;
  provider: "turnstile" | "recaptcha" | "hcaptcha";
  mode: "visible" | "invisible";
  site_key: string;
}

export const fetchCaptchaLayers = () =>
  http.get<{ data: CaptchaLayer[] }>("/auth/captcha").then((r) => r.data ?? []);

const SCRIPTS: Record<string, { src: string; global: string }> = {
  turnstile: { src: "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit", global: "turnstile" },
  recaptcha: { src: "https://www.google.com/recaptcha/api.js?render=explicit", global: "grecaptcha" },
  hcaptcha: { src: "https://js.hcaptcha.com/1/api.js?render=explicit", global: "hcaptcha" },
};

const loaded = new Map<string, Promise<any>>();

function loadProvider(provider: string): Promise<any> {
  if (!loaded.has(provider)) {
    const { src, global } = SCRIPTS[provider];
    loaded.set(provider, new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = src;
      s.async = true;
      s.onerror = () => reject(new Error(`Failed to load ${provider} script`));
      document.head.appendChild(s);
      const started = Date.now();
      const poll = () => {
        const api = (window as any)[global];
        // grecaptcha exposes render() only once fully ready.
        if (api && typeof api.render === "function") resolve(api);
        else if (Date.now() - started > 15000) reject(new Error(`${provider} did not initialise`));
        else setTimeout(poll, 100);
      };
      s.onload = poll;
    }));
  }
  return loaded.get(provider)!;
}

const PROVIDER_LABELS: Record<string, string> = {
  turnstile: "Cloudflare Turnstile",
  recaptcha: "Google reCAPTCHA",
  hcaptcha: "hCaptcha",
};

export interface CaptchaStack {
  el: HTMLElement;
  /** Resolved tokens keyed by layer id; null until every layer is solved. */
  tokens(): Record<string, string> | null;
  /** Fires once when the final layer resolves. */
  onComplete(fn: (tokens: Record<string, string>) => void): void;
  /** Reset all widgets and re-execute invisible ones (after a failed login). */
  reset(): void;
}

/**
 * Renders every layer: visible widgets stack vertically; invisible ones get
 * a status row (provider name + spinner → check) and execute immediately.
 */
export function CaptchaStack(layers: CaptchaLayer[]): CaptchaStack {
  const root = el("div.col", { style: { gap: "var(--sp-3)", alignItems: "stretch" } });
  const solved = new Map<number, string>();
  const widgets: Array<{ layer: CaptchaLayer; api: any; widgetId: unknown }> = [];
  const statusRows = new Map<number, HTMLElement>();
  let completeFn: ((tokens: Record<string, string>) => void) | null = null;
  let announced = false;

  function collect(): Record<string, string> | null {
    if (solved.size !== layers.length) return null;
    const out: Record<string, string> = {};
    for (const [id, token] of solved) out[String(id)] = token;
    return out;
  }

  function markSolved(layer: CaptchaLayer, token: string) {
    solved.set(layer.id, token);
    const row = statusRows.get(layer.id);
    if (row) {
      row.querySelector(".rst-captcha__state")?.replaceChildren(icon("circle-check", { class: "rst-captcha__ok" }));
    }
    const tokens = collect();
    if (tokens && completeFn && !announced) {
      announced = true;
      completeFn(tokens);
    }
  }

  for (const layer of layers) {
    const invisible = layer.mode === "invisible";
    const holder = el("div", {
      dataset: { layer: String(layer.id) },
      style: invisible ? { position: "absolute", width: "0", height: "0", overflow: "hidden" } : { alignSelf: "center" },
    });
    if (invisible) {
      const row = el("div.rst-captcha__row",
        el("span.rst-captcha__state", Spinner(13)),
        el("span", {}, PROVIDER_LABELS[layer.provider] ?? layer.provider),
        el("span.faint", "· verifying"),
      );
      statusRows.set(layer.id, row);
      root.appendChild(row);
    }
    root.appendChild(holder);

    loadProvider(layer.provider)
      .then((api) => {
        const params: Record<string, unknown> = {
          sitekey: layer.site_key,
          theme: "dark",
          callback: (token: string) => markSolved(layer, token),
          "expired-callback": () => solved.delete(layer.id),
        };
        if (invisible) {
          if (layer.provider === "turnstile") {
            // Turnstile handles execution itself; only surface UI on demand.
            params.appearance = "interaction-only";
          } else {
            params.size = "invisible";
          }
        }
        const widgetId = api.render(holder, params);
        widgets.push({ layer, api, widgetId });
        // reCAPTCHA / hCaptcha invisible widgets need an explicit kick.
        if (invisible && layer.provider !== "turnstile") {
          api.execute(widgetId);
        }
      })
      .catch((err) => {
        const target = statusRows.get(layer.id) ?? holder;
        target.replaceChildren(el("span", { style: { color: "var(--danger)", fontSize: "12px" } },
          `${PROVIDER_LABELS[layer.provider] ?? layer.provider}: ${err.message}`));
      });
  }

  return {
    el: root,
    tokens: collect,
    onComplete(fn) {
      completeFn = fn;
      const tokens = collect();
      if (tokens && !announced) {
        announced = true;
        fn(tokens);
      }
    },
    reset() {
      solved.clear();
      announced = false;
      for (const { layer, api, widgetId } of widgets) {
        try {
          api.reset(widgetId);
          statusRows.get(layer.id)?.querySelector(".rst-captcha__state")?.replaceChildren(Spinner(13));
          if (layer.mode === "invisible" && layer.provider !== "turnstile") api.execute(widgetId);
        } catch { /* widget gone */ }
      }
    },
  };
}

/**
 * Cloudflare-style interstitial shown before the login card when invisible
 * layers are configured: brand, spinner, one status row per layer. Visible
 * layers render inline on the same page, so mixed configurations work.
 * Calls onVerified with the tokens once every layer resolves.
 */
export function CaptchaGate(layers: CaptchaLayer[], onVerified: (tokens: Record<string, string>, stack: CaptchaStack) => void): HTMLElement {
  const stack = CaptchaStack(layers);
  const headline = el("div.rst-connect__tag.faint", "Checking your browser before signing in…");

  stack.onComplete((tokens) => {
    headline.replaceChildren(icon("circle-check", { class: "rst-captcha__ok" }), " Verified — loading sign-in");
    setTimeout(() => onVerified(tokens, stack), 450);
  });

  return el("div.rst-connect__card",
    el("div.rst-connect__brand",
      el("div.rst-connect__logo", icon("shield-halved")),
      el("div",
        el("div.rst-connect__name", store.appName.value),
        headline,
      ),
    ),
    stack.el,
    el("div.rst-connect__footer.faint",
      icon("lock"),
      el("span", {}, `Protected by ${layers.map((l) => PROVIDER_LABELS[l.provider] ?? l.provider).join(" + ")}`),
    ),
  );
}
