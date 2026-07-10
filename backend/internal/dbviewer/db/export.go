package db

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ExportFormat selects the serialization used by table exports.
type ExportFormat string

const (
	FormatSQL  ExportFormat = "sql"
	FormatCSV  ExportFormat = "csv"
	FormatJSON ExportFormat = "json"
)

// sqlInsertBatch controls how many rows go into one INSERT statement when
// streaming a SQL dump, so re-imports stay under max_allowed_packet.
const sqlInsertBatch = 200

// MimeFor maps an export format to its content type.
func MimeFor(format ExportFormat) string {
	switch format {
	case FormatCSV:
		return "text/csv"
	case FormatJSON:
		return "application/json"
	default:
		return "application/sql"
	}
}

// ExportSource is an opened, ready-to-stream table export. Opening it runs the
// initial query (and DDL for SQL), so errors surface *before* the caller writes
// any response headers; WriteTo then streams the body row-by-row.
type ExportSource struct {
	rows   *sql.Rows
	format ExportFormat
	table  string
	ddl    string
}

// OpenExportTable prepares a streaming export of one table.
func OpenExportTable(ctx context.Context, db *sql.DB, schema, table string, format ExportFormat) (*ExportSource, error) {
	var ddl string
	if format == FormatSQL {
		var err error
		if ddl, err = CreateTableSQL(ctx, db, schema, table); err != nil {
			return nil, err
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+QuoteIdent(schema)+"."+QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	return &ExportSource{rows: rows, format: format, table: table, ddl: ddl}, nil
}

// Close releases the underlying rows.
func (s *ExportSource) Close() {
	if s.rows != nil {
		_ = s.rows.Close()
	}
}

// Stream writes the export body to w without buffering the whole table.
func (s *ExportSource) Stream(w io.Writer) error {
	bw := bufio.NewWriterSize(w, 64*1024)
	defer bw.Flush()
	switch s.format {
	case FormatCSV:
		return streamCSV(bw, s.rows)
	case FormatJSON:
		return streamJSON(bw, s.rows)
	default:
		return streamSQLTable(bw, s.table, s.ddl, s.rows)
	}
}

// ExportDatabaseTo streams a SQL dump of every base table in a schema to w.
func ExportDatabaseTo(ctx context.Context, db *sql.DB, schema string, w io.Writer) error {
	tables, err := Tables(ctx, db, schema)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 64*1024)
	defer bw.Flush()

	fmt.Fprintf(bw, "-- GoTypeMyAdmin dump\n-- Database: %s\n-- Generated: %s\n\n",
		schema, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(bw, "CREATE DATABASE IF NOT EXISTS %s;\nUSE %s;\n\n", QuoteIdent(schema), QuoteIdent(schema))

	for _, t := range tables {
		if t.Type == "VIEW" {
			continue
		}
		ddl, err := CreateTableSQL(ctx, db, schema, t.Name)
		if err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+QuoteIdent(schema)+"."+QuoteIdent(t.Name))
		if err != nil {
			return err
		}
		if err := streamSQLTable(bw, t.Name, ddl, rows); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		bw.WriteString("\n")
	}
	return nil
}

// ---- per-format streamers --------------------------------------------------

func streamCSV(w io.Writer, rows *sql.Rows) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return err
	}
	rec := make([]string, len(cols))
	err = scanEach(rows, func(row []*string) error {
		for i, c := range row {
			if c == nil {
				rec[i] = ""
			} else {
				rec[i] = *c
			}
		}
		return cw.Write(rec)
	})
	cw.Flush()
	if err == nil {
		err = cw.Error()
	}
	return err
}

func streamJSON(w io.Writer, rows *sql.Rows) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return err
	}
	first := true
	obj := make(map[string]any, len(cols))
	err = scanEach(rows, func(row []*string) error {
		if !first {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		first = false
		for i, col := range cols {
			if row[i] == nil {
				obj[col] = nil
			} else {
				obj[col] = *row[i]
			}
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		_, err = w.Write(append([]byte("  "), b...))
		return err
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n]\n")
	return err
}

func streamSQLTable(w io.Writer, table, ddl string, rows *sql.Rows) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "-- Table: %s\nDROP TABLE IF EXISTS %s;\n", table, QuoteIdent(table))
	io.WriteString(w, ddl)
	io.WriteString(w, ";\n\n")

	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = QuoteIdent(c)
	}
	colList := strings.Join(quoted, ", ")

	n := 0
	err = scanEach(rows, func(row []*string) error {
		if n%sqlInsertBatch == 0 {
			if n > 0 {
				io.WriteString(w, ";\n")
			}
			fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES\n  (", QuoteIdent(table), colList)
		} else {
			io.WriteString(w, ",\n  (")
		}
		for j, cell := range row {
			if j > 0 {
				io.WriteString(w, ", ")
			}
			io.WriteString(w, sqlLiteral(cell))
		}
		io.WriteString(w, ")")
		n++
		return nil
	})
	if n > 0 {
		io.WriteString(w, ";\n")
	}
	return err
}

// scanEach reads rows one at a time into reusable buffers and hands each row to
// fn — no full-table buffering.
func scanEach(rows *sql.Rows, fn func(row []*string) error) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	raw := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	row := make([]*string, len(cols))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, b := range raw {
			if b == nil {
				row[i] = nil
			} else {
				s := string(b)
				row[i] = &s
			}
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// sqlLiteral renders a cell as a SQL literal (NULL or single-quoted, escaped).
func sqlLiteral(cell *string) string {
	if cell == nil {
		return "NULL"
	}
	s := *cell
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return "'" + s + "'"
}
