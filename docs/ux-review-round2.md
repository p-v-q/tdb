# UX review round 2: discoverability and SQL generation

Date: 2026-10-21

Scope: a second hands-on pass over the demo and SQLite sessions, plus a review
of `docs/product-gaps.md` against the current code. The goal is the P1 layer
that makes everyday browsing fast; data editing (the remaining P0) needs the
table-data groundwork added here and is planned next.

## Findings

### 1. The Help menu promises key bindings, the app shows one line

`Help → Key Bindings` (and `F1`) run `actKeymap`, which only sets a transient
status message naming F10 and F2. A terminal client with this many shortcuts
needs a real, scrollable list; otherwise `Alt+1`…`Alt+3` and the tree filter are
undiscoverable without the README.

### 2. The status bar ignores the focused pane

The left side always shows `Ln/Col`, SQL completion context and candidate count,
even when the object tree or the result grid owns the keyboard. The right side
always advertises completion and save. A focused pane should show what its keys
do and where the caret is in that pane.

### 3. No SQL generation from the object tree

The object context menu can insert or copy a name and show DDL, but cannot
produce the statements people write all day: `SELECT`, `INSERT`, `UPDATE`,
`DELETE`. Every compared client has this; it is cheap because the catalog
already has every column and the primary-key flags.

### 4. No quick table data view

Expanding a table shows columns only. Answering "what is in this table" means
writing `SELECT *` by hand, even though the object tree knows the qualified,
quoted name.

## Decisions

- **SQL generation is a template, not data**: identifiers are quoted with the
  active dialect, placeholders follow the dialect (`$1` on PostgreSQL, `?`
  elsewhere), and the statement opens in a new tab. Nothing runs automatically.
- `UPDATE`/`DELETE` use the primary key in `WHERE`. A table without a primary
  key still gets a statement, but with a warning comment and a full-row `WHERE`
  so the risk is visible before it is executed.
- **View data** opens `SELECT * FROM <table> LIMIT 200` in a new tab and runs it,
  because the point is to see rows immediately. The limit is a reading default,
  not a hard cap.
- **Key bindings** becomes a modal overlay built from `menuDefs` plus the
  pane-specific keys, so it cannot drift from the command table. It scrolls and
  closes with `Esc`.
- **Status bar**: the left side is pane-aware (`Object 3/18`, `Row 4/120`,
  editor `Ln/Col`), and the right side lists the focused pane's keys. The
  global hints stay when no pane has a more useful answer.

## Out of scope

- Cell editing / row insert / row delete (next round, builds on View data).
- Keychain credentials, CSV import, lint, parameterized queries.
- Any change to driver query text outside the new generation helpers.

## Acceptance

- `make check` and `make race` pass; both catalogs stay complete.
- New generation helpers are pure and unit-tested for PostgreSQL, MySQL and
  SQLite quoting/placeholders, with and without a primary key.
- The key-binding overlay is rendered from `menuDefs`; a test asserts it lists
  every global shortcut and that `Esc` closes it.
- The status bar has a test per focused pane.
