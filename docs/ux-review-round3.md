# UX and robustness review round 3: commercial readiness

Date: 2026-10-21

Scope: an audit of the code against what a database client needs to be usable on
a production database, plus a comparison with the closest terminal competitor
(lazysql) and the GUI clients (DBeaver, DataGrip). Round 2 added SQL generation,
a key-binding overlay and a focus-aware status bar; this round closes the
robustness and credential gaps that block real use, and prepares the ground for
cell editing.

## What the code audit found

### 1. A hung host hangs the client forever

`ui/connection.go` opens the connection and loads the catalog with
`context.Background()`. There is no timeout, so a host that accepts the TCP
connection and then stalls keeps the background goroutine alive for the rest of
the session, and the UI shows nothing at all — no spinner, no error, no way to
cancel. The same applies to the catalog load in `Init`.

### 2. There is no way to recover a broken connection

The context menu of the *active* connection offers Test, Edit, Refresh and New
connection — but not Reconnect and not Disconnect. When the network drops, the
only way back is to edit the connection by hand. A commercial client must let
the user re-open the session and must close the old one cleanly.

### 3. Passwords are stored in clear text, with no alternative

`settings.Connection.Password` is written to `settings.json` as-is. The
competition keeps secrets out of the configuration file; at minimum a client
must be able to read the password from an environment variable so a
configuration file can be committed or shared.

### 4. Query history cannot be searched

The history dialog lists the last 200 statements and nothing else. Finding
"that UPDATE I ran yesterday" means scrolling.

## What the competitor does that we do not (and should)

lazysql (the closest terminal client) shows the bar we should aim at: a records
view per table with cell editing and row insert/delete, multi-row marking with
copy as SQL/JSON/Markdown/CSV, foreign-key jumps, a global object search, an
exact/estimated row count, a row cap with a visible "truncated" marker,
cancellation that keeps partial rows, and secrets that stay out of the config
file.

Of that list, TDB already has: the row cap and the truncated marker, query
cancellation, read-only mode, the confirmation gate for destructive
statements, the manual-commit mode, the object filter, and JSON/Markdown
export. The remaining gaps, in the order they block real work:

1. **Cell editing** (round 4): change a cell, generate the `UPDATE` from the
   primary key, apply it inside the transaction, roll it back. Round 2's
   "View data" already produces the table-scoped result set this needs.
2. **Multi-row marking and copy formats** (round 4): mark rows, copy them as
   SQL, JSON, Markdown or CSV — the formats a developer pastes into a ticket
   or a migration.
3. **Per-connection read-only mode** (round 5): today read-only is a global
   switch; a client used against a production database needs the guard rail on
   the connection itself, not only in the session.
4. **Keychain credentials** (round 5): the environment-variable password of
   this round is the portable half of it; the OS keychain is the desktop half.

## Decisions for this round

- **Connection timeout**: 15 seconds for opening the connection *and* for the
  first catalog load. The timeout is reported in the status bar with the
  driver's error, so a slow network is visible instead of silent.
- **Reconnect** re-opens the saved connection and reuses the existing tree
  state; **Disconnect** closes the session, empties the tree and leaves the
  saved connection in place. Both are context-menu actions on the connection
  row.
- **Environment-variable password**: a new connection field. When it is set,
  the variable's value is used as the password at connect time and the
  password itself is never written to the settings file. A missing variable is
  reported before the connection attempt, not as an authentication error.
- **History filter**: `/` inside the history dialog filters the statements by
  text and connection name; `Esc` clears it.

## Out of scope

- Cell editing and multi-row marking (round 4).
- Per-connection read-only and OS keychain (round 5).
- SSH tunnels, TLS option dialogs (the DSN covers them).

## Acceptance

- `make check` and `make race` pass; both catalogs stay complete.
- A stalled host produces a timeout message within 15 seconds instead of
  hanging.
- Reconnect and Disconnect are reachable from the connection row and leave the
  session in a consistent state (no orphaned connection, no stale tree).
- The settings file never contains a password when the environment-variable
  field is used.
- The history dialog filters and clears.
