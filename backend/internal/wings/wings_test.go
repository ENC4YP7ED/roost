package wings

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"roost/internal/store"
)

// fakeDaemon stands in for wings, recording what the panel sent.
type fakeDaemon struct {
	*httptest.Server
	lastMethod string
	lastPath   string
	lastAuth   string
	lastBody   map[string]any
	status     int
	response   string
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{status: http.StatusOK, response: "{}"}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.lastMethod, d.lastPath = r.Method, r.URL.Path
		d.lastAuth = r.Header.Get("Authorization")
		d.lastBody = nil
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			json.Unmarshal(raw, &d.lastBody)
		}
		w.WriteHeader(d.status)
		io.WriteString(w, d.response)
	}))
	t.Cleanup(d.Close)
	return d
}

// nodeFor points a store.Node at the fake daemon.
func nodeFor(t *testing.T, d *fakeDaemon) *store.Node {
	t.Helper()
	u, err := url.Parse(d.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	if _, err := fmtSscan(u.Port(), &port); err != nil {
		t.Fatal(err)
	}
	return &store.Node{
		UUID: "node-uuid", Name: "n", FQDN: u.Hostname(), Scheme: "http",
		DaemonTokenID: "tid", DaemonToken: "secret", DaemonListen: port, DaemonSFTP: 2022,
	}
}

func fmtSscan(s string, out *int) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotANumber
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return 1, nil
}

var errNotANumber = errString("port is not a number")

type errString string

func (e errString) Error() string { return string(e) }

func TestBaseURLAndAuthorization(t *testing.T) {
	d := newFakeDaemon(t)
	node := nodeFor(t, d)
	c := New(node)

	if got := c.BaseURL(); got != d.URL {
		t.Errorf("BaseURL = %q, want %q", got, d.URL)
	}
	if err := c.Do("GET", "/api/system", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if d.lastAuth != "Bearer tid.secret" {
		t.Errorf("Authorization = %q, want the token_id.token form", d.lastAuth)
	}
}

func TestDoDecodesResponse(t *testing.T) {
	d := newFakeDaemon(t)
	d.response = `{"state":"running","utilization":{"memory_bytes":123}}`
	c := New(nodeFor(t, d))

	var out struct {
		State       string `json:"state"`
		Utilization struct {
			Memory int64 `json:"memory_bytes"`
		} `json:"utilization"`
	}
	if err := c.Do("GET", "/api/servers/x", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.State != "running" || out.Utilization.Memory != 123 {
		t.Errorf("decoded = %+v", out)
	}
}

func TestDoSurfacesDaemonErrors(t *testing.T) {
	d := newFakeDaemon(t)
	d.status = http.StatusConflict
	d.response = `{"error":"server is already running"}`
	c := New(nodeFor(t, d))

	err := c.Do("POST", "/api/servers/x/power", map[string]string{"action": "start"}, nil)
	if err == nil {
		t.Fatal("a 409 response did not produce an error")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want the status and daemon body", err)
	}
}

func TestDoReportsUnreachableDaemon(t *testing.T) {
	node := &store.Node{FQDN: "127.0.0.1", Scheme: "http", DaemonListen: 1, DaemonTokenID: "t", DaemonToken: "s"}
	err := New(node).Do("GET", "/api/system", nil, nil)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if !strings.Contains(err.Error(), "wings unreachable") {
		t.Errorf("error = %q, want it to say the daemon is unreachable", err)
	}
}

func TestPowerAndCommandPayloads(t *testing.T) {
	d := newFakeDaemon(t)
	c := New(nodeFor(t, d))

	if err := c.SendPower("srv-uuid", "restart"); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/srv-uuid/power" || d.lastMethod != "POST" {
		t.Errorf("power hit %s %s", d.lastMethod, d.lastPath)
	}
	if d.lastBody["action"] != "restart" {
		t.Errorf("power body = %v", d.lastBody)
	}

	if err := c.SendCommands("srv-uuid", []string{"say hi", "stop"}); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/srv-uuid/commands" {
		t.Errorf("commands hit %s", d.lastPath)
	}
	cmds := d.lastBody["commands"].([]any)
	if len(cmds) != 2 || cmds[0] != "say hi" {
		t.Errorf("commands body = %v", d.lastBody)
	}
}

func TestServerLifecycleCalls(t *testing.T) {
	d := newFakeDaemon(t)
	c := New(nodeFor(t, d))

	if err := c.CreateServer("u1", true); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers" || d.lastBody["uuid"] != "u1" || d.lastBody["start_on_completion"] != true {
		t.Errorf("create = %s %v", d.lastPath, d.lastBody)
	}

	if err := c.DeleteServer("u1"); err != nil {
		t.Fatal(err)
	}
	if d.lastMethod != "DELETE" || d.lastPath != "/api/servers/u1" {
		t.Errorf("delete = %s %s", d.lastMethod, d.lastPath)
	}

	if err := c.Reinstall("u1"); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/u1/reinstall" {
		t.Errorf("reinstall = %s", d.lastPath)
	}

	if err := c.Sync("u1"); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/u1/sync" {
		t.Errorf("sync = %s", d.lastPath)
	}

	if err := c.Backup("u1", map[string]any{"adapter": "wings", "uuid": "b1"}); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/u1/backup" || d.lastBody["uuid"] != "b1" {
		t.Errorf("backup = %s %v", d.lastPath, d.lastBody)
	}

	if err := c.DeleteBackup("u1", "b1"); err != nil {
		t.Fatal(err)
	}
	if d.lastMethod != "DELETE" || d.lastPath != "/api/servers/u1/backup/b1" {
		t.Errorf("delete backup = %s %s", d.lastMethod, d.lastPath)
	}

	if err := c.RestoreBackup("u1", "b1", map[string]any{"truncate_directory": true}); err != nil {
		t.Fatal(err)
	}
	if d.lastPath != "/api/servers/u1/backup/b1/restore" {
		t.Errorf("restore = %s", d.lastPath)
	}
}

func TestRawPassesThroughBodyAndContentType(t *testing.T) {
	d := newFakeDaemon(t)
	c := New(nodeFor(t, d))

	res, err := c.Raw("POST", "/api/servers/u1/files/write", strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if d.lastPath != "/api/servers/u1/files/write" {
		t.Errorf("path = %s", d.lastPath)
	}
	if d.lastAuth != "Bearer tid.secret" {
		t.Errorf("Raw did not authenticate: %q", d.lastAuth)
	}
}

// ---- signed URLs handed to the browser ----

func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func testNode() *store.Node {
	return &store.Node{FQDN: "node.example.com", Scheme: "https", DaemonListen: 8080,
		DaemonToken: "daemon-secret", DaemonTokenID: "tid"}
}

func TestWebsocketToken(t *testing.T) {
	node := testNode()
	srv := &store.Server{UUID: "srv-uuid"}
	user := &store.User{ID: 7, UUID: "user-uuid"}

	token, socket, err := WebsocketToken("https://panel", node, srv, user, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	if socket != "wss://node.example.com:8080/api/servers/srv-uuid/ws" {
		t.Errorf("socket = %q", socket)
	}
	claims := decodeClaims(t, token)
	if claims["server_uuid"] != "srv-uuid" || claims["user_uuid"] != "user-uuid" {
		t.Errorf("claims = %v", claims)
	}
	if claims["user_id"] != float64(7) {
		t.Errorf("user_id = %v", claims["user_id"])
	}
	if claims["iss"] != "https://panel" {
		t.Errorf("iss = %v", claims["iss"])
	}
	aud := claims["aud"].([]any)
	if aud[0] != "https://node.example.com:8080" {
		t.Errorf("aud = %v", aud)
	}
	if perms := claims["permissions"].([]any); perms[0] != "*" {
		t.Errorf("permissions = %v", perms)
	}
}

func TestWebsocketSchemeFollowsNode(t *testing.T) {
	node := testNode()
	node.Scheme = "http"
	_, socket, err := WebsocketToken("https://panel", node, &store.Server{UUID: "s"}, &store.User{UUID: "u"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(socket, "ws://") {
		t.Errorf("socket = %q, want a ws:// url for an http node", socket)
	}
}

func TestSignedDownloadAndUploadURLs(t *testing.T) {
	node := testNode()
	srv := &store.Server{UUID: "srv-uuid"}
	user := &store.User{UUID: "user-uuid"}

	t.Run("file download", func(t *testing.T) {
		raw, err := FileDownloadURL("https://panel", node, srv, user, "/logs/latest.log")
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != "node.example.com:8080" || u.Path != "/download/file" {
			t.Errorf("url = %q", raw)
		}
		claims := decodeClaims(t, u.Query().Get("token"))
		if claims["file_path"] != "/logs/latest.log" || claims["server_uuid"] != "srv-uuid" {
			t.Errorf("claims = %v", claims)
		}
		if claims["unique_id"] == nil {
			t.Error("no unique_id: the link would be replayable")
		}
	})

	t.Run("backup download", func(t *testing.T) {
		raw, err := BackupDownloadURL("https://panel", node, srv, user, "backup-uuid")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(raw)
		if u.Path != "/download/backup" {
			t.Errorf("path = %q", u.Path)
		}
		claims := decodeClaims(t, u.Query().Get("token"))
		if claims["backup_uuid"] != "backup-uuid" {
			t.Errorf("claims = %v", claims)
		}
	})

	t.Run("upload", func(t *testing.T) {
		raw, err := UploadURL("https://panel", node, srv, user)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(raw)
		if u.Path != "/upload/file" {
			t.Errorf("path = %q", u.Path)
		}
		claims := decodeClaims(t, u.Query().Get("token"))
		if claims["server_uuid"] != "srv-uuid" {
			t.Errorf("claims = %v", claims)
		}
	})

	t.Run("tokens with special characters are escaped", func(t *testing.T) {
		raw, err := FileDownloadURL("https://panel", node, srv, user, "/a b/c&d.txt")
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url is not parseable: %v", err)
		}
		claims := decodeClaims(t, u.Query().Get("token"))
		if claims["file_path"] != "/a b/c&d.txt" {
			t.Errorf("file_path = %v", claims["file_path"])
		}
	})
}

func TestSignedURLsDifferPerRequest(t *testing.T) {
	node := testNode()
	srv := &store.Server{UUID: "s"}
	user := &store.User{UUID: "u"}
	a, _ := UploadURL("https://panel", node, srv, user)
	b, _ := UploadURL("https://panel", node, srv, user)
	if a == b {
		t.Error("signed URLs are identical across calls; jti/unique_id is not random")
	}
}

func TestTruncateLongDaemonErrors(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := truncate(long, 300)
	if len(got) > 305 {
		t.Errorf("truncate returned %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated string is not marked with an ellipsis")
	}
	if got := truncate("short", 300); got != "short" {
		t.Errorf("short string was modified: %q", got)
	}
}
