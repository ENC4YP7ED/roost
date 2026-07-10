package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// allowedPrivileges is the whitelist of MySQL privilege keywords GRANT accepts.
// GRANT targets cannot be parameterized, so the only safe path is validation.
var allowedPrivileges = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"CREATE": true, "DROP": true, "RELOAD": true, "PROCESS": true,
	"REFERENCES": true, "INDEX": true, "ALTER": true, "SHOW DATABASES": true,
	"CREATE TEMPORARY TABLES": true, "LOCK TABLES": true, "EXECUTE": true,
	"CREATE VIEW": true, "SHOW VIEW": true, "CREATE ROUTINE": true,
	"ALTER ROUTINE": true, "EVENT": true, "TRIGGER": true, "GRANT OPTION": true,
	"CREATE USER": true, "USAGE": true,
}

var bareIdentRe = regexp.MustCompile(`^[A-Za-z0-9_$]+$`)

// sanitizePrivileges validates a comma-separated privilege list against the
// whitelist and returns a normalized form, or an error on anything unknown.
func sanitizePrivileges(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.EqualFold(p, "ALL") || strings.EqualFold(p, "ALL PRIVILEGES") {
		return "ALL PRIVILEGES", nil
	}
	parts := strings.Split(p, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		key := strings.ToUpper(strings.Join(strings.Fields(part), " "))
		if !allowedPrivileges[key] {
			return "", fmt.Errorf("unsupported privilege: %q", strings.TrimSpace(part))
		}
		out = append(out, key)
	}
	return strings.Join(out, ", "), nil
}

// sanitizeScope validates and canonicalizes a GRANT scope ("*.*", "db.*",
// "db.table"), re-quoting identifiers so nothing can break out of the statement.
func sanitizeScope(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*.*" {
		return "*.*", nil
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid scope %q (expected db.* or db.table)", s)
	}
	db, err := scopeIdent(parts[0])
	if err != nil {
		return "", err
	}
	tbl, err := scopeIdent(parts[1])
	if err != nil {
		return "", err
	}
	return db + "." + tbl, nil
}

func scopeIdent(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "`")
	if p == "*" {
		return "*", nil
	}
	if !bareIdentRe.MatchString(p) {
		return "", fmt.Errorf("invalid identifier %q in scope", p)
	}
	return QuoteIdent(p), nil
}

// User is one MySQL account plus a coarse privilege summary.
type User struct {
	User      string `json:"user"`
	Host      string `json:"host"`
	SuperUser bool   `json:"superUser"`
	Locked    bool   `json:"locked"`
}

// Users lists accounts from mysql.user.
func Users(ctx context.Context, db *sql.DB) ([]User, error) {
	// Column availability differs across MySQL/MariaDB versions; select the
	// portable subset and probe Super_priv which both expose.
	rows, err := db.QueryContext(ctx, "SELECT User, Host, Super_priv FROM mysql.user ORDER BY User, Host")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var super string
		if err := rows.Scan(&u.User, &u.Host, &super); err != nil {
			return nil, err
		}
		u.SuperUser = strings.EqualFold(super, "Y")
		out = append(out, u)
	}
	return out, rows.Err()
}

// Grants returns the SHOW GRANTS output for an account.
func Grants(ctx context.Context, db *sql.DB, user, host string) ([]string, error) {
	q := fmt.Sprintf("SHOW GRANTS FOR %s@%s", quoteString(user), quoteString(host))
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var grants []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// CreateUser creates an account, optionally with a password.
func CreateUser(ctx context.Context, db *sql.DB, user, host, password string) error {
	q := fmt.Sprintf("CREATE USER %s@%s", quoteString(user), quoteString(host))
	if password != "" {
		q += " IDENTIFIED BY " + quoteString(password)
	}
	_, err := db.ExecContext(ctx, q)
	return err
}

// DropUser removes an account.
func DropUser(ctx context.Context, db *sql.DB, user, host string) error {
	q := fmt.Sprintf("DROP USER %s@%s", quoteString(user), quoteString(host))
	_, err := db.ExecContext(ctx, q)
	return err
}

// GrantPrivileges grants a privilege set on a scope (e.g. "*.*" or "`db`.*").
// Both privileges and scope are validated/canonicalized because GRANT targets
// cannot be passed as bound parameters.
func GrantPrivileges(ctx context.Context, db *sql.DB, user, host, privileges, scope string) error {
	privs, err := sanitizePrivileges(privileges)
	if err != nil {
		return err
	}
	safeScope, err := sanitizeScope(scope)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("GRANT %s ON %s TO %s@%s", privs, safeScope, quoteString(user), quoteString(host))
	if _, err := db.ExecContext(ctx, q); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "FLUSH PRIVILEGES")
	return err
}

// quoteString single-quotes and escapes a string literal (for identifiers that
// are values, like user/host names, which cannot be parameterized in DDL).
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "''") + "'"
}
