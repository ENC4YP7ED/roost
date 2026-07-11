package db

import (
	"strings"
	"testing"
)

// These cover the SQL-safety helpers — the code that keeps identifiers and
// literals from breaking out of a statement — plus statement splitting. None
// need a live database.

func TestQuoteIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"users", "`users`"},
		{"my table", "`my table`"},
		{"has`tick", "`has``tick`"}, // embedded backtick doubled
		{"`; DROP TABLE x;--", "```; DROP TABLE x;--`"},
		{"", "``"},
	}
	for _, tc := range cases {
		if got := QuoteIdent(tc.in); got != tc.want {
			t.Errorf("QuoteIdent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStartsWithAny(t *testing.T) {
	if !startsWithAny("SELECT 1", "INSERT", "SELECT") {
		t.Error("startsWithAny missed a matching prefix")
	}
	if startsWithAny("DELETE", "SELECT", "INSERT") {
		t.Error("startsWithAny matched a non-prefix")
	}
	if startsWithAny("x") {
		t.Error("startsWithAny with no prefixes should be false")
	}
}

func TestQuoteString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"O'Brien", "'O''Brien'"},
		{`back\slash`, `'back\\slash'`},
		{`'; DROP--`, "'''; DROP--'"},
	}
	for _, tc := range cases {
		if got := quoteString(tc.in); got != tc.want {
			t.Errorf("quoteString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSQLLiteral(t *testing.T) {
	if got := sqlLiteral(nil); got != "NULL" {
		t.Errorf("sqlLiteral(nil) = %q, want NULL", got)
	}
	s := "line1\nline2\r\t'q'\\x"
	got := sqlLiteral(&s)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("sqlLiteral not quoted: %q", got)
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Errorf("sqlLiteral left raw newlines: %q", got)
	}
	if !strings.Contains(got, "''q''") {
		t.Errorf("sqlLiteral did not double the quote: %q", got)
	}
}

func TestMimeFor(t *testing.T) {
	cases := map[ExportFormat]string{
		FormatCSV:  "text/csv",
		FormatJSON: "application/json",
		FormatSQL:  "application/sql",
		"unknown":  "application/sql",
	}
	for f, want := range cases {
		if got := MimeFor(f); got != want {
			t.Errorf("MimeFor(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestSanitizePrivileges(t *testing.T) {
	for _, in := range []string{"", "ALL", "all privileges"} {
		got, err := sanitizePrivileges(in)
		if err != nil || got != "ALL PRIVILEGES" {
			t.Errorf("sanitizePrivileges(%q) = %q, %v", in, got, err)
		}
	}
	if got, err := sanitizePrivileges("select, insert"); err != nil || got != "SELECT, INSERT" {
		t.Errorf("valid privilege list = %q, %v", got, err)
	}
	// An injection attempt is rejected, not quoted.
	if _, err := sanitizePrivileges("SELECT; DROP TABLE users"); err == nil {
		t.Error("sanitizePrivileges accepted an injection")
	}
	if _, err := sanitizePrivileges("NONEXISTENT"); err == nil {
		t.Error("sanitizePrivileges accepted an unknown privilege")
	}
}

func TestSanitizeScope(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "*.*", true},
		{"*.*", "*.*", true},
		{"shop.*", "`shop`.*", true},
		{"shop.orders", "`shop`.`orders`", true},
		{"`shop`.`orders`", "`shop`.`orders`", true},
		{"no-dot", "", false},
		{"a.b.c", "", false}, // SplitN(2) → "a" and "b.c"; "b.c" is not a bare ident
		{"bad;name.*", "", false},
	}
	for _, tc := range cases {
		got, err := sanitizeScope(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("sanitizeScope(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		} else if err == nil {
			t.Errorf("sanitizeScope(%q) = %q, want an error", tc.in, got)
		}
	}
}

func TestScopeIdentRejectsInjection(t *testing.T) {
	if _, err := scopeIdent("users`; DROP"); err == nil {
		t.Error("scopeIdent accepted an identifier with a backtick/semicolon")
	}
	if got, err := scopeIdent("*"); err != nil || got != "*" {
		t.Errorf("scopeIdent(*) = %q, %v", got, err)
	}
}

func TestBuildConditions(t *testing.T) {
	where, args, err := buildConditions([]Condition{
		{Column: "name", Op: "=", Value: "alice"},
		{Column: "age", Op: ">", Value: "18"},
		{Column: "deleted", Op: "IS NULL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if where != "`name` = ? AND `age` > ? AND `deleted` IS NULL" {
		t.Errorf("where = %q", where)
	}
	if len(args) != 2 {
		t.Errorf("args = %v, want 2 bound values", args)
	}
	// An unknown operator is refused (no injection via the op).
	if _, _, err := buildConditions([]Condition{{Column: "x", Op: "; DROP", Value: "y"}}); err == nil {
		t.Error("buildConditions accepted an unknown operator")
	}
}

func TestBuildWhere(t *testing.T) {
	val := "v"
	where, args := buildWhere(map[string]Cell{"id": &val})
	if where != "`id` = ?" || len(args) != 1 || args[0] != "v" {
		t.Errorf("buildWhere = %q, %v", where, args)
	}
	// A nil cell becomes IS NULL with no bound arg.
	nullWhere, nullArgs := buildWhere(map[string]Cell{"deleted": nil})
	if nullWhere != "`deleted` IS NULL" || len(nullArgs) != 0 {
		t.Errorf("buildWhere(nil) = %q, %v", nullWhere, nullArgs)
	}
}

func TestCellArg(t *testing.T) {
	if cellArg(nil) != nil {
		t.Error("cellArg(nil) should be nil")
	}
	v := "x"
	if got := cellArg(&v); got != "x" {
		t.Errorf("cellArg(&\"x\") = %v", got)
	}
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{"simple", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"trailing no-semicolon", "SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in string literal", "INSERT INTO t VALUES ('a;b'); SELECT 1;", []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"}},
		{"line comment", "SELECT 1; -- a comment; still comment\nSELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"block comment", "SELECT 1; /* block ; comment */ SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"empty", "   ;  ; ", nil},
		{"double-quoted string", `INSERT INTO t VALUES ("x;y"); SELECT 1;`, []string{`INSERT INTO t VALUES ("x;y")`, "SELECT 1"}},
		{"escaped quote in string", "INSERT INTO t VALUES ('O\\'Brien; test');", []string{"INSERT INTO t VALUES ('O\\'Brien; test')"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitStatements(tc.script)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitStatements(%q) = %#v, want %#v", tc.script, got, tc.want)
			}
			for i := range got {
				if strings.TrimSpace(got[i]) != strings.TrimSpace(tc.want[i]) {
					t.Errorf("statement %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
