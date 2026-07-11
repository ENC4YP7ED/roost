package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"roost/internal/dbviewer/session"
)

// The viewer's HTTP surface is exercised against a live MySQL/MariaDB when
// ROOST_TEST_MYSQL is set (see the db package integration tests for how CI
// brings up the container).

func mysqlEnv(t *testing.T) (host string, port int, pw string) {
	addr := os.Getenv("ROOST_TEST_MYSQL")
	if addr == "" {
		t.Skip("set ROOST_TEST_MYSQL=host:port to run database-viewer API tests")
	}
	h, p, ok := strings.Cut(addr, ":")
	if !ok {
		t.Fatalf("ROOST_TEST_MYSQL=%q must be host:port", addr)
	}
	port = 0
	for _, r := range p {
		port = port*10 + int(r-'0')
	}
	pw = os.Getenv("ROOST_TEST_MYSQL_PW")
	if pw == "" {
		pw = "testpw"
	}
	return h, port, pw
}

type viewerHarness struct {
	t     *testing.T
	h     http.Handler
	token string
}

func newViewerHarness(t *testing.T) *viewerHarness {
	t.Helper()
	sessions := session.NewStore(time.Hour)
	t.Cleanup(sessions.Close)
	api := New(sessions, Config{})
	return &viewerHarness{t: t, h: api}
}

func (v *viewerHarness) req(method, path string, body any) *httptest.ResponseRecorder {
	v.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:5000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}
	rec := httptest.NewRecorder()
	v.h.ServeHTTP(rec, req)
	return rec
}

func (v *viewerHarness) json(rec *httptest.ResponseRecorder) map[string]any {
	v.t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		v.t.Fatalf("response not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

func TestViewerAPIRequiresAuth(t *testing.T) {
	// No live DB needed: unauthenticated requests are refused before any query.
	sessions := session.NewStore(time.Hour)
	defer sessions.Close()
	h := New(sessions, Config{})

	for _, path := range []string{"/session", "/databases", "/server/info"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, rec.Code)
		}
	}
}

func TestViewerConnectRejectsBadCredentials(t *testing.T) {
	host, port, _ := mysqlEnv(t)
	v := newViewerHarness(t)
	rec := v.req("POST", "/connect", map[string]any{
		"host": host, "port": port, "user": "root", "password": "definitely-wrong"})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("bad-credential connect = %d, want 502", rec.Code)
	}
}

func TestViewerFullHTTPFlow(t *testing.T) {
	host, port, pw := mysqlEnv(t)
	v := newViewerHarness(t)

	// Connect.
	rec := v.req("POST", "/connect", map[string]any{"host": host, "port": port, "user": "root", "password": pw})
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d: %s", rec.Code, rec.Body.String())
	}
	v.token = v.json(rec)["token"].(string)
	if v.token == "" {
		t.Fatal("no session token issued")
	}

	// Session lookup and server info.
	if rec := v.req("GET", "/session", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /session = %d", rec.Code)
	}
	if rec := v.req("GET", "/server/info", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /server/info = %d", rec.Code)
	}

	// Create a schema through the query endpoint, then introspect it.
	const schema = "roost_api_test"
	v.req("POST", "/query", map[string]any{"sql": "DROP DATABASE IF EXISTS " + schema})
	if rec := v.req("POST", "/databases", map[string]any{"name": schema}); rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create database = %d: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		v.req("POST", "/query", map[string]any{"sql": "DROP DATABASE IF EXISTS " + schema})
	})

	// Build a table + rows via the import endpoint.
	importScript := "CREATE TABLE items (id INT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(50));" +
		"INSERT INTO items (label) VALUES ('one'),('two');"
	rec = v.reqRaw("POST", "/import?database="+schema, importScript)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body.String())
	}

	// Databases / tables / columns / indexes.
	if rec := v.req("GET", "/databases", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /databases = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/tables", nil); rec.Code != http.StatusOK {
		t.Errorf("GET tables = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/tables/items/columns", nil); rec.Code != http.StatusOK {
		t.Errorf("GET columns = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/tables/items/indexes", nil); rec.Code != http.StatusOK {
		t.Errorf("GET indexes = %d", rec.Code)
	}

	// Browse + DDL + export.
	if rec := v.req("GET", "/databases/"+schema+"/tables/items/rows?limit=10", nil); rec.Code != http.StatusOK {
		t.Errorf("browse rows = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/tables/items/ddl", nil); rec.Code != http.StatusOK {
		t.Errorf("ddl = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/tables/items/export?format=csv", nil); rec.Code != http.StatusOK {
		t.Errorf("table export = %d", rec.Code)
	}
	if rec := v.req("GET", "/databases/"+schema+"/export?format=sql", nil); rec.Code != http.StatusOK {
		t.Errorf("database export = %d", rec.Code)
	}

	// Row insert / update / delete through the API.
	ins := v.req("POST", "/databases/"+schema+"/tables/items/insert", map[string]any{
		"values": map[string]any{"label": "three"}})
	if ins.Code != http.StatusOK {
		t.Errorf("insert row = %d: %s", ins.Code, ins.Body.String())
	}
	upd := v.req("POST", "/databases/"+schema+"/tables/items/update", map[string]any{
		"values": map[string]any{"label": "ONE"}, "where": map[string]any{"label": "one"}})
	if upd.Code != http.StatusOK {
		t.Errorf("update row = %d: %s", upd.Code, upd.Body.String())
	}
	del := v.req("POST", "/databases/"+schema+"/tables/items/delete", map[string]any{
		"where": map[string]any{"label": "two"}})
	if del.Code != http.StatusOK {
		t.Errorf("delete row = %d: %s", del.Code, del.Body.String())
	}

	// Search.
	search := v.req("POST", "/databases/"+schema+"/tables/items/search", map[string]any{
		"conditions": []map[string]any{{"column": "label", "op": "LIKE", "value": "%O%"}},
		"limit":      10,
	})
	if search.Code != http.StatusOK {
		t.Errorf("search = %d: %s", search.Code, search.Body.String())
	}

	// Users listing.
	if rec := v.req("GET", "/users", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /users = %d: %s", rec.Code, rec.Body.String())
	}

	// A read query.
	q := v.req("POST", "/query", map[string]any{"sql": "SELECT COUNT(*) FROM " + schema + ".items"})
	if q.Code != http.StatusOK {
		t.Errorf("query = %d: %s", q.Code, q.Body.String())
	}

	// Disconnect ends the session.
	if rec := v.req("POST", "/disconnect", nil); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Errorf("disconnect = %d", rec.Code)
	}
	if rec := v.req("GET", "/session", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("session after disconnect = %d, want 401", rec.Code)
	}
}

// reqRaw posts a raw (non-JSON) body — used by the import endpoint.
func (v *viewerHarness) reqRaw(method, path, body string) *httptest.ResponseRecorder {
	v.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5000"
	if v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}
	rec := httptest.NewRecorder()
	v.h.ServeHTTP(rec, req)
	return rec
}

// TestViewerRemainingEndpoints covers foreign-keys, user management, and the
// drop paths, plus a few negative branches.
func TestViewerRemainingEndpoints(t *testing.T) {
	host, port, pw := mysqlEnv(t)
	v := newViewerHarness(t)

	// Bad request bodies are rejected before touching the DB.
	if rec := v.reqRaw("POST", "/connect", "{not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed connect body = %d, want 400", rec.Code)
	}

	rec := v.req("POST", "/connect", map[string]any{"host": host, "port": port, "user": "root", "password": pw})
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d: %s", rec.Code, rec.Body.String())
	}
	v.token = v.json(rec)["token"].(string)

	const schema = "roost_api_test2"
	v.req("POST", "/query", map[string]any{"sql": "DROP DATABASE IF EXISTS " + schema})
	v.req("POST", "/databases", map[string]any{"name": schema})
	t.Cleanup(func() { v.req("POST", "/query", map[string]any{"sql": "DROP DATABASE IF EXISTS " + schema}) })

	v.reqRaw("POST", "/import?database="+schema,
		"CREATE TABLE parent (id INT PRIMARY KEY);"+
			"CREATE TABLE child (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES parent(id));")

	// Foreign keys.
	if rec := v.req("GET", "/databases/"+schema+"/tables/child/foreign-keys", nil); rec.Code != http.StatusOK {
		t.Errorf("foreign-keys = %d: %s", rec.Code, rec.Body.String())
	}

	// User lifecycle: create → grants → drop.
	const user = "roost_api_user"
	const uhostRaw = "%"      // MySQL host wildcard
	const uhostURL = "%25"    // %-encoded for the URL path
	v.req("DELETE", "/users/"+user+"/"+uhostURL, nil) // clean slate
	create := v.req("POST", "/users", map[string]any{
		"user": user, "host": uhostRaw, "password": "P@ssw0rd-strong!", "privileges": "SELECT", "scope": "*.*"})
	if create.Code != http.StatusOK && create.Code != http.StatusCreated {
		t.Errorf("create user = %d: %s", create.Code, create.Body.String())
	}
	if rec := v.req("GET", "/users/"+user+"/"+uhostURL+"/grants", nil); rec.Code != http.StatusOK {
		t.Errorf("user grants = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := v.req("DELETE", "/users/"+user+"/"+uhostURL, nil); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Errorf("drop user = %d: %s", rec.Code, rec.Body.String())
	}

	// Drop a table, then the database.
	if rec := v.req("DELETE", "/databases/"+schema+"/tables/child", nil); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Errorf("drop table = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := v.req("DELETE", "/databases/"+schema, nil); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Errorf("drop database = %d: %s", rec.Code, rec.Body.String())
	}

	// A query error (bad SQL) surfaces as a 4xx/5xx, not a panic.
	if rec := v.req("POST", "/query", map[string]any{"sql": "SELECT * FROM nonexistent_table_xyz"}); rec.Code < 400 {
		t.Errorf("bad query = %d, want an error status", rec.Code)
	}
}
