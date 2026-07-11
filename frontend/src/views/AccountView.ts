import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { TextInput, TextArea } from "../components/TextInput.ts";
import { Tabs } from "../components/Tabs.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge } from "../components/misc.ts";
import { client, unwrap } from "../api/client.ts";
import { store } from "../state/store.ts";
import { page, timeAgo, localTime } from "./shared.ts";
import { passkeysSupported, createPasskey } from "../util/webauthn.ts";

export function AccountView(tab?: string): HTMLElement {
  const tabs = Tabs([
    { id: "settings", label: "Settings", icon: "user-gear", render: SettingsTab },
    { id: "2fa", label: "Two-factor", icon: "shield-halved", render: TwoFactorTab },
    { id: "passkeys", label: "Passkeys", icon: "fingerprint", render: PasskeysTab },
    { id: "api", label: "API keys", icon: "key", render: ApiKeysTab },
    { id: "ssh", label: "SSH keys", icon: "terminal", render: SshKeysTab },
    { id: "activity", label: "Activity", icon: "clock-rotate-left", render: ActivityTab },
  ], { active: tab && ["settings", "2fa", "passkeys", "api", "ssh", "activity"].includes(tab) ? tab : "settings" });

  const u = store.user.peek()!;
  return page("Account", { icon: "circle-user", sub: `${u.email} · ${u.admin ? "administrator" : "user"}` }, tabs.el);
}

function SettingsTab(): HTMLElement {
  const u = store.user.peek()!;

  const email = TextInput({ label: "New email address", icon: "envelope", value: u.email });
  const emailPw = TextInput({ label: "Current password", icon: "key", type: "password" });
  const emailBtn = Button({
    label: "Update email", variant: "primary", icon: "check",
    onClick: async () => {
      try {
        await client.accountApi.updateEmail(email.value, emailPw.value);
        notify.success("Email updated");
        store.user.value = { ...u, email: email.value };
      } catch (err) { notify.error(String((err as Error).message)); }
    },
  });

  const cur = TextInput({ label: "Current password", icon: "key", type: "password" });
  const next = TextInput({ label: "New password", icon: "lock", type: "password", hint: "At least 8 characters." });
  const pwBtn = Button({
    label: "Change password", variant: "primary", icon: "check",
    onClick: async () => {
      try {
        await client.accountApi.updatePassword(cur.value, next.value);
        notify.success("Password changed");
        cur.value = ""; next.value = "";
      } catch (err) { notify.error(String((err as Error).message)); }
    },
  });

  return el("div.col", { style: { gap: "var(--sp-4)", paddingTop: "var(--sp-4)" } },
    el("div.rst-card",
      el("div.rst-card__title", icon("envelope"), "Email address"),
      el("div.rst-form__row", email.el, emailPw.el),
      el("div.row", emailBtn),
    ),
    el("div.rst-card",
      el("div.rst-card__title", icon("lock"), "Password"),
      el("div.rst-form__row", cur.el, next.el),
      el("div.row", pwBtn),
    ),
  );
}

function TwoFactorTab(): HTMLElement {
  const root = el("div.col", { style: { gap: "var(--sp-4)", paddingTop: "var(--sp-4)" } });

  function render() {
    const u = store.user.peek()!;
    if (u["2fa_enabled"]) {
      const pw = TextInput({ label: "Current password", icon: "key", type: "password" });
      root.replaceChildren(el("div.rst-card",
        el("div.rst-card__title", icon("shield-halved"), "Two-factor authentication ", Badge("enabled", "success")),
        el("p.faint", "Codes from your authenticator app are required when signing in."),
        pw.el,
        el("div.row", Button({
          label: "Disable two-factor", variant: "danger", icon: "shield-slash" as never,
          onClick: async () => {
            try {
              await client.accountApi.twoFactorDisable(pw.value);
              store.user.value = { ...u, "2fa_enabled": false };
              notify.success("Two-factor disabled");
              render();
            } catch (err) { notify.error(String((err as Error).message)); }
          },
        })),
      ));
      return;
    }

    const setupBtn = Button({
      label: "Begin setup", variant: "primary", icon: "qrcode",
      onClick: async () => {
        const setup = await client.accountApi.twoFactorSetup();
        const code = TextInput({ label: "Code from your app", icon: "hashtag", placeholder: "000000" });
        openModal({
          title: "Enable two-factor",
          icon: "shield-halved",
          width: 480,
          body: el("div.col", { style: { gap: "var(--sp-4)" } },
            el("p.faint", "Add this secret to your authenticator app (or scan the otpauth URI), then confirm with a code."),
            el("div.rst-codeblock", setup.data.secret),
            el("div.rst-codeblock", { style: { fontSize: "11px" } }, setup.data.image_url_data),
            code.el,
          ),
          actions: [
            { label: "Cancel" },
            {
              label: "Enable", variant: "primary", closeOnClick: false,
              onClick: async () => {
                try {
                  const res = await client.accountApi.twoFactorEnable(code.value.trim(), "");
                  const tokens = (res.attributes as { tokens: string[] }).tokens;
                  store.user.value = { ...store.user.peek()!, "2fa_enabled": true };
                  openModal({
                    title: "Recovery tokens",
                    icon: "life-ring",
                    width: 480,
                    body: el("div.col", { style: { gap: "var(--sp-3)" } },
                      el("p.faint", "Store these somewhere safe — each can be used once in place of a code. They will not be shown again."),
                      el("div.rst-recovery", ...tokens.map((t) => el("code", t))),
                    ),
                    actions: [{ label: "I saved them", variant: "primary", onClick: render }],
                  });
                } catch (err) { notify.error(String((err as Error).message)); }
              },
            },
          ],
        });
      },
    });

    root.replaceChildren(el("div.rst-card",
      el("div.rst-card__title", icon("shield-halved"), "Two-factor authentication ", Badge("disabled", "outline")),
      el("p.faint", "Protect your account with time-based one-time codes (TOTP). Works with any authenticator app."),
      el("div.row", setupBtn),
    ));
  }

  render();
  return root;
}

function ApiKeysTab(): HTMLElement {
  const root = el("div.col", { style: { gap: "var(--sp-4)", paddingTop: "var(--sp-4)" } });

  async function load() {
    root.replaceChildren(LoadingState());
    const keys = unwrap<any>(await client.accountApi.apiKeys());
    const list = el("div.rst-activity");
    for (const k of keys) {
      list.appendChild(el("div.rst-activity__item",
        icon("key", { class: "faint" }),
        el("span.mono", k.identifier),
        el("span.faint", k.description),
        el("span.rst-activity__meta", `last used ${timeAgo(k.last_used_at)}`),
        Button({ icon: "trash", size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
          if (!(await confirmModal({ title: "Delete API key", message: `Delete key ${k.identifier}? Applications using it will stop working.`, danger: true }))) return;
          await client.accountApi.deleteApiKey(k.identifier);
          load();
        } }),
      ));
    }
    const desc = TextInput({ label: "Description", icon: "tag", placeholder: "What is this key for?" });
    const createBtn = Button({
      label: "Create key", variant: "primary", icon: "plus",
      onClick: async () => {
        try {
          const res = await client.accountApi.createApiKey(desc.value || "API key", []);
          openModal({
            title: "API key created",
            icon: "key",
            width: 520,
            body: el("div.col", { style: { gap: "var(--sp-3)" } },
              el("p.faint", "Copy the token now — it is only shown once. Send it as \"Authorization: Bearer <token>\" against /api/client."),
              el("div.rst-codeblock", res.meta.secret_token),
            ),
            actions: [{ label: "Done", variant: "primary", onClick: load }],
          });
        } catch (err) { notify.error(String((err as Error).message)); }
      },
    });
    root.replaceChildren(
      el("div.rst-card",
        el("div.rst-card__title", icon("plus"), "New API key"),
        el("div.rst-form__row", desc.el),
        el("div.row", createBtn),
      ),
      keys.length ? el("div.rst-card", el("div.rst-card__title", icon("key"), `Your keys (${keys.length})`), list)
        : EmptyState({ icon: "key", title: "No API keys", description: "Personal tokens for the client API (ptlc_…)." }),
    );
  }

  load();
  return root;
}

function SshKeysTab(): HTMLElement {
  const root = el("div.col", { style: { gap: "var(--sp-4)", paddingTop: "var(--sp-4)" } });

  async function load() {
    root.replaceChildren(LoadingState());
    const keys = unwrap<any>(await client.accountApi.sshKeys());
    const list = el("div.rst-activity");
    for (const k of keys) {
      list.appendChild(el("div.rst-activity__item",
        icon("terminal", { class: "faint" }),
        el("span", {}, k.name),
        el("span.mono.faint.truncate", { style: { maxWidth: "300px" } }, k.fingerprint),
        el("span.rst-activity__meta", localTime(k.created_at)),
        Button({ icon: "trash", size: "sm", variant: "ghost", title: "Remove", onClick: async () => {
          if (!(await confirmModal({ title: "Remove SSH key", message: `Remove "${k.name}"? SFTP logins with it will stop working.`, danger: true }))) return;
          await client.accountApi.deleteSshKey(k.fingerprint);
          load();
        } }),
      ));
    }
    const name = TextInput({ label: "Name", icon: "tag" });
    const key = TextArea({ placeholder: "ssh-ed25519 AAAA… you@host", rows: 3, mono: true });
    root.replaceChildren(
      el("div.rst-card",
        el("div.rst-card__title", icon("plus"), "Add a public key"),
        name.el,
        key,
        el("div.row", Button({
          label: "Add key", variant: "primary", icon: "plus",
          onClick: async () => {
            try {
              await client.accountApi.createSshKey(name.value || "key", key.value);
              notify.success("SSH key added");
              load();
            } catch (err) { notify.error(String((err as Error).message)); }
          },
        })),
      ),
      keys.length ? el("div.rst-card", el("div.rst-card__title", icon("terminal"), `Your keys (${keys.length})`), list)
        : EmptyState({ icon: "terminal", title: "No SSH keys", description: "Used for SFTP access to your servers." }),
    );
  }

  load();
  return root;
}

function PasskeysTab(): HTMLElement {
  const root = el("div.col", { style: { gap: "var(--sp-4)", paddingTop: "var(--sp-4)" } });

  async function addPasskey() {
    if (!passkeysSupported()) {
      notify.error("This browser doesn't support passkeys.");
      return;
    }
    const name = TextInput({ label: "Name", icon: "tag", placeholder: "e.g. iPhone, YubiKey" });
    const ok = await confirmModal({
      title: "Add a passkey", icon: "fingerprint", confirmLabel: "Continue",
      message: el("div.col", { style: { gap: "var(--sp-3)" } },
        el("p.faint", "Give this passkey a name, then follow your browser or device prompt to create it."),
        name.el,
      ),
    });
    if (!ok) return;
    try {
      const { session, publicKey } = await client.accountApi.passkeyRegisterBegin();
      const attestation = await createPasskey(publicKey);
      await client.accountApi.passkeyRegisterFinish(session, name.value.trim() || "Passkey", attestation);
      notify.success("Passkey added");
      load();
    } catch (err) {
      const message = String((err as Error).message);
      if (!/cancel|abort|NotAllowed/i.test(message)) notify.error(message);
    }
  }

  async function load() {
    root.replaceChildren(LoadingState());
    const keys = unwrap<any>(await client.accountApi.passkeys());
    const list = el("div.rst-activity");
    for (const k of keys) {
      list.appendChild(el("div.rst-activity__item",
        icon("fingerprint", { class: "faint" }),
        el("span", {}, k.name),
        k.backed_up ? Badge("synced", "info") : null,
        el("span.rst-activity__meta", k.last_used_at ? `used ${timeAgo(k.last_used_at)}` : "never used"),
        Button({ icon: "pen", size: "sm", variant: "ghost", title: "Rename", onClick: async () => {
          const name = TextInput({ label: "Name", icon: "tag", value: k.name });
          if (!(await confirmModal({ title: "Rename passkey", confirmLabel: "Save", message: name.el }))) return;
          await client.accountApi.renamePasskey(k.id, name.value.trim() || k.name);
          load();
        } }),
        Button({ icon: "trash", size: "sm", variant: "ghost", title: "Remove", onClick: async () => {
          if (!(await confirmModal({ title: "Remove passkey", message: `Remove "${k.name}"? You won't be able to sign in with it anymore.`, danger: true }))) return;
          await client.accountApi.deletePasskey(k.id);
          load();
        } }),
      ));
    }
    root.replaceChildren(
      el("div.rst-card",
        el("div.rst-card__title", icon("fingerprint"), "Passkeys"),
        el("p.faint", { style: { marginBottom: "var(--sp-3)" } }, "Sign in without a password using your device's biometrics or a security key. A passkey can also act as your second factor."),
        el("div.row", Button({ label: "Add a passkey", variant: "primary", icon: "plus", onClick: addPasskey })),
      ),
      keys.length ? el("div.rst-card", el("div.rst-card__title", icon("fingerprint"), `Your passkeys (${keys.length})`), list)
        : EmptyState({ icon: "fingerprint", title: "No passkeys yet", description: "Add one to enable passwordless sign-in." }),
    );
  }

  load();
  return root;
}

function ActivityTab(): HTMLElement {
  const root = el("div", { style: { paddingTop: "var(--sp-4)" } }, LoadingState());
  client.accountApi.activity().then((res) => {
    root.replaceChildren(ActivityList(unwrap<any>(res)));
  });
  return root;
}

/** Shared renderer for activity_log lists. */
export function ActivityList(logs: Array<{ event: string; ip: string; properties: Record<string, unknown>; timestamp: string }>): HTMLElement {
  if (!logs.length) return EmptyState({ icon: "clock-rotate-left", title: "No activity yet" });
  const list = el("div.rst-activity");
  for (const l of logs) {
    const props = Object.entries(l.properties ?? {}).map(([k, v]) => `${k}=${String(v)}`).join(" ");
    list.appendChild(el("div.rst-activity__item",
      icon(l.event.startsWith("auth:") ? "right-to-bracket" : l.event.includes("power") ? "power-off" : "circle-info", { class: "faint" }),
      el("span.rst-activity__event", l.event),
      props ? el("span.rst-activity__props", props) : null,
      el("span.rst-activity__meta", `${l.ip} · ${localTime(l.timestamp)}`),
    ));
  }
  return el("div.rst-card", list);
}
