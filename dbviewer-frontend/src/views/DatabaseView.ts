import { el, icon, clear } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { Badge, LoadingState, EmptyState } from "../components/misc.ts";
import { DataGrid } from "../components/DataGrid.ts";
import { confirmModal } from "../components/Modal.ts";
import { notify } from "../components/Toast.ts";
import { api } from "../api/client.ts";
import { navigate, refreshTree } from "../state/store.ts";
import { formatBytes, formatNumber } from "../util/format.ts";
import { downloadUrl } from "../util/download.ts";

/** Lists the tables in one database. */
export function DatabaseView(database: string): HTMLElement {
  const root = el("div.gtma-page.grow");
  load();

  async function load() {
    clear(root);
    root.appendChild(LoadingState());
    try {
      const tables = await api.tables(database);
      clear(root);
      root.appendChild(build(tables));
    } catch (err) {
      clear(root);
      root.appendChild(EmptyState({ icon: "triangle-exclamation", title: "Could not load tables", description: String(err) }));
    }
  }

  function build(tables: Awaited<ReturnType<typeof api.tables>>): HTMLElement {
    const totalRows = tables.reduce((s, t) => s + t.rows, 0);
    const totalSize = tables.reduce((s, t) => s + t.sizeBytes, 0);

    const grid = DataGrid({
      columns: [
        { name: "Table" }, { name: "Engine" }, { name: "Rows", type: "int" },
        { name: "Size", type: "int" }, { name: "Type" },
      ],
      rows: tables.map((t) => [t.name, t.engine || "—", formatNumber(t.rows), formatBytes(t.sizeBytes), t.type === "VIEW" ? "view" : "table"]),
      rowMenu: (ri) => {
        const t = tables[ri];
        return [
          { label: "Browse", icon: "table-list", onSelect: () => navigate({ kind: "table", database, table: t.name }) },
          { label: "Structure", icon: "diagram-project", onSelect: () => navigate({ kind: "table", database, table: t.name }) },
          { separator: true },
          { label: "Drop table", icon: "trash", danger: true, onSelect: () => dropTable(t.name) },
        ];
      },
    });

    grid.el.addEventListener("click", (e) => {
      const cell = (e.target as HTMLElement).closest("td.gtma-grid__cell") as HTMLTableCellElement | null;
      if (!cell || cell.cellIndex !== 1) return;
      const tr = cell.closest("tr")!;
      const idx = Array.from(tr.parentElement!.children).indexOf(tr);
      navigate({ kind: "table", database, table: tables[idx].name });
    });

    return el("div.gtma-page__inner",
      el("div.gtma-page__head",
        el("div.col.gap-1",
          el("div.row.gap-2.muted",
            icon("database"),
            el("span.mono", database),
          ),
          el("h1.gtma-page__title", "Tables"),
        ),
        el("div.row.gap-2",
          Badge(`${tables.length} tables`, "outline"),
          Badge(`${formatNumber(totalRows)} rows`, "neutral"),
          Badge(formatBytes(totalSize), "neutral"),
          Button({ label: "Export", icon: "file-export", onClick: exportDatabase }),
          Button({ label: "SQL", icon: "terminal", onClick: () => navigate({ kind: "sql", database }) }),
        ),
      ),
      tables.length ? grid.el : EmptyState({ icon: "table-cells", title: "No tables", description: "This database is empty." }),
    );
  }

  function exportDatabase() {
    downloadUrl(api.exportDatabaseHref(database), `${database}.sql`);
    notify.success("Download started");
  }

  async function dropTable(name: string) {
    const ok = await confirmModal({
      title: "Drop table",
      danger: true,
      confirmLabel: "Drop",
      message: el("div", "Permanently delete table ", el("b.mono", {}, `${database}.${name}`), "?"),
    });
    if (!ok) return;
    try {
      await api.dropTable(database, name);
      notify.success(`Dropped ${name}`);
      refreshTree();
      load();
    } catch (err) {
      notify.error(String(err));
    }
  }

  return root;
}
