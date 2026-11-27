# TDB

**A terminal database client with first-class keyboard and mouse input.** Browse PostgreSQL, MySQL, MariaDB and SQLite from one Go binary, with SQL completion, editable result grids and an English or Simplified Chinese interface.

<p>
  <a href="https://github.com/p-v-q/tdb/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/p-v-q/tdb/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/p-v-q/tdb/releases"><img alt="Release" src="https://img.shields.io/github/v/release/p-v-q/tdb?include_prereleases"></a>
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache%202.0-blue.svg"></a>
  <a href="go.mod"><img alt="Go 1.27.1+" src="https://img.shields.io/badge/go-1.27.1-00ADD8.svg"></a>
</p>

<p align="center">
  <img src="docs/demo.gif" alt="TDB terminal demo: object browser, SQL editor, completion, results and keyboard or mouse navigation" width="900">
</p>

A real terminal recording of TDB on Windows ConPTY, scripted by the [integration-test harness](docs/integration-testing.md).

## Highlights

- **Browse and write:** capability-aware object trees, generated SQL and context-aware completion.
- **Inspect results:** sort, filter, resize, copy and export CSV, TSV, JSON or Markdown; scripts keep multiple result sets.
- **Edit safely:** read-only mode, risk confirmations, pending parameterized updates and manual transactions.
- **Work your way:** saved connections, server-fed pickers, environment-variable passwords, an encrypted vault, named tabs, first-class keyboard and mouse input, themes and two interface languages.

## Install

Download an archive for Linux, macOS or Windows from [Releases](https://github.com/p-v-q/tdb/releases). Each contains one binary built with `CGO_ENABLED=0`. TDB has no server component; the offline demo needs no database server.

The commands below assume the repository and its Releases are publicly
readable. They download the latest release unless a version is pinned.

### Install scripts

**Linux and macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/p-v-q/tdb/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/p-v-q/tdb/main/install.ps1 | iex
```

The scripts detect the OS and architecture, verify the release against
`SHA256SUMS`, install `tdb` (`tdb.exe` on Windows) and add it to `PATH`
where needed. To pin a version or install elsewhere, download the script
first:

```bash
curl -fsSL https://raw.githubusercontent.com/p-v-q/tdb/main/install.sh -o /tmp/tdb-install.sh
TDB_VERSION=0.9.2 TDB_INSTALL_DIR="$HOME/bin" sh /tmp/tdb-install.sh
```

```powershell
$env:TDB_VERSION = "0.9.2"
$env:TDB_INSTALL_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/p-v-q/tdb/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

### Manual download

Assets are named `tdb_<version>_<os>_<arch>`, with `os` equal to
`linux`, `darwin` or `windows`, and `arch` equal to `amd64` or `arm64`.

**Linux (amd64 shown):**

```bash
VERSION=0.9.2
curl -LO "https://github.com/p-v-q/tdb/releases/download/v${VERSION}/tdb_${VERSION}_linux_amd64.tar.gz"
tar -xzf "tdb_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 "tdb_${VERSION}_linux_amd64/tdb" /usr/local/bin/tdb
tdb -version
```

**macOS (arm64 shown; use `darwin_amd64` on Intel):**

```bash
VERSION=0.9.2
curl -LO "https://github.com/p-v-q/tdb/releases/download/v${VERSION}/tdb_${VERSION}_darwin_arm64.tar.gz"
tar -xzf "tdb_${VERSION}_darwin_arm64.tar.gz"
sudo install -m 0755 "tdb_${VERSION}_darwin_arm64/tdb" /usr/local/bin/tdb
tdb -version
```

**Windows (amd64 shown; use `windows_arm64` on ARM):**

```powershell
$VERSION = "0.9.2"
$asset = "tdb_${VERSION}_windows_amd64.zip"
Invoke-WebRequest "https://github.com/p-v-q/tdb/releases/download/v$VERSION/$asset" -OutFile $asset
Expand-Archive $asset -DestinationPath .
.\tdb_${VERSION}_windows_amd64\tdb.exe -version
```

Each archive contains the `tdb` binary (`tdb.exe` on Windows), plus
`README.md`, `LICENSE` and `CHANGELOG.md`.

**With Go** (use the version declared in [go.mod](go.mod) or newer):

```bash
go install github.com/p-v-q/tdb/cmd/tdb@latest
```

**From source:**

```bash
git clone https://github.com/p-v-q/tdb.git
cd tdb
make build
# or: go build -o bin/tdb ./cmd/tdb
```

Put the installed binary on your `PATH`. Source builds are written to `bin/`.

**Container image** (linux/amd64 and linux/arm64):

```bash
docker run --rm -it \
  -v "$HOME/.config/tdb:/home/tdb/.config/tdb" \
  ghcr.io/p-v-q/tdb:latest
```

The image is published by the release workflow next to the six binary archives.

## Fonts

TDB enables Nerd Font icons by default (`"icons": true`). The screenshots and demo GIF on this page use **CaskaydiaMono Nerd Font Mono**, family **CaskaydiaMono NFM**, from the Nerd Fonts v3 CascadiaMono package.

1. Download the **CascadiaMono** package from [Nerd Fonts](https://www.nerdfonts.com/fonts) or the [Nerd Fonts releases](https://github.com/ryanoasis/nerd-fonts/releases), extract it and install the CaskaydiaMono Nerd Font Mono `.ttf` files.
2. Select `CaskaydiaMono NFM` in your terminal settings. In Windows Terminal: **Settings → Profiles → Defaults → Appearance → Font face**. On macOS use Font Book; on Linux copy the files to `~/.local/share/fonts` and run `fc-cache -f`.

Without a Nerd Font the icon glyphs render as boxes. Turn off **Settings → Icons** (`F2`), or set `"icons": false`, to use portable symbols instead.

## Quick start

```bash
tdb                                      # offline demo catalog; no query execution
tdb -d sqlite -dsn shop.db               # open or create a SQLite database
tdb -d postgres -dsn 'postgres://user:pass@localhost:5432/shop?sslmode=disable'
tdb -d mysql    -dsn 'user:pass@tcp(localhost:3306)/shop'
tdb -lang zh-Hans                        # force Simplified Chinese
tdb query.sql                            # open a SQL file; Ctrl+S saves it
```

On a new SQLite file, try:

```sql
CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT);
INSERT INTO users VALUES (1, 'ada@example.com', 'Ada');
SELECT * FROM users;
```

Press `F5` to run the selection, statement under the caret or whole script. Press `F1` for all key bindings and `F2` for settings.

### CLI flags

| Flag | Meaning |
| --- | --- |
| `-d <driver>` | `postgres`, `mysql`, `mariadb`, `sqlite` or `demo` (default) |
| `-dsn <string>` | Connection URL/DSN, or a file path for SQLite |
| `-lang <en\|zh-Hans>` | Force the UI language; otherwise use the saved preference or system locale |
| `-settings <path>` | Use another settings file |
| `-drivers` | List registered drivers and dialects, then exit |
| `-print-settings` | Print resolved settings as JSON, then exit |
| `-version` | Print the version, then exit |

## Feature tour

### Browse, edit and inspect results

The object browser shows only kinds the driver can introspect. Double-click a table or view for **View data**; right-click for **View definition**, or a table for generated `SELECT` / `INSERT` / `UPDATE` / `DELETE` (views get `SELECT`). `Ctrl+Space` completes against the live catalog, **Edit → Format** formats the selection or buffer, and `F6` runs `EXPLAIN`.

<p align="center"><img src="docs/img/postgres.png" alt="PostgreSQL session with the object browser, SQL editor and sorted join results" width="900"></p>

*PostgreSQL: object browsing, SQL editing and sorted join results.*

Column types and a row/timing summary sit above the data. Sort with a header click or `s`, filter with `/`, resize columns, copy cells or rows as TSV, and use `F7` to export the current view. `[` / `]` switch result sets; wide grids get a horizontal scrollbar. SQL `NULL` stays distinct from an empty string.

### Connect and protect credentials

PostgreSQL and MySQL/MariaDB database names, plus PostgreSQL schemas, can be picked from server-supplied lists. The form also exposes driver-specific TLS/SSL modes and `passwordEnv` for passwords kept outside the settings file.

<p align="center"><img src="docs/img/connection-form.png" alt="PostgreSQL connection form with the server-provided database list open" width="900"></p>

*PostgreSQL: a new connection form with the database list open.*

**Settings → Connection passphrase…** seals the saved-connection list with PBKDF2-HMAC-SHA256 and AES-256-GCM. The passphrase is never stored and there is no recovery, so keep a backup first. See [the vault design and threat model](docs/secrets-encryption.md).

### Edit data with guardrails

- An eligible single-table `SELECT` with its primary key in the result can be edited with `e`; joins, aggregates and keyless results stay read-only.
- Changes remain pending until you confirm the exact parameterized `UPDATE` statements. Apply with `Ctrl+S` or `Ctrl+Enter` while the grid is focused; **Query → Discard Changes** drops them without writing.
- **Query → Read-only Mode** blocks writes and DDL. DDL and destructive writes such as `DELETE` without `WHERE` ask for confirmation.
- **Query → Manual Commit** keeps a batch in an open transaction; `F9` commits and `F11` rolls back. Controls are hidden without driver support.

### Browse MySQL and MariaDB catalogs

MySQL and MariaDB expose databases as schemas, with tables, views, routines, triggers and indexes. MariaDB also lists system-versioned tables and sequences.

<p align="center"><img src="docs/img/mysql.png" alt="MySQL object browser showing databases, the Tables group and the customers table" width="900"></p>

*MySQL: object-browser navigation across databases and tables.*

### Use English or Simplified Chinese

Use `-lang zh-Hans` or **Settings → Language** to switch. Data and identifiers keep their original text; CJK and emoji widths are measured in terminal cells so grid columns line up.

<p align="center"><img src="docs/img/chinese.png" alt="Simplified Chinese interface with a SQLite query and NULL-aware results" width="900"></p>

*Simplified Chinese: a SQLite query with NULL-aware results.*

## Supported databases

| Driver | Database | Catalog and connection notes |
| --- | --- | --- |
| `postgres` | PostgreSQL (tested on 17; pgx v5) | Schemas, tables, views, materialized views, functions, procedures, triggers, indexes and sequences; SSL modes; default schema in `search_path`. |
| `mysql` | MySQL 8+ (tested on 8.4) | Tables, views, routines, triggers, functional/multi-valued indexes, generated columns and JSON; TLS modes. |
| `mariadb` | MariaDB (tested on 11.4) | The MySQL driver and dialect, plus system-versioned tables and sequences. |
| `sqlite` | SQLite (pure-Go `modernc.org/sqlite`) | Tables, views, triggers, indexes and generated columns; attached databases; no CGO. |
| `demo` | Offline sample | PostgreSQL-shaped metadata and DDL; queries are not executed. |

Across the SQL drivers, identifiers are quoted per dialect, statements are cancellable, and results are capped at 1,000 rows with a truncation notice.

## Keyboard and mouse

`F1` opens a scrollable overlay generated from the command table and pane key handlers. The essentials:

| Key | Action |
| --- | --- |
| `F5` / `Ctrl+Enter` | Run selection, statement under the caret or whole script |
| `F6` | Explain the statement |
| `Ctrl+Space` | Complete; `Tab` / `Enter` accepts |
| `F1` / `F2` | Key bindings / settings |
| `Ctrl+F` / `Ctrl+H` | Find in the editor / query history |
| `Ctrl+O` / `Ctrl+S` | Open a SQL file / save it |
| `Ctrl+B` / `Ctrl+E` | Toggle objects / results |
| `Alt+1` / `Alt+2` / `Alt+3` | Focus objects / editor / results |
| `/` | Filter the tree, history or result grid |
| `s` / `e` / `y` | Sort / edit / copy a result cell |
| `F7` | Export the result view |
| `F9` / `F11` | Commit / roll back an open transaction |
| `Ctrl+Q` | Quit |

The mouse is a first-class input too: it supports caret placement, text and row selection, context menus, column resizing, pane dividers and scrollbars. While a divider is dragged, panes render their frames only and redraw contents on release.

Some Windows console shortcuts lose modifiers: use **Query → History** if `Ctrl+H` arrives as Backspace. `Ctrl+Enter` also applies pending grid edits when a terminal swallows `Ctrl+S`. More notes are in [docs/integration-testing.md](docs/integration-testing.md).

## Configuration

Preferences and saved connections live in one JSON file:

```text
Linux:    ~/.config/tdb/settings.json
macOS:    ~/Library/Application Support/tdb/settings.json
Windows:  %AppData%\tdb\settings.json
```

Set the `TDB_CONFIG` environment variable or pass `-settings` to override the path. Query history sits beside it in `history.json`. Missing files use defaults; unknown fields are ignored; saves are atomic, and an unreadable file is kept as `<path>.bak` before replacement.

```json
{
  "version": 1, "locale": "en", "theme": "dark",
  "mouse": true, "gutter": true, "icons": true, "readOnly": false,
  "layout": { "objectsVisible": true, "objectsWidth": 32, "resultsVisible": true, "resultsHeight": 8 },
  "connections": [
    { "name": "local", "driver": "sqlite", "file": "shop.db" },
    { "name": "prod", "driver": "postgres", "host": "db.example.com",
      "port": 5432, "user": "app", "database": "shop", "schema": "public",
      "passwordEnv": "TDB_PG_PASSWORD", "sslMode": "verify-full" }
  ]
}
```

Use `"locale": ""` to follow the system, or `"zh-Hans"` for Simplified Chinese. Themes are `"dark"` and `"light"`. Plain `connections` are cleartext; a vault writes the sealed `vault` object instead. Prefer `passwordEnv` over a literal password.

## Documentation

- [Architecture](docs/architecture.md) — packages, data flow and key decisions.
- [Integration testing](docs/integration-testing.md) — real-terminal harness, database setup, screenshots and measured results.
- [Secrets encryption](docs/secrets-encryption.md) — vault format and threat model.
- [Product gaps](docs/product-gaps.md) — known limits and roadmap.
- [Release checklist](docs/release.md) and [Changelog](CHANGELOG.md) — publishing and release notes.
- [Contributing](CONTRIBUTING.md), [Security](SECURITY.md) and [Code of Conduct](CODE_OF_CONDUCT.md).

## Development

```bash
make check      # gofmt check + go vet + all tests
make race       # race detector on stateful packages
make build      # build bin/tdb
make demo       # re-record docs/demo.gif
```

Unit tests need no external services. CI also runs PostgreSQL and MySQL driver integration tests. The separate Windows ConPTY suite is `.itest/run-all.ps1`; see [Integration testing](docs/integration-testing.md) for prerequisites.

## Status

TDB is in the `0.9.x` release line and is pre-1.0. The settings format is versioned, but CLI and UI details may still change before 1.0. Known limitations are tracked in [Product gaps](docs/product-gaps.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
