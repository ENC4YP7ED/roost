import { el } from "../core/dom.ts";

export interface CodeEditorOptions {
  value?: string;
  placeholder?: string;
  onChange?: (value: string) => void;
  onRun?: (value: string) => void;
}

export interface CodeEditorHandle {
  el: HTMLElement;
  get value(): string;
  set value(v: string);
  focus(): void;
  insert(text: string): void;
}

const KEYWORDS = new Set([
  "SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE",
  "CREATE", "TABLE", "DATABASE", "DROP", "ALTER", "ADD", "COLUMN", "INDEX", "PRIMARY",
  "KEY", "FOREIGN", "REFERENCES", "JOIN", "INNER", "LEFT", "RIGHT", "OUTER", "ON",
  "GROUP", "BY", "ORDER", "HAVING", "LIMIT", "OFFSET", "AS", "AND", "OR", "NOT",
  "NULL", "IS", "IN", "LIKE", "BETWEEN", "DISTINCT", "COUNT", "SUM", "AVG", "MIN",
  "MAX", "UNION", "ALL", "EXISTS", "CASE", "WHEN", "THEN", "ELSE", "END", "ASC",
  "DESC", "DEFAULT", "AUTO_INCREMENT", "UNIQUE", "CONSTRAINT", "SHOW", "USE",
  "EXPLAIN", "DESCRIBE", "TRUNCATE", "REPLACE", "GRANT", "WITH", "VIEW", "IF",
]);

/**
 * Lightweight SQL editor: a transparent textarea layered over a syntax-
 * highlighted backdrop, with a synced line-number gutter. No external deps.
 */
export function CodeEditor(opts: CodeEditorOptions): CodeEditorHandle {
  const gutter = el("div.gtma-editor__gutter", { attrs: { "aria-hidden": "true" } });
  const highlight = el("pre.gtma-editor__highlight", { attrs: { "aria-hidden": "true" } });
  const textarea = el("textarea.gtma-editor__textarea", {
    attrs: {
      placeholder: opts.placeholder ?? "",
      spellcheck: false,
      autocapitalize: "off",
      autocomplete: "off",
      autocorrect: "off",
      wrap: "off",
    },
  }) as HTMLTextAreaElement;
  textarea.value = opts.value ?? "";

  const root = el("div.gtma-editor", {}, gutter, el("div.gtma-editor__surface", {}, highlight, textarea));

  function paint() {
    const code = textarea.value;
    highlight.innerHTML = highlightSQL(code) + "\n";
    const lines = code.split("\n").length;
    const want = Math.max(lines, 1);
    if (gutter.childElementCount !== want) {
      gutter.replaceChildren();
      for (let i = 1; i <= want; i++) gutter.appendChild(el("div.gtma-editor__lineno", {}, String(i)));
    }
  }

  const syncScroll = () => {
    highlight.scrollTop = textarea.scrollTop;
    highlight.scrollLeft = textarea.scrollLeft;
    gutter.scrollTop = textarea.scrollTop;
  };

  textarea.addEventListener("input", () => { paint(); opts.onChange?.(textarea.value); });
  textarea.addEventListener("scroll", syncScroll);
  textarea.addEventListener("keydown", (e) => {
    // Run on Ctrl/Cmd+Enter.
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      opts.onRun?.(textarea.value);
      return;
    }
    // Tab inserts two spaces instead of leaving the field.
    if (e.key === "Tab") {
      e.preventDefault();
      insertAtCursor("  ");
    }
  });

  function insertAtCursor(text: string) {
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    textarea.setRangeText(text, start, end, "end");
    paint();
    opts.onChange?.(textarea.value);
  }

  paint();

  return {
    el: root,
    get value() { return textarea.value; },
    set value(v: string) { textarea.value = v; paint(); },
    focus: () => textarea.focus(),
    insert: insertAtCursor,
  };
}

function escapeHTML(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** Tokenize SQL into highlighted spans. Order matters: strings/comments first. */
function highlightSQL(code: string): string {
  const tokenizer = /(\/\*[\s\S]*?\*\/|--[^\n]*|#[^\n]*)|('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|`[^`]*`)|(\b\d+\.?\d*\b)|([A-Za-z_][A-Za-z0-9_]*)|([(),;*=<>!+\-/%.]+)/g;
  let out = "";
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = tokenizer.exec(code)) !== null) {
    out += escapeHTML(code.slice(last, m.index));
    last = tokenizer.lastIndex;
    const [full, comment, str, num, word, punct] = m;
    if (comment) out += `<span class="tok-comment">${escapeHTML(comment)}</span>`;
    else if (str) out += `<span class="tok-string">${escapeHTML(str)}</span>`;
    else if (num) out += `<span class="tok-number">${escapeHTML(num)}</span>`;
    else if (word) {
      const cls = KEYWORDS.has(word.toUpperCase()) ? "tok-keyword" : "tok-ident";
      out += `<span class="${cls}">${escapeHTML(word)}</span>`;
    } else if (punct) out += `<span class="tok-punct">${escapeHTML(punct)}</span>`;
    else out += escapeHTML(full);
  }
  out += escapeHTML(code.slice(last));
  return out;
}
