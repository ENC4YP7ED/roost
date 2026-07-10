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
import { client } from "./api/client.ts";
import { store } from "./state/store.ts";
import { LoginView } from "./views/LoginView.ts";
import { AppShell } from "./views/AppShell.ts";

const appRoot = document.getElementById("app")!;

function showLogin() {
  store.user.value = null;
  mount(appRoot, LoginView(showApp));
}

function showApp() {
  mount(appRoot, AppShell(showLogin));
}

async function boot() {
  try {
    const account = await client.account();
    store.user.value = account.attributes as never;
    showApp();
  } catch {
    showLogin();
  }
}

boot();
