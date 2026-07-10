import { el, icon, clear } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { Badge } from "../components/misc.ts";
import { LoadingState, EmptyState } from "../components/misc.ts";
import { DataGrid } from "../components/DataGrid.ts";
import { notify } from "../components/Toast.ts";
import { openModal } from "../components/Modal.ts";
import { TextInput } from "../components/TextInput.ts";
import { confirmModal } from "../components/Modal.ts";
import { api } from "../api/client.ts";
import { store, navigate, refreshTree } from "../state/store.ts";
import { formatBytes, formatNumber, formatUptime } from "../util/format.ts";
import type { DatabaseMeta, ServerInfo } from "../api/types.ts";

/** The server landing page: stats + database list. */
export function HomeView(): HTMLElement {
  const root = el("div.gtma-home.grow");
  load();

  async function load() {
    clear(root);
    root.appendChild(LoadingState("Reading server…"));
    try {
      const [info, dbs] = await Promise.all([api.serverInfo(), api.databases()]);
      store.serverInfo.value = info;
      clear(root);
      root.appendChild(buildContent(info, dbs));
    } catch (err) {
      clear(root);
      root.appendChild(EmptyState({ icon: "triangle-exclamation", title: "Could not load server", description: String(err) }));
    }
  }

  function buildContent(info: ServerInfo, dbs: DatabaseMeta[]): HTMLElement {
    const totalTables = dbs.reduce((s, d) => s + d.tables, 0);
    const totalSize = dbs.reduce((s, d) => s + d.sizeBytes, 0);

    const stat = (ic: string, label: string, value: string) =>
      el("div.gtma-stat",
        el("div.gtma-stat__icon", icon(ic)),
        el("div.col",
          el("div.gtma-stat__value", value),
          el("div.gtma-stat__label.muted", label),
        ),
      );

    const grid = el("div.gtma-grid__scroll");
    grid.appendChild(DataGrid({
      columns: [
        { name: "Database" }, { name: "Tables", type: "int" },
        { name: "Size", type: "int" }, { name: "Collation" },
      ],
      rows: dbs.map((d) => [d.name, String(d.tables), formatBytes(d.sizeBytes), d.collation]),
      rowMenu: (ri) => {
        const d = dbs[ri];
        return [
          { label: "Open", icon: "folder-open", onSelect: () => navigate({ kind: "database", database: d.name }) },
          { label: "SQL console", icon: "terminal", onSelect: () => navigate({ kind: "sql", database: d.name }) },
          { separator: true },
          { label: "Drop database", icon: "trash", danger: true, onSelect: () => dropDatabase(d.name) },
        ];
      },
    }).el);

    // Make database names clickable through the grid.
    grid.addEventListener("click", (e) => {
      const cell = (e.target as HTMLElement).closest("td.gtma-grid__cell");
      if (!cell) return;
      const tr = cell.closest("tr");
      const idx = tr ? Array.from(tr.parentElement!.children).indexOf(tr) : -1;
      if (idx >= 0 && (cell as HTMLTableCellElement).cellIndex === 1) navigate({ kind: "database", database: dbs[idx].name });
    });

    return el("div.gtma-home__inner",
      el("div.gtma-home__head",
        el("div.col.gap-1",
          el("h1.gtma-home__title", "Server overview"),
          el("div.row.gap-2.muted",
            icon("server"),
            el("span.mono", store.server.value),
          ),
        ),
        el("div.row.gap-2",
          Button({ label: "New database", icon: "plus", variant: "primary", onClick: newDatabase }),
          Button({ label: "SQL", icon: "terminal", onClick: () => navigate({ kind: "sql" }) }),
        ),
      ),
      el("div.gtma-home__stats",
        stat("database", "Databases", formatNumber(dbs.length)),
        stat("table-cells", "Tables", formatNumber(totalTables)),
        stat("hard-drive", "Total size", formatBytes(totalSize)),
        stat("clock", "Uptime", formatUptime(info.uptime)),
      ),
      el("div.gtma-home__server",
        el("div.row.gap-3",
          Badge(`${info.versionComment || "MySQL"} ${info.version}`, "outline", { icon: "circle-nodes" }),
          Badge(info.charset || "utf8mb4", "neutral", { icon: "font" }),
          Badge(info.user, "neutral", { icon: "user" }),
        ),
      ),
      el("div.gtma-home__section",
        el("div.gtma-home__section-head", el("h2", "Databases"), el("span.muted", `${dbs.length} schemas`)),
        dbs.length ? grid : EmptyState({ icon: "database", title: "No databases", description: "Create one to get started." }),
      ),
    );
  }

  async function dropDatabase(name: string) {
    const ok = await confirmModal({
      title: "Drop database",
      danger: true,
      confirmLabel: "Drop",
      message: el("div", "This permanently deletes ", el("b.mono", {}, name), " and all of its tables."),
    });
    if (!ok) return;
    try {
      await api.dropDatabase(name);
      notify.success(`Dropped ${name}`);
      refreshTree();
      load();
    } catch (err) {
      notify.error(String(err));
    }
  }

  function newDatabase() {
    const nameInput = TextInput({ label: "Database name", icon: "database", autofocus: true, placeholder: "my_app" });
    const charsetInput = TextInput({ label: "Charset (optional)", value: "utf8mb4", icon: "font" });
    const modal = openModal({
      title: "New database",
      icon: "database",
      width: 420,
      body: el("div.col.gap-3", nameInput.el, charsetInput.el),
      actions: [
        { label: "Cancel", variant: "ghost" },
        {
          label: "Create", variant: "primary", icon: "check", closeOnClick: false,
          onClick: async () => {
            const name = nameInput.value.trim();
            if (!name) { nameInput.setError("Required"); return; }
            try {
              await api.createDatabase(name, charsetInput.value.trim() || undefined);
              notify.success(`Created ${name}`);
              refreshTree();
              load();
              modal.close();
            } catch (err) {
              nameInput.setError(String(err));
            }
          },
        },
      ],
    });
  }

  return root;
}
