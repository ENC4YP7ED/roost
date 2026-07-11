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
import { LandingView } from "./views/LandingView.ts";
import { AppShell } from "./views/AppShell.ts";

const appRoot = document.getElementById("app")!;

function showLanding() {
  store.user.value = null;
  mount(appRoot, LandingView({
    onSignIn: () => showLogin("login"),
    onRegister: () => showLogin("register"),
    onConfigure: () => showLogin("login"),
  }));
}

function showLogin(mode: "login" | "register" = "login") {
  store.user.value = null;
  mount(appRoot, LoginView(showApp, { mode, onBack: showLanding }));
}

function showApp() {
  mount(appRoot, AppShell(showLanding));
}

async function boot() {
  try {
    const account = await client.account();
    store.user.value = account.attributes as never;
    showApp();
  } catch {
    // A logged-out visitor sees the public landing page first, unless they
    // deep-linked to a specific in-app route.
    if (location.hash && location.hash !== "#/" && !location.hash.startsWith("#/billing/shop")) {
      showLogin("login");
    } else {
      showLanding();
    }
  }
}

boot();
