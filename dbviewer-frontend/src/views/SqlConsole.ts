import { el, icon, clear } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { CodeEditor } from "../components/CodeEditor.ts";
import { DataGrid } from "../components/DataGrid.ts";
import { Badge, EmptyState } from "../components/misc.ts";
import { api, ApiError } from "../api/client.ts";
import { refreshTree } from "../state/store.ts";
import { formatDuration, formatNumber } from "../util/format.ts";
import type { ImportResult, ResultSet } from "../api/types.ts";

export interface SqlConsoleOptions {
  database?: string;
  initialSQL?: string;
}

/** A SQL editor + result panel. Runs against an optional default database. */
export function SqlConsole(opts: SqlConsoleOptions = {}): HTMLElement {
  const resultArea = el("div.gtma-sql__result.grow");

  const editor = CodeEditor({
    value: opts.initialSQL ?? "SELECT * FROM information_schema.TABLES LIMIT 25;",
    placeholder: "Write SQL…  (Ctrl/Cmd + Enter to run)",
    onRun: () => run(),
  });

  const dbBadge = opts.database
    ? Badge(opts.database, "outline", { icon: "database" })
    : Badge("no database selected", "neutral", { icon: "circle-info" });

  let running = false;
  const runBtn = Button({ label: "Run", icon: "play", variant: "primary", onClick: () => run() });

  // Hidden file input drives the "Import .sql" action. The File is uploaded as
  // a stream (never read into a JS string), so the import size is unbounded.
  const fileInput = el("input", {
    attrs: { type: "file", accept: ".sql,.txt", hidden: true },
    onchange: async (e: Event) => {
      const file = (e.target as HTMLInputElement).files?.[0];
      if (!file) return;
      await runImport(file, file.name);
      (e.target as HTMLInputElement).value = "";
    },
  }) as HTMLInputElement;
  const importBtn = Button({ label: "Import .sql", icon: "file-import", onClick: () => fileInput.click() });

  async function runImport(body: string | Blob, filename: string) {
    if (running) return;
    running = true;
    clear(resultArea);
    resultArea.appendChild(el("div.gtma-sql__running", icon("spinner", { spin: true }), el("span.muted", {}, `Importing ${filename}…`)));
    try {
      const result = await api.importSQL(opts.database ?? "", body);
      showImport(result, filename);
      refreshTree();
    } catch (err) {
      showError(err);
    } finally {
      running = false;
    }
  }

  function showImport(r: ImportResult, filename: string) {
    clear(resultArea);
    const failed = r.failedAt > 0;
    resultArea.append(
      el("div.gtma-sql__meta",
        Badge(filename, "outline", { icon: "file-import" }),
        Badge(`${r.executed}/${r.statements} statements`, failed ? "warning" : "success", { icon: "list-check" }),
        Badge(`${formatNumber(r.affected)} affected`, "neutral", { icon: "pen" }),
        Badge(formatDuration(r.durationMs), "neutral", { icon: "stopwatch" }),
      ),
      failed
        ? el("div.gtma-sql__error",
            el("div.row.gap-2", icon("circle-xmark"), el("span.strong", {}, `Failed at statement ${r.failedAt}`)),
            el("pre.gtma-sql__error-msg.mono", {}, r.error))
        : EmptyState({ icon: "circle-check", title: "Import complete", description: `${r.executed} statements executed.` }),
    );
  }

  function showError(err: unknown) {
    const msg = err instanceof ApiError ? err.message : String(err);
    clear(resultArea);
    resultArea.appendChild(el("div.gtma-sql__error",
      el("div.row.gap-2", icon("circle-xmark"), el("span.strong", {}, "Failed")),
      el("pre.gtma-sql__error-msg.mono", {}, msg),
    ));
  }

  async function run() {
    const sql = editor.value.trim();
    if (!sql || running) return;
    running = true;
    runBtn.setAttribute("disabled", "");
    clear(resultArea);
    resultArea.appendChild(el("div.gtma-sql__running", icon("spinner", { spin: true }), el("span.muted", {}, "Executing…")));
    try {
      const result = await api.query(sql, opts.database);
      showResult(result);
      if (!result.isQuery) refreshTree();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : String(err);
      clear(resultArea);
      resultArea.appendChild(el("div.gtma-sql__error",
        el("div.row.gap-2", icon("circle-xmark"), el("span.strong", {}, "Query failed")),
        el("pre.gtma-sql__error-msg.mono", {}, msg),
      ));
    } finally {
      running = false;
      runBtn.removeAttribute("disabled");
    }
  }

  function showResult(rs: ResultSet) {
    clear(resultArea);
    const meta = el("div.gtma-sql__meta",
      rs.isQuery
        ? Badge(`${formatNumber(rs.rows.length)} rows`, "success", { icon: "table-list" })
        : Badge(`${formatNumber(rs.rowsAffected)} affected`, "success", { icon: "check" }),
      Badge(formatDuration(rs.durationMs), "neutral", { icon: "stopwatch" }),
      rs.lastInsertId ? Badge(`insert id ${rs.lastInsertId}`, "neutral", { icon: "hashtag" }) : null,
      rs.truncated ? Badge("truncated", "warning", { icon: "scissors" }) : null,
    );

    if (rs.isQuery) {
      if (!rs.columns.length) {
        resultArea.append(meta, EmptyState({ icon: "circle-check", title: "Done", description: "Statement executed." }));
        return;
      }
      const grid = DataGrid({
        columns: rs.columns.map((name, i) => ({ name, type: rs.columnTypes[i] })),
        rows: rs.rows,
        virtual: true,
      });
      resultArea.append(meta, el("div.gtma-sql__gridwrap", {}, grid.el));
    } else {
      resultArea.append(meta, EmptyState({ icon: "circle-check", title: "Statement executed", description: `${formatNumber(rs.rowsAffected)} row(s) affected.` }));
    }
  }

  const toolbar = el("div.gtma-sql__toolbar",
    el("div.row.gap-2", icon("terminal", { class: "muted" }), el("span.strong", {}, "SQL console"), dbBadge),
    el("span.spacer"),
    el("span.gtma-sql__hint.faint", el("kbd", {}, "Ctrl"), "+", el("kbd", {}, "↵")),
    importBtn,
    fileInput,
    runBtn,
  );

  // Show an idle placeholder before first run.
  resultArea.appendChild(EmptyState({ icon: "play", title: "Run a query", description: "Results appear here." }));

  return el("div.gtma-sql.col.grow",
    toolbar,
    el("div.gtma-sql__editor", editor.el),
    resultArea,
  );
}
