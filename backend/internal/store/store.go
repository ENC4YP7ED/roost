// Package store owns the SQLite database: schema, migrations, and all
// queries. Roost keeps the entire panel state in a single database file so
// deployment is one binary + one file.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("record not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is happiest with a single writer connection.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// now returns the canonical timestamp format stored in TEXT columns and
// emitted by the API (ISO 8601, UTC).
func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Additive migrations for databases created before a column existed.
	// SQLite has no "ADD COLUMN IF NOT EXISTS", so a duplicate-column error is
	// expected and ignored.
	for _, stmt := range alterMigrations {
		if _, err := s.db.Exec(stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migration %q: %w", stmt, err)
		}
	}
	return nil
}

// alterMigrations add columns to tables that predate them.
var alterMigrations = []string{
	`ALTER TABLE products ADD COLUMN configurable INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE products ADD COLUMN price_per_gb_cents INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE products ADD COLUMN min_memory INTEGER NOT NULL DEFAULT 1024`,
	`ALTER TABLE products ADD COLUMN max_memory INTEGER NOT NULL DEFAULT 16384`,
	`ALTER TABLE products ADD COLUMN nest_id INTEGER`,
	`ALTER TABLE orders ADD COLUMN config_memory INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE orders ADD COLUMN config_egg INTEGER NOT NULL DEFAULT 0`,
}

const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  external_id   TEXT,
  uuid          TEXT NOT NULL UNIQUE,
  username      TEXT NOT NULL UNIQUE,
  email         TEXT NOT NULL UNIQUE,
  name_first    TEXT NOT NULL DEFAULT '',
  name_last     TEXT NOT NULL DEFAULT '',
  password      TEXT NOT NULL,
  language      TEXT NOT NULL DEFAULT 'en',
  root_admin    INTEGER NOT NULL DEFAULT 0,
  use_totp      INTEGER NOT NULL DEFAULT 0,
  totp_secret   TEXT,
  totp_authenticated_at TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS recovery_tokens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token      TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS password_resets (
  email      TEXT NOT NULL,
  token      TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_ssh_keys (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  public_key  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

-- key_type: 1 = account (ptlc_), 2 = application (ptla_)
CREATE TABLE IF NOT EXISTS api_keys (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_type     INTEGER NOT NULL,
  identifier   TEXT NOT NULL UNIQUE,
  token_hash   TEXT NOT NULL,
  memo         TEXT NOT NULL DEFAULT '',
  allowed_ips  TEXT NOT NULL DEFAULT '[]',
  last_used_at TEXT,
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip         TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- WebAuthn (passkey) credentials. credential_id / public_key / aaguid are the
-- raw binary blobs from the authenticator; transports is a JSON array.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL DEFAULT 'Passkey',
  credential_id BLOB NOT NULL UNIQUE,
  public_key    BLOB NOT NULL,
  attestation   TEXT NOT NULL DEFAULT '',
  aaguid        BLOB,
  sign_count    INTEGER NOT NULL DEFAULT 0,
  transports    TEXT NOT NULL DEFAULT '[]',
  backup_eligible INTEGER NOT NULL DEFAULT 0,
  backup_state    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  last_used_at  TEXT
);

CREATE TABLE IF NOT EXISTS locations (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  short      TEXT NOT NULL UNIQUE,
  long       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid                  TEXT NOT NULL UNIQUE,
  public                INTEGER NOT NULL DEFAULT 1,
  name                  TEXT NOT NULL,
  description           TEXT NOT NULL DEFAULT '',
  location_id           INTEGER NOT NULL REFERENCES locations(id),
  fqdn                  TEXT NOT NULL,
  scheme                TEXT NOT NULL DEFAULT 'https',
  behind_proxy          INTEGER NOT NULL DEFAULT 0,
  maintenance_mode      INTEGER NOT NULL DEFAULT 0,
  memory                INTEGER NOT NULL,
  memory_overallocate   INTEGER NOT NULL DEFAULT 0,
  disk                  INTEGER NOT NULL,
  disk_overallocate     INTEGER NOT NULL DEFAULT 0,
  upload_size           INTEGER NOT NULL DEFAULT 100,
  daemon_token_id       TEXT NOT NULL,
  daemon_token          TEXT NOT NULL,
  daemon_listen         INTEGER NOT NULL DEFAULT 8080,
  daemon_sftp           INTEGER NOT NULL DEFAULT 2022,
  daemon_base           TEXT NOT NULL DEFAULT '/var/lib/pterodactyl/volumes',
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS allocations (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id   INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  ip        TEXT NOT NULL,
  ip_alias  TEXT,
  port      INTEGER NOT NULL,
  server_id INTEGER,
  notes     TEXT,
  UNIQUE (node_id, ip, port)
);

CREATE TABLE IF NOT EXISTS nests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid        TEXT NOT NULL UNIQUE,
  author      TEXT NOT NULL,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS eggs (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid             TEXT NOT NULL UNIQUE,
  nest_id          INTEGER NOT NULL REFERENCES nests(id) ON DELETE CASCADE,
  author           TEXT NOT NULL,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  features         TEXT NOT NULL DEFAULT '[]',
  docker_images    TEXT NOT NULL DEFAULT '{}',
  file_denylist    TEXT NOT NULL DEFAULT '[]',
  update_url       TEXT,
  config_files     TEXT NOT NULL DEFAULT '{}',
  config_startup   TEXT NOT NULL DEFAULT '{}',
  config_logs      TEXT NOT NULL DEFAULT '{}',
  config_stop      TEXT NOT NULL DEFAULT '',
  config_from      INTEGER,
  startup          TEXT NOT NULL DEFAULT '',
  script_container TEXT NOT NULL DEFAULT 'alpine:3.4',
  script_entry     TEXT NOT NULL DEFAULT 'ash',
  script_privileged INTEGER NOT NULL DEFAULT 1,
  script_install   TEXT NOT NULL DEFAULT '',
  copy_script_from INTEGER,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS egg_variables (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  egg_id        INTEGER NOT NULL REFERENCES eggs(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  env_variable  TEXT NOT NULL,
  default_value TEXT NOT NULL DEFAULT '',
  user_viewable INTEGER NOT NULL DEFAULT 1,
  user_editable INTEGER NOT NULL DEFAULT 1,
  rules         TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS servers (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  external_id     TEXT,
  uuid            TEXT NOT NULL UNIQUE,
  uuid_short      TEXT NOT NULL UNIQUE,
  node_id         INTEGER NOT NULL REFERENCES nodes(id),
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  status          TEXT,
  skip_scripts    INTEGER NOT NULL DEFAULT 0,
  owner_id        INTEGER NOT NULL REFERENCES users(id),
  memory          INTEGER NOT NULL,
  swap            INTEGER NOT NULL DEFAULT 0,
  disk            INTEGER NOT NULL,
  io              INTEGER NOT NULL DEFAULT 500,
  cpu             INTEGER NOT NULL DEFAULT 0,
  threads         TEXT,
  oom_disabled    INTEGER NOT NULL DEFAULT 1,
  allocation_id   INTEGER REFERENCES allocations(id),
  nest_id         INTEGER NOT NULL REFERENCES nests(id),
  egg_id          INTEGER NOT NULL REFERENCES eggs(id),
  startup         TEXT NOT NULL,
  image           TEXT NOT NULL,
  allocation_limit INTEGER NOT NULL DEFAULT 0,
  database_limit  INTEGER NOT NULL DEFAULT 0,
  backup_limit    INTEGER NOT NULL DEFAULT 0,
  installed_at    TEXT,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS server_variables (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id      INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  variable_id    INTEGER NOT NULL REFERENCES egg_variables(id) ON DELETE CASCADE,
  variable_value TEXT NOT NULL DEFAULT '',
  UNIQUE (server_id, variable_id)
);

CREATE TABLE IF NOT EXISTS subusers (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  server_id   INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  permissions TEXT NOT NULL DEFAULT '[]',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE (user_id, server_id)
);

CREATE TABLE IF NOT EXISTS database_hosts (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL,
  host          TEXT NOT NULL,
  port          INTEGER NOT NULL DEFAULT 3306,
  username      TEXT NOT NULL,
  password      TEXT NOT NULL,
  max_databases INTEGER,
  node_id       INTEGER REFERENCES nodes(id),
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS server_databases (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id        INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  database_host_id INTEGER NOT NULL REFERENCES database_hosts(id),
  database         TEXT NOT NULL,
  username         TEXT NOT NULL,
  remote           TEXT NOT NULL DEFAULT '%',
  password         TEXT NOT NULL,
  max_connections  INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedules (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id         INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  cron_day_of_week  TEXT NOT NULL DEFAULT '*',
  cron_month        TEXT NOT NULL DEFAULT '*',
  cron_day_of_month TEXT NOT NULL DEFAULT '*',
  cron_hour         TEXT NOT NULL DEFAULT '*',
  cron_minute       TEXT NOT NULL DEFAULT '*',
  is_active         INTEGER NOT NULL DEFAULT 1,
  is_processing     INTEGER NOT NULL DEFAULT 0,
  only_when_online  INTEGER NOT NULL DEFAULT 0,
  last_run_at       TEXT,
  next_run_at       TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedule_tasks (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  schedule_id         INTEGER NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
  sequence_id         INTEGER NOT NULL,
  action              TEXT NOT NULL,
  payload             TEXT NOT NULL DEFAULT '',
  time_offset         INTEGER NOT NULL DEFAULT 0,
  is_queued           INTEGER NOT NULL DEFAULT 0,
  continue_on_failure INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS backups (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id     INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  uuid          TEXT NOT NULL UNIQUE,
  upload_id     TEXT,
  is_successful INTEGER NOT NULL DEFAULT 0,
  is_locked     INTEGER NOT NULL DEFAULT 0,
  name          TEXT NOT NULL,
  ignored_files TEXT NOT NULL DEFAULT '[]',
  disk          TEXT NOT NULL DEFAULT 'wings',
  checksum      TEXT,
  bytes         INTEGER NOT NULL DEFAULT 0,
  completed_at  TEXT,
  deleted_at    TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mounts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid           TEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL UNIQUE,
  description    TEXT NOT NULL DEFAULT '',
  source         TEXT NOT NULL,
  target         TEXT NOT NULL,
  read_only      INTEGER NOT NULL DEFAULT 0,
  user_mountable INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mount_node (
  mount_id INTEGER NOT NULL REFERENCES mounts(id) ON DELETE CASCADE,
  node_id  INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  PRIMARY KEY (mount_id, node_id)
);

CREATE TABLE IF NOT EXISTS egg_mount (
  mount_id INTEGER NOT NULL REFERENCES mounts(id) ON DELETE CASCADE,
  egg_id   INTEGER NOT NULL REFERENCES eggs(id) ON DELETE CASCADE,
  PRIMARY KEY (mount_id, egg_id)
);

CREATE TABLE IF NOT EXISTS mount_server (
  mount_id  INTEGER NOT NULL REFERENCES mounts(id) ON DELETE CASCADE,
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  PRIMARY KEY (mount_id, server_id)
);

CREATE TABLE IF NOT EXISTS server_transfers (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id          INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  successful         INTEGER,
  old_node           INTEGER NOT NULL,
  new_node           INTEGER NOT NULL,
  old_allocation     INTEGER NOT NULL,
  new_allocation     INTEGER NOT NULL,
  old_additional_allocations TEXT NOT NULL DEFAULT '[]',
  new_additional_allocations TEXT NOT NULL DEFAULT '[]',
  archived           INTEGER NOT NULL DEFAULT 0,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity_logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  batch       TEXT,
  event       TEXT NOT NULL,
  ip          TEXT NOT NULL DEFAULT '',
  description TEXT,
  actor_id    INTEGER,
  api_key_id  INTEGER,
  properties  TEXT NOT NULL DEFAULT '{}',
  timestamp   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity_log_subjects (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  activity_log_id INTEGER NOT NULL REFERENCES activity_logs(id) ON DELETE CASCADE,
  subject_type    TEXT NOT NULL,
  subject_id      INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_event ON activity_logs (event);
CREATE INDEX IF NOT EXISTS idx_activity_subject ON activity_log_subjects (subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_allocations_server ON allocations (server_id);
CREATE INDEX IF NOT EXISTS idx_servers_owner ON servers (owner_id);
CREATE INDEX IF NOT EXISTS idx_subusers_user ON subusers (user_id);

-- ============================================================ billing
-- All monetary amounts are stored as integer minor units (e.g. euro cents)
-- alongside an ISO-4217 currency, so no floating-point rounding ever touches
-- an invoice total. VAT rates are basis points (1900 = 19.00%).

-- A plan a customer can buy: pricing plus the server specification that gets
-- provisioned automatically once payment clears.
CREATE TABLE IF NOT EXISTS products (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  price_cents      INTEGER NOT NULL,
  currency         TEXT NOT NULL DEFAULT 'EUR',
  billing_interval TEXT NOT NULL DEFAULT 'month', -- one_time | month | year
  egg_id           INTEGER NOT NULL REFERENCES eggs(id),
  node_id          INTEGER REFERENCES nodes(id),   -- NULL = auto-pick a node
  docker_image     TEXT NOT NULL DEFAULT '',
  memory           INTEGER NOT NULL,
  swap             INTEGER NOT NULL DEFAULT 0,
  disk             INTEGER NOT NULL,
  io               INTEGER NOT NULL DEFAULT 500,
  cpu              INTEGER NOT NULL DEFAULT 0,
  databases        INTEGER NOT NULL DEFAULT 0,
  allocations      INTEGER NOT NULL DEFAULT 1,
  backups          INTEGER NOT NULL DEFAULT 0,
  active           INTEGER NOT NULL DEFAULT 1,
  sort             INTEGER NOT NULL DEFAULT 0,
  configurable        INTEGER NOT NULL DEFAULT 0,   -- customer picks RAM/game
  price_per_gb_cents  INTEGER NOT NULL DEFAULT 0,   -- added per GiB of RAM
  min_memory          INTEGER NOT NULL DEFAULT 1024,
  max_memory          INTEGER NOT NULL DEFAULT 16384,
  nest_id             INTEGER REFERENCES nests(id), -- game category (configurable)
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);

-- A customer's billing identity, used to build compliant invoices. A VAT id
-- present + a country different from the seller's triggers EU reverse charge.
CREATE TABLE IF NOT EXISTS billing_profiles (
  user_id     INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL DEFAULT '',
  company     TEXT NOT NULL DEFAULT '',
  address     TEXT NOT NULL DEFAULT '',
  city        TEXT NOT NULL DEFAULT '',
  postal_code TEXT NOT NULL DEFAULT '',
  country     TEXT NOT NULL DEFAULT '',
  vat_id      TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL
);

-- A recurring plan a customer holds. The provider owns the schedule; we mirror
-- its state so we can suspend/unsuspend the linked server.
CREATE TABLE IF NOT EXISTS subscriptions (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid               TEXT NOT NULL UNIQUE,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id         INTEGER NOT NULL REFERENCES products(id),
  provider           TEXT NOT NULL,             -- stripe | revolut
  provider_ref       TEXT NOT NULL DEFAULT '',  -- subscription id at the provider
  status             TEXT NOT NULL DEFAULT 'pending', -- pending|active|past_due|canceled
  server_id          INTEGER REFERENCES servers(id) ON DELETE SET NULL,
  current_period_end TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

-- One purchase attempt. Drives checkout, then provisioning on payment.
CREATE TABLE IF NOT EXISTS orders (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid            TEXT NOT NULL UNIQUE,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id      INTEGER NOT NULL REFERENCES products(id),
  provider        TEXT NOT NULL,               -- stripe | revolut
  provider_ref    TEXT NOT NULL DEFAULT '',    -- checkout session / order id
  status          TEXT NOT NULL DEFAULT 'pending', -- pending|paid|failed|canceled|refunded
  net_cents       INTEGER NOT NULL,
  vat_cents       INTEGER NOT NULL DEFAULT 0,
  gross_cents     INTEGER NOT NULL,
  vat_rate        INTEGER NOT NULL DEFAULT 0,  -- basis points
  reverse_charge  INTEGER NOT NULL DEFAULT 0,
  currency        TEXT NOT NULL DEFAULT 'EUR',
  server_id       INTEGER REFERENCES servers(id) ON DELETE SET NULL,
  subscription_id INTEGER REFERENCES subscriptions(id) ON DELETE SET NULL,
  config_memory   INTEGER NOT NULL DEFAULT 0,  -- chosen RAM for a configurable plan
  config_egg      INTEGER NOT NULL DEFAULT 0,  -- chosen game for a configurable plan
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  paid_at         TEXT
);

-- A gapless, immutable accounting record. Seller and buyer details are
-- snapshotted as JSON so a later profile edit never rewrites history.
CREATE TABLE IF NOT EXISTS invoices (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  number         TEXT NOT NULL UNIQUE,
  user_id        INTEGER NOT NULL REFERENCES users(id),
  order_id       INTEGER REFERENCES orders(id),
  status         TEXT NOT NULL DEFAULT 'issued', -- issued | paid | void
  currency       TEXT NOT NULL DEFAULT 'EUR',
  net_cents      INTEGER NOT NULL,
  vat_cents      INTEGER NOT NULL,
  gross_cents    INTEGER NOT NULL,
  vat_rate       INTEGER NOT NULL,
  reverse_charge INTEGER NOT NULL DEFAULT 0,
  seller         TEXT NOT NULL DEFAULT '{}', -- JSON snapshot
  buyer          TEXT NOT NULL DEFAULT '{}', -- JSON snapshot
  lines          TEXT NOT NULL DEFAULT '[]', -- JSON line items
  notes          TEXT NOT NULL DEFAULT '',
  issued_at      TEXT NOT NULL,
  due_at         TEXT,
  paid_at        TEXT,
  created_at     TEXT NOT NULL
);

-- Per-year gapless invoice sequence, incremented inside the issuing tx.
CREATE TABLE IF NOT EXISTS invoice_sequences (
  year TEXT PRIMARY KEY,
  last INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_orders_user ON orders (user_id);
CREATE INDEX IF NOT EXISTS idx_orders_provider_ref ON orders (provider, provider_ref);
CREATE INDEX IF NOT EXISTS idx_subscriptions_user ON subscriptions (user_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_provider_ref ON subscriptions (provider, provider_ref);
CREATE INDEX IF NOT EXISTS idx_invoices_user ON invoices (user_id);
`
