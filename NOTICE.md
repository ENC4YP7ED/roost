# Third-party notices

Roost is an independent reimplementation, but it interoperates with — and
bundles data from — the projects below. Their licences are reproduced here.

## Pterodactyl Panel — MIT

Roost implements the Pterodactyl client, application and remote (Wings) APIs
so that existing Wings daemons and API integrations keep working. It also
ships Pterodactyl's default **egg exports** (`backend/internal/seed/eggs/`),
which are distributed under the MIT licence by Pterodactyl Software.

<https://github.com/pterodactyl/panel> — Copyright (c) Dane Everitt and
contributors.

No Pterodactyl source code is included; the eggs are data files in the public
PTDL export format.

## GoTypeMyAdmin

The built-in database viewer (`backend/internal/dbviewer/`,
`dbviewer-frontend/`) is vendored from GoTypeMyAdmin, by the same author as
this project, and carries the same MIT licence.

## Go module dependencies

- `modernc.org/sqlite` — BSD-3-Clause (pure-Go SQLite; no cgo)
- `golang.org/x/crypto` — BSD-3-Clause (bcrypt, ACME/autocert)
- `github.com/go-sql-driver/mysql` — MPL-2.0 (used only by the database viewer)

Run `go mod download && go tool licenses` or inspect `backend/go.sum` for the
exact pinned versions.

## Fonts and icons (frontend)

- **Inter** and **JetBrains Mono** via `@fontsource` — SIL Open Font License 1.1
- **Font Awesome Free** — icons CC BY 4.0, fonts SIL OFL 1.1, code MIT
