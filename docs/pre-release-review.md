# Pre-release review — 0.8.0

Three independent reviews of the 0.8.0 tree were run before tagging: the driver
layer, the UI state machine, and the infrastructure/secrets/release bits. Every
finding was triaged; the table below says what was fixed in this release and
what is deliberately left for later (tracked in
[`product-gaps.md`](product-gaps.md)). The full reports live in the review
workspace (`.artifacts/review/`), the fixes are part of the 0.8.0 changelog.

## 0.9.0 final review

A fourth, pre-0.9.0 review covered the release candidate again. It found two
blockers (case-insensitive column mapping on PostgreSQL, and a second `BEGIN`
inside a managed transaction causing MySQL to implicitly commit) plus six
high-severity issues in asynchronous UI state and confirm-dialog hit testing.
All eight were fixed with regression tests and are listed under
`## [0.9.0]` in [`CHANGELOG.md`](../CHANGELOG.md). The previous sections below
record the 0.8.0 review and its resolution.

## Release blockers — all fixed

| Area | Finding | Fix |
| --- | --- | --- |
| sql | `AnalyzeContext` panicked on an unterminated multi-line quoted identifier (`"` + Enter), reachable after every keystroke | no column arithmetic across rows; fuzz + regression tests (`sql/context.go`, `sql/context_test.go`) |
| drivers | User-typed `BEGIN … COMMIT/ROLLBACK` was silently neutralised: `BEGIN` ran on a pooled connection that was then destroyed, later writes autocommitted, `ROLLBACK` reported success | shared `driver.TransactionControl` parser; all three SQL drivers route transaction-control statements to their transaction manager; live PG/MySQL/SQLite tests prove rollback works and `InTx()` is truthful |
| ui | The grid-apply confirmation was not bound to its connection/result: a connection switch while the dialog was open executed the UPDATEs on the new connection | the batch travels inside the dialog, is re-validated at accept time and is executed on the captured connection; a switch closes it with a notice |
| ui | A background query outcome arriving while the apply dialog was open cleared the batch's transaction/row-count guard | the batch is carried by the dialog; Begin/Commit and affected-row checks always run (tests assert Begin/Commit counts) |
| build | 14 committed Go files were not gofmt-clean, so `make check`/CI was red | full `gofmt -w` sweep; `.gitattributes` keeps Go sources LF on Windows so the check is meaningful locally too |
| deps | `golang.org/x/text v0.29.0` had two reachable vulnerabilities (panic / infinite loop) via pgx | upgraded to v0.41.0; `govulncheck ./...` reports no vulnerabilities |

## Fixed in 0.8.0

| Severity | Finding | Status |
| --- | --- | --- |
| high | PostgreSQL `json`/`jsonb` and arrays were decoded and re-encoded, corrupting big integers, decimals, key order and multidimensional arrays; `timestamptz` was always UTC | fixed: JSON, array and timestamptz columns are read from the server's text; `Infinity` preserved; live PG tests |
| high | The 1000-row cap drained the rest of a huge result (apparent hang) | fixed: PostgreSQL and MySQL cancel the statement at the cap (live tests) |
| high | SQLite `file::memory:` / `:memory:?params` got a multi-connection pool of *different* databases | fixed: every memory form forces one connection |
| high | First-run vault offer was skipped on a genuinely fresh install because the config directory had to pre-exist | fixed: the nearest existing ancestor is checked and the directory is created on first save |
| high | MySQL routed `CALL`, `(SELECT …)`, `ANALYZE`/`OPTIMIZE`/`REPAIR` and MariaDB `RETURNING` through Exec, discarding their rows; `WITH … UPDATE` reported 0 affected | fixed: statement classifier extended; WITH-DML uses Exec (live tests) |
| high | Pending grid edits were dropped silently by close-tab / close-others / quit / connection switch | fixed: confirmation dialogs in all those paths (tests) |
| medium | SQL splitter merged the script after a `CASE … END CASE`; MySQL backslash escapes broke string lexing; an invalid exponent shifted following statement offsets | fixed with tests (`sql/split.go`, `sql/lex.go`) |
| medium | Stale `queryMsg` could clear the running/cancel state and overwrite newer results | fixed: query generations, stale outcomes ignored |
| medium | Drag state survived a tab switch | fixed: pointer drags reset in `switchTab` |
| medium | MySQL functional/multi-valued index definitions failed to scan NULL and FULLTEXT/SPATIAL DDL was invalid | fixed: nullable scan, `(expression)` parts, correct CREATE form, DESC/INVISIBLE/IGNORED (live MariaDB/MySQL tests) |
| medium | PostgreSQL index/trigger definitions were not search_path-independent | fixed: rendered under an empty `search_path` (live tests) |
| medium | SQLite catalog missed generated columns, claimed non-nullability for ordinary TEXT PRIMARY KEYs and hid `sqlite…` user tables (LIKE wildcard) | fixed with `table_xinfo`, real nullability rules, ESCAPE |
| medium | Affected count for SQLite DDL/PRAGMA was stale (`changes()` from a previous statement) | fixed: only DML reports the count |
| medium | MariaDB sequences were listed but "View definition" failed | fixed: `SHOW CREATE SEQUENCE` |
| medium | Data races on the drivers' `closed` flags | fixed with `atomic.Bool` |
| low | MySQL zero dates rendered as `0001-01-01` | fixed: `0000-00-00` |
| low | PostgreSQL sequences lost `OWNED BY`, `UNLOGGED` tables printed as logged | fixed |
| low | PostgreSQL aggregates/window functions and foreign tables were offered a "View definition" that could not work | fixed: they are no longer advertised as definable; function/procedure kind is filtered |

## Deliberately left (tracked in product-gaps)

- Overloaded PostgreSQL functions: `DefinitionRef` has no signature, so a
  same-named overload may open the other overload's DDL (kind is filtered).
- `BEGIN ISOLATION LEVEL …` / `START TRANSACTION READ ONLY`: routed as a
  transaction but the options are not applied.
- PostgreSQL partitioned/inherited tables, materialized-view reloptions,
  identity sequence options and per-column storage attributes are not in the
  generated DDL (documented reconstruction limits).
- SQLite generated columns are listed but the catalog has no "read-only column"
  flag, so the grid may offer to edit them.
- Unsaved editor buffers have no dirty-tracking prompt (only unapplied grid
  edits are guarded).
- New results / Clear Results still replace unapplied grid edits directly;
  disconnect still drops them (the close/quit/switch paths ask).
- MySQL has no `clientFoundRows`: an edit that changes only the literal
  representation (e.g. `01` → `1` on an INT) stays pending.
- Keychain integration, CSV import, global object search, parameter prompts,
  linting, result diff, ER diagram and an SSH-tunnel UI remain on the roadmap.

## How this was verified

- `go test ./...` (16 packages), `go vet ./...`, `gofmt -l` clean.
- `go test -race` on the stateful packages (ui, drivers, settings, sql,
  complete, history, textbuf): clean.
- Live driver integration tests against PostgreSQL 17, MySQL 8.4 and MariaDB
  11.4: clean.
- The full PTY integration suite (98 scenarios against real databases):
  see [`integration-testing.md`](integration-testing.md) and
  `.artifacts/itest-summary.md`.
- `govulncheck ./...`: no vulnerabilities.
