import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { notify } from "../components/Toast.ts";
import { auth, client, passkeyLogin } from "../api/client.ts";
import { store } from "../state/store.ts";
import { passkeysSupported, getPasskey } from "../util/webauthn.ts";
import { fetchCaptchaLayers, CaptchaGate, type CaptchaLayer, type CaptchaStack } from "./captcha.ts";

/**
 * Login flow. When CAPTCHA layers are configured the user first passes a
 * Cloudflare-style verification gate (invisible layers run silently, visible
 * ones render there), then reaches the credentials card with tokens in hand.
 */
export interface LoginOptions {
  mode?: "login" | "register";
  onBack?: () => void;
}

export function LoginView(onLogin: () => void, options: LoginOptions = {}): HTMLElement {
  let card = el("div.rst-connect__card");
  const shell = el("div.rst-connect", el("div.rst-connect__grid"), card);

  let captchaLayers: CaptchaLayer[] = [];
  let captchaTokens: Record<string, string> | undefined;
  let gateStack: CaptchaStack | null = null;

  function swapCard(next: HTMLElement) {
    shell.replaceChild(next, card);
    card = next;
  }

  async function finishLogin() {
    const account = await client.account();
    store.user.value = account.attributes as never;
    onLogin();
  }

  function renderGate() {
    captchaTokens = undefined;
    swapCard(CaptchaGate(captchaLayers, (tokens, stack) => {
      captchaTokens = tokens;
      gateStack = stack;
      renderPassword();
    }));
  }

  function renderPassword() {
    const user = TextInput({ label: "Email or username", icon: "user", autofocus: true, onEnter: submit });
    const pass = TextInput({ label: "Password", icon: "key", type: "password", onEnter: submit });
    const btn = Button({ label: "Sign in", variant: "primary", icon: "right-to-bracket", block: true, onClick: submit });

    async function submit() {
      if (!user.value || !pass.value) return;
      btn.disabled = true;
      try {
        const res = await auth.login(user.value, pass.value, captchaTokens);
        if (res.data.complete) {
          await finishLogin();
        } else {
          renderCheckpoint(res.data.confirmation_token!);
        }
      } catch (err) {
        const message = String((err as Error).message);
        btn.disabled = false;
        if (captchaLayers.length && /captcha/i.test(message)) {
          // Token expired or rejected — run the gate again.
          notify.error(message);
          gateStack?.reset();
          renderGate();
          return;
        }
        pass.setError(message);
      }
    }

    const verifiedNote = captchaLayers.length
      ? el("div.rst-connect__footer.faint", icon("circle-check", { class: "rst-captcha__ok" }),
          el("span", {}, `Browser verified (${captchaLayers.length} layer${captchaLayers.length > 1 ? "s" : ""})`))
      : null;

    const passkeyBtn = Button({ label: "Sign in with a passkey", variant: "default", icon: "fingerprint", block: true, onClick: passkeySignIn });
    async function passkeySignIn() {
      passkeyBtn.disabled = true;
      try {
        const { session, publicKey } = await passkeyLogin.begin();
        const assertion = await getPasskey(publicKey);
        await passkeyLogin.finish(session, assertion);
        await finishLogin();
      } catch (err) {
        passkeyBtn.disabled = false;
        const message = String((err as Error).message);
        // A user cancelling the OS prompt is not an error worth shouting about.
        if (!/cancel|abort|NotAllowed/i.test(message)) notify.error(message);
      }
    }

    const next = el("div.rst-connect__card",
      brand(),
      el("div.rst-connect__form", user.el, pass.el, btn),
      passkeysSupported()
        ? el("div.col", { style: { gap: "var(--sp-3)" } }, el("div.rst-connect__or", el("span", "or")), passkeyBtn)
        : null,
      el("button.rst-connect__link", { onclick: renderForgot }, "Forgot your password?"),
      el("button.rst-connect__link", { onclick: renderRegister }, "Create an account"),
      options.onBack ? el("button.rst-connect__link", { onclick: options.onBack }, "← Back to home") : null,
      verifiedNote ?? footer(),
    );
    swapCard(next);
    user.focus();
  }

  function renderCheckpoint(token: string) {
    let useRecovery = false;
    const code = TextInput({ label: "Authentication code", icon: "shield-halved", placeholder: "000000", autofocus: true, onEnter: submit });
    const btn = Button({ label: "Verify", variant: "primary", icon: "check", block: true, onClick: submit });
    const toggle = el("button.rst-connect__link", {
      onclick: () => {
        useRecovery = !useRecovery;
        code.input.placeholder = useRecovery ? "recovery token" : "000000";
        toggle.textContent = useRecovery ? "Use an authenticator code instead" : "Use a recovery token instead";
      },
    }, "Use a recovery token instead");

    async function submit() {
      btn.disabled = true;
      try {
        await auth.checkpoint(token, code.value.trim(), useRecovery);
        await finishLogin();
      } catch (err) {
        code.setError(String((err as Error).message));
        btn.disabled = false;
      }
    }

    swapCard(el("div.rst-connect__card",
      brand("Two-factor authentication"),
      el("div.rst-connect__form", code.el, btn, toggle),
      el("button.rst-connect__link", { onclick: () => (captchaLayers.length ? renderGate() : renderPassword()) }, "← Back to login"),
    ));
    code.focus();
  }

  function renderForgot() {
    const email = TextInput({ label: "Account email", icon: "envelope", autofocus: true, onEnter: submit });
    const btn = Button({ label: "Request reset token", variant: "primary", icon: "paper-plane", block: true, onClick: submit });

    async function submit() {
      try {
        await auth.forgot(email.value);
        notify.success("If that account exists, a reset token was issued (check the panel log).");
        renderReset(email.value);
      } catch (err) {
        email.setError(String((err as Error).message));
      }
    }

    swapCard(el("div.rst-connect__card",
      brand("Reset your password"),
      el("div.rst-connect__form", email.el, btn),
      el("button.rst-connect__link", { onclick: renderPassword }, "← Back to login"),
    ));
  }

  function renderReset(emailValue: string) {
    const email = TextInput({ label: "Account email", icon: "envelope", value: emailValue });
    const token = TextInput({ label: "Reset token", icon: "ticket" });
    const pass = TextInput({ label: "New password", icon: "key", type: "password", onEnter: submit });
    const btn = Button({ label: "Set new password", variant: "primary", icon: "rotate", block: true, onClick: submit });

    async function submit() {
      try {
        await auth.reset(email.value, token.value.trim(), pass.value);
        notify.success("Password updated");
        await finishLogin();
      } catch (err) {
        pass.setError(String((err as Error).message));
      }
    }

    swapCard(el("div.rst-connect__card",
      brand("Enter your reset token"),
      el("div.rst-connect__form", email.el, token.el, pass.el, btn),
      el("button.rst-connect__link", { onclick: renderPassword }, "← Back to login"),
    ));
  }

  function brand(tag = "Game server management, reimagined in Go + TypeScript") {
    return el("div.rst-connect__brand",
      el("div.rst-connect__logo", icon("feather-pointed")),
      el("div",
        el("div.rst-connect__name", store.appName.value),
        el("div.rst-connect__tag.faint", tag),
      ),
    );
  }

  function footer() {
    return el("div.rst-connect__footer.faint",
      icon("lock"),
      el("span", {}, "Session-cookie auth · rate-limited · one Go binary behind this page"),
    );
  }

  function renderRegister() {
    const first = TextInput({ label: "First name", icon: "user", autofocus: true });
    const last = TextInput({ label: "Last name", icon: "user" });
    const username = TextInput({ label: "Username", icon: "at" });
    const email = TextInput({ label: "Email", icon: "envelope", type: "email" });
    const pass = TextInput({ label: "Password", icon: "key", type: "password", hint: "At least 8 characters.", onEnter: submit });
    const btn = Button({ label: "Create account", variant: "primary", icon: "user-plus", block: true, onClick: submit });

    async function submit() {
      if (!email.value || !username.value || pass.value.length < 8) {
        pass.setError("Fill in every field; password needs 8+ characters.");
        return;
      }
      btn.disabled = true;
      try {
        await auth.register({
          email: email.value, username: username.value,
          first_name: first.value, last_name: last.value, password: pass.value,
        });
        await finishLogin();
      } catch (err) {
        pass.setError(String((err as Error).message));
        btn.disabled = false;
      }
    }

    swapCard(el("div.rst-connect__card",
      brand("Create your account"),
      el("div.rst-connect__form",
        el("div.rst-connect__row", first.el, last.el),
        username.el, email.el, pass.el, btn,
      ),
      el("button.rst-connect__link", { onclick: renderPassword }, "Already have an account? Sign in"),
      options.onBack ? el("button.rst-connect__link", { onclick: options.onBack }, "← Back to home") : null,
    ));
    first.focus();
  }

  if (options.mode === "register") renderRegister();
  else renderPassword();
  fetchCaptchaLayers()
    .then((layers) => {
      if (layers.length) {
        captchaLayers = layers;
        renderGate();
      }
    })
    .catch(() => { /* captcha endpoint unavailable — proceed without */ });

  return shell;
}
