package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
)

// ---- subuser-by-uuid endpoints ----

func TestSubuserByUUIDEndpoints(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	owner := h.login("owner", "ownerpass1")

	invite := h.do("POST", "/api/client/servers/abc12345/users", map[string]any{
		"email": "invitee@example.com", "permissions": []string{"control.console"},
	}, withCookie(owner))
	if invite.Code != http.StatusOK {
		t.Fatalf("invite = %d: %s", invite.Code, invite.Body.String())
	}
	uuid := invite.json()["attributes"].(map[string]any)["uuid"].(string)

	// GET a single subuser.
	if res := h.do("GET", "/api/client/servers/abc12345/users/"+uuid, nil, withCookie(owner)); res.Code != http.StatusOK {
		t.Errorf("get subuser = %d", res.Code)
	}
	// Update permissions.
	upd := h.do("POST", "/api/client/servers/abc12345/users/"+uuid,
		map[string]any{"permissions": []string{"control.start", "backup.read"}}, withCookie(owner))
	if upd.Code != http.StatusOK {
		t.Errorf("update subuser = %d", upd.Code)
	}
	// Unknown subuser.
	if res := h.do("GET", "/api/client/servers/abc12345/users/does-not-exist", nil, withCookie(owner)); res.Code != http.StatusNotFound {
		t.Errorf("unknown subuser = %d, want 404", res.Code)
	}
	// Delete.
	if res := h.do("DELETE", "/api/client/servers/abc12345/users/"+uuid, nil, withCookie(owner)); res.Code != http.StatusNoContent {
		t.Errorf("delete subuser = %d", res.Code)
	}
}

// ---- admin server databases (trServerDatabaseApp) ----

func TestAdminServerDatabases(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	host := &store.DatabaseHost{Name: "h", Host: "127.0.0.1", Port: 3306, Username: "root", Password: "p"}
	h.st.CreateDatabaseHost(host)

	create := h.do("POST", fmt.Sprintf("/api/application/servers/%d/databases", f.server.ID),
		map[string]any{"database": "prod", "remote": "%", "host": host.ID}, withCookie(cookie))
	if create.Code != http.StatusCreated {
		t.Fatalf("create db = %d: %s", create.Code, create.Body.String())
	}
	dbID := int64(create.json()["attributes"].(map[string]any)["id"].(float64))

	if list := h.do("GET", fmt.Sprintf("/api/application/servers/%d/databases", f.server.ID), nil, withCookie(cookie)); len(list.json()["data"].([]any)) != 1 {
		t.Error("server database not listed")
	}
	if res := h.do("POST", fmt.Sprintf("/api/application/servers/%d/databases/%d/reset-password", f.server.ID, dbID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("reset db password = %d", res.Code)
	}
	if res := h.do("DELETE", fmt.Sprintf("/api/application/servers/%d/databases/%d", f.server.ID, dbID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete db = %d", res.Code)
	}
}

// ---- full user update ----

func TestAdminUpdateUserAllFields(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	target := h.mkUser("target", "target@example.com", "targetpass1", false)

	ext := "sso-42"
	upd := h.do("PATCH", fmt.Sprintf("/api/application/users/%d", target.ID), map[string]any{
		"external_id": ext, "email": "moved@example.com", "username": "renamed",
		"first_name": "First", "last_name": "Last", "language": "de",
		"password": "brandnewpassword", "root_admin": true,
	}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", upd.Code, upd.Body.String())
	}
	got, _ := h.st.UserByID(target.ID)
	if got.Email != "moved@example.com" || got.Username != "renamed" || got.Language != "de" || !got.RootAdmin {
		t.Errorf("fields not applied: %+v", got)
	}
	if got.ExternalID == nil || *got.ExternalID != ext {
		t.Error("external_id not applied")
	}
	if !auth.CheckPassword(got.Password, "brandnewpassword") {
		t.Error("password not changed")
	}
}

// ---- reset password success path ----

func TestPasswordResetSucceedsWithValidToken(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()

	// Plant a reset token exactly as handleForgotPassword would.
	token := "known-reset-token"
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	h.st.SetSetting("password_reset:owner@example.com", auth.SHA256Hex(token)+"|"+expiry)

	res := h.do("POST", "/auth/password/reset", map[string]string{
		"email": "owner@example.com", "token": token, "password": "areallynewpassword"})
	if res.Code != http.StatusOK {
		t.Fatalf("reset = %d: %s", res.Code, res.Body.String())
	}
	// The password changed and a session cookie was issued.
	owner, _ := h.st.UserByUsername("owner")
	if !auth.CheckPassword(owner.Password, "areallynewpassword") {
		t.Error("password not reset")
	}
	if len(res.Result().Cookies()) == 0 {
		t.Error("no session issued after reset")
	}
	// The token is single-use.
	if h.st.Setting("password_reset:owner@example.com", "") != "" {
		t.Error("reset token not consumed")
	}
}

// ---- backup actions ----

func TestBackupActions(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	b := &store.Backup{ServerID: f.server.ID, UUID: auth.UUID(), Name: "snap", IgnoredFiles: "[]",
		Disk: "wings", IsSuccessful: true, CompletedAt: ptrStr(nowISO())}
	h.st.CreateBackup(b)

	// View a single backup.
	if res := h.do("GET", "/api/client/servers/abc12345/backups/"+b.UUID, nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("view backup = %d", res.Code)
	}
	// Lock toggles.
	if res := h.do("POST", "/api/client/servers/abc12345/backups/"+b.UUID+"/lock", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("lock backup = %d", res.Code)
	}
	if got, _ := h.st.BackupByUUID(b.UUID); !got.IsLocked {
		t.Error("backup not locked")
	}
	// Download URL is signed.
	if res := h.do("GET", "/api/client/servers/abc12345/backups/"+b.UUID+"/download", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("download backup = %d", res.Code)
	}
	// Unknown backup.
	if res := h.do("GET", "/api/client/servers/abc12345/backups/unknown", nil, withCookie(cookie)); res.Code != http.StatusNotFound {
		t.Errorf("unknown backup = %d, want 404", res.Code)
	}
}

// ---- backup create parses ignored files (splitLines) ----

func TestBackupCreateParsesIgnoredFiles(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/servers/abc12345/backups", map[string]any{
		"name": "with-ignores", "ignored": "logs/\ncache/\n\n*.tmp",
	}, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("create backup = %d: %s", res.Code, res.Body.String())
	}
	backups, _ := h.st.BackupsForServer(f.server.ID)
	if len(backups) == 0 {
		t.Fatal("backup not created")
	}
	// The blank line must have been dropped.
	var ignored []string
	if err := json.Unmarshal([]byte(backups[0].IgnoredFiles), &ignored); err != nil {
		t.Fatal(err)
	}
	if len(ignored) != 3 {
		t.Errorf("ignored files = %v, want 3 entries", ignored)
	}
}

// ---- auto-pick allocation on provisioning ----

func TestProvisionAutoPicksNode(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	// Add a second free allocation so auto-pick has room after the fixture's is used.
	h.st.CreateAllocations(f.node.ID, "127.0.0.1", nil, []int{25600})

	srv, err := h.api.provisionServer(ProvisionSpec{
		Name: "auto", OwnerID: f.owner.ID, EggID: f.egg.ID, NodeID: 0, // auto
		Memory: 512, Disk: 1024, IO: 500, Allocations: 1,
	})
	if err != nil {
		t.Fatalf("provisionServer (auto node): %v", err)
	}
	if srv.NodeID != f.node.ID {
		t.Errorf("auto-pick chose node %d, want %d", srv.NodeID, f.node.ID)
	}
	if srv.AllocationID == nil {
		t.Error("no allocation assigned")
	}

	// With every allocation taken, provisioning fails cleanly.
	for {
		_, err := h.st.FreeAllocation(f.node.ID)
		if err != nil {
			break
		}
		free, _ := h.st.FreeAllocation(f.node.ID)
		free.ServerID = &srv.ID
		h.st.UpdateAllocation(free)
	}
	if _, err := h.api.provisionServer(ProvisionSpec{Name: "x", OwnerID: f.owner.ID, EggID: f.egg.ID}); err == nil {
		t.Error("provisioning should fail with no free allocation")
	}
}

// ---- scheduler start/stop ----

func TestStartSchedulerStopsOnContextCancel(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.api.StartScheduler(ctx)
	cancel() // the goroutine must observe cancellation and return
	time.Sleep(20 * time.Millisecond)
}

// ---- account API key with allowed IPs ----

func TestCreateAccountKeyWithAllowedIPs(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	res := h.do("POST", "/api/client/account/api-keys", map[string]any{
		"description": "restricted", "allowed_ips": []string{"1.2.3.4", "10.0.0.0/8"},
	}, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("create key = %d: %s", res.Code, res.Body.String())
	}
	// Missing description is rejected.
	if bad := h.do("POST", "/api/client/account/api-keys", map[string]any{}, withCookie(cookie)); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing description = %d, want 422", bad.Code)
	}
}

func ptrStr(s string) *string { return &s }

// ---- revolut webhook fulfilment ----

func revolutSigHeaders(secret, body string) http.Header {
	ts := fmt.Sprintf("%d", timeNowMilli())
	mac := hmacSHA256(secret, "v1."+ts+"."+body)
	return http.Header{
		"Revolut-Signature":         {"v1=" + mac},
		"Revolut-Request-Timestamp": {ts},
	}
}

func TestRevolutWebhookFulfilment(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.st.SetSetting("billing:enabled", "1")
	h.st.SetSetting("billing:currency", "EUR")
	h.st.SetSetting("billing:vat_rate", "1900")
	h.st.SetSetting("billing:seller_name", "Roost UG")
	h.st.SetSetting("billing:seller_country", "DE")
	h.st.SetSetting("billing:revolut_enabled", "1")
	h.st.SetSetting("billing:revolut_secret", "rk")
	h.st.SetSetting("billing:revolut_webhook_secret", "rwsec")
	p := h.mkProduct(f)
	h.st.CreateOrder(&store.Order{UUID: "o", UserID: f.owner.ID, ProductID: p.ID,
		Provider: "revolut", ProviderRef: "revord_1", Status: "pending",
		NetCents: 1000, VATCents: 190, GrossCents: 1190, Currency: "EUR"})

	body := `{"event":"ORDER_COMPLETED","order_id":"revord_1"}`
	res := h.doRaw("POST", "/api/billing/webhook/revolut", body, revolutSigHeaders("rwsec", body))
	if res.Code != http.StatusNoContent {
		t.Fatalf("revolut webhook = %d: %s", res.Code, res.Body.String())
	}
	got, _ := h.st.OrderByUUID("o")
	if got.Status != "paid" || got.ServerID == nil {
		t.Errorf("revolut order not fulfilled: %+v", got)
	}

	// A failed-payment event marks a pending order failed.
	h.st.CreateOrder(&store.Order{UUID: "o2", UserID: f.owner.ID, ProductID: p.ID,
		Provider: "revolut", ProviderRef: "revord_2", Status: "pending",
		NetCents: 1000, GrossCents: 1190, Currency: "EUR"})
	failBody := `{"event":"ORDER_CANCELLED","order_id":"revord_2"}`
	h.doRaw("POST", "/api/billing/webhook/revolut", failBody, revolutSigHeaders("rwsec", failBody))
	if got, _ := h.st.OrderByUUID("o2"); got.Status != "failed" {
		t.Errorf("cancelled order status = %q, want failed", got.Status)
	}
}

// ---- captcha verification against a fake siteverify server ----

func TestVerifyCaptchaAgainstFakeProvider(t *testing.T) {
	// Stand up a fake "siteverify" endpoint and point the turnstile URL at it.
	var lastToken string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		lastToken = r.FormValue("response")
		if lastToken == "good" {
			w.Write([]byte(`{"success":true}`))
		} else {
			w.Write([]byte(`{"success":false}`))
		}
	})
	defer srv.Close()

	h := newHarness(t)
	restore := overrideCaptchaURL("turnstile", srv.URL)
	defer restore()

	h.st.SetSetting("captcha:providers", `[{"id":1,"provider":"turnstile","mode":"visible","site_key":"k","secret":"s"}]`)

	// A good token verifies.
	if err := h.api.verifyCaptchaLayers(map[string]string{"1": "good"}, "1.2.3.4"); err != nil {
		t.Errorf("good token rejected: %v", err)
	}
	// A bad token is refused.
	if err := h.api.verifyCaptchaLayers(map[string]string{"1": "bad"}, "1.2.3.4"); err == nil {
		t.Error("bad token accepted")
	}
}

// ---- shared test helpers ----

func timeNowMilli() int64 { return time.Now().UnixMilli() }

func hmacNew(secret string) hash.Hash { return hmac.New(sha256.New, []byte(secret)) }

func hexEncode(b []byte) string { return hex.EncodeToString(b) }

func hmacSHA256(secret, msg string) string {
	m := hmacNew(secret)
	m.Write([]byte(msg))
	return hexEncode(m.Sum(nil))
}

func newTestServer(fn http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(fn)
}

// overrideCaptchaURL points a provider's siteverify endpoint at a test server
// and returns a function to restore the original.
func overrideCaptchaURL(provider, url string) func() {
	orig := captchaVerifyURLs[provider]
	captchaVerifyURLs[provider] = url
	return func() { captchaVerifyURLs[provider] = orig }
}

// runSchedule with a backup task exercises the backup-creation branch.
func TestRunScheduleBackupTask(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	sc := &store.Schedule{ServerID: f.server.ID, Name: "nightly-backup", CronMinute: "*",
		CronHour: "*", CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true}
	h.st.CreateSchedule(sc)
	h.st.CreateTask(&store.ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "backup",
		Payload: "", ContinueOnFailure: true})
	h.st.CreateTask(&store.ScheduleTask{ScheduleID: sc.ID, SequenceID: 2, Action: "power",
		Payload: "restart", ContinueOnFailure: true})

	a := h.api
	a.runSchedule(sc)

	// A backup row was created by the backup task.
	backups, _ := h.st.BackupsForServer(f.server.ID)
	found := false
	for _, b := range backups {
		if b.Name == "Scheduled backup: nightly-backup" {
			found = true
		}
	}
	if !found {
		t.Error("scheduled backup task did not create a backup")
	}
	got, _ := h.st.ScheduleByID(sc.ID)
	if got.IsProcessing {
		t.Error("schedule left in processing state")
	}
}
