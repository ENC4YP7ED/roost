import { el, icon, clear } from "../core/dom.ts";
import { attachContextMenu } from "./ContextMenu.ts";
import { type MenuItem } from "./Menu.ts";
import { notify } from "./Toast.ts";

export interface GridColumn {
  name: string;
  type?: string;
  primary?: boolean;
  /** When set, the cell value renders as a link (e.g. a foreign key). */
  link?: (value: string) => void;
}

export interface DataGridOptions {
  columns: GridColumn[];
  rows: Array<Array<string | null>>;
  sortBy?: string;
  sortDir?: "asc" | "desc";
  rowNumberStart?: number;
  onSort?: (column: string) => void;
  rowMenu?: (rowIndex: number, row: Array<string | null>) => MenuItem[];
  /** Row-virtualize large result sets (only the visible window is in the DOM). */
  virtual?: boolean;
}

export interface DataGridHandle {
  el: HTMLElement;
  setData(columns: GridColumn[], rows: Array<Array<string | null>>): void;
}

const NUMERIC = /^(INT|BIGINT|SMALLINT|TINYINT|MEDIUMINT|DECIMAL|FLOAT|DOUBLE|YEAR|BIT)/i;
const VIRTUAL_MIN = 200; // below this, just render everything
const OVERSCAN = 10;

/** Spreadsheet-style result grid: sticky header, NULL styling, cell copy,
 *  optional row virtualization for huge result sets. */
export function DataGrid(opts: DataGridOptions): DataGridHandle {
  const table = el("table.gtma-grid");
  const root = el("div.gtma-grid__scroll", {}, table) as HTMLDivElement;

  // A single scroll handler delegates to whatever the current render installed
  // (the virtual window redraw, or nothing for fully-rendered grids).
  let onScroll: (() => void) | null = null;
  root.addEventListener("scroll", () => onScroll?.(), { passive: true });

  // Let the vertical wheel pan the table horizontally when it overflows
  // sideways. Vertical scrolling wins while there's room; at the vertical edges
  // (or when the table fits vertically) the wheel moves left/right instead.
  root.addEventListener("wheel", (e: WheelEvent) => {
    const canX = root.scrollWidth - root.clientWidth > 1;
    if (!canX || e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
    const canY = root.scrollHeight - root.clientHeight > 1;
    if (canY) {
      const atTop = root.scrollTop <= 0;
      const atBottom = root.scrollTop + root.clientHeight >= root.scrollHeight - 1;
      if (e.deltaY < 0 ? !atTop : !atBottom) return; // still vertical room
    }
    root.scrollLeft += e.deltaY;
    e.preventDefault();
  }, { passive: false });

  function render(columns: GridColumn[], rows: Array<Array<string | null>>, sortBy?: string, sortDir?: string) {
    onScroll = null;
    root.scrollTop = 0;
    table.style.tableLayout = "";
    clear(table);
    table.appendChild(buildHeader(columns, sortBy, sortDir));

    if (!rows.length) {
      table.appendChild(el("tbody", {}, el("tr", {},
        el("td.gtma-grid__empty", { attrs: { colspan: columns.length + 1 } }, "No rows"))));
      return;
    }
    if (opts.virtual && rows.length > VIRTUAL_MIN) {
      renderVirtual(columns, rows);
    } else {
      const tbody = el("tbody");
      rows.forEach((row, ri) => tbody.appendChild(buildBodyRow(ri, row, columns)));
      table.appendChild(tbody);
    }
  }

  // ---- virtual path ----
  function renderVirtual(columns: GridColumn[], rows: Array<Array<string | null>>) {
    const tbody = el("tbody");
    table.appendChild(tbody);
    let rowH = 37;
    let calibrated = false;

    const draw = () => {
      const vh = root.clientHeight || 480;
      const start = Math.max(0, Math.floor(root.scrollTop / rowH) - OVERSCAN);
      const end = Math.min(rows.length, start + Math.ceil(vh / rowH) + OVERSCAN * 2);
      clear(tbody);
      tbody.appendChild(spacerRow(start * rowH, columns.length + 1));
      for (let i = start; i < end; i++) tbody.appendChild(buildBodyRow(i, rows[i], columns));
      tbody.appendChild(spacerRow((rows.length - end) * rowH, columns.length + 1));

      if (!calibrated) {
        calibrated = true;
        requestAnimationFrame(() => {
          const r = tbody.querySelector("tr.gtma-grid__row") as HTMLElement | null;
          if (r) {
            const h = r.getBoundingClientRect().height;
            if (h > 8) rowH = h;
          }
          lockColumnWidths();
          draw(); // redraw with the calibrated row height
        });
      }
    };

    onScroll = rafThrottle(draw);
    draw();
  }

  // Measure header widths and freeze them via a <colgroup> + fixed layout, so
  // columns don't jitter as rows scroll in and out of the window.
  function lockColumnWidths() {
    const ths = [...table.querySelectorAll("thead th")] as HTMLElement[];
    if (!ths.length) return;
    table.querySelector("colgroup")?.remove();
    const cg = document.createElement("colgroup");
    for (const th of ths) {
      const col = document.createElement("col");
      col.style.width = `${Math.round(th.getBoundingClientRect().width)}px`;
      cg.appendChild(col);
    }
    table.insertBefore(cg, table.firstChild);
    table.style.tableLayout = "fixed";
  }

  // ---- shared builders ----
  function buildHeader(columns: GridColumn[], sortBy?: string, sortDir?: string): HTMLElement {
    return el("thead", {}, el("tr", {},
      el("th.gtma-grid__rownum", {}, "#"),
      ...columns.map((c) => {
        const sortable = !!opts.onSort;
        const isSorted = sortBy === c.name;
        return el("th.gtma-grid__th", {
          class: [sortable ? "is-sortable" : "", isSorted ? "is-sorted" : ""].join(" "),
          onclick: sortable ? () => opts.onSort!(c.name) : undefined,
        },
          el("div.gtma-grid__th-inner", {},
            c.primary ? icon("key", { class: "gtma-grid__pk" }) : null,
            el("span.gtma-grid__col-name", {}, c.name),
            c.type ? el("span.gtma-grid__col-type.mono", {}, baseType(c.type)) : null,
            isSorted ? icon(sortDir === "desc" ? "arrow-down-long" : "arrow-up-long", { class: "gtma-grid__sort" }) : null,
          ),
        );
      }),
    ));
  }

  function buildBodyRow(ri: number, row: Array<string | null>, columns: GridColumn[]): HTMLElement {
    const tr = el("tr.gtma-grid__row", {},
      el("td.gtma-grid__rownum", {}, String((opts.rowNumberStart ?? 1) + ri)),
      ...row.map((cell, ci) => renderCell(cell, columns[ci])),
    );
    if (opts.rowMenu) attachContextMenu(tr, () => opts.rowMenu!(ri, row));
    return tr;
  }

  function renderCell(cell: string | null, col: GridColumn): HTMLElement {
    if (cell === null) {
      return el("td.gtma-grid__cell.gtma-grid__cell--null", { attrs: { title: "NULL" } }, "NULL");
    }
    const numeric = col?.type ? NUMERIC.test(col.type) : false;
    const truncated = cell.length > 220 ? cell.slice(0, 220) + "…" : cell;
    if (col?.link) {
      return el("td.gtma-grid__cell", { class: numeric ? "gtma-grid__cell--num" : "" },
        el("button.gtma-grid__fk", {
          attrs: { type: "button", title: `Jump to referenced row (${cell})` },
          onclick: (e: MouseEvent) => { e.stopPropagation(); col.link!(cell); },
        }, el("span.mono.truncate", {}, truncated), icon("arrow-up-right-from-square")));
    }
    return el("td.gtma-grid__cell", {
      class: numeric ? "gtma-grid__cell--num mono" : "",
      attrs: { title: cell.length > 80 ? cell : null },
      ondblclick: () => copyCell(cell),
    }, truncated);
  }

  async function copyCell(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      notify.success("Cell copied");
    } catch {
      notify.error("Clipboard unavailable");
    }
  }

  render(opts.columns, opts.rows, opts.sortBy, opts.sortDir);

  return {
    el: root,
    setData: (columns, rows) => render(columns, rows, opts.sortBy, opts.sortDir),
  };
}

function spacerRow(px: number, cols: number): HTMLElement {
  return el("tr.gtma-grid__spacer", { attrs: { "aria-hidden": "true" } },
    el("td", { attrs: { colspan: cols }, style: { height: `${px}px`, padding: "0", border: "0" } }));
}

function rafThrottle(fn: () => void): () => void {
  let queued = false;
  return () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; fn(); });
  };
}

function baseType(t: string): string {
  return t.replace(/\(.*$/, "").toLowerCase();
}
