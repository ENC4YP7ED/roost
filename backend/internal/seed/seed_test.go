package seed

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"roost/internal/api"
	"roost/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The bundled egg exports must stay parseable — a malformed one would only
// surface on a user's first boot otherwise.
func TestEmbeddedEggsAreValid(t *testing.T) {
	count := 0
	err := fs.WalkDir(eggFS, "eggs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		count++
		raw, err := eggFS.ReadFile(path)
		if err != nil {
			return err
		}
		var doc api.EggDocument
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Errorf("%s: not a valid egg export: %v", path, err)
			return nil
		}
		if doc.Name == "" {
			t.Errorf("%s: egg has no name", path)
		}
		if doc.Startup == "" {
			t.Errorf("%s: egg has no startup command", path)
		}
		if len(doc.DockerImages) == 0 && doc.Image == "" && len(doc.Images) == 0 {
			t.Errorf("%s: egg declares no docker image", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count < 10 {
		t.Errorf("found only %d bundled eggs, expected the full default set", count)
	}
}

func TestNestMetadataCoversEveryDirectory(t *testing.T) {
	dirs, err := fs.ReadDir(eggFS, "eggs")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if _, ok := nestMeta[d.Name()]; !ok {
			t.Errorf("egg directory %q has no nest metadata; it would be named by fallback", d.Name())
		}
	}
}

func TestRunSeedsEverythingOnce(t *testing.T) {
	s := newStore(t)
	a := api.New(s)

	if err := Run(a, s); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Nests + eggs.
	nests, _ := s.Nests()
	if len(nests) != len(nestMeta) {
		t.Errorf("seeded %d nests, want %d", len(nests), len(nestMeta))
	}
	totalEggs := 0
	for _, n := range nests {
		eggs, _ := s.EggsForNest(n.ID)
		totalEggs += len(eggs)
		for _, e := range eggs {
			// Every egg must carry usable configuration JSON.
			var v any
			if err := json.Unmarshal([]byte(e.ConfigFiles), &v); err != nil {
				t.Errorf("egg %q has invalid config_files: %v", e.Name, err)
			}
			if err := json.Unmarshal([]byte(e.DockerImages), &v); err != nil {
				t.Errorf("egg %q has invalid docker_images: %v", e.Name, err)
			}
			if vars, _ := s.EggVariables(e.ID); len(vars) == 0 && e.Name != "Custom Source Engine Game" {
				t.Logf("note: egg %q has no variables", e.Name)
			}
		}
	}
	if totalEggs < 10 {
		t.Errorf("seeded %d eggs, want the full default set", totalEggs)
	}

	// Administrator.
	users, _ := s.Users("")
	if len(users) != 1 {
		t.Fatalf("seeded %d users, want 1", len(users))
	}
	if !users[0].RootAdmin {
		t.Error("the seeded user is not an administrator")
	}
	if users[0].Password == "" || len(users[0].Password) < 20 {
		t.Error("the seeded admin password is not a bcrypt hash")
	}

	// Default location + node + allocations on the panel host.
	locations, _ := s.Locations()
	if len(locations) != 1 || locations[0].Short != "local" {
		t.Fatalf("locations = %v", locations)
	}
	nodes, _ := s.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("seeded %d nodes, want 1", len(nodes))
	}
	node := nodes[0]
	if node.FQDN != "127.0.0.1" {
		t.Errorf("default node fqdn = %q, want the loopback address", node.FQDN)
	}
	if node.Scheme != "http" {
		t.Errorf("default node scheme = %q; TLS on loopback would need a certificate", node.Scheme)
	}
	if node.DaemonToken == "" || node.DaemonTokenID == "" {
		t.Error("default node has no daemon credentials")
	}
	if len(node.DaemonToken) < 32 {
		t.Error("default node daemon token is too short to be secure")
	}
	allocs, _ := s.AllocationsForNode(node.ID)
	if len(allocs) != 16 {
		t.Errorf("default node has %d allocations, want 16", len(allocs))
	}
	free, err := s.FreeAllocation(node.ID)
	if err != nil {
		t.Fatalf("no free allocation on a freshly seeded node: %v", err)
	}
	if free.Port != 25565 {
		t.Errorf("first free port = %d, want 25565", free.Port)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	s := newStore(t)
	a := api.New(s)
	if err := Run(a, s); err != nil {
		t.Fatal(err)
	}
	nestsBefore, _ := s.Nests()
	usersBefore, _ := s.Users("")
	nodesBefore, _ := s.Nodes()

	// A second boot must not duplicate anything or reset the admin password.
	if err := Run(a, s); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	nestsAfter, _ := s.Nests()
	usersAfter, _ := s.Users("")
	nodesAfter, _ := s.Nodes()

	if len(nestsAfter) != len(nestsBefore) {
		t.Errorf("nests grew from %d to %d on reboot", len(nestsBefore), len(nestsAfter))
	}
	if len(usersAfter) != len(usersBefore) {
		t.Errorf("users grew from %d to %d on reboot", len(usersBefore), len(usersAfter))
	}
	if len(nodesAfter) != len(nodesBefore) {
		t.Errorf("nodes grew from %d to %d on reboot", len(nodesBefore), len(nodesAfter))
	}
	if usersAfter[0].Password != usersBefore[0].Password {
		t.Error("the admin password was regenerated on reboot")
	}

	allocs, _ := s.AllocationsForNode(nodesAfter[0].ID)
	if len(allocs) != 16 {
		t.Errorf("allocations duplicated on reboot: %d", len(allocs))
	}
}

// An operator who already configured a node must not get the local one.
func TestSeedLocalNodeSkippedWhenNodesExist(t *testing.T) {
	s := newStore(t)
	loc := &store.Location{Short: "eu"}
	s.CreateLocation(loc)
	s.CreateNode(&store.Node{UUID: "u", Name: "existing", LocationID: loc.ID, FQDN: "10.0.0.1",
		Scheme: "https", DaemonTokenID: "t", DaemonToken: "s", DaemonListen: 8080, DaemonSFTP: 2022})

	if err := seedLocalNode(s); err != nil {
		t.Fatalf("seedLocalNode: %v", err)
	}
	nodes, _ := s.Nodes()
	if len(nodes) != 1 || nodes[0].Name != "existing" {
		t.Errorf("nodes = %d, the local node was added despite an existing one", len(nodes))
	}
}

// If a location already exists, reuse it rather than making a second one.
func TestSeedLocalNodeReusesExistingLocation(t *testing.T) {
	s := newStore(t)
	s.CreateLocation(&store.Location{Short: "eu", Long: "Europe"})

	if err := seedLocalNode(s); err != nil {
		t.Fatal(err)
	}
	locations, _ := s.Locations()
	if len(locations) != 1 {
		t.Errorf("locations = %d, want the existing one reused", len(locations))
	}
	nodes, _ := s.Nodes()
	if len(nodes) != 1 || nodes[0].LocationID != locations[0].ID {
		t.Error("the seeded node is not attached to the existing location")
	}
}

func TestSeedAdminSkippedWhenUsersExist(t *testing.T) {
	s := newStore(t)
	s.CreateUser(&store.User{UUID: "u", Username: "someone", Email: "s@e.com", Password: "hash"})
	if err := seedAdmin(s); err != nil {
		t.Fatal(err)
	}
	users, _ := s.Users("")
	if len(users) != 1 {
		t.Errorf("users = %d, an extra admin was created", len(users))
	}
}
