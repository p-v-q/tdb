# UX review round 6: the dialogs

Date: 2026-10-23

Every modal in TDB grew one feature at a time: the connection form gained an
environment-variable password, a default schema, a DSN; the settings dialog
gained rows; the export dialog gained formats. Each is readable on its own, but
they no longer look like one program: some hug their content in a narrow box,
the connection form is a single tall list of unrelated fields, and choices
that are really choices (theme, language, export format, SSL mode) are still
"press ←/→ to cycle" — fine for two options, miserable for six.

This round makes the dialogs share one geometry and one control vocabulary.

## One box, one size

A modal gets a **minimum useful size** and grows with the screen instead of
hugging its content:

- width: `max(content, 60)` columns, capped at `min(screen-4, 100)`;
- height: `max(content, 12)` rows, capped at the area between the menu bar and
  the status bar;
- one blank row of padding inside the border, the title on the top border, the
  hint line at the bottom, buttons on the last row.

The rules live in one place (`ui/modal.go`) so a new dialog cannot invent its
own margins. Rendering and hit testing keep sharing the computed rectangle, as
they already do.

## Choices become selects

A field that can only take one of a known set becomes a dropdown, opened with
`Enter`/`Space`/`↓` and picked with `Enter`:

- connection form: **SSL mode** (new — `disable`, `allow`/`prefer`,
  `require`, `verify-ca`, `verify-full` for PostgreSQL; the go-sql-driver
  equivalents for MySQL/MariaDB), alongside the existing database-type select;
- settings: **language** and **theme**, replacing the cycling values;
- export: **format** (CSV, TSV, JSON, Markdown), replacing cycling.

Text fields stay text: host, port, user, password, DSN, and identifiers.

## The connection form

It is the longest form in the program (up to eleven fields) and it was a flat
list where "port" sat between "host" and "user" with no visual grouping. It
becomes four groups, each with a quiet header:

```
Server       Host, Port
Credentials  User, Password, Password from environment variable
Database     Database, Default schema, SSL mode
Advanced     DSN
```

The group headers are not focusable; `↑`/`↓`/`Tab` move between fields only.
The value column is wide enough for a DSN (`56` columns) and scrolls
horizontally to keep the caret visible, instead of widening the whole window.

## The other dialogs

- **Settings**: same box rules, two-column rows (label, value select), and the
  description under the selected row rather than as a second line per row. The
  connection passphrase actions (set, change, remove, lock now) live here.
- **Export**: format select + a preview that can use two thirds of the screen
  height instead of a fixed few rows.
- **Confirm**: keeps its role — it is the last thing the user sees before a
  write — so it grows enough to show the statement, the bound values and the
  target table without scrolling for the common case.
- **History**: wider (the SQL is the content), more rows.
- **Rename tab**: a small box centred on the tab bar rather than the screen.
- **Key bindings**: already a scrolling overlay; widened to fit the longest
  binding without wrapping.

## Acceptance

- `make check` and `make race` pass; every new string exists in both catalogs.
- Every dialog stays inside the screen at `40x80` and at `120x40`, keeps its
  hint line visible, and keeps its buttons clickable (the hit-test rectangle
  equals the drawn one).
- The connection form writes `sslMode` to the settings file and composes it into
  the DSN; a hand-written DSN still wins.
- Selects are keyboard- and mouse-operable and never leak a key to the field
  behind them.
