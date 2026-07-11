package api

import (
	"fmt"
	"net/http"
	"testing"

	"roost/internal/store"
)

// These drive the client feature endpoints that proxy to wings or mutate
// server-scoped resources. With no wings daemon reachable the proxy calls
// return a gateway error, but the handler bodies (auth, permission checks,
// request shaping) still execute — which is what we cover here.

func TestFileManagerEndpoints(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	base := "/api/client/servers/abc12345/files"

	// Proxied endpoints — wings is offline, so each returns 502, exercising
	// the proxy path and permission gate.
	proxied := []struct{ method, path string; body any }{
		{"GET", base + "/list?directory=/", nil},
		{"GET", base + "/contents?file=server.properties", nil},
		{"PUT", base + "/rename", map[string]any{"root": "/", "files": []map[string]string{{"from": "a", "to": "b"}}}},
		{"POST", base + "/copy", map[string]any{"location": "/a"}},
		{"POST", base + "/compress", map[string]any{"root": "/", "files": []string{"a"}}},
		{"POST", base + "/decompress", map[string]any{"root": "/", "file": "a.zip"}},
		{"POST", base + "/delete", map[string]any{"root": "/", "files": []string{"a"}}},
		{"POST", base + "/create-folder", map[string]any{"root": "/", "name": "new"}},
		{"POST", base + "/chmod", map[string]any{"root": "/", "files": []map[string]any{{"file": "a", "mode": "0644"}}}},
		{"POST", base + "/pull", map[string]any{"url": "https://example.com/x"}},
	}
	for _, p := range proxied {
		res := h.do(p.method, p.path, p.body, withCookie(cookie))
		if res.Code == http.StatusUnauthorized || res.Code == http.StatusForbidden {
			t.Errorf("%s %s should pass auth but got %d", p.method, p.path, res.Code)
		}
	}

	// Signed URL endpoints resolve locally (no wings call) and return 200.
	if res := h.do("GET", base+"/download?file=logs/latest.log", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("download signed url = %d, want 200", res.Code)
	}
	if res := h.do("GET", base+"/upload", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("upload signed url = %d, want 200", res.Code)
	}
	// Missing file param on download.
	if res := h.do("GET", base+"/download", nil, withCookie(cookie)); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("download without file = %d, want 422", res.Code)
	}
}

func TestNetworkAllocationLifecycle(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	base := "/api/client/servers/abc12345/network/allocations"

	// The fixture's node has a second free allocation (25566); adding one works.
	add := h.do("POST", base, nil, withCookie(cookie))
	if add.Code != http.StatusOK {
		t.Fatalf("add allocation = %d: %s", add.Code, add.Body.String())
	}
	newID := int64(add.json()["attributes"].(map[string]any)["id"].(float64))

	// Set a note on it.
	if res := h.do("POST", fmt.Sprintf("%s/%d", base, newID), map[string]any{"notes": "web"}, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("set notes = %d", res.Code)
	}
	// Make it primary.
	if res := h.do("POST", fmt.Sprintf("%s/%d/primary", base, newID), nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("set primary = %d", res.Code)
	}
	// The old primary can now be removed.
	if res := h.do("DELETE", fmt.Sprintf("%s/%d", base, *f.server.AllocationID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete old primary = %d: %s", res.Code, res.Body.String())
	}
}

func TestClientDatabaseLifecycle(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	host := &store.DatabaseHost{Name: "h", Host: "127.0.0.1", Port: 3306, Username: "root", Password: "p"}
	h.st.CreateDatabaseHost(host)
	base := "/api/client/servers/abc12345/databases"

	create := h.do("POST", base, map[string]any{"database": "mydb", "remote": "%"}, withCookie(cookie))
	if create.Code != http.StatusOK {
		t.Fatalf("create db = %d: %s", create.Code, create.Body.String())
	}
	id := create.json()["attributes"].(map[string]any)["id"].(string)

	if res := h.do("POST", base+"/"+id+"/rotate-password", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("rotate = %d", res.Code)
	}
	if res := h.do("DELETE", base+"/"+id, nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete db = %d", res.Code)
	}
	_ = f
}

func TestScheduleExecuteAndTaskDelete(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	sc := &store.Schedule{ServerID: f.server.ID, Name: "s", CronMinute: "*", CronHour: "*",
		CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true}
	h.st.CreateSchedule(sc)
	task := &store.ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "command", Payload: "x"}
	h.st.CreateTask(task)
	base := fmt.Sprintf("/api/client/servers/abc12345/schedules/%d", sc.ID)

	// Execute triggers a background run (accepted).
	if res := h.do("POST", base+"/execute", nil, withCookie(cookie)); res.Code != http.StatusAccepted {
		t.Errorf("execute = %d, want 202", res.Code)
	}
	// Update the schedule.
	if res := h.do("POST", base, map[string]any{"name": "renamed", "minute": "0", "hour": "2"}, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("update schedule = %d", res.Code)
	}
	// Update then delete the task.
	if res := h.do("POST", fmt.Sprintf("%s/tasks/%d", base, task.ID),
		map[string]any{"action": "power", "payload": "restart"}, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("update task = %d", res.Code)
	}
	if res := h.do("DELETE", fmt.Sprintf("%s/tasks/%d", base, task.ID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete task = %d", res.Code)
	}
}

func TestServerActivityAndCommandAndSettings(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Rename via settings.
	if res := h.do("POST", "/api/client/servers/abc12345/settings/rename",
		map[string]any{"name": "New Name", "description": "d"}, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("rename = %d", res.Code)
	}
	if got, _ := h.st.ServerByID(f.server.ID); got.Name != "New Name" {
		t.Error("server not renamed")
	}
	// Reinstall (wings offline → 502, but the status flips to installing first).
	h.do("POST", "/api/client/servers/abc12345/settings/reinstall", nil, withCookie(cookie))

	// A console command with wings down returns 502.
	if res := h.do("POST", "/api/client/servers/abc12345/command",
		map[string]any{"command": "say hi"}, withCookie(cookie)); res.Code != http.StatusBadGateway {
		t.Errorf("command with wings down = %d, want 502", res.Code)
	}
}

func TestUpdateEmailSuccess(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")
	res := h.do("PUT", "/api/client/account/email",
		map[string]any{"email": "brand-new@example.com", "password": "ownerpass1"}, withCookie(cookie))
	if res.Code != http.StatusNoContent {
		t.Fatalf("update email = %d: %s", res.Code, res.Body.String())
	}
	if _, err := h.st.UserByEmail("brand-new@example.com"); err != nil {
		t.Error("email not updated")
	}
	// A duplicate email conflicts.
	h.mkUser("other", "taken@example.com", "otherpass1", false)
	dup := h.do("PUT", "/api/client/account/email",
		map[string]any{"email": "taken@example.com", "password": "ownerpass1"}, withCookie(cookie))
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate email = %d, want 409", dup.Code)
	}
}
