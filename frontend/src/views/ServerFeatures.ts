import { el, icon } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { TextInput, TextArea } from "../components/TextInput.ts";
import { Select } from "../components/Select.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge, Switch, Field } from "../components/misc.ts";
import { client, unwrap } from "../api/client.ts";
import { page, localTime, timeAgo } from "./shared.ts";
import { formatBytes } from "../util/format.ts";
import { ActivityList } from "./AccountView.ts";
import type { ServerCtx } from "./ServerView.ts";

// ---------------------------------------------------------------- databases

export function DatabasesTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const dbs = unwrap<any>(await client.databases.list(ctx.id));
    if (!dbs.length) {
      body.replaceChildren(EmptyState({ icon: "database", title: "No databases", description: `Limit: ${ctx.attrs.feature_limits.databases}` }));
      return;
    }
    const cards = dbs.map((d) => {
      const pw = d.relationships?.password?.attributes?.password ?? "••••••••";
      return el("div.rst-card",
        el("div.rst-card__title", icon("database"), d.name),
        el("dl.rst-kv",
          el("dt", "Host"), el("dd.mono", d.host ? `${d.host.address}:${d.host.port}` : "—"),
          el("dt", "Username"), el("dd.mono", d.username),
          el("dt", "Password"), el("dd.mono", pw),
          el("dt", "Connections from"), el("dd.mono", d.connections_from),
        ),
        el("div.row",
          ctx.can("database.update") ? Button({ label: "Rotate password", icon: "rotate", size: "sm", onClick: async () => { await client.databases.rotate(ctx.id, d.id); notify.success("Password rotated"); load(); } }) : null,
          ctx.can("database.delete") ? Button({ label: "Delete", icon: "trash", size: "sm", variant: "danger", onClick: async () => {
            if (await confirmModal({ title: "Delete database", message: `Delete ${d.name}? This cannot be undone.`, danger: true })) {
              await client.databases.remove(ctx.id, d.id); load();
            }
          } }) : null,
        ),
      );
    });
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...cards));
  }

  const createBtn = ctx.can("database.create") ? Button({
    label: "New database", icon: "plus", variant: "primary", size: "sm",
    onClick: () => {
      const name = TextInput({ label: "Database name", autofocus: true });
      const remote = TextInput({ label: "Connections from", value: "%" });
      openModal({
        title: "Create database", icon: "database",
        body: el("div.rst-form", name.el, remote.el),
        actions: [
          { label: "Cancel" },
          { label: "Create", variant: "primary", onClick: async () => {
            try { await client.databases.create(ctx.id, name.value, remote.value); load(); }
            catch (err) { notify.error(String((err as Error).message)); }
          } },
        ],
      });
    },
  }) : null;

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Databases", { icon: "database", actions: createBtn ? [createBtn] : [] }, body);
}

// ---------------------------------------------------------------- schedules

export function SchedulesTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const schedules = unwrap<any>(await client.schedules.list(ctx.id));
    if (!schedules.length) {
      body.replaceChildren(EmptyState({ icon: "calendar-days", title: "No schedules", description: "Automate commands, power actions and backups on a cron cadence." }));
      return;
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...schedules.map(renderSchedule)));
  }

  function cronText(s: any): string {
    return `${s.cron.minute} ${s.cron.hour} ${s.cron.day_of_month} ${s.cron.month} ${s.cron.day_of_week}`;
  }

  function renderSchedule(s: any): HTMLElement {
    const tasks = (s.relationships?.tasks?.data ?? []).map((t: any) => t.attributes);
    const taskList = el("div.rst-activity");
    for (const t of tasks) {
      taskList.appendChild(el("div.rst-activity__item",
        icon(t.action === "power" ? "power-off" : t.action === "backup" ? "box-archive" : "terminal", { class: "faint" }),
        el("span.mono", t.action),
        el("span.faint.truncate", t.payload || "—"),
        t.time_offset ? el("span.faint", `+${t.time_offset}s`) : null,
        el("span.rst-activity__meta"),
        ctx.can("schedule.update") ? IconButton("pen", { size: "sm", variant: "ghost", title: "Edit task", onClick: () => taskModal(s, t) }) : null,
        ctx.can("schedule.update") ? IconButton("trash", { size: "sm", variant: "ghost", title: "Delete task", onClick: async () => {
          await client.schedules.removeTask(ctx.id, s.id, t.id); load();
        } }) : null,
      ));
    }
    return el("div.rst-card",
      el("div.rst-section__head",
        el("div.rst-card__title", icon("calendar-days"), s.name, " ",
          Badge(s.is_active ? "active" : "paused", s.is_active ? "success" : "outline"),
          s.is_processing ? Badge("running", "warning") : null),
        el("div.row",
          ctx.can("schedule.update") ? Button({ label: "Run now", icon: "play", size: "sm", onClick: async () => { await client.schedules.execute(ctx.id, s.id); notify.success("Schedule triggered"); } }) : null,
          ctx.can("schedule.update") ? Button({ label: "New task", icon: "plus", size: "sm", onClick: () => taskModal(s, null) }) : null,
          ctx.can("schedule.update") ? IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => scheduleModal(s) }) : null,
          ctx.can("schedule.delete") ? IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
            if (await confirmModal({ title: "Delete schedule", message: `Delete "${s.name}"?`, danger: true })) {
              await client.schedules.remove(ctx.id, s.id); load();
            }
          } }) : null,
        ),
      ),
      el("dl.rst-kv",
        el("dt", "Cron"), el("dd.mono", cronText(s)),
        el("dt", "Last run"), el("dd", timeAgo(s.last_run_at)),
        el("dt", "Next run"), el("dd", s.next_run_at ? localTime(s.next_run_at) : "—"),
      ),
      tasks.length ? taskList : el("p.faint", "No tasks yet."),
    );
  }

  function scheduleModal(existing: any | null) {
    const name = TextInput({ label: "Name", value: existing?.name ?? "", autofocus: true });
    const minute = TextInput({ label: "Minute", value: existing?.cron.minute ?? "*/5" });
    const hour = TextInput({ label: "Hour", value: existing?.cron.hour ?? "*" });
    const dom = TextInput({ label: "Day (month)", value: existing?.cron.day_of_month ?? "*" });
    const month = TextInput({ label: "Month", value: existing?.cron.month ?? "*" });
    const dow = TextInput({ label: "Day (week)", value: existing?.cron.day_of_week ?? "*" });
    let active = existing?.is_active ?? true;
    let onlyOnline = existing?.only_when_online ?? false;
    openModal({
      title: existing ? "Edit schedule" : "New schedule", icon: "calendar-days", width: 560,
      body: el("div.rst-form",
        name.el,
        el("div.rst-form__row", minute.el, hour.el, dom.el, month.el, dow.el),
        el("div.row",
          Switch({ checked: active, label: "Active", onChange: (v) => { active = v; } }),
          Switch({ checked: onlyOnline, label: "Only when online", onChange: (v) => { onlyOnline = v; } }),
        ),
      ),
      actions: [
        { label: "Cancel" },
        { label: existing ? "Save" : "Create", variant: "primary", onClick: async () => {
          const body = {
            name: name.value, minute: minute.value, hour: hour.value,
            day_of_month: dom.value, month: month.value, day_of_week: dow.value,
            is_active: active, only_when_online: onlyOnline,
          };
          try {
            if (existing) await client.schedules.update(ctx.id, existing.id, body);
            else await client.schedules.create(ctx.id, body);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  function taskModal(schedule: any, existing: any | null) {
    const action = Select({
      options: [
        { value: "command", label: "Send command", icon: "terminal" },
        { value: "power", label: "Power action", icon: "power-off" },
        { value: "backup", label: "Create backup", icon: "box-archive" },
      ],
      value: existing?.action ?? "command",
    });
    const payload = TextInput({ label: "Payload", value: existing?.payload ?? "", hint: "Command text, power signal (start/stop/restart/kill), or ignored files for backups." });
    const offset = TextInput({ label: "Delay (seconds)", value: String(existing?.time_offset ?? 0), type: "number" });
    openModal({
      title: existing ? "Edit task" : "New task", icon: "list-check",
      body: el("div.rst-form", Field("Action", action.el), payload.el, offset.el),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const body = { action: action.value, payload: payload.value, time_offset: Number(offset.value) || 0 };
          try {
            if (existing) await client.schedules.updateTask(ctx.id, schedule.id, existing.id, body);
            else await client.schedules.createTask(ctx.id, schedule.id, body);
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  const newBtn = ctx.can("schedule.create")
    ? Button({ label: "New schedule", icon: "plus", variant: "primary", size: "sm", onClick: () => scheduleModal(null) })
    : null;

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Schedules", { icon: "calendar-days", actions: newBtn ? [newBtn] : [] }, body);
}

// ---------------------------------------------------------------- subusers

export function SubusersTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());
  let allPermissions: string[] = [];

  async function load() {
    if (!allPermissions.length) {
      allPermissions = (await client.permissions()).attributes.permissions.filter((p) => p !== "websocket.connect");
    }
    const subs = unwrap<any>(await client.subusers.list(ctx.id));
    if (!subs.length) {
      body.replaceChildren(EmptyState({ icon: "users", title: "No subusers", description: "Invite others to help manage this server with granular permissions." }));
      return;
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...subs.map((sub) =>
      el("div.rst-card",
        el("div.rst-section__head",
          el("div.rst-card__title", icon("user"), sub.username, " ", el("span.faint", sub.email),
            sub["2fa_enabled"] ? Badge("2FA", "success") : null),
          el("div.row",
            ctx.can("user.update") ? Button({ label: "Permissions", icon: "user-lock", size: "sm", onClick: () => permsModal(sub) }) : null,
            ctx.can("user.delete") ? IconButton("trash", { size: "sm", variant: "ghost", title: "Remove", onClick: async () => {
              if (await confirmModal({ title: "Remove subuser", message: `Remove ${sub.email} from this server?`, danger: true })) {
                await client.subusers.remove(ctx.id, sub.uuid); load();
              }
            } }) : null,
          ),
        ),
        el("p.faint", `${(sub.permissions as string[]).length} permission(s) · added ${timeAgo(sub.created_at)}`),
      ),
    )));
  }

  function permGrid(selected: Set<string>): HTMLElement {
    const grid = el("div.rst-perms");
    for (const p of allPermissions) {
      const cell = el("button.rst-perm", {
        class: selected.has(p) ? "is-on" : "",
        onclick: () => {
          if (selected.has(p)) { selected.delete(p); cell.classList.remove("is-on"); }
          else { selected.add(p); cell.classList.add("is-on"); }
        },
      }, icon(selected.has(p) ? "check" : "minus"), el("span", {}, p));
      grid.appendChild(cell);
    }
    return grid;
  }

  function permsModal(sub: any) {
    const selected = new Set<string>((sub.permissions as string[]).filter((p) => p !== "websocket.connect"));
    openModal({
      title: `Permissions — ${sub.email}`, icon: "user-lock", width: 760,
      body: permGrid(selected),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          await client.subusers.update(ctx.id, sub.uuid, [...selected]);
          notify.success("Permissions updated");
          load();
        } },
      ],
    });
  }

  const inviteBtn = ctx.can("user.create") ? Button({
    label: "Invite user", icon: "user-plus", variant: "primary", size: "sm",
    onClick: () => {
      const email = TextInput({ label: "Email address", icon: "envelope", autofocus: true });
      const selected = new Set<string>(["control.console", "control.start", "control.stop", "control.restart"]);
      openModal({
        title: "Invite subuser", icon: "user-plus", width: 760,
        body: el("div.rst-form", email.el, permGrid(selected)),
        actions: [
          { label: "Cancel" },
          { label: "Invite", variant: "primary", onClick: async () => {
            try { await client.subusers.create(ctx.id, email.value, [...selected]); load(); }
            catch (err) { notify.error(String((err as Error).message)); }
          } },
        ],
      });
    },
  }) : null;

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Users", { icon: "users", actions: inviteBtn ? [inviteBtn] : [] }, body);
}

// ---------------------------------------------------------------- backups

export function BackupsTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const backups = unwrap<any>(await client.backups.list(ctx.id));
    if (!backups.length) {
      body.replaceChildren(EmptyState({ icon: "box-archive", title: "No backups", description: `Limit: ${ctx.attrs.feature_limits.backups}` }));
      return;
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...backups.map((b) =>
      el("div.rst-card",
        el("div.rst-section__head",
          el("div.rst-card__title",
            icon(b.is_locked ? "lock" : "box-archive"), b.name, " ",
            b.completed_at
              ? Badge(b.is_successful ? "complete" : "failed", b.is_successful ? "success" : "danger")
              : Badge("in progress", "warning"),
          ),
          el("div.row",
            ctx.can("backup.download") && b.completed_at && b.is_successful ? Button({ label: "Download", icon: "download", size: "sm", onClick: async () => {
              const res = await client.backups.downloadURL(ctx.id, b.uuid);
              window.open((res.attributes as { url: string }).url, "_blank");
            } }) : null,
            ctx.can("backup.restore") && b.completed_at && b.is_successful ? Button({ label: "Restore", icon: "clock-rotate-left", size: "sm", onClick: async () => {
              if (await confirmModal({ title: "Restore backup", message: "Restore this backup? Current files will be overwritten.", danger: true })) {
                await client.backups.restore(ctx.id, b.uuid, false);
                notify.success("Restore started");
              }
            } }) : null,
            ctx.can("backup.delete") ? IconButton(b.is_locked ? "lock-open" : "lock", { size: "sm", variant: "ghost", title: b.is_locked ? "Unlock" : "Lock", onClick: async () => { await client.backups.lock(ctx.id, b.uuid); load(); } }) : null,
            ctx.can("backup.delete") && !b.is_locked ? IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
              if (await confirmModal({ title: "Delete backup", message: `Delete "${b.name}"?`, danger: true })) {
                await client.backups.remove(ctx.id, b.uuid); load();
              }
            } }) : null,
          ),
        ),
        el("dl.rst-kv",
          el("dt", "Size"), el("dd.mono", b.bytes ? formatBytes(b.bytes) : "—"),
          el("dt", "Checksum"), el("dd.mono.truncate", b.checksum ?? "—"),
          el("dt", "Created"), el("dd", localTime(b.created_at)),
        ),
      ),
    )));
  }

  const createBtn = ctx.can("backup.create") ? Button({
    label: "Create backup", icon: "plus", variant: "primary", size: "sm",
    onClick: () => {
      const name = TextInput({ label: "Name (optional)" });
      const ignored = TextArea({ placeholder: "Ignored files/directories, one per line", rows: 3, mono: true });
      openModal({
        title: "Create backup", icon: "box-archive",
        body: el("div.rst-form", name.el, ignored),
        actions: [
          { label: "Cancel" },
          { label: "Start backup", variant: "primary", onClick: async () => {
            try { await client.backups.create(ctx.id, { name: name.value, ignored: ignored.value }); load(); }
            catch (err) { notify.error(String((err as Error).message)); }
          } },
        ],
      });
    },
  }) : null;

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Backups", { icon: "box-archive", actions: createBtn ? [createBtn] : [] }, body);
}

// ---------------------------------------------------------------- network

export function NetworkTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const allocs = unwrap<any>(await client.network.list(ctx.id));
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...allocs.map((a) =>
      el("div.rst-card",
        el("div.rst-section__head",
          el("div.rst-card__title",
            icon("network-wired"),
            el("span.mono", `${a.alias || a.ip}:${a.port}`), " ",
            a.is_default ? Badge("primary", "success") : null),
          el("div.row",
            !a.is_default && ctx.can("allocation.update") ? Button({ label: "Make primary", icon: "star", size: "sm", onClick: async () => { await client.network.primary(ctx.id, a.id); load(); } }) : null,
            !a.is_default && ctx.can("allocation.delete") ? IconButton("trash", { size: "sm", variant: "ghost", title: "Remove", onClick: async () => {
              try { await client.network.remove(ctx.id, a.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
            } }) : null,
          ),
        ),
        el("p.faint", a.notes || "No notes."),
      ),
    )));
  }

  const addBtn = ctx.can("allocation.create") ? Button({
    label: "Add allocation", icon: "plus", variant: "primary", size: "sm",
    onClick: async () => {
      try { await client.network.create(ctx.id); notify.success("Allocation added"); load(); }
      catch (err) { notify.error(String((err as Error).message)); }
    },
  }) : null;

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Network", { icon: "network-wired", actions: addBtn ? [addBtn] : [], sub: `Allocation limit: ${ctx.attrs.feature_limits.allocations}` }, body);
}

// ---------------------------------------------------------------- startup

export function StartupTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const res = await client.startup.get(ctx.id);
    const vars = unwrap<any>(res);
    const meta = (res as any).meta ?? {};
    const cards: HTMLElement[] = [
      el("div.rst-card",
        el("div.rst-card__title", icon("terminal"), "Startup command"),
        el("div.rst-codeblock", meta.startup_command ?? ctx.attrs.invocation),
      ),
    ];
    for (const v of vars) {
      const input = TextInput({ value: String(v.server_value ?? ""), disabled: !v.is_editable || !ctx.can("startup.update") });
      cards.push(el("div.rst-card",
        el("div.rst-section__head",
          el("div.rst-card__title", v.name, " ", el("span.mono.faint", `{{${v.env_variable}}}`)),
          v.is_editable && ctx.can("startup.update") ? Button({
            label: "Save", icon: "check", size: "sm",
            onClick: async () => {
              try { await client.startup.setVariable(ctx.id, v.env_variable, input.value); notify.success(`${v.env_variable} saved`); }
              catch (err) { notify.error(String((err as Error).message)); }
            },
          }) : Badge("read-only", "outline"),
        ),
        v.description ? el("p.faint", v.description) : null,
        input.el,
        v.rules ? el("p.faint.mono", { style: { fontSize: "11px" } }, v.rules) : null,
      ));
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...cards));
  }

  load().catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Startup", { icon: "rocket" }, body);
}

// ---------------------------------------------------------------- settings

export function SettingsTab(ctx: ServerCtx): HTMLElement {
  const cards: HTMLElement[] = [];

  if (ctx.can("settings.rename")) {
    const name = TextInput({ label: "Server name", value: ctx.attrs.name });
    const desc = TextArea({ placeholder: "Description", rows: 2, value: ctx.attrs.description ?? "" });
    cards.push(el("div.rst-card",
      el("div.rst-card__title", icon("pen"), "Rename"),
      name.el, desc,
      el("div.row", Button({ label: "Save", variant: "primary", icon: "check", onClick: async () => {
        try { await client.settings.rename(ctx.id, name.value, desc.value); notify.success("Server renamed"); }
        catch (err) { notify.error(String((err as Error).message)); }
      } })),
    ));
  }

  if (ctx.can("startup.docker-image")) {
    const images = Object.entries(ctx.attrs.docker_image ? { current: ctx.attrs.docker_image } : {});
    client.startup.get(ctx.id).then((res) => {
      const meta = (res as any).meta ?? {};
      const opts = Object.entries(meta.docker_images ?? {}).map(([label, img]) => ({ value: String(img), label: `${label} (${img})` }));
      if (!opts.length) return;
      const sel = Select({ options: opts, value: ctx.attrs.docker_image });
      imageCard.replaceChildren(
        el("div.rst-card__title", icon("docker", { brand: true }), "Docker image"),
        Field("Image", sel.el),
        el("div.row", Button({ label: "Apply", variant: "primary", icon: "check", onClick: async () => {
          try { await client.settings.dockerImage(ctx.id, sel.value); notify.success("Image updated — takes effect on next start"); }
          catch (err) { notify.error(String((err as Error).message)); }
        } })),
      );
    });
    const imageCard = el("div.rst-card", el("div.rst-card__title", icon("docker", { brand: true }), "Docker image"), LoadingState());
    cards.push(imageCard);
    void images;
  }

  const sftp = ctx.attrs.sftp_details;
  const user = `${(window as any).__roostUser ?? ""}`;
  void user;
  cards.push(el("div.rst-card",
    el("div.rst-card__title", icon("server"), "SFTP details"),
    el("dl.rst-kv",
      el("dt", "Address"), el("dd.mono", sftp ? `sftp://${sftp.ip}:${sftp.port}` : "—"),
      el("dt", "Username"), el("dd.mono", `<your-username>.${ctx.attrs.identifier}`),
    ),
    el("p.faint", "Sign in with your panel password or an SSH key from your account."),
  ));

  if (ctx.can("settings.reinstall")) {
    cards.push(el("div.rst-card",
      el("div.rst-card__title", icon("triangle-exclamation"), "Reinstall server"),
      el("p.faint", "Re-runs the egg install script. Files may be overwritten by the installer."),
      el("div.row", Button({ label: "Reinstall", variant: "danger", icon: "rotate", onClick: async () => {
        if (await confirmModal({ title: "Reinstall server", message: "Re-run the install script for this server?", danger: true })) {
          try { await client.settings.reinstall(ctx.id); notify.success("Reinstall started"); }
          catch (err) { notify.error(String((err as Error).message)); }
        }
      } })),
    ));
  }

  cards.push(el("div.rst-card",
    el("div.rst-card__title", icon("fingerprint"), "Debug information"),
    el("dl.rst-kv",
      el("dt", "Identifier"), el("dd.mono", ctx.attrs.identifier),
      el("dt", "UUID"), el("dd.mono", ctx.attrs.uuid),
      el("dt", "Node"), el("dd", ctx.attrs.node ?? "—"),
    ),
  ));

  return page("Settings", { icon: "gear" }, el("div.col", { style: { gap: "var(--sp-3)" } }, ...cards));
}

// ---------------------------------------------------------------- activity

export function ServerActivityTab(ctx: ServerCtx): HTMLElement {
  const body = el("div", LoadingState());
  client.activity(ctx.id).then((res) => {
    body.replaceChildren(ActivityList(unwrap<any>(res)));
  }).catch((err) => body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load", description: String(err.message) })));
  return page("Activity", { icon: "clock-rotate-left" }, body);
}
