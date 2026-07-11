package store

import (
	"database/sql"
	"errors"
)

const credCols = `id, user_id, name, credential_id, public_key, attestation, aaguid,
	sign_count, transports, backup_eligible, backup_state, created_at, last_used_at`

func scanCredential(row interface{ Scan(...any) error }) (*WebAuthnCredential, error) {
	c := &WebAuthnCredential{}
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.CredentialID, &c.PublicKey, &c.Attestation,
		&c.AAGUID, &c.SignCount, &c.Transports, &c.BackupEligible, &c.BackupState,
		&c.CreatedAt, &c.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// WebAuthnCredentials lists a user's passkeys, newest first.
func (s *Store) WebAuthnCredentials(userID int64) ([]*WebAuthnCredential, error) {
	rows, err := s.db.Query(`SELECT `+credCols+` FROM webauthn_credentials WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebAuthnCredential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WebAuthnCredentialByID resolves a passkey by its raw credential id — used
// during login to find which user (and key) an assertion belongs to.
func (s *Store) WebAuthnCredentialByID(credentialID []byte) (*WebAuthnCredential, error) {
	return scanCredential(s.db.QueryRow(`SELECT `+credCols+` FROM webauthn_credentials WHERE credential_id = ?`, credentialID))
}

func (s *Store) CreateWebAuthnCredential(c *WebAuthnCredential) error {
	c.CreatedAt = now()
	if c.Transports == "" {
		c.Transports = "[]"
	}
	res, err := s.db.Exec(`INSERT INTO webauthn_credentials
		(user_id, name, credential_id, public_key, attestation, aaguid, sign_count,
		 transports, backup_eligible, backup_state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.UserID, c.Name, c.CredentialID, c.PublicKey, c.Attestation, c.AAGUID,
		c.SignCount, c.Transports, c.BackupEligible, c.BackupState, c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

// TouchWebAuthnCredential records a successful assertion: the authenticator's
// new signature counter and the last-used timestamp (replay protection).
func (s *Store) TouchWebAuthnCredential(credentialID []byte, signCount uint32) error {
	ts := now()
	_, err := s.db.Exec(`UPDATE webauthn_credentials SET sign_count = ?, last_used_at = ? WHERE credential_id = ?`,
		signCount, ts, credentialID)
	return err
}

func (s *Store) RenameWebAuthnCredential(userID, id int64, name string) error {
	res, err := s.db.Exec(`UPDATE webauthn_credentials SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWebAuthnCredential(userID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
