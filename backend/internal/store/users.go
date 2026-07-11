package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const userCols = `id, external_id, uuid, username, email, name_first, name_last, password,
	language, root_admin, use_totp, totp_secret, totp_authenticated_at, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.ExternalID, &u.UUID, &u.Username, &u.Email, &u.NameFirst,
		&u.NameLast, &u.Password, &u.Language, &u.RootAdmin, &u.UseTOTP, &u.TOTPSecret,
		&u.TOTPAuthenticatedAt, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByEmail(email string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE email = ? COLLATE NOCASE`, email))
}

func (s *Store) UserByUsername(name string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, name))
}

func (s *Store) UserByExternalID(ext string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE external_id = ?`, ext))
}

func (s *Store) UserByUUID(uuid string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE uuid = ?`, uuid))
}

// Users returns all users matching an optional case-insensitive filter on
// username/email, ordered by id.
func (s *Store) Users(filter string) ([]*User, error) {
	q := `SELECT ` + userCols + ` FROM users`
	var args []any
	if filter != "" {
		q += ` WHERE username LIKE ? OR email LIKE ? OR name_first LIKE ? OR name_last LIKE ?`
		like := "%" + filter + "%"
		args = append(args, like, like, like, like)
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateUser(u *User) error {
	ts := now()
	u.CreatedAt, u.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO users (external_id, uuid, username, email, name_first,
		name_last, password, language, root_admin, use_totp, totp_secret, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ExternalID, u.UUID, u.Username, u.Email, u.NameFirst, u.NameLast, u.Password,
		u.Language, u.RootAdmin, u.UseTOTP, u.TOTPSecret, ts, ts)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateUser(u *User) error {
	u.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE users SET external_id=?, username=?, email=?, name_first=?,
		name_last=?, password=?, language=?, root_admin=?, use_totp=?, totp_secret=?,
		totp_authenticated_at=?, updated_at=? WHERE id=?`,
		u.ExternalID, u.Username, u.Email, u.NameFirst, u.NameLast, u.Password, u.Language,
		u.RootAdmin, u.UseTOTP, u.TOTPSecret, u.TOTPAuthenticatedAt, u.UpdatedAt, u.ID)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE owner_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("cannot delete a user with %d server(s) attached to their account", n)
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *Store) CountUsers() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ---- sessions ----

func (s *Store) CreateSession(sess *Session) error {
	sess.CreatedAt = now()
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, ip, user_agent, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.IP, sess.UserAgent, sess.ExpiresAt, sess.CreatedAt)
	return err
}

// SessionUser resolves a session token hash to its user, enforcing expiry.
func (s *Store) SessionUser(tokenHash string) (*User, error) {
	var userID int64
	err := s.db.QueryRow(`SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, now()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.UserByID(userID)
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) PruneSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now())
	return err
}

// ---- API keys ----

func (s *Store) CreateAPIKey(k *APIKey) error {
	k.CreatedAt = now()
	res, err := s.db.Exec(`INSERT INTO api_keys (user_id, key_type, identifier, token_hash, memo,
		allowed_ips, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		k.UserID, k.KeyType, k.Identifier, k.TokenHash, k.Memo, k.AllowedIPs, k.CreatedAt)
	if err != nil {
		return err
	}
	k.ID, _ = res.LastInsertId()
	return nil
}

const apiKeyCols = `id, user_id, key_type, identifier, token_hash, memo, allowed_ips, last_used_at, created_at`

func scanAPIKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	k := &APIKey{}
	err := row.Scan(&k.ID, &k.UserID, &k.KeyType, &k.Identifier, &k.TokenHash, &k.Memo,
		&k.AllowedIPs, &k.LastUsedAt, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

func (s *Store) APIKeysForUser(userID int64, keyType int) ([]*APIKey, error) {
	rows, err := s.db.Query(`SELECT `+apiKeyCols+` FROM api_keys WHERE user_id = ? AND key_type = ? ORDER BY id`, userID, keyType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) APIKeyByIdentifier(identifier string) (*APIKey, error) {
	return scanAPIKey(s.db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE identifier = ?`, identifier))
}

func (s *Store) TouchAPIKey(id int64) {
	s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now(), id)
}

func (s *Store) DeleteAPIKey(userID int64, identifier string, keyType int) error {
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE user_id = ? AND identifier = ? AND key_type = ?`,
		userID, identifier, keyType)
	return err
}

// ---- SSH keys ----

func (s *Store) SSHKeysForUser(userID int64) ([]*SSHKey, error) {
	rows, err := s.db.Query(`SELECT id, user_id, name, fingerprint, public_key, created_at
		FROM user_ssh_keys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SSHKey
	for rows.Next() {
		k := &SSHKey{}
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) CreateSSHKey(k *SSHKey) error {
	k.CreatedAt = now()
	res, err := s.db.Exec(`INSERT INTO user_ssh_keys (user_id, name, fingerprint, public_key, created_at)
		VALUES (?, ?, ?, ?, ?)`, k.UserID, k.Name, k.Fingerprint, k.PublicKey, k.CreatedAt)
	if err != nil {
		return err
	}
	k.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) DeleteSSHKeyByFingerprint(userID int64, fingerprint string) error {
	_, err := s.db.Exec(`DELETE FROM user_ssh_keys WHERE user_id = ? AND fingerprint = ?`, userID, fingerprint)
	return err
}

// ---- recovery tokens ----

func (s *Store) ReplaceRecoveryTokens(userID int64, hashes []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM recovery_tokens WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(`INSERT INTO recovery_tokens (user_id, token, created_at) VALUES (?, ?, ?)`,
			userID, h, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ConsumeRecoveryToken deletes and returns true if any stored hash matches.
func (s *Store) ConsumeRecoveryToken(userID int64, hash string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM recovery_tokens WHERE user_id = ? AND token = ?`, userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SearchUsers is a lighter list for admin pickers: matches prefix on
// username/email, capped.
func (s *Store) SearchUsers(q string, limit int) ([]*User, error) {
	users, err := s.Users(strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(users) > limit {
		users = users[:limit]
	}
	return users, nil
}
