package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ---- locations ----

func scanLocation(row interface{ Scan(...any) error }) (*Location, error) {
	l := &Location{}
	err := row.Scan(&l.ID, &l.Short, &l.Long, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (s *Store) Locations() ([]*Location, error) {
	rows, err := s.db.Query(`SELECT id, short, long, created_at, updated_at FROM locations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Location
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) LocationByID(id int64) (*Location, error) {
	return scanLocation(s.db.QueryRow(`SELECT id, short, long, created_at, updated_at FROM locations WHERE id = ?`, id))
}

func (s *Store) CreateLocation(l *Location) error {
	ts := now()
	l.CreatedAt, l.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO locations (short, long, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		l.Short, l.Long, ts, ts)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateLocation(l *Location) error {
	l.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE locations SET short=?, long=?, updated_at=? WHERE id=?`,
		l.Short, l.Long, l.UpdatedAt, l.ID)
	return err
}

func (s *Store) DeleteLocation(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE location_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete a location that has %d node(s) assigned to it", n)
	}
	_, err := s.db.Exec(`DELETE FROM locations WHERE id = ?`, id)
	return err
}

// ---- nodes ----

const nodeCols = `id, uuid, public, name, description, location_id, fqdn, scheme, behind_proxy,
	maintenance_mode, memory, memory_overallocate, disk, disk_overallocate, upload_size,
	daemon_token_id, daemon_token, daemon_listen, daemon_sftp, daemon_base, created_at, updated_at`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	n := &Node{}
	err := row.Scan(&n.ID, &n.UUID, &n.Public, &n.Name, &n.Description, &n.LocationID, &n.FQDN,
		&n.Scheme, &n.BehindProxy, &n.MaintenanceMode, &n.Memory, &n.MemoryOverallocate, &n.Disk,
		&n.DiskOverallocate, &n.UploadSize, &n.DaemonTokenID, &n.DaemonToken, &n.DaemonListen,
		&n.DaemonSFTP, &n.DaemonBase, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func (s *Store) Nodes() ([]*Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) NodeByID(id int64) (*Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id))
}

func (s *Store) NodeByTokenID(tokenID string) (*Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE daemon_token_id = ?`, tokenID))
}

func (s *Store) CreateNode(n *Node) error {
	ts := now()
	n.CreatedAt, n.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO nodes (uuid, public, name, description, location_id, fqdn,
		scheme, behind_proxy, maintenance_mode, memory, memory_overallocate, disk,
		disk_overallocate, upload_size, daemon_token_id, daemon_token, daemon_listen, daemon_sftp,
		daemon_base, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.UUID, n.Public, n.Name, n.Description, n.LocationID, n.FQDN, n.Scheme, n.BehindProxy,
		n.MaintenanceMode, n.Memory, n.MemoryOverallocate, n.Disk, n.DiskOverallocate,
		n.UploadSize, n.DaemonTokenID, n.DaemonToken, n.DaemonListen, n.DaemonSFTP, n.DaemonBase,
		ts, ts)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateNode(n *Node) error {
	n.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE nodes SET public=?, name=?, description=?, location_id=?, fqdn=?,
		scheme=?, behind_proxy=?, maintenance_mode=?, memory=?, memory_overallocate=?, disk=?,
		disk_overallocate=?, upload_size=?, daemon_listen=?, daemon_sftp=?, daemon_base=?,
		updated_at=? WHERE id=?`,
		n.Public, n.Name, n.Description, n.LocationID, n.FQDN, n.Scheme, n.BehindProxy,
		n.MaintenanceMode, n.Memory, n.MemoryOverallocate, n.Disk, n.DiskOverallocate,
		n.UploadSize, n.DaemonListen, n.DaemonSFTP, n.DaemonBase, n.UpdatedAt, n.ID)
	return err
}

func (s *Store) DeleteNode(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE node_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete a node with %d server(s) on it", n)
	}
	_, err := s.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// NodeUsage returns allocated memory/disk sums across a node's servers.
func (s *Store) NodeUsage(nodeID int64) (mem, disk, count int64, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(SUM(memory),0), COALESCE(SUM(disk),0), COUNT(*)
		FROM servers WHERE node_id = ?`, nodeID).Scan(&mem, &disk, &count)
	return
}

// ---- allocations ----

const allocCols = `id, node_id, ip, ip_alias, port, server_id, notes`

func scanAlloc(row interface{ Scan(...any) error }) (*Allocation, error) {
	a := &Allocation{}
	err := row.Scan(&a.ID, &a.NodeID, &a.IP, &a.IPAlias, &a.Port, &a.ServerID, &a.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) AllocationsForNode(nodeID int64) ([]*Allocation, error) {
	return s.allocQuery(`SELECT `+allocCols+` FROM allocations WHERE node_id = ? ORDER BY ip, port`, nodeID)
}

func (s *Store) AllocationsForServer(serverID int64) ([]*Allocation, error) {
	return s.allocQuery(`SELECT `+allocCols+` FROM allocations WHERE server_id = ? ORDER BY id`, serverID)
}

func (s *Store) allocQuery(q string, args ...any) ([]*Allocation, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Allocation
	for rows.Next() {
		a, err := scanAlloc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AllocationByID(id int64) (*Allocation, error) {
	return scanAlloc(s.db.QueryRow(`SELECT `+allocCols+` FROM allocations WHERE id = ?`, id))
}

// CreateAllocations inserts ip/port pairs, ignoring duplicates. Returns how
// many were actually created.
func (s *Store) CreateAllocations(nodeID int64, ip string, alias *string, ports []int) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	created := 0
	for _, p := range ports {
		res, err := tx.Exec(`INSERT OR IGNORE INTO allocations (node_id, ip, ip_alias, port) VALUES (?, ?, ?, ?)`,
			nodeID, ip, alias, p)
		if err != nil {
			return created, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created++
		}
	}
	return created, tx.Commit()
}

func (s *Store) UpdateAllocation(a *Allocation) error {
	_, err := s.db.Exec(`UPDATE allocations SET ip_alias=?, server_id=?, notes=? WHERE id=?`,
		a.IPAlias, a.ServerID, a.Notes, a.ID)
	return err
}

func (s *Store) DeleteAllocation(id int64) error {
	a, err := s.AllocationByID(id)
	if err != nil {
		return err
	}
	if a.ServerID != nil {
		return errors.New("cannot delete an allocation that is assigned to a server")
	}
	_, err = s.db.Exec(`DELETE FROM allocations WHERE id = ?`, id)
	return err
}

// FreeAllocation finds an unassigned allocation on a node.
func (s *Store) FreeAllocation(nodeID int64) (*Allocation, error) {
	return scanAlloc(s.db.QueryRow(`SELECT `+allocCols+` FROM allocations
		WHERE node_id = ? AND server_id IS NULL ORDER BY id LIMIT 1`, nodeID))
}

// ---- database hosts ----

const dbHostCols = `id, name, host, port, username, password, max_databases, node_id, created_at, updated_at`

func scanDBHost(row interface{ Scan(...any) error }) (*DatabaseHost, error) {
	h := &DatabaseHost{}
	err := row.Scan(&h.ID, &h.Name, &h.Host, &h.Port, &h.Username, &h.Password, &h.MaxDatabases,
		&h.NodeID, &h.CreatedAt, &h.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return h, err
}

func (s *Store) DatabaseHosts() ([]*DatabaseHost, error) {
	rows, err := s.db.Query(`SELECT ` + dbHostCols + ` FROM database_hosts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DatabaseHost
	for rows.Next() {
		h, err := scanDBHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) DatabaseHostByID(id int64) (*DatabaseHost, error) {
	return scanDBHost(s.db.QueryRow(`SELECT `+dbHostCols+` FROM database_hosts WHERE id = ?`, id))
}

func (s *Store) CreateDatabaseHost(h *DatabaseHost) error {
	ts := now()
	h.CreatedAt, h.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO database_hosts (name, host, port, username, password,
		max_databases, node_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		h.Name, h.Host, h.Port, h.Username, h.Password, h.MaxDatabases, h.NodeID, ts, ts)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateDatabaseHost(h *DatabaseHost) error {
	h.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE database_hosts SET name=?, host=?, port=?, username=?, password=?,
		max_databases=?, node_id=?, updated_at=? WHERE id=?`,
		h.Name, h.Host, h.Port, h.Username, h.Password, h.MaxDatabases, h.NodeID, h.UpdatedAt, h.ID)
	return err
}

func (s *Store) DeleteDatabaseHost(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM server_databases WHERE database_host_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete a database host with %d database(s) on it", n)
	}
	_, err := s.db.Exec(`DELETE FROM database_hosts WHERE id = ?`, id)
	return err
}

// ---- mounts ----

func (s *Store) Mounts() ([]*Mount, error) {
	rows, err := s.db.Query(`SELECT id, uuid, name, description, source, target, read_only, user_mountable FROM mounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Mount
	for rows.Next() {
		m := &Mount{}
		if err := rows.Scan(&m.ID, &m.UUID, &m.Name, &m.Description, &m.Source, &m.Target, &m.ReadOnly, &m.UserMountable); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MountByID(id int64) (*Mount, error) {
	m := &Mount{}
	err := s.db.QueryRow(`SELECT id, uuid, name, description, source, target, read_only, user_mountable
		FROM mounts WHERE id = ?`, id).
		Scan(&m.ID, &m.UUID, &m.Name, &m.Description, &m.Source, &m.Target, &m.ReadOnly, &m.UserMountable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) CreateMount(m *Mount) error {
	res, err := s.db.Exec(`INSERT INTO mounts (uuid, name, description, source, target, read_only, user_mountable)
		VALUES (?,?,?,?,?,?,?)`, m.UUID, m.Name, m.Description, m.Source, m.Target, m.ReadOnly, m.UserMountable)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateMount(m *Mount) error {
	_, err := s.db.Exec(`UPDATE mounts SET name=?, description=?, source=?, target=?, read_only=?, user_mountable=? WHERE id=?`,
		m.Name, m.Description, m.Source, m.Target, m.ReadOnly, m.UserMountable, m.ID)
	return err
}

func (s *Store) DeleteMount(id int64) error {
	_, err := s.db.Exec(`DELETE FROM mounts WHERE id = ?`, id)
	return err
}

// MountsForServer resolves mounts attached directly to a server.
func (s *Store) MountsForServer(serverID int64) ([]*Mount, error) {
	rows, err := s.db.Query(`SELECT m.id, m.uuid, m.name, m.description, m.source, m.target, m.read_only, m.user_mountable
		FROM mounts m JOIN mount_server ms ON ms.mount_id = m.id WHERE ms.server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Mount
	for rows.Next() {
		m := &Mount{}
		if err := rows.Scan(&m.ID, &m.UUID, &m.Name, &m.Description, &m.Source, &m.Target, &m.ReadOnly, &m.UserMountable); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SetMountServer(mountID, serverID int64, attach bool) error {
	var err error
	if attach {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO mount_server (mount_id, server_id) VALUES (?, ?)`, mountID, serverID)
	} else {
		_, err = s.db.Exec(`DELETE FROM mount_server WHERE mount_id = ? AND server_id = ?`, mountID, serverID)
	}
	return err
}

// ResetNodeToken persists regenerated daemon credentials.
func (s *Store) ResetNodeToken(n *Node) error {
	n.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE nodes SET daemon_token_id=?, daemon_token=?, updated_at=? WHERE id=?`,
		n.DaemonTokenID, n.DaemonToken, n.UpdatedAt, n.ID)
	return err
}
