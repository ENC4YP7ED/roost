import { el, icon, clear } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { Tabs } from "../components/Tabs.ts";
import { DataGrid } from "../components/DataGrid.ts";
import { Select } from "../components/Select.ts";
import { TextInput } from "../components/TextInput.ts";
import { LoadingState, EmptyState, Field, Segmented } from "../components/misc.ts";
import { confirmModal } from "../components/Modal.ts";
import { SqlConsole } from "./SqlConsole.ts";
import { openRowEditor, rowIdentity } from "./RowEditor.ts";
import { downloadUrl } from "../util/download.ts";
import { notify } from "../components/Toast.ts";
import { tooltip } from "../components/Tooltip.ts";
import { api } from "../api/client.ts";
import { navigate } from "../state/store.ts";
import { formatNumber, formatDuration } from "../util/format.ts";
import type { ColumnMeta, ExportFormat, ForeignKey, IndexMeta, SearchCondition } from "../api/types.ts";

/** The per-table workspace: Browse · Structure · SQL · Export. */
export function TableView(database: string, table: string, initialFilter?: SearchCondition[]): HTMLElement {
  const header = el("div.gtma-page__head",
    el("div.col.gap-1",
      el("div.row.gap-2.muted",
        icon("database"), el("span.mono", database),
        icon("angle-right", { class: "faint" }),
        icon("table", { class: "muted" }),
      ),
      el("h1.gtma-page__title.mono", table),
    ),
  );

  const tabs = Tabs([
    { id: "browse", label: "Browse", icon: "table-list", render: () => BrowseTab(database, table, initialFilter) },
    { id: "structure", label: "Structure", icon: "diagram-project", render: () => StructureTab(database, table) },
    { id: "sql", label: "SQL", icon: "terminal", render: () => SqlConsole({ database, initialSQL: `SELECT * FROM \`${table}\` LIMIT 50;` }) },
    { id: "export", label: "Export", icon: "file-export", render: () => ExportTab(database, table) },
  ]);

  return el("div.gtma-page.gtma-table.col.grow", el("div.gtma-table__head", header), tabs.el);
}

const SEARCH_OPS = ["=", "!=", "<", ">", "<=", ">=", "LIKE", "NOT LIKE", "IS NULL", "IS NOT NULL"];

// ---- Browse ----------------------------------------------------------------

function BrowseTab(database: string, table: string, initialFilter?: SearchCondition[]): HTMLElement {
  const root = el("div.gtma-browse.col.grow");
  const filterSlot = el("div");      // persistent filter bar
  const body = el("div.col.grow");   // reloaded grid + pager
  root.append(filterSlot, body);

  let limit = 50;
  let offset = 0;
  let orderBy = "";
  let dir: "asc" | "desc" = "asc";
  let total = 0;
  let columns: ColumnMeta[] = [];
  let foreignKeys: ForeignKey[] = [];
  let conditions: SearchCondition[] = initialFilter ? [...initialFilter] : [];

  init();

  async function init() {
    try {
      [columns, foreignKeys] = await Promise.all([
        api.columns(database, table),
        api.foreignKeys(database, table).catch(() => [] as ForeignKey[]),
      ]);
    } catch { /* keep going; grid still renders from result columns */ }
    renderFilterBar();
    load();
  }

  async function load() {
    clear(body);
    body.appendChild(LoadingState("Fetching rows…"));
    try {
      const res = conditions.length
        ? await api.search(database, table, { conditions, limit, offset, orderBy, dir })
        : await api.browse(database, table, { limit, offset, orderBy, dir });
      total = res.total;
      clear(body);
      body.appendChild(build(res));
    } catch (err) {
      clear(body);
      body.appendChild(EmptyState({ icon: "triangle-exclamation", title: "Browse failed", description: String(err) }));
    }
  }

  // ---- filter bar (persistent so the draft doesn't reset on reload) ----
  function renderFilterBar() {
    clear(filterSlot);
    if (!columns.length) return;
    const colSel = Select({ size: "sm", searchable: true, options: columns.map((c) => ({ value: c.name, label: c.name, icon: c.key === "PRI" ? "key" : undefined })) });
    const opSel = Select({ size: "sm", value: "=", options: SEARCH_OPS.map((o) => ({ value: o, label: o })) });
    const valInput = TextInput({ size: "sm", placeholder: "value" });

    const addFilter = () => {
      const op = opSel.value;
      const noValue = op === "IS NULL" || op === "IS NOT NULL";
      conditions.push({ column: colSel.value, op, value: noValue ? "" : valInput.value });
      valInput.value = "";
      offset = 0;
      renderFilterBar();
      load();
    };
    valInput.input.addEventListener("keydown", (e) => { if (e.key === "Enter") addFilter(); });

    const chips = conditions.map((c, idx) =>
      el("button.gtma-filter__chip", {
        attrs: { type: "button", title: "Remove filter" },
        onclick: () => { conditions.splice(idx, 1); offset = 0; renderFilterBar(); load(); },
      },
        el("span.mono", {}, c.column),
        el("span.gtma-filter__op", {}, c.op),
        c.value ? el("span.mono.gtma-filter__val", {}, c.value) : null,
        icon("xmark", { class: "gtma-filter__x" }),
      ));

    filterSlot.appendChild(el("div.gtma-filter",
      el("div.gtma-filter__row",
        icon("filter", { class: "muted" }),
        colSel.el, opSel.el, valInput.el,
        Button({ label: "Add filter", icon: "plus", size: "sm", onClick: addFilter }),
      ),
      conditions.length
        ? el("div.gtma-filter__chips",
            ...chips,
            el("button.gtma-filter__clear", { attrs: { type: "button" }, onclick: () => { conditions = []; offset = 0; renderFilterBar(); load(); } },
              icon("xmark"), el("span", {}, "Clear all")))
        : null,
    ));
  }

  async function deleteRow(rowIndex: number, rs: Awaited<ReturnType<typeof api.browse>>["result"]) {
    const ok = await confirmModal({
      title: "Delete row",
      danger: true,
      confirmLabel: "Delete",
      message: el("div", "Delete this row from ", el("b.mono", {}, table), "? This cannot be undone."),
    });
    if (!ok) return;
    try {
      const where = rowIdentity(columns, rs.rows[rowIndex]);
      await api.deleteRow(database, table, where);
      notify.success("Row deleted");
      load();
    } catch (err) {
      notify.error(String(err));
    }
  }

  function build(res: Awaited<ReturnType<typeof api.browse>>): HTMLElement {
    const rs = res.result;
    const editable = columns.length > 0 && res.result.columns.length === columns.length;
    const fkByColumn = new Map(foreignKeys.map((fk) => [fk.column, fk]));
    const grid = DataGrid({
      columns: rs.columns.map((name, i) => {
        const meta = columns.find((c) => c.name === name);
        const fk = fkByColumn.get(name);
        return {
          name, type: rs.columnTypes[i], primary: meta?.key === "PRI",
          link: fk ? (value: string) => navigate({
            kind: "table", database: fk.refSchema || database, table: fk.refTable,
            filter: [{ column: fk.refColumn, op: "=", value }],
          }) : undefined,
        };
      }),
      rows: rs.rows,
      rowNumberStart: offset + 1,
      sortBy: orderBy || undefined,
      sortDir: dir,
      onSort: (col) => {
        if (orderBy === col) dir = dir === "asc" ? "desc" : "asc";
        else { orderBy = col; dir = "asc"; }
        offset = 0;
        load();
      },
      rowMenu: editable ? (ri) => [
        { label: "Edit row", icon: "pen", onSelect: () => openRowEditor({ database, table, columns, mode: "edit", row: rs.rows[ri], onSaved: load }) },
        { label: "Duplicate", icon: "clone", onSelect: () => openRowEditor({ database, table, columns, mode: "insert", row: rs.rows[ri], onSaved: load }) },
        { separator: true },
        { label: "Delete row", icon: "trash", danger: true, onSelect: () => deleteRow(ri, rs) },
      ] : undefined,
    });

    const pages = Math.max(1, Math.ceil(total / limit));
    const page = Math.floor(offset / limit) + 1;

    const toolbar = el("div.gtma-browse__toolbar",
      Button({ label: "Insert row", icon: "plus", size: "sm", variant: "primary", disabled: !editable,
        onClick: () => openRowEditor({ database, table, columns, mode: "insert", onSaved: load }) }),
      el("span.spacer"),
      editable ? el("span.faint.gtma-browse__hint", icon("circle-info"), el("span", {}, "right-click a row to edit or delete")) : null,
    );

    const pager = el("div.gtma-browse__pager",
      el("div.row.gap-2.muted",
        icon("table-list"),
        el("span", {}, `${formatNumber(total)} rows`),
        el("span.faint", {}, "·"),
        el("span", {}, `${formatDuration(rs.durationMs)}`),
      ),
      el("span.spacer"),
      el("div.row.gap-1",
        navBtn("angles-left", offset === 0, () => { offset = 0; load(); }, "First"),
        navBtn("angle-left", offset === 0, () => { offset = Math.max(0, offset - limit); load(); }, "Previous"),
        el("span.gtma-browse__page.mono", {}, `${page} / ${pages}`),
        navBtn("angle-right", page >= pages, () => { offset += limit; load(); }, "Next"),
        navBtn("angles-right", page >= pages, () => { offset = (pages - 1) * limit; load(); }, "Last"),
      ),
      el("div.gtma-browse__limit",
        el("span.faint", {}, "rows"),
        ...[25, 50, 100, 250].map((n) =>
          el("button.gtma-browse__limit-btn", {
            class: n === limit ? "is-active" : "",
            onclick: () => { limit = n; offset = 0; load(); },
          }, String(n))),
      ),
    );

    return el("div.col.grow", toolbar, el("div.gtma-browse__grid.grow", grid.el), pager);
  }

  function navBtn(ic: string, disabled: boolean, onClick: () => void, title: string): HTMLElement {
    const b = IconButton(ic, { size: "sm", variant: "ghost", disabled, onClick });
    tooltip(b, title);
    return b;
  }

  return root;
}

// ---- Structure -------------------------------------------------------------

function StructureTab(database: string, table: string): HTMLElement {
  const root = el("div.gtma-structure.grow");
  load();

  async function load() {
    clear(root);
    root.appendChild(LoadingState());
    try {
      const [columns, indexes, fks] = await Promise.all([
        api.columns(database, table),
        api.indexes(database, table),
        api.foreignKeys(database, table).catch(() => [] as ForeignKey[]),
      ]);
      clear(root);
      root.appendChild(build(columns, indexes, fks));
    } catch (err) {
      clear(root);
      root.appendChild(EmptyState({ icon: "triangle-exclamation", title: "Could not load structure", description: String(err) }));
    }
  }

  function build(columns: ColumnMeta[], indexes: IndexMeta[], fks: ForeignKey[]): HTMLElement {
    const colGrid = DataGrid({
      columns: [
        { name: "Column" }, { name: "Type" }, { name: "Null" },
        { name: "Key" }, { name: "Default" }, { name: "Extra" },
      ],
      rows: columns.map((c) => [
        c.name, c.type, c.nullable ? "YES" : "NO",
        c.key || "", c.default ?? null, c.extra || "",
      ]),
    });

    const idxGrid = indexes.length ? DataGrid({
      columns: [{ name: "Index" }, { name: "Unique" }, { name: "Type" }, { name: "Columns" }],
      rows: indexes.map((i) => [i.name, i.unique ? "YES" : "NO", i.type, i.columns.join(", ")]),
    }).el : EmptyState({ icon: "list", title: "No indexes" });

    const pk = columns.filter((c) => c.key === "PRI").map((c) => c.name);

    const fkSection = fks.length ? el("div.gtma-structure__section",
      el("h3.gtma-structure__h", icon("link"), "Foreign keys"),
      DataGrid({
        columns: [{ name: "Constraint" }, { name: "Column" }, { name: "References" }, { name: "On update" }, { name: "On delete" }],
        rows: fks.map((fk) => [
          fk.name, fk.column,
          `${fk.refTable}.${fk.refColumn}`,
          fk.onUpdate, fk.onDelete,
        ]),
      }).el,
    ) : null;

    return el("div.gtma-structure__inner",
      el("div.gtma-structure__summary",
        Field("Columns", String(columns.length)),
        Field("Primary key", pk.length ? el("span.mono", {}, pk.join(", ")) : el("span.faint", {}, "none")),
        Field("Indexes", String(indexes.length)),
        Field("Foreign keys", String(fks.length)),
      ),
      el("div.gtma-structure__section",
        el("h3.gtma-structure__h", icon("diagram-project"), "Columns"),
        colGrid.el,
      ),
      el("div.gtma-structure__section",
        el("h3.gtma-structure__h", icon("list"), "Indexes"),
        idxGrid,
      ),
      fkSection,
    );
  }

  return root;
}

// ---- Export ----------------------------------------------------------------

function ExportTab(database: string, table: string): HTMLElement {
  const root = el("div.gtma-export.grow");
  let format: ExportFormat = "sql";
  const preview = el("pre.gtma-export__code.mono");
  let current = "";

  async function refresh() {
    preview.replaceChildren(LoadingState(`Generating ${format.toUpperCase()}…`));
    try {
      current = await api.exportTable(database, table, format);
      preview.textContent = current.length > 20000 ? current.slice(0, 20000) + "\n… (truncated in preview — download for full)" : current;
      if (format === "sql") preview.innerHTML = highlightExportSQL(preview.textContent);
    } catch (err) {
      preview.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Export failed", description: String(err) }));
    }
  }

  async function copy() {
    try { await navigator.clipboard.writeText(current); notify.success("Copied"); }
    catch { notify.error("Clipboard unavailable"); }
  }

  const inner = el("div.gtma-export__inner",
    el("div.gtma-export__bar",
      el("div.row.gap-2", icon("file-export"), el("span.strong", {}, "Export"), el("span.faint.mono", {}, `${database}.${table}`)),
      el("span.spacer"),
      Segmented({
        value: "sql",
        options: [
          { value: "sql", label: "SQL", icon: "database" },
          { value: "csv", label: "CSV", icon: "table" },
          { value: "json", label: "JSON", icon: "code" },
        ],
        onChange: (v) => { format = v as ExportFormat; refresh(); },
      }),
      Button({ label: "Copy", icon: "copy", size: "sm", onClick: copy }),
      Button({ label: "Download", icon: "download", size: "sm", variant: "primary",
        onClick: () => { downloadUrl(api.exportTableHref(database, table, format), `${database}.${table}.${format}`); notify.success("Download started"); } }),
    ),
    preview,
  );
  root.appendChild(inner);
  refresh();
  return root;
}

/** Minimal keyword highlight for the SQL export preview. */
function highlightExportSQL(text: string): string {
  const esc = text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return esc
    .replace(/^(--.*)$/gm, '<span class="tok-comment">$1</span>')
    .replace(/\b(CREATE|TABLE|INSERT|INTO|VALUES|DROP|IF|EXISTS|NOT|NULL|DEFAULT|PRIMARY|KEY|UNIQUE|ENGINE|AUTO_INCREMENT|USE|DATABASE)\b/g,
      '<span class="tok-keyword">$1</span>');
}
