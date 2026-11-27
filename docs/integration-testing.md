# TDB integration testing against real databases

This document records a large-scale end-to-end test campaign of **TDB** (the
terminal database client in this repository) against real database servers and
realistic data, driving the actual `tdb` binary through a real terminal. It
lists what was tested, what broke, what was fixed, and how to reproduce the
whole suite.

The campaign ran on Windows 10 with:

- **PostgreSQL 17.6** on `127.0.0.1:55432` (portable binaries, trust auth, role
  `tdb`/`tdbpass`),
- **MySQL 8.4.4** on `127.0.0.1:53306` (`root`, empty password; `tdb`/`tdbpass`),
- **MariaDB 11.4.4** on `127.0.0.1:53307` (`root`/`tdbpass`, `tdb`/`tdbpass`),
- **SQLite** databases generated with the driver's own `modernc.org/sqlite`.

The datasets live under `.dbs` and are described in
[`.itest/datasets/README.md`](../.itest/datasets/README.md); `.itest/datasets/load.ps1`
rebuilds them idempotently in about five minutes.

## How the UI is tested

The interesting part of TDB is the terminal interface, so the test harness does
not stop at the driver layer: it starts **the real binary** and interacts with it
the way a user does.

```
.itest/harness      starts bin/tdb.exe inside a Windows ConPTY pseudo console
                    (github.com/aymanbagabas/go-pty), feeds keystrokes/mouse
                    events, and renders the byte stream with a VT emulator
                    (charmbracelet/x/vt). After every step it can snapshot the
                    screen as text, HTML and — through headless Chrome — PNG,
                    so UI layout and colours can be reviewed, not just asserted.
.itest/scenario     shared plumbing: environment discovery, seeded settings,
                    session start/stop, artifact snapshots, screen assertions and
                    independent database verification connections.
.itest/scenarios    the scenario suites (87 permanent scenarios).
.itest/datasets     realistic large datasets and the idempotent loader.
.itest/run-all.ps1  builds the binary and runs the whole suite, writing
                    .artifacts/itest-summary.md.
```

Every scenario is a multi-step user workflow (connect, browse, edit, run, sort,
export, apply a grid edit, switch language, …) with a screenshot at each step.
Assertions read the rendered screen; database effects are verified through an
independent `database/sql` connection, never through the UI under test.

## Screenshot font (prerequisite)

Chrome-rendered screenshots and demo frames use **CaskaydiaMono Nerd Font Mono**
from Nerd Fonts v3. Install *CaskaydiaMono Nerd Font Mono* for your Windows user;
Chrome knows its family as **`CaskaydiaMono NFM`**. Download it from
[nerdfonts.com/fonts](https://www.nerdfonts.com/fonts) or use the
`CascadiaMono.zip` asset in a [Nerd Fonts v3 GitHub
release](https://github.com/ryanoasis/nerd-fonts/releases), then restart Chrome
before recording. Without the font, the terminal's private-use-area icons render
as tofu boxes (□), even though the text and layout still work.

At 14px this monospace font advances about 8.4px per terminal cell. The
harness's browser width, `cols*8.6 + 40`, leaves room for that advance and the
HTML padding, so full-width rows do not wrap.

## Rebuilding the demo GIF

The demo recorder needs the screenshot font above, headless Chrome, a built
`bin/tdb.exe`, and the SQLite shop dataset. From the repository root:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
. .itest/env.ps1
go build -o bin/tdb.exe ./cmd/tdb
make demo

# Equivalent direct invocation when make is not installed:
cd .itest
go run ./cmd/demo -root .. -out ../.artifacts/demo
```

`make demo` (or the direct invocation) runs `.itest/cmd/demo`, writes HTML/PNG
frames and `manifest.txt` under `.artifacts/demo`, then invokes `.itest/cmd/gif`.
The GIF builder uses only the Go standard library: it scales frames to 980px
wide, quantizes every frame
with one shared Plan 9 palette, merges identical consecutive frames while adding
their delays, samples merged recordings longer than 120 frames, and loops
forever. No Python interpreter or Pillow installation is required; the demo's old
`-python` flag is accepted only for compatibility and ignored.

To rebuild a GIF from existing frames without recording the session again:

```powershell
cd ..
. .itest/env.ps1
cd .itest
go run ./cmd/gif ../.artifacts/demo/frames ../.artifacts/demo/manifest.txt ../.artifacts/demo ../.artifacts/demo/demo.gif
```

The manifest format is one `<name> <delay-ms>` entry per line. Missing PNGs are
skipped with a warning; the builder fails if no manifest frames are available.
`.itest/demo/make_gif.py` remains as the reference implementation, but the demo
no longer calls it.

## What the data looks like

| Backend | Highlights |
|---|---|
| PostgreSQL `shop` | 7 schemas (public, sales, inventory, analytics, reporting, `LEGACY_2019`, `Warehouse`), 200k customers, 500k orders (25 monthly partitions), 1.5M order items, 500k payments, 1M jsonb events, 200k reviews. Arrays, uuid, domains, enums, generated/identity columns, comments, a materialized view, 6 views, 5 functions + 1 procedure, triggers, sequences, a 40-column table, an empty table, a NULL-only table, a 1 MB text/bytea row, quoted reserved-word identifiers. |
| PostgreSQL `legacy` | 12 messy schemas: mixed case, spaces in names, reserved words, empty tables. |
| MySQL `shop` | 150k customers, 300k orders, 800k order items, JSON, enum, generated columns, FULLTEXT and multi-valued indexes, partitioned table, DEFINER routines, triggers, reserved-word table `` `order` ``, CJK/emoji; plus 12 databases (`legacy`, `legacy_*`) for the multi-database tree. |
| MariaDB `shop` | 100k customers, 200k orders, a SYSTEM VERSIONED table, a SEQUENCE, DEFINER routines, JSON alias, generated columns; plus 12 databases. |
| SQLite | `shop.db` with 600k rows across users/orders/items and `weird.db` with 19 edge-case objects (reserved words, spaces, non-ASCII names, WITHOUT ROWID, STRICT, generated columns, 1 MB values). |

## Running the suite

```powershell
# once: start the servers and load the datasets (see .itest/env.ps1 and
# .itest/datasets/README.md for the exact portable-server commands)
C:\Users\qxwxi\repos\tdb\.itest\datasets\load.ps1

# build + run everything (~20-30 min on this machine)
C:\Users\qxwxi\repos\tdb\.itest\run-all.ps1

# or one group
C:\Users\qxwxi\repos\tdb\.itest\run-all.ps1 -Group grid
```

The unit suite stays green as well:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
. .itest\env.ps1
go test ./...
```

## Results

Final run (`bin/tdb.exe` built 2026-11-26 19:37, `.itest/run-all.ps1`):

| Group | Scenarios | Passed | Failed | Skipped | Time |
|---|---|---|---|---|---|
| smoke (startup, SQLite query) | 2 | 2 | 0 | 0 | 7s |
| interaction (menus, grid, tabs and their bulk closes, pane resizing, completion, find, history, mouse, plan, double-click, stable connection rows) | 14 | 14 | 0 | 0 | 110s |
| chrome (menu bar, toolbar, preferences, language, read-only, about, welcome) | 8 | 8 | 0 | 0 | 67s |
| connection (connection form, vault passphrase setup/unlock) | 2 | 2 | 0 | 0 | 36s |
| showcase (curated PostgreSQL/MySQL/Chinese/welcome screenshots) | 4 | 4 | 0 | 0 | 56s |
| editor (script targets, selection, find, comment, format, save/open, tabs, history, dialogs, unicode, errors) | 12 | 12 | 0 | 0 | 146s |
| grid (sort/filter/copy, NULLs, wide tables, result sets, export, grid edit, read-only, risk dialogs, errors, large results, horizontal scrollbar) | 13 | 13 | 0 | 0 | 189s |
| PostgreSQL (catalog, browser filter, default schema and its public fallback, completion, joins, jsonb/arrays, edge tables, plans, cancel, generated SQL, DDL, database/schema pickers) | 24 | 24 | 0 | 0 | 561s |
| MySQL/MariaDB/SQLite (catalog, browse, JSON/enum/generated/FULLTEXT, DDL stripping, multi-database, generated SQL, database picker, big SQLite results, weird schema) | 19 | 19 | 0 | 0 | 443s |
| **Total** | **98** | **98** | **0** | **0** | **1,615s (~27 min)** |

Two defect-documenting scenarios that used to skip (PostgreSQL view options,
MariaDB system-versioned table/sequence listing) now pass, because the defects
are fixed.

Alongside the scenario suite:

- `go build ./...` and `go vet ./...` are clean;
- `go test ./...` passes for every package (catalog, cmd, complete, demo, mysql,
  postgres, sqlite, history, i18n, settings, sql, textbuf, ui, vault);
- the live driver integration tests pass against the real servers:
  `TDB_POSTGRES_DSN=… go test ./driver/postgres -run TestIntegration` and
  `TDB_MYSQL_DSN=… go test ./driver/mysql -run TestIntegration`
  (the MySQL one now also checks that a MySQL server lists no sequences while
  the MariaDB-capable driver still advertises the kind).

Artifacts: `.artifacts/itest-summary.md` (the table above) and one directory per
scenario under `.artifacts/itest/<TestName>/`.

Post-campaign changes, each covered by unit tests and a real-terminal scenario:

- double-clicking a table or view in the object browser opens **View data** —
  the bounded SELECT, run in its own tab — instead of inserting the name
  (`ui/objects_double_click_test.go`,
  `.itest/scenarios/objects_double_click_test.go`);
- the tab context menu closes tabs in bulk — **Close others**, **Close to the
  left**, **Close to the right**, **Close all** — and offers those entries only
  when they have something to close (`ui/tab_bulk_close_test.go`,
  `.itest/scenarios/tabs_connections_test.go`);
- opening a saved connection no longer moves its row to the top of the tree:
  the connection list keeps the settings-file order, the caret follows the row
  that was opened and the pane scrolls only as far as needed
  (`ui/connection_order_test.go`,
  `.itest/scenarios/tabs_connections_test.go`);
- the result grid has a horizontal scrollbar when the columns are wider than the
  pane — drag the thumb, click the track, and the bar costs one data row only
  while it is shown (`ui/hscroll_test.go`,
  `.itest/scenarios/results_hscroll_test.go`);
- a PostgreSQL default schema keeps the server's default `search_path` entries
  behind the chosen schema, so unqualified queries resolve in the chosen schema
  *and* in public (`driver/postgres/postgres_test.go`,
  `.itest/scenarios/pg_search_path_test.go`);
- dragging a pane divider draws only the pane chrome and a dim "resizing…" hint;
  the editor and grid contents are laid out again on release, which keeps the
  drag smooth on a large result set (`ui/resize_preview_test.go`,
  `.itest/scenarios/pane_resize_test.go`).

## Defects found and fixed

The campaign found 20 product defects; every one is fixed and covered by a
regression test (unit tests in `ui/`, `driver/`, `cmd/`, or a scenario in
`.itest/scenarios/`).

| # | Symptom (as a user sees it) | Root cause | Fix |
|---|---|---|---|
| 1 | MySQL connection showed "metadata not loaded" and an empty object browser whenever a table had a functional or multi-valued index | `driver/mysql` scanned `information_schema.STATISTICS.COLUMN_NAME` into a `string`; MySQL reports `NULL` for expression index parts, so the whole catalog failed | scan a `sql.NullString` and list expression parts as `expression` |
| 2 | MariaDB SYSTEM VERSIONED tables were missing from the object browser | `loadTables` only accepted `BASE TABLE`/`VIEW` | accept `SYSTEM VERSIONED` as a table |
| 3 | MariaDB SEQUENCE objects were missing (no "Sequences" group at all) | sequences appear as `TABLE_TYPE='SEQUENCE'` and the driver neither queried nor advertised them | new `loadSequences` pass and `KindSequence` capability |
| 4 | Copying a PostgreSQL view definition and re-running it produced a *weaker* view: `WITH (security_barrier=true)` and `WITH CHECK OPTION` were dropped | view DDL only wrapped `pg_get_viewdef` | render `pg_class.reloptions` as `WITH (...)` and `check_option` as `WITH CASCADED/LOCAL CHECK OPTION` |
| 5 | A committed cell edit kept showing the old database value (only the row marker said otherwise) | the grid styled pending cells but never substituted the pending value | draw the pending value (and `NULL` for a pending NULL) |
| 6 | Cells wider than their column were cut with no indication | grid rendered `truncateANSI(cell, width)` | clipped cells end with `…` (grid, header, type row, object tree) |
| 7 | CJK/emoji cells were clipped even though they fit: columns were measured in runes, drawn in terminal cells | `columnWidths` used `len([]rune(...))` | measure with display width of the cleaned text |
| 8 | The export dialog promised "⏎ export" but Enter on the path field did nothing — keyboard-only users could not export | the path branch of `handleExportKey` had no `enter` case | Enter submits the export |
| 9 | Ctrl+S on a tab with no file did nothing (the "no file to save" message vanished) | `saveFile` had a value receiver, so `setMessage` wrote into a copy | pointer receiver |
| 10 | On the welcome page, Down+Enter connected to a saved connection whose row was never highlighted | the highlight was applied only to rows with an accelerator | highlight rows without one too |
| 11 | A cancelled or expired context could still execute a statement | drivers relied on the pool to notice the deadline | explicit `ctx.Err()` guard in the sqlite/PostgreSQL/MySQL query path |
| 12 | Documented Ctrl+/ (comment) did nothing on Windows | Windows delivers a unit separator, which Bubble Tea names `ctrl+_` | alias `ctrl+_` → `ctrl+/` |
| 13 | Documented Ctrl+Space (completion) did nothing on Windows | Windows delivers Shift+Space-less control events as a lone NUL rune | alias NUL → `ctrl+space` |
| 14 | Typed text could carry invisible control bytes (e.g. an Alt-modified ESC arriving as NUL) into a statement | the plain-input path inserted `msg.Runes` verbatim | strip control characters, like the paste path already did |
| 15 | A scoped, single-row grid UPDATE was labelled "unsafe" in the confirmation dialog | every non-DDL risk was mapped to `KeyRiskDangerousWrite` | new "write" risk label for plain writes |
| 16 | The file picker's buttons ("Choose this folder", "Open", "Cancel") were reachable only with the mouse | Tab cycled list ↔ path only | third focus stop on the buttons; ←/→ selects, Enter activates, hint shows `⇥` |
| 17 | A first run could offer the vault setup for a settings file that can never be created (Windows reports a path through a regular file as "not found") | `isFirstRun` trusted `ErrNotExist` alone | also require the parent to exist and be a directory |
| 18 | Status lines read "1 rows in 1ms", "1 schemas / 1 tables", "1 pending change(s)", "Applied 1 change(s)", "1 statement(s)" | no singular forms in the message catalogs | singular keys for rows/affected/selected/pending/applied/statement/read-only-blocked/exported/copied and for schema/table counts (en + zh), including a wrong Chinese word for "schema" |
| 20 | Choosing a PostgreSQL (or MySQL/MariaDB) database and default schema meant typing names from memory in bare text fields, and the tree never said which database the schemas belonged to — the hierarchy the server has (database → schema) was invisible | the form had no way to ask the server for names, and the session kept no visible database label | the Database and PostgreSQL schema fields read their values from the server on ↓ (maintenance connection for the list, "database default" as the first schema entry, picking a database clears a stale schema choice); the header and the connection row name the PostgreSQL database |
| 19 | The repository's own test suite failed on Windows (file permissions, POSIX paths, nanosecond deadlines, coarse-clock durations) | tests assumed POSIX semantics | platform-aware tests (permission checks, picker root, unreadable folders, path separators, duration/cancellation determinism) |

Regression tests added for the UI/UX defects:

- `ui/defect_regression_test.go` — save-file feedback, pending cell rendering,
  Enter-to-export, wide-glyph column widths,
- `ui/editor_control_input_test.go` — control runes, Windows key aliases,
  Ctrl+Space completion,
- `ui/welcome_selection_test.go` — the selected saved-connection row,
- `ui/conn_options_test.go` — server-fed database/schema lists, the database
  default entry, the picked database resetting the schema, the header naming
  the PostgreSQL database,
- `.itest/scenarios/pg_picker_test.go`, `.itest/scenarios/conn_picker_test.go` —
  the same flows through the real terminal against the real servers.
- updated tests in `ui/file_picker_test.go`, `ui/export_test.go`,
  `driver/sqlite/sqlite_test.go`, `settings/settings_test.go`,
  `history/history_test.go` and `cmd/tdb/main_test.go`.

## Platform limitations found (documented, not fixable from the app)

Bubble Tea v1 on Windows reads console input records (`coninput`), and some of
TDB's documented shortcuts are unreachable there because the modifier state is
lost before the app sees the key:

- **Ctrl+H (query history)** arrives as plain Backspace (VK_BACK ignores the
  Ctrl state). Use **Query → History (Alt+Q, H)**.
- **Ctrl+PgUp / Ctrl+PgDn (previous/next tab)** arrive as plain PgUp/PgDn. Use
  **View → Previous/Next Tab (Alt+V, P/N)** or click the tab.
- **Shift+F10 (context menu)** arrives as F10. Use the mouse right-click.

Ctrl+/ and Ctrl+Space were in the same class and are covered by defects 12–13.

Harness-side quirks (not product bugs): the synthetic ConPTY input path drops
4-byte UTF-8 (emoji typed through the harness), sometimes emits a stray NUL
before a control byte, and the machine's coarse monotonic clock reports 0 for
sub-millisecond durations (the UI already rounds those up to "1ms").

## Remaining UI observations (not defects)

Recorded while reviewing screenshots, in rough priority order:

- The history dialog title uses the same accent background as the selected row.
- The welcome page keeps reporting "Ln 1, Col 1" in the status bar (it is not an
  editor page).
- In the light theme, dialogs/panels repaint light while the editor and results
  keep the terminal background — a mixed look on a dark terminal.
- With the result grid focused, the status bar shows two overlapping hint
  strings (grid hints on the left, global hints on the right).
- Switching result sets updates the results summary but leaves the previous
  set's message in the status bar.
- SQLite BLOB cells render as sanitized raw bytes (empty-looking) while MySQL
  renders hex — inconsistent presentation across drivers.
- Object-browser labels and comments are clipped with an ellipsis now, but the
  vertical scrollbar can still overlap the last column.
- MyISAM-style MySQL table DDL is unqualified when the connection's default
  database is the object's database (server `SHOW CREATE` behavior).

## Evidence

Every scenario writes its screenshots, text dumps and HTML snapshots to
`.artifacts/itest/<TestName>/`. Highlights:

| What | Path |
|---|---|
| Large PostgreSQL session with a sorted join | `.artifacts/itest/TestShowcasePostgres/036-pg-join-sorted.png` |
| PostgreSQL query plan | `.artifacts/itest/TestShowcasePostgres/041-pg-plan.png` |
| MySQL object browser + JSON grid | `.artifacts/itest/TestShowcaseMySQL/014-mysql-groups.png` |
| PostgreSQL database picker reading the server list | `.artifacts/itest/TestPGConnectionPickerShowsDatabasesAndSchemas/` |
| MySQL database picker | `.artifacts/itest/TestMySQLConnectionPickerShowsDatabases/` |
| Simplified Chinese UI with real data | `.artifacts/itest/TestShowcaseChinese/005-chinese-grid.png` |
| Welcome page with saved connections | `.artifacts/itest/TestShowcaseWelcome/001-welcome-connections.png` |
| MariaDB SYSTEM VERSIONED table + SEQUENCE in the tree | `.artifacts/itest/TestMariaSystemVersionedTableListed/`, `.../TestMariaSequenceListed/` |
| PostgreSQL view DDL with enforcement options | `.artifacts/itest/TestDDLConstraintViewDefect/` |
| Grid edit apply confirmation and applied row | `.artifacts/itest/TestGridEditApply/` |
| Risk confirmation dialogs | `.artifacts/itest/TestGridRiskConfirmations/` |
| Export dialog and CSV/JSON exports | `.artifacts/itest/TestGridExportCSVJSON/` |
| Wide-character grid values | `.artifacts/itest/TestPGUnicodeRendering/` |
| MySQL functional-index catalog (was empty) | `.artifacts/itest/TestMySQLBrowseObjectTree/` |
| Result grid horizontal scrollbar (before / dragged / back) | `.artifacts/itest/TestResultsHorizontalScrollbar/` |
| PostgreSQL default schema resolving sales *and* public tables unqualified | `.artifacts/itest/TestPGDefaultSchemaKeepsPublicFallback/` |

The consolidated result table is written to
`.artifacts/itest-summary.md` by `.itest/run-all.ps1`.
