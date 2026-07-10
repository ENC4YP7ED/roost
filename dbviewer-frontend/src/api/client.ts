import type {
  BrowseResult, CellValue, ColumnMeta, ConnectResult, DatabaseMeta,
  ExportFormat, ForeignKey, ImportResult, IndexMeta, ResultSet, SearchCondition,
  ServerInfo, TableMeta, UserMeta,
} from "./types.ts";

const TOKEN_KEY = "roost.dbviewer.token";

/** The viewer is mounted under /dbviewer/ inside the Roost panel. */
const API_BASE = "/dbviewer/api";

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

class ApiClient {
  private token: string | null = localStorage.getItem(TOKEN_KEY);

  get authenticated(): boolean {
    return !!this.token;
  }

  setToken(token: string | null): void {
    this.token = token;
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const res = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });

    if (res.status === 204) return undefined as T;

    let payload: unknown = null;
    const text = await res.text();
    if (text) {
      try { payload = JSON.parse(text); } catch { payload = { error: text }; }
    }

    if (!res.ok) {
      const msg = (payload as { error?: string })?.error ?? `Request failed (${res.status})`;
      if (res.status === 401) this.setToken(null);
      throw new ApiError(msg, res.status);
    }
    return payload as T;
  }

  /** Fetch a raw text body (for file exports). */
  private async requestText(path: string): Promise<string> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const res = await fetch(`${API_BASE}${path}`, { headers });
    const text = await res.text();
    if (!res.ok) {
      let msg = `Request failed (${res.status})`;
      try { msg = JSON.parse(text).error ?? msg; } catch { /* keep default */ }
      throw new ApiError(msg, res.status);
    }
    return text;
  }

  // ---- connection ----
  async connect(input: { host: string; port: number; user: string; password: string }): Promise<ConnectResult> {
    const res = await this.request<ConnectResult>("POST", "/connect", input);
    this.setToken(res.token);
    return res;
  }

  async disconnect(): Promise<void> {
    try { await this.request("POST", "/disconnect"); } finally { this.setToken(null); }
  }

  session(): Promise<{ server: string; user: string }> {
    return this.request("GET", "/session");
  }

  // ---- server / databases ----
  serverInfo(): Promise<ServerInfo> {
    return this.request("GET", "/server/info");
  }

  async databases(): Promise<DatabaseMeta[]> {
    const r = await this.request<{ databases: DatabaseMeta[] }>("GET", "/databases");
    return r.databases ?? [];
  }

  createDatabase(name: string, charset?: string, collation?: string): Promise<void> {
    return this.request("POST", "/databases", { name, charset, collation });
  }

  dropDatabase(name: string): Promise<void> {
    return this.request("DELETE", `/databases/${encodeURIComponent(name)}`);
  }

  // ---- tables ----
  async tables(db: string): Promise<TableMeta[]> {
    const r = await this.request<{ tables: TableMeta[] }>("GET", `/databases/${encodeURIComponent(db)}/tables`);
    return r.tables ?? [];
  }

  async columns(db: string, table: string): Promise<ColumnMeta[]> {
    const r = await this.request<{ columns: ColumnMeta[] }>("GET", `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/columns`);
    return r.columns ?? [];
  }

  async indexes(db: string, table: string): Promise<IndexMeta[]> {
    const r = await this.request<{ indexes: IndexMeta[] }>("GET", `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/indexes`);
    return r.indexes ?? [];
  }

  browse(db: string, table: string, params: { limit: number; offset: number; orderBy?: string; dir?: string }): Promise<BrowseResult> {
    const q = new URLSearchParams({ limit: String(params.limit), offset: String(params.offset) });
    if (params.orderBy) q.set("orderBy", params.orderBy);
    if (params.dir) q.set("dir", params.dir);
    return this.request("GET", `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/rows?${q}`);
  }

  async foreignKeys(db: string, table: string): Promise<ForeignKey[]> {
    const r = await this.request<{ foreignKeys: ForeignKey[] }>("GET", `${this.tablePath(db, table)}/foreign-keys`);
    return r.foreignKeys ?? [];
  }

  search(db: string, table: string, params: { conditions: SearchCondition[]; limit: number; offset: number; orderBy?: string; dir?: string }): Promise<BrowseResult> {
    return this.request("POST", `${this.tablePath(db, table)}/search`, params);
  }

  async ddl(db: string, table: string): Promise<string> {
    const r = await this.request<{ ddl: string }>("GET", `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/ddl`);
    return r.ddl;
  }

  dropTable(db: string, table: string): Promise<void> {
    return this.request("DELETE", `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}`);
  }

  // ---- row mutations ----
  private tablePath(db: string, table: string): string {
    return `/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}`;
  }

  async insertRow(db: string, table: string, values: Record<string, CellValue>): Promise<number> {
    const r = await this.request<{ insertId: number }>("POST", `${this.tablePath(db, table)}/insert`, { values });
    return r.insertId;
  }

  async updateRow(db: string, table: string, where: Record<string, CellValue>, values: Record<string, CellValue>): Promise<number> {
    const r = await this.request<{ affected: number }>("POST", `${this.tablePath(db, table)}/update`, { where, values });
    return r.affected;
  }

  async deleteRow(db: string, table: string, where: Record<string, CellValue>): Promise<number> {
    const r = await this.request<{ affected: number }>("POST", `${this.tablePath(db, table)}/delete`, { where });
    return r.affected;
  }

  // ---- export / import ----
  /** Fetch export text (used for the preview pane). */
  exportTable(db: string, table: string, format: ExportFormat): Promise<string> {
    return this.requestText(`${this.tablePath(db, table)}/export?format=${format}`);
  }

  /** Authenticated URL the browser can download directly (streamed to disk). */
  exportTableHref(db: string, table: string, format: ExportFormat): string {
    const q = new URLSearchParams({ format });
    if (this.token) q.set("token", this.token);
    return `${API_BASE}${this.tablePath(db, table)}/export?${q}`;
  }

  exportDatabaseHref(db: string): string {
    const q = new URLSearchParams();
    if (this.token) q.set("token", this.token);
    return `${API_BASE}/databases/${encodeURIComponent(db)}/export?${q}`;
  }

  /** Stream a SQL script (string or a File/Blob) to the import endpoint without
   *  buffering it as JSON; the body is uploaded directly. */
  async importSQL(database: string, body: string | Blob): Promise<ImportResult> {
    const headers: Record<string, string> = { "Content-Type": "application/sql" };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const res = await fetch(`${API_BASE}/import?database=${encodeURIComponent(database)}`, { method: "POST", headers, body });
    const text = await res.text();
    let payload: unknown = null;
    try { payload = JSON.parse(text); } catch { payload = { error: text }; }
    if (!res.ok) {
      if (res.status === 401) this.setToken(null);
      throw new ApiError((payload as { error?: string })?.error ?? `Request failed (${res.status})`, res.status);
    }
    return (payload as { result: ImportResult }).result;
  }

  // ---- users ----
  async users(): Promise<UserMeta[]> {
    const r = await this.request<{ users: UserMeta[] }>("GET", "/users");
    return r.users ?? [];
  }

  createUser(input: { user: string; host: string; password: string; privileges?: string; scope?: string }): Promise<void> {
    return this.request("POST", "/users", input);
  }

  dropUser(user: string, host: string): Promise<void> {
    return this.request("DELETE", `/users/${encodeURIComponent(user)}/${encodeURIComponent(host)}`);
  }

  async grants(user: string, host: string): Promise<string[]> {
    const r = await this.request<{ grants: string[] }>("GET", `/users/${encodeURIComponent(user)}/${encodeURIComponent(host)}/grants`);
    return r.grants ?? [];
  }

  // ---- arbitrary SQL ----
  async query(sql: string, database?: string): Promise<ResultSet> {
    const r = await this.request<{ result: ResultSet }>("POST", "/query", { sql, database: database ?? "" });
    return r.result;
  }
}

export const api = new ApiClient();
