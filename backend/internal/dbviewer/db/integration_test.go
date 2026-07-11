package db

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// These integration tests run against a real MySQL/MariaDB when ROOST_TEST_MYSQL
// is set (host:port for a root account whose password is ROOST_TEST_MYSQL_PW,
// default "testpw"). The Makefile / CI bring up a container; locally:
//
//	docker run -d --name roost-mariadb -e MARIADB_ROOT_PASSWORD=testpw \
//	  -p 13306:3306 mariadb:11
//	ROOST_TEST_MYSQL=127.0.0.1:13306 go test ./internal/dbviewer/...
func mysqlAddr(t *testing.T) (host string, port int) {
	addr := os.Getenv("ROOST_TEST_MYSQL")
	if addr == "" {
		t.Skip("set ROOST_TEST_MYSQL=host:port to run database-viewer integration tests")
	}
	h, p, ok := strings.Cut(addr, ":")
	if !ok {
		t.Fatalf("ROOST_TEST_MYSQL=%q must be host:port", addr)
	}
	port = 0
	for _, r := range p {
		port = port*10 + int(r-'0')
	}
	return h, port
}

func mysqlPassword() string {
	if pw := os.Getenv("ROOST_TEST_MYSQL_PW"); pw != "" {
		return pw
	}
	return "testpw"
}

// TestFullDatabaseViewerFlow exercises the whole db package end to end against
// a live server: schema introspection, browsing, searching, row mutation,
// DDL, export, users/grants, and script import.
func TestFullDatabaseViewerFlow(t *testing.T) {
	host, port := mysqlAddr(t)
	ctx := context.Background()
	pool, err := Open(ctx, host, port, "root", mysqlPassword())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	// Info reports the server version.
	info, err := Info(ctx, pool)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Version == "" {
		t.Error("Info returned no version")
	}

	// Provision a throwaway schema.
	const schema = "roost_viewer_test"
	if _, err := pool.ExecContext(ctx, "DROP DATABASE IF EXISTS "+QuoteIdent(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, "CREATE DATABASE "+QuoteIdent(schema)); err != nil {
		t.Fatal(err)
	}
	defer pool.ExecContext(ctx, "DROP DATABASE IF EXISTS "+QuoteIdent(schema))

	// Import a script that builds a schema with a relationship.
	script := `
		CREATE TABLE customers (
			id INT PRIMARY KEY AUTO_INCREMENT,
			name VARCHAR(100) NOT NULL,
			email VARCHAR(200)
		);
		CREATE TABLE orders (
			id INT PRIMARY KEY AUTO_INCREMENT,
			customer_id INT NOT NULL,
			total DECIMAL(10,2) NOT NULL DEFAULT 0,
			FOREIGN KEY (customer_id) REFERENCES customers(id)
		);
		INSERT INTO customers (name, email) VALUES ('Grace Hopper', 'grace@example.com');
		INSERT INTO customers (name, email) VALUES ('Ada Lovelace', NULL);
		INSERT INTO orders (customer_id, total) VALUES (1, 99.99);
	`
	res, err := RunScript(ctx, pool, schema, script)
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if res.Executed < 5 {
		t.Errorf("RunScript executed %d statements, want >= 5", res.Executed)
	}

	// Databases includes our schema.
	dbs, err := Databases(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range dbs {
		if d.Name == schema {
			found = true
			if d.Tables < 2 {
				t.Errorf("schema reports %d tables, want >= 2", d.Tables)
			}
		}
	}
	if !found {
		t.Fatal("created schema not listed by Databases")
	}

	// Tables, columns, indexes.
	tables, err := Tables(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Errorf("Tables = %d, want 2", len(tables))
	}
	cols, err := Columns(ctx, pool, schema, "customers")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 3 {
		t.Errorf("Columns(customers) = %d, want 3", len(cols))
	}
	if _, err := Indexes(ctx, pool, schema, "customers"); err != nil {
		t.Fatalf("Indexes: %v", err)
	}

	// Foreign keys on orders reference customers.
	fks, err := ForeignKeys(ctx, pool, schema, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(fks) != 1 || fks[0].RefTable != "customers" {
		t.Errorf("ForeignKeys = %+v", fks)
	}

	// Browse the customers table.
	rs, _, err := Browse(ctx, pool, schema, "customers", 10, 0, "id", "asc")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) != 2 {
		t.Errorf("Browse returned %d rows, want 2", len(rs.Rows))
	}

	// Search with a bound condition.
	page, total, err := Search(ctx, pool, schema, "customers",
		[]Condition{{Column: "name", Op: "LIKE", Value: "Grace%"}}, 10, 0, "id", "asc")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page.Rows) != 1 {
		t.Errorf("Search matched total=%d rows=%d, want 1/1", total, len(page.Rows))
	}

	// Insert / update / delete a row.
	name := "Katherine Johnson"
	email := "kj@example.com"
	if _, err := InsertRow(ctx, pool, schema, "customers", map[string]Cell{"name": &name, "email": &email}); err != nil {
		t.Fatalf("InsertRow: %v", err)
	}
	newEmail := "katherine@nasa.gov"
	if _, err := UpdateRow(ctx, pool, schema, "customers",
		map[string]Cell{"email": &newEmail}, map[string]Cell{"name": &name}); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}
	if _, err := DeleteRow(ctx, pool, schema, "customers", map[string]Cell{"name": &name}); err != nil {
		t.Fatalf("DeleteRow: %v", err)
	}
	if _, total, _ := Search(ctx, pool, schema, "customers",
		[]Condition{{Column: "name", Op: "=", Value: name}}, 10, 0, "", ""); total != 0 {
		t.Error("deleted row still present")
	}

	// DDL for a table.
	ddl, err := CreateTableSQL(ctx, pool, schema, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(ddl), "CREATE TABLE") {
		t.Errorf("CreateTableSQL = %q", ddl)
	}

	// A read-only query via Exec.
	result, err := Exec(ctx, pool, schema, "SELECT COUNT(*) AS n FROM customers", 100)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("Exec returned %d rows", len(result.Rows))
	}

	// Export the table as SQL and as CSV.
	for _, format := range []ExportFormat{FormatSQL, FormatCSV, FormatJSON} {
		src, err := OpenExportTable(ctx, pool, schema, "orders", format)
		if err != nil {
			t.Fatalf("OpenExportTable(%s): %v", format, err)
		}
		var buf bytes.Buffer
		if err := src.Stream(&buf); err != nil {
			src.Close()
			t.Fatalf("export Stream(%s): %v", format, err)
		}
		src.Close()
		if buf.Len() == 0 {
			t.Errorf("export(%s) produced no output", format)
		}
	}

	// Whole-database export.
	var dbDump bytes.Buffer
	if err := ExportDatabaseTo(ctx, pool, schema, &dbDump); err != nil {
		t.Fatalf("ExportDatabaseTo: %v", err)
	}
	if !strings.Contains(dbDump.String(), "CREATE TABLE") {
		t.Error("database export missing CREATE TABLE")
	}
}

// TestUserManagement covers account creation, grants and removal.
func TestUserManagement(t *testing.T) {
	host, port := mysqlAddr(t)
	ctx := context.Background()
	pool, err := Open(ctx, host, port, "root", mysqlPassword())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	const user, hostPat = "roost_test_user", "%"
	// Clean up any leftover from a previous run.
	DropUser(ctx, pool, user, hostPat)

	if err := CreateUser(ctx, pool, user, hostPat, "s3cretPassw0rd!"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := GrantPrivileges(ctx, pool, user, hostPat, "SELECT, INSERT", "*.*"); err != nil {
		t.Fatalf("GrantPrivileges: %v", err)
	}
	// A malicious privilege string must be rejected, not executed.
	if err := GrantPrivileges(ctx, pool, user, hostPat, "SELECT; DROP TABLE mysql.user", "*.*"); err == nil {
		t.Error("GrantPrivileges accepted an injection")
	}

	users, err := Users(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	present := false
	for _, u := range users {
		if u.User == user {
			present = true
		}
	}
	if !present {
		t.Error("created user not listed")
	}

	grants, err := Grants(ctx, pool, user, hostPat)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) == 0 {
		t.Error("no grants returned for the new user")
	}

	if err := DropUser(ctx, pool, user, hostPat); err != nil {
		t.Fatalf("DropUser: %v", err)
	}
}

// TestOpenRejectsBadCredentials covers the connection error path.
func TestOpenRejectsBadCredentials(t *testing.T) {
	host, port := mysqlAddr(t)
	if _, err := Open(context.Background(), host, port, "root", "wrong-password"); err == nil {
		t.Error("Open accepted the wrong password")
	}
}

// TestRunScriptStreamAndExecWrite covers the streaming import path and a
// write-mode Exec (INSERT), which the earlier flow test did not reach.
func TestRunScriptStreamAndExecWrite(t *testing.T) {
	host, port := mysqlAddr(t)
	ctx := context.Background()
	pool, err := Open(ctx, host, port, "root", mysqlPassword())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const schema = "roost_stream_test"
	pool.ExecContext(ctx, "DROP DATABASE IF EXISTS "+QuoteIdent(schema))
	pool.ExecContext(ctx, "CREATE DATABASE "+QuoteIdent(schema))
	defer pool.ExecContext(ctx, "DROP DATABASE IF EXISTS "+QuoteIdent(schema))

	// Streaming import: build a table + rows, including a line comment and a
	// block comment that the streaming splitter must skip.
	script := "-- a leading comment\n" +
		"CREATE TABLE notes (id INT PRIMARY KEY AUTO_INCREMENT, body TEXT);\n" +
		"/* block ; comment */ INSERT INTO notes (body) VALUES ('has ; semicolon');\n" +
		"INSERT INTO notes (body) VALUES ('second');\n"
	res, err := RunScriptStream(ctx, pool, schema, strings.NewReader(script))
	if err != nil {
		t.Fatalf("RunScriptStream: %v", err)
	}
	if res.Executed < 3 {
		t.Errorf("streamed import executed %d statements, want >= 3", res.Executed)
	}

	// A write-mode Exec (INSERT) reports rows affected.
	w, err := Exec(ctx, pool, schema, "INSERT INTO notes (body) VALUES ('via exec')", 100)
	if err != nil {
		t.Fatalf("Exec write: %v", err)
	}
	if w.RowsAffected < 1 {
		t.Errorf("Exec INSERT RowsAffected = %d, want >= 1", w.RowsAffected)
	}

	// A failing statement inside a script is reported via FailedAt/Error, not
	// a returned error (partial progress is preserved).
	bad := "CREATE TABLE ok (id INT); INSERT INTO nonexistent_xyz VALUES (1);"
	r2, err := RunScript(ctx, pool, schema, bad)
	if err != nil {
		t.Fatalf("RunScript returned a hard error: %v", err)
	}
	if r2.FailedAt != 2 || r2.Error == "" {
		t.Errorf("RunScript should report the failing statement: FailedAt=%d err=%q", r2.FailedAt, r2.Error)
	}
}
