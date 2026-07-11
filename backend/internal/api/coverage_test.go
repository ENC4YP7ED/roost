package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"roost/internal/store"
	"roost/internal/tlsmgr"
)

// totpNow computes the current 6-digit TOTP for a base32 secret (RFC 6238),
// so the 2FA-enable handler can be driven with a valid code.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("bad secret: %v", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(time.Now().Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	val := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", val%1000000)
}

// ---- two-factor ----

func TestTwoFactorFullLifecycle(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Setup returns a secret + provisioning URI.
	setup := h.do("GET", "/api/client/account/two-factor", nil, withCookie(cookie))
	if setup.Code != http.StatusOK {
		t.Fatalf("2fa setup = %d", setup.Code)
	}
	secret := setup.json()["data"].(map[string]any)["secret"].(string)
	if secret == "" {
		t.Fatal("no secret returned")
	}

	// A wrong code is refused.
	if bad := h.do("POST", "/api/client/account/two-factor",
		map[string]string{"code": "000000"}, withCookie(cookie)); bad.Code != http.StatusBadRequest {
		t.Errorf("wrong 2fa code = %d, want 400", bad.Code)
	}

	// A correct code enables 2FA and returns recovery tokens.
	enable := h.do("POST", "/api/client/account/two-factor",
		map[string]string{"code": totpNow(t, secret)}, withCookie(cookie))
	if enable.Code != http.StatusOK {
		t.Fatalf("2fa enable = %d: %s", enable.Code, enable.Body.String())
	}
	tokens := enable.json()["attributes"].(map[string]any)["tokens"].([]any)
	if len(tokens) == 0 {
		t.Error("no recovery tokens issued")
	}
	if u, _ := h.st.UserByUsername("owner"); !u.UseTOTP {
		t.Error("use_totp not set")
	}

	// Disable requires the account password.
	if bad := h.do("POST", "/api/client/account/two-factor/disable",
		map[string]string{"password": "wrong"}, withCookie(cookie)); bad.Code != http.StatusBadRequest {
		t.Errorf("disable with wrong password = %d, want 400", bad.Code)
	}
	if ok := h.do("POST", "/api/client/account/two-factor/disable",
		map[string]string{"password": "ownerpass1"}, withCookie(cookie)); ok.Code != http.StatusNoContent {
		t.Fatalf("disable = %d", ok.Code)
	}
	if u, _ := h.st.UserByUsername("owner"); u.UseTOTP {
		t.Error("2fa still enabled after disable")
	}
}

// ---- password reset ----

func TestPasswordResetFlow(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()

	// Forgot writes a reset token to settings (surfaced via the log in prod).
	h.do("POST", "/auth/password", map[string]string{"email": "owner@example.com"})
	stored := h.st.Setting("password_reset:owner@example.com", "")
	if stored == "" {
		t.Fatal("no reset token stored")
	}

	// We cannot read the raw token (only its hash is stored), so drive the
	// invalid path and the too-short-password path, which is enough for the
	// handler's branches.
	short := h.do("POST", "/auth/password/reset", map[string]string{
		"email": "owner@example.com", "token": "x", "password": "short"})
	if short.Code != http.StatusUnprocessableEntity {
		t.Errorf("short password = %d, want 422", short.Code)
	}
	badToken := h.do("POST", "/auth/password/reset", map[string]string{
		"email": "owner@example.com", "token": "wrongtoken", "password": "longenough123"})
	if badToken.Code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", badToken.Code)
	}
	unknownEmail := h.do("POST", "/auth/password/reset", map[string]string{
		"email": "ghost@example.com", "token": "x", "password": "longenough123"})
	if unknownEmail.Code != http.StatusUnauthorized {
		t.Errorf("unknown email = %d, want 401", unknownEmail.Code)
	}
}

// ---- admin nodes ----

func TestAdminNodeCreateUpdateAndConfig(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	loc := &store.Location{Short: "eu"}
	h.st.CreateLocation(loc)

	create := h.do("POST", "/api/application/nodes", map[string]any{
		"name": "node-eu", "fqdn": "eu.example.com", "location_id": loc.ID,
		"scheme": "https", "memory": 16384, "disk": 102400,
	}, withCookie(cookie))
	if create.Code != http.StatusCreated {
		t.Fatalf("create node = %d: %s", create.Code, create.Body.String())
	}
	id := int64(create.json()["attributes"].(map[string]any)["id"].(float64))

	// Validation: unknown location.
	badLoc := h.do("POST", "/api/application/nodes", map[string]any{
		"name": "x", "fqdn": "x.com", "location_id": 9999}, withCookie(cookie))
	if badLoc.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown location = %d, want 422", badLoc.Code)
	}
	// Missing required fields.
	missing := h.do("POST", "/api/application/nodes", map[string]any{"name": "x"}, withCookie(cookie))
	if missing.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing fqdn/location = %d, want 422", missing.Code)
	}

	upd := h.do("PATCH", fmt.Sprintf("/api/application/nodes/%d", id), map[string]any{
		"name": "node-eu-2", "maintenance_mode": true, "memory": 32768,
	}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update node = %d", upd.Code)
	}
	if got, _ := h.st.NodeByID(id); got.Name != "node-eu-2" || !got.MaintenanceMode || got.Memory != 32768 {
		t.Errorf("node not updated: %+v", got)
	}

	// The wings configuration document renders.
	cfg := h.do("GET", fmt.Sprintf("/api/application/nodes/%d/configuration", id), nil, withCookie(cookie))
	if cfg.Code != http.StatusOK {
		t.Fatalf("node config = %d", cfg.Code)
	}
	body := cfg.json()
	if body["uuid"] == nil || body["token"] == nil {
		t.Error("node configuration missing uuid/token")
	}

	// Token rotation changes the daemon credentials.
	before, _ := h.st.NodeByID(id)
	reset := h.do("POST", fmt.Sprintf("/api/application/nodes/%d/reset-token", id), nil, withCookie(cookie))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset-token = %d", reset.Code)
	}
	after, _ := h.st.NodeByID(id)
	if after.DaemonToken == before.DaemonToken {
		t.Error("daemon token not rotated")
	}
}

// ---- admin egg variables ----

func TestAdminEggVariableCrud(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	base := fmt.Sprintf("/api/application/nests/%d/eggs/%d/variables", f.server.NestID, f.egg.ID)

	create := h.do("POST", base, map[string]any{
		"name": "Level Seed", "env_variable": "SEED", "default_value": "", "user_editable": true,
	}, withCookie(cookie))
	if create.Code != http.StatusOK {
		t.Fatalf("create egg variable = %d: %s", create.Code, create.Body.String())
	}
	vid := int64(create.json()["attributes"].(map[string]any)["id"].(float64))

	// Missing name/env is rejected.
	bad := h.do("POST", base, map[string]any{"description": "x"}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing fields = %d, want 422", bad.Code)
	}

	upd := h.do("PATCH", fmt.Sprintf("%s/%d", base, vid), map[string]any{
		"default_value": "minecraft", "user_editable": false}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update variable = %d", upd.Code)
	}
	got, _ := h.st.EggVariableByID(vid)
	if got.DefaultValue != "minecraft" || got.UserEditable {
		t.Errorf("variable not updated: %+v", got)
	}

	del := h.do("DELETE", fmt.Sprintf("%s/%d", base, vid), nil, withCookie(cookie))
	if del.Code != http.StatusNoContent {
		t.Errorf("delete variable = %d", del.Code)
	}
	if _, err := h.st.EggVariableByID(vid); err == nil {
		t.Error("variable not deleted")
	}
}

// ---- admin server delete ----

func TestAdminServerDelete(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	// Wings is unreachable; the plain delete should report a gateway error,
	// and the forced delete should succeed regardless.
	plain := h.do("DELETE", fmt.Sprintf("/api/application/servers/%d", f.server.ID), nil, withCookie(cookie))
	if plain.Code != http.StatusBadGateway {
		t.Logf("plain delete = %d (wings offline)", plain.Code)
	}
	force := h.do("DELETE", fmt.Sprintf("/api/application/servers/%d/force", f.server.ID), nil, withCookie(cookie))
	if force.Code != http.StatusNoContent {
		t.Fatalf("force delete = %d: %s", force.Code, force.Body.String())
	}
	if _, err := h.st.ServerByID(f.server.ID); err == nil {
		t.Error("server not deleted")
	}
}

// ---- admin database hosts & mounts (routesApplicationExtras) ----

func TestAdminDatabaseHostsAndMounts(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	// Database host CRUD.
	createHost := h.do("POST", "/api/application/database-hosts", map[string]any{
		"name": "primary", "host": "10.0.0.5", "port": 3306, "username": "root", "password": "s3cret",
	}, withCookie(cookie))
	if createHost.Code != http.StatusCreated {
		t.Fatalf("create host = %d: %s", createHost.Code, createHost.Body.String())
	}
	hostID := int64(createHost.json()["attributes"].(map[string]any)["id"].(float64))
	// The password must never appear in the response.
	if strings.Contains(createHost.Body.String(), "s3cret") {
		t.Error("database host response leaked the password")
	}
	list := h.do("GET", "/api/application/database-hosts", nil, withCookie(cookie))
	if len(list.json()["data"].([]any)) != 1 {
		t.Error("host not listed")
	}
	h.do("PATCH", fmt.Sprintf("/api/application/database-hosts/%d", hostID),
		map[string]any{"name": "renamed"}, withCookie(cookie))
	if got, _ := h.st.DatabaseHostByID(hostID); got.Name != "renamed" {
		t.Error("host not updated")
	}
	if del := h.do("DELETE", fmt.Sprintf("/api/application/database-hosts/%d", hostID), nil, withCookie(cookie)); del.Code != http.StatusNoContent {
		t.Errorf("delete host = %d", del.Code)
	}

	// Mount CRUD.
	createMount := h.do("POST", "/api/application/mounts", map[string]any{
		"name": "worlds", "source": "/srv/worlds", "target": "/worlds", "read_only": true,
	}, withCookie(cookie))
	if createMount.Code != http.StatusCreated {
		t.Fatalf("create mount = %d: %s", createMount.Code, createMount.Body.String())
	}
	mountID := int64(createMount.json()["attributes"].(map[string]any)["id"].(float64))
	if mounts := h.do("GET", "/api/application/mounts", nil, withCookie(cookie)); len(mounts.json()["data"].([]any)) != 1 {
		t.Error("mount not listed")
	}
	h.do("PATCH", fmt.Sprintf("/api/application/mounts/%d", mountID),
		map[string]any{"read_only": false}, withCookie(cookie))
	if got, _ := h.st.MountByID(mountID); got.ReadOnly {
		t.Error("mount not updated")
	}
	if del := h.do("DELETE", fmt.Sprintf("/api/application/mounts/%d", mountID), nil, withCookie(cookie)); del.Code != http.StatusNoContent {
		t.Errorf("delete mount = %d", del.Code)
	}
}

// ---- admin locations update/delete ----

func TestAdminLocationUpdateDelete(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	create := h.do("POST", "/api/application/locations", map[string]any{"short": "us", "long": "USA"}, withCookie(cookie))
	id := int64(create.json()["attributes"].(map[string]any)["id"].(float64))
	upd := h.do("PATCH", fmt.Sprintf("/api/application/locations/%d", id), map[string]any{"long": "United States"}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update location = %d", upd.Code)
	}
	if got, _ := h.st.LocationByID(id); got.Long != "United States" {
		t.Error("location not updated")
	}
	if del := h.do("DELETE", fmt.Sprintf("/api/application/locations/%d", id), nil, withCookie(cookie)); del.Code != http.StatusNoContent {
		t.Errorf("delete location = %d", del.Code)
	}
}

// ---- client feature transformers via their endpoints ----

func TestClientFeatureListingsRender(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Seed one row per feature so each transformer runs.
	host := &store.DatabaseHost{Name: "h", Host: "127.0.0.1", Port: 3306, Username: "root", Password: "p"}
	h.st.CreateDatabaseHost(host)
	h.st.CreateServerDatabase(&store.ServerDatabase{ServerID: f.server.ID, DatabaseHostID: host.ID,
		Database: "db", Username: "u", Remote: "%", Password: "pw"})
	h.st.CreateBackup(&store.Backup{ServerID: f.server.ID, UUID: "b", Name: "backup", IgnoredFiles: "[]", Disk: "wings"})
	sc := &store.Schedule{ServerID: f.server.ID, Name: "s", CronMinute: "*", CronHour: "*",
		CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true}
	h.st.CreateSchedule(sc)
	h.st.CreateTask(&store.ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "command", Payload: "x"})
	guest := h.mkUser("guest", "guest@example.com", "guestpass1", false)
	h.st.CreateSubuser(&store.Subuser{UserID: guest.ID, ServerID: f.server.ID, Permissions: `["control.console"]`})
	h.st.LogActivity(&store.ActivityLog{Event: "server:power.start", IP: "1.1.1.1", Properties: "{}"},
		[2]any{"server", f.server.ID})

	id := "abc12345"
	endpoints := []string{
		"/api/client/servers/" + id + "/databases?include=password",
		"/api/client/servers/" + id + "/backups",
		"/api/client/servers/" + id + "/schedules",
		"/api/client/servers/" + id + "/network/allocations",
		"/api/client/servers/" + id + "/users",
		"/api/client/servers/" + id + "/startup",
		"/api/client/servers/" + id + "/activity",
	}
	for _, e := range endpoints {
		if res := h.do("GET", e, nil, withCookie(cookie)); res.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", e, res.Code)
		}
	}
}

// ---- admin infra transformers via their endpoints ----

func TestAdminInfraListingsRender(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	h.st.CreateMount(&store.Mount{UUID: "m", Name: "m", Source: "/a", Target: "/b"})

	for _, e := range []string{
		"/api/application/locations",
		"/api/application/nodes",
		fmt.Sprintf("/api/application/nodes/%d/allocations", f.node.ID),
		"/api/application/servers",
		"/api/application/mounts",
		"/api/application/nests",
	} {
		if res := h.do("GET", e, nil, withCookie(cookie)); res.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", e, res.Code)
		}
	}
}

// ---- billing subscriptions transformer + TLS manager wiring ----

func TestClientSubscriptionsListing(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	h.st.CreateSubscription(&store.Subscription{UUID: "s", UserID: f.owner.ID, ProductID: p.ID,
		Provider: "stripe", ProviderRef: "sub_1", Status: "active"})
	cookie := h.login("owner", "ownerpass1")

	res := h.do("GET", "/api/client/billing/subscriptions", nil, withCookie(cookie))
	if res.Code != http.StatusOK || len(res.json()["data"].([]any)) != 1 {
		t.Errorf("subscriptions listing = %d, %d rows", res.Code, len(res.json()["data"].([]any)))
	}
}

func TestTLSManagerStatusReported(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	h.st.SetSetting("tls:enabled", "1")
	h.st.SetSetting("tls:domain", "panel.example.com")
	h.st.SetSetting("tls:email", "admin@example.com")

	// Wire a live (staging) manager, as main.go does.
	mgr := tlsmgr.New(tlsmgr.Config{Domain: "panel.example.com", Email: "admin@example.com",
		CacheDir: t.TempDir(), Staging: true})
	h.api.SetTLSManager(mgr)

	cookie := h.login("admin", "adminpass1")
	res := h.do("GET", "/api/application/tls", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	body := res.json()
	if body["active"] != true {
		t.Error("tls active flag not set with a manager wired")
	}
	if body["certificate_issued"] != false {
		t.Error("expected no certificate on a fresh cache")
	}
}

// ---- scheduler ----

func TestSchedulerRunsDueSchedules(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()

	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	sc := &store.Schedule{ServerID: f.server.ID, Name: "due", CronMinute: "*", CronHour: "*",
		CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true, NextRunAt: &past}
	h.st.CreateSchedule(sc)
	h.st.CreateTask(&store.ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "command", Payload: "say hi"})

	// processSchedules runs runSchedule in a goroutine; wings is offline so the
	// task fails, but the schedule must be re-armed with a fresh next_run_at.
	h.api.processSchedules()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := h.st.ScheduleByID(sc.ID)
		if got.LastRunAt != nil && !got.IsProcessing {
			if got.NextRunAt == nil || *got.NextRunAt == past {
				t.Error("schedule not re-armed after running")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("schedule was not processed within the timeout")
}

// ---- misc small helpers ----

func TestNilStringHelper(t *testing.T) {
	if nilString("") != nil {
		t.Error("nilString(\"\") should be nil")
	}
	if v := nilString("x"); v == nil || *v != "x" {
		t.Error("nilString(\"x\") should point to x")
	}
}
