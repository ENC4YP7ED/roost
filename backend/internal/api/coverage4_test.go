package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"roost/internal/store"
)

// Admin server mutation endpoints (details/build/startup/reinstall) — the bulk
// of routesApplicationServers that the earlier tests did not reach.

func TestAdminServerDetailsBuildStartup(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")
	sid := f.server.ID

	// details
	ext := "billing-9"
	det := h.do("PATCH", fmt.Sprintf("/api/application/servers/%d/details", sid), map[string]any{
		"external_id": ext, "name": "Renamed", "user": f.owner.ID, "description": "d",
	}, withCookie(cookie))
	if det.Code != http.StatusOK {
		t.Fatalf("details = %d: %s", det.Code, det.Body.String())
	}
	if got, _ := h.st.ServerByID(sid); got.Name != "Renamed" {
		t.Error("details not applied")
	}
	// details with an unknown owner is rejected.
	if bad := h.do("PATCH", fmt.Sprintf("/api/application/servers/%d/details", sid),
		map[string]any{"user": 9999}, withCookie(cookie)); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("details unknown user = %d, want 422", bad.Code)
	}

	// build: change limits, feature limits, and reassign the allocation.
	build := h.do("PATCH", fmt.Sprintf("/api/application/servers/%d/build", sid), map[string]any{
		"memory": 4096, "swap": 512, "disk": 20480, "io": 600, "cpu": 300, "oom_disabled": false,
		"feature_limits": map[string]any{"databases": 5, "allocations": 3, "backups": 7},
	}, withCookie(cookie))
	if build.Code != http.StatusOK {
		t.Fatalf("build = %d: %s", build.Code, build.Body.String())
	}
	got, _ := h.st.ServerByID(sid)
	if got.Memory != 4096 || got.CPU != 300 || got.DatabaseLimit != 5 || got.BackupLimit != 7 {
		t.Errorf("build not applied: %+v", got)
	}

	// startup: change command, image, egg, skip_scripts, and an env var.
	start := h.do("PATCH", fmt.Sprintf("/api/application/servers/%d/startup", sid), map[string]any{
		"startup": "java -jar custom.jar", "image": "ghcr.io/x:17", "egg": f.egg.ID,
		"skip_scripts": true, "environment": map[string]string{"SERVER_JARFILE": "custom.jar"},
	}, withCookie(cookie))
	if start.Code != http.StatusOK {
		t.Fatalf("startup = %d: %s", start.Code, start.Body.String())
	}
	got, _ = h.st.ServerByID(sid)
	if got.Startup != "java -jar custom.jar" || got.Image != "ghcr.io/x:17" || !got.SkipScripts {
		t.Errorf("startup not applied: %+v", got)
	}
	// startup with an unknown egg is rejected.
	if bad := h.do("PATCH", fmt.Sprintf("/api/application/servers/%d/startup", sid),
		map[string]any{"egg": 9999}, withCookie(cookie)); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("startup unknown egg = %d, want 422", bad.Code)
	}

	// reinstall flips the status to installing (wings offline, but async).
	if res := h.do("POST", fmt.Sprintf("/api/application/servers/%d/reinstall", sid), nil, withCookie(cookie)); res.Code != http.StatusAccepted {
		t.Errorf("reinstall = %d, want 202", res.Code)
	}
	if got, _ := h.st.ServerByID(sid); got.Status == nil || *got.Status != "installing" {
		t.Error("reinstall did not set installing status")
	}
}

// Webhooks + egg-import-from-URL (routesExtras).

func TestAdminWebhooksAndEggImportURL(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	// Webhooks: list (empty), save, list again.
	if res := h.do("GET", "/api/application/webhooks", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("list webhooks = %d", res.Code)
	}
	save := h.do("PUT", "/api/application/webhooks", []map[string]any{
		{"url": "https://example.com/hook", "events": []string{"server:*"}},
	}, withCookie(cookie))
	if save.Code != http.StatusOK {
		t.Fatalf("save webhooks = %d: %s", save.Code, save.Body.String())
	}
	// A non-http URL is rejected.
	if bad := h.do("PUT", "/api/application/webhooks", []map[string]any{{"url": "ftp://x"}}, withCookie(cookie)); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("ftp webhook = %d, want 422", bad.Code)
	}

	// Egg import from a URL: point at a local test server serving a PTDL egg.
	egg := `{"meta":{"version":"PTDL_v2"},"name":"URL Egg","author":"a@b.c",` +
		`"docker_images":{"Java":"img"},"startup":"run","config":{"files":"{}","startup":"{}","logs":"{}","stop":"stop"},` +
		`"scripts":{"installation":{"script":"echo hi","container":"alpine","entrypoint":"ash"}},"variables":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(egg)) }))
	defer srv.Close()

	imp := h.do("POST", fmt.Sprintf("/api/application/nests/%d/eggs/import-url", f.server.NestID),
		map[string]any{"url": srv.URL}, withCookie(cookie))
	if imp.Code != http.StatusCreated {
		t.Fatalf("import-url = %d: %s", imp.Code, imp.Body.String())
	}
	// A non-http URL is rejected.
	if bad := h.do("POST", fmt.Sprintf("/api/application/nests/%d/eggs/import-url", f.server.NestID),
		map[string]any{"url": "notaurl"}, withCookie(cookie)); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad import url = %d, want 422", bad.Code)
	}
	// Import into an unknown nest.
	if nf := h.do("POST", "/api/application/nests/9999/eggs/import-url",
		map[string]any{"url": srv.URL}, withCookie(cookie)); nf.Code != http.StatusNotFound {
		t.Errorf("import to unknown nest = %d, want 404", nf.Code)
	}
}

// Nest & egg CRUD (routesApplicationNests).

func TestAdminNestAndEggCrud(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	// Create a nest.
	nc := h.do("POST", "/api/application/nests", map[string]any{"name": "Custom", "description": "d"}, withCookie(cookie))
	if nc.Code != http.StatusCreated {
		t.Fatalf("create nest = %d: %s", nc.Code, nc.Body.String())
	}
	nestID := int64(nc.json()["attributes"].(map[string]any)["id"].(float64))
	// Update it.
	if res := h.do("PATCH", fmt.Sprintf("/api/application/nests/%d", nestID),
		map[string]any{"name": "Custom2"}, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("update nest = %d", res.Code)
	}
	// Get single nest + a single egg.
	if res := h.do("GET", fmt.Sprintf("/api/application/nests/%d", f.server.NestID), nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("get nest = %d", res.Code)
	}
	if res := h.do("GET", fmt.Sprintf("/api/application/nests/%d/eggs/%d", f.server.NestID, f.egg.ID), nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("get egg = %d", res.Code)
	}
	// Update the egg.
	if res := h.do("PATCH", fmt.Sprintf("/api/application/nests/%d/eggs/%d", f.server.NestID, f.egg.ID),
		map[string]any{"name": "Paper 2", "startup": "new", "config_stop": "end",
			"docker_images": map[string]any{"Java 21": "img:21"}, "script_install": "echo hi"}, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("update egg = %d", res.Code)
	}
	// Delete the empty nest.
	if res := h.do("DELETE", fmt.Sprintf("/api/application/nests/%d", nestID), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete nest = %d", res.Code)
	}
	// Deleting a nest that still has servers is rejected.
	if res := h.do("DELETE", fmt.Sprintf("/api/application/nests/%d", f.server.NestID), nil, withCookie(cookie)); res.Code != http.StatusBadRequest {
		t.Errorf("delete in-use nest = %d, want 400", res.Code)
	}

	// Create an egg in the new nest, then delete it.
	nc2 := h.do("POST", "/api/application/nests", map[string]any{"name": "Deletable"}, withCookie(cookie))
	nid2 := int64(nc2.json()["attributes"].(map[string]any)["id"].(float64))
	imp := h.do("POST", fmt.Sprintf("/api/application/nests/%d/eggs/import", nid2), map[string]any{
		"meta": map[string]any{"version": "PTDL_v2"}, "name": "E", "author": "a@b.c",
		"docker_images": map[string]any{"J": "i"}, "startup": "run",
		"config":    map[string]any{"files": "{}", "startup": "{}", "logs": "{}", "stop": "stop"},
		"scripts":   map[string]any{"installation": map[string]any{"script": "s"}},
		"variables": []any{},
	}, withCookie(cookie))
	if imp.Code != http.StatusCreated {
		t.Fatalf("import egg = %d: %s", imp.Code, imp.Body.String())
	}
	eid := int64(imp.json()["attributes"].(map[string]any)["id"].(float64))
	if res := h.do("DELETE", fmt.Sprintf("/api/application/nests/%d/eggs/%d", nid2, eid), nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete egg = %d", res.Code)
	}
}

// Account activity + api-key + ssh-key listings (routesClientAccount).

func TestAccountListingsAndDeletes(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	// Create a key and an SSH key, list them, then delete them.
	key := h.do("POST", "/api/client/account/api-keys",
		map[string]any{"description": "ci", "allowed_ips": []string{}}, withCookie(cookie))
	ident := key.json()["attributes"].(map[string]any)["identifier"].(string)
	if res := h.do("GET", "/api/client/account/api-keys", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("list api keys = %d", res.Code)
	}
	if res := h.do("DELETE", "/api/client/account/api-keys/"+ident, nil, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("delete api key = %d", res.Code)
	}

	const sshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEsomethingsomethingsomethingXYZ user@host"
	h.do("POST", "/api/client/account/ssh-keys", map[string]any{"name": "k", "public_key": sshKey}, withCookie(cookie))
	list := h.do("GET", "/api/client/account/ssh-keys", nil, withCookie(cookie))
	if list.Code != http.StatusOK {
		t.Errorf("list ssh keys = %d", list.Code)
	}
	fp := list.json()["data"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["fingerprint"].(string)
	if res := h.do("POST", "/api/client/account/ssh-keys/remove",
		map[string]any{"fingerprint": fp}, withCookie(cookie)); res.Code != http.StatusNoContent {
		t.Errorf("remove ssh key = %d", res.Code)
	}

	// Account activity listing.
	if res := h.do("GET", "/api/client/account/activity", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Errorf("account activity = %d", res.Code)
	}
}

// Exercises the remaining node-update field branches and edge helpers.
func TestAdminNodeUpdateAllFields(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	upd := h.do("PATCH", fmt.Sprintf("/api/application/nodes/%d", f.node.ID), map[string]any{
		"public": false, "description": "edge node", "scheme": "https", "behind_proxy": true,
		"memory": 16384, "memory_overallocate": 20, "disk": 200000, "disk_overallocate": 10,
		"upload_size": 200, "daemon_listen": 9090, "daemon_sftp": 2223, "daemon_base": "/data",
	}, withCookie(cookie))
	if upd.Code != http.StatusOK {
		t.Fatalf("update node = %d: %s", upd.Code, upd.Body.String())
	}
	got, _ := h.st.NodeByID(f.node.ID)
	if got.Public || got.Scheme != "https" || !got.BehindProxy || got.MemoryOverallocate != 20 ||
		got.UploadSize != 200 || got.DaemonListen != 9090 || got.DaemonBase != "/data" {
		t.Errorf("node fields not fully applied: %+v", got)
	}
}

// Renders an invoice whose issued date is short and whose due date is absent,
// covering the dateOnly/dueLine fallback branches.
func TestInvoiceHTMLEdgeDates(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	inv := &store.Invoice{
		UserID: f.owner.ID, Status: "issued", Currency: "EUR",
		NetCents: 1000, VATCents: 190, GrossCents: 1190, VATRate: 1900,
		Seller: `{"name":"S"}`, Buyer: `{"name":"B"}`,
		Lines:    `[{"description":"Item","quantity":2,"unit_cents":500,"net_cents":1000}]`,
		IssuedAt: "short", // < 10 chars → dateOnly fallback
		DueAt:    nil,     // → dueLine returns ""
	}
	if err := h.st.CreateInvoice(inv, "INV"); err != nil {
		t.Fatal(err)
	}
	cookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client/billing/invoices/"+inv.Number+"/html", nil, withCookie(cookie))
	if res.Code != http.StatusOK {
		t.Fatalf("invoice html = %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Item") {
		t.Error("invoice line not rendered")
	}
}

// Provisioning onto a maintenance-mode node with auto-pick must skip it.
func TestProvisionSkipsMaintenanceNode(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	// Put the only node into maintenance; auto-pick then finds nothing.
	f.node.MaintenanceMode = true
	h.st.UpdateNode(f.node)
	h.st.CreateAllocations(f.node.ID, "127.0.0.1", nil, []int{25599})

	if _, err := h.api.provisionServer(ProvisionSpec{
		Name: "x", OwnerID: f.owner.ID, EggID: f.egg.ID, NodeID: 0,
	}); err == nil {
		t.Error("auto-pick should skip a maintenance-mode node and fail")
	}
}
