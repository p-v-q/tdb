# Architecture

TDB is a terminal database client built around three rules:

1. SQL semantics and database access do not depend on the UI.
2. Geometry and hit testing share one pure layout function.
3. Every user action has one implementation, regardless of whether it was
   triggered by a key, a menu, the toolbar or the mouse.

## Layers

```
cmd/tdb          composition root: flags and driver blank imports
        │
        ▼
ui                  Bubble Tea presentation and interaction
        │
        ├── complete ── sql, catalog, dialect
        ├── history, settings, i18n
        │        └── settings ── vault (at-rest encryption)
        └── driver (interfaces only)
                 │
                 ▼
          driver/sqlite, driver/postgres, driver/mysql, driver/demo
```

Dependencies point downwards. `ui` imports the `driver` interfaces but never a
concrete driver; `cmd/tdb` is the only package that chooses implementations.
A driver imports `driver`, `catalog` and `dialect`, never `ui`.

The `ui` package is further split by responsibility:

| Files | Responsibility |
| --- | --- |
| `model.go`, `update.go`, `status.go` | Bubble Tea state and message loop |
| `query.go`, `connection.go`, `tabs.go` | use cases: run, connect, tabs |
| `editor*.go`, `selection.go` | editor state, commands, key dispatch |
| `results*.go` | result state, input, rendering, copying |
| `grid_edit.go` | editing a cell of the result grid and the pending changes |
| `sqlgen.go` | starter SQL for a relation (SELECT/INSERT/UPDATE/DELETE) |
| `objects.go`, `context_menu.go` | object tree, its filtering and context menu |
| `result_menu.go` | result-grid context menu |
| `menu*.go`, `toolbar.go` | command table, shortcuts and dispatch |
| `layout.go`, `panes.go`, `mouse*.go` | geometry, pane focus and pointer dispatch |
| `modal.go`, `select_list.go` | the shared dialog size rule and the dropdown control |
| `file_picker.go` | the built-in file/directory picker (open SQL file, export folder) |
| `open_file.go` | *Open SQL File…*: size cap, UTF-8 check, routing a picked path |
| `welcome.go` | the welcome tab: page layout, action rows, conversion to an editor tab |
| `vault_dialog.go` | the passphrase dialogs (setup, unlock, change, remove) |
| `render.go`, `theme.go` | composition and styling |
| `*_dialog.go` | modal forms and pickers |

## State ownership

`Model` owns application-wide state: settings, the live connection, layout,
menus, dialogs and the open tab list. Each `session` owns what belongs to one
editor tab: its text buffer, caret, selection, completion popup, scroll offsets
and result grid. A session also keeps every result set from its last execution;
only the active set lives in the promoted `result` field, and the others keep
their own sort, filter, scroll and caret state in `resultSets`. Switching tabs
swaps a pointer, so editor helpers always act on the tab the user is looking at.

A session whose `welcome` flag is set is the welcome tab (`welcome.go`): an
ordinary empty buffer whose editor body is drawn as an action list — *New
query*, *Open SQL File…*, the saved connections, *Settings*, *Keyboard
shortcuts*. The page only stands in front of the buffer, so converting it to an
editor tab clears the flag and nothing else; a key the page does not own
(a character, a paste) is handed to the tab untouched, which is why typing can
never strand a user on the page.

Background commands return messages carrying the `*session` that started them.
A slow query on tab A therefore cannot write its rows into tab B.

## Catalog resources

The catalog is the only database-specific metadata the UI sees. It divides
objects into two groups:

- **Relations** (`Schema.Tables`): tables, views and materialized views. They
  own columns, so SQL completion and column browsing use the same relation
  list.
- **Resources** (`Schema.Resources`): functions, procedures, triggers, indexes
  and sequences. They share the `catalog.Resource` descriptor with explicit
  `Kind`, `Owner`, `Detail` and `Comment` fields.

`catalog/resources.go` defines the shared vocabulary. `catalog.ResourceKinds`
defines the display order, and `ResourceKind.IsRelation` separates object kinds
that own columns from leaf resources. `Catalog.Capabilities` is the driver's
declaration of which kinds it can actually introspect. The object browser
iterates the canonical order, skips unsupported or empty kinds, and renders one
foldable group per kind. It has no per-driver branches.

Adding a resource kind is therefore a data change plus a driver query:

1. add the `ResourceKind` constant and its position in `ResourceKinds`;
2. teach the relevant driver to fill `Schema.Resources` and declare the kind in
   `Capabilities`;
3. add the localized group label and, if needed, an icon mapping.

Databases keep their own introspection queries in their own package. SQLite
reads `sqlite_master`; PostgreSQL reads `pg_proc`, `pg_trigger`, `pg_index`,
`pg_class`, `pg_namespace`, `pg_attribute`, `pg_attrdef` and `pg_constraint`;
MySQL and MariaDB read `information_schema`. PostgreSQL lists every routine
outside the system schemas, extension-provided ones included, and hides only
`pg_catalog` and `information_schema`. None of these queries leak into `ui` or
`complete`.

## The object tree

`objectsRows` is the single projection of the catalog into tree rows: rendering,
hit testing, the context menu and the filter all index the same slice, so a row
means the same thing to every consumer. `objectsState` keeps the user's
expansion state in the `collapsed` and `expanded` maps and caches no drawn rows.

A filter (`/` in the browser) rebuilds the rows from the matches instead of
reading those maps: connections stay, a schema or group hit shows one level of
members, and a table hit opens its columns while a column hit opens its table.
Because the projection decides for itself what is open, the stored maps are
read-only while a filter is in effect — `objectsSetOpenIfUnfiltered` drops user
toggles, and the twisty is drawn from each row's `hasChildren` field rather than
from the maps. Clearing the filter therefore restores exactly the tree the user
left. A group's count is computed while the rows are built, so it matches the
rows underneath it even when filtering.

The filter input is modal: while it is open it consumes every key before the
menu accelerators and global shortcuts, so `Ctrl+A` or `Alt+1` cannot reach the
buffer or move the focus mid-word. `Enter` keeps the filter and gives the keys
back to the tree; `Esc` clears it. The query is appended to the pane title
through `renderPaneTitleWith`, which truncates the title side so a long query
cannot push out the close button.

## Compound statements

The SQL layer must never split or reformat the body of a compound statement.
Two shapes matter:

- **Dollar-quoted bodies** (PostgreSQL): `CREATE FUNCTION … AS $$ … $$`,
  `CREATE PROCEDURE …`, `DO $$ … $$`, `CREATE TRIGGER … EXECUTE FUNCTION`. The
  lexer treats `$$…$$` and `$tag$…$tag$` as a single string token, so the body
  is opaque to splitting, highlighting and formatting.
- **Block bodies** (SQLite/MySQL): `CREATE TRIGGER … BEGIN … END;`,
  `CREATE PROCEDURE … BEGIN … END`. The splitter counts `BEGIN` / `END`
  (and `CASE` / `END`) nesting inside a statement and only breaks on a
  semicolon at nesting depth zero.

Rules that keep this predictable:

1. The lexer is the single place that knows about literals and quoted regions;
   the splitter and the formatter only consume its tokens.
2. A statement is split on a semicolon only at block depth zero, and never
   inside a string, quoted identifier or comment.
3. The formatter never rewrites tokens inside a body it cannot parse: a
   dollar-quoted body is copied verbatim, and a block body keeps its internal
   layout (it is emitted between `BEGIN` and its matching `END` unchanged).

## Object definitions

Browsing an object should also show its DDL. Introspection snapshots stay
cheap, so the definition is fetched on demand through an *optional* interface
in `driver`:

```go
type DefinitionProvider interface {
    Definition(ctx context.Context, ref DefinitionRef) (string, error)
}
```

A driver that can fetch definitions implements it (`sqlite_master.sql`,
`pg_get_viewdef` / `pg_get_functiondef` / `pg_get_triggerdef` / `pg_get_indexdef`,
MySQL `SHOW CREATE …`). The UI discovers it with a type assertion and offers
"View definition" in the object context menu only when the connected driver
provides it; the returned DDL opens in a new tab so it can be copied, edited
and executed. Databases that cannot produce DDL simply do not implement the
interface — no error paths and no UI branches per database.

Tables are the one definition PostgreSQL cannot print itself, so the driver
assembles `CREATE TABLE` from `pg_attribute`, `pg_attrdef` and
`pg_get_constraintdef` — primary key, unique, check, foreign key and exclusion
constraints, with `ON DELETE` / `ON UPDATE` and deferrability — plus identity
and generated columns from `attidentity` / `attgenerated`. Catalog helpers are
schema-qualified so an unusual `search_path` cannot shadow `format` or
`string_agg`. It is a readable reconstruction, not a dump: `PARTITION BY`,
inheritance, identity sequence options and per-column storage attributes are
out of scope, though a partitioned table (`relkind = 'p'`) still produces a
statement instead of an error.

## Input and rendering

`layoutState.compute` maps the model onto `paneRects`. Rendering and mouse hit
testing both use those rectangles, so what is drawn is what can be clicked.

Input follows the same priority everywhere:

1. menu bar and open menus
2. modal dialogs
3. context menus and the find prompt
4. focused pane
5. global shortcuts
6. editor text input

Two overlays are more impatient than the list: the key-bindings overlay and
the vault dialog come first, and the file picker has a layer of its own right
below them — it can sit on top of the export dialog, so its keys and its
motion are claimed before anything underneath sees them.

`layoutState.focus` names the pane that owns the keyboard. `focusPane` is the
one way a command moves it: it reveals the target pane first (one-way, so
repeating the command never hides anything), then sets the focus, selects the
first tree row when the selection is missing or out of range and scrolls it into
view, and closes the completion popup for every pane except the editor. Only a
reveal is persisted, so focus changes do not rewrite the layout settings.

Dialogs and context menus are overlays. Their geometry is a pure function shared
by the renderer and hit testing, and their key handler consumes every key except
its own shortcuts, so typing cannot leak into the editor behind them. The file
picker is built the same way: `pickerLayout` derives its rows and rectangles
(and `modalSize` its frame) from one computation, so rendering and hit testing
cannot disagree.

Lists share one pointer rule. Each keeps two positions — `sel`, the row the
keyboard cursor and `Enter` act on, and `hover`, the row under the pointer
(`-1` when the pointer is elsewhere) — and the renderer may draw either,
with the selected style winning when both land on the same row. Motion goes to
the open modal first and is ignored outside it, then to the menu bar, the
toolbar, the tab bar and the panes. Hover only draws: it never moves `sel`,
never moves the caret, never changes a value and never opens anything. That one
rule covers the connection form's dropdowns, the settings and export
dropdowns, the vault manager, the object tree, the tab bar, the welcome page,
the file picker and the result grid rows.

## Commands and shortcuts

`menuDefs` in `menu_defs.go` is the single source of truth for menu labels,
accelerators, global shortcuts and actions. `keymapAction` derives the global key
map from it. The menu bar, keyboard and toolbar all call `runMenuAction`, so a
shortcut shown in a menu is the shortcut that actually runs.

A global shortcut pressed while a menu is open closes the menu and runs the
action, so `Ctrl+B` or `Alt+1` is never merely swallowed by the open menu.
Alt+letter menu switches and plain-letter mnemonics keep their keys because
they are resolved first.

The same pattern applies to context menus: `open*Menu` builds the actions for a
target, `run*MenuAction` performs them, and one function renders the result.

The **Key Bindings** overlay (`keybindings_dialog.go`) is generated from the same
table: the menu rows are read from `menuDefs` on every draw, so the help can
never advertise a binding the command table does not have. The keys the panes
own themselves (the tree's `/` filter, the grid's sorting) are listed
separately, because they are not menu items.

## The result grid

`results.go` owns the grid of one tab: the result snapshot, the column widths,
the filter, the sort and the caret. `grid_edit.go` adds the one thing a grid
may change — the data itself — under three rules:

1. **Only a single-table `SELECT` is editable.** `resolveSource` parses the
   statement, resolves the table in the live catalog and maps every result
   column to a catalog column. A join, a sub-query, a set operation, a CTE, a
   missing key column or a table without a primary key leaves the grid
   read-only, and the status bar names the reason. The primary key is what
   addresses exactly one row, so the rules are not negotiable.
2. **Changes stay in memory until the user approves them.** A pending change
   records the row in the *original* result order and the column, so sorting
   and filtering do not move it. The cell is marked in the grid and the
   summary counts the pending changes.
3. **The statements are parameterized.** An edit becomes
   `UPDATE "public"."users" SET "email" = $1 WHERE "id" = $2` with the value
   and the key as arguments — never as text in the statement. The batch runs
   through the same path as any other execution, so the manual-commit mode
   keeps it inside the open transaction, and the confirmation dialog shows the
   SQL before it runs. After a successful apply the original `SELECT` runs
   again; a failed apply keeps the pending changes, because losing the user's
   typing would be worse than the error.

**Row selection** is a span of *view* positions between `selAnchor` and
`cursorRow` — both in view order, so a highlight can never slide onto other
data. `selKey` fingerprints the view the selection was made on (the filter and
the sort), and `selectionSpan` reports no selection once it no longer matches,
so a sort, a filter or a new result drops the span without each of those
callers knowing it exists. `Shift+↑`/`Shift+↓`/`Shift+Home`/`Shift+End`,
`Shift+click` and a pointer drag extend it, `Esc` clears it before the pane
gives the focus back, and a plain caret move clears it the way a plain move
ends the editor's selection — `moveCursor` is what the arrow keys *and* the
wheel go through. The summary counts the rows, and the span is copied as TSV
without a header: `Ctrl+C` does it with the grid focused or not (it refuses to
quit while rows are selected), and `y` does it while the grid has the focus.
`hover` and `rowDrag` are the pointer side of
the same state; hover only draws, and it is dropped when the rows scroll under
a still pointer.

`sqlgen.go` holds the pure generators for the object browser: `SELECT`,
`INSERT`, `UPDATE` and `DELETE` for a relation, plus the bounded `SELECT *`
that "View data" runs. They take a dialect and a `catalog.Table` and return
text, so every dialect can be tested without a terminal. Identifiers always go
through `dialect.QuoteIdent`; placeholders follow the dialect (`$1` on
PostgreSQL, `?` elsewhere) without adding a method to the dialect interface.

## Connections and secrets

Opening a connection is a background command with a deadline: `connectTimeout`
bounds both the connection and the first catalog load, so a host that accepts
the TCP connection and then stalls cannot pin the UI. The connection row's
context menu offers the whole lifecycle — Reconnect, Disconnect and, after a
disconnect, Connect again — and quitting closes the live connection instead of
leaving it to the server.

A password can come from the settings file or from an environment variable
named by `settings.Connection.PasswordEnv`. The variable wins when it is set,
and the resolved value lives only in the driver configuration: the settings
file keeps the *name* of the variable, never the secret. A missing variable is
reported before the connection is attempted, so the user is told which name to
set instead of seeing an authentication error.

Writing the settings never destroys an unreadable file: `SaveWithBackup` keeps
a copy of a file that cannot be parsed as `<path>.bak` before overwriting it,
and the UI says where the copy went.

### The vault

The saved connections can be encrypted as one blob (`vault`, see
`docs/secrets-encryption.md`). The design rule is that `settings.Settings`
holds either plain-text `Connections` (no passphrase yet) or a `Vault`
envelope, never both as a source of truth:

- `Load` discards `connections` whenever a vault is present, so adding a
  plain-text copy to a sealed file cannot downgrade it.
- While locked, the in-memory connection list is empty; nothing can be
  connected, edited or saved. `Unlock` decrypts once and keeps the derived
  key, so later saves only reseal (fresh nonce, same salt) instead of paying
  the KDF again.
- Every path that writes the file — `Save`, `SaveWithBackup` and `JSON()`
  (used by `-print-settings`) — builds a disk copy through `marshalForDisk`.
  A settings file therefore never contains a connection field in clear text
  once a passphrase is set, and `.bak` copies inherit that property.
- The passphrase itself is never stored, never put in a status message, and
  never logged; the hint is clear text by design and the UI says so.

The envelope records its KDF and cipher parameters, so a future build can raise
the iteration count and still open old files.


## Adding a feature

- **A result action**: add a `resultMenuAction`, a label key in `i18n`, an entry
  in `openResultMenu`, and a case in `runResultMenuAction`. Put data operations
  in `results_copy.go` or `results.go`, not in the menu renderer.
- **A dialog**: keep its state in a small struct with an `open` flag, give it a
  pure rectangle function, route keys before the editor, and render it as an
  overlay in `render.go`.
- **A driver**: implement `driver.Connection`, register a factory from `init()`,
  and link it with a blank import in `cmd/tdb`.
- **A catalog resource**: add a `catalog.ResourceKind`, include it in
  `ResourceKinds`, fill it from the driver's own system-catalog query, and add
  the localized group label. The UI grouping, capability filtering and tree
  rows are generic.
- **A shortcut**: add it to the relevant `menuItem`; do not add a second key map.
- **A grid action**: put the state in `resultState` (or in `grid_edit.go` when
  it changes data), keep the data operation out of the renderer, and run it
  through `executeText` so the transaction, the read-only gate and the
  confirmation dialog still apply.

## Testing

`go test ./...` drives the UI headlessly. Layout, menu, mouse, result-grid and
dialog tests exercise the model directly; `ui/e2e_test.go` runs a full loop
against SQLite. Catalog tests cover capability filtering and resource counts;
the SQLite driver tests assert trigger/index introspection. The PostgreSQL and
MySQL drivers have optional integration tests guarded by `TDB_POSTGRES_DSN`
and `TDB_MYSQL_DSN`; the PostgreSQL one also exercises every PostgreSQL
resource kind.

`make check` runs gofmt, `go vet` and the test suite. `make race` runs the race
detector over the stateful packages. Both run in GitHub Actions on every push
and pull request. A second CI job runs the suite against real PostgreSQL and
MySQL servers (the `integration` job in `.github/workflows/ci.yml`): the
integration tests are guarded by `TDB_POSTGRES_DSN` and
`TDB_MYSQL_DSN`, so the same command runs everywhere and only finds the
servers when they are there.
