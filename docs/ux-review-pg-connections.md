# UX review round: PostgreSQL metadata and connection dialog

Date: 2026-10-21

Scope: fix what a hands-on pass against PostgreSQL 16 exposed, plus two
navigation gaps that make large catalogs hard to use. SQLite/MySQL behaviour is
kept unchanged.

## Findings

### 1. PostgreSQL table DDL never loads (root cause)

`driver/postgres.postgres.go` builds `CREATE TABLE` with a scalar subquery that
aggregates a primary key:

```sql
SELECT … string_agg(…) ||
       (SELECT E',\n    CONSTRAINT ' || format('%I', con.conname) || ' PRIMARY KEY (' ||
               string_agg(format('%I', pa.attname), ', ' ORDER BY key_ord.ord) || ')'
        FROM pg_constraint …
```

`con.conname` is neither grouped nor aggregated, so the server rejects the whole
statement with SQLSTATE 42803 ("column con.conname must appear in the GROUP BY
clause…"). Every table therefore reports "definition of table …: ERROR", which
is what the user sees as "no primary key / foreign key".

Fix: aggregate each constraint kind in its own subquery and join the rendered
constraint list. While there, reproduce what a table definition should show:

- primary key and unique constraints (named)
- foreign keys from `pg_get_constraintdef`, including `ON DELETE` / `ON UPDATE`
- check constraints
- generated columns (`attgenerated = 's'`) and identity columns
  (`attidentity = 'a' | 'd'`)
- defaults, `NOT NULL`, type and safe identifier quoting (already present)

### 2. PostgreSQL functions

User-defined functions at HEAD do appear under the Functions group (verified
against a live server with `book_count()`, `author_books(integer)` and a
procedure). The only functions hidden by the driver are extension-owned ones
(`pg_depend.deptype = 'e'`), which makes a database whose functions come from an
extension (pg_trgm, PostGIS, …) look as if it has no functions at all. Those
should be listed; only `pg_catalog` and `information_schema` stay hidden.

### 3. Connection type is a cycle, not a dropdown

The form's `fieldChoice` reacts to ←/→ only. The value looks like plain text, so
there is no affordance that it can be changed, and users cannot see the
available types. It needs a real dropdown (keyboard and mouse).

### 4. Panes cannot be focused from the keyboard

`paneObjects` / `paneResults` receive focus only from mouse clicks. A terminal
client must be fully usable without a mouse. Add explicit focus commands
(`Alt+1` objects, `Alt+2` editor, `Alt+3` results) exposed in the View menu.

### 5. No object filter in the browser

A large PostgreSQL database has hundreds of tables and resources; reaching the
Functions group means scrolling past schemas and groups. Add an incremental
filter (`/` while the tree is focused) that matches object names, comments and
function signatures and keeps the matching ancestors visible.

## Out of scope

- Data editing / transactions (already tracked in `docs/product-gaps.md`).
- ER diagrams, import, lint.
- Changing the driver model or adding a new database.
- Credentials storage.

## Acceptance

- `make check` and `make race` pass.
- PostgreSQL integration test (when `TDB_POSTGRES_DSN` is set) asserts the
  table DDL contains PK, FK, UNIQUE and CHECK, and that extension-owned
  functions are visible.
- New behaviour has regression tests; i18n catalogs stay complete in English
  and Simplified Chinese.
