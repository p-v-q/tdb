package scenarios

// editor_test.go — end-to-end scenarios for the SQL editor, tabs, query history
// and the editor-adjacent dialogs. Every scenario drives the real tdb.exe in
// a 160x48 ConPTY and asserts on the rendered screen, the way an analyst would
// work: write a report query, run it, edit it, save it, reopen it, dig through
// the history and rename the tab they keep coming back to.
//
// Keys the ConPTY input path cannot deliver with their modifier (Ctrl+H,
// Ctrl+/, Ctrl+PgUp/Down, Shift+F10) are pressed through the menu bar, exactly
// as the scenarios' comments say.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// Terminal escape sequences for the modified keys the harness does not name.
// They are the xterm encodings Bubble Tea decodes into "shift+arrow",
// "shift+home/end" and "ctrl+home/end".
const (
	edShiftUp    = "\x1b[1;2A"
	edShiftDown  = "\x1b[1;2B"
	edShiftRight = "\x1b[1;2C"
	edShiftLeft  = "\x1b[1;2D"
	edShiftHome  = "\x1b[1;2H"
	edShiftEnd   = "\x1b[1;2F"
	edCtrlHome   = "\x1b[1;5H"
	edCtrlEnd    = "\x1b[1;5F"

	// Bracketed paste is what a terminal sends for Ctrl+V / right-click paste.
	edPasteStart = "\x1b[200~"
	edPasteEnd   = "\x1b[201~"

	// edNoFileSaveMsg is what Ctrl+S reports on a tab that was never given a
	// file, from i18n.KeyMsgNoFileToSave.
	edNoFileSaveMsg = "no file to save"
)

// ---- Session and input helpers ----

// edStartShop launches TDB on the standard scenario database and waits until
// the connection banner is up, like a user who just opened the app.
func edStartShop(t *testing.T) *scenario.Ctx {
	t.Helper()
	c := scenario.New(t)
	db := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", db)
	c.WaitFor("conn shop.db", 25*time.Second)
	return c
}

// edSend sends one key sequence (or terminal escape) and lets the screen settle.
//
// Driving the console byte by byte has one quirk the scenarios have to absorb:
// sends of a control byte (Ctrl+A, Ctrl+S, Ctrl+Home, …) are sometimes delivered
// as a stray NUL rune in addition to the real key, and the UI maps a lone NUL
// rune to Ctrl+Space. The completion popup therefore opens as a side effect. It
// consumes the arrow keys and Enter that follow (they move inside the popup /
// accept its first candidate) and it hides the SQL in screenshots, so it is
// dismissed here, with the rest of the scenario unchanged. The stray event can
// arrive a beat after the real key, which is why the dismissal retries.
func edSend(c *scenario.Ctx, keys string) {
	c.T.Helper()
	if err := c.S.Send(keys); err != nil {
		c.Fail("send %q: %v", keys, err)
	}
	attempts := 0
	if len(keys) == 1 && keys[0] < 0x20 && keys != harness.KeyEnter && keys != harness.KeyTab {
		attempts = 3 // a control byte can be relayed as a delayed extra NUL
	} else if strings.Contains(c.Screen(), "candidates") {
		attempts = 2
	}
	edDismissPopup(c, attempts)
}

// edDismissPopup closes the completion popup, looking attempts times so a late
// duplicate event cannot slip through after the first look.
func edDismissPopup(c *scenario.Ctx, attempts int) {
	c.T.Helper()
	for i := 0; i < attempts; i++ {
		time.Sleep(120 * time.Millisecond)
		if !strings.Contains(c.Screen(), "candidates") {
			return
		}
		if err := c.S.Send(harness.KeyEsc); err != nil {
			c.Fail("dismiss completion popup: %v", err)
		}
	}
}

// edDismissSpuriousPopup dismisses a completion popup that may already be open
// (a single look, without waiting for a late duplicate).
func edDismissSpuriousPopup(c *scenario.Ctx) {
	c.T.Helper()
	if !strings.Contains(c.Screen(), "candidates") {
		return
	}
	if err := c.S.Send(harness.KeyEsc); err != nil {
		c.Fail("dismiss completion popup: %v", err)
	}
}

// edType types text as keystrokes.
func edType(c *scenario.Ctx, text string) {
	c.T.Helper()
	if err := c.S.Type(text); err != nil {
		c.Fail("type %q: %v", text, err)
	}
}

// edPaste inserts text the way a terminal paste does (bracketed paste).
func edPaste(c *scenario.Ctx, text string) {
	c.T.Helper()
	edSend(c, edPasteStart+text+edPasteEnd)
}

// edSelectAll selects the whole buffer.
func edSelectAll(c *scenario.Ctx) { edSend(c, harness.KeyCtrlA) }

// edReplaceAll replaces the whole buffer with text, the way a user starts a new
// statement: Ctrl+A, then type over the selection.
func edReplaceAll(c *scenario.Ctx, text string) {
	edSelectAll(c)
	edType(c, text)
}

// edDismissPopup closes the completion popup if one is open. Esc only clears a
// selection when no popup is open, and by the time it is called here typing has
// just replaced any selection, so the text is untouched.
func edSendEsc(c *scenario.Ctx) { edSend(c, harness.KeyEsc) }

// edCaretHome moves the caret to the start of the buffer.
//
// The ConPTY input path delivers Ctrl+Home as two key events: a stray NUL rune
// (which the UI maps to Ctrl+Space, i.e. "open completion") followed by the
// real Ctrl+Home. The caret still lands at the top, but the completion popup
// opens as a side effect, so it is dismissed here; otherwise the following
// arrow keys would move inside the popup instead of moving the caret. This is
// an artifact of driving the console byte by byte (a real keyboard sends one
// key record), so the scenarios keep using the key and clean up after it.
func edCaretHome(c *scenario.Ctx) {
	c.T.Helper()
	edSend(c, edCtrlHome)
	edDismissPopup(c, 3)
}

// edCaretEnd moves the caret to the end of the buffer; see edCaretHome for the
// completion popup the ConPTY Ctrl+End sequence leaves behind.
func edCaretEnd(c *scenario.Ctx) {
	c.T.Helper()
	edSend(c, edCtrlEnd)
	edDismissPopup(c, 3)
}

// edTypeScript types a multi-line script line by line. Enter inserts the newline
// after dismissing the completion popup, which would otherwise accept a
// candidate on Enter; the dismissal is re-checked because a stray NUL can open
// the popup a beat after the Esc that closed it.
func edTypeScript(c *scenario.Ctx, lines ...string) {
	c.T.Helper()
	for i, line := range lines {
		edType(c, line)
		if i < len(lines)-1 {
			edSendEsc(c)
			edDismissPopup(c, 2)
			edSend(c, harness.KeyEnter)
		}
	}
	edSendEsc(c)
}

// edRun presses F5 and waits for want to appear in the result pane.
func edRun(c *scenario.Ctx, want string) {
	c.T.Helper()
	edSend(c, harness.KeyF5)
	c.WaitFor(want, 25*time.Second)
}

// edStatus returns the bottom status-bar line.
func edStatus(c *scenario.Ctx) string {
	c.T.Helper()
	lines := strings.Split(c.Screen(), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// edStatusContains asserts the status bar contains every want.
func edStatusContains(c *scenario.Ctx, wants ...string) {
	c.T.Helper()
	line := edStatus(c)
	for _, want := range wants {
		if !strings.Contains(line, want) {
			c.Fail("status bar does not contain %q\nstatus: %q", want, line)
		}
	}
}

// edWaitCaretStatus waits until the status bar is back to reporting the caret
// position. A query result or a copy leaves a transient status message up for a
// few seconds; while it is shown the caret/selection report is replaced by it.
func edWaitCaretStatus(c *scenario.Ctx) {
	c.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(edStatus(c), "Ln ") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.Fail("status bar never returned to the caret report\nstatus: %q", edStatus(c))
}

// edTabRow locates the tab strip and the screen column of every tab's close
// glyph, in tab order. The tab strip sits directly above the "SQL Editor" pane
// title; each tab is drawn as " label × ".
func edTabRow(c *scenario.Ctx) (row int, line string, closeCols []int) {
	c.T.Helper()
	lines := strings.Split(c.Screen(), "\n")
	titleRow := -1
	for i, l := range lines {
		if strings.Contains(l, "SQL Editor") {
			titleRow = i
			break
		}
	}
	if titleRow <= 0 {
		c.Fail("cannot locate the editor pane title %q on screen", "SQL Editor")
	}
	row = titleRow - 1
	line = lines[row]
	for i, r := range []rune(line) {
		if r == '×' {
			closeCols = append(closeCols, i)
		}
	}
	return row, line, closeCols
}

// edRightClickTab opens the context menu of tab idx with a real right click on
// its label.
func edRightClickTab(c *scenario.Ctx, idx int) {
	c.T.Helper()
	row, _, cols := edTabRow(c)
	if idx < 0 || idx >= len(cols) {
		c.Fail("tab %d does not exist (%d tabs on screen)", idx, len(cols))
	}
	x := cols[idx] - 2 // last character of the label (the close glyph is at cols[idx])
	if err := c.S.MouseClickButton(x, row, harness.MouseRight); err != nil {
		c.Fail("right-click tab %d at (%d,%d): %v", idx, x, row, err)
	}
}

// edClickTab switches to tab idx with a left click on its label.
func edClickTab(c *scenario.Ctx, idx int) {
	c.T.Helper()
	row, _, cols := edTabRow(c)
	if idx < 0 || idx >= len(cols) {
		c.Fail("tab %d does not exist (%d tabs on screen)", idx, len(cols))
	}
	if err := c.S.MouseClick(cols[idx]-2, row); err != nil {
		c.Fail("click tab %d: %v", idx, err)
	}
}

// edTabLineContains asserts a tab label appears on the strip.
func edTabLineContains(c *scenario.Ctx, want string) {
	c.T.Helper()
	_, line, _ := edTabRow(c)
	if !strings.Contains(line, want) {
		c.Fail("tab bar does not contain %q\ntab bar: %q", want, line)
	}
}

// edTabCount counts how many tab labels contain want, which is how a scenario
// tells a duplicated tab from the original.
func edTabCount(c *scenario.Ctx, want string) int {
	c.T.Helper()
	_, line, _ := edTabRow(c)
	return strings.Count(line, want)
}

// edWaitFile polls a file until pred accepts its content, so a scenario never
// races the background write of a save or the history store.
func edWaitFile(t *testing.T, path, what string, pred func(content string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			last = string(data)
			if pred(last) {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("file %s did not become %s; last content:\n%s", path, what, last)
	return last
}

// ---- 1. Running a script: statement under the caret, selection, whole script ----

// TestEditorScriptRunTargets writes an ad-hoc analyst script of three
// statements and runs the statement under the caret, then a selection, then the
// whole script, checking the result set the grid shows each time. It finishes
// by undoing and redoing the last typed line.
func TestEditorScriptRunTargets(t *testing.T) {
	c := edStartShop(t)

	edSelectAll(c)
	edTypeScript(c,
		"SELECT 'alpha' AS tag;",
		"SELECT id, 'beta' AS tag FROM orders;",
		"SELECT 1 AS one UNION ALL SELECT 2;",
	)
	c.Shot("script-typed")
	c.RequireContains("SELECT 'alpha' AS tag", "SELECT id, 'beta' AS tag", "UNION ALL")

	// The caret sits on the second statement: F5 runs only that one, and the
	// grid shows the eight orders rows, not the one-row statements around it.
	edCaretHome(c)
	edSend(c, harness.KeyDown)
	c.RequireContains("Ln 2, Col 1")
	edRun(c, "8 rows in")
	c.RequireContains("beta")
	c.RequireNotContains("Result 1/")
	c.Shot("run-statement-under-caret")

	// A selection wins over the statement under the caret: select the first
	// line and only alpha runs.
	edCaretHome(c)
	edSend(c, edShiftEnd)
	edWaitCaretStatus(c) // the result of the previous run still owns the status bar
	c.RequireContains("1 line(s) /")
	edRun(c, "1 row in")
	c.Shot("run-selection")

	// Selecting everything runs the whole script: three result sets, the last
	// one shown first.
	edSelectAll(c)
	edRun(c, "Result 3/3")
	c.Shot("run-whole-script")

	// Undo removes the last typed line as one group; redo brings it back.
	edSend(c, harness.KeyCtrlZ)
	c.RequireNotContains("UNION ALL")
	c.Shot("undo-last-line")
	edSend(c, harness.KeyCtrlY)
	c.RequireContains("UNION ALL")
	c.Shot("redo-last-line")

	// The restored script still runs as a whole.
	edSelectAll(c)
	edRun(c, "Result 3/3")
}

// ---- 2. Selection, caret movement, copy and cut ----

// TestEditorSelectionWorkflow exercises the selection vocabulary an editor must
// support: Ctrl+A, Shift+arrows, Home/End, Ctrl+Home/End, typing over a
// selection, copy and cut (with undo restoring the cut text).
func TestEditorSelectionWorkflow(t *testing.T) {
	c := edStartShop(t)

	buf := "SELECT id, email, city FROM users ORDER BY id;"
	edReplaceAll(c, buf)
	c.Shot("typed")

	// Ctrl+A selects all, and typing replaces the selection.
	edSelectAll(c)
	c.RequireContains("1 line(s) /")
	edType(c, "SELECT name FROM users;")
	c.RequireNotContains("ORDER BY")
	c.Shot("typing-replaces-selection")

	// Typing at the end of the buffer forms one undo group, so one Ctrl+Z
	// removes the whole appended fragment and Ctrl+Y brings it back.
	edCaretEnd(c)
	edType(c, " -- top users")
	c.RequireContains("-- top users")
	edSend(c, harness.KeyCtrlZ)
	c.RequireNotContains("-- top users")
	c.RequireContains("SELECT name FROM users;")
	c.Shot("undo-append")
	edSend(c, harness.KeyCtrlY)
	edSendEsc(c)
	c.RequireContains("-- top users")
	c.Shot("redo-append")

	// Home / End / Ctrl+Home / Ctrl+End position the caret; the status bar
	// shows the one-based column.
	buf = "SELECT name FROM users; -- top users"
	edCaretHome(c)
	c.RequireContains("Ln 1, Col 1")
	edSend(c, harness.KeyEnd)
	c.RequireContains(fmt.Sprintf("Col %d", len(buf)+1))
	edSend(c, harness.KeyHome)
	c.RequireContains("Ln 1, Col 1")
	edCaretEnd(c)
	c.RequireContains(fmt.Sprintf("Col %d", len(buf)+1))
	c.Shot("caret-moves")

	// Shift+arrows extend and shrink the selection.
	edSend(c, harness.KeyHome)
	edSend(c, edShiftRight)
	edSend(c, edShiftRight)
	edSend(c, edShiftRight)
	edSend(c, edShiftRight)
	edSend(c, edShiftRight)
	edSend(c, edShiftRight)
	c.RequireContains("6 char(s) selected")
	c.Shot("shift-right-selection")
	edSend(c, edShiftLeft)
	edSend(c, edShiftLeft)
	c.RequireContains("4 char(s) selected")
	// Typing replaces exactly the selection: the four "SELE" characters become
	// "DEL ", so the line now reads "DEL CT name FROM users; ...".
	edType(c, "DEL ")
	c.RequireContains("DEL CT name FROM users;")
	c.Shot("typed-over-partial-selection")

	// Back to a clean buffer for the multi-line part.
	edSelectAll(c)
	edTypeScript(c,
		"SELECT COUNT(*) AS total FROM users;",
		"SELECT COUNT(*) AS pending FROM orders WHERE status = 'pending';",
	)
	edCaretHome(c)
	edSend(c, edShiftDown)
	c.RequireContains("2 line(s) /")
	edSend(c, edShiftEnd)
	c.RequireContains("2 line(s) /")
	c.Shot("two-line-selection")

	// Copy must not disturb the buffer.
	edSend(c, harness.KeyCtrlC)
	edStatusContains(c, "Copied")
	c.RequireContains("SELECT COUNT(*) AS total", "FROM orders", "status = 'pending'")
	c.Shot("copied")

	// Cut removes the selected lines and reports the copy; copy left the
	// selection in place, so the cut acts on the same two lines.
	edSend(c, harness.KeyCtrlX)
	c.RequireNotContains("FROM orders")
	c.Shot("cut")
	edSend(c, harness.KeyCtrlZ)
	c.RequireContains("FROM orders", "status = 'pending'")
	c.Shot("undo-cut")
	edWaitCaretStatus(c) // the copy confirmation still owns the status bar

	// Esc clears a selection without touching the text. The undo may have left
	// a completion popup behind, and Esc would only close that one, so the
	// ("spurious") popup is dismissed first.
	edDismissSpuriousPopup(c)
	edSelectAll(c)
	c.RequireContains("line(s) /")
	edSend(c, harness.KeyEsc)
	c.RequireNotContains("line(s) /")
	c.RequireContains("SELECT COUNT(*) AS total")

	// Ctrl+Home and a Shift+End selection run only the first line.
	edCaretHome(c)
	edSend(c, edShiftEnd)
	edRun(c, "1 row in")
	c.Shot("run-selected-line")
}

// ---- 3. Incremental find ----

// TestEditorFind drives Ctrl+F the way a user searches a script: incremental
// matches, next/previous, Esc, a query with no matches, and a query seeded from
// the selection.
func TestEditorFind(t *testing.T) {
	c := edStartShop(t)

	edSelectAll(c)
	edTypeScript(c,
		"SELECT id, email FROM users WHERE city = 'Paris';",
		"SELECT name FROM users WHERE age > 30;",
		"-- users without a city",
	)
	edCaretHome(c)

	edSend(c, harness.KeyCtrlF)
	c.WaitFor("Find:", 5*time.Second)
	c.RequireContains("Type to search", "Enter next", "Esc close")
	c.Shot("find-open")

	// The query is incremental: two characters already narrow the search.
	edType(c, "us")
	c.RequireContains("Matches: 3")
	c.Shot("find-incremental")
	edType(c, "ers")
	c.RequireContains("Matches: 3")

	// Enter moves to the next match. The prompt owns the status bar while it is
	// open, so the position is checked after Esc closes it: the match stays
	// selected and the caret report names the line it landed on.
	edSend(c, harness.KeyEnter)
	edSend(c, harness.KeyEsc)
	c.WaitGone("Find:", 5*time.Second)
	c.RequireContains("Ln 2,", "1 line(s) / 5 char(s) selected")
	c.Shot("find-next-match")

	// Up from the second match goes back to the first. Reopening the prompt
	// seeds the query from the selection, so no typing is needed.
	edSend(c, harness.KeyCtrlF)
	c.WaitFor("Matches: 3", 5*time.Second)
	c.Shot("find-seeded-from-selection")
	edSend(c, harness.KeyUp)
	edSend(c, harness.KeyEsc)
	c.WaitGone("Find:", 5*time.Second)
	c.RequireContains("Ln 1,")

	// Clear the query and search for something that does not exist.
	edSend(c, harness.KeyCtrlF)
	c.WaitFor("Matches: 3", 5*time.Second)
	for i := 0; i < 5; i++ {
		edSend(c, harness.KeyBackspace)
	}
	c.RequireContains("Type to search")
	edType(c, "zzz")
	c.RequireContains("No matches")
	c.Shot("find-no-matches")

	for i := 0; i < 3; i++ {
		edSend(c, harness.KeyBackspace)
	}
	edSend(c, harness.KeyEsc)
	c.WaitGone("Find:", 5*time.Second)
	c.RequireContains("SELECT id, email FROM users")
}

// ---- 4. Comment toggle through the Edit menu ----

// TestEditorCommentToggle comments and uncomments a whole selection and then a
// single line, through Edit -> Toggle Comment (Alt+E, T), and finally checks the
// direct Ctrl+/ shortcut on the same buffer.
func TestEditorCommentToggle(t *testing.T) {
	c := edStartShop(t)

	edSelectAll(c)
	edTypeScript(c,
		"SELECT id, name",
		"FROM users",
		"WHERE age > 30;",
	)
	edSelectAll(c)
	c.Shot("plain-script")

	// Comment the whole selection.
	c.Menu('e', 't')
	c.RequireContains("-- SELECT id, name", "-- FROM users", "-- WHERE age > 30;")
	c.Shot("commented-all")

	// Commenting a fully commented selection uncomments it again.
	edSelectAll(c)
	c.Menu('e', 't')
	c.RequireContains("SELECT id, name", "FROM users", "WHERE age > 30;")
	c.RequireNotContains("-- SELECT", "-- FROM users")
	c.Shot("uncommented-all")

	// With the caret on the second line, only that line is commented.
	edCaretHome(c)
	edSend(c, harness.KeyDown)
	c.RequireContains("Ln 2,")
	c.Menu('e', 't')
	c.RequireContains("-- FROM users")
	c.RequireNotContains("-- SELECT")
	c.Shot("commented-one-line")

	// And uncommenting the same line restores it.
	c.Menu('e', 't')
	c.RequireNotContains("-- FROM users")
	c.RequireContains("FROM users")
	c.Shot("uncommented-one-line")

	// The direct shortcut does the same thing: Ctrl+/ reaches the app as
	// ctrl+_ through the console and is aliased onto Ctrl+/.
	edSelectAll(c)
	edSend(c, harness.KeyCtrlSlash)
	c.RequireContains("-- SELECT id, name", "-- FROM users", "-- WHERE age > 30;")
	c.Shot("commented-ctrl-slash")
	edSelectAll(c)
	edSend(c, harness.KeyCtrlSlash)
	c.RequireNotContains("-- SELECT", "-- FROM users")
	c.Shot("uncommented-ctrl-slash")
}

// ---- 5. Format through the Edit menu ----

// TestEditorFormatTrickySQL formats two statements through Edit -> Format
// (Alt+E, F): a SELECT full of things a formatter must not damage (quoted
// identifiers, string literals, a line comment, a CASE, a subquery) and a
// SQLite trigger whose BEGIN…END body must stay byte-for-byte.
func TestEditorFormatTrickySQL(t *testing.T) {
	c := edStartShop(t)

	// --- A report query with strings, a comment, a CASE and a subquery ---
	edSelectAll(c)
	edTypeScript(c,
		`select "order", case when total > 100 then 'big' else 'small' end as size, (select count(*) from orders) as n`,
		`from orders where status = 'paid' -- keep this`,
	)
	edCaretHome(c)
	c.Shot("unformatted-report")
	c.Menu('e', 'f')
	c.WaitFor("SQL formatted", 10*time.Second)
	c.RequireContains(
		"SELECT",
		`"order",`,
		"CASE WHEN total > 100 THEN 'big' ELSE 'small' END AS size,",
		"(SELECT count(*) FROM orders) AS n",
		"FROM orders",
		"WHERE status = 'paid' -- keep this",
	)
	c.RequireOrderBefore("FROM orders", "WHERE status = 'paid'")
	c.Shot("formatted-report")

	// Formatting is one edit, so one undo restores the exact text the user
	// typed (lower-case keywords and all).
	edSend(c, harness.KeyCtrlZ)
	c.RequireContains("case when total > 100", "from orders where status")
	c.RequireNotContains("CASE WHEN total")
	c.Shot("undo-format")
	edWaitCaretStatus(c) // let the "SQL formatted" message expire before the next one

	// --- A SQLite trigger: the BEGIN…END body is copied verbatim ---
	edSelectAll(c)
	edTypeScript(c,
		"create trigger trg_audit after insert on orders begin",
		"  insert into audit_log(action, payload) values ('insert_order', NEW.id);",
		"end;",
	)
	edCaretHome(c)
	c.Shot("unformatted-trigger")
	c.Menu('e', 'f')
	c.WaitFor("SQL formatted", 10*time.Second)
	c.RequireContains(
		"CREATE trigger trg_audit after INSERT",
		"ON orders BEGIN",
		"insert into audit_log(action, payload) values ('insert_order', NEW.id);",
		"END;",
	)
	// The body is verbatim: it must not be re-cased by the formatter.
	c.RequireNotContains("INSERT INTO audit_log")
	c.Shot("formatted-trigger")
}

// ---- 6. Save to the command-line file and reopen through the picker ----

// TestEditorSaveAndOpenFile opens report.sql from the command line, writes a
// report query, saves it with Ctrl+S, verifies the exact bytes from Go, edits
// and saves again, then opens a second SQL file through the Ctrl+O picker and
// checks the Esc cancel path.
func TestEditorSaveAndOpenFile(t *testing.T) {
	c := scenario.New(t)
	db := scenario.ShopSQLite(t, c)
	report := filepath.Join(c.ArtifactDir(), "report.sql")
	weekly := filepath.Join(c.ArtifactDir(), "weekly.sql")
	// The artifact directory survives between runs; start from a clean slate so
	// "a new file on the command line" means exactly that.
	_ = os.Remove(report)
	_ = os.Remove(weekly)

	// A file passed on the command line: the tab holds it and Ctrl+S saves it.
	c.Start("-d", "sqlite", "-dsn", db, report)
	c.WaitFor("conn shop.db", 25*time.Second)
	c.RequireContains("SELECT 1;") // the starter statement of a new file
	c.Shot("started-with-file")

	edSelectAll(c)
	edTypeScript(c,
		"SELECT u.email, SUM(o.total) AS spent",
		"FROM users u JOIN orders o ON o.user_id = u.id",
		"GROUP BY u.email;",
	)
	wantReport := "SELECT u.email, SUM(o.total) AS spent\n" +
		"FROM users u JOIN orders o ON o.user_id = u.id\n" +
		"GROUP BY u.email;"
	edSend(c, harness.KeyCtrlS)
	c.WaitFor("Saved", 10*time.Second)
	edDismissPopup(c, 2)
	c.RequireContains("report.sql")
	c.Shot("saved")
	edWaitFile(t, report, "the typed report", func(got string) bool { return got == wantReport })

	// A second save after the analyst appends a note to the last line rewrites
	// the file. The note is typed on the same line on purpose: it keeps the
	// scenario independent of the completion popup a plain Enter could accept.
	edType(c, " -- reviewed by analyst")
	edSend(c, harness.KeyCtrlS)
	c.WaitFor("Saved", 10*time.Second)
	edDismissPopup(c, 2)
	c.Shot("saved-again")
	edWaitFile(t, report, "the edited report", func(got string) bool {
		return got == wantReport+" -- reviewed by analyst"
	})

	// A second SQL file appears next to it; open it through the picker.
	weeklySQL := "SELECT strftime('%Y-%W', created_at) AS week,\n" +
		"       COUNT(*) AS orders\n" +
		"FROM orders\n" +
		"GROUP BY week;"
	if err := os.WriteFile(weekly, []byte(weeklySQL), 0o644); err != nil {
		t.Fatalf("write weekly.sql: %v", err)
	}

	edSend(c, harness.KeyCtrlO)
	c.WaitFor("Open SQL file", 10*time.Second)
	c.RequireContains("weekly.sql", "Showing .sql")
	c.Shot("open-picker")
	edType(c, "week")
	c.RequireContains("weekly.sql")
	edSend(c, harness.KeyEnter) // opens the highlighted file in a new tab
	c.WaitFor("strftime('%Y-%W'", 10*time.Second)
	c.RequireContains("GROUP BY week;", "weekly.sql")
	c.Shot("opened-weekly")

	// Esc cancels the picker and leaves the editor alone.
	edSend(c, harness.KeyCtrlO)
	c.WaitFor("Open SQL file", 10*time.Second)
	edSend(c, harness.KeyEsc)
	c.WaitGone("Open SQL file", 10*time.Second)
	c.RequireContains("strftime('%Y-%W'", "GROUP BY week;")
	c.Shot("picker-cancelled")
}

// TestEditorSaveWithoutFileFeedback saves a tab that has no file: File -> Save
// must say so in the status bar instead of doing nothing.
func TestEditorSaveWithoutFileFeedback(t *testing.T) {
	c := edStartShop(t)
	// The starter tab of `tdb -d sqlite -dsn …` has no file.
	edSend(c, harness.KeyCtrlS)
	c.WaitFor(edNoFileSaveMsg, 5*time.Second)
	c.Shot("save-without-file")

	// The same message through the File menu's Save entry.
	edWaitCaretStatus(c)
	c.Menu('f', 's')
	c.WaitFor(edNoFileSaveMsg, 5*time.Second)
	c.Shot("save-without-file-menu")
}

// ---- 7. Tabs: switching, results per tab, rename, duplicate, close ----

// TestEditorTabsContextMenu runs two independent query tabs, switches them with
// the View menu (Ctrl+PgUp/PgDown do not arrive with their modifier through a
// ConPTY), then renames, duplicates and closes a tab through its right-click
// context menu.
func TestEditorTabsContextMenu(t *testing.T) {
	c := edStartShop(t)

	// Tab 1: users.
	edSelectAll(c)
	edType(c, "SELECT id, email FROM users ORDER BY id;")
	edRun(c, "ada@example.com")
	c.RequireContains("5 rows in")

	// Tab 2: products, with its own result grid.
	edSend(c, harness.KeyCtrlT)
	edType(c, "SELECT sku, name, price FROM products ORDER BY id;")
	edRun(c, "Keyboard")
	c.RequireContains("4 rows in")
	c.Shot("two-tabs")

	// View -> Previous Tab / Next Tab: each tab keeps its own results.
	c.Menu('v', 'p')
	c.RequireContains("SELECT id, email FROM users", "ada@example.com")
	c.RequireNotContains("Keyboard")
	c.Shot("previous-tab")
	c.Menu('v', 'n')
	c.RequireContains("SELECT sku, name, price", "Keyboard")
	c.RequireNotContains("ada@example.com")
	c.Shot("next-tab")

	// A left click on the first tab switches too.
	edClickTab(c, 0)
	c.RequireContains("SELECT id, email FROM users", "ada@example.com")

	// Rename tab 1 through its right-click menu: Rename… is the first item.
	edRightClickTab(c, 0)
	c.WaitFor("Rename…", 5*time.Second)
	c.RequireContains("Duplicate", "Close")
	c.Shot("tab-context-menu")
	edSend(c, harness.KeyEnter)
	c.WaitFor("New name", 5*time.Second)
	c.RequireContains("Automatic name:")
	edType(c, "Report A")
	edSend(c, harness.KeyEnter)
	c.WaitFor("Tab renamed", 10*time.Second)
	edTabLineContains(c, "Report A")
	c.Shot("tab-renamed")

	// Duplicate it: the copy lands right after the original and shows the same
	// SQL, so there are two "Report A" tabs.
	edRightClickTab(c, 0)
	c.WaitFor("Duplicate", 5*time.Second)
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyEnter)
	c.WaitFor("Tab duplicated", 10*time.Second)
	if got := edTabCount(c, "Report A"); got != 2 {
		c.Fail("duplicated tab not on screen: %d tabs say %q", got, "Report A")
	}
	c.RequireContains("SELECT id, email FROM users")
	c.Shot("tab-duplicated")

	// Close the duplicate through the same menu: the bulk closes follow it, so
	// Close is reached with Home and two downs, not with End.
	edRightClickTab(c, 1)
	c.WaitFor("Close", 5*time.Second)
	edSend(c, harness.KeyHome)
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyEnter)
	c.WaitFor("Tab closed", 10*time.Second)
	if got := edTabCount(c, "Report A"); got != 1 {
		c.Fail("duplicate tab was not closed: %d tabs say %q", got, "Report A")
	}
	c.Shot("tab-closed")

	// The two remaining tabs still hold their own SQL.
	edClickTab(c, 1)
	c.RequireContains("SELECT sku, name, price")
	c.Shot("second-tab-after-close")

	// Closing every tab falls back to the welcome page instead of quitting.
	edSend(c, harness.KeyCtrlW)
	edSend(c, harness.KeyCtrlW)
	c.WaitFor("Welcome to TDB", 10*time.Second)
	c.RequireContains("New query", "Keyboard shortcuts")
	c.Shot("welcome-fallback")
}

// ---- 8. Query history ----

// TestEditorHistory runs a few statements, browses the Query History (Alt+Q, H
// because Ctrl+H cannot arrive through a ConPTY), filters it, reopens an entry
// in a new tab and deletes another one; the history file on disk is verified
// with Go.
func TestEditorHistory(t *testing.T) {
	// History is persisted next to the settings file and the artifact directory
	// survives between runs, so start from an empty log: the scenario counts its
	// own three statements.
	c := scenario.New(t)
	db := scenario.ShopSQLite(t, c)
	histPath := filepath.Join(c.ArtifactDir(), "settings", "history.json")
	_ = os.Remove(histPath)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", db)
	c.WaitFor("conn shop.db", 25*time.Second)

	queries := []struct {
		sql  string
		want string
	}{
		{"SELECT 'alpha' AS tag;", "alpha"},
		{"SELECT COUNT(*) AS orders_count FROM orders;", "orders_count"},
		{"SELECT name, city FROM users WHERE city IS NOT NULL;", "Shanghai"},
	}
	for _, q := range queries {
		edSelectAll(c)
		edType(c, q.sql)
		edRun(c, q.want)
	}

	// The newest statement is at the top of the list.
	c.Menu('q', 'h')
	c.WaitFor("Query History (3)", 10*time.Second)
	c.RequireContains("WHERE city IS NOT NULL", "orders_count", "alpha")
	c.Shot("history-open")

	// "/" filters the list to the entries whose SQL matches.
	edSend(c, "/")
	c.RequireContains("Type to filter")
	edType(c, "orders_count")
	c.WaitFor("Query History (1)", 10*time.Second)
	c.RequireContains("orders_count")
	c.RequireNotContains("alpha")
	c.Shot("history-filtered")

	// Enter leaves the filter input with the filter still in effect; Esc then
	// clears the filter and the next Esc closes the dialog.
	edSend(c, harness.KeyEnter)
	c.WaitFor("Query History (1)", 5*time.Second)
	edSend(c, harness.KeyEsc)
	c.WaitFor("Query History (3)", 10*time.Second)
	edSend(c, harness.KeyEsc)
	c.WaitGone("Query History (", 10*time.Second)
	c.Shot("history-closed")

	// Enter on an entry opens the statement in a fresh tab.
	edSend(c, harness.KeyCtrlT) // leave the first tab alone; history opens beside it
	edSend(c, harness.KeyCtrlW)
	c.Menu('q', 'h')
	c.WaitFor("Query History (3)", 10*time.Second)
	edSend(c, harness.KeyDown) // second entry: the orders count
	edSend(c, harness.KeyEnter)
	c.WaitGone("Query History (", 10*time.Second)
	c.RequireContains("COUNT(*) AS orders_count FROM orders")
	c.Shot("history-entry-opened")

	// Delete an entry: the title shrinks and the message confirms it.
	c.Menu('q', 'h')
	c.WaitFor("Query History (3)", 10*time.Second)
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyDelete)
	c.WaitFor("Removed a history entry", 10*time.Second)
	c.WaitFor("Query History (2)", 10*time.Second)
	c.Shot("history-entry-deleted")
	edSend(c, harness.KeyEsc)
	c.WaitGone("Query History (", 10*time.Second)

	// History persists next to the settings file (history.json).
	content := edWaitFile(t, histPath, "a saved history", func(s string) bool {
		return strings.Contains(s, "alpha") && !strings.Contains(s, "orders_count")
	})
	if !strings.Contains(content, "SELECT 'alpha' AS tag") {
		t.Fatalf("history.json lost the alpha statement:\n%s", content)
	}
}

// ---- 9. Key bindings, preferences and About ----

// TestEditorDialogsAndMenus opens the Key Bindings overlay with F1, the
// Preferences dialog with F2 (toggling a boolean and switching the theme), and
// the About item through the application menu, which has no Alt accelerator and
// is therefore opened with a mouse click on the "TDB" title.
func TestEditorDialogsAndMenus(t *testing.T) {
	c := edStartShop(t)
	edSelectAll(c)
	edType(c, "SELECT id, email FROM users;")
	c.Shot("ready")

	// --- F1: Key Bindings overlay ---
	edSend(c, harness.KeyF1)
	c.WaitFor("Key Bindings", 10*time.Second)
	c.RequireContains("Toggle Comment", "^/", "F5", "Esc close")
	c.Shot("keybindings")
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyDown)
	c.Shot("keybindings-scrolled")
	edSend(c, harness.KeyEnd)
	c.Shot("keybindings-end")
	edSend(c, harness.KeyEsc)
	c.WaitGone("Key Bindings", 10*time.Second)
	c.RequireContains("SELECT id, email FROM users")

	// --- F2: Preferences dialog ---
	edSend(c, harness.KeyF2)
	c.WaitFor("Language", 10*time.Second)
	c.RequireContains("Theme", "Icons", "Mouse", "Line numbers", "Read-only mode", "Esc close")
	c.Shot("preferences")

	// Tab moves between rows (Down on a select row opens its dropdown instead,
	// which is how the language/theme rows are driven). Line numbers is the
	// fifth row, so Tab four times, then Enter toggles it.
	edSend(c, harness.KeyTab)
	edSend(c, harness.KeyTab)
	edSend(c, harness.KeyTab)
	edSend(c, harness.KeyTab)
	edSend(c, harness.KeyEnter)
	c.WaitFor("Line numbers hidden", 10*time.Second)
	c.Shot("preferences-gutter-off")
	edSend(c, harness.KeyEnter)
	c.WaitFor("Line numbers shown", 10*time.Second)

	// The theme row opens a dropdown; picking Light repaints the whole UI.
	for i := 0; i < 3; i++ { // Shift+Tab back up to Theme
		edSend(c, harness.KeyShiftTab)
	}
	edSend(c, harness.KeyEnter)
	c.WaitFor("Light", 10*time.Second)
	c.Shot("preferences-theme-dropdown")
	edSend(c, harness.KeyDown)
	edSend(c, harness.KeyEnter)
	c.Shot("preferences-light-theme")
	c.RequireContains("Light")
	// Switch back to the dark theme.
	edSend(c, harness.KeyEnter)
	c.WaitFor("Dark", 5*time.Second)
	edSend(c, harness.KeyUp)
	edSend(c, harness.KeyEnter)
	c.Shot("preferences-dark-theme")

	edSend(c, harness.KeyEsc)
	c.WaitGone("Read-only mode", 10*time.Second)
	c.RequireContains("Settings saved")

	// --- About through the application menu (mouse click on the title) ---
	c.ClickText("TDB")
	c.WaitFor("About TDB", 10*time.Second)
	c.RequireContains("Preferences…", "Quit TDB")
	c.Shot("app-menu")
	edSend(c, "a")
	c.WaitFor("a terminal database client", 10*time.Second)
	c.Shot("about-message")
}

// ---- 10. Multibyte / Unicode editing ----

// TestEditorUnicode writes a script with CJK text and a Latin accent, checks the
// caret arithmetic, runs it, undoes an edit, searches for a CJK substring and
// appends a CJK comment with a bracketed paste.
//
// Emoji note: the ConPTY input relay drops 4-byte UTF-8 characters before they
// reach the application, so an emoji cannot be typed or pasted into this
// harness at all (the same app has unit coverage for multibyte text elsewhere).
// The scenario still sends one at the end to show the UI neither crashes nor
// corrupts the buffer; it asserts the caret report stays sane instead of
// asserting the emoji itself.
func TestEditorUnicode(t *testing.T) {
	c := edStartShop(t)

	// CJK typed as keystrokes; the newline goes through edTypeScript's Enter,
	// which is what a user typing the script does.
	edSelectAll(c)
	edTypeScript(c,
		"-- 数据分析: 本周销售",
		"SELECT '日本語' AS lang, 'naïve' AS accent;",
	)
	c.Shot("unicode-typed")
	c.RequireContains("数据分析", "日本語", "naïve")
	c.RequireNotContains("SELECT 1;")

	// Caret arithmetic counts runes, not bytes or cells: three Right presses
	// from the buffer start land on column 4 of the comment line.
	edCaretHome(c)
	c.RequireContains("Ln 1, Col 1")
	edSend(c, harness.KeyRight)
	edSend(c, harness.KeyRight)
	edSend(c, harness.KeyRight)
	c.RequireContains("Ln 1, Col 4")
	c.Shot("unicode-caret")

	// End of buffer is the end of the second line, after the accented literal.
	edCaretEnd(c)
	c.RequireContains("Ln 2, Col")
	c.Shot("unicode-caret-end")

	// Backspace deletes one rune (the semicolon) and undo brings it back.
	edSend(c, harness.KeyBackspace)
	c.RequireNotContains("accent;")
	c.RequireContains("accent")
	c.Shot("unicode-backspace")
	edSend(c, harness.KeyCtrlZ)
	c.RequireContains("accent;")

	// The statement runs and the grid keeps the multibyte values intact.
	edCaretEnd(c)
	edRun(c, "1 row in")
	c.RequireContains("日本語")
	c.Shot("unicode-results")

	// Find works on CJK text as well.
	edSend(c, harness.KeyCtrlF)
	c.WaitFor("Find:", 5*time.Second)
	edType(c, "日本語")
	c.WaitFor("Matches: 1", 5*time.Second)
	c.RequireContains("日本語")
	c.Shot("unicode-find")
	edSend(c, harness.KeyEsc)
	c.WaitGone("Find:", 5*time.Second)

	// A single-line CJK comment arrives through a bracketed paste (the way a
	// real terminal delivers Ctrl+V).
	edCaretEnd(c)
	edPaste(c, " -- 备注: 已复核")
	c.RequireContains("备注", "已复核")
	c.Shot("unicode-pasted")

	// A 4-byte emoji is dropped by the ConPTY input relay: the UI must stay
	// alive and keep the caret math consistent.
	before := edStatus(c)
	edSend(c, "🎉")
	c.RequireContains("备注", "已复核")
	if after := edStatus(c); after == "" {
		c.Fail("status bar disappeared after an emoji keystroke")
	} else if after != before {
		t.Logf("emoji keystroke changed the caret report from %q to %q "+
			"(the input relay normally drops it)", before, after)
	}
	c.Shot("unicode-emoji")
}

// ---- 11. Error paths must not cost the user their buffer ----

// TestEditorErrorPaths runs a statement against a missing table and a syntax
// error, checks the error surfaces while the SQL stays in the editor, then runs
// F5 on an empty buffer and recovers with a valid statement.
func TestEditorErrorPaths(t *testing.T) {
	c := edStartShop(t)

	// A missing table: the grid says the query failed and names the table,
	// while the editor keeps the statement.
	edSelectAll(c)
	edType(c, "SELECT * FROM missing_table;")
	edSend(c, harness.KeyF5)
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("no such table", "missing_table")
	c.Shot("missing-table-error")

	// The buffer is intact, so a correction runs straight away.
	edSelectAll(c)
	edType(c, "SELECT COUNT(*) AS product_count FROM products;")
	edRun(c, "product_count")
	c.RequireContains("1 row in")
	c.Shot("recovered")

	// A syntax error is reported the same way, without eating the text.
	edSelectAll(c)
	edType(c, "SELEC 1;")
	edSend(c, harness.KeyF5)
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("SELEC 1;", "syntax error")
	c.Shot("syntax-error")

	// F5 on an empty buffer is harmless: a hint, no error, no crash.
	edSelectAll(c)
	edSend(c, harness.KeyDelete)
	c.RequireNotContains("SELEC 1;")
	edSend(c, harness.KeyF5)
	c.WaitFor("Nothing to run", 10*time.Second)
	c.Shot("empty-run")

	// The editor still works after all of that.
	edType(c, "SELECT 1 AS one;")
	edRun(c, "one")
	c.RequireContains("1 row in")
	c.Shot("final-run")
}
