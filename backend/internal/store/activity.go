package store

import "database/sql"

// LogActivity records an event, optionally linked to subjects
// ("server:12", "user:3" pairs given as type/id tuples).
func (s *Store) LogActivity(a *ActivityLog, subjects ...[2]any) error {
	if a.Timestamp == "" {
		a.Timestamp = now()
	}
	res, err := s.db.Exec(`INSERT INTO activity_logs (batch, event, ip, description, actor_id,
		api_key_id, properties, timestamp) VALUES (?,?,?,?,?,?,?,?)`,
		a.Batch, a.Event, a.IP, a.Description, a.ActorID, a.APIKeyID, a.Properties, a.Timestamp)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	for _, sub := range subjects {
		s.db.Exec(`INSERT INTO activity_log_subjects (activity_log_id, subject_type, subject_id) VALUES (?,?,?)`,
			a.ID, sub[0], sub[1])
	}
	return nil
}

const activityCols = `id, batch, event, ip, description, actor_id, api_key_id, properties, timestamp`

func (s *Store) activityQuery(q string, args ...any) ([]*ActivityLog, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ActivityLog
	for rows.Next() {
		a := &ActivityLog{}
		if err := rows.Scan(&a.ID, &a.Batch, &a.Event, &a.IP, &a.Description, &a.ActorID,
			&a.APIKeyID, &a.Properties, &a.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityForActor lists a user's own account activity, newest first.
func (s *Store) ActivityForActor(userID int64, limit int) ([]*ActivityLog, error) {
	return s.activityQuery(`SELECT `+activityCols+` FROM activity_logs
		WHERE actor_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
}

// ActivityForSubject lists activity attached to a subject (e.g. a server).
func (s *Store) ActivityForSubject(subjectType string, subjectID int64, limit int) ([]*ActivityLog, error) {
	return s.activityQuery(`SELECT `+prefixCols(activityCols, "a.")+` FROM activity_logs a
		JOIN activity_log_subjects s ON s.activity_log_id = a.id
		WHERE s.subject_type = ? AND s.subject_id = ? ORDER BY a.id DESC LIMIT ?`,
		subjectType, subjectID, limit)
}

// ---- settings ----

func (s *Store) Setting(key, fallback string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows || err != nil {
		return fallback
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
