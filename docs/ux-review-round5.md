# UX review round 5: tab names and the default schema

Date: 2026-10-22

Scope: two requests from using the client for real work — tabs should carry a
meaningful name instead of a clock time, and a connection should have a
default schema so unqualified names in the editor resolve where the user
expects.

## 1. Tab names

Today a tab is named after the file it holds, and a tab without a file is named
after the time it was opened (`15:04:25`). That name says nothing about what
the tab contains, and a screen full of `15:04:25` tabs is unreadable. The
objects a user opens in a database client have names — the table, the view,
the function — and the tab should carry them.

### Rules

- A tab that holds a file keeps the file's name (today's behaviour).
- A tab opened from the object browser is named after the object:
  `public.users` — schema-qualified, because `users` alone is ambiguous in a
  database with several schemas.
- A tab that holds a generated statement is named after the relation it was
  generated from, with the kind of statement in brackets when it helps:
  `public.users [SELECT]`, `public.users [data]`.
- A tab opened from the query history is named after the statement's first
  line, trimmed to what fits.
- A tab the user renames keeps that name until it is closed; the name is not
  derived from the content any more.
- A tab with no name at all is still called *untitled*.

### Renaming

The tab bar gets a context menu (right-click, or `Shift+F10` while the tab bar
has the focus) with **Rename…**, **Duplicate** and **Close**. Renaming opens a
small modal input; `Enter` accepts, `Esc` cancels, the name may be empty (which
means "go back to the derived name"). The name is stored in the session, so it
survives switching tabs and re-rendering.

## 2. A default schema per connection

A connection should say which schema it works in. Today the search path is
whatever the server happens to use, so the same query can resolve to different
tables on different machines, and the object tree has no notion of "the schema
I am working in".

### Rules

- A connection gets a **Default schema** field (empty means "server default").
- PostgreSQL applies it when the connection opens: `SET search_path TO
  "<schema>"`, so unqualified names in the editor resolve there. The schema is
  quoted as an identifier, never interpolated raw.
- MySQL and MariaDB already have a database; the field is the database, so
  nothing changes there.
- SQLite has one schema (`main`); the field is not offered.
- The field is validated against the catalog after connecting: if the schema
  does not exist, the client says so instead of silently using another one.
- The object tree marks the default schema, and expanding a connection starts
  with it.

### Where it lives

`settings.Connection.Schema` — per connection, because two connections to the
same server often work in different schemas. A global default for new
connections is a preference, not a connection property, and stays out of this
round.

## Acceptance

- `make check` and `make race` pass; both catalogs stay complete.
- A tab opened from the object browser shows the schema-qualified object name;
  a renamed tab keeps its name; a tab with a file keeps the file name.
- The rename dialog is modal and never leaks keys into the editor.
- The default schema is applied on connect (PostgreSQL), quoted safely, and
  reported when it does not exist.
- The settings file round-trips the new field, and old files without it load
  unchanged.
