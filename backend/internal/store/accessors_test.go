package store

import (
	"errors"
	"testing"
	"time"
)

// TestAccessorsExerciseEveryQuery calls every read/update/scan accessor against
// a fully-populated database, so the store's own coverage reflects the code the
// API layer relies on. Assertions stay light — correctness of each query lives
// in the focused tests; here we guarantee no query is silently broken.
func TestAccessorsExerciseEveryQuery(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	// ---- users ----
	ext := "ext-123"
	f.user.ExternalID = &ext
	if err := s.UpdateUser(f.user); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if got, err := s.UserByExternalID("ext-123"); err != nil || got.ID != f.user.ID {
		t.Errorf("UserByExternalID: %v", err)
	}
	if _, err := s.UserByExternalID("nope"); !errors.Is(err, ErrNotFound) {
		t.Error("UserByExternalID unknown should be ErrNotFound")
	}
	if n, err := s.CountUsers(); err != nil || n != 1 {
		t.Errorf("CountUsers = %d, %v", n, err)
	}
	if users, err := s.SearchUsers("owner", 5); err != nil || len(users) != 1 {
		t.Errorf("SearchUsers = %d, %v", len(users), err)
	}
	if users, _ := s.SearchUsers("", 0); len(users) != 1 {
		t.Errorf("SearchUsers no-limit = %d", len(users))
	}

	// API keys.
	key := &APIKey{UserID: f.user.ID, KeyType: KeyTypeAccount, Identifier: "ptlc_abcdefghijk",
		TokenHash: "h", AllowedIPs: "[]"}
	if err := s.CreateAPIKey(key); err != nil {
		t.Fatal(err)
	}
	if got, err := s.APIKeyByIdentifier("ptlc_abcdefghijk"); err != nil || got.ID != key.ID {
		t.Errorf("APIKeyByIdentifier: %v", err)
	}
	if _, err := s.APIKeyByIdentifier("missing"); !errors.Is(err, ErrNotFound) {
		t.Error("APIKeyByIdentifier missing should be ErrNotFound")
	}
	s.TouchAPIKey(key.ID)
	if got, _ := s.APIKeyByIdentifier("ptlc_abcdefghijk"); got.LastUsedAt == nil {
		t.Error("TouchAPIKey did not stamp last_used_at")
	}
	if keys, _ := s.APIKeysForUser(f.user.ID, KeyTypeAccount); len(keys) != 1 {
		t.Errorf("APIKeysForUser = %d, want 1", len(keys))
	}

	// SSH keys.
	sshKey := &SSHKey{UserID: f.user.ID, Name: "laptop", Fingerprint: "SHA256:abc", PublicKey: "ssh-ed25519 AAAA"}
	if err := s.CreateSSHKey(sshKey); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.SSHKeysForUser(f.user.ID); len(keys) != 1 {
		t.Errorf("SSHKeysForUser = %d, want 1", len(keys))
	}
	if err := s.DeleteSSHKeyByFingerprint(f.user.ID, "SHA256:abc"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.SSHKeysForUser(f.user.ID); len(keys) != 0 {
		t.Error("SSH key not deleted")
	}

	// Recovery tokens (SessionUser exercised elsewhere).
	if err := s.ReplaceRecoveryTokens(f.user.ID, []string{"r1"}); err != nil {
		t.Fatal(err)
	}

	// ---- nests & eggs ----
	if nests, _ := s.Nests(); len(nests) != 1 {
		t.Errorf("Nests = %d, want 1", len(nests))
	}
	if got, err := s.NestByID(f.nest.ID); err != nil || got.ID != f.nest.ID {
		t.Errorf("NestByID: %v", err)
	}
	if _, err := s.NestByID(9999); !errors.Is(err, ErrNotFound) {
		t.Error("NestByID missing should be ErrNotFound")
	}
	if got, err := s.NestByName("Minecraft"); err != nil || got.ID != f.nest.ID {
		t.Errorf("NestByName: %v", err)
	}
	if _, err := s.NestByName("nope"); !errors.Is(err, ErrNotFound) {
		t.Error("NestByName missing should be ErrNotFound")
	}
	f.nest.Description = "updated"
	if err := s.UpdateNest(f.nest); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.NestByID(f.nest.ID); got.Description != "updated" {
		t.Error("UpdateNest not applied")
	}

	if eggs, _ := s.EggsForNest(f.nest.ID); len(eggs) != 1 {
		t.Errorf("EggsForNest = %d, want 1", len(eggs))
	}
	if got, err := s.EggByID(f.egg.ID); err != nil || got.ID != f.egg.ID {
		t.Errorf("EggByID: %v", err)
	}
	if got, err := s.EggByUUID(f.egg.UUID); err != nil || got.ID != f.egg.ID {
		t.Errorf("EggByUUID: %v", err)
	}
	if _, err := s.EggByUUID("nope"); !errors.Is(err, ErrNotFound) {
		t.Error("EggByUUID missing should be ErrNotFound")
	}
	f.egg.Description = "new"
	if err := s.UpdateEgg(f.egg); err != nil {
		t.Fatal(err)
	}

	v := &EggVariable{EggID: f.egg.ID, Name: "Var", EnvVariable: "VAR", DefaultValue: "x"}
	if err := s.CreateEggVariable(v); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EggVariableByID(v.ID); err != nil || got.ID != v.ID {
		t.Errorf("EggVariableByID: %v", err)
	}
	if _, err := s.EggVariableByID(9999); !errors.Is(err, ErrNotFound) {
		t.Error("EggVariableByID missing should be ErrNotFound")
	}
	v.DefaultValue = "y"
	if err := s.UpdateEggVariable(v); err != nil {
		t.Fatal(err)
	}
	if vars, _ := s.EggVariables(f.egg.ID); len(vars) != 1 {
		t.Errorf("EggVariables = %d, want 1", len(vars))
	}

	// ---- locations & nodes ----
	if locs, _ := s.Locations(); len(locs) != 1 {
		t.Errorf("Locations = %d, want 1", len(locs))
	}
	if got, err := s.LocationByID(f.loc.ID); err != nil || got.ID != f.loc.ID {
		t.Errorf("LocationByID: %v", err)
	}
	if _, err := s.LocationByID(9999); !errors.Is(err, ErrNotFound) {
		t.Error("LocationByID missing should be ErrNotFound")
	}
	f.loc.Long = "Long name"
	if err := s.UpdateLocation(f.loc); err != nil {
		t.Fatal(err)
	}

	if nodes, _ := s.Nodes(); len(nodes) != 1 {
		t.Errorf("Nodes = %d, want 1", len(nodes))
	}
	if got, err := s.NodeByID(f.node.ID); err != nil || got.ID != f.node.ID {
		t.Errorf("NodeByID: %v", err)
	}
	f.node.Description = "n"
	if err := s.UpdateNode(f.node); err != nil {
		t.Fatal(err)
	}
	oldToken := f.node.DaemonToken
	f.node.DaemonToken = "rotated-token-value"
	if err := s.ResetNodeToken(f.node); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.NodeByID(f.node.ID); got.DaemonToken == oldToken {
		t.Error("ResetNodeToken did not persist")
	}

	if allocs, _ := s.AllocationsForServer(f.server.ID); len(allocs) != 1 {
		t.Errorf("AllocationsForServer = %d, want 1", len(allocs))
	}

	// ---- database hosts ----
	host := &DatabaseHost{Name: "h", Host: "127.0.0.1", Port: 3306, Username: "root", Password: "p"}
	if err := s.CreateDatabaseHost(host); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := s.DatabaseHosts(); len(hosts) != 1 {
		t.Errorf("DatabaseHosts = %d, want 1", len(hosts))
	}
	if got, err := s.DatabaseHostByID(host.ID); err != nil || got.ID != host.ID {
		t.Errorf("DatabaseHostByID: %v", err)
	}
	if _, err := s.DatabaseHostByID(9999); !errors.Is(err, ErrNotFound) {
		t.Error("DatabaseHostByID missing should be ErrNotFound")
	}
	host.Port = 3307
	if err := s.UpdateDatabaseHost(host); err != nil {
		t.Fatal(err)
	}

	// ---- mounts ----
	m := &Mount{UUID: "m1", Name: "maps", Source: "/a", Target: "/b"}
	if err := s.CreateMount(m); err != nil {
		t.Fatal(err)
	}
	if mounts, _ := s.Mounts(); len(mounts) != 1 {
		t.Errorf("Mounts = %d, want 1", len(mounts))
	}
	if got, err := s.MountByID(m.ID); err != nil || got.ID != m.ID {
		t.Errorf("MountByID: %v", err)
	}
	if _, err := s.MountByID(9999); !errors.Is(err, ErrNotFound) {
		t.Error("MountByID missing should be ErrNotFound")
	}
	m.Description = "d"
	if err := s.UpdateMount(m); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMount(m.ID); err != nil {
		t.Fatal(err)
	}
	if mounts, _ := s.Mounts(); len(mounts) != 0 {
		t.Error("Mount not deleted")
	}

	// ---- servers ----
	if servers, _ := s.Servers(); len(servers) != 1 {
		t.Errorf("Servers = %d, want 1", len(servers))
	}
	if servers, _ := s.ServersForNode(f.node.ID); len(servers) != 1 {
		t.Errorf("ServersForNode = %d, want 1", len(servers))
	}
	if servers, _ := s.ServersOwnedBy(f.user.ID); len(servers) != 1 {
		t.Errorf("ServersOwnedBy = %d, want 1", len(servers))
	}
	if got, err := s.ServerByID(f.server.ID); err != nil || got.ID != f.server.ID {
		t.Errorf("ServerByID: %v", err)
	}
	if got, err := s.ServerByUUID(f.server.UUID); err != nil || got.ID != f.server.ID {
		t.Errorf("ServerByUUID: %v", err)
	}
	srvExt := "srv-ext"
	f.server.ExternalID = &srvExt
	if err := s.UpdateServer(f.server); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ServerByExternalID("srv-ext"); err != nil || got.ID != f.server.ID {
		t.Errorf("ServerByExternalID: %v", err)
	}
	if n, err := s.CountServers(); err != nil || n != 1 {
		t.Errorf("CountServers = %d, %v", n, err)
	}

	// Subusers.
	guest := mkUser(t, s, "guest", "guest@example.com")
	sub := &Subuser{UserID: guest.ID, ServerID: f.server.ID, Permissions: "[]"}
	if err := s.CreateSubuser(sub); err != nil {
		t.Fatal(err)
	}
	if subs, _ := s.SubusersForServer(f.server.ID); len(subs) != 1 {
		t.Errorf("SubusersForServer = %d, want 1", len(subs))
	}
	sub.Permissions = `["control.console"]`
	if err := s.UpdateSubuser(sub); err != nil {
		t.Fatal(err)
	}

	// Server databases.
	db := &ServerDatabase{ServerID: f.server.ID, DatabaseHostID: host.ID, Database: "s1_db",
		Username: "u1", Remote: "%", Password: "pw"}
	if err := s.CreateServerDatabase(db); err != nil {
		t.Fatal(err)
	}
	if dbs, _ := s.DatabasesForServer(f.server.ID); len(dbs) != 1 {
		t.Errorf("DatabasesForServer = %d, want 1", len(dbs))
	}
	if got, err := s.ServerDatabaseByID(db.ID); err != nil || got.ID != db.ID {
		t.Errorf("ServerDatabaseByID: %v", err)
	}
	db.Password = "new"
	if err := s.UpdateServerDatabase(db); err != nil {
		t.Fatal(err)
	}

	// ---- schedules & backups ----
	sc := &Schedule{ServerID: f.server.ID, Name: "s", CronMinute: "0", CronHour: "3",
		CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true}
	if err := s.CreateSchedule(sc); err != nil {
		t.Fatal(err)
	}
	if scs, _ := s.SchedulesForServer(f.server.ID); len(scs) != 1 {
		t.Errorf("SchedulesForServer = %d, want 1", len(scs))
	}
	if got, err := s.ScheduleByID(sc.ID); err != nil || got.ID != sc.ID {
		t.Errorf("ScheduleByID: %v", err)
	}
	sc.Name = "renamed"
	if err := s.UpdateSchedule(sc); err != nil {
		t.Fatal(err)
	}
	task := &ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "command", Payload: "x"}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if got, err := s.TaskByID(task.ID); err != nil || got.ID != task.ID {
		t.Errorf("TaskByID: %v", err)
	}
	task.Payload = "y"
	if err := s.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}

	backup := &Backup{ServerID: f.server.ID, UUID: "b1", Name: "b", IgnoredFiles: "[]", Disk: "wings"}
	if err := s.CreateBackup(backup); err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.BackupsForServer(f.server.ID); len(bs) != 1 {
		t.Errorf("BackupsForServer = %d, want 1", len(bs))
	}

	// ---- billing ----
	p := mkProduct(t, s, f, 1500)
	order := &Order{UUID: "o", UserID: f.user.ID, ProductID: p.ID, Provider: "stripe",
		Status: "paid", NetCents: 1500, GrossCents: 1785, Currency: "EUR"}
	if err := s.CreateOrder(order); err != nil {
		t.Fatal(err)
	}
	if got, err := s.OrderByID(order.ID); err != nil || got.ID != order.ID {
		t.Errorf("OrderByID: %v", err)
	}
	if orders, _ := s.Orders(); len(orders) != 1 {
		t.Errorf("Orders = %d, want 1", len(orders))
	}
	inv := &Invoice{UserID: f.user.ID, OrderID: &order.ID, Status: "issued", Currency: "EUR",
		NetCents: 1500, GrossCents: 1785, Seller: "{}", Buyer: "{}", Lines: "[]", IssuedAt: now()}
	if err := s.CreateInvoice(inv, "INV"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.InvoiceByID(inv.ID); err != nil || got.ID != inv.ID {
		t.Errorf("InvoiceByID: %v", err)
	}
	if invs, _ := s.Invoices(); len(invs) != 1 {
		t.Errorf("Invoices = %d, want 1", len(invs))
	}
	if err := s.MarkInvoicePaid(inv.ID, now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.InvoiceByID(inv.ID); got.Status != "paid" {
		t.Error("MarkInvoicePaid did not update status")
	}

	// ---- settings & activity ----
	s.SetSetting("k", "v")
	if all, _ := s.Settings(); all["k"] != "v" {
		t.Error("Settings did not return the value")
	}
	log := &ActivityLog{Event: "e", IP: "1.1.1.1", Properties: "{}", Timestamp: time.Now().UTC().Format(time.RFC3339)}
	if err := s.LogActivity(log); err != nil {
		t.Fatal(err)
	}
}

// TestOpenRejectsUnwritablePath exercises the error path of Open.
func TestOpenRejectsUnwritablePath(t *testing.T) {
	if _, err := Open("/proc/nonexistent-dir/db.sqlite"); err == nil {
		t.Error("Open accepted an unwritable path")
	}
}

// TestDeleteWithDependenciesErrorPaths covers the guard branches.
func TestDeleteEggNestServerGuards(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	// Egg/nest deletion is blocked while a server uses them.
	if err := s.DeleteEgg(f.egg.ID); err == nil {
		t.Error("DeleteEgg should be blocked by a server")
	}
	if err := s.DeleteNest(f.nest.ID); err == nil {
		t.Error("DeleteNest should be blocked by a server")
	}
	if err := s.DeleteServer(f.server.ID); err != nil {
		t.Fatal(err)
	}
	// Now they delete cleanly (DeleteEgg cascades its variables).
	v := &EggVariable{EggID: f.egg.ID, Name: "V", EnvVariable: "V"}
	s.CreateEggVariable(v)
	if err := s.DeleteEgg(f.egg.ID); err != nil {
		t.Errorf("DeleteEgg after server removal: %v", err)
	}
	if err := s.DeleteNest(f.nest.ID); err != nil {
		t.Errorf("DeleteNest after server removal: %v", err)
	}
}
