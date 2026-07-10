package api

import (
	"errors"

	"roost/internal/auth"
	"roost/internal/store"
	"roost/internal/wings"
)

// ProvisionSpec describes a server to create. It is the shared core behind
// both the admin "create server" endpoint and automatic provisioning from a
// paid order.
type ProvisionSpec struct {
	Name        string
	Description string
	OwnerID     int64
	EggID       int64
	NodeID      int64 // 0 = auto-pick a node with a free allocation
	DockerImage string
	Startup     string
	Environment map[string]string
	SkipScripts bool
	OOMDisabled bool

	Memory  int64
	Swap    int64
	Disk    int64
	IO      int64
	CPU     int64
	Threads *string

	Databases   int64
	Allocations int64
	Backups     int64

	StartOnCompletion bool
}

// errNoAllocation signals that no node has a free allocation for a server.
var errNoAllocation = errors.New("no free allocation is available")

// provisionServer creates a server per spec, assigns a free allocation, stores
// its egg variables, and asks wings to install it. It is transactional at the
// application level: on any early failure nothing is left half-created.
func (a *API) provisionServer(spec ProvisionSpec) (*store.Server, error) {
	egg, err := a.Store.EggByID(spec.EggID)
	if err != nil {
		return nil, errors.New("the requested egg does not exist")
	}
	if _, err := a.Store.UserByID(spec.OwnerID); err != nil {
		return nil, errors.New("the requested owner does not exist")
	}

	// Find a free allocation, on the requested node or the first node that has
	// one when auto-picking.
	alloc, err := a.pickAllocation(spec.NodeID)
	if err != nil {
		return nil, err
	}

	if spec.DockerImage == "" {
		for _, v := range jsonObj(egg.DockerImages) {
			if s, ok := v.(string); ok {
				spec.DockerImage = s
				break
			}
		}
	}
	if spec.Startup == "" {
		spec.Startup = egg.Startup
	}

	status := "installing"
	srv := &store.Server{
		UUID:      auth.UUID(),
		UUIDShort: auth.RandomHex(4),
		NodeID:    alloc.NodeID,
		Name:      spec.Name, Description: spec.Description,
		Status: &status, SkipScripts: spec.SkipScripts, OwnerID: spec.OwnerID,
		Memory: spec.Memory, Swap: spec.Swap, Disk: spec.Disk, IO: spec.IO, CPU: spec.CPU,
		Threads: spec.Threads, OOMDisabled: spec.OOMDisabled,
		NestID: egg.NestID, EggID: egg.ID, Startup: spec.Startup, Image: spec.DockerImage,
		DatabaseLimit: spec.Databases, AllocationLimit: spec.Allocations, BackupLimit: spec.Backups,
	}
	if err := a.Store.CreateServer(srv); err != nil {
		return nil, err
	}

	alloc.ServerID = &srv.ID
	a.Store.UpdateAllocation(alloc)
	srv.AllocationID = &alloc.ID
	a.Store.UpdateServer(srv)

	vars, _ := a.Store.EggVariables(egg.ID)
	for _, v := range vars {
		val := v.DefaultValue
		if got, ok := spec.Environment[v.EnvVariable]; ok {
			val = got
		}
		a.Store.SetServerVariable(srv.ID, v.ID, val)
	}

	if node, err := a.Store.NodeByID(srv.NodeID); err == nil {
		go wings.New(node).CreateServer(srv.UUID, spec.StartOnCompletion)
	}
	return srv, nil
}

// pickAllocation returns a free allocation. With nodeID > 0 it is restricted to
// that node; otherwise it scans nodes in order for the first available one.
func (a *API) pickAllocation(nodeID int64) (*store.Allocation, error) {
	if nodeID > 0 {
		alloc, err := a.Store.FreeAllocation(nodeID)
		if err != nil {
			return nil, errNoAllocation
		}
		return alloc, nil
	}
	nodes, err := a.Store.Nodes()
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.MaintenanceMode {
			continue
		}
		if alloc, err := a.Store.FreeAllocation(n.ID); err == nil {
			return alloc, nil
		}
	}
	return nil, errNoAllocation
}
