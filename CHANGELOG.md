# Changelog

All notable changes to TDB are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); version numbers follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.1] - 2026-11-27

### Added

- **Install scripts:** `install.sh` for Linux/macOS and `install.ps1` for Windows detect the OS/architecture, verify the release checksum and install `tdb`.

### Changed

- **License:** TDB is now distributed under the Apache License 2.0; see `LICENSE` and `NOTICE`.
- **Rename:** the binary, command directory, configuration directory and environment variables are now 	db, cmd/tdb, ~/.config/tdb and TDB_*; the old 	ermdb names are gone.

### Fixed

- **Release publishing:** the publish job downloads only `release-*` artifacts, so BuildKit's GHA cache artifact no longer fails the release after the archives were fetched.
- **Test cleanup:** the read-only menu test drains its queued settings write before `t.TempDir` cleanup, removing a race with the background writer.
- **PostgreSQL end-to-end test:** the connection helper now opens through the mutated model, so the request generation matches and the default-schema test passes again.
- **Release assets:** rerunning a tag deletes the previous release's assets before uploading the fresh archives, so renamed files do not linger.

## [0.9.0] - 2026-11-26

### Added

- **Multi-arch container image:** the release workflow publishes `ghcr.io/p-v-q/tdb` for `linux/amd64` and `linux/arm64` next to the six binary archives; a manual workflow dispatch can release an existing tag.

### Changed

- **README and demo refresh**: a clearer, easier-to-read README and a
  re-recorded demo GIF (regenerate with `make demo`).

### Fixed

- **Grid edits on case-sensitive schemas:** PostgreSQL now matches result columns to catalog columns case-sensitively; ambiguous case-folded matches on other dialects refuse the edit instead of updating the wrong column.
- **Nested `BEGIN` in manual transactions:** all three SQL drivers treat a second `BEGIN` as a no-op instead of forwarding it to the server (MySQL/MariaDB would implicitly commit the open transaction).
- **Grid apply failures:** a failed `Begin` no longer clears pending cell edits; it is reported through the same apply path.
- **Connection and metadata races:** metadata refreshes, connection opens/reconnects and connection-form pickers now carry request generations; stale responses can no longer overwrite a newer connection's catalog or form values.
- **Settings writes:** persistence goes through one ordered background writer, so an older snapshot cannot overwrite a newer one (including around vault encryption).
- **Closing a busy connection:** queries are cancelled first and connection close/rollback run off the UI goroutine with a bounded rollback context, so Disconnect/Quit no longer freeze on a long-running query.
- **Confirm dialogs on short terminals:** rendering and hit testing are clamped to the dialog height; an invisible button can no longer be activated by a click or Enter.

## [0.8.0] - 2026-11-26

Release highlights:

- **Encrypted connections**: the saved-connection list can be sealed behind a
  master passphrase (PBKDF2-SHA256, 600k iterations, AES-256-GCM); a locked
  session keeps every connection field out of the settings file, `.bak` copies
  and `-print-settings`.
- **A welcome page** with the recent connections, a built-in SQL file picker and
  a first-run experience that explains the keys.
- **Editable results and safe writes**: grid edits with pending markers and a
  confirmation listing the exact `UPDATE`s, plus manual transactions.
- **A richer connection form**: server-fed database/schema pickers,
  PostgreSQL default schema with the public fallback, SSL modes and grouped
  fields.
- **Fast, predictable grid**: multi-row selection, four export formats with a
  preview, a horizontal scrollbar for wide results, CJK-correct column widths
  and stable connection rows in the tree.
- **Polish across the board**: the localized plural forms, the file picker's
  keyboard path, bulk tab closing, double-click *View data*, and smooth pane
  resizing — with a 98-scenario integration campaign behind them.

### Added

- **Encrypted connections**: the whole saved-connection list — names, hosts,
  users, passwords, DSNs, schemas, SSL modes — can be sealed behind a master
  passphrase instead of sitting in clear text in `settings.json`. The first
  run offers to set one (refusing keeps the plain behaviour), and *Settings →
  Connection passphrase…* sets, changes or removes encryption at any time. The
  key is derived with PBKDF2-HMAC-SHA256 (600,000 iterations, standard
  library only) and the list is encrypted with AES-256-GCM; the envelope
  stores the parameters, so a future format upgrade does not strand old
  files. A clear-text **hint** is kept beside the ciphertext and shown at
  unlock — the interface refuses a hint that is the passphrase itself. With
  encryption on, startup shows the unlock dialog first; `Esc` continues
  locked, and a locked session cannot open, create, edit or test a
  connection. *Lock connections now* locks the running session immediately:
  open connections are not dropped, but the saved list becomes unreadable.
  Saving re-seals with the key already in memory and a fresh nonce, so
  preference-only saves do not re-run the KDF. The passphrase is the single
  key: there is no recovery, and forgetting it loses the saved list — the
  hint is the only help, and keeping a copy of the list in another file before
  turning encryption on is the escape hatch. While the vault is locked,
  `settings.json`, its `.bak` copy and `-print-settings` carry no connection
  fields, and a file that carries
  both the vault and a plain `connections` array is read as encrypted (the
  plaintext array is dropped, so an old backup cannot downgrade the file).
- **SSL mode in the connection form**: the server drivers' forms offer the
  words their driver understands — PostgreSQL `disable`, `allow`, `prefer`,
  `require`, `verify-ca` and `verify-full` (mapped to `sslmode=`), MySQL and
  MariaDB `false`, `preferred`, `skip-verify` and `true` (mapped to `tls=`;
  go-sql-driver has no `require`, and `skip-verify` carries what MySQL's own
  `ssl-mode=REQUIRED` does). `default` leaves the parameter to the driver.
  A hand-written DSN overrides the SSL mode, as it overrides every other
  field.
- **Grouped connection form**: the fields sit under four section titles —
  *Server*, *Credentials*, *Database* and *Advanced* — that are drawn like
  labels but are not focusable, so `Tab` steps through fields only. The value
  column is capped at 56 columns; a longer value scrolls inside it instead of
  widening the dialog.
- **A passphrase menu and dialog**: the Settings menu gains *Connection
  passphrase…* (which routes by state — setup on an unencrypted file, unlock,
  change, hint and removal on an encrypted one) and *Lock connections now*
  (shown only while unlocked). The setup dialog shows the length note ("at
  least 8 characters; 12 or more is recommended") and the clear-text hint
  warning while the passphrase is chosen.
- **Hover highlight in every list**: the pointer now highlights the row under
  it in the connection form's type and SSL-mode dropdowns, the settings
  dialog's language and theme dropdowns, the export dialog's format dropdown,
  the vault manager, the object tree, the tab bar, the welcome page, the file
  picker and the result grid rows. Hovering only draws: it never moves the
  keyboard cursor and never changes a value — the click is still what picks a
  row, and the selected row keeps its own style when the two land on the same
  row.
- **A welcome page**: a session with no file open starts on it, and closing
  the last tab brings it back. The page lists *New query*, *Open SQL File…*,
  one row per saved connection, *Settings* and *Keyboard shortcuts*
  (accelerators `n`, `o`, `s`, `f1`). `↑`/`↓` and `Tab` move, `Enter` runs
  the row, `Esc` closes the page while another tab exists — and typing a
  character turns the tab into a normal editor tab, so the page cannot trap
  someone who starts writing. While the vault is locked the connection rows
  are replaced by the locked notice.
- **Multi-row selection in the result grid**: `Shift+↑`, `Shift+↓`,
  `Shift+Home`, `Shift+End`, `Shift+click` and dragging across rows select a
  span of rows, and the summary line shows `N rows selected`. With a
  selection, `Ctrl+C` and `y` copy the selected rows as TSV without the header
  (without one they keep their old meaning — the caret's row and the caret's
  cell). `Esc` clears the selection before it leaves the pane. A selection
  addresses rows by their position in the view it was made on, so sorting,
  filtering, a new result and a plain caret move (the arrow keys, the wheel)
  drop it.
- **A built-in file and directory picker**: a modal list instead of a system
  dialog — an editable path line that accepts a full path, `~` and relative
  paths, and an entry list below it. `↑`/`↓`/`PgUp`/`PgDn`/`Home`/`End` move,
  `Enter` descends into a directory or picks a file, `Backspace` walks up to
  the parent (it deletes a typed filter character first), `Tab` moves between
  the path line and the list and `Esc` cancels. Typing filters the list
  case-insensitively; hidden files appear only when the filter starts with
  `.`. The picker never writes anything — it only reports the chosen path.
- **Open SQL file**: *File → Open SQL File…* (`Ctrl+O`, also offered on the
  welcome page) opens the picker on the folder of the active file (or the
  working directory), listing `.sql` files first — the hint line says so, and
  `Backspace` with an empty filter lifts the restriction. The chosen file
  opens in a tab named after it — on the welcome page it reuses that tab,
  otherwise a new one — with the file recorded, so `Ctrl+S` writes back to it.
  Files are read as UTF-8: a file larger than 8 MiB or one that is not valid
  UTF-8 is refused with a message in the status bar and opens no tab.
- **Export to a directory**: the export dialog gains a *Directory* row with a
  *Browse…* button that opens the directory picker; picking a folder replaces
  the folder part of the path and leaves the file name alone.

### Changed

- **Resizing a pane draws its chrome only while the divider moves**: the pane
  titles, the highlighted divider and a dim "resizing…" hint are rendered, and
  the editor/grid contents are laid out again on release. Re-rendering a large
  result grid or the editor on every mouse motion was what made resizing feel
  slow. A press without motion still shows the normal contents.
- **The result grid has a horizontal scrollbar**: when the columns are wider
  than the pane, a track with a thumb appears under the data rows. Dragging the
  thumb scrolls the grid sideways, clicking the track jumps to that column, and
  the bar takes one data row only while it is needed.
- **A PostgreSQL default schema keeps the public fallback**: the chosen schema
  goes first in the `search_path` and the server's default entries ("$user" and
  public) stay behind it, so unqualified queries find tables in the chosen
  schema *and* in public. Replacing the whole path made public tables vanish
  from unqualified queries — psql, DBeaver and DataGrip all keep the defaults.
- **The tab menu closes tabs in bulk**: right-clicking a tab (or `Shift+F10`)
  now offers **Close others**, **Close to the left**, **Close to the right** and
  **Close all** next to Rename/Duplicate/Close. The bulk entries act on the tab
  the menu was opened for, leave the active tab intact when it survives, and are
  shown only when they have something to close.
- **Opening a connection no longer moves its row**: the object browser keeps the
  saved order of the connection list instead of putting the live session first,
  so double-clicking a saved connection leaves its row where the user clicked it;
  the caret follows the opened connection and the tree scrolls only as far as
  needed, instead of jumping to the top.
- **Double-clicking a table or view opens its data**: the object browser's second
  click on a relation now runs the same bounded `SELECT` as **View data** in the
  context menu, in a tab of its own. Connections, schemas, groups, columns and
  resources keep their previous double-click behaviour (connect, fold/unfold,
  insert the name).
- **The connection form lists the server's databases and schemas**: the
  database field (PostgreSQL, MySQL, MariaDB) and the PostgreSQL default-schema
  field are no longer bare text inputs that assume the user knows the names by
  heart. ↓ on either field opens a list read from the server with the
  credentials already typed (a maintenance connection is used for the database
  list, so an unknown database name is exactly what the list is for), the
  schema list starts with "database default", and picking another database
  clears the schema choice because it says nothing about the new one. Typing a
  name the server did not offer still works, and a server that cannot be
  reached reports why in the status bar. The header and the object browser's
  connection row now name the PostgreSQL database a session is attached to, so
  the schema list below it has a clear parent.
- **Dialogs share one sizing rule**: the connection, settings, export and
  confirmation dialogs use the same geometry — width at least 60 and never
  wider than min(screen - 4, 100), height at least 12, centred between the
  menu bar and the status bar. A dialog that wants less room is padded up to
  that shape instead of drifting smaller, so the frames look alike everywhere;
  the compact exception is described next.
- **Short dialogs size to their content**: the unlock dialog and the smallest
  connection forms — the demo and SQLite file dialogs — no longer pad up to
  the shared minimum height; they use a compact floor of 8 rows instead of 12,
  so a one-field form is not padded into a wall of blank rows. The width rules
  are shared with every other dialog.
- **Language, theme and export format are dropdowns**: the settings dialog's
  language and theme rows and the export dialog's format row open an option
  list like the connection form's database type — `Enter`, `Space` or `↓`
  opens, `↑`/`↓` and `Home`/`End` move, `Enter` picks, `Esc` closes only the
  list, and the mouse opens, picks and closes. `←`/`→` keep cycling through
  the values with the list closed.
- **The export dialog previews the result**: under the format row it shows
  four lines of what the current format writes for the first rows of the
  result, so the choice between CSV, TSV, JSON and Markdown happens with the
  actual output in sight.
- **The connection form asks for the Database name**: the server drivers'
  former `Database` field is spelled `Database name` now, and the grouping
  puts it next to *Default schema*.
- **`Ctrl+C` copies the grid's selection from anywhere**: while the result
  grid has rows selected, `Ctrl+C` copies them as TSV even when the keyboard
  focus has moved to the editor or another pane — instead of following the
  editor's own copy-or-quit rules, which used to apply there (a grid
  selection was reachable only with the grid focused, so `Ctrl+C` elsewhere
  could quit with rows still selected).
- **The export dialog has four focus stops**: path, format, directory and
  *Browse…* — `Tab` and `↑↓` wrap over all four instead of the two the dialog
  had before the directory row.
- **Closing the last tab returns to the welcome page**: it used to leave a
  fresh empty tab behind; quitting is still `Ctrl+Q`.

### Fixed

- **Typed `BEGIN … COMMIT/ROLLBACK` is a real transaction in every driver**: the
  statement keyword is recognised (`BEGIN`, `START TRANSACTION`, `COMMIT`, `END`,
  `ROLLBACK`, `ABORT`, `SAVEPOINT`, `RELEASE`, skipping comments) and routed to
  the driver transaction instead of running `BEGIN` on a pooled connection that
  is then thrown away — scripts like `BEGIN; DELETE …; ROLLBACK;` used to commit
  the delete and report success, and `InTx()` lied about the state. MySQL and
  SQLite are covered too.
- **PostgreSQL `json`/`jsonb`, arrays and `timestamptz` show the server's text**:
  values are no longer decoded into Go maps/slices and re-encoded, which changed
  big integers (snowflake IDs), `1.0`, `1e2`, duplicate keys and key order,
  flattened multidimensional arrays and moved timestamps to UTC. `Infinity`
  prints as PostgreSQL prints it.
- **The row cap stops the query**: PostgreSQL and MySQL cancel the statement when
  the 1000-row cap is reached instead of draining a huge result set in the
  background.
- **SQLite in-memory DSNs use one connection**: `file::memory:` and
  `:memory:?params` (and any `mode=memory` URI) are treated like `:memory:`, so
  a table created on one connection is visible to the next statement.
- **MySQL rows-returning statements are not routed to Exec**: `CALL`, a
  parenthesised query expression, `ANALYZE`/`OPTIMIZE`/`REPAIR` and MariaDB
  `INSERT/UPDATE/DELETE … RETURNING` return rows to the grid, and `WITH … UPDATE`
  reports its affected count.
- **MySQL index definitions handle expression indexes**: functional and
  multi-valued indexes print `(expression)` parts, `FULLTEXT`/`SPATIAL` use the
  real `CREATE … INDEX` keyword form, and `DESC`/`INVISIBLE`/`IGNORED` survive;
  MariaDB sequences get a `SHOW CREATE SEQUENCE` definition.
- **PostgreSQL index and trigger definitions are schema-qualified** (rendered
  with an empty `search_path`), so they can be re-executed in another session.
- **SQLite catalog shows generated columns**: `PRAGMA table_xinfo` replaces
  `table_info`, PRIMARY KEY nullability follows SQLite's actual rules, and the
  `sqlite_` filter no longer hides user tables such as `sqlitex`; the affected
  count is read only for DML statements.
- **Grid edits cannot hit the wrong connection**: the apply confirmation is
  bound to the connection and result it was built from; a connection switch
  closes the dialog and drops the batch with a notice, and a background query
  can no longer clear the transaction/affected-row guard behind the dialog.
- **Pending grid edits and quit ask first**: closing a tab (or all/others),
  quitting and switching connections ask before discarding pending edits.
- **Completion no longer crashes on an unterminated multi-line identifier**
  (typing `"` + Enter caused a slice panic in `sql.AnalyzeContext`), and the
  splitter/lexer handle `END CASE`, MySQL backslash strings and invalid numeric
  exponents.
- **The first-run vault offer appears on a real fresh install** (the whole
  configuration directory may be missing; it is created at the first save).
- **Hardening**: the demo driver's `closed` flag is atomic, the vulnerable
  `golang.org/x/text` version was upgraded (govulncheck: no vulnerabilities), and
  a data race on the other drivers' `closed` flags was removed.
- **MySQL catalogs survive functional and multi-valued indexes**: a table with
  an index on an expression (MySQL 8 multi-valued `CAST(tags->'$[*]' …)`, or any
  functional index) made the whole object browser fail with "metadata not
  loaded", because the index introspection scanned a `NULL` column name into a
  string. Expression index parts are listed as "expression" now.
- **MariaDB system-versioned tables and sequences are in the object browser**:
  `TABLE_TYPE` is `SYSTEM VERSIONED` for the former and `SEQUENCE` for the
  latter; the driver accepted only `BASE TABLE`/`VIEW`, so a flagship MariaDB
  table could not be opened, generated SQL'd or have its definition viewed, and
  the tree had no Sequences group at all. The table pass accepts system-versioned
  tables and a sequence pass fills the new group.
- **PostgreSQL view definitions keep their enforcement options**: copying a view
  definition and re-running it used to drop `WITH (security_barrier=…)` and
  `WITH … CHECK OPTION` (both live in `pg_class.reloptions`), silently creating
  a weaker view. The statement now reproduces them where PostgreSQL accepts
  them.
- **A committed grid edit is drawn in its cell**: after Enter the grid marked
  the row as pending but kept drawing the old database value, so the edit was
  invisible until the confirmation dialog. Pending values (including pending
  NULLs) are drawn now.
- **Clipped cells say so**: a value wider than its column used to end silently,
  which looks like the value ends there. The cell, the column header, the type
  row and object-browser rows now end with `…`.
- **CJK and emoji cells are no longer clipped**: column widths were computed in
  runes while the grid draws terminal cells, so every wide glyph under-measured
  its column. Widths are measured in display cells of the cleaned text.
- **Keyboard-only export**: the export dialog advertises "⏎ export" but Enter on
  the path field did nothing; only the mouse button submitted. Enter exports.
- **Ctrl+S on a tab without a file says why**: the "no file to save" message
  was written into a value receiver's copy and silently dropped.
- **The selected saved connection is visible on the welcome page**: rows without
  an accelerator were never highlighted, so Down + Enter connected to a
  connection the user could not see selected.
- **A cancelled context never executes a statement**: the sqlite, PostgreSQL and
  MySQL query paths now check `ctx.Err()` before sending anything.
- **Windows input fixes**: Ctrl+/ (comment) and Ctrl+Space (completion) now work
  on Windows, where they arrive as a unit separator and a lone NUL rune; typed
  key events no longer insert invisible control bytes into SQL.
- **The file picker's buttons are reachable with Tab**: list → path → buttons,
  ←/→ picks the primary action or Cancel, Enter runs it.
- **Plural forms**: "1 row in 1ms" instead of "1 rows in 1ms", "1 schema / 1
  table", "1 pending change", "Applied 1 change", "1 statement", and the same
  for selected rows, read-only blocked statements, exported and copied rows (en
  and zh; the Chinese header also said "schema" instead of "模式").
- **A scoped write is not labelled "unsafe"**: the confirmation dialog now
  distinguishes a plain write from a statement that can touch rows it never
  named.
- **A missing settings file whose parent is a regular file is not a first run**:
  Windows reports such a path as "not found" (POSIX reports ENOTDIR), which
  offered the vault setup for a file that can never be created.
- **The test suite runs on Windows**: tests that assumed POSIX permission bits,
  `/tmp`-style paths, control characters in file names, or a nanosecond
  deadline now skip or use platform-appropriate expectations; the SQLite
  duration test measures a slow statement instead of a sub-millisecond one.
- **The MySQL driver advertises the sequence kind**: it serves MariaDB too, and
  the empty group is hidden on MySQL; the live driver test checks exactly that.

- **The `mysql://` URL's parameters reach the driver**: query parameters on a
  composed or pasted MySQL/MariaDB URL (`tls`, `timeout`, `readTimeout`,
  `writeTimeout`, `parseTime`, `loc`, `maxAllowedPacket`, `multiStatements`,
  `rejectReadOnly`, …) were kept as connection properties and never applied to
  the driver's configuration, so `tls=` silently left encryption off. They are
  mapped to the go-sql-driver config now; the defaults of a native DSN
  (`parseTime`, `utf8mb4`) still apply when a parameter is absent.
- **MySQL and MariaDB no longer offer `require` as an SSL mode**: go-sql-driver
  has no `tls=require`, so a saved connection with that value failed to open
  with `unknown config name: require`. The form lists only the values the
  driver accepts, and `skip-verify` stands for MySQL's `ssl-mode=REQUIRED`.
- **Settings saves are marshalled on the UI goroutine**: `persistSettings`
  used to hand a copy of the settings to a background command, so the
  background marshal could read the connection slice while the interface
  edited it in place, and re-seal with a key that locking had just zeroed.
  The bytes are sealed up front and the command only writes them.
- **An unreadable envelope no longer reports a wrong passphrase**: an
  unsupported envelope version or cipher is shown as itself, so a file this
  build cannot read does not send the user back to typing the passphrase.
- **A stale plain-text backup is dropped once encryption is on**: a `.bak`
  written before a passphrase was set would otherwise sit next to the sealed
  file with every password in clear text; a backup that is already ciphertext
  is kept.
- **A hint equal to the passphrase is refused on every path**: the *change
  hint* dialog validates it the same way setup and change do, by
  trial-opening the envelope.
- **Locked connections are refused at the entry points**: opening, testing
  and deleting a connection and saving the connection form check the vault
  themselves instead of relying on the menus being hidden.
- **A first-run prompt with no place to save**: when no settings path can be
  resolved, the passphrase dialog is no longer offered, because nothing
  would have been written down.

### Release engineering

- The module path is `github.com/p-v-q/tdb`, so `go install
  github.com/p-v-q/tdb/cmd/tdb@latest` resolves to this repository.
- `docs/demo.gif` with a recorded terminal demo, `docs/img/` screenshots, and
  `.itest/cmd/demo` + `.itest/demo/make_gif.py` to regenerate it (`make demo`).
- `docs/release.md` (release checklist) and `.github/workflows/release.yml`
  (tag → version/format/vet/test gate → cross-build matrix → `SHA256SUMS` →
  GitHub release).
- `.gitattributes` keeps Go sources LF on Windows, where a CRLF checkout made
  `gofmt -l` flag every file and `make check` fail locally.

## [0.7.0] - 2026-10-22

### Added

- **MySQL and MariaDB support**: the `mysql` and `mariadb` driver names now open
  real connections (`go-sql-driver/mysql`, pure Go) with query execution,
  `information_schema` introspection (tables, views, columns, routines,
  triggers, indexes) and capability declaration. Previously both names were
  placeholders that refused to open.
- **View object definitions**: the object browser's context menu offers *View
  definition* for tables, views, functions, procedures, triggers, indexes and
  sequences; the DDL opens in a new tab. Drivers expose it through the optional
  `driver.DefinitionProvider` interface (`sqlite_master.sql`, PostgreSQL
  `pg_get_*def`, MySQL `SHOW CREATE …`).
- **Read-only mode**: toggled from `Query → Read-only Mode` or the settings
  dialog and persisted as `"readOnly"` in `settings.json`; write and DDL
  statements are blocked before they reach the database.
- **Execution guardrails**: statements are classified by risk (`sql/risk.go`);
  DDL and unsafe writes (an `UPDATE`/`DELETE` without `WHERE`, `DROP`,
  `TRUNCATE`) show a confirmation dialog listing exactly the statements that
  need it.
- **JSON and Markdown export**: the export dialog (`F7`) now cycles CSV, TSV,
  JSON (array of objects, SQL `NULL` as a JSON `null`) and Markdown (a
  GitHub-style table with pipes and newlines escaped).
- **Keyboard focus for panes**: `Alt+1`, `Alt+2` and `Alt+3` focus the object
  browser, the editor and the results pane, also available as *Focus Objects*,
  *Focus Editor* and *Focus Results* in the View menu. A hidden pane is shown
  first, focusing the tree with no usable selection selects and reveals its
  first row, and focusing a pane other than the editor closes the completion
  popup. The shortcuts also work while a menu is open: the menu closes and the
  command runs.
- **Object tree filter**: `/` while the browser has the focus filters the tree
  as you type, matching object and schema-qualified names, function signatures,
  schema and group labels, and table, column and object comments. The query is
  shown in the pane title, the ancestors of a match open automatically, folding
  leaves the stored tree state alone, and `Enter` keeps the filter while `Esc`
  clears it. While the input is open it owns the keyboard; a query with no
  matches says so in the UI language.
- **Cell editing in the result grid**: a result that comes from a single-table
  `SELECT` of a table with a primary key can be edited in place. `e` starts
  editing the cell under the caret, `Enter` keeps the new value, `Esc` drops it,
  and `\N` types a NULL while an empty text stays an empty string. The changes
  wait in memory — the row gets a `*` next to its number and the summary counts
  them — until `Ctrl+S` (or *Query → Apply Changes*) opens a confirmation
  dialog with the exact `UPDATE`s. The new value and the primary key are sent
  as bound parameters, so nothing that was typed becomes part of the SQL, and
  the query runs again afterwards, so the grid shows what the database holds
  now. *Query → Discard Changes* throws the pending edits away, and read-only
  mode blocks editing and applying alike.
- **Generated SQL from the object browser**: the context menu of a table offers
  *Generate SELECT*, *Generate INSERT*, *Generate UPDATE* and *Generate
  DELETE* — a view only gets `SELECT` — plus *View data*, which opens
  `SELECT * … LIMIT 200` and runs it. The statement opens in a tab of its own,
  the identifiers are quoted the way the dialect wants them, and the
  placeholders are the dialect's (`$1` on PostgreSQL, `?` on the others).
- **Named tabs**: a tab opened from the object browser carries the
  schema-qualified name of the object (`public.users`), a generated statement
  adds the kind of statement in brackets (`[SELECT]`, `[INSERT]`, `[UPDATE]`,
  `[DELETE]`, `[data]`), and a tab opened from the query history takes the
  first line of the statement. Right-click a tab — or press `Shift+F10` while
  the editor has the focus — for *Rename…*, *Duplicate* and *Close*; an empty
  name in the rename box restores the automatic one.
- **A Key Bindings overlay**: `F1` (or *Help → Key Bindings*) opens a floating
  list of every shortcut the client understands — the global ones from the menu
  bar and the keys the object browser, the editor and the result grid handle
  themselves. The list is generated from the command table, so it cannot
  advertise a binding that does not work; `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End`
  and the mouse wheel scroll it, and `Esc` closes it.
- **A default schema per connection**: a PostgreSQL connection can name the
  schema it works in, and unqualified names in the editor resolve there
  afterwards. The schema is set on every connection of the pool and quoted as
  an identifier. A schema the database does not have is reported, and the
  connection keeps the server's default; the title bar shows the default
  schema and the object browser marks it.
- **Passwords from an environment variable**: the connection form of the server
  drivers has a *Password from environment variable* field. The value of that
  variable is used as the password and is never written to the settings file —
  only the name of the variable is. A connection whose variable is not set is
  refused before it is opened, and the status bar names the missing variable.
- **A filter for the query history**: `/` inside the history overlay filters
  the list as you type, matching the SQL text and the connection name; `Esc`
  clears the filter.
- **The lifecycle of the live connection**: the row of the connected database
  has *Reconnect* and *Disconnect*. A disconnected connection can be opened
  again, and the client closes the connection when it quits.
- **Transaction control**: the Query menu offers *Manual Commit* to switch
  between auto-commit and manual commit, and *Commit* (`F9`) and *Rollback*
  (`F11`) to end the open transaction. In manual-commit mode every batch runs
  inside a transaction that stays open until it is committed or rolled back by
  hand — the pending edits of the result grid included. The status bar shows
  `manual commit` while the mode is on and `● uncommitted` while work is
  pending, dropping back to auto-commit with an open transaction is refused,
  and switching to another database rolls the transaction back loudly instead
  of dropping it silently. A driver without transaction support keeps the
  auto-commit behaviour, and the menu items stay hidden.

### Changed

- **Compound statements survive the SQL pipeline**: `CREATE VIEW`, `CREATE
  FUNCTION`/`PROCEDURE` with `$$…$$` or `$tag$…$tag$` bodies, `DO` blocks and
  `BEGIN…END` trigger/procedure bodies are no longer broken apart by statement
  splitting, rewritten by the formatter or mis-highlighted.
- **Completion knows DDL positions**: procedure, trigger, index and sequence
  names from the catalog are completion candidates, and `DROP`/`ALTER`
  statements complete only the object kind the statement actually names.
- **The database type is a dropdown**: in the new/edit connection form, the
  type field opens an option list with `Enter`, `Space` or `↓` (or a click).
  `↑`/`↓` and `Home`/`End` move the highlight, `Enter`/`Space` picks a type and
  `Esc` only closes the list. `←`/`→` still cycle through the types while the
  list is closed. Types are shown with their product label (`PostgreSQL`,
  `MySQL`, …) while the saved value stays the driver ID.
- **The status bar follows the focused pane**: the object browser shows the
  position of the selected row and its name (`Object 3/18  users`), or the
  filter query while its filter is open (`Filter /users`); the right-hand side
  lists the keys of the focused pane, so the hints are never about a pane that
  is not in use.
- **Connecting is bounded by a deadline**: opening a connection and loading the
  first metadata share a 15-second deadline, so a server that accepts the
  connection but never answers is reported as a timeout instead of hanging the
  interface. A query that fails because the connection is gone says so instead
  of a plain error.

### Fixed

- **PostgreSQL table definitions load again**: the `CREATE TABLE` query failed
  with SQLSTATE 42803, so *View definition* reported an error and the table
  looked as if it had no primary or foreign key. Definitions now include the
  primary key, unique, check, foreign-key and exclusion constraints (with
  `ON DELETE` / `ON UPDATE`), identity and generated columns, defaults and
  `NOT NULL`, and the table name is schema-qualified.
- **Extension-provided functions are visible**: functions and procedures
  installed by an extension (`pg_trgm`, PostGIS, …) now appear under Functions
  and Procedures and are offered by completion, so a database whose routines
  come from extensions no longer looks empty. `pg_catalog` and
  `information_schema` stay hidden.
- **PostgreSQL definitions always name their schema**: the same table produced
  two different `CREATE` statements depending on the session it was read in,
  because the server prints a relation without its schema as long as it is
  visible in the session's search path. The definitions are read with an empty
  search path now, so every relation carries its schema and the statement means
  the same thing everywhere.
- **MySQL and MariaDB definitions can be replayed**: *View definition* no
  longer prints the `DEFINER=`, `ALGORITHM=` and `SQL SECURITY DEFINER`
  clauses. They belong to the server that created the object and keep the
  statement from running under another account.
- **The MySQL object tree is not empty any more**: a routine that returns no
  table structure — every procedure, every function with a scalar result — made
  the whole metadata load fail ("converting NULL to string is unsupported") and
  left the object browser without a single table. The catalog loads again, and
  the routines are listed with their details.
- **A broken settings file is kept**: a settings file that cannot be parsed is
  copied to `<path>.bak` before it is overwritten, and the status bar says
  where the copy went, so a hand-edited file is never lost silently.
- **A query with a `WHERE` or an `ORDER BY` can be edited**: such a clause was
  read as an alias of the table, so the result of `SELECT … FROM users ORDER BY
  id` was refused as uneditable. The table is found behind its clauses now.
- **Editing a cell keeps what you typed**: a cell that already holds a pending
  change opens with that value again (a pending NULL shows as `\N`), instead of
  the value the database still holds.
- **The reason a result cannot be edited is accurate**: a result whose primary
  key is not part of the result now says so, instead of blaming the query for
  it.
- **`Shift+F10` works in a real terminal**: a terminal cannot send the
  `Shift+F10` sequence — most send the `F13` one instead, which Bubble Tea
  v1 decodes as `KeyF13`. The application treats `F13` as `Shift+F10` now, so
  the context menu of a tree row, a grid cell and a tab opens with the key
  the terminal actually delivers.
- **`Ctrl+Enter` applies the pending grid changes**: a terminal whose tty has
  the software flow control (IXON) enabled swallows `Ctrl+S` (0x13) before it
  reaches the application, so the pending changes could not be applied there.
  `Ctrl+Enter` (which the terminals send as `Ctrl+J`) applies them as well.

[unreleased]: https://github.com/p-v-q/tdb/compare/v0.9.1...HEAD
[0.9.1]: https://github.com/p-v-q/tdb/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/p-v-q/tdb/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/p-v-q/tdb/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/p-v-q/tdb/releases/tag/v0.7.0
