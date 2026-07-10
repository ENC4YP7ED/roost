// Package seed provisions first-boot data: the default nests and eggs
// (bundled from Pterodactyl's own egg exports) and the initial admin user.
package seed

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"strings"

	"roost/internal/api"
	"roost/internal/auth"
	"roost/internal/store"
)

//go:embed eggs
var eggFS embed.FS

// nestMeta maps the bundled directories to nest names/descriptions,
// mirroring Pterodactyl's NestSeeder.
var nestMeta = map[string][2]string{
	"minecraft":     {"Minecraft", "Minecraft — the classic game from Mojang. With support for Vanilla MC, Spigot, and many others!"},
	"rust":          {"Rust", "Rust — A game where you must fight to survive."},
	"source-engine": {"Source Engine", "Includes support for most Source Dedicated Server games."},
	"voice-servers": {"Voice Servers", "Voice servers such as Mumble and Teamspeak 3."},
}

// Run performs all first-boot provisioning. Idempotent.
func Run(a *api.API, s *store.Store) error {
	if err := seedEggs(a, s); err != nil {
		return fmt.Errorf("seed eggs: %w", err)
	}
	if err := seedAdmin(s); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	if err := seedLocalNode(s); err != nil {
		return fmt.Errorf("seed local node: %w", err)
	}
	return nil
}

// seedLocalNode provisions a location + node representing the machine the
// panel itself runs on, with a default allocation range. A fresh install is
// therefore immediately ready to create a server once wings is running here.
func seedLocalNode(s *store.Store) error {
	nodes, err := s.Nodes()
	if err != nil || len(nodes) > 0 {
		return err
	}

	locations, err := s.Locations()
	if err != nil {
		return err
	}
	var loc *store.Location
	if len(locations) > 0 {
		loc = locations[0]
	} else {
		loc = &store.Location{Short: "local", Long: "Panel host machine"}
		if err := s.CreateLocation(loc); err != nil {
			return err
		}
	}

	node := &store.Node{
		UUID:          auth.UUID(),
		Public:        true,
		Name:          "local",
		Description:   "Wings daemon on the same machine as the panel.",
		LocationID:    loc.ID,
		FQDN:          "127.0.0.1",
		Scheme:        "http", // loopback: TLS would need a cert for 127.0.0.1
		Memory:        8192,
		Disk:          51200,
		UploadSize:    100,
		DaemonTokenID: auth.RandomAlnum(16),
		DaemonToken:   auth.RandomAlnum(64),
		DaemonListen:  8080,
		DaemonSFTP:    2022,
		DaemonBase:    "/var/lib/pterodactyl/volumes",
	}
	if err := s.CreateNode(node); err != nil {
		return err
	}

	ports := make([]int, 0, 16)
	for p := 25565; p <= 25580; p++ {
		ports = append(ports, p)
	}
	created, err := s.CreateAllocations(node.ID, "127.0.0.1", nil, ports)
	if err != nil {
		return err
	}
	log.Printf("seeded default location %q and node %q (127.0.0.1) with %d allocations", loc.Short, node.Name, created)
	log.Printf("  → configure wings on this machine: Admin → Nodes → local → Wings config")
	return nil
}

func seedEggs(a *api.API, s *store.Store) error {
	nests, err := s.Nests()
	if err != nil {
		return err
	}
	if len(nests) > 0 {
		return nil
	}
	dirs, err := eggFS.ReadDir("eggs")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		meta, ok := nestMeta[dir.Name()]
		if !ok {
			meta = [2]string{strings.Title(dir.Name()), ""}
		}
		nest := &store.Nest{
			UUID: auth.UUID(), Author: "support@pterodactyl.io",
			Name: meta[0], Description: meta[1],
		}
		if err := s.CreateNest(nest); err != nil {
			return err
		}
		files, err := fs.ReadDir(eggFS, "eggs/"+dir.Name())
		if err != nil {
			return err
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			raw, err := eggFS.ReadFile("eggs/" + dir.Name() + "/" + f.Name())
			if err != nil {
				return err
			}
			var doc api.EggDocument
			if err := json.Unmarshal(raw, &doc); err != nil {
				log.Printf("seed: skipping %s: %v", f.Name(), err)
				continue
			}
			if _, err := a.ImportEgg(nest.ID, &doc); err != nil {
				log.Printf("seed: failed to import %s: %v", f.Name(), err)
			}
		}
	}
	log.Printf("seeded default nests and eggs")
	return nil
}

func seedAdmin(s *store.Store) error {
	count, err := s.CountUsers()
	if err != nil || count > 0 {
		return err
	}
	password := auth.RandomAlnum(16)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	admin := &store.User{
		UUID: auth.UUID(), Username: "admin", Email: "admin@example.com",
		NameFirst: "Admin", NameLast: "User", Password: hash,
		Language: "en", RootAdmin: true,
	}
	if err := s.CreateUser(admin); err != nil {
		return err
	}
	log.Printf("┌──────────────────────────────────────────────────┐")
	log.Printf("│  first boot: created administrator account       │")
	log.Printf("│    email:    admin@example.com                   │")
	log.Printf("│    password: %s                    │", password)
	log.Printf("│  change these after logging in!                  │")
	log.Printf("└──────────────────────────────────────────────────┘")
	return nil
}
