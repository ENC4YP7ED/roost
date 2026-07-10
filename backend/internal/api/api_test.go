package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
)

// The login throttle is process-wide; the suite would otherwise trip it.
func TestMain(m *testing.M) {
	loginLimiter = newRateLimiter(100000, time.Minute)
	os.Exit(m.Run())
}

// ---- harness ----

type harness struct {
	t   *testing.T
	api *API
	h   http.Handler
	st  *store.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(st)
	return &harness{t: t, api: a, st: st, h: a.WrapExternal(a.Mux())}
}

type response struct {
	*httptest.ResponseRecorder
	t *testing.T
}

func (r *response) json() map[string]any {
	r.t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		r.t.Fatalf("response is not a JSON object: %v\nbody: %s", err, r.Body.String())
	}
	return out
}

// detail extracts the message from an {"errors":[...]} envelope.
func (r *response) detail() string {
	r.t.Helper()
	var out struct {
		Errors []struct {
			Detail string `json:"detail"`
			Status string `json:"status"`
			Code   string `json:"code"`
		} `json:"errors"`
	}
	json.Unmarshal(r.Body.Bytes(), &out)
	if len(out.Errors) == 0 {
		r.t.Fatalf("body is not an error envelope: %s", r.Body.String())
	}
	return out.Errors[0].Detail
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }
func withBearer(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (h *harness) do(method, path string, body any, opts ...reqOpt) *response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "192.0.2.10:1234"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return &response{ResponseRecorder: rec, t: h.t}
}

func (h *harness) mkUser(username, email, password string, admin bool) *store.User {
	h.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.t.Fatal(err)
	}
	u := &store.User{UUID: auth.UUID(), Username: username, Email: email, Password: hash,
		Language: "en", RootAdmin: admin}
	if err := h.st.CreateUser(u); err != nil {
		h.t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// login performs a real password login and returns the session cookie.
func (h *harness) login(user, password string) *http.Cookie {
	h.t.Helper()
	res := h.do("POST", "/auth/login", map[string]string{"user": user, "password": password})
	if res.Code != http.StatusOK {
		h.t.Fatalf("login failed: %d %s", res.Code, res.Body.String())
	}
	for _, c := range res.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	h.t.Fatal("no session cookie issued")
	return nil
}

// apiKey inserts a key of the given type and returns the bearer token.
func (h *harness) apiKey(userID int64, keyType int) string {
	h.t.Helper()
	prefix := "ptlc_"
	if keyType == store.KeyTypeApplication {
		prefix = "ptla_"
	}
	identifier := prefix + auth.RandomAlnum(11) // 16 chars
	secret := auth.RandomAlnum(32)
	k := &store.APIKey{UserID: userID, KeyType: keyType, Identifier: identifier,
		TokenHash: auth.SHA256Hex(secret), AllowedIPs: "[]"}
	if err := h.st.CreateAPIKey(k); err != nil {
		h.t.Fatal(err)
	}
	return identifier + secret
}

type fixture struct {
	owner  *store.User
	admin  *store.User
	node   *store.Node
	alloc  *store.Allocation
	egg    *store.Egg
	server *store.Server
	jarVar *store.EggVariable
}

// seedFixture builds a full owner/node/egg/server graph via the store.
func (h *harness) seedFixture() fixture {
	h.t.Helper()
	admin := h.mkUser("admin", "admin@example.com", "adminpass1", true)
	owner := h.mkUser("owner", "owner@example.com", "ownerpass1", false)

	loc := &store.Location{Short: "local"}
	if err := h.st.CreateLocation(loc); err != nil {
		h.t.Fatal(err)
	}
	node := &store.Node{UUID: auth.UUID(), Name: "node01", LocationID: loc.ID, FQDN: "127.0.0.1",
		Scheme: "http", Memory: 8192, Disk: 51200, DaemonTokenID: "tid1234567890123",
		DaemonToken: "daemon-secret", DaemonListen: 18099, DaemonSFTP: 2022, DaemonBase: "/var/lib"}
	if err := h.st.CreateNode(node); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.st.CreateAllocations(node.ID, "127.0.0.1", nil, []int{25565, 25566}); err != nil {
		h.t.Fatal(err)
	}
	alloc, _ := h.st.FreeAllocation(node.ID)

	nest := &store.Nest{UUID: auth.UUID(), Name: "Minecraft"}
	h.st.CreateNest(nest)
	egg := &store.Egg{UUID: auth.UUID(), NestID: nest.ID, Name: "Paper",
		Features: `["eula"]`, DockerImages: `{"Java 21":"ghcr.io/x:21","Java 17":"ghcr.io/x:17"}`,
		FileDenylist:  "[]",
		ConfigFiles:   `{"server.properties":{"parser":"properties","find":{"server-port":"{{server.build.default.port}}"}}}`,
		ConfigStartup: `{"done":")! For help,"}`, ConfigLogs: "{}", ConfigStop: "stop",
		Startup: "java -jar {{SERVER_JARFILE}}", ScriptContainer: "alpine", ScriptEntry: "ash",
		ScriptInstall: "echo install"}
	if err := h.st.CreateEgg(egg); err != nil {
		h.t.Fatal(err)
	}
	jar := &store.EggVariable{EggID: egg.ID, Name: "Jar File", EnvVariable: "SERVER_JARFILE",
		DefaultValue: "server.jar", UserViewable: true, UserEditable: true}
	h.st.CreateEggVariable(jar)
	hidden := &store.EggVariable{EggID: egg.ID, Name: "Secret", EnvVariable: "HIDDEN",
		DefaultValue: "s3cr3t", UserViewable: false, UserEditable: false}
	h.st.CreateEggVariable(hidden)

	srv := &store.Server{UUID: auth.UUID(), UUIDShort: "abc12345", NodeID: node.ID, Name: "SMP",
		OwnerID: owner.ID, Memory: 2048, Disk: 10240, IO: 500, CPU: 200, OOMDisabled: true,
		NestID: nest.ID, EggID: egg.ID, Startup: egg.Startup, Image: "ghcr.io/x:21",
		AllocationID: &alloc.ID, DatabaseLimit: 2, AllocationLimit: 2, BackupLimit: 2}
	if err := h.st.CreateServer(srv); err != nil {
		h.t.Fatal(err)
	}
	alloc.ServerID = &srv.ID
	h.st.UpdateAllocation(alloc)
	h.st.SetServerVariable(srv.ID, jar.ID, "paper.jar")

	return fixture{owner: owner, admin: admin, node: node, alloc: alloc, egg: egg, server: srv, jarVar: jar}
}

// ============================================================ auth

func TestLoginSuccessIssuesSessionAndLogsActivity(t *testing.T) {
	h := newHarness(t)
	u := h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)

	res := h.do("POST", "/auth/login", map[string]string{"user": "admin@example.com", "password": "hunter2hunter2"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	data := res.json()["data"].(map[string]any)
	if data["complete"] != true {
		t.Error("login not marked complete for a user without 2FA")
	}
	if user := data["user"].(map[string]any); user["email"] != "admin@example.com" || user["admin"] != true {
		t.Errorf("unexpected user payload: %v", user)
	}

	var cookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie is not SameSite=Lax")
	}
	// The raw cookie value must never be what is stored.
	if _, err := h.st.SessionUser(cookie.Value); err == nil {
		t.Error("session token stored unhashed")
	}
	if _, err := h.st.SessionUser(auth.SHA256Hex(cookie.Value)); err != nil {
		t.Errorf("session not stored as a hash: %v", err)
	}

	logs, _ := h.st.ActivityForActor(u.ID, 10)
	if len(logs) != 1 || logs[0].Event != "auth:success" {
		t.Errorf("expected an auth:success activity entry, got %v", logs)
	}
}

func TestLoginByUsernameOrEmail(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)
	for _, ident := range []string{"admin", "admin@example.com", "ADMIN@EXAMPLE.COM"} {
		res := h.do("POST", "/auth/login", map[string]string{"user": ident, "password": "hunter2hunter2"})
		if res.Code != http.StatusOK {
			t.Errorf("login with %q failed: %d", ident, res.Code)
		}
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)

	cases := []struct{ name, user, pass string }{
		{"wrong password", "admin@example.com", "nope"},
		{"unknown user", "ghost@example.com", "hunter2hunter2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do("POST", "/auth/login", map[string]string{"user": tc.user, "password": tc.pass})
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.Code)
			}
			// The same message for both, so accounts cannot be enumerated.
			if got := res.detail(); got != "These credentials do not match our records." {
				t.Errorf("detail = %q", got)
			}
			if len(res.Result().Cookies()) != 0 {
				t.Error("a cookie was issued for a failed login")
			}
		})
	}
}

func TestLoginValidatesBody(t *testing.T) {
	h := newHarness(t)
	res := h.do("POST", "/auth/login", map[string]string{"user": "", "password": ""})
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", res.Code)
	}
}

func TestTwoFactorCheckpointFlow(t *testing.T) {
	h := newHarness(t)
	u := h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)
	secret := auth.NewTOTPSecret()
	u.UseTOTP = true
	u.TOTPSecret = &secret
	if err := h.st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	h.st.ReplaceRecoveryTokens(u.ID, []string{auth.SHA256Hex("RECOVER1")})

	res := h.do("POST", "/auth/login", map[string]string{"user": "admin", "password": "hunter2hunter2"})
	if res.Code != http.StatusOK {
		t.Fatalf("password step failed: %d", res.Code)
	}
	data := res.json()["data"].(map[string]any)
	if data["complete"] != false {
		t.Fatal("login completed without the second factor")
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("session issued before the second factor")
	}
	token := data["confirmation_token"].(string)

	// A wrong code must not authenticate.
	bad := h.do("POST", "/auth/login/checkpoint", map[string]string{
		"confirmation_token": token, "authentication_code": "000000"})
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("wrong TOTP code accepted: %d", bad.Code)
	}

	// An unknown confirmation token must not authenticate.
	unknown := h.do("POST", "/auth/login/checkpoint", map[string]string{
		"confirmation_token": "bogus", "recovery_token": "RECOVER1"})
	if unknown.Code != http.StatusUnauthorized {
		t.Errorf("unknown confirmation token accepted: %d", unknown.Code)
	}

	// The recovery token completes the login...
	ok := h.do("POST", "/auth/login/checkpoint", map[string]string{
		"confirmation_token": token, "recovery_token": "RECOVER1"})
	if ok.Code != http.StatusOK {
		t.Fatalf("recovery token rejected: %d %s", ok.Code, ok.Body.String())
	}
	if len(ok.Result().Cookies()) == 0 {
		t.Error("no session issued after a successful checkpoint")
	}

	// ...exactly once, and the confirmation token is consumed with it.
	replay := h.do("POST", "/auth/login/checkpoint", map[string]string{
		"confirmation_token": token, "recovery_token": "RECOVER1"})
	if replay.Code == http.StatusOK {
		t.Error("confirmation token replayable")
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)
	cookie := h.login("admin", "hunter2hunter2")

	if res := h.do("GET", "/api/client/account", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Fatalf("account before logout: %d", res.Code)
	}
	if res := h.do("POST", "/auth/logout", nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", res.Code)
	}
	if res := h.do("GET", "/api/client/account", nil, withCookie(cookie)); res.Code != http.StatusUnauthorized {
		t.Error("session still valid after logout")
	}
}

func TestForgotPasswordDoesNotLeakAccountExistence(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)

	known := h.do("POST", "/auth/password", map[string]string{"email": "admin@example.com"})
	unknown := h.do("POST", "/auth/password", map[string]string{"email": "ghost@example.com"})
	if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Error("forgot-password responses differ for known and unknown accounts")
	}
}

func TestLoginRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("request %d blocked below the limit", i+1)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Error("limit not enforced")
	}
	if !rl.allow("5.6.7.8") {
		t.Error("limiter is not per-IP")
	}

	// A limiter with an elapsed window admits again.
	old := newRateLimiter(1, time.Nanosecond)
	old.allow("1.2.3.4")
	time.Sleep(time.Millisecond)
	if !old.allow("1.2.3.4") {
		t.Error("window never expires")
	}
}

func TestThrottleReturns429(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	handler := throttle(rl, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func() int {
		req := httptest.NewRequest("POST", "/auth/login", nil)
		req.RemoteAddr = "192.0.2.1:1"
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}
	if got := call(); got != http.StatusOK {
		t.Fatalf("first call = %d", got)
	}
	if got := call(); got != http.StatusTooManyRequests {
		t.Errorf("second call = %d, want 429", got)
	}
}

// ============================================================ authn/authz middleware

func TestUnauthenticatedRoutesAreRejected(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	paths := []struct{ method, path string }{
		{"GET", "/api/client"},
		{"GET", "/api/client/account"},
		{"GET", "/api/client/servers/abc12345"},
		{"GET", "/api/application/users"},
		{"GET", "/api/application/nodes"},
		{"GET", "/api/remote/servers"},
		{"GET", "/api/application/captcha"},
		{"GET", "/api/application/tls"},
	}
	for _, p := range paths {
		res := h.do(p.method, p.path, nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", p.method, p.path, res.Code)
		}
	}
}

func TestNonAdminCannotReachApplicationAPI(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("GET", "/api/application/users", nil, withCookie(cookie))
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	if !strings.Contains(res.detail(), "administrative") {
		t.Errorf("detail = %q", res.detail())
	}
	_ = f
}

func TestAPIKeyScopesAreEnforced(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	clientKey := h.apiKey(f.admin.ID, store.KeyTypeAccount)
	appKey := h.apiKey(f.admin.ID, store.KeyTypeApplication)

	if res := h.do("GET", "/api/client/account", nil, withBearer(clientKey)); res.Code != http.StatusOK {
		t.Errorf("client key on client API = %d, want 200", res.Code)
	}
	if res := h.do("GET", "/api/application/users", nil, withBearer(appKey)); res.Code != http.StatusOK {
		t.Errorf("application key on application API = %d, want 200", res.Code)
	}
	// An application key must not authenticate a client-API request.
	if res := h.do("GET", "/api/client/account", nil, withBearer(appKey)); res.Code != http.StatusUnauthorized {
		t.Errorf("application key accepted on the client API: %d", res.Code)
	}
	// Garbage and truncated tokens.
	for _, tok := range []string{"nonsense", clientKey[:10], clientKey[:16] + "wrongsecretwrongsecretwrongsecre"} {
		if res := h.do("GET", "/api/client/account", nil, withBearer(tok)); res.Code != http.StatusUnauthorized {
			t.Errorf("token %q accepted: %d", tok, res.Code)
		}
	}
}

func TestAPIKeyLastUsedIsRecorded(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	tok := h.apiKey(f.admin.ID, store.KeyTypeAccount)
	h.do("GET", "/api/client/account", nil, withBearer(tok))

	keys, _ := h.st.APIKeysForUser(f.admin.ID, store.KeyTypeAccount)
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Error("last_used_at not stamped on key use")
	}
}

// ============================================================ client API

func TestClientServerListScoping(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	stranger := h.mkUser("stranger", "stranger@example.com", "strangerpass", false)

	ownerCookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client", nil, withCookie(ownerCookie))
	if res.Code != http.StatusOK {
		t.Fatalf("owner list: %d", res.Code)
	}
	body := res.json()
	if body["object"] != "list" {
		t.Errorf("envelope object = %v", body["object"])
	}
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("owner sees %d servers, want 1", len(data))
	}
	attrs := data[0].(map[string]any)["attributes"].(map[string]any)
	if attrs["identifier"] != "abc12345" || attrs["server_owner"] != true {
		t.Errorf("unexpected attributes: %v", attrs)
	}
	// Hidden egg variables must never reach the client.
	rel := attrs["relationships"].(map[string]any)["variables"].(map[string]any)["data"].([]any)
	for _, v := range rel {
		env := v.(map[string]any)["attributes"].(map[string]any)["env_variable"]
		if env == "HIDDEN" {
			t.Error("a non-user-viewable egg variable leaked to the client API")
		}
	}

	strangerCookie := h.login("stranger", "strangerpass")
	res = h.do("GET", "/api/client", nil, withCookie(strangerCookie))
	if got := len(res.json()["data"].([]any)); got != 0 {
		t.Errorf("stranger sees %d servers, want 0", got)
	}
	_ = stranger
	_ = f
}

func TestClientServerAccessControl(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.mkUser("stranger", "stranger@example.com", "strangerpass", false)

	// Stranger: 404 (not 403) so server existence is not disclosed.
	strangerCookie := h.login("stranger", "strangerpass")
	if res := h.do("GET", "/api/client/servers/abc12345", nil, withCookie(strangerCookie)); res.Code != http.StatusNotFound {
		t.Errorf("stranger got %d, want 404", res.Code)
	}
	// Owner: full access, meta lists the wildcard permission.
	ownerCookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client/servers/abc12345", nil, withCookie(ownerCookie))
	if res.Code != http.StatusOK {
		t.Fatalf("owner got %d", res.Code)
	}
	meta := res.json()["meta"].(map[string]any)
	if meta["is_server_owner"] != true {
		t.Error("is_server_owner not set for the owner")
	}
	perms := meta["user_permissions"].([]any)
	if len(perms) == 0 || perms[0] != "*" {
		t.Errorf("owner permissions = %v, want [*]", perms)
	}
	// Admin: access to a server they do not own.
	adminCookie := h.login("admin", "adminpass1")
	if res := h.do("GET", "/api/client/servers/abc12345", nil, withCookie(adminCookie)); res.Code != http.StatusOK {
		t.Errorf("admin got %d, want 200", res.Code)
	}
	// Server addressable by full UUID as well.
	if res := h.do("GET", "/api/client/servers/"+f.server.UUID, nil, withCookie(ownerCookie)); res.Code != http.StatusOK {
		t.Errorf("lookup by full uuid got %d", res.Code)
	}
}

func TestSubuserPermissionsGateEndpoints(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	guest := h.mkUser("guest", "guest@example.com", "guestpass1", false)
	h.st.CreateSubuser(&store.Subuser{UserID: guest.ID, ServerID: f.server.ID,
		Permissions: `["websocket.connect"]`})
	cookie := h.login("guest", "guestpass1")

	// Viewing the server is allowed (no explicit permission required)...
	if res := h.do("GET", "/api/client/servers/abc12345", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Fatalf("subuser cannot view the server: %d", res.Code)
	}
	// ...but backup.read is not granted.
	res := h.do("GET", "/api/client/servers/abc12345/backups", nil, withCookie(cookie))
	if res.Code != http.StatusForbidden {
		t.Fatalf("backups without permission = %d, want 403", res.Code)
	}
	if !strings.Contains(res.detail(), "backup.read") {
		t.Errorf("error does not name the missing permission: %q", res.detail())
	}

	// Grant it and retry.
	sub, _ := h.st.Subuser(f.server.ID, guest.ID)
	sub.Permissions = `["websocket.connect","backup.read"]`
	h.st.UpdateSubuser(sub)
	if res := h.do("GET", "/api/client/servers/abc12345/backups", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("backups with permission = %d, want 200", res.Code)
	}
}

func TestClientPermissionsEndpoint(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client/permissions", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	attrs := res.json()["attributes"].(map[string]any)
	perms := attrs["permissions"].([]any)
	if len(perms) != len(AllPermissions) {
		t.Errorf("returned %d permissions, want %d", len(perms), len(AllPermissions))
	}
}

func TestAccountEndpoints(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	t.Run("update email requires the current password", func(t *testing.T) {
		res := h.do("PUT", "/api/client/account/email", map[string]string{
			"email": "new@example.com", "password": "wrong"}, withCookie(cookie))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", res.Code)
		}
		if u, _ := h.st.UserByEmail("owner@example.com"); u == nil {
			t.Error("email changed despite a wrong password")
		}
	})

	t.Run("update email succeeds", func(t *testing.T) {
		res := h.do("PUT", "/api/client/account/email", map[string]string{
			"email": "new@example.com", "password": "ownerpass1"}, withCookie(cookie))
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}
		if _, err := h.st.UserByEmail("new@example.com"); err != nil {
			t.Error("email not updated")
		}
	})

	t.Run("password change enforces length and current password", func(t *testing.T) {
		short := h.do("PUT", "/api/client/account/password", map[string]string{
			"current_password": "ownerpass1", "password": "short"}, withCookie(cookie))
		if short.Code != http.StatusUnprocessableEntity {
			t.Errorf("short password = %d, want 422", short.Code)
		}
		wrong := h.do("PUT", "/api/client/account/password", map[string]string{
			"current_password": "nope", "password": "longenoughpassword"}, withCookie(cookie))
		if wrong.Code != http.StatusBadRequest {
			t.Errorf("wrong current password = %d, want 400", wrong.Code)
		}
		ok := h.do("PUT", "/api/client/account/password", map[string]string{
			"current_password": "ownerpass1", "password": "longenoughpassword"}, withCookie(cookie))
		if ok.Code != http.StatusNoContent {
			t.Fatalf("password change = %d", ok.Code)
		}
		u, _ := h.st.UserByID(1)
		_ = u
		owner, _ := h.st.UserByUsername("owner")
		if !auth.CheckPassword(owner.Password, "longenoughpassword") {
			t.Error("password not actually changed")
		}
	})
}

func TestAccountAPIKeyCreateRevealsSecretOnce(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/account/api-keys", map[string]any{
		"description": "ci", "allowed_ips": []string{}}, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.Code, res.Body.String())
	}
	meta := res.json()["meta"].(map[string]any)
	secret := meta["secret_token"].(string)
	if !strings.HasPrefix(secret, "ptlc_") || len(secret) != 48 {
		t.Errorf("secret token %q has an unexpected shape", secret)
	}
	// The token works, and only its hash is stored.
	if r := h.do("GET", "/api/client/account", nil, withBearer(secret)); r.Code != http.StatusOK {
		t.Errorf("issued key does not authenticate: %d", r.Code)
	}
	keys, _ := h.st.APIKeysForUser(2, store.KeyTypeAccount)
	for _, k := range keys {
		if strings.Contains(secret, k.TokenHash) {
			t.Error("token stored in plaintext")
		}
	}

	// Listing never returns the secret.
	list := h.do("GET", "/api/client/account/api-keys", nil, withCookie(cookie))
	if strings.Contains(list.Body.String(), secret[16:]) {
		t.Error("secret token exposed by the listing endpoint")
	}
}

func TestSSHKeyValidationAndFingerprint(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	bad := h.do("POST", "/api/client/account/ssh-keys", map[string]string{
		"name": "k", "public_key": "not a key"}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid key = %d, want 422", bad.Code)
	}

	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEsomethingsomethingsomethingXYZ user@host"
	ok := h.do("POST", "/api/client/account/ssh-keys", map[string]string{
		"name": "laptop", "public_key": key}, withCookie(cookie))
	if ok.Code != http.StatusOK {
		t.Fatalf("valid key rejected: %d %s", ok.Code, ok.Body.String())
	}
	attrs := ok.json()["attributes"].(map[string]any)
	fp := attrs["fingerprint"].(string)
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("fingerprint %q is not in SHA256 form", fp)
	}
}

func TestServerResourcesDegradesWhenWingsIsDown(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Nothing listens on the node's daemon port; the endpoint must still
	// answer so the dashboard stays usable.
	res := h.do("GET", "/api/client/servers/abc12345/resources", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an offline payload", res.Code)
	}
	attrs := res.json()["attributes"].(map[string]any)
	if attrs["current_state"] != "offline" {
		t.Errorf("current_state = %v, want offline", attrs["current_state"])
	}
}

func TestPowerAndCommandFailCleanlyWhenWingsIsDown(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/servers/abc12345/power", map[string]string{"signal": "start"}, withCookie(cookie))
	if res.Code != http.StatusBadGateway {
		t.Errorf("power with wings down = %d, want 502", res.Code)
	}
	bad := h.do("POST", "/api/client/servers/abc12345/power", map[string]string{"signal": "explode"}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid signal = %d, want 422", bad.Code)
	}
	empty := h.do("POST", "/api/client/servers/abc12345/command", map[string]string{"command": ""}, withCookie(cookie))
	if empty.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty command = %d, want 422", empty.Code)
	}
}

func TestWebsocketCredentialsAreSignedForTheNode(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("GET", "/api/client/servers/abc12345/websocket", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	data := res.json()["data"].(map[string]any)
	socket := data["socket"].(string)
	if !strings.HasPrefix(socket, "ws://127.0.0.1:18099/api/servers/") || !strings.HasSuffix(socket, "/ws") {
		t.Errorf("socket url = %q", socket)
	}
	token := data["token"].(string)
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token is not a JWT: %q", token)
	}
	// Claims must bind the token to this server and user.
	payload, err := b64urlDecode(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	json.Unmarshal(payload, &claims)
	if claims["server_uuid"] != f.server.UUID {
		t.Errorf("server_uuid = %v", claims["server_uuid"])
	}
	if claims["user_uuid"] != f.owner.UUID {
		t.Errorf("user_uuid = %v", claims["user_uuid"])
	}
	if perms, ok := claims["permissions"].([]any); !ok || len(perms) == 0 {
		t.Error("no permissions embedded in the websocket token")
	}
}

func TestStartupEndpointHidesInvisibleVariables(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("GET", "/api/client/servers/abc12345/startup", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	body := res.json()
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("returned %d variables, want 1 (the hidden one must be filtered)", len(data))
	}
	attrs := data[0].(map[string]any)["attributes"].(map[string]any)
	if attrs["server_value"] != "paper.jar" {
		t.Errorf("server_value = %v", attrs["server_value"])
	}
	meta := body["meta"].(map[string]any)
	// The rendered command substitutes the stored value.
	if got := meta["startup_command"].(string); !strings.Contains(got, "paper.jar") {
		t.Errorf("startup_command = %q, want the substituted jar", got)
	}
	if got := meta["raw_startup_command"].(string); !strings.Contains(got, "{{SERVER_JARFILE}}") {
		t.Errorf("raw_startup_command = %q, want the placeholder", got)
	}
}

func TestStartupVariableUpdateRespectsEditability(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	ok := h.do("PUT", "/api/client/servers/abc12345/startup/variable",
		map[string]string{"key": "SERVER_JARFILE", "value": "custom.jar"}, withCookie(cookie))
	if ok.Code != http.StatusOK {
		t.Fatalf("editable variable = %d: %s", ok.Code, ok.Body.String())
	}
	// The non-viewable/non-editable variable must be refused.
	bad := h.do("PUT", "/api/client/servers/abc12345/startup/variable",
		map[string]string{"key": "HIDDEN", "value": "pwned"}, withCookie(cookie))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("hidden variable = %d, want 400", bad.Code)
	}
	missing := h.do("PUT", "/api/client/servers/abc12345/startup/variable",
		map[string]string{"key": "NOPE", "value": "x"}, withCookie(cookie))
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown variable = %d, want 404", missing.Code)
	}
}

func TestDockerImageMustBeOfferedByTheEgg(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	bad := h.do("PUT", "/api/client/servers/abc12345/settings/docker-image",
		map[string]string{"docker_image": "evil/backdoor:latest"}, withCookie(cookie))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("arbitrary image = %d, want 400", bad.Code)
	}
	ok := h.do("PUT", "/api/client/servers/abc12345/settings/docker-image",
		map[string]string{"docker_image": "ghcr.io/x:17"}, withCookie(cookie))
	if ok.Code != http.StatusNoContent {
		t.Errorf("allowed image = %d, want 204", ok.Code)
	}
	srv, _ := h.st.ServerByIdentifier("abc12345")
	if srv.Image != "ghcr.io/x:17" {
		t.Errorf("image = %q", srv.Image)
	}
}

func TestFeatureLimitsAreEnforced(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Backups: limit is 2.
	for i := 0; i < 2; i++ {
		h.st.CreateBackup(&store.Backup{ServerID: f.server.ID, UUID: auth.UUID(),
			Name: fmt.Sprintf("b%d", i), IgnoredFiles: "[]", Disk: "wings"})
	}
	res := h.do("POST", "/api/client/servers/abc12345/backups", map[string]string{"name": "third"}, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("backup over limit = %d, want 400", res.Code)
	}

	// Databases: no host configured yet.
	dbRes := h.do("POST", "/api/client/servers/abc12345/databases",
		map[string]string{"database": "test", "remote": "%"}, withCookie(cookie))
	if dbRes.Code != http.StatusBadRequest {
		t.Errorf("database without a host = %d, want 400", dbRes.Code)
	}
}

func TestLockedBackupCannotBeDeleted(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	b := &store.Backup{ServerID: f.server.ID, UUID: auth.UUID(), Name: "keep",
		IgnoredFiles: "[]", Disk: "wings", IsLocked: true}
	h.st.CreateBackup(b)

	res := h.do("DELETE", "/api/client/servers/abc12345/backups/"+b.UUID, nil, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("locked backup delete = %d, want 400", res.Code)
	}
}

func TestPrimaryAllocationCannotBeRemoved(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	path := fmt.Sprintf("/api/client/servers/abc12345/network/allocations/%d", f.alloc.ID)
	if res := h.do("DELETE", path, nil, withCookie(cookie)); res.Code != http.StatusBadRequest {
		t.Errorf("deleting the primary allocation = %d, want 400", res.Code)
	}
}

func TestSubuserCannotBeTheOwner(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/servers/abc12345/users",
		map[string]any{"email": "owner@example.com", "permissions": []string{"control.console"}}, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("adding the owner as a subuser = %d, want 400", res.Code)
	}
}

func TestSubuserPermissionsAreSanitised(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/servers/abc12345/users", map[string]any{
		"email":       "invitee@example.com",
		"permissions": []string{"control.console", "not.a.real.permission", "control.console"},
	}, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("invite = %d: %s", res.Code, res.Body.String())
	}
	perms := res.json()["attributes"].(map[string]any)["permissions"].([]any)
	got := map[string]bool{}
	for _, p := range perms {
		got[p.(string)] = true
	}
	if got["not.a.real.permission"] {
		t.Error("an unknown permission was stored")
	}
	if !got["websocket.connect"] {
		t.Error("websocket.connect was not implicitly granted")
	}
	if len(perms) != 2 {
		t.Errorf("permissions = %v, want deduplicated [control.console websocket.connect]", perms)
	}
}

// ============================================================ schedules

func TestScheduleCrudAndCronValidation(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	bad := h.do("POST", "/api/client/servers/abc12345/schedules", map[string]any{
		"name": "bad", "minute": "*/0"}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid cron = %d, want 422", bad.Code)
	}

	res := h.do("POST", "/api/client/servers/abc12345/schedules", map[string]any{
		"name": "nightly", "minute": "0", "hour": "3", "day_of_month": "*", "month": "*", "day_of_week": "*",
	}, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("create schedule = %d: %s", res.Code, res.Body.String())
	}
	attrs := res.json()["attributes"].(map[string]any)
	if attrs["next_run_at"] == nil {
		t.Error("next_run_at not computed")
	}
	id := int64(attrs["id"].(float64))

	task := h.do("POST", fmt.Sprintf("/api/client/servers/abc12345/schedules/%d/tasks", id),
		map[string]any{"action": "command", "payload": "say hi"}, withCookie(cookie))
	if task.Code != http.StatusOK {
		t.Fatalf("create task = %d: %s", task.Code, task.Body.String())
	}
	badTask := h.do("POST", fmt.Sprintf("/api/client/servers/abc12345/schedules/%d/tasks", id),
		map[string]any{"action": "launch-missiles", "payload": "x"}, withCookie(cookie))
	if badTask.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid task action = %d, want 422", badTask.Code)
	}
	emptyPayload := h.do("POST", fmt.Sprintf("/api/client/servers/abc12345/schedules/%d/tasks", id),
		map[string]any{"action": "command", "payload": ""}, withCookie(cookie))
	if emptyPayload.Code != http.StatusUnprocessableEntity {
		t.Errorf("command task without payload = %d, want 422", emptyPayload.Code)
	}

	del := h.do("DELETE", fmt.Sprintf("/api/client/servers/abc12345/schedules/%d", id), nil, withCookie(cookie))
	if del.Code != http.StatusNoContent {
		t.Errorf("delete schedule = %d", del.Code)
	}
}

func TestScheduleOfAnotherServerIsNotReachable(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	// A schedule attached to a different server id.
	other := &store.Server{UUID: auth.UUID(), UUIDShort: "zzz99999", NodeID: f.node.ID, Name: "other",
		OwnerID: f.admin.ID, Memory: 1, Disk: 1, NestID: f.server.NestID, EggID: f.egg.ID,
		Startup: "x", Image: "i"}
	h.st.CreateServer(other)
	sc := &store.Schedule{ServerID: other.ID, Name: "theirs", IsActive: true}
	h.st.CreateSchedule(sc)

	cookie := h.login("owner", "ownerpass1")
	res := h.do("GET", fmt.Sprintf("/api/client/servers/abc12345/schedules/%d", sc.ID), nil, withCookie(cookie))
	if res.Code != http.StatusNotFound {
		t.Errorf("cross-server schedule access = %d, want 404", res.Code)
	}
}

// ============================================================ cron engine

func TestParseCronField(t *testing.T) {
	cases := []struct {
		expr     string
		min, max int
		want     []int
		invalid  bool
	}{
		{expr: "*", min: 0, max: 3, want: []int{0, 1, 2, 3}},
		{expr: "", min: 0, max: 2, want: []int{0, 1, 2}},
		{expr: "5", min: 0, max: 59, want: []int{5}},
		{expr: "1-3", min: 0, max: 59, want: []int{1, 2, 3}},
		{expr: "1,3,5", min: 0, max: 59, want: []int{1, 3, 5}},
		{expr: "*/15", min: 0, max: 59, want: []int{0, 15, 30, 45}},
		{expr: "10-20/5", min: 0, max: 59, want: []int{10, 15, 20}},
		{expr: "0,30", min: 0, max: 59, want: []int{0, 30}},
		{expr: "60", min: 0, max: 59, invalid: true},
		{expr: "-1", min: 0, max: 59, invalid: true},
		{expr: "5-1", min: 0, max: 59, invalid: true},
		{expr: "*/0", min: 0, max: 59, invalid: true},
		{expr: "abc", min: 0, max: 59, invalid: true},
		{expr: "1-", min: 0, max: 59, invalid: true},
	}
	for _, tc := range cases {
		got, ok := parseCronField(tc.expr, tc.min, tc.max)
		if tc.invalid {
			if ok {
				t.Errorf("parseCronField(%q) accepted an invalid expression", tc.expr)
			}
			continue
		}
		if !ok {
			t.Errorf("parseCronField(%q) rejected a valid expression", tc.expr)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("parseCronField(%q) = %v values, want %v", tc.expr, len(got), tc.want)
			continue
		}
		for _, v := range tc.want {
			if !got[v] {
				t.Errorf("parseCronField(%q) missing %d", tc.expr, v)
			}
		}
	}
}

func TestNextCronRun(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)

	t.Run("every five minutes", func(t *testing.T) {
		sc := &store.Schedule{CronMinute: "*/5", CronHour: "*", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*"}
		got, ok := nextCronRun(sc, base)
		if !ok {
			t.Fatal("no next run")
		}
		if want := base.Add(5 * time.Minute); !got.Equal(want) {
			t.Errorf("next = %v, want %v", got, want)
		}
	})

	t.Run("daily at 03:00 rolls to tomorrow", func(t *testing.T) {
		sc := &store.Schedule{CronMinute: "0", CronHour: "3", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*"}
		got, ok := nextCronRun(sc, base)
		if !ok {
			t.Fatal("no next run")
		}
		want := time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("next = %v, want %v", got, want)
		}
	})

	t.Run("weekday constraint", func(t *testing.T) {
		// 2026-03-01 is a Sunday; ask for Monday (1) at 00:00.
		sc := &store.Schedule{CronMinute: "0", CronHour: "0", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "1"}
		got, ok := nextCronRun(sc, base)
		if !ok {
			t.Fatal("no next run")
		}
		if got.Weekday() != time.Monday {
			t.Errorf("next run falls on %v, want Monday", got.Weekday())
		}
	})

	t.Run("never matches", func(t *testing.T) {
		// 30 February.
		sc := &store.Schedule{CronMinute: "0", CronHour: "0", CronDayOfMonth: "30", CronMonth: "2", CronDayOfWeek: "*"}
		if _, ok := nextCronRun(sc, base); ok {
			t.Error("found a next run for 30 February")
		}
	})

	t.Run("invalid expression", func(t *testing.T) {
		sc := &store.Schedule{CronMinute: "nope", CronHour: "*", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*"}
		if _, ok := nextCronRun(sc, base); ok {
			t.Error("invalid cron accepted")
		}
	})

	t.Run("result is always in the future", func(t *testing.T) {
		sc := &store.Schedule{CronMinute: "*", CronHour: "*", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*"}
		got, _ := nextCronRun(sc, base)
		if !got.After(base) {
			t.Errorf("next = %v is not after %v", got, base)
		}
	})
}

// ============================================================ application API

func TestApplicationUserCrud(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	create := h.do("POST", "/api/application/users", map[string]any{
		"email": "new@example.com", "username": "newbie", "first_name": "New", "last_name": "Bie",
	}, withCookie(cookie))
	if create.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", create.Code, create.Body.String())
	}
	attrs := create.json()["attributes"].(map[string]any)
	id := int64(attrs["id"].(float64))
	if attrs["root_admin"] != false {
		t.Error("new user unexpectedly an admin")
	}
	// The response must never carry the password hash.
	if strings.Contains(create.Body.String(), "password") {
		t.Error("user payload mentions password")
	}

	dup := h.do("POST", "/api/application/users", map[string]any{
		"email": "new@example.com", "username": "other"}, withCookie(cookie))
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate email = %d, want 409", dup.Code)
	}

	missing := h.do("POST", "/api/application/users", map[string]any{"email": "x@y.z"}, withCookie(cookie))
	if missing.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing username = %d, want 422", missing.Code)
	}

	upd := h.do("PATCH", fmt.Sprintf("/api/application/users/%d", id),
		map[string]any{"first_name": "Renamed"}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update = %d", upd.Code)
	}
	if upd.json()["attributes"].(map[string]any)["first_name"] != "Renamed" {
		t.Error("update not applied")
	}

	del := h.do("DELETE", fmt.Sprintf("/api/application/users/%d", id), nil, withCookie(cookie))
	if del.Code != http.StatusNoContent {
		t.Errorf("delete = %d", del.Code)
	}
	if res := h.do("GET", fmt.Sprintf("/api/application/users/%d", id), nil, withCookie(cookie)); res.Code != http.StatusNotFound {
		t.Errorf("deleted user still readable: %d", res.Code)
	}
}

func TestAdminCannotDemoteThemselves(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	res := h.do("PATCH", fmt.Sprintf("/api/application/users/%d", f.admin.ID),
		map[string]any{"root_admin": false}, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("self-demotion = %d, want 400", res.Code)
	}
}

func TestApplicationDeleteUserWithServersIsRejected(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	res := h.do("DELETE", fmt.Sprintf("/api/application/users/%d", f.owner.ID), nil, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("deleting a server owner = %d, want 400", res.Code)
	}
}

func TestNodeAllocationPortParsing(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	path := fmt.Sprintf("/api/application/nodes/%d/allocations", f.node.ID)

	ok := h.do("POST", path, map[string]any{"ip": "10.0.0.1", "ports": []string{"27015", "27020-27022"}}, withCookie(cookie))
	if ok.Code != http.StatusNoContent {
		t.Fatalf("valid ports = %d: %s", ok.Code, ok.Body.String())
	}
	list := h.do("GET", path, nil, withCookie(cookie))
	if got := len(list.json()["data"].([]any)); got != 2+4 { // fixture 2 + 1 + 3
		t.Errorf("node has %d allocations, want 6", got)
	}

	for _, bad := range [][]string{{"notaport"}, {"5-1"}, {"1-99999"}} {
		res := h.do("POST", path, map[string]any{"ip": "10.0.0.2", "ports": bad}, withCookie(cookie))
		if res.Code != http.StatusUnprocessableEntity {
			t.Errorf("ports %v = %d, want 422", bad, res.Code)
		}
	}
}

func TestApplicationServerCreateAssignsAllocationAndDefaults(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	free, err := h.st.FreeAllocation(f.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	res := h.do("POST", "/api/application/servers", map[string]any{
		"name": "Second", "user": f.owner.ID, "egg": f.egg.ID,
		"allocation":     map[string]any{"default": free.ID},
		"limits":         map[string]any{"memory": 1024, "disk": 2048, "io": 500, "cpu": 100},
		"feature_limits": map[string]any{"databases": 1, "allocations": 1, "backups": 1},
		"environment":    map[string]string{"SERVER_JARFILE": "custom.jar"},
	}, withCookie(cookie))
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", res.Code, res.Body.String())
	}
	attrs := res.json()["attributes"].(map[string]any)
	if attrs["status"] != "installing" {
		t.Errorf("status = %v, want installing", attrs["status"])
	}
	container := attrs["container"].(map[string]any)
	if container["image"] == "" {
		t.Error("docker image not defaulted from the egg")
	}
	env := container["environment"].(map[string]any)
	if env["SERVER_JARFILE"] != "custom.jar" {
		t.Errorf("environment override lost: %v", env["SERVER_JARFILE"])
	}

	// The allocation is now taken.
	updated, _ := h.st.AllocationByID(free.ID)
	if updated.ServerID == nil {
		t.Error("allocation not assigned to the new server")
	}
	// Re-using it must fail.
	again := h.do("POST", "/api/application/servers", map[string]any{
		"name": "Third", "user": f.owner.ID, "egg": f.egg.ID,
		"allocation": map[string]any{"default": free.ID},
	}, withCookie(cookie))
	if again.Code != http.StatusUnprocessableEntity {
		t.Errorf("reusing an assigned allocation = %d, want 422", again.Code)
	}
}

func TestApplicationServerCreateValidation(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"user": f.owner.ID, "egg": f.egg.ID}},
		{"unknown owner", map[string]any{"name": "x", "user": 9999, "egg": f.egg.ID, "allocation": map[string]any{"default": 2}}},
		{"unknown egg", map[string]any{"name": "x", "user": f.owner.ID, "egg": 9999, "allocation": map[string]any{"default": 2}}},
		{"unknown allocation", map[string]any{"name": "x", "user": f.owner.ID, "egg": f.egg.ID, "allocation": map[string]any{"default": 9999}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do("POST", "/api/application/servers", tc.body, withCookie(cookie))
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422 (%s)", res.Code, res.Body.String())
			}
		})
	}
}

func TestSuspendAndUnsuspend(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	if res := h.do("POST", fmt.Sprintf("/api/application/servers/%d/suspend", f.server.ID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Fatalf("suspend = %d", res.Code)
	}
	srv, _ := h.st.ServerByID(f.server.ID)
	if srv.Status == nil || *srv.Status != "suspended" {
		t.Fatal("server not marked suspended")
	}
	// The client API reflects suspension.
	ownerCookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client/servers/abc12345", nil, withCookie(ownerCookie))
	if res.json()["attributes"].(map[string]any)["is_suspended"] != true {
		t.Error("client API does not report suspension")
	}

	if res := h.do("POST", fmt.Sprintf("/api/application/servers/%d/unsuspend", f.server.ID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Fatalf("unsuspend = %d", res.Code)
	}
	srv, _ = h.st.ServerByID(f.server.ID)
	if srv.Status != nil {
		t.Errorf("status = %v after unsuspend, want nil", *srv.Status)
	}
}

func TestExternalServerLookupRoute(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	ext := "billing-1234"
	f.server.ExternalID = &ext
	h.st.UpdateServer(f.server)
	cookie := h.login("admin", "adminpass1")

	res := h.do("GET", "/api/application/servers/external/billing-1234", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("external lookup = %d: %s", res.Code, res.Body.String())
	}
	if res.json()["attributes"].(map[string]any)["identifier"] != "abc12345" {
		t.Error("wrong server returned")
	}
	// The sibling route must still work (this pairing used to collide).
	if res := h.do("GET", fmt.Sprintf("/api/application/servers/%d/databases", f.server.ID), nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("/servers/{id}/databases = %d, want 200", res.Code)
	}
	if res := h.do("GET", "/api/application/servers/external/nope", nil, withCookie(cookie)); res.Code != http.StatusNotFound {
		t.Errorf("unknown external id = %d, want 404", res.Code)
	}
}

func TestNestAndEggListing(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	nests := h.do("GET", "/api/application/nests", nil, withCookie(cookie))
	if nests.Code != http.StatusOK {
		t.Fatal(nests.Code)
	}
	data := nests.json()["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("nests = %d", len(data))
	}
	if got := data[0].(map[string]any)["attributes"].(map[string]any)["eggs_count"]; got != float64(1) {
		t.Errorf("eggs_count = %v, want 1", got)
	}

	eggs := h.do("GET", fmt.Sprintf("/api/application/nests/%d/eggs", f.server.NestID), nil, withCookie(cookie))
	eggAttrs := eggs.json()["data"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	if eggAttrs["docker_image"] == "" {
		t.Error("docker_image not derived from docker_images")
	}
	vars := eggAttrs["relationships"].(map[string]any)["variables"].(map[string]any)["data"].([]any)
	if len(vars) != 2 {
		t.Errorf("egg exposes %d variables to admins, want 2 (including hidden)", len(vars))
	}
}

func TestEggExportRoundTrip(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	exp := h.do("GET", fmt.Sprintf("/api/application/nests/%d/eggs/%d/export", f.server.NestID, f.egg.ID), nil, withCookie(cookie))
	if exp.Code != http.StatusOK {
		t.Fatalf("export = %d", exp.Code)
	}
	if cd := exp.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	var doc map[string]any
	if err := json.Unmarshal(exp.Body.Bytes(), &doc); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if doc["meta"].(map[string]any)["version"] != "PTDL_v2" {
		t.Error("export is not PTDL_v2")
	}
	if len(doc["variables"].([]any)) != 2 {
		t.Error("variables missing from the export")
	}

	// Re-import it into the same nest.
	imp := h.do("POST", fmt.Sprintf("/api/application/nests/%d/eggs/import", f.server.NestID), doc, withCookie(cookie))
	if imp.Code != http.StatusCreated {
		t.Fatalf("re-import = %d: %s", imp.Code, imp.Body.String())
	}
	eggs, _ := h.st.EggsForNest(f.server.NestID)
	if len(eggs) != 2 {
		t.Errorf("nest has %d eggs after re-import, want 2", len(eggs))
	}
}

func TestSettingsEndpointOnlyAcceptsKnownKeys(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	res := h.do("PATCH", "/api/application/settings", map[string]string{
		"app:name": "MyPanel", "app:url": "https://panel.example.com/", "evil:key": "x",
	}, withCookie(cookie))
	if res.Code != http.StatusNoContent {
		t.Fatalf("patch = %d", res.Code)
	}
	if got := h.st.Setting("app:name", ""); got != "MyPanel" {
		t.Errorf("app:name = %q", got)
	}
	if got := h.st.Setting("app:url", ""); got != "https://panel.example.com" {
		t.Errorf("trailing slash not stripped: %q", got)
	}
	if got := h.st.Setting("evil:key", "unset"); got != "unset" {
		t.Error("an unknown settings key was written")
	}
}

// ============================================================ remote (wings) API

func nodeToken(f fixture) string { return f.node.DaemonTokenID + "." + f.node.DaemonToken }

func TestRemoteRequiresNodeToken(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()

	for _, tok := range []string{"", "garbage", "tid1234567890123.wrong", "wrong.daemon-secret"} {
		res := h.do("GET", "/api/remote/servers", nil, withBearer(tok))
		if res.Code != http.StatusUnauthorized {
			t.Errorf("token %q = %d, want 401", tok, res.Code)
		}
	}
	if res := h.do("GET", "/api/remote/servers", nil, withBearer(nodeToken(f))); res.Code != http.StatusOK {
		t.Errorf("valid node token = %d, want 200", res.Code)
	}
	// A panel session must not authenticate the remote API.
	cookie := h.login("admin", "adminpass1")
	if res := h.do("GET", "/api/remote/servers", nil, withCookie(cookie)); res.Code != http.StatusUnauthorized {
		t.Errorf("admin cookie accepted on the remote API: %d", res.Code)
	}
}

func TestRemoteServerConfiguration(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()

	res := h.do("GET", "/api/remote/servers/"+f.server.UUID, nil, withBearer(nodeToken(f)))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	body := res.json()
	settings := body["settings"].(map[string]any)

	if settings["uuid"] != f.server.UUID {
		t.Errorf("uuid = %v", settings["uuid"])
	}
	build := settings["build"].(map[string]any)
	if build["memory_limit"] != float64(2048) || build["cpu_limit"] != float64(200) {
		t.Errorf("build limits = %v", build)
	}
	if build["oom_disabled"] != true {
		t.Error("oom_disabled not propagated")
	}
	def := settings["allocations"].(map[string]any)["default"].(map[string]any)
	if def["ip"] != "127.0.0.1" || def["port"] != float64(25565) {
		t.Errorf("default allocation = %v", def)
	}
	env := settings["environment"].(map[string]any)
	if env["SERVER_JARFILE"] != "paper.jar" {
		t.Errorf("SERVER_JARFILE = %v, want the per-server override", env["SERVER_JARFILE"])
	}
	if env["SERVER_PORT"] != "25565" || env["SERVER_IP"] != "127.0.0.1" {
		t.Errorf("SERVER_PORT/IP not injected: %v", env)
	}
	if env["P_SERVER_UUID"] != f.server.UUID {
		t.Error("P_SERVER_UUID missing")
	}
	if env["SERVER_MEMORY"] != "2048" {
		t.Errorf("SERVER_MEMORY = %v", env["SERVER_MEMORY"])
	}
	if settings["suspended"] != false {
		t.Error("suspended flag wrong")
	}

	// process_configuration: placeholder substitution and stop command.
	proc := body["process_configuration"].(map[string]any)
	stop := proc["stop"].(map[string]any)
	if stop["type"] != "command" || stop["value"] != "stop" {
		t.Errorf("stop = %v", stop)
	}
	done := proc["startup"].(map[string]any)["done"].([]any)
	if len(done) != 1 || done[0] != ")! For help," {
		t.Errorf("startup.done = %v", done)
	}
	configs := proc["configs"].([]any)
	if len(configs) != 1 {
		t.Fatalf("configs = %d, want 1", len(configs))
	}
	cfg := configs[0].(map[string]any)
	if cfg["file"] != "server.properties" || cfg["parser"] != "properties" {
		t.Errorf("config file entry = %v", cfg)
	}
	replace := cfg["replace"].([]any)[0].(map[string]any)
	if replace["match"] != "server-port" || replace["replace_with"] != "25565" {
		t.Errorf("{{server.build.default.port}} not substituted: %v", replace)
	}
}

func TestRemoteSignalStopCommand(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	f.egg.ConfigStop = "^C"
	h.st.UpdateEgg(f.egg)

	res := h.do("GET", "/api/remote/servers/"+f.server.UUID, nil, withBearer(nodeToken(f)))
	stop := res.json()["process_configuration"].(map[string]any)["stop"].(map[string]any)
	if stop["type"] != "signal" || stop["value"] != "C" {
		t.Errorf("stop = %v, want a SIGINT-style signal", stop)
	}
}

func TestRemoteServerListPagination(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	res := h.do("GET", "/api/remote/servers?per_page=1&page=1", nil, withBearer(nodeToken(f)))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	body := res.json()
	meta := body["meta"].(map[string]any)
	if meta["total"] != float64(1) || meta["per_page"] != float64(1) {
		t.Errorf("meta = %v", meta)
	}
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %d rows", len(data))
	}
	entry := data[0].(map[string]any)
	for _, k := range []string{"uuid", "settings", "process_configuration"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("remote list entry missing %q", k)
		}
	}
}

func TestRemoteServerScopedToNode(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	// Another node must not be able to read this server.
	other := &store.Node{UUID: auth.UUID(), Name: "other", LocationID: 1, FQDN: "10.0.0.9",
		Scheme: "http", DaemonTokenID: "othertokenid1234", DaemonToken: "other-secret",
		DaemonListen: 8080, DaemonSFTP: 2022}
	h.st.CreateNode(other)

	res := h.do("GET", "/api/remote/servers/"+f.server.UUID, nil,
		withBearer(other.DaemonTokenID+"."+other.DaemonToken))
	if res.Code != http.StatusNotFound {
		t.Errorf("cross-node server read = %d, want 404", res.Code)
	}
}

func TestRemoteInstallLifecycle(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	tok := nodeToken(f)

	script := h.do("GET", "/api/remote/servers/"+f.server.UUID+"/install", nil, withBearer(tok))
	if script.Code != http.StatusOK {
		t.Fatal(script.Code)
	}
	body := script.json()
	if body["container_image"] != "alpine" || body["entrypoint"] != "ash" {
		t.Errorf("install container = %v/%v", body["container_image"], body["entrypoint"])
	}
	if body["script"] != "echo install" {
		t.Errorf("script = %v", body["script"])
	}

	// Success clears the status and stamps installed_at.
	status := "installing"
	f.server.Status = &status
	h.st.UpdateServer(f.server)
	ok := h.do("POST", "/api/remote/servers/"+f.server.UUID+"/install",
		map[string]any{"successful": true}, withBearer(tok))
	if ok.Code != http.StatusNoContent {
		t.Fatalf("install callback = %d", ok.Code)
	}
	srv, _ := h.st.ServerByUUID(f.server.UUID)
	if srv.Status != nil {
		t.Errorf("status = %v after a successful install", *srv.Status)
	}
	if srv.InstalledAt == nil {
		t.Error("installed_at not stamped")
	}

	// Failure marks install_failed.
	fail := h.do("POST", "/api/remote/servers/"+f.server.UUID+"/install",
		map[string]any{"successful": false}, withBearer(tok))
	if fail.Code != http.StatusNoContent {
		t.Fatal(fail.Code)
	}
	srv, _ = h.st.ServerByUUID(f.server.UUID)
	if srv.Status == nil || *srv.Status != "install_failed" {
		t.Error("failed install not recorded")
	}
}

func TestRemoteSFTPAuth(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	tok := nodeToken(f)

	t.Run("owner with a correct password", func(t *testing.T) {
		res := h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "owner.abc12345", "password": "ownerpass1"}, withBearer(tok))
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}
		body := res.json()
		if body["server"] != f.server.UUID || body["user"] != f.owner.UUID {
			t.Errorf("payload = %v", body)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		res := h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "owner.abc12345", "password": "nope"}, withBearer(tok))
		if res.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.Code)
		}
	})

	t.Run("malformed username", func(t *testing.T) {
		res := h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "owner", "password": "ownerpass1"}, withBearer(tok))
		if res.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.Code)
		}
	})

	t.Run("subuser without file.sftp is refused", func(t *testing.T) {
		guest := h.mkUser("guest", "guest@example.com", "guestpass1", false)
		h.st.CreateSubuser(&store.Subuser{UserID: guest.ID, ServerID: f.server.ID,
			Permissions: `["control.console"]`})
		res := h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "guest.abc12345", "password": "guestpass1"}, withBearer(tok))
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", res.Code)
		}

		// Granting file.sftp lets them in.
		sub, _ := h.st.Subuser(f.server.ID, guest.ID)
		sub.Permissions = `["control.console","file.sftp"]`
		h.st.UpdateSubuser(sub)
		res = h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "guest.abc12345", "password": "guestpass1"}, withBearer(tok))
		if res.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", res.Code)
		}
	})

	t.Run("unknown server", func(t *testing.T) {
		res := h.do("POST", "/api/remote/sftp/auth", map[string]string{
			"type": "password", "username": "owner.zzzzzzzz", "password": "ownerpass1"}, withBearer(tok))
		if res.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.Code)
		}
	})
}

func TestRemoteActivityIngest(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	res := h.do("POST", "/api/remote/activity", map[string]any{
		"data": []map[string]any{
			{"server": f.server.UUID, "event": "server:power.start", "ip": "9.9.9.9",
				"timestamp": time.Now().UTC().Format(time.RFC3339), "user": f.owner.UUID,
				"metadata": map[string]any{"signal": "start"}},
			{"server": "unknown-uuid", "event": "ignored", "ip": "1.1.1.1",
				"timestamp": time.Now().UTC().Format(time.RFC3339)},
		},
	}, withBearer(nodeToken(f)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}

	logs, _ := h.st.ActivityForSubject("server", f.server.ID, 10)
	if len(logs) != 1 {
		t.Fatalf("stored %d activity rows, want 1 (the unknown server must be skipped)", len(logs))
	}
	if logs[0].Event != "server:power.start" || logs[0].IP != "9.9.9.9" {
		t.Errorf("row = %+v", logs[0])
	}
	if logs[0].ActorID == nil || *logs[0].ActorID != f.owner.ID {
		t.Error("actor not resolved from the user uuid")
	}
}

func TestRemoteBackupStatusCallback(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	b := &store.Backup{ServerID: f.server.ID, UUID: auth.UUID(), Name: "b", IgnoredFiles: "[]", Disk: "wings"}
	h.st.CreateBackup(b)

	res := h.do("POST", "/api/remote/backups/"+b.UUID, map[string]any{
		"successful": true, "checksum": "abc123", "checksum_type": "sha1", "size": 4096,
	}, withBearer(nodeToken(f)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d", res.Code)
	}
	got, _ := h.st.BackupByUUID(b.UUID)
	if !got.IsSuccessful || got.Bytes != 4096 || got.CompletedAt == nil {
		t.Errorf("backup not finalised: %+v", got)
	}
	if got.Checksum == nil || *got.Checksum != "sha1:abc123" {
		t.Errorf("checksum = %v", got.Checksum)
	}
	if res := h.do("POST", "/api/remote/backups/unknown", map[string]any{}, withBearer(nodeToken(f))); res.Code != http.StatusNotFound {
		t.Errorf("unknown backup = %d, want 404", res.Code)
	}
}

// ============================================================ envelopes

func TestErrorEnvelopeShape(t *testing.T) {
	h := newHarness(t)
	res := h.do("GET", "/api/client/account", nil)
	if res.Code != http.StatusUnauthorized {
		t.Fatal(res.Code)
	}
	var out struct {
		Errors []struct {
			Code   string `json:"code"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("not an error envelope: %v", err)
	}
	if len(out.Errors) != 1 || out.Errors[0].Status != "401" || out.Errors[0].Code != "Unauthorized" {
		t.Errorf("envelope = %+v", out.Errors)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestListPagination(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	for i := 0; i < 7; i++ {
		h.mkUser(fmt.Sprintf("u%d", i), fmt.Sprintf("u%d@example.com", i), "password12", false)
	}

	res := h.do("GET", "/api/application/users?per_page=3&page=2", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	body := res.json()
	if len(body["data"].([]any)) != 3 {
		t.Errorf("page has %d rows, want 3", len(body["data"].([]any)))
	}
	p := body["meta"].(map[string]any)["pagination"].(map[string]any)
	if p["total"] != float64(9) || p["current_page"] != float64(2) || p["total_pages"] != float64(3) {
		t.Errorf("pagination = %v", p)
	}

	// Out-of-range pages return an empty list, not an error.
	far := h.do("GET", "/api/application/users?per_page=3&page=99", nil, withCookie(cookie))
	if far.Code != http.StatusOK || len(far.json()["data"].([]any)) != 0 {
		t.Errorf("far page = %d with %d rows", far.Code, len(far.json()["data"].([]any)))
	}
	// per_page is capped.
	capped := h.do("GET", "/api/application/users?per_page=100000", nil, withCookie(cookie))
	pc := capped.json()["meta"].(map[string]any)["pagination"].(map[string]any)
	if pc["per_page"].(float64) > 500 {
		t.Errorf("per_page not capped: %v", pc["per_page"])
	}
}

// ============================================================ captcha

func TestVerifyCaptchaLayers(t *testing.T) {
	h := newHarness(t)

	t.Run("no providers configured", func(t *testing.T) {
		if err := h.api.verifyCaptchaLayers(nil, "1.2.3.4"); err != nil {
			t.Errorf("err = %v, want nil when captcha is disabled", err)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		h.st.SetSetting("captcha:providers", `[{"id":1,"provider":"turnstile","mode":"visible","site_key":"k","secret":"s"}]`)
		err := h.api.verifyCaptchaLayers(map[string]string{}, "1.2.3.4")
		if err == nil || !strings.Contains(err.Error(), "not completed") {
			t.Errorf("err = %v, want a 'not completed' error", err)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		h.st.SetSetting("captcha:providers", `[{"id":1,"provider":"bogus","mode":"visible","site_key":"k","secret":"s"}]`)
		err := h.api.verifyCaptchaLayers(map[string]string{"1": "token"}, "1.2.3.4")
		if err == nil || !strings.Contains(err.Error(), "unknown captcha provider") {
			t.Errorf("err = %v, want an unknown-provider error", err)
		}
	})
}

func TestCaptchaAdminValidationAndSecrecy(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	bad := h.do("PUT", "/api/application/captcha", []map[string]any{
		{"provider": "nope", "mode": "visible", "site_key": "k", "secret": "s"}}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown provider = %d, want 422", bad.Code)
	}
	badMode := h.do("PUT", "/api/application/captcha", []map[string]any{
		{"provider": "turnstile", "mode": "sneaky", "site_key": "k", "secret": "s"}}, withCookie(cookie))
	if badMode.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad mode = %d, want 422", badMode.Code)
	}
	noSecret := h.do("PUT", "/api/application/captcha", []map[string]any{
		{"provider": "turnstile", "mode": "visible", "site_key": "k"}}, withCookie(cookie))
	if noSecret.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing secret = %d, want 422", noSecret.Code)
	}

	ok := h.do("PUT", "/api/application/captcha", []map[string]any{
		{"provider": "turnstile", "site_key": "k1", "secret": "s1"},
		{"provider": "hcaptcha", "mode": "invisible", "site_key": "k2", "secret": "s2"},
	}, withCookie(cookie))
	if ok.Code != http.StatusOK {
		t.Fatalf("valid layers = %d: %s", ok.Code, ok.Body.String())
	}
	layers := h.api.captchaProviders()
	if len(layers) != 2 {
		t.Fatalf("stored %d layers", len(layers))
	}
	if layers[0].Mode != "visible" {
		t.Errorf("mode not defaulted to visible: %q", layers[0].Mode)
	}
	if layers[0].ID != 1 || layers[1].ID != 2 {
		t.Error("layer ids not assigned sequentially")
	}

	// The public endpoint must never expose secrets.
	pub := h.do("GET", "/auth/captcha", nil)
	if pub.Code != http.StatusOK {
		t.Fatal(pub.Code)
	}
	if strings.Contains(pub.Body.String(), "s1") || strings.Contains(pub.Body.String(), "secret") {
		t.Errorf("public captcha endpoint leaked secrets: %s", pub.Body.String())
	}
	if !strings.Contains(pub.Body.String(), "k1") {
		t.Error("public endpoint does not expose the site key")
	}
}

func TestLoginBlockedWhenCaptchaConfigured(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "hunter2hunter2", true)
	h.st.SetSetting("captcha:providers", `[{"id":1,"provider":"turnstile","mode":"visible","site_key":"k","secret":"s"}]`)

	res := h.do("POST", "/auth/login", map[string]string{"user": "admin", "password": "hunter2hunter2"})
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("login without a captcha token = %d, want 422", res.Code)
	}
	if !strings.Contains(res.detail(), "turnstile") {
		t.Errorf("detail = %q", res.detail())
	}
	if len(res.Result().Cookies()) != 0 {
		t.Error("a session was issued despite the captcha gate")
	}
}

// ============================================================ TLS settings

func TestTLSSettingsValidation(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	cases := []struct{ name, domain, email string }{
		{"ip address", "1.2.3.4", "a@b.com"},
		{"localhost", "localhost", "a@b.com"},
		{"scheme", "https://x.com", "a@b.com"},
		{"port", "x.com:443", "a@b.com"},
		{"dot-local", "panel.local", "a@b.com"},
		{"empty domain", "", "a@b.com"},
		{"bad email", "panel.example.com", "nope"},
		{"empty email", "panel.example.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do("PUT", "/api/application/tls", map[string]any{
				"enabled": true, "domain": tc.domain, "email": tc.email}, withCookie(cookie))
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", res.Code)
			}
		})
	}

	ok := h.do("PUT", "/api/application/tls", map[string]any{
		"enabled": true, "domain": "Panel.Example.COM", "email": "admin@example.com", "staging": true,
	}, withCookie(cookie))
	if ok.Code != http.StatusOK {
		t.Fatalf("valid settings = %d: %s", ok.Code, ok.Body.String())
	}
	if ok.json()["restart_required"] != true {
		t.Error("restart_required not signalled")
	}
	cfg := h.api.TLSSettings()
	if cfg.Domain != "panel.example.com" {
		t.Errorf("domain not normalised to lowercase: %q", cfg.Domain)
	}
	if !cfg.Enabled || !cfg.Staging {
		t.Errorf("flags not stored: %+v", cfg)
	}
	// Enabling pins app:url so wings receives an https address.
	if got := h.st.Setting("app:url", ""); got != "https://panel.example.com" {
		t.Errorf("app:url = %q", got)
	}

	// Disabling skips validation entirely.
	off := h.do("PUT", "/api/application/tls", map[string]any{"enabled": false}, withCookie(cookie))
	if off.Code != http.StatusOK {
		t.Errorf("disable = %d", off.Code)
	}
}

func TestTLSRequestWithoutManagerIsRejected(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	res := h.do("POST", "/api/application/tls/request", nil, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.Code)
	}
	if !strings.Contains(res.detail(), "not running") {
		t.Errorf("detail = %q", res.detail())
	}
}

// ============================================================ webhooks & health

func TestWebhookValidationAndDispatch(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	bad := h.do("PUT", "/api/application/webhooks", []map[string]any{{"url": "ftp://x", "events": []string{"*"}}}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-http url = %d, want 422", bad.Code)
	}

	// Capture dispatches with a local receiver.
	received := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		received <- body
	}))
	defer srv.Close()

	ok := h.do("PUT", "/api/application/webhooks",
		[]map[string]any{{"url": srv.URL, "events": []string{"admin:user.*"}}}, withCookie(cookie))
	if ok.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", ok.Code, ok.Body.String())
	}

	// A matching event fires...
	h.do("POST", "/api/application/users", map[string]any{
		"email": "hooked@example.com", "username": "hooked"}, withCookie(cookie))
	select {
	case body := <-received:
		if body["event"] != "admin:user.create" {
			t.Errorf("event = %v", body["event"])
		}
		if _, ok := body["timestamp"]; !ok {
			t.Error("payload has no timestamp")
		}
		actor := body["data"].(map[string]any)["actor"].(map[string]any)
		if actor["username"] != "admin" {
			t.Errorf("actor = %v", actor)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not delivered")
	}

	// ...a non-matching one does not.
	h.do("PUT", "/api/application/settings", map[string]string{"app:name": "X"}, withCookie(cookie))
	select {
	case body := <-received:
		t.Errorf("non-matching event delivered: %v", body["event"])
	case <-time.After(400 * time.Millisecond):
	}
}

func TestHealthEndpointIsPublic(t *testing.T) {
	h := newHarness(t)
	res := h.do("GET", "/api/system/health", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	body := res.json()
	if body["status"] != "ok" || body["name"] != "Roost" {
		t.Errorf("body = %v", body)
	}
}

// ============================================================ database viewer gate

func TestDBViewerRequiresAdmin(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "dbv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := New(st)
	mux := a.Mux()
	viewer := NewDBViewer(time.Hour, nil)
	defer viewer.Close()
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>viewer</html>"))
	})
	a.MountDBViewer(mux, viewer, spa)
	h := &harness{t: t, api: a, st: st, h: mux}

	h.mkUser("admin", "admin@example.com", "adminpass1", true)
	h.mkUser("user", "user@example.com", "userpass12", false)

	t.Run("anonymous api is 401", func(t *testing.T) {
		if res := h.do("GET", "/dbviewer/api/databases", nil); res.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", res.Code)
		}
	})
	t.Run("anonymous page redirects to login", func(t *testing.T) {
		res := h.do("GET", "/dbviewer/", nil, withHeader("Accept", "text/html"))
		if res.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", res.Code)
		}
		if loc := res.Header().Get("Location"); loc != "/" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("non-admin page is 403", func(t *testing.T) {
		cookie := h.login("user", "userpass12")
		res := h.do("GET", "/dbviewer/", nil, withCookie(cookie), withHeader("Accept", "application/json"))
		if res.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.Code)
		}
	})
	t.Run("non-admin api is 403", func(t *testing.T) {
		cookie := h.login("user", "userpass12")
		res := h.do("GET", "/dbviewer/api/databases", nil, withCookie(cookie))
		if res.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.Code)
		}
	})
	t.Run("admin reaches the spa", func(t *testing.T) {
		cookie := h.login("admin", "adminpass1")
		res := h.do("GET", "/dbviewer/", nil, withCookie(cookie), withHeader("Accept", "text/html"))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "viewer") {
			t.Errorf("status = %d body = %q", res.Code, res.Body.String())
		}
	})
	t.Run("admin reaches the viewer api", func(t *testing.T) {
		cookie := h.login("admin", "adminpass1")
		// No database session yet, so the vendored API answers 401 itself —
		// which proves the request was proxied past Roost's admin gate.
		res := h.do("GET", "/dbviewer/api/databases", nil, withCookie(cookie))
		if res.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want the viewer's own 401", res.Code)
		}
		if strings.Contains(res.Body.String(), "Administrative access") {
			t.Error("request was blocked by Roost's gate instead of reaching the viewer")
		}
	})
}

// ============================================================ egg import

func TestImportEggConfigEncodings(t *testing.T) {
	h := newHarness(t)
	nest := &store.Nest{UUID: auth.UUID(), Name: "Test"}
	h.st.CreateNest(nest)

	t.Run("config fields as JSON-encoded strings (PTDL_v2)", func(t *testing.T) {
		var doc EggDocument
		raw := `{
			"meta": {"version": "PTDL_v2"},
			"name": "StringCfg", "author": "a@b.c",
			"docker_images": {"Java": "img"},
			"config": {"files": "{\"a.txt\":{\"parser\":\"file\"}}", "startup": "{\"done\":\"x\"}", "logs": "{}", "stop": "stop"},
			"scripts": {"installation": {"script": "echo hi", "container": "alpine", "entrypoint": "ash"}},
			"variables": [{"name":"V","env_variable":"V","default_value":"1","user_viewable":true,"user_editable":false}]
		}`
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		egg, err := h.api.ImportEgg(nest.ID, &doc)
		if err != nil {
			t.Fatalf("ImportEgg: %v", err)
		}
		if !strings.Contains(egg.ConfigFiles, "a.txt") {
			t.Errorf("config_files = %q", egg.ConfigFiles)
		}
		vars, _ := h.st.EggVariables(egg.ID)
		if len(vars) != 1 || !vars[0].UserViewable || vars[0].UserEditable {
			t.Errorf("variable flags wrong: %+v", vars[0])
		}
	})

	t.Run("config fields as objects", func(t *testing.T) {
		var doc EggDocument
		raw := `{
			"meta": {"version": "PTDL_v2"},
			"name": "ObjectCfg", "author": "a@b.c",
			"docker_images": {"Java": "img"},
			"config": {"files": {"b.txt": {"parser": "file"}}, "startup": {"done": "x"}, "logs": {}, "stop": "^C"},
			"scripts": {"installation": {"script": "echo hi"}},
			"variables": []
		}`
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		egg, err := h.api.ImportEgg(nest.ID, &doc)
		if err != nil {
			t.Fatalf("ImportEgg: %v", err)
		}
		if !strings.Contains(egg.ConfigFiles, "b.txt") {
			t.Errorf("config_files = %q", egg.ConfigFiles)
		}
		// Defaults are applied when the export omits them.
		if egg.ScriptContainer != "alpine:3.4" || egg.ScriptEntry != "ash" {
			t.Errorf("script defaults = %q/%q", egg.ScriptContainer, egg.ScriptEntry)
		}
	})

	t.Run("PTDL_v1 single image", func(t *testing.T) {
		var doc EggDocument
		raw := `{"meta":{"version":"PTDL_v1"},"name":"V1","image":"legacy:1","config":{},"scripts":{"installation":{}},"variables":[]}`
		json.Unmarshal([]byte(raw), &doc)
		egg, err := h.api.ImportEgg(nest.ID, &doc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(egg.DockerImages, "legacy:1") {
			t.Errorf("docker_images = %q", egg.DockerImages)
		}
		// Empty config objects must still be valid JSON.
		if egg.ConfigFiles != "{}" || egg.ConfigStartup != "{}" {
			t.Errorf("empty configs = %q / %q", egg.ConfigFiles, egg.ConfigStartup)
		}
	})

	t.Run("legacy images array", func(t *testing.T) {
		var doc EggDocument
		raw := `{"name":"V1b","images":["a:1","b:2"],"config":{},"scripts":{"installation":{}},"variables":[]}`
		json.Unmarshal([]byte(raw), &doc)
		egg, err := h.api.ImportEgg(nest.ID, &doc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(egg.DockerImages, "a:1") || !strings.Contains(egg.DockerImages, "b:2") {
			t.Errorf("docker_images = %q", egg.DockerImages)
		}
	})
}

func TestTruthyAcceptsMixedEncodings(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true}, {false, false},
		{float64(1), true}, {float64(0), false},
		{"1", true}, {"true", true}, {"0", false}, {"", false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := truthy(tc.in); got != tc.want {
			t.Errorf("truthy(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRawJSONString(t *testing.T) {
	cases := []struct{ in, want string }{
		{``, "{}"},
		{`null`, "{}"},
		{`"{\"a\":1}"`, `{"a":1}`},
		{`{"a":1}`, `{"a":1}`},
		{`""`, "{}"},
	}
	for _, tc := range cases {
		got := rawJSONString(json.RawMessage(tc.in))
		if got != tc.want {
			t.Errorf("rawJSONString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---- small helpers ----

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
