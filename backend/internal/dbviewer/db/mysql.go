// Package db holds the MySQL/MariaDB introspection and query helpers used by
// the API layer. Everything here operates on a *sql.DB owned by a session.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Open establishes a verified connection pool to a MySQL/MariaDB server.
func Open(ctx context.Context, host string, port int, user, pass string) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.ParseTime = true
	cfg.InterpolateParams = false
	cfg.Params = map[string]string{"charset": "utf8mb4"}

	pool, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	pool.SetConnMaxLifetime(30 * time.Minute)
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(2)

	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

// ServerInfo summarises the connected server.
type ServerInfo struct {
	Version        string `json:"version"`
	VersionComment string `json:"versionComment"`
	Charset        string `json:"charset"`
	User           string `json:"user"`
	Uptime         int64  `json:"uptime"`
}

// Info returns assorted server-wide facts shown on the home screen.
func Info(ctx context.Context, db *sql.DB) (ServerInfo, error) {
	var info ServerInfo
	_ = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&info.Version)
	_ = db.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&info.User)
	_ = scanVar(ctx, db, "version_comment", &info.VersionComment)
	_ = scanVar(ctx, db, "character_set_server", &info.Charset)
	var uptimeStr string
	if err := db.QueryRowContext(ctx,
		"SHOW GLOBAL STATUS LIKE 'Uptime'").Scan(new(string), &uptimeStr); err == nil {
		fmt.Sscan(uptimeStr, &info.Uptime)
	}
	return info, nil
}

func scanVar(ctx context.Context, db *sql.DB, name string, dst *string) error {
	return db.QueryRowContext(ctx, "SHOW VARIABLES LIKE ?", name).Scan(new(string), dst)
}

// Database is one schema with rollup stats.
type Database struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
	Tables    int    `json:"tables"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Databases lists every schema visible to the user, with table counts & sizes.
func Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	const q = `
		SELECT s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME, s.DEFAULT_COLLATION_NAME,
		       COUNT(t.TABLE_NAME),
		       COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0)
		FROM information_schema.SCHEMATA s
		LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
		GROUP BY s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME, s.DEFAULT_COLLATION_NAME
		ORDER BY s.SCHEMA_NAME`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.Charset, &d.Collation, &d.Tables, &d.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Table is one table or view with metadata.
type Table struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // BASE TABLE | VIEW
	Engine    string `json:"engine"`
	Rows      int64  `json:"rows"`
	SizeBytes int64  `json:"sizeBytes"`
	Collation string `json:"collation"`
	Comment   string `json:"comment"`
}

// Tables lists tables and views in a schema.
func Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	const q = `
		SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE,''),
		       COALESCE(TABLE_ROWS,0), COALESCE(DATA_LENGTH+INDEX_LENGTH,0),
		       COALESCE(TABLE_COLLATION,''), COALESCE(TABLE_COMMENT,'')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME`
	rows, err := db.QueryContext(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Type, &t.Engine, &t.Rows, &t.SizeBytes, &t.Collation, &t.Comment); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Column describes one column of a table's structure.
type Column struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Nullable  bool    `json:"nullable"`
	Key       string  `json:"key"` // PRI | UNI | MUL | ""
	Default   *string `json:"default"`
	Extra     string  `json:"extra"`
	Comment   string  `json:"comment"`
	Collation *string `json:"collation"`
	Position  int     `json:"position"`
}

// Columns returns the structure of a single table.
func Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	const q = `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY,
		       COLUMN_DEFAULT, EXTRA, COLUMN_COMMENT, COLLATION_NAME, ORDINAL_POSITION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Column
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &c.Key,
			&c.Default, &c.Extra, &c.Comment, &c.Collation, &c.Position); err != nil {
			return nil, err
		}
		c.Nullable = strings.EqualFold(nullable, "YES")
		out = append(out, c)
	}
	return out, rows.Err()
}

// Index describes one index (possibly multi-column).
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Type    string   `json:"type"`
	Columns []string `json:"columns"`
}

// Indexes returns indexes for a table, grouping multi-column ones together.
func Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	const q = `
		SELECT INDEX_NAME, NON_UNIQUE, INDEX_TYPE, COLUMN_NAME, SEQ_IN_INDEX
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byName := map[string]*Index{}
	for rows.Next() {
		var name, idxType, col string
		var nonUnique, seq int
		if err := rows.Scan(&name, &nonUnique, &idxType, &col, &seq); err != nil {
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			idx = &Index{Name: name, Unique: nonUnique == 0, Type: idxType}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, col)
	}
	out := make([]Index, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, rows.Err()
}

// ResultSet is the generic shape returned for any SELECT-style query.
type ResultSet struct {
	Columns      []string    `json:"columns"`
	ColumnTypes  []string    `json:"columnTypes"`
	Rows         [][]*string `json:"rows"`
	RowsAffected int64       `json:"rowsAffected"`
	LastInsertID int64       `json:"lastInsertId"`
	DurationMS   float64     `json:"durationMs"`
	IsQuery      bool        `json:"isQuery"`
	Truncated    bool        `json:"truncated"`
}

// Exec runs an arbitrary statement. SELECT/SHOW/etc. return rows; everything
// else returns affected-row counts. `schema`, when non-empty, is USEd first.
// maxRows > 0 caps how many rows are buffered (0 = unlimited).
func Exec(ctx context.Context, db *sql.DB, schema, statement string, maxRows int) (*ResultSet, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if schema != "" {
		if _, err := conn.ExecContext(ctx, "USE "+QuoteIdent(schema)); err != nil {
			return nil, err
		}
	}

	start := time.Now()
	trimmed := strings.ToUpper(strings.TrimSpace(statement))
	returnsRows := startsWithAny(trimmed, "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "TABLE", "CALL", "ANALYZE", "CHECK")

	if returnsRows {
		rows, err := conn.QueryContext(ctx, statement)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		rs, err := scanRows(rows, maxRows)
		if err != nil {
			return nil, err
		}
		rs.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
		rs.IsQuery = true
		return rs, nil
	}

	res, err := conn.ExecContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return &ResultSet{
		RowsAffected: affected,
		LastInsertID: lastID,
		DurationMS:   float64(time.Since(start).Microseconds()) / 1000.0,
		IsQuery:      false,
	}, nil
}

// Browse returns a page of rows from a table plus the total row estimate.
func Browse(ctx context.Context, db *sql.DB, schema, table string, limit, offset int, orderBy, dir string) (*ResultSet, int64, error) {
	q := "SELECT * FROM " + QuoteIdent(schema) + "." + QuoteIdent(table)
	if orderBy != "" {
		d := "ASC"
		if strings.EqualFold(dir, "desc") {
			d = "DESC"
		}
		q += " ORDER BY " + QuoteIdent(orderBy) + " " + d
	}
	q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rs, err := Exec(ctx, db, "", q, 0)
	if err != nil {
		return nil, 0, err
	}

	var total int64
	_ = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+QuoteIdent(schema)+"."+QuoteIdent(table)).Scan(&total)
	return rs, total, nil
}

// CreateTableSQL returns the SHOW CREATE TABLE statement for export/inspection.
func CreateTableSQL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	var name, ddl string
	err := db.QueryRowContext(ctx,
		"SHOW CREATE TABLE "+QuoteIdent(schema)+"."+QuoteIdent(table)).Scan(&name, &ddl)
	return ddl, err
}

func scanRows(rows *sql.Rows, maxRows int) (*ResultSet, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	colTypes, _ := rows.ColumnTypes()
	typeNames := make([]string, len(cols))
	for i, ct := range colTypes {
		typeNames[i] = ct.DatabaseTypeName()
	}

	rs := &ResultSet{Columns: cols, ColumnTypes: typeNames, Rows: [][]*string{}}
	for rows.Next() {
		if maxRows > 0 && len(rs.Rows) >= maxRows {
			rs.Truncated = true
			break
		}
		raw := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, b := range raw {
			if b == nil {
				row[i] = nil
			} else {
				s := string(b)
				row[i] = &s
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, rows.Err()
}

// QuoteIdent backtick-quotes an identifier, escaping embedded backticks.
func QuoteIdent(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func startsWithAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
