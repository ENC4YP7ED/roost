import { el, icon } from "../core/dom.ts";
import { openModal } from "../components/Modal.ts";
import { TextInput, TextArea } from "../components/TextInput.ts";
import { notify } from "../components/Toast.ts";
import { Badge } from "../components/misc.ts";
import { api, ApiError } from "../api/client.ts";
import type { CellValue, ColumnMeta } from "../api/types.ts";

interface RowEditorOptions {
  database: string;
  table: string;
  columns: ColumnMeta[];
  mode: "insert" | "edit";
  /** Current row values aligned to `columns`, required for edit. */
  row?: Array<CellValue>;
  onSaved: () => void;
}

const LONG_TYPE = /(text|blob|json)/i;

/** Build the identity (WHERE) map for an existing row: PK columns if present,
 *  otherwise every column. */
export function rowIdentity(columns: ColumnMeta[], row: Array<CellValue>): Record<string, CellValue> {
  const pkCols = columns.filter((c) => c.key === "PRI");
  const use = pkCols.length ? pkCols : columns;
  const where: Record<string, CellValue> = {};
  for (const c of use) {
    const idx = columns.indexOf(c);
    where[c.name] = row[idx];
  }
  return where;
}

/** Open the insert/edit form modal for a single row. */
export function openRowEditor(opts: RowEditorOptions): void {
  const fields = opts.columns.map((col, i) => Field(col, opts.mode, opts.row?.[i] ?? null));

  const modal = openModal({
    title: opts.mode === "insert" ? "Insert row" : "Edit row",
    icon: opts.mode === "insert" ? "plus" : "pen",
    width: 560,
    body: el("div.gtma-roweditor", ...fields.map((f) => f.el)),
    actions: [
      { label: "Cancel", variant: "ghost" },
      {
        label: opts.mode === "insert" ? "Insert" : "Save",
        variant: "primary",
        icon: "check",
        closeOnClick: false,
        onClick: async () => {
          const values: Record<string, CellValue> = {};
          for (const f of fields) {
            const v = f.read();
            if (v.include) values[f.column.name] = v.value;
          }
          try {
            if (opts.mode === "insert") {
              await api.insertRow(opts.database, opts.table, values);
              notify.success("Row inserted");
            } else {
              const where = rowIdentity(opts.columns, opts.row!);
              const affected = await api.updateRow(opts.database, opts.table, where, values);
              notify.success(affected ? "Row updated" : "No changes");
            }
            opts.onSaved();
            modal.close();
          } catch (err) {
            const msg = err instanceof ApiError ? err.message : String(err);
            notify.error(msg, "Save failed");
          }
        },
      },
    ],
  });
}

interface FieldHandle {
  el: HTMLElement;
  column: ColumnMeta;
  read(): { include: boolean; value: CellValue };
}

function Field(col: ColumnMeta, mode: "insert" | "edit", current: CellValue): FieldHandle {
  const isPK = col.key === "PRI";
  const isAuto = col.extra.includes("auto_increment");
  const readOnly = mode === "edit" && isPK;
  // Auto-increment columns are omitted by default on insert (DB assigns them).
  let omitted = mode === "insert" && isAuto;
  let isNull = current === null && mode === "edit";

  const long = LONG_TYPE.test(col.type);
  let input: { el: HTMLElement; get: () => string; set: (v: string) => void; disable: (d: boolean) => void };

  if (long) {
    const ta = TextArea({ value: current ?? "", rows: 3, mono: true });
    input = { el: ta, get: () => ta.value, set: (v) => (ta.value = v), disable: (d) => (ta.disabled = d) };
  } else {
    const ti = TextInput({ value: current ?? "", size: "sm", placeholder: isAuto ? "(auto)" : col.nullable ? "" : "value" });
    input = { el: ti.el, get: () => ti.value, set: (v) => (ti.value = v), disable: (d) => (ti.input.disabled = d) };
  }
  if (readOnly) input.disable(true);

  // NULL / DEFAULT toggle chips.
  const toggles = el("div.gtma-roweditor__toggles");
  const nullChip = col.nullable ? toggleChip("NULL", isNull, () => {
    isNull = !isNull;
    omitted = false;
    sync();
  }) : null;
  const defaultChip = (mode === "insert" && (isAuto || col.default !== null)) ? toggleChip("DEFAULT", omitted, () => {
    omitted = !omitted;
    isNull = false;
    sync();
  }) : null;
  if (nullChip) toggles.appendChild(nullChip);
  if (defaultChip) toggles.appendChild(defaultChip);

  function sync() {
    input.disable(readOnly || isNull || omitted);
    nullChip?.classList.toggle("is-active", isNull);
    defaultChip?.classList.toggle("is-active", omitted);
  }
  sync();

  const root = el("div.gtma-roweditor__field",
    el("div.gtma-roweditor__meta",
      el("span.gtma-roweditor__name.mono", col.name),
      el("span.gtma-roweditor__type.mono.faint", col.type),
      isPK ? Badge("PK", "warning", { icon: "key" }) : null,
      !col.nullable && !isAuto ? Badge("required", "outline") : null,
      el("span.spacer"),
      toggles,
    ),
    input.el,
  );

  return {
    el: root,
    column: col,
    read() {
      if (omitted) return { include: false, value: null };
      if (isNull) return { include: true, value: null };
      return { include: true, value: input.get() };
    },
  };
}

function toggleChip(label: string, active: boolean, onClick: () => void): HTMLElement {
  return el("button.gtma-roweditor__chip", {
    class: active ? "is-active" : "",
    attrs: { type: "button" },
    onclick: onClick,
  }, icon("circle-dot"), el("span", {}, label));
}
