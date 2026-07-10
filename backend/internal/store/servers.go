package store

import (
	"database/sql"
	"errors"
	"strings"
)

const serverCols = `id, external_id, uuid, uuid_short, node_id, name, description, status,
	skip_scripts, owner_id, memory, swap, disk, io, cpu, threads, oom_disabled, allocation_id,
	nest_id, egg_id, startup, image, allocation_limit, database_limit, backup_limit,
	installed_at, created_at, updated_at`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	v := &Server{}
	err := row.Scan(&v.ID, &v.ExternalID, &v.UUID, &v.UUIDShort, &v.NodeID, &v.Name,
		&v.Description, &v.Status, &v.SkipScripts, &v.OwnerID, &v.Memory, &v.Swap, &v.Disk,
		&v.IO, &v.CPU, &v.Threads, &v.OOMDisabled, &v.AllocationID, &v.NestID, &v.EggID,
		&v.Startup, &v.Image, &v.AllocationLimit, &v.DatabaseLimit, &v.BackupLimit,
		&v.InstalledAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) serverQuery(q string, args ...any) ([]*Server, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Server
	for rows.Next() {
		v, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Servers() ([]*Server, error) {
	return s.serverQuery(`SELECT ` + serverCols + ` FROM servers ORDER BY id`)
}

func (s *Store) ServersForNode(nodeID int64) ([]*Server, error) {
	return s.serverQuery(`SELECT `+serverCols+` FROM servers WHERE node_id = ? ORDER BY id`, nodeID)
}

// ServersForUser returns servers the user owns plus servers they are a
// subuser on.
func (s *Store) ServersForUser(userID int64) ([]*Server, error) {
	return s.serverQuery(`SELECT DISTINCT `+prefixCols(serverCols, "s.")+` FROM servers s
		LEFT JOIN subusers su ON su.server_id = s.id
		WHERE s.owner_id = ? OR su.user_id = ? ORDER BY s.id`, userID, userID)
}

func (s *Store) ServersOwnedBy(userID int64) ([]*Server, error) {
	return s.serverQuery(`SELECT `+serverCols+` FROM servers WHERE owner_id = ? ORDER BY id`, userID)
}

func (s *Store) ServerByID(id int64) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
}

func (s *Store) ServerByUUID(uuid string) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE uuid = ?`, uuid))
}

func (s *Store) ServerByExternalID(ext string) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE external_id = ?`, ext))
}

// ServerByIdentifier accepts the short uuid or the full uuid, mirroring
// Pterodactyl's client API route binding.
func (s *Store) ServerByIdentifier(ident string) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers
		WHERE uuid_short = ? OR uuid = ?`, ident, ident))
}

func (s *Store) CreateServer(v *Server) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO servers (external_id, uuid, uuid_short, node_id, name,
		description, status, skip_scripts, owner_id, memory, swap, disk, io, cpu, threads,
		oom_disabled, allocation_id, nest_id, egg_id, startup, image, allocation_limit,
		database_limit, backup_limit, installed_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ExternalID, v.UUID, v.UUIDShort, v.NodeID, v.Name, v.Description, v.Status,
		v.SkipScripts, v.OwnerID, v.Memory, v.Swap, v.Disk, v.IO, v.CPU, v.Threads,
		v.OOMDisabled, v.AllocationID, v.NestID, v.EggID, v.Startup, v.Image,
		v.AllocationLimit, v.DatabaseLimit, v.BackupLimit, v.InstalledAt, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateServer(v *Server) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE servers SET external_id=?, node_id=?, name=?, description=?,
		status=?, skip_scripts=?, owner_id=?, memory=?, swap=?, disk=?, io=?, cpu=?, threads=?,
		oom_disabled=?, allocation_id=?, nest_id=?, egg_id=?, startup=?, image=?,
		allocation_limit=?, database_limit=?, backup_limit=?, installed_at=?, updated_at=?
		WHERE id=?`,
		v.ExternalID, v.NodeID, v.Name, v.Description, v.Status, v.SkipScripts, v.OwnerID,
		v.Memory, v.Swap, v.Disk, v.IO, v.CPU, v.Threads, v.OOMDisabled, v.AllocationID,
		v.NestID, v.EggID, v.Startup, v.Image, v.AllocationLimit, v.DatabaseLimit,
		v.BackupLimit, v.InstalledAt, v.UpdatedAt, v.ID)
	return err
}

func (s *Store) DeleteServer(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE allocations SET server_id = NULL, notes = NULL WHERE server_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM servers WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CountServers() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&n)
	return n, err
}

// ---- server variables ----

// ServerVariableValues returns variable_id -> value for a server.
func (s *Store) ServerVariableValues(serverID int64) (map[int64]string, error) {
	rows, err := s.db.Query(`SELECT variable_id, variable_value FROM server_variables WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var val string
		if err := rows.Scan(&id, &val); err != nil {
			return nil, err
		}
		out[id] = val
	}
	return out, rows.Err()
}

func (s *Store) SetServerVariable(serverID, variableID int64, value string) error {
	_, err := s.db.Exec(`INSERT INTO server_variables (server_id, variable_id, variable_value)
		VALUES (?, ?, ?) ON CONFLICT (server_id, variable_id) DO UPDATE SET variable_value = excluded.variable_value`,
		serverID, variableID, value)
	return err
}

// ---- subusers ----

func (s *Store) SubusersForServer(serverID int64) ([]*Subuser, error) {
	rows, err := s.db.Query(`SELECT id, user_id, server_id, permissions, created_at, updated_at
		FROM subusers WHERE server_id = ? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subuser
	for rows.Next() {
		u := &Subuser{}
		if err := rows.Scan(&u.ID, &u.UserID, &u.ServerID, &u.Permissions, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Subuser(serverID, userID int64) (*Subuser, error) {
	u := &Subuser{}
	err := s.db.QueryRow(`SELECT id, user_id, server_id, permissions, created_at, updated_at
		FROM subusers WHERE server_id = ? AND user_id = ?`, serverID, userID).
		Scan(&u.ID, &u.UserID, &u.ServerID, &u.Permissions, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) CreateSubuser(u *Subuser) error {
	ts := now()
	u.CreatedAt, u.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO subusers (user_id, server_id, permissions, created_at, updated_at)
		VALUES (?,?,?,?,?)`, u.UserID, u.ServerID, u.Permissions, ts, ts)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateSubuser(u *Subuser) error {
	u.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE subusers SET permissions=?, updated_at=? WHERE id=?`, u.Permissions, u.UpdatedAt, u.ID)
	return err
}

func (s *Store) DeleteSubuser(serverID, userID int64) error {
	_, err := s.db.Exec(`DELETE FROM subusers WHERE server_id = ? AND user_id = ?`, serverID, userID)
	return err
}

// ---- server databases ----

const srvDBCols = `id, server_id, database_host_id, database, username, remote, password,
	max_connections, created_at, updated_at`

func scanServerDB(row interface{ Scan(...any) error }) (*ServerDatabase, error) {
	d := &ServerDatabase{}
	err := row.Scan(&d.ID, &d.ServerID, &d.DatabaseHostID, &d.Database, &d.Username, &d.Remote,
		&d.Password, &d.MaxConnections, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) DatabasesForServer(serverID int64) ([]*ServerDatabase, error) {
	rows, err := s.db.Query(`SELECT `+srvDBCols+` FROM server_databases WHERE server_id = ? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServerDatabase
	for rows.Next() {
		d, err := scanServerDB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ServerDatabaseByID(id int64) (*ServerDatabase, error) {
	return scanServerDB(s.db.QueryRow(`SELECT `+srvDBCols+` FROM server_databases WHERE id = ?`, id))
}

func (s *Store) CreateServerDatabase(d *ServerDatabase) error {
	ts := now()
	d.CreatedAt, d.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO server_databases (server_id, database_host_id, database,
		username, remote, password, max_connections, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ServerID, d.DatabaseHostID, d.Database, d.Username, d.Remote, d.Password,
		d.MaxConnections, ts, ts)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateServerDatabase(d *ServerDatabase) error {
	d.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE server_databases SET password=?, remote=?, max_connections=?, updated_at=? WHERE id=?`,
		d.Password, d.Remote, d.MaxConnections, d.UpdatedAt, d.ID)
	return err
}

func (s *Store) DeleteServerDatabase(id int64) error {
	_, err := s.db.Exec(`DELETE FROM server_databases WHERE id = ?`, id)
	return err
}

func (s *Store) CountDatabasesForServer(serverID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM server_databases WHERE server_id = ?`, serverID).Scan(&n)
	return n, err
}

// prefixCols rewrites a comma-separated column list to be table-qualified.
func prefixCols(cols, prefix string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
