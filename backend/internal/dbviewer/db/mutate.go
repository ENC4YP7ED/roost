package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Cell is a nullable value moving between the API and the database. A nil
// pointer means SQL NULL.
type Cell = *string

// InsertRow inserts one row and returns the new auto-increment id (if any).
func InsertRow(ctx context.Context, db *sql.DB, schema, table string, values map[string]Cell) (int64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("no values provided")
	}
	cols := make([]string, 0, len(values))
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for col, val := range values {
		cols = append(cols, QuoteIdent(col))
		placeholders = append(placeholders, "?")
		args = append(args, cellArg(val))
	}
	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
		QuoteIdent(schema), QuoteIdent(table),
		strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// UpdateRow updates the row(s) matching `where`, setting `values`. It is capped
// at one row for safety. Returns rows affected.
func UpdateRow(ctx context.Context, db *sql.DB, schema, table string, values, where map[string]Cell) (int64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("no values to update")
	}
	if len(where) == 0 {
		return 0, fmt.Errorf("refusing to update without a WHERE clause")
	}

	sets := make([]string, 0, len(values))
	args := make([]any, 0, len(values)+len(where))
	for col, val := range values {
		sets = append(sets, QuoteIdent(col)+" = ?")
		args = append(args, cellArg(val))
	}
	whereSQL, whereArgs := buildWhere(where)
	args = append(args, whereArgs...)

	q := fmt.Sprintf("UPDATE %s.%s SET %s WHERE %s LIMIT 1",
		QuoteIdent(schema), QuoteIdent(table), strings.Join(sets, ", "), whereSQL)
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteRow deletes the row matching `where` (capped at one). Returns rows affected.
func DeleteRow(ctx context.Context, db *sql.DB, schema, table string, where map[string]Cell) (int64, error) {
	if len(where) == 0 {
		return 0, fmt.Errorf("refusing to delete without a WHERE clause")
	}
	whereSQL, args := buildWhere(where)
	q := fmt.Sprintf("DELETE FROM %s.%s WHERE %s LIMIT 1",
		QuoteIdent(schema), QuoteIdent(table), whereSQL)
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// buildWhere turns an identity map into "a = ? AND b IS NULL …" plus its args,
// using IS NULL for nil cells so equality works against NULLs.
func buildWhere(where map[string]Cell) (string, []any) {
	clauses := make([]string, 0, len(where))
	args := make([]any, 0, len(where))
	for col, val := range where {
		if val == nil {
			clauses = append(clauses, QuoteIdent(col)+" IS NULL")
		} else {
			clauses = append(clauses, QuoteIdent(col)+" = ?")
			args = append(args, *val)
		}
	}
	return strings.Join(clauses, " AND "), args
}

func cellArg(c Cell) any {
	if c == nil {
		return nil
	}
	return *c
}
