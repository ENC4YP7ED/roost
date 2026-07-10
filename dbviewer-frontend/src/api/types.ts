// Mirror of the Go API response shapes.

export interface ServerInfo {
  version: string;
  versionComment: string;
  charset: string;
  user: string;
  uptime: number;
}

export interface ConnectResult {
  token: string;
  server: string;
  user: string;
  info: ServerInfo;
}

export interface DatabaseMeta {
  name: string;
  charset: string;
  collation: string;
  tables: number;
  sizeBytes: number;
}

export interface TableMeta {
  name: string;
  type: string;
  engine: string;
  rows: number;
  sizeBytes: number;
  collation: string;
  comment: string;
}

export interface ColumnMeta {
  name: string;
  type: string;
  nullable: boolean;
  key: string;
  default: string | null;
  extra: string;
  comment: string;
  collation: string | null;
  position: number;
}

export interface IndexMeta {
  name: string;
  unique: boolean;
  type: string;
  columns: string[];
}

export interface ResultSet {
  columns: string[];
  columnTypes: string[];
  rows: Array<Array<string | null>>;
  rowsAffected: number;
  lastInsertId: number;
  durationMs: number;
  isQuery: boolean;
  truncated?: boolean;
}

export interface BrowseResult {
  result: ResultSet;
  total: number;
  limit: number;
  offset: number;
}

export interface ImportResult {
  statements: number;
  executed: number;
  affected: number;
  durationMs: number;
  failedAt: number;
  error: string;
}

export interface UserMeta {
  user: string;
  host: string;
  superUser: boolean;
  locked: boolean;
}

export interface ForeignKey {
  name: string;
  column: string;
  refSchema: string;
  refTable: string;
  refColumn: string;
  onUpdate: string;
  onDelete: string;
}

export interface SearchCondition {
  column: string;
  op: string;
  value: string;
}

export type ExportFormat = "sql" | "csv" | "json";

/** A nullable cell value used when writing rows (null === SQL NULL). */
export type CellValue = string | null;
