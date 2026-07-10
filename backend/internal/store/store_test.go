package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkUser(t *testing.T, s *Store, username, email string) *User {
	t.Helper()
	u := &User{UUID: username + "-uuid", Username: username, Email: email, Password: "hash", Language: "en"}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// fixture builds a complete location → node → allocation → egg → server graph.
type fixture struct {
	user   *User
	loc    *Location
	node   *Node
	alloc  *Allocation
	nest   *Nest
	egg    *Egg
	server *Server
}

func mkFixture(t *testing.T, s *Store) fixture {
	t.Helper()
	u := mkUser(t, s, "owner", "owner@example.com")

	loc := &Location{Short: "local", Long: "Local"}
	if err := s.CreateLocation(loc); err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	node := &Node{UUID: "node-uuid", Name: "node01", LocationID: loc.ID, FQDN: "127.0.0.1",
		Scheme: "http", Memory: 8192, Disk: 51200, DaemonTokenID: "tokenid", DaemonToken: "secret",
		DaemonListen: 8080, DaemonSFTP: 2022, DaemonBase: "/var/lib"}
	if err := s.CreateNode(node); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if _, err := s.CreateAllocations(node.ID, "127.0.0.1", nil, []int{25565, 25566}); err != nil {
		t.Fatalf("CreateAllocations: %v", err)
	}
	alloc, err := s.FreeAllocation(node.ID)
	if err != nil {
		t.Fatalf("FreeAllocation: %v", err)
	}

	nest := &Nest{UUID: "nest-uuid", Author: "a@b.c", Name: "Minecraft"}
	if err := s.CreateNest(nest); err != nil {
		t.Fatalf("CreateNest: %v", err)
	}
	egg := &Egg{UUID: "egg-uuid", NestID: nest.ID, Author: "a@b.c", Name: "Paper",
		Features: "[]", DockerImages: `{"Java 21":"img:21"}`, FileDenylist: "[]",
		ConfigFiles: "{}", ConfigStartup: "{}", ConfigLogs: "{}", ConfigStop: "stop",
		Startup: "java -jar {{SERVER_JARFILE}}", ScriptContainer: "alpine", ScriptEntry: "ash"}
	if err := s.CreateEgg(egg); err != nil {
		t.Fatalf("CreateEgg: %v", err)
	}

	srv := &Server{UUID: "srv-uuid", UUIDShort: "abc12345", NodeID: node.ID, Name: "SMP",
		OwnerID: u.ID, Memory: 2048, Disk: 10240, IO: 500, NestID: nest.ID, EggID: egg.ID,
		Startup: egg.Startup, Image: "img:21", AllocationID: &alloc.ID,
		DatabaseLimit: 2, AllocationLimit: 2, BackupLimit: 2}
	if err := s.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	alloc.ServerID = &srv.ID
	if err := s.UpdateAllocation(alloc); err != nil {
		t.Fatalf("UpdateAllocation: %v", err)
	}
	return fixture{user: u, loc: loc, node: node, alloc: alloc, nest: nest, egg: egg, server: srv}
}

// ---- schema / migrations ----

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	mkUser(t, s, "admin", "admin@example.com")
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (migrations not idempotent): %v", err)
	}
	defer s2.Close()
	if _, err := s2.UserByEmail("admin@example.com"); err != nil {
		t.Errorf("data lost across reopen: %v", err)
	}
}

// ---- users ----

func TestUserLookupsAndCaseInsensitivity(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "Admin", "Admin@Example.com")

	if got, err := s.UserByEmail("admin@example.com"); err != nil || got.ID != u.ID {
		t.Errorf("UserByEmail is case-sensitive: %v", err)
	}
	if got, err := s.UserByUsername("ADMIN"); err != nil || got.ID != u.ID {
		t.Errorf("UserByUsername is case-sensitive: %v", err)
	}
	if _, err := s.UserByEmail("nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user error = %v, want ErrNotFound", err)
	}
	if _, err := s.UserByID(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id error = %v, want ErrNotFound", err)
	}
}

func TestUserUniqueConstraints(t *testing.T) {
	s := newStore(t)
	mkUser(t, s, "a", "a@example.com")

	if err := s.CreateUser(&User{UUID: "x", Username: "a", Email: "other@example.com", Password: "h"}); err == nil {
		t.Error("duplicate username accepted")
	}
	if err := s.CreateUser(&User{UUID: "y", Username: "b", Email: "a@example.com", Password: "h"}); err == nil {
		t.Error("duplicate email accepted")
	}
}

func TestDeleteUserBlockedByServers(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	err := s.DeleteUser(f.user.ID)
	if err == nil {
		t.Fatal("deleted a user who still owns a server")
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("unhelpful error: %v", err)
	}

	// Removing the server unblocks the delete.
	if err := s.DeleteServer(f.server.ID); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if err := s.DeleteUser(f.user.ID); err != nil {
		t.Errorf("DeleteUser after server removal: %v", err)
	}
}

func TestUsersFilter(t *testing.T) {
	s := newStore(t)
	mkUser(t, s, "alice", "alice@example.com")
	mkUser(t, s, "bob", "bob@other.org")

	all, _ := s.Users("")
	if len(all) != 2 {
		t.Fatalf("Users(\"\") returned %d, want 2", len(all))
	}
	filtered, _ := s.Users("other.org")
	if len(filtered) != 1 || filtered[0].Username != "bob" {
		t.Errorf("filter by email domain returned %d rows", len(filtered))
	}
	if none, _ := s.Users("zzz-no-match"); len(none) != 0 {
		t.Errorf("non-matching filter returned %d rows", len(none))
	}
}

// ---- sessions ----

func TestSessionLifecycle(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "admin", "admin@example.com")

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if err := s.CreateSession(&Session{TokenHash: "hash1", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.SessionUser("hash1")
	if err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser: %v", err)
	}
	if _, err := s.SessionUser("unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown token error = %v, want ErrNotFound", err)
	}

	if err := s.DeleteSession("hash1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.SessionUser("hash1"); !errors.Is(err, ErrNotFound) {
		t.Error("session survived deletion")
	}
}

func TestExpiredSessionIsRejectedAndPruned(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "admin", "admin@example.com")
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	s.CreateSession(&Session{TokenHash: "stale", UserID: u.ID, ExpiresAt: past})

	if _, err := s.SessionUser("stale"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session authenticated a request")
	}
	if err := s.PruneSessions(); err != nil {
		t.Fatalf("PruneSessions: %v", err)
	}
}

func TestSessionsCascadeOnUserDelete(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "temp", "temp@example.com")
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	s.CreateSession(&Session{TokenHash: "h", UserID: u.ID, ExpiresAt: future})

	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := s.SessionUser("h"); !errors.Is(err, ErrNotFound) {
		t.Error("session outlived its user (foreign key cascade not enforced)")
	}
}

// ---- API keys & recovery tokens ----

func TestAPIKeysScopedByType(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "admin", "admin@example.com")

	client := &APIKey{UserID: u.ID, KeyType: KeyTypeAccount, Identifier: "ptlc_aaaaaaaaaaa", TokenHash: "h1", AllowedIPs: "[]"}
	app := &APIKey{UserID: u.ID, KeyType: KeyTypeApplication, Identifier: "ptla_bbbbbbbbbbb", TokenHash: "h2", AllowedIPs: "[]"}
	if err := s.CreateAPIKey(client); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAPIKey(app); err != nil {
		t.Fatal(err)
	}

	accountKeys, _ := s.APIKeysForUser(u.ID, KeyTypeAccount)
	if len(accountKeys) != 1 || accountKeys[0].Identifier != client.Identifier {
		t.Errorf("account key listing leaked other scopes: %d rows", len(accountKeys))
	}

	// Deleting with the wrong scope must not remove the key.
	if err := s.DeleteAPIKey(u.ID, client.Identifier, KeyTypeApplication); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.APIKeysForUser(u.ID, KeyTypeAccount); len(keys) != 1 {
		t.Error("key deleted under the wrong key type")
	}
	if err := s.DeleteAPIKey(u.ID, client.Identifier, KeyTypeAccount); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.APIKeysForUser(u.ID, KeyTypeAccount); len(keys) != 0 {
		t.Error("key not deleted")
	}
}

func TestRecoveryTokensAreSingleUse(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "admin", "admin@example.com")
	if err := s.ReplaceRecoveryTokens(u.ID, []string{"h1", "h2"}); err != nil {
		t.Fatal(err)
	}

	used, err := s.ConsumeRecoveryToken(u.ID, "h1")
	if err != nil || !used {
		t.Fatalf("first consume failed: used=%v err=%v", used, err)
	}
	used, _ = s.ConsumeRecoveryToken(u.ID, "h1")
	if used {
		t.Error("recovery token reusable")
	}
	used, _ = s.ConsumeRecoveryToken(u.ID, "nope")
	if used {
		t.Error("unknown recovery token accepted")
	}

	// Replacing wipes the old set.
	s.ReplaceRecoveryTokens(u.ID, []string{"fresh"})
	if used, _ := s.ConsumeRecoveryToken(u.ID, "h2"); used {
		t.Error("old recovery token survived a replacement")
	}
}

// ---- locations & nodes ----

func TestDeleteLocationBlockedByNodes(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	if err := s.DeleteLocation(f.loc.ID); err == nil {
		t.Error("deleted a location that still has nodes")
	}
}

func TestDeleteNodeBlockedByServers(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	if err := s.DeleteNode(f.node.ID); err == nil {
		t.Error("deleted a node that still hosts servers")
	}
	s.DeleteServer(f.server.ID)
	if err := s.DeleteNode(f.node.ID); err != nil {
		t.Errorf("DeleteNode after server removal: %v", err)
	}
}

func TestNodeByTokenID(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	got, err := s.NodeByTokenID("tokenid")
	if err != nil || got.ID != f.node.ID {
		t.Fatalf("NodeByTokenID: %v", err)
	}
	if _, err := s.NodeByTokenID("wrong"); !errors.Is(err, ErrNotFound) {
		t.Error("unknown daemon token id resolved to a node")
	}
}

func TestNodeUsageSumsServerResources(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	mem, disk, count, err := s.NodeUsage(f.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mem != 2048 || disk != 10240 || count != 1 {
		t.Errorf("NodeUsage = (%d, %d, %d), want (2048, 10240, 1)", mem, disk, count)
	}
}

// ---- allocations ----

func TestCreateAllocationsIgnoresDuplicates(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	// 25565/25566 already exist from the fixture.
	created, err := s.CreateAllocations(f.node.ID, "127.0.0.1", nil, []int{25566, 25567, 25568})
	if err != nil {
		t.Fatalf("CreateAllocations: %v", err)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2 (one duplicate skipped)", created)
	}
	all, _ := s.AllocationsForNode(f.node.ID)
	if len(all) != 4 {
		t.Errorf("node has %d allocations, want 4", len(all))
	}
}

func TestDeleteAssignedAllocationIsRejected(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	if err := s.DeleteAllocation(f.alloc.ID); err == nil {
		t.Error("deleted an allocation assigned to a server")
	}

	free, err := s.FreeAllocation(f.node.ID)
	if err != nil {
		t.Fatalf("FreeAllocation: %v", err)
	}
	if free.ID == f.alloc.ID {
		t.Fatal("FreeAllocation returned an assigned allocation")
	}
	if err := s.DeleteAllocation(free.ID); err != nil {
		t.Errorf("could not delete a free allocation: %v", err)
	}
}

func TestFreeAllocationExhausted(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	free, _ := s.FreeAllocation(f.node.ID)
	free.ServerID = &f.server.ID
	s.UpdateAllocation(free)

	if _, err := s.FreeAllocation(f.node.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("exhausted node returned err = %v, want ErrNotFound", err)
	}
}

func TestDeleteServerReleasesAllocations(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	if err := s.DeleteServer(f.server.ID); err != nil {
		t.Fatal(err)
	}
	al, err := s.AllocationByID(f.alloc.ID)
	if err != nil {
		t.Fatalf("allocation was deleted along with the server: %v", err)
	}
	if al.ServerID != nil {
		t.Error("allocation still assigned after the server was deleted")
	}
}

// ---- servers ----

func TestServerLookups(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	if got, err := s.ServerByIdentifier("abc12345"); err != nil || got.ID != f.server.ID {
		t.Errorf("ServerByIdentifier(short): %v", err)
	}
	if got, err := s.ServerByIdentifier("srv-uuid"); err != nil || got.ID != f.server.ID {
		t.Errorf("ServerByIdentifier(uuid): %v", err)
	}
	if _, err := s.ServerByIdentifier("nope"); !errors.Is(err, ErrNotFound) {
		t.Error("unknown identifier resolved")
	}
}

func TestServersForUserIncludesSubuserGrants(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	guest := mkUser(t, s, "guest", "guest@example.com")

	if servers, _ := s.ServersForUser(guest.ID); len(servers) != 0 {
		t.Fatalf("guest sees %d servers before being invited", len(servers))
	}
	if err := s.CreateSubuser(&Subuser{UserID: guest.ID, ServerID: f.server.ID, Permissions: `["control.console"]`}); err != nil {
		t.Fatal(err)
	}
	servers, _ := s.ServersForUser(guest.ID)
	if len(servers) != 1 || servers[0].ID != f.server.ID {
		t.Errorf("guest sees %d servers after invite, want 1", len(servers))
	}
	// The owner still sees exactly one (no duplicate row from the join).
	if owned, _ := s.ServersForUser(f.user.ID); len(owned) != 1 {
		t.Errorf("owner sees %d servers, want 1 (join duplicated rows?)", len(owned))
	}
}

func TestSubuserUniquePerServer(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	guest := mkUser(t, s, "guest", "guest@example.com")

	sub := &Subuser{UserID: guest.ID, ServerID: f.server.ID, Permissions: "[]"}
	if err := s.CreateSubuser(sub); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSubuser(&Subuser{UserID: guest.ID, ServerID: f.server.ID, Permissions: "[]"}); err == nil {
		t.Error("same user added twice to one server")
	}
	if err := s.DeleteSubuser(f.server.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subuser(f.server.ID, guest.ID); !errors.Is(err, ErrNotFound) {
		t.Error("subuser survived deletion")
	}
}

func TestServerVariablesUpsert(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	v := &EggVariable{EggID: f.egg.ID, Name: "Jar", EnvVariable: "SERVER_JARFILE", DefaultValue: "server.jar"}
	if err := s.CreateEggVariable(v); err != nil {
		t.Fatal(err)
	}

	if err := s.SetServerVariable(f.server.ID, v.ID, "paper.jar"); err != nil {
		t.Fatal(err)
	}
	// A second write must update, not fail on the unique constraint.
	if err := s.SetServerVariable(f.server.ID, v.ID, "custom.jar"); err != nil {
		t.Fatalf("second SetServerVariable: %v", err)
	}
	values, _ := s.ServerVariableValues(f.server.ID)
	if values[v.ID] != "custom.jar" {
		t.Errorf("variable = %q, want custom.jar", values[v.ID])
	}
}

func TestDeleteEggVariableCascadesToServerValues(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	v := &EggVariable{EggID: f.egg.ID, Name: "Jar", EnvVariable: "SERVER_JARFILE"}
	s.CreateEggVariable(v)
	s.SetServerVariable(f.server.ID, v.ID, "x.jar")

	if err := s.DeleteEggVariable(v.ID); err != nil {
		t.Fatal(err)
	}
	if values, _ := s.ServerVariableValues(f.server.ID); len(values) != 0 {
		t.Error("server variable value outlived its egg variable")
	}
}

// ---- eggs / nests ----

func TestDeleteEggAndNestBlockedByServers(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	if err := s.DeleteEgg(f.egg.ID); err == nil {
		t.Error("deleted an egg still used by a server")
	}
	if err := s.DeleteNest(f.nest.ID); err == nil {
		t.Error("deleted a nest still used by a server")
	}
}

func TestEggVariablesScopedToEgg(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	other := &Egg{UUID: "e2", NestID: f.nest.ID, Name: "Other", Features: "[]", DockerImages: "{}",
		FileDenylist: "[]", ConfigFiles: "{}", ConfigStartup: "{}", ConfigLogs: "{}"}
	s.CreateEgg(other)

	s.CreateEggVariable(&EggVariable{EggID: f.egg.ID, Name: "A", EnvVariable: "A"})
	s.CreateEggVariable(&EggVariable{EggID: other.ID, Name: "B", EnvVariable: "B"})

	vars, _ := s.EggVariables(f.egg.ID)
	if len(vars) != 1 || vars[0].EnvVariable != "A" {
		t.Errorf("egg variables leaked across eggs: %d rows", len(vars))
	}
}

// ---- databases ----

func TestServerDatabaseCounting(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	host := &DatabaseHost{Name: "h", Host: "127.0.0.1", Port: 3306, Username: "root", Password: "p"}
	if err := s.CreateDatabaseHost(host); err != nil {
		t.Fatal(err)
	}

	d := &ServerDatabase{ServerID: f.server.ID, DatabaseHostID: host.ID, Database: "s1_db", Username: "u1", Remote: "%", Password: "pw"}
	if err := s.CreateServerDatabase(d); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountDatabasesForServer(f.server.ID); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	if err := s.DeleteDatabaseHost(host.ID); err == nil {
		t.Error("deleted a database host that still holds databases")
	}
	s.DeleteServerDatabase(d.ID)
	if err := s.DeleteDatabaseHost(host.ID); err != nil {
		t.Errorf("DeleteDatabaseHost after cleanup: %v", err)
	}
}

// ---- schedules & backups ----

func TestScheduleTasksOrderedAndCascade(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	sc := &Schedule{ServerID: f.server.ID, Name: "nightly", CronMinute: "0", CronHour: "3",
		CronDayOfMonth: "*", CronMonth: "*", CronDayOfWeek: "*", IsActive: true}
	if err := s.CreateSchedule(sc); err != nil {
		t.Fatal(err)
	}
	s.CreateTask(&ScheduleTask{ScheduleID: sc.ID, SequenceID: 2, Action: "power", Payload: "restart"})
	s.CreateTask(&ScheduleTask{ScheduleID: sc.ID, SequenceID: 1, Action: "command", Payload: "say hi"})

	tasks, _ := s.TasksForSchedule(sc.ID)
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].SequenceID != 1 || tasks[1].SequenceID != 2 {
		t.Error("tasks are not ordered by sequence_id")
	}

	if err := s.DeleteSchedule(sc.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.TasksForSchedule(sc.ID); len(left) != 0 {
		t.Error("tasks outlived their schedule")
	}
}

func TestDueSchedulesRespectsStateAndTime(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

	due := &Schedule{ServerID: f.server.ID, Name: "due", IsActive: true, NextRunAt: &past}
	notYet := &Schedule{ServerID: f.server.ID, Name: "later", IsActive: true, NextRunAt: &future}
	inactive := &Schedule{ServerID: f.server.ID, Name: "off", IsActive: false, NextRunAt: &past}
	running := &Schedule{ServerID: f.server.ID, Name: "busy", IsActive: true, IsProcessing: true, NextRunAt: &past}
	for _, sc := range []*Schedule{due, notYet, inactive, running} {
		if err := s.CreateSchedule(sc); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.DueSchedules()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "due" {
		names := []string{}
		for _, g := range got {
			names = append(names, g.Name)
		}
		t.Errorf("DueSchedules returned %v, want [due]", names)
	}
}

func TestBackupsExcludeSoftDeleted(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	b := &Backup{ServerID: f.server.ID, UUID: "b1", Name: "backup", IgnoredFiles: "[]", Disk: "wings"}
	if err := s.CreateBackup(b); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountBackupsForServer(f.server.ID); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	b.DeletedAt = &ts
	if err := s.UpdateBackup(b); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.BackupsForServer(f.server.ID); len(list) != 0 {
		t.Error("soft-deleted backup still listed")
	}
	if n, _ := s.CountBackupsForServer(f.server.ID); n != 0 {
		t.Error("soft-deleted backup still counted against the limit")
	}
	// Still addressable by UUID so wings callbacks can find it.
	if _, err := s.BackupByUUID("b1"); err != nil {
		t.Errorf("BackupByUUID after soft delete: %v", err)
	}
}

// ---- activity & settings ----

func TestActivityLoggingAndSubjects(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	log := &ActivityLog{Event: "server:power.start", IP: "1.2.3.4", ActorID: &f.user.ID, Properties: `{"a":1}`}
	if err := s.LogActivity(log, [2]any{"server", f.server.ID}); err != nil {
		t.Fatal(err)
	}
	s.LogActivity(&ActivityLog{Event: "auth:fail", IP: "5.6.7.8", Properties: "{}"})

	byActor, _ := s.ActivityForActor(f.user.ID, 10)
	if len(byActor) != 1 || byActor[0].Event != "server:power.start" {
		t.Errorf("ActivityForActor returned %d rows", len(byActor))
	}
	bySubject, _ := s.ActivityForSubject("server", f.server.ID, 10)
	if len(bySubject) != 1 {
		t.Errorf("ActivityForSubject returned %d rows, want 1", len(bySubject))
	}
	if none, _ := s.ActivityForSubject("server", 9999, 10); len(none) != 0 {
		t.Error("activity leaked across subjects")
	}
}

func TestActivityNewestFirstAndLimited(t *testing.T) {
	s := newStore(t)
	u := mkUser(t, s, "admin", "admin@example.com")
	for i := 0; i < 5; i++ {
		s.LogActivity(&ActivityLog{Event: "e", IP: "1.1.1.1", ActorID: &u.ID, Properties: "{}"})
	}
	rows, _ := s.ActivityForActor(u.ID, 3)
	if len(rows) != 3 {
		t.Fatalf("limit ignored: %d rows", len(rows))
	}
	if rows[0].ID < rows[2].ID {
		t.Error("activity not ordered newest-first")
	}
}

func TestSettingsUpsertAndFallback(t *testing.T) {
	s := newStore(t)
	if got := s.Setting("missing", "fallback"); got != "fallback" {
		t.Errorf("Setting fallback = %q", got)
	}
	if err := s.SetSetting("app:name", "Roost"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("app:name", "Renamed"); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if got := s.Setting("app:name", ""); got != "Renamed" {
		t.Errorf("Setting = %q, want Renamed", got)
	}
	all, _ := s.Settings()
	if all["app:name"] != "Renamed" {
		t.Error("Settings() disagrees with Setting()")
	}
}

// ---- mounts ----

func TestMountServerAttachDetach(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	m := &Mount{UUID: "m1", Name: "maps", Source: "/host", Target: "/container"}
	if err := s.CreateMount(m); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMountServer(m.ID, f.server.ID, true); err != nil {
		t.Fatal(err)
	}
	// Attaching twice must be idempotent, not a primary-key error.
	if err := s.SetMountServer(m.ID, f.server.ID, true); err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	mounts, _ := s.MountsForServer(f.server.ID)
	if len(mounts) != 1 {
		t.Fatalf("server has %d mounts, want 1", len(mounts))
	}
	s.SetMountServer(m.ID, f.server.ID, false)
	if mounts, _ := s.MountsForServer(f.server.ID); len(mounts) != 0 {
		t.Error("mount still attached after detach")
	}
}

func TestMountNameUnique(t *testing.T) {
	s := newStore(t)
	s.CreateMount(&Mount{UUID: "m1", Name: "maps", Source: "/a", Target: "/b"})
	if err := s.CreateMount(&Mount{UUID: "m2", Name: "maps", Source: "/c", Target: "/d"}); err == nil {
		t.Error("duplicate mount name accepted")
	}
}

// ---- helpers ----

func TestPrefixCols(t *testing.T) {
	got := prefixCols("a, b,\n\tc", "s.")
	if got != "s.a, s.b, s.c" {
		t.Errorf("prefixCols = %q", got)
	}
}
