package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ---- nests ----

func (s *Store) Nests() ([]*Nest, error) {
	rows, err := s.db.Query(`SELECT id, uuid, author, name, description, created_at, updated_at FROM nests ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Nest
	for rows.Next() {
		n := &Nest{}
		if err := rows.Scan(&n.ID, &n.UUID, &n.Author, &n.Name, &n.Description, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) NestByID(id int64) (*Nest, error) {
	n := &Nest{}
	err := s.db.QueryRow(`SELECT id, uuid, author, name, description, created_at, updated_at FROM nests WHERE id = ?`, id).
		Scan(&n.ID, &n.UUID, &n.Author, &n.Name, &n.Description, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func (s *Store) NestByName(name string) (*Nest, error) {
	n := &Nest{}
	err := s.db.QueryRow(`SELECT id, uuid, author, name, description, created_at, updated_at FROM nests WHERE name = ?`, name).
		Scan(&n.ID, &n.UUID, &n.Author, &n.Name, &n.Description, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func (s *Store) CreateNest(n *Nest) error {
	ts := now()
	n.CreatedAt, n.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO nests (uuid, author, name, description, created_at, updated_at)
		VALUES (?,?,?,?,?,?)`, n.UUID, n.Author, n.Name, n.Description, ts, ts)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateNest(n *Nest) error {
	n.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE nests SET author=?, name=?, description=?, updated_at=? WHERE id=?`,
		n.Author, n.Name, n.Description, n.UpdatedAt, n.ID)
	return err
}

func (s *Store) DeleteNest(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE nest_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete a nest with %d server(s) using it", n)
	}
	_, err := s.db.Exec(`DELETE FROM nests WHERE id = ?`, id)
	return err
}

// ---- eggs ----

const eggCols = `id, uuid, nest_id, author, name, description, features, docker_images,
	file_denylist, update_url, config_files, config_startup, config_logs, config_stop,
	config_from, startup, script_container, script_entry, script_privileged, script_install,
	copy_script_from, created_at, updated_at`

func scanEgg(row interface{ Scan(...any) error }) (*Egg, error) {
	e := &Egg{}
	err := row.Scan(&e.ID, &e.UUID, &e.NestID, &e.Author, &e.Name, &e.Description, &e.Features,
		&e.DockerImages, &e.FileDenylist, &e.UpdateURL, &e.ConfigFiles, &e.ConfigStartup,
		&e.ConfigLogs, &e.ConfigStop, &e.ConfigFrom, &e.Startup, &e.ScriptContainer,
		&e.ScriptEntry, &e.ScriptPrivileged, &e.ScriptInstall, &e.CopyScriptFrom,
		&e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func (s *Store) EggsForNest(nestID int64) ([]*Egg, error) {
	rows, err := s.db.Query(`SELECT `+eggCols+` FROM eggs WHERE nest_id = ? ORDER BY id`, nestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Egg
	for rows.Next() {
		e, err := scanEgg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) EggByID(id int64) (*Egg, error) {
	return scanEgg(s.db.QueryRow(`SELECT `+eggCols+` FROM eggs WHERE id = ?`, id))
}

func (s *Store) EggByUUID(uuid string) (*Egg, error) {
	return scanEgg(s.db.QueryRow(`SELECT `+eggCols+` FROM eggs WHERE uuid = ?`, uuid))
}

func (s *Store) CreateEgg(e *Egg) error {
	ts := now()
	e.CreatedAt, e.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO eggs (uuid, nest_id, author, name, description, features,
		docker_images, file_denylist, update_url, config_files, config_startup, config_logs,
		config_stop, config_from, startup, script_container, script_entry, script_privileged,
		script_install, copy_script_from, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.UUID, e.NestID, e.Author, e.Name, e.Description, e.Features, e.DockerImages,
		e.FileDenylist, e.UpdateURL, e.ConfigFiles, e.ConfigStartup, e.ConfigLogs, e.ConfigStop,
		e.ConfigFrom, e.Startup, e.ScriptContainer, e.ScriptEntry, e.ScriptPrivileged,
		e.ScriptInstall, e.CopyScriptFrom, ts, ts)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateEgg(e *Egg) error {
	e.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE eggs SET nest_id=?, author=?, name=?, description=?, features=?,
		docker_images=?, file_denylist=?, update_url=?, config_files=?, config_startup=?,
		config_logs=?, config_stop=?, config_from=?, startup=?, script_container=?,
		script_entry=?, script_privileged=?, script_install=?, copy_script_from=?, updated_at=?
		WHERE id=?`,
		e.NestID, e.Author, e.Name, e.Description, e.Features, e.DockerImages, e.FileDenylist,
		e.UpdateURL, e.ConfigFiles, e.ConfigStartup, e.ConfigLogs, e.ConfigStop, e.ConfigFrom,
		e.Startup, e.ScriptContainer, e.ScriptEntry, e.ScriptPrivileged, e.ScriptInstall,
		e.CopyScriptFrom, e.UpdatedAt, e.ID)
	return err
}

func (s *Store) DeleteEgg(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE egg_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete an egg with %d server(s) using it", n)
	}
	_, err := s.db.Exec(`DELETE FROM eggs WHERE id = ?`, id)
	return err
}

// ---- egg variables ----

const eggVarCols = `id, egg_id, name, description, env_variable, default_value, user_viewable,
	user_editable, rules, created_at, updated_at`

func scanEggVar(row interface{ Scan(...any) error }) (*EggVariable, error) {
	v := &EggVariable{}
	err := row.Scan(&v.ID, &v.EggID, &v.Name, &v.Description, &v.EnvVariable, &v.DefaultValue,
		&v.UserViewable, &v.UserEditable, &v.Rules, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) EggVariables(eggID int64) ([]*EggVariable, error) {
	rows, err := s.db.Query(`SELECT `+eggVarCols+` FROM egg_variables WHERE egg_id = ? ORDER BY id`, eggID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EggVariable
	for rows.Next() {
		v, err := scanEggVar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) EggVariableByID(id int64) (*EggVariable, error) {
	return scanEggVar(s.db.QueryRow(`SELECT `+eggVarCols+` FROM egg_variables WHERE id = ?`, id))
}

func (s *Store) CreateEggVariable(v *EggVariable) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO egg_variables (egg_id, name, description, env_variable,
		default_value, user_viewable, user_editable, rules, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		v.EggID, v.Name, v.Description, v.EnvVariable, v.DefaultValue, v.UserViewable,
		v.UserEditable, v.Rules, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateEggVariable(v *EggVariable) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE egg_variables SET name=?, description=?, env_variable=?,
		default_value=?, user_viewable=?, user_editable=?, rules=?, updated_at=? WHERE id=?`,
		v.Name, v.Description, v.EnvVariable, v.DefaultValue, v.UserViewable, v.UserEditable,
		v.Rules, v.UpdatedAt, v.ID)
	return err
}

func (s *Store) DeleteEggVariable(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM server_variables WHERE variable_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM egg_variables WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
