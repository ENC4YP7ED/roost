package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ForeignKey describes one foreign-key relationship from a column to another
// table's column.
type ForeignKey struct {
	Name      string `json:"name"`
	Column    string `json:"column"`
	RefSchema string `json:"refSchema"`
	RefTable  string `json:"refTable"`
	RefColumn string `json:"refColumn"`
	OnUpdate  string `json:"onUpdate"`
	OnDelete  string `json:"onDelete"`
}

// ForeignKeys returns the outgoing foreign keys of a table.
func ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	const q = `
		SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME,
		       COALESCE(k.REFERENCED_TABLE_SCHEMA,''), COALESCE(k.REFERENCED_TABLE_NAME,''),
		       COALESCE(k.REFERENCED_COLUMN_NAME,''),
		       COALESCE(r.UPDATE_RULE,''), COALESCE(r.DELETE_RULE,'')
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ForeignKey
	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(&fk.Name, &fk.Column, &fk.RefSchema, &fk.RefTable,
			&fk.RefColumn, &fk.OnUpdate, &fk.OnDelete); err != nil {
			return nil, err
		}
		out = append(out, fk)
	}
	return out, rows.Err()
}

// Condition is a single WHERE predicate for table search.
type Condition struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

// allowedOps whitelists comparison operators usable in a search condition.
var allowedOps = map[string]bool{
	"=": true, "!=": true, "<>": true, "<": true, ">": true, "<=": true, ">=": true,
	"LIKE": true, "NOT LIKE": true, "IS NULL": true, "IS NOT NULL": true,
}

func buildConditions(conds []Condition) (string, []any, error) {
	clauses := make([]string, 0, len(conds))
	args := make([]any, 0, len(conds))
	for _, c := range conds {
		op := strings.ToUpper(strings.TrimSpace(c.Op))
		if !allowedOps[op] {
			return "", nil, fmt.Errorf("unsupported operator: %q", c.Op)
		}
		col := QuoteIdent(c.Column)
		if op == "IS NULL" || op == "IS NOT NULL" {
			clauses = append(clauses, col+" "+op)
			continue
		}
		clauses = append(clauses, col+" "+op+" ?")
		args = append(args, c.Value)
	}
	return strings.Join(clauses, " AND "), args, nil
}

// Search returns a filtered, paginated page of rows plus the total count of
// rows matching the conditions. Conditions are combined with AND; values are
// always passed as bound parameters.
func Search(ctx context.Context, db *sql.DB, schema, table string, conds []Condition, limit, offset int, orderBy, dir string) (*ResultSet, int64, error) {
	where, args, err := buildConditions(conds)
	if err != nil {
		return nil, 0, err
	}
	from := QuoteIdent(schema) + "." + QuoteIdent(table)

	q := "SELECT * FROM " + from
	countQ := "SELECT COUNT(*) FROM " + from
	if where != "" {
		q += " WHERE " + where
		countQ += " WHERE " + where
	}
	if orderBy != "" {
		d := "ASC"
		if strings.EqualFold(dir, "desc") {
			d = "DESC"
		}
		q += " ORDER BY " + QuoteIdent(orderBy) + " " + d
	}
	q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	rs, err := scanRows(rows, 0)
	if err != nil {
		return nil, 0, err
	}
	rs.IsQuery = true

	var total int64
	_ = db.QueryRowContext(ctx, countQ, args...).Scan(&total)
	return rs, total, nil
}
