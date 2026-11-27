# UX review round 4: editing data in the result grid

Date: 2026-10-21

Scope: the last P0 gap — changing data from the client. The design follows what
DBeaver, DataGrip and lazysql do, but keeps TDB's rules: nothing is written
before the user sees the statement, the read-only gate still applies, and the
manual-commit mode is the safety net when it is on.

## Preconditions the earlier rounds built

- Round 2's **View data** produces a result set for a known table, and the
  generated SQL is schema-qualified and quoted.
- The catalog already carries `Column.PK`, so a row can be addressed by its
  primary key.
- `driver.Connection.Query(ctx, sql, args...)` takes arguments, so an update
  can be sent as a parameterized statement — no string concatenation of user
  data, no quoting bugs.
- The confirmation dialog already exists for statements that need a second
  look, and the manual-commit mode already keeps a batch inside a transaction.

## Design

### Which results can be edited

A result set is editable when all of the following hold:

1. The statement was a single `SELECT` over exactly one table (no join, no
   sub-query in `FROM`, no set operation). The table is resolved against the
   live catalog.
2. The table has a primary key, and every primary-key column is present in the
   result by name.
3. The client is not in read-only mode.

Anything else keeps the grid read-only, and the reason is shown in the status
line instead of failing at the moment the user presses the key.

### Editing a cell

- `e` in the result grid starts editing the cell under the caret. The cell is
   drawn with a text cursor; the grid owns the keyboard until the edit ends.
- `Enter` stores the value as a pending change; `Esc` cancels. The literal
   `\N` means SQL NULL, the way `COPY` does — an empty cell stays an empty
   string.
- A pending change is drawn in the cell (a marker in the row number gutter
   plus a highlighted cell) and counted in the result summary. Nothing has
   reached the database yet.
- Editing the same cell again replaces the pending value.

### Applying the changes

- `Ctrl+S` in the grid applies the pending changes; the Query menu offers the
  same action as **Apply Changes**, and **Discard Changes** drops them.
- Applying shows the confirmation dialog with the exact `UPDATE` statements
  that will run, so the user reads the SQL before it touches the data.
- The statements run in one batch: in manual-commit mode they join the open
  transaction (and can still be rolled back with `F11`); otherwise they
  commit on their own.
- After a successful apply the original `SELECT` runs again, so the grid shows
  what the database now holds. A failed apply keeps the pending changes —
  losing them would lose the user's typing.

### What is deliberately not in this round

- Inserting or deleting rows (round 5).
- Foreign-key jumps, column visibility, a records view with pagination.
- Type-aware validation of the entered value: the database is the authority,
  and its error message is shown verbatim.

## Acceptance

- `make check` and `make race` pass; both catalogs stay complete.
- The generated `UPDATE` is parameterized: the value and the key are arguments,
  never text in the statement.
- A result set that is not editable says why, instead of failing later.
- The confirmation dialog shows the statements before they run, and the
  read-only gate blocks the apply.
- A failed apply keeps the pending changes; a successful apply refreshes the
  grid.
