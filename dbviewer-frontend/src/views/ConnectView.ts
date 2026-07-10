import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { notify } from "../components/Toast.ts";
import { api, ApiError } from "../api/client.ts";
import { store } from "../state/store.ts";

/** The connection screen shown when no session is active. */
export function ConnectView(onConnected: () => void): HTMLElement {
  const host = TextInput({ label: "Host", value: "127.0.0.1", icon: "server", placeholder: "127.0.0.1" });
  const port = TextInput({ label: "Port", value: "3306", icon: "plug", inputMode: "numeric", placeholder: "3306" });
  const user = TextInput({ label: "Username", value: "root", icon: "user", autofocus: true });
  const pass = TextInput({ label: "Password", type: "password", icon: "lock", placeholder: "••••••••" });

  let busy = false;
  const submit = async () => {
    if (busy) return;
    busy = true;
    connectBtn.setAttribute("aria-busy", "true");
    connectBtn.replaceChildren(icon("spinner", { spin: true }), el("span", {}, "Connecting…"));
    try {
      const res = await api.connect({
        host: host.value.trim() || "127.0.0.1",
        port: parseInt(port.value, 10) || 3306,
        user: user.value.trim(),
        password: pass.value,
      });
      store.connected.value = true;
      store.server.value = res.server;
      store.user.value = res.user;
      store.serverInfo.value = res.info;
      notify.success(`Connected to ${res.server}`, "MySQL");
      onConnected();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : String(err);
      notify.error(msg, "Connection failed");
      pass.setError("Check your credentials and host");
    } finally {
      busy = false;
      connectBtn.removeAttribute("aria-busy");
      connectBtn.replaceChildren(icon("right-to-bracket"), el("span", {}, "Connect"));
    }
  };

  for (const f of [host, port, user, pass]) {
    f.input.addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  }

  const connectBtn = Button({ label: "Connect", icon: "right-to-bracket", variant: "primary", block: true, onClick: submit });

  const card = el("div.gtma-connect__card",
    el("div.gtma-connect__brand",
      el("div.gtma-connect__logo", icon("database")),
      el("div.col",
        el("div.gtma-connect__name", "Database Viewer"),
        el("div.gtma-connect__tag.muted", "Go · TypeScript · MySQL / MariaDB"),
      ),
    ),
    el("div.gtma-connect__form",
      el("div.gtma-connect__row",
        el("div.gtma-connect__host", host.el),
        el("div.gtma-connect__port", port.el),
      ),
      user.el,
      pass.el,
      connectBtn,
    ),
    el("div.gtma-connect__footer.faint",
      icon("circle-info"),
      el("span", {}, "Credentials are proxied to the server and never stored in the browser."),
    ),
  );

  return el("div.gtma-connect", card, el("div.gtma-connect__grid"));
}
