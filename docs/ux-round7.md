# Round 7: pointer hover, a welcome tab, row selection and built-in pickers

This round closes five gaps that show up the moment the client is used with a
mouse and a real workspace:

1. dropdowns never highlight the row under the pointer;
2. there is no welcome page to start from;
3. the result grid can only address one row at a time;
4. export needs a path typed by hand;
5. SQL files cannot be opened from inside the client.

The rule for the round is the same as before: a feature is done when the
keyboard and the pointer both reach it, the dialog geometry stays inside the
shared rules of `ui/modal.go`, and every new string exists in English and
Simplified Chinese.

## 1. Hover belongs to every list

`ui/mouse.go` routes `MouseActionMotion` only to the menu bar, the toolbar and
the column edges. Every dialog handles `MouseActionPress` and returns early on
anything else, so a list opened with the mouse cannot be steered without
clicking.

Decision: **a list highlights the row under the pointer, and hovering never
changes a value.** Concretely:

- While a dialog is open, motion events go to that dialog before anything
  behind it; a motion outside the dialog is ignored (the dialog is modal).
- A list keeps two positions: `sel` (keyboard/cursor, the row Enter acts on)
  and `hover` (`-1` when the pointer is elsewhere). Rendering shows the hover
  style on the hovered row and the selected style on `sel`; when both land on
  the same row, the selected style wins.
- Moving the pointer onto a row does **not** move `sel`. Clicking a row picks
  it, exactly as today. Leaving the list clears `hover` only.
- Surfaces: the connection form's dropdown, the settings dropdowns, the export
  format dropdown, the vault manager entries, the object tree rows, the tab
  bar, and the result grid rows (section 3).

The same `hover` field is added to `selectList` (settings and export) and to
the connection form's own dropdown state, because the two grew independently
and merging them is a separate refactor, not this round.

## 2. A welcome tab

The first tab of a session is a welcome page when nothing else is open, and it
reappears when the last tab is closed. It is the only place where "start here"
lives, so the empty editor stops being the onboarding.

- The tab is a real tab with `welcome bool` set; it owns an empty buffer so all
  the generic tab machinery keeps working.
- It renders a title, one line of orientation, and an action list: *New query*,
  *Open SQL file…*, then one row per saved connection (or a line saying none
  are saved), then *Settings* and *Keyboard shortcuts*.
- `↑`/`↓` (and `Tab`) move, `Enter` activates, `Esc` closes the tab when
  another tab exists. The accelerators printed on the rows (`n`, `o`, `s`,
  `f1`) work directly.
- **Typing anything that is not navigation turns the tab into a normal editor
  tab** with that first character, so the welcome page can never trap a user
  who starts writing. A click in the editor body does the same.
- While the vault is locked, the connection rows are replaced by the existing
  "connections are locked" line; the rest of the page still works.

## 3. Multi-row selection in the result grid

The grid addresses a single cell (`cursorRow`, `cursorCol`). Selecting several
rows is needed to copy a slice of a result.

- `resultState` gains `selAnchor int` (`-1` = no selection). The selected range
  is the inclusive span between `selAnchor` and `cursorRow` in **view order**,
  so sorting and filtering cannot move the highlight onto different data.
- Gestures: `Shift+↑`/`Shift+↓`/`Shift+Home`/`Shift+End` extend; `Shift+click`
  sets the anchor and extends; dragging across rows extends. `Esc` clears the
  selection first, and only then leaves the pane.
- `Ctrl+C` (and `y`) copy the selected rows as TSV without the header when a
  selection exists; with no selection they keep their current meaning.
- The summary line says how many rows are selected, so the state is never
  invisible. Rows in the selection use a dedicated selection style.
- Export keeps exporting the view (filtered and sorted), not the selection:
  one code path, and the selection's job is the clipboard.

## 4. Built-in file and directory picker

The client has no file browser, and a terminal application cannot rely on a
system dialog. A modal picker is added in `ui/file_picker.go` and shared by
"open SQL file" and "export to a directory".

- Layout: a path line at the top (editable, horizontally scrolled), the entry
  list below, a hint line and the buttons at the bottom. Size comes from
  `modalSize` with a 72×18 want.
- Entries: `..`, directories (marked with the folder glyph), then files. The
  mode decides what can be picked: *file* shows every file, *directory* shows
  directories only and picks the current folder.
- Keys: `↑`/`↓`/`PgUp`/`PgDn`/`Home`/`End` move, `Enter` descends into a
  directory (or picks a file in file mode), `Backspace` goes to the parent,
  `Tab` moves between the path line and the list, `Esc` cancels. Typing filters
  the list; the path line accepts a full path and `~`.
- The directory mode has a *Choose this folder* button that picks the current
  folder regardless of the highlight.
- Dotfiles are hidden unless the filter starts with `.`. An unreadable folder
  or a vanished entry shows an inline message instead of closing the picker.
- The picker never writes anything; it only returns a path.

## 5. Opening SQL files

- *File → Open SQL File…* (`Ctrl+O`) opens the picker in file mode.
- The chosen file becomes a new tab named after the base name, with
  `fileName` set, so the existing *Save* (`Ctrl+S`) writes back to it.
- Files are read as UTF-8; invalid UTF-8 is reported rather than decoded
  lossily, because silently replacing bytes in SQL is worse than refusing it.
  A size cap (8 MiB) keeps a mistaken `/dev/zero` from freezing the interface.
- Failures (missing file, permissions, size, encoding) close the picker and
  report in the status bar; they never leave a half-open tab behind.

## Acceptance

- `make check` and `make race` pass; `gofmt -l .` is empty.
- Hover works in every list named in section 1 and never changes a value.
- The welcome tab appears on a fresh start, lists the saved connections,
  can be typed over, and returns when the last tab closes.
- Shift/drag select rows, the count is visible, and copying yields TSV.
- The picker opens a SQL file into a tab and picks an export directory; `~`,
  relative paths, unreadable folders and cancel behave as described.
- English and Simplified Chinese strings exist for every new string.
