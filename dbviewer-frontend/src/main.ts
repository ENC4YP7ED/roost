// Fonts (self-hosted via @fontsource) and Font Awesome.
import "@fontsource/inter/400.css";
import "@fontsource/inter/500.css";
import "@fontsource/inter/600.css";
import "@fontsource/inter/700.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fortawesome/fontawesome-free/css/fontawesome.css";
import "@fortawesome/fontawesome-free/css/solid.css";
import "@fortawesome/fontawesome-free/css/regular.css";
import "@fortawesome/fontawesome-free/css/brands.css";

// Design system + component styles.
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/components.css";
import "./styles/views.css";

import { mount } from "./core/dom.ts";
import { api } from "./api/client.ts";
import { store } from "./state/store.ts";
import { ConnectView } from "./views/ConnectView.ts";
import { AppShell } from "./views/AppShell.ts";
import { notify } from "./components/Toast.ts";

const appRoot = document.getElementById("app")!;

function showConnect() {
  store.connected.value = false;
  mount(appRoot, ConnectView(showApp));
}

function showApp() {
  store.connected.value = true;
  mount(appRoot, AppShell(showConnect));
}

async function boot() {
  if (!api.authenticated) {
    showConnect();
    return;
  }
  // We have a stored token — verify it's still alive before showing the shell.
  try {
    const s = await api.session();
    store.server.value = s.server;
    store.user.value = s.user;
    showApp();
  } catch {
    notify.info("Session expired — please reconnect");
    showConnect();
  }
}

boot();
