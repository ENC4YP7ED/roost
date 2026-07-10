import { el, icon, clear } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { Select } from "../components/Select.ts";
import { DataGrid } from "../components/DataGrid.ts";
import { Badge, LoadingState, EmptyState } from "../components/misc.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { api } from "../api/client.ts";
import type { UserMeta } from "../api/types.ts";

/** Account & privilege management. */
export function UsersView(): HTMLElement {
  const root = el("div.gtma-page.grow");
  load();

  async function load() {
    clear(root);
    root.appendChild(LoadingState("Reading accounts…"));
    try {
      const users = await api.users();
      clear(root);
      root.appendChild(build(users));
    } catch (err) {
      clear(root);
      root.appendChild(EmptyState({ icon: "triangle-exclamation", title: "Could not load users", description: String(err) }));
    }
  }

  function build(users: UserMeta[]): HTMLElement {
    const grid = DataGrid({
      columns: [{ name: "User" }, { name: "Host" }, { name: "Privileges" }],
      rows: users.map((u) => [u.user || "(anonymous)", u.host, u.superUser ? "SUPER" : "—"]),
      rowMenu: (ri) => {
        const u = users[ri];
        return [
          { header: `${u.user}@${u.host}` },
          { label: "Show grants", icon: "key", onSelect: () => showGrants(u) },
          { separator: true },
          { label: "Drop user", icon: "trash", danger: true, onSelect: () => dropUser(u) },
        ];
      },
    });

    return el("div.gtma-page__inner",
      el("div.gtma-page__head",
        el("div.col.gap-1",
          el("div.row.gap-2.muted", icon("users"), el("span", {}, "Accounts")),
          el("h1.gtma-page__title", "Users & privileges"),
        ),
        el("div.row.gap-2",
          Badge(`${users.length} accounts`, "outline"),
          Button({ label: "New user", icon: "user-plus", variant: "primary", onClick: newUser }),
        ),
      ),
      grid.el,
    );
  }

  async function showGrants(u: UserMeta) {
    let grants: string[] = [];
    try { grants = await api.grants(u.user, u.host); }
    catch (err) { notify.error(String(err)); return; }
    openModal({
      title: `Grants · ${u.user}@${u.host}`,
      icon: "key",
      width: 640,
      body: el("div.gtma-grants",
        grants.length
          ? el("pre.gtma-grants__code.mono", {}, grants.map((g) => g + ";").join("\n"))
          : EmptyState({ icon: "ban", title: "No grants" }),
      ),
      actions: [{ label: "Close", variant: "ghost" }],
    });
  }

  async function dropUser(u: UserMeta) {
    const ok = await confirmModal({
      title: "Drop user",
      danger: true,
      confirmLabel: "Drop",
      message: el("div", "Permanently remove account ", el("b.mono", {}, `${u.user}@${u.host}`), "?"),
    });
    if (!ok) return;
    try {
      await api.dropUser(u.user, u.host);
      notify.success(`Dropped ${u.user}@${u.host}`);
      load();
    } catch (err) {
      notify.error(String(err));
    }
  }

  function newUser() {
    const userI = TextInput({ label: "Username", icon: "user", autofocus: true, placeholder: "appuser" });
    const hostI = TextInput({ label: "Host", icon: "globe", value: "%" });
    const passI = TextInput({ label: "Password", icon: "lock", type: "password" });
    const privSel = Select({
      options: [
        { value: "", label: "No privileges (USAGE)", icon: "ban" },
        { value: "ALL PRIVILEGES", label: "ALL PRIVILEGES", icon: "crown" },
        { value: "SELECT", label: "SELECT (read-only)", icon: "eye" },
        { value: "SELECT, INSERT, UPDATE, DELETE", label: "CRUD (no DDL)", icon: "pen" },
      ],
      value: "",
    });
    const scopeI = TextInput({ label: "Scope", icon: "bullseye", value: "*.*", hint: "e.g. *.*  or  `mydb`.*" });

    const modal = openModal({
      title: "New user",
      icon: "user-plus",
      width: 460,
      body: el("div.col.gap-3",
        el("div.row.gap-3", el("div.grow", userI.el), el("div", { style: { width: "130px" } }, hostI.el)),
        passI.el,
        el("div.gtma-input__label", "Privileges"),
        privSel.el,
        scopeI.el,
      ),
      actions: [
        { label: "Cancel", variant: "ghost" },
        {
          label: "Create", variant: "primary", icon: "check", closeOnClick: false,
          onClick: async () => {
            const user = userI.value.trim();
            if (!user) { userI.setError("Required"); return; }
            try {
              await api.createUser({
                user, host: hostI.value.trim() || "%", password: passI.value,
                privileges: privSel.value, scope: scopeI.value.trim(),
              });
              notify.success(`Created ${user}@${hostI.value.trim() || "%"}`);
              load();
              modal.close();
            } catch (err) {
              userI.setError(String(err));
            }
          },
        },
      ],
    });
  }

  return root;
}
