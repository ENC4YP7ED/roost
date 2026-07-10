import { el, icon } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { TextInput, TextArea } from "../components/TextInput.ts";
import { Select } from "../components/Select.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState, Badge, Switch, Field } from "../components/misc.ts";
import { admin, unwrap } from "../api/client.ts";
import { navigate } from "../state/store.ts";
import { page, localTime } from "./shared.ts";
import { formatBytes } from "../util/format.ts";

// =================================================================== nodes

export function AdminNodes(openId?: number): HTMLElement {
  const body = el("div", LoadingState());

  async function load() {
    const nodes = unwrap<any>(await admin.nodes.list());
    if (!nodes.length) {
      body.replaceChildren(EmptyState({ icon: "hard-drive", title: "No nodes", description: "A node is a machine running the Wings daemon." }));
      return;
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...nodes.map((n) => el("div.rst-card",
      el("div.rst-section__head",
        el("div.rst-card__title",
          icon("hard-drive"), n.name, " ",
          n.maintenance_mode ? Badge("maintenance", "warning") : null,
          el("span.mono.faint", `${n.scheme}://${n.fqdn}:${n.daemon_listen}`),
        ),
        el("div.row",
          Button({ label: "Allocations", icon: "network-wired", size: "sm", onClick: () => allocationsModal(n) }),
          Button({ label: "Wings config", icon: "file-code", size: "sm", onClick: () => configModal(n) }),
          IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => nodeModal(n) }),
          IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
            if (await confirmModal({ title: "Delete node", message: `Delete "${n.name}"?`, danger: true })) {
              try { await admin.nodes.remove(n.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
            }
          } }),
        ),
      ),
      el("dl.rst-kv",
        el("dt", "Memory"), el("dd", `${formatBytes((n.allocated_resources?.memory ?? 0) * 1024 * 1024)} allocated of ${formatBytes(n.memory * 1024 * 1024)}`),
        el("dt", "Disk"), el("dd", `${formatBytes((n.allocated_resources?.disk ?? 0) * 1024 * 1024)} allocated of ${formatBytes(n.disk * 1024 * 1024)}`),
        el("dt", "Servers"), el("dd", String(n.servers_count ?? 0)),
        el("dt", "SFTP"), el("dd.mono", `${n.fqdn}:${n.daemon_sftp}`),
      ),
    ))));
    if (openId != null) {
      const n = nodes.find((x) => x.id === openId);
      if (n) configModal(n);
      openId = undefined;
    }
  }

  async function nodeModal(existing: any | null) {
    const locations = unwrap<any>(await admin.locations.list());
    if (!locations.length) { notify.error("Create a location first."); return; }
    const name = TextInput({ label: "Name", value: existing?.name ?? "", autofocus: true });
    const fqdn = TextInput({ label: "FQDN / IP", value: existing?.fqdn ?? "", icon: "globe" });
    const scheme = Select({ options: [{ value: "https", label: "https" }, { value: "http", label: "http" }], value: existing?.scheme ?? "https" });
    const loc = Select({ options: locations.map((l) => ({ value: String(l.id), label: l.short })), value: String(existing?.location_id ?? locations[0].id) });
    const memory = TextInput({ label: "Memory (MiB)", value: String(existing?.memory ?? 4096), type: "number" });
    const disk = TextInput({ label: "Disk (MiB)", value: String(existing?.disk ?? 51200), type: "number" });
    const listen = TextInput({ label: "Daemon port", value: String(existing?.daemon_listen ?? 8080), type: "number" });
    const sftp = TextInput({ label: "SFTP port", value: String(existing?.daemon_sftp ?? 2022), type: "number" });
    let maintenance = existing?.maintenance_mode ?? false;
    openModal({
      title: existing ? `Edit ${existing.name}` : "New node", icon: "hard-drive", width: 620,
      body: el("div.rst-form",
        el("div.rst-form__row", name.el, fqdn.el),
        el("div.rst-form__row", Field("Scheme", scheme.el), Field("Location", loc.el)),
        el("div.rst-form__row", memory.el, disk.el),
        el("div.rst-form__row", listen.el, sftp.el),
        existing ? Switch({ checked: maintenance, label: "Maintenance mode", onChange: (v) => { maintenance = v; } }) : null,
      ),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          const payload = {
            name: name.value, fqdn: fqdn.value, scheme: scheme.value,
            location_id: Number(loc.value), memory: Number(memory.value), disk: Number(disk.value),
            daemon_listen: Number(listen.value), daemon_sftp: Number(sftp.value),
            maintenance_mode: maintenance,
          };
          try {
            if (existing) await admin.nodes.update(existing.id, payload);
            else {
              await admin.nodes.create(payload);
              notify.success("Node created — grab its wings config next");
            }
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  async function configModal(n: any) {
    const cfg = await admin.nodes.configuration(n.id);
    openModal({
      title: `Wings configuration — ${n.name}`, icon: "file-code", width: 720,
      body: el("div.col", { style: { gap: "var(--sp-3)" } },
        el("p.faint", "Place this as /etc/pterodactyl/config.yml (as JSON, wings accepts both) on the node, then start wings."),
        el("div.rst-codeblock", JSON.stringify(cfg, null, 2)),
        el("div.row",
          Button({ label: "Copy", icon: "copy", size: "sm", onClick: () => { navigator.clipboard.writeText(JSON.stringify(cfg, null, 2)); notify.success("Copied"); } }),
          Button({ label: "Rotate token", icon: "rotate", size: "sm", variant: "danger", onClick: async () => {
            if (await confirmModal({ title: "Rotate daemon token", message: "The node must be reconfigured with the new token. Continue?", danger: true })) {
              await admin.nodes.resetToken(n.id);
              notify.success("Token rotated — re-download the configuration");
            }
          } }),
        ),
      ),
      actions: [{ label: "Close", variant: "primary" }],
    });
  }

  async function allocationsModal(n: any) {
    const list = el("div.rst-activity", LoadingState());
    async function loadAllocs() {
      const allocs = unwrap<any>(await admin.nodes.allocations(n.id));
      list.replaceChildren(...allocs.map((a) => el("div.rst-activity__item",
        icon("network-wired", { class: "faint" }),
        el("span.mono", `${a.ip}:${a.port}`),
        a.alias ? el("span.faint.mono", a.alias) : null,
        a.assigned ? Badge("assigned", "info") : Badge("free", "outline"),
        el("span.rst-activity__meta"),
        !a.assigned ? IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
          await admin.nodes.removeAllocation(n.id, a.id); loadAllocs();
        } }) : null,
      )));
      if (!allocs.length) list.replaceChildren(el("p.faint", "No allocations yet."));
    }
    loadAllocs();
    const ip = TextInput({ label: "IP address", value: n.fqdn, size: "sm" });
    const ports = TextInput({ label: "Ports", placeholder: "25565-25570, 27015", size: "sm" });
    openModal({
      title: `Allocations — ${n.name}`, icon: "network-wired", width: 640,
      body: el("div.col", { style: { gap: "var(--sp-3)" } },
        el("div.rst-form__row", ip.el, ports.el),
        el("div.row", Button({ label: "Add allocations", icon: "plus", size: "sm", variant: "primary", onClick: async () => {
          try {
            await admin.nodes.createAllocations(n.id, ip.value, ports.value.split(",").map((s) => s.trim()).filter(Boolean));
            ports.value = "";
            loadAllocs();
          } catch (err) { notify.error(String((err as Error).message)); }
        } })),
        list,
      ),
      actions: [{ label: "Done", variant: "primary" }],
    });
  }

  load();
  return page("Nodes", { icon: "hard-drive", actions: [Button({ label: "New node", icon: "plus", variant: "primary", size: "sm", onClick: () => nodeModal(null) })] }, body);
}

// =================================================================== servers

export function AdminServers(openId?: number): HTMLElement {
  void openId;
  const body = el("div", LoadingState());
  const search = TextInput({ placeholder: "Search servers…", icon: "magnifying-glass", size: "sm", clearable: true, onInput: (v) => load(v) });

  async function load(filter = "") {
    const servers = unwrap<any>(await admin.servers.list(filter));
    if (!servers.length) {
      body.replaceChildren(EmptyState({ icon: "server", title: "No servers", description: "Create one once you have a node with free allocations." }));
      return;
    }
    body.replaceChildren(el("div.rst-card", el("div.rst-activity", ...servers.map((s) => el("div.rst-activity__item",
      icon("server", { class: "faint" }),
      el("span.strong", s.name),
      el("span.mono.faint", s.identifier),
      s.status ? Badge(String(s.status).replace("_", " "), s.status === "suspended" ? "danger" : "warning") : Badge("ok", "success"),
      el("span.faint", `${s.limits.memory} MiB · ${s.limits.disk} MiB`),
      el("span.rst-activity__meta"),
      Button({ label: "Open", icon: "arrow-up-right-from-square", size: "sm", variant: "ghost", onClick: () => navigate({ kind: "server", id: s.identifier, tab: "console" }) }),
      IconButton(s.status === "suspended" ? "play" : "pause", { size: "sm", variant: "ghost", title: s.status === "suspended" ? "Unsuspend" : "Suspend", onClick: async () => {
        if (s.status === "suspended") await admin.servers.unsuspend(s.id);
        else await admin.servers.suspend(s.id);
        load(filter);
      } }),
      IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
        if (await confirmModal({ title: "Delete server", message: `Delete "${s.name}" and its files? This cannot be undone.`, danger: true })) {
          try { await admin.servers.remove(s.id); } catch { await admin.servers.remove(s.id, true); }
          load(filter);
        }
      } }),
    )))));
  }

  async function createModal() {
    const [users, nests, nodes] = await Promise.all([
      unwrap<any>(await admin.users.list()),
      unwrap<any>(await admin.nests.list()),
      unwrap<any>(await admin.nodes.list()),
    ]);
    if (!nodes.length) { notify.error("Create a node with allocations first."); return; }

    const name = TextInput({ label: "Server name", autofocus: true });
    const owner = Select({ options: users.map((u) => ({ value: String(u.id), label: `${u.username} (${u.email})` })), searchable: true });

    // The Select component has immutable options, so the allocation picker is
    // rebuilt from scratch every time the node changes.
    let allocSel: ReturnType<typeof Select> | null = null;
    let allocCount = 0;
    const allocField = el("div");
    const allocLabel = el("p.faint", "…");

    async function refreshAllocs(nodeId: number) {
      allocSel = null;
      allocCount = 0;
      allocLabel.textContent = "Loading allocations…";
      const allocs = unwrap<any>(await admin.nodes.allocations(nodeId)).filter((a) => !a.assigned);
      const opts = allocs.map((a) => ({ value: String(a.id), label: `${a.ip}:${a.port}${a.alias ? ` (${a.alias})` : ""}` }));
      allocCount = opts.length;
      if (!opts.length) {
        allocField.replaceChildren();
        allocLabel.textContent = "No free allocations on this node — add some under Admin → Nodes → Allocations.";
        return;
      }
      allocSel = Select({ options: opts, searchable: opts.length > 8 });
      allocField.replaceChildren(Field("Default allocation", allocSel.el));
      allocLabel.textContent = `${opts.length} free allocation(s) on this node`;
    }

    const nodeSel = Select({
      options: nodes.map((n) => ({ value: String(n.id), label: n.name })),
      onChange: (v) => { refreshAllocs(Number(v)); },
    });

    const eggOptions: Array<{ value: string; label: string }> = [];
    for (const nest of nests) {
      const eggs = unwrap<any>(await admin.nests.eggs(nest.id));
      for (const egg of eggs) eggOptions.push({ value: String(egg.id), label: `${nest.name} / ${egg.name}` });
    }
    const eggSel = Select({ options: eggOptions, searchable: true });

    const memory = TextInput({ label: "Memory (MiB)", value: "2048", type: "number" });
    const disk = TextInput({ label: "Disk (MiB)", value: "10240", type: "number" });
    const cpu = TextInput({ label: "CPU (%)", value: "0", hint: "0 = unlimited", type: "number" });
    const dbLimit = TextInput({ label: "Databases", value: "0", type: "number" });
    const allocLimit = TextInput({ label: "Allocations", value: "1", type: "number" });
    const backupLimit = TextInput({ label: "Backups", value: "0", type: "number" });

    await refreshAllocs(Number(nodeSel.value));

    const modal = openModal({
      title: "New server", icon: "server", width: 680,
      body: el("div.rst-form",
        name.el,
        el("div.rst-form__row", Field("Owner", owner.el), Field("Egg", eggSel.el)),
        el("div.rst-form__row", Field("Node", nodeSel.el), allocField),
        allocLabel,
        el("div.rst-form__row", memory.el, disk.el, cpu.el),
        el("div.rst-form__row", dbLimit.el, allocLimit.el, backupLimit.el),
      ),
      actions: [
        { label: "Cancel" },
        { label: "Create server", variant: "primary", closeOnClick: false, onClick: async () => {
          if (!allocSel || !allocCount) { notify.error("No free allocation on the selected node — add allocations to it first."); return; }
          if (!name.value.trim()) { notify.error("Give the server a name."); return; }
          try {
            await admin.servers.create({
              name: name.value,
              user: Number(owner.value),
              egg: Number(eggSel.value),
              allocation: { default: Number(allocSel.value) },
              limits: { memory: Number(memory.value), swap: 0, disk: Number(disk.value), io: 500, cpu: Number(cpu.value) },
              feature_limits: { databases: Number(dbLimit.value), allocations: Number(allocLimit.value), backups: Number(backupLimit.value) },
              environment: {},
              start_on_completion: false,
            });
            notify.success("Server created — wings will run the install script");
            modal.close();
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  load();
  return page("Servers", { icon: "server", actions: [search.el, Button({ label: "New server", icon: "plus", variant: "primary", size: "sm", onClick: createModal })] }, body);
}

// =================================================================== nests & eggs

export function AdminEggs(openNest?: number): HTMLElement {
  void openNest;
  const body = el("div", LoadingState());

  async function load() {
    const nests = unwrap<any>(await admin.nests.list());
    const cards: HTMLElement[] = [];
    for (const nest of nests) {
      const eggList = el("div.rst-activity", LoadingState());
      admin.nests.eggs(nest.id).then((res) => {
        const eggs = unwrap<any>(res);
        eggList.replaceChildren(...eggs.map((egg) => el("div.rst-activity__item",
          icon("egg", { class: "faint" }),
          el("span.strong", egg.name),
          el("span.faint.truncate", { style: { maxWidth: "340px" } }, egg.description ?? ""),
          el("span.rst-activity__meta"),
          Button({ label: "Export", icon: "download", size: "sm", variant: "ghost", onClick: () => window.open(admin.nests.exportURL(nest.id, egg.id), "_blank") }),
          IconButton("pen", { size: "sm", variant: "ghost", title: "Edit", onClick: () => eggModal(nest, egg) }),
          IconButton("trash", { size: "sm", variant: "ghost", title: "Delete", onClick: async () => {
            if (await confirmModal({ title: "Delete egg", message: `Delete "${egg.name}"?`, danger: true })) {
              try { await admin.nests.removeEgg(nest.id, egg.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
            }
          } }),
        )));
        if (!eggs.length) eggList.replaceChildren(el("p.faint", "No eggs in this nest."));
      });
      cards.push(el("div.rst-card",
        el("div.rst-section__head",
          el("div.rst-card__title", icon("folder"), nest.name, el("span.faint", nest.description ?? "")),
          el("div.row",
            Button({ label: "Import egg", icon: "file-import", size: "sm", onClick: () => importModal(nest) }),
            IconButton("trash", { size: "sm", variant: "ghost", title: "Delete nest", onClick: async () => {
              if (await confirmModal({ title: "Delete nest", message: `Delete "${nest.name}" and all of its eggs?`, danger: true })) {
                try { await admin.nests.remove(nest.id); load(); } catch (err) { notify.error(String((err as Error).message)); }
              }
            } }),
          ),
        ),
        eggList,
      ));
    }
    body.replaceChildren(el("div.col", { style: { gap: "var(--sp-3)" } }, ...cards));
  }

  function importModal(nest: any) {
    const url = TextInput({ label: "Import from URL", placeholder: "https://…/egg-something.json", icon: "link" });
    const file = el("input", { attrs: { type: "file", accept: ".json" }, style: { color: "var(--text-muted)" } }) as HTMLInputElement;
    openModal({
      title: `Import egg into ${nest.name}`, icon: "file-import", width: 560,
      body: el("div.rst-form",
        el("p.faint", "Accepts standard Pterodactyl/Pelican PTDL_v1 & v2 egg exports."),
        url.el,
        Field("…or upload a file", file),
      ),
      actions: [
        { label: "Cancel" },
        { label: "Import", variant: "primary", onClick: async () => {
          try {
            if (url.value) {
              await admin.nests.importEggURL(nest.id, url.value);
            } else if (file.files?.[0]) {
              const doc = JSON.parse(await file.files[0].text());
              await admin.nests.importEgg(nest.id, doc);
            } else {
              notify.error("Provide a URL or file"); return;
            }
            notify.success("Egg imported");
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  function eggModal(nest: any, egg: any) {
    const name = TextInput({ label: "Name", value: egg.name });
    const startup = TextArea({ value: egg.startup, rows: 2, mono: true });
    const stop = TextInput({ label: "Stop command", value: egg.config?.stop ?? "stop" });
    const images = TextArea({ value: JSON.stringify(egg.docker_images ?? {}, null, 2), rows: 4, mono: true });
    const script = TextArea({ value: egg.script?.install ?? "", rows: 10, mono: true });
    openModal({
      title: `Edit egg — ${egg.name}`, icon: "egg", width: 760,
      body: el("div.rst-form",
        name.el,
        Field("Startup command", startup),
        stop.el,
        Field("Docker images (JSON)", images),
        Field("Install script", script),
      ),
      actions: [
        { label: "Cancel" },
        { label: "Save", variant: "primary", onClick: async () => {
          try {
            await admin.nests.updateEgg(nest.id, egg.id, {
              name: name.value,
              startup: startup.value,
              config_stop: stop.value,
              docker_images: JSON.parse(images.value),
              script_install: script.value,
            });
            notify.success("Egg updated");
            load();
          } catch (err) { notify.error(String((err as Error).message)); }
        } },
      ],
    });
  }

  const newNestBtn = Button({
    label: "New nest", icon: "plus", variant: "primary", size: "sm",
    onClick: () => {
      const name = TextInput({ label: "Name", autofocus: true });
      const desc = TextInput({ label: "Description" });
      openModal({
        title: "New nest", icon: "folder-plus",
        body: el("div.rst-form", name.el, desc.el),
        actions: [
          { label: "Cancel" },
          { label: "Create", variant: "primary", onClick: async () => {
            await admin.nests.create(name.value, desc.value);
            load();
          } },
        ],
      });
    },
  });

  load();
  return page("Nests & Eggs", { icon: "egg", actions: [newNestBtn] }, body);
}

void localTime;
