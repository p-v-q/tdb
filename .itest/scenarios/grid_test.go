package scenarios

// grid_test.go — realistic end-to-end scenarios for the result grid, export,
// grid editing, read-only mode and the confirmation dialogs (shared task-3).
//
// Every scenario drives the real tdb.exe inside a Windows ConPTY and
// asserts on the rendered screen (the harness's VT emulator); database effects
// are verified through an independent database/sql connection opened on the
// same SQLite file, never through the UI.
//
// Run:
//   . C:\Users\qxwxi\repos\tdb\.itest\env.ps1
//   cd C:\Users\qxwxi\repos\tdb\.itest
//   go test ./scenarios -run 'TestGrid' -v -count=1 -timeout 900s

import (
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// ---- helpers ----

// startGridShop launches TDB on the standard scenario database (users,
// products, orders, audit_log, v_order_totals).
func startGridShop(t *testing.T) *scenario.Ctx {
	t.Helper()
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)
	return c
}

// startGridDB creates a SQLite fixture with the given statements, launches TDB
// on it and waits for the connection banner.
func startGridDB(t *testing.T, name string, stmts ...string) (*scenario.Ctx, string) {
	t.Helper()
	c := scenario.New(t)
	dbPath := scenario.CreateSQLite(t, c.ArtifactDir(), name, stmts...)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn "+name, 25*time.Second)
	return c, dbPath
}

// gridRun replaces the editor content and runs it the way a user does:
// select all, type the statement, F5.
func gridRun(c *scenario.Ctx, sqlText string) {
	c.T.Helper()
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", sqlText)
	c.Keys("run", harness.KeyF5)
}

// send sends keys without taking a snapshot (cheap navigation between shots).
func send(c *scenario.Ctx, keys string) {
	c.T.Helper()
	if err := c.S.Send(keys); err != nil {
		c.Fail("send %q: %v", keys, err)
	}
}

// logScreen writes the full rendered screen into the test log / artifact log.
func logScreen(c *scenario.Ctx, label string) {
	c.T.Helper()
	c.Logf("=== %s ===\n%s", label, c.Screen())
}

// headerRow returns the screen row index of the result grid's column-name row
// and that row's text. A result grid always paints its summary line directly
// above the header, so the summary is used as the anchor (a bare search for a
// column name would hit the SQL editor text first).
func headerRow(c *scenario.Ctx) (int, string) {
	c.T.Helper()
	lines := strings.Split(c.Screen(), "\n")
	for y, line := range lines {
		if strings.Contains(line, " rows in ") || strings.Contains(line, " affected in ") {
			if y+1 < len(lines) {
				return y + 1, lines[y+1]
			}
		}
	}
	c.Fail("no result summary line on screen")
	return 0, ""
}

// clickColumnHeader clicks the named result column's header cell, which the
// grid treats as "sort by this column".
func clickColumnHeader(c *scenario.Ctx, name string) {
	c.T.Helper()
	y, line := headerRow(c)
	idx := strings.Index(line, name)
	if idx < 0 {
		c.Fail("column %q is not in the header row %d: %q", name, y, line)
	}
	c.ClickLeft(idx+1, y)
}

// clickDialogButton finds the modal button row (the one that also carries the
// Cancel label) and clicks the given label on it.
func clickDialogButton(c *scenario.Ctx, label string) {
	c.T.Helper()
	for y, line := range strings.Split(c.Screen(), "\n") {
		if strings.Contains(line, "Cancel") && strings.Contains(line, label) {
			idx := strings.Index(line, label)
			c.ClickLeft(idx+1, y)
			return
		}
	}
	c.Fail("dialog button %q not found (no row with both it and Cancel)", label)
}

// clipboardSince decodes the last OSC 52 payload written after raw position n,
// or "<none>" when the copy path wrote nothing.
func clipboardSince(c *scenario.Ctx, n int) string {
	raw := string(c.S.RawOutput()[n:])
	i := strings.Index(raw, "\x1b]52;c;")
	if i < 0 {
		return "<none>"
	}
	payload := raw[i+len("\x1b]52;c;"):]
	if j := strings.IndexAny(payload, "\x07\x1b"); j >= 0 {
		payload = payload[:j]
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Sprintf("<decode error %v: %q>", err, payload)
	}
	return string(decoded)
}

// dbCount runs a one-column query on an independent SQLite connection.
func dbCount(db *sql.DB, query string) (int64, error) {
	var n int64
	err := db.QueryRow(query).Scan(&n)
	return n, err
}

// waitDB polls the independent connection until want is reached or the timeout
// passes, tolerating the transient "database is locked" a concurrent writer
// can produce. It reports the last value/error for the failure message.
func waitDB(t *testing.T, db *sql.DB, query string, want int64, timeout time.Duration) (int64, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = dbCount(db, query)
		if lastErr == nil && last == want {
			return last, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last, lastErr
}

// clearPathField deletes the export-dialog path by pressing Backspace enough
// times to empty the suggested name.
func clearPathField(c *scenario.Ctx) {
	c.T.Helper()
	send(c, strings.Repeat(harness.KeyBackspace, 90))
}

// ---- 1. results grid: rows, types, sort (keyboard + mouse), filter, copy ----

// TestGridResultsSortFilterCopy walks an analyst's first pass over a result:
// row counts and timings, the column-type row, sorting with 's' and by
// clicking the header, the sort indicator, '/' filtering (Enter/Esc), copying
// one cell with 'y', the caret row with Ctrl+C and a Shift-arrow range.
func TestGridResultsSortFilterCopy(t *testing.T) {
	c := startGridShop(t)
	gridRun(c, "SELECT id, email, city FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)

	// Row count and timing, and the dimmed type row under the names.
	c.RequireContains("5 rows in ", "id", "email", "city")
	c.RequireMatch(`5 rows in \d+(\.\d+)?(µs|ms|s)`)
	c.RequireContains("INTEGER", "TEXT")
	c.Shot("results")

	// Focus the grid: the status bar advertises the grid keys.
	c.FocusResults()
	c.RequireContains("⇧↑↓ select", "s sort", "/ filter")
	send(c, harness.KeyRight) // id -> email
	c.Keys("sort-asc", "s")
	c.RequireContains("▲")
	c.RequireOrderBefore("ada@example.com", "yuan@example.com")
	c.Shot("sorted-ascending")

	c.Keys("sort-descending", "s")
	c.RequireContains("▼")
	c.RequireOrderBefore("yuan@example.com", "ada@example.com")
	c.Shot("sorted-descending")

	// A third 's' restores the database order: no arrow at all.
	c.Keys("sort-restored", "s")
	c.RequireNotContains("▲", "▼")

	// Clicking the header sorts by that column too (mouse path).
	clickColumnHeader(c, "email")
	c.RequireContains("▲")
	c.RequireOrderBefore("ada@example.com", "yuan@example.com")
	c.Shot("sorted-by-click")

	// Filter with '/', apply with Enter.
	c.Keys("open-filter", "/")
	c.RequireContains("Filter")
	c.Type("filter-text", "Shanghai")
	c.Keys("apply-filter", harness.KeyEnter)
	c.WaitFor("filtered from 5", 10*time.Second)
	c.RequireContains("lin@example.com")
	c.RequireNotContains("ada@example.com")
	c.Shot("filtered")

	// '/' then Esc clears the filter again.
	c.Keys("reopen-filter", "/")
	c.Keys("clear-filter", harness.KeyEsc)
	c.WaitGone("filtered from 5", 10*time.Second)
	c.WaitFor("ada@example.com", 10*time.Second)
	c.Shot("filter-cleared")

	// 'y' copies the cell under the caret (still the email column, first row).
	before := len(c.S.RawOutput())
	c.Keys("copy-cell", "y")
	if got := clipboardSince(c, before); got != "ada@example.com" {
		c.Fail("y must copy the caret cell %q, copied %q", "ada@example.com", got)
	}
	c.RequireContains("Copied 15 characters")

	// Ctrl+C without a selection copies the whole caret row as TSV.
	before = len(c.S.RawOutput())
	c.Keys("copy-row", harness.KeyCtrlC)
	if got := clipboardSince(c, before); got != "1\tada@example.com\tLondon" {
		c.Fail("Ctrl+C must copy the caret row as TSV, copied %q", got)
	}

	// Shift+Down extends the selection; the summary counts the rows and Ctrl+C
	// copies exactly them.
	send(c, "\x1b[1;2B") // shift+down
	send(c, "\x1b[1;2B")
	c.WaitFor("3 rows selected", 10*time.Second)
	c.Shot("rows-selected")
	before = len(c.S.RawOutput())
	c.Keys("copy-selection", harness.KeyCtrlC)
	got := clipboardSince(c, before)
	if lines := strings.Split(got, "\n"); len(lines) != 3 {
		c.Fail("the selection copy must carry 3 TSV rows, got %d: %q", len(lines), got)
	}
	c.RequireContains("Copied 3 rows")
	c.Shot("selection-copied")
}

// ---- 2. NULL vs empty string ----

// TestGridNullVsEmptyString checks that a SQL NULL is drawn as "NULL" and is
// not confused with the empty string: only one NULL is on screen (the NULL
// name) and copying an empty cell writes nothing while copying NULL puts the
// literal NULL on the clipboard.
func TestGridNullVsEmptyString(t *testing.T) {
	c := startGridShop(t)
	gridRun(c, "SELECT id, name, '' AS empty_text FROM users ORDER BY id;")
	c.WaitFor("Ada Lovelace", 25*time.Second)

	// Bob's name is NULL; every empty_text cell is the empty string. Exactly
	// one NULL is rendered, so "", which is drawn blank, is not spelled NULL.
	if n := strings.Count(c.Screen(), "NULL"); n != 1 {
		c.Fail("expected exactly one NULL cell on screen, found %d\n%s", n, c.Screen())
	}
	c.RequireContains("empty_text")
	c.Shot("null-and-empty")

	c.FocusResults()
	send(c, harness.KeyDown)  // first row -> bob
	send(c, harness.KeyRight) // id -> name
	before := len(c.S.RawOutput())
	c.Keys("copy-null", "y")
	if got := clipboardSince(c, before); got != "NULL" {
		c.Fail("copying the NULL cell must yield %q, got %q", "NULL", got)
	}
	c.RequireContains("Copied 4 characters")
	c.Shot("null-copied")

	// The empty string is a real value, not NULL: the copy path writes an
	// empty payload, which the clipboard deliberately skips, so no OSC52 is
	// emitted and the status says 0 characters.
	send(c, harness.KeyRight) // name -> empty_text
	before = len(c.S.RawOutput())
	c.Keys("copy-empty", "y")
	if got := clipboardSince(c, before); got != "<none>" {
		c.Fail("copying an empty string must not put text on the clipboard, got %q", got)
	}
	c.RequireContains("Copied 0 characters")
	c.Shot("empty-copied")
}

// ---- 3. wide table horizontal behaviour ----

// TestGridWideTableHorizontal reads a 12-column table that cannot fit and
// scrolls the grid horizontally by moving the caret: the headers and the rows
// must follow, with the far columns only reachable after the scroll.
func TestGridWideTableHorizontal(t *testing.T) {
	stmts := []string{`CREATE TABLE wide (
		id INTEGER PRIMARY KEY,
		c01 TEXT, c02 TEXT, c03 TEXT, c04 TEXT, c05 TEXT, c06 TEXT,
		c07 TEXT, c08 TEXT, c09 TEXT, c10 TEXT, c11 TEXT, c12 TEXT)`}
	for r := 1; r <= 3; r++ {
		vals := make([]string, 12)
		for i := range vals {
			vals[i] = fmt.Sprintf("'r%02d-c%02d-abcdefgh'", r, i+1)
		}
		stmts = append(stmts, fmt.Sprintf("INSERT INTO wide VALUES (%d, %s)", r, strings.Join(vals, ", ")))
	}
	c, _ := startGridDB(t, "wide.db", stmts...)
	gridRun(c, "SELECT * FROM wide ORDER BY id;")
	c.WaitFor("3 rows", 25*time.Second)

	// The left end is visible, the right end is not: the pane clips the grid.
	c.RequireContains("c01", "r01-c01-abcdefgh")
	c.RequireNotContains("c12")
	c.Shot("wide-left")

	// Move the caret across every column: ensureResultVisible scrolls the
	// column window, so the right end comes into view and the left one leaves.
	c.FocusResults()
	send(c, strings.Repeat(harness.KeyRight, 12))
	c.WaitFor("c12", 10*time.Second)
	c.RequireContains("r01-c12-abcdefgh")
	c.RequireNotContains("c01")
	c.Shot("wide-right")
}

// ---- 4. multiple result sets and a failing script ----

// TestGridResultSets runs a two-statement script and switches between the
// result sets with '[' and ']', then runs a script that fails in the middle.
func TestGridResultSets(t *testing.T) {
	c := startGridShop(t)
	gridRun(c, "SELECT id, email FROM users ORDER BY id; SELECT COUNT(*) AS order_count FROM orders;")
	c.WaitFor("Result 2/2", 25*time.Second)
	c.RequireContains("order_count", "8")
	c.Shot("second-result-set")

	c.FocusResults()
	c.Keys("previous-set", "[")
	c.WaitFor("Result 1/2", 10*time.Second)
	c.RequireContains("5 rows in", "ada@example.com")
	c.Shot("first-result-set")

	c.Keys("next-set", "]")
	c.WaitFor("Result 2/2", 10*time.Second)
	c.RequireContains("order_count")

	// A script whose middle statement fails shows the server error and does
	// not pretend the whole script produced rows.
	gridRun(c, "SELECT 1 AS one; SELECT * FROM no_such_table; SELECT 3 AS three;")
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("no such table", "no_such_table")
	c.Shot("script-failed-midway")

	// The app is still usable after the failed script.
	gridRun(c, "SELECT 42 AS answer;")
	c.WaitFor("answer", 20*time.Second)
	c.RequireContains("42")
	c.Shot("usable-after-error")
}

// ---- 5. export: CSV quoting, JSON NULL, picker, filtered view ----

// TestGridExportCSVJSON exports the grid through the F7 dialog: the path is
// chosen in the directory picker, CSV is written and verified with
// encoding/csv (comma, quote and newline quoting, NULL as empty field), JSON
// is written and verified (NULL as a real JSON null, "" as an empty string),
// and a filtered view exports only the visible rows.
func TestGridExportCSVJSON(t *testing.T) {
	c, _ := startGridDB(t, "exports.db",
		`CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT, note TEXT)`,
		`INSERT INTO items VALUES (1, 'plain', 'ok')`,
		`INSERT INTO items VALUES (2, 'quote''s', 'he said "hi"')`,
		`INSERT INTO items VALUES (3, 'comma,value', 'line1' || char(10) || 'line2')`,
		`INSERT INTO items VALUES (4, NULL, '')`,
	)

	gridRun(c, "SELECT * FROM items ORDER BY id;")
	c.WaitFor("quote's", 25*time.Second)

	csvPath := filepath.Join(c.ArtifactDir(), "items.csv")
	c.Keys("open-export", harness.KeyF7)
	c.RequireContains("Export results", "File", "Format", "Directory", "Browse…", "CSV")
	c.RequireContains("⏎ export")
	c.Shot("export-dialog")

	// Pick the target path, then use Browse… -> the directory picker to
	// confirm the folder (the filename stays as typed).
	clearPathField(c)
	c.Type("export-path", csvPath)
	c.Keys("focus-format", harness.KeyTab)
	c.Keys("focus-directory", harness.KeyTab)
	c.Keys("focus-browse", harness.KeyTab)
	c.Keys("open-picker", harness.KeyEnter)
	c.WaitFor("Choose a directory", 10*time.Second)
	c.RequireContains("Choose this folder")
	c.Shot("directory-picker")
	clickDialogButton(c, "Choose this folder")
	c.WaitFor("Export results", 10*time.Second)
	// The picker put the chosen folder in front of the file name; the long
	// path is truncated in the field, so the file itself is the proof.
	c.RequireContains("Directory", "Browse…")
	c.Shot("path-after-picker")

	// The hint says "⏎ export"; the actual submit is the Export button
	// (keyboard Enter on the path field is inert -- see the skipped defect
	// scenario). A real user clicks the button.
	clickDialogButton(c, "Export")
	c.WaitFor("Exported 4 rows", 15*time.Second)
	c.Shot("csv-exported")

	data, err := os.ReadFile(csvPath)
	if err != nil {
		c.Fail("read exported CSV: %v", err)
	}
	recs, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		c.Fail("parse exported CSV %q: %v", string(data), err)
	}
	wantCSV := [][]string{
		{"id", "label", "note"},
		{"1", "plain", "ok"},
		{"2", "quote's", `he said "hi"`},
		{"3", "comma,value", "line1\nline2"},
		{"4", "", ""},
	}
	if len(recs) != len(wantCSV) {
		c.Fail("CSV record count = %d, want %d\nraw:\n%s", len(recs), len(wantCSV), string(data))
	}
	for i := range wantCSV {
		if strings.Join(recs[i], "\x00") != strings.Join(wantCSV[i], "\x00") {
			c.Fail("CSV record %d = %q, want %q\nraw:\n%s", i, recs[i], wantCSV[i], string(data))
		}
	}
	// The quoting is what makes those values survive: embedded quotes double,
	// a comma forces quotes, a newline keeps the field quoted.
	for _, quoted := range []string{`"comma,value"`, `"he said ""hi"""`, "\"line1\nline2\""} {
		if !strings.Contains(string(data), quoted) {
			c.Fail("CSV must quote %q; raw:\n%s", quoted, string(data))
		}
	}
	// NULL becomes an empty field, exactly like "" in CSV (CSV has no NULL);
	// the JSON export below is what tells them apart.
	if !strings.Contains(string(data), "\n4,,") {
		c.Fail("NULL row must export as empty fields; raw:\n%s", string(data))
	}

	// JSON: NULL becomes a real JSON null, the empty string stays "".
	jsonPath := filepath.Join(c.ArtifactDir(), "items.json")
	c.Keys("open-export-json", harness.KeyF7)
	clearPathField(c)
	c.Type("json-path", jsonPath)
	c.Keys("focus-format", harness.KeyTab)
	c.Keys("open-formats", harness.KeyEnter)
	c.RequireContains("Markdown")
	c.Keys("to-tsv", harness.KeyDown)
	c.Keys("to-json", harness.KeyDown)
	c.Shot("format-json")
	c.Keys("pick-json", harness.KeyEnter)
	c.RequireContains("JSON ▾")
	clickDialogButton(c, "Export")
	c.WaitFor("Exported 4 rows", 15*time.Second)
	c.Shot("json-exported")

	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		c.Fail("read exported JSON: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rawJSON, &rows); err != nil {
		c.Fail("parse exported JSON %q: %v", string(rawJSON), err)
	}
	if len(rows) != 4 {
		c.Fail("JSON row count = %d, want 4: %s", len(rows), string(rawJSON))
	}
	if rows[3]["label"] != nil {
		c.Fail("NULL must be a JSON null, got %#v in %s", rows[3]["label"], string(rawJSON))
	}
	if v, ok := rows[3]["note"].(string); !ok || v != "" {
		c.Fail("the empty string must stay \"\", got %#v in %s", rows[3]["note"], string(rawJSON))
	}
	if rows[2]["note"] != "line1\nline2" {
		c.Fail("embedded newline must survive JSON export, got %#v", rows[2]["note"])
	}
	if !strings.Contains(string(rawJSON), `"label":null`) {
		c.Fail("raw JSON must carry a bare null: %s", string(rawJSON))
	}

	// Export what the user sees: filter the grid down to one row first.
	c.FocusResults()
	c.Keys("open-filter", "/")
	c.Type("filter", "comma")
	c.Keys("apply-filter", harness.KeyEnter)
	c.WaitFor("filtered from 4", 10*time.Second)
	filteredPath := filepath.Join(c.ArtifactDir(), "filtered.csv")
	c.Keys("open-export-filtered", harness.KeyF7)
	clearPathField(c)
	c.Type("filtered-path", filteredPath)
	clickDialogButton(c, "Export")
	c.WaitFor("Exported 1 row", 15*time.Second)
	c.Shot("filtered-exported")

	filtered, err := os.ReadFile(filteredPath)
	if err != nil {
		c.Fail("read filtered CSV: %v", err)
	}
	fRecs, err := csv.NewReader(strings.NewReader(string(filtered))).ReadAll()
	if err != nil {
		c.Fail("parse filtered CSV %q: %v", string(filtered), err)
	}
	if len(fRecs) != 2 || fRecs[1][1] != "comma,value" {
		c.Fail("the filtered export must contain header + 1 row, got %q", fRecs)
	}
}

// ---- 6. grid editing end to end ----

// TestGridEditApply edits a cell, commits it in memory, reads the exact UPDATE
// in the confirmation dialog, cancels once (nothing written), applies it
// (verified in the database), then edits a cell to NULL with \N, applies that,
// and finally discards a pending change through the Query menu.
func TestGridEditApply(t *testing.T) {
	c := startGridShop(t)
	dbPath := filepath.Join(c.ArtifactDir(), "shop.db")
	dbh := c.Env.OpenSQLite(t, dbPath)
	defer dbh.Close()

	gridRun(c, "SELECT * FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)
	c.FocusResults()

	// The primary key is not editable: it is what addresses the row.
	c.Keys("edit-key", "e")
	c.RequireContains("the primary key column cannot be edited")
	c.Shot("pk-not-editable")

	// Fix a typo in Lin's e-mail: caret to the email cell of row 3.
	send(c, harness.KeyRight) // id -> email
	send(c, harness.KeyDown)  // row 2
	send(c, harness.KeyDown)  // row 3 (Lin)
	c.Keys("edit-cell", "e")
	c.RequireContains("Edit cell lin@example.com", `\N for NULL`)
	c.Shot("cell-editor")
	send(c, harness.KeyHome) // caret to start
	send(c, harness.KeyRight)
	send(c, harness.KeyRight)
	send(c, harness.KeyRight) // after "lin"
	c.Type("insert-middle", ".wei")
	c.Keys("commit-cell", harness.KeyEnter)
	c.RequireContains("1 pending change")
	c.RequireContains("3*") // the pending-row marker in the gutter
	c.Shot("pending-change")

	// Esc inside the editor abandons the edit: the pending change count and
	// the database stay exactly as they were.
	c.Keys("edit-again", "e")
	c.Type("typed-junk", "junk")
	c.Keys("cancel-edit", harness.KeyEsc)
	c.RequireContains("1 pending change")
	c.Shot("edit-cancelled")

	// Ctrl+S with the grid focused asks for confirmation and shows the exact
	// UPDATE with its bound values; nothing has reached the database yet.
	c.Keys("apply-changes", harness.KeyCtrlS)
	c.WaitFor("Confirm execution", 10*time.Second)
	c.RequireContains(
		"1 statement · 1 write · 0 DDL or unsafe",
		"This script can modify or drop data.",
		"table: main.users",
		`UPDATE "main"."users" SET "email" = ? WHERE "id" = ?`,
		"1 = 'lin.wei@example.com'",
		"2 = '3'",
	)
	c.Shot("confirm-apply-dialog")
	if n, _ := dbCount(dbh, `SELECT COUNT(*) FROM users WHERE id = 3 AND email = 'lin@example.com'`); n != 1 {
		c.Fail("the database must be untouched before the dialog is accepted")
	}

	// Esc rejects: nothing is written and the pending change survives.
	c.Keys("reject-apply", harness.KeyEsc)
	c.WaitFor("Query cancelled", 10*time.Second)
	c.RequireContains("1 pending change")
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM users WHERE id = 3 AND email = 'lin@example.com'`); err != nil || n != 1 {
		c.Fail("Esc must not write; email check = %d, err %v", n, err)
	}
	c.Shot("apply-rejected")

	// Accept: the UPDATE runs, the grid refreshes and the database holds it.
	c.Keys("apply-again", harness.KeyCtrlS)
	c.WaitFor("Confirm execution", 10*time.Second)
	c.Keys("accept-apply", harness.KeyEnter)
	c.WaitFor("Applied 1 change", 15*time.Second)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM users WHERE id = 3 AND email = 'lin.wei@example.com'`, 1, 10*time.Second); err != nil || n != 1 {
		c.Fail("the applied email is not in the database: count=%d err=%v", n, err)
	}
	c.WaitFor("lin.wei@example.com", 10*time.Second)
	c.RequireNotContains("pending change(s)")
	c.Shot("change-applied")

	// \N edits a cell to SQL NULL. The grid was refreshed, so the caret is
	// back at the top left: go to Yuan's name (row 4, column 3).
	c.FocusResults()
	send(c, harness.KeyRight)
	send(c, harness.KeyRight) // -> name
	send(c, harness.KeyDown)
	send(c, harness.KeyDown)
	send(c, harness.KeyDown) // -> row 4 (Yuan)
	c.Keys("edit-to-null", "e")
	c.RequireContains("Edit cell Yuan Zhang")
	send(c, harness.KeyEnd)
	send(c, strings.Repeat(harness.KeyBackspace, 20)) // clear the seeded value
	c.Type("null-marker", `\N`)
	c.Keys("commit-null", harness.KeyEnter)
	c.RequireContains("1 pending change")
	c.Keys("apply-null", harness.KeyCtrlS)
	c.WaitFor("Confirm execution", 10*time.Second)
	c.RequireContains("1 = NULL", "2 = '4'")
	c.Shot("confirm-null-dialog")
	c.Keys("accept-null", harness.KeyEnter)
	c.WaitFor("Applied 1 change", 15*time.Second)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM users WHERE id = 4 AND name IS NULL`, 1, 10*time.Second); err != nil || n != 1 {
		c.Fail("\\N must write SQL NULL: count=%d err=%v", n, err)
	}
	c.WaitFor("NULL", 10*time.Second)
	c.Shot("null-applied")

	// Discard a pending change through the Query menu (Alt+Q, then D).
	c.FocusResults()
	send(c, strings.Repeat(harness.KeyRight, 4)) // -> city
	c.Keys("edit-city", "e")
	c.RequireContains("Edit cell London")
	send(c, harness.KeyEnd)
	send(c, strings.Repeat(harness.KeyBackspace, 10))
	c.Type("new-city", "Paris")
	c.Keys("commit-city", harness.KeyEnter)
	c.RequireContains("1 pending change")
	c.Menu('q', 'd') // Query -> Discard Changes
	c.RequireContains("Pending changes discarded")
	c.RequireNotContains("pending change(s)")
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM users WHERE id = 1 AND city = 'London'`); err != nil || n != 1 {
		c.Fail("discarding must not touch the database: count=%d err=%v", n, err)
	}
	c.Shot("changes-discarded")
}

// ---- 7. read-only mode ----

// TestGridReadOnly toggles read-only mode from the Query menu, checks that a
// write is blocked with a clear status message and that grid editing is
// refused for the same reason, then toggles back and lets the write through.
func TestGridReadOnly(t *testing.T) {
	c := startGridShop(t)
	dbPath := filepath.Join(c.ArtifactDir(), "shop.db")
	dbh := c.Env.OpenSQLite(t, dbPath)
	defer dbh.Close()

	gridRun(c, "SELECT * FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)

	c.Menu('q', 'o') // Query -> Read-only Mode
	c.RequireContains("Read-only mode on: write and DDL statements are blocked")
	c.Shot("readonly-on")

	// Reads still work.
	gridRun(c, "SELECT COUNT(*) AS user_count FROM users;")
	c.WaitFor("user_count", 20*time.Second)
	c.RequireContains("5")
	c.Shot("readonly-select")

	// Grid editing is refused because the client is read-only (a plain
	// single-table select is editable otherwise).
	gridRun(c, "SELECT * FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 20*time.Second)
	c.FocusResults()
	send(c, harness.KeyRight)
	c.Keys("try-edit-readonly", "e")
	c.RequireContains("This result cannot be edited: the client is in read-only mode")
	c.Shot("readonly-edit-blocked")

	// A write is blocked before it reaches the database.
	gridRun(c, "INSERT INTO users(id, email, name) VALUES (99, 'ro@example.com', 'RO');")
	c.WaitFor("Read-only mode: 1 write statement not executed", 20*time.Second)
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM users WHERE id = 99`); err != nil || n != 0 {
		c.Fail("read-only mode must not write: count=%d err=%v", n, err)
	}
	c.Shot("readonly-insert-blocked")

	// Toggle back: the same INSERT now runs and is visibly affected.
	c.Menu('q', 'o')
	c.RequireContains("Read-only mode off")
	gridRun(c, "INSERT INTO users(id, email, name) VALUES (99, 'ro@example.com', 'RO');")
	c.WaitFor("1 row affected", 20*time.Second)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM users WHERE id = 99`, 1, 10*time.Second); err != nil || n != 1 {
		c.Fail("after toggling read-only off the INSERT must land: count=%d err=%v", n, err)
	}
	c.Shot("readonly-off-insert-ran")
}

// ---- 8. risk confirmation dialogs ----

// TestGridRiskConfirmations checks the guard between the editor and
// destructive SQL: an UPDATE without WHERE and a DROP ask first, Esc rejects
// them (the database is unchanged), Enter accepts a controlled DELETE and a
// CREATE TABLE (both verified in the database).
func TestGridRiskConfirmations(t *testing.T) {
	c, dbPath := startGridDB(t, "risk.db",
		`CREATE TABLE t1 (id INTEGER PRIMARY KEY, v TEXT)`,
		`CREATE TABLE t2 (id INTEGER PRIMARY KEY, v TEXT)`,
		`INSERT INTO t1 VALUES (1, 'a'), (2, 'b')`,
		`INSERT INTO t2 VALUES (1, 'a'), (2, 'b')`,
	)
	dbh := c.Env.OpenSQLite(t, dbPath)
	defer dbh.Close()

	// UPDATE without WHERE: the dialog names the risk, Esc rejects it.
	gridRun(c, "UPDATE t1 SET v = 'x';")
	c.WaitFor("Confirm execution", 10*time.Second)
	c.RequireContains("1 statement · 1 write · 1 DDL or unsafe", "This script can modify or drop data.", "table: t1", "UPDATE t1 SET v = 'x'")
	c.Shot("update-without-where-dialog")
	c.Keys("reject-update", harness.KeyEsc)
	c.WaitFor("Query cancelled", 10*time.Second)
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM t1 WHERE v = 'x'`); err != nil || n != 0 {
		c.Fail("Esc must not run the unguarded UPDATE: count=%d err=%v", n, err)
	}
	c.Shot("update-rejected")

	// DELETE without WHERE: Enter accepts the controlled deletion.
	gridRun(c, "DELETE FROM t2;")
	c.WaitFor("Confirm execution", 10*time.Second)
	c.Shot("delete-without-where-dialog")
	c.Keys("accept-delete", harness.KeyEnter)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM t2`, 0, 10*time.Second); err != nil || n != 0 {
		c.Fail("the accepted DELETE must empty t2: count=%d err=%v", n, err)
	}
	c.Shot("delete-accepted")

	// DROP TABLE is DDL: Esc rejects, the table must still be there.
	gridRun(c, "DROP TABLE t1;")
	c.WaitFor("Confirm execution", 10*time.Second)
	c.RequireContains("DDL")
	c.Shot("drop-table-dialog")
	c.Keys("reject-drop", harness.KeyEsc)
	c.WaitFor("Query cancelled", 10*time.Second)
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM sqlite_master WHERE name = 't1'`); err != nil || n != 1 {
		c.Fail("Esc must not drop t1: count=%d err=%v", n, err)
	}

	// The second run accepts it.
	gridRun(c, "DROP TABLE t1;")
	c.WaitFor("Confirm execution", 10*time.Second)
	c.Keys("accept-drop", harness.KeyEnter)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM sqlite_master WHERE name = 't1'`, 0, 10*time.Second); err != nil || n != 0 {
		c.Fail("the accepted DROP must remove t1: count=%d err=%v", n, err)
	}
	c.Shot("drop-accepted")

	// CREATE TABLE is DDL too; Enter accepts a harmless one.
	gridRun(c, "CREATE TABLE t3 (a INTEGER, b TEXT);")
	c.WaitFor("Confirm execution", 10*time.Second)
	c.Keys("accept-create", harness.KeyEnter)
	if n, err := waitDB(t, dbh, `SELECT COUNT(*) FROM sqlite_master WHERE name = 't3'`, 1, 10*time.Second); err != nil || n != 1 {
		c.Fail("the accepted CREATE must create t3: count=%d err=%v", n, err)
	}
	c.Shot("create-accepted")
}

// ---- 9. errors and very long cells ----

// TestGridErrorsAndLongCell checks that syntax errors, a missing table, a
// constraint violation and a missing column all read as errors in the grid,
// that the app stays usable afterwards, and that a very long cell is truncated
// in place without breaking the row layout.
func TestGridErrorsAndLongCell(t *testing.T) {
	long := strings.Repeat("longvalue-", 30) + "ENDMARK"
	c, dbPath := startGridDB(t, "errors.db",
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE)`,
		`INSERT INTO users VALUES (1, 'a@x.com'), (2, 'b@x.com'), (3, 'c@x.com')`,
		`CREATE TABLE longvals (id INTEGER PRIMARY KEY, long_text TEXT, tail TEXT)`,
		fmt.Sprintf(`INSERT INTO longvals VALUES (1, '%s', 'tail-Z')`, long),
		`INSERT INTO longvals VALUES (2, 'short', 'tail-Y')`,
	)
	dbh := c.Env.OpenSQLite(t, dbPath)
	defer dbh.Close()

	// Syntax error.
	gridRun(c, "SELEKT * FROM users;")
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("syntax error")
	c.Shot("syntax-error")

	// Missing table.
	gridRun(c, "SELECT * FROM missing_table;")
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("no such table", "missing_table")
	c.Shot("missing-table")

	// Constraint violation: duplicate UNIQUE email.
	gridRun(c, "INSERT INTO users(id, email) VALUES (9, 'b@x.com');")
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("UNIQUE constraint failed")
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM users`); err != nil || n != 3 {
		c.Fail("the failed INSERT must not add a row: count=%d err=%v", n, err)
	}
	c.Shot("constraint-violation")

	// Missing column.
	gridRun(c, "SELECT nope FROM users;")
	c.WaitFor("Query failed", 20*time.Second)
	c.RequireContains("no such column")
	c.Shot("missing-column")

	// The app is still fully usable.
	gridRun(c, "SELECT 42 AS answer;")
	c.WaitFor("answer", 20*time.Second)
	c.RequireContains("42")
	c.Shot("usable-after-errors")

	// A 300-character value is clipped to the column width (40 by default),
	// and the following column stays on the same line, so nothing wraps.
	gridRun(c, "SELECT id, long_text, tail FROM longvals ORDER BY id;")
	c.WaitFor("tail-Z", 20*time.Second)
	screen := c.Screen()
	var row string
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "tail-Z") {
			row = line
			break
		}
	}
	if row == "" {
		c.Fail("the row with tail-Z is not on screen\n%s", screen)
	}
	if !strings.Contains(row, "longvalue-longvalue-longvalue") {
		c.Fail("the long cell must show its truncated beginning on the same row: %q", row)
	}
	if strings.Contains(screen, "ENDMARK") {
		c.Fail("the end of a 300-character cell must be clipped\n%s", screen)
	}
	c.Shot("long-cell-truncated")
}

// ---- 10. large result ----

// TestGridLargeResult runs SELECT over a generated 50k-row table. The SQLite
// driver caps a result at 1000 rows, so the grid must say it is truncated, the
// query must finish promptly, and PgDn/End must scroll the grid to the last
// (1000th) row.
func TestGridLargeResult(t *testing.T) {
	c, dbPath := startGridDB(t, "big.db",
		`CREATE TABLE big (id INTEGER PRIMARY KEY, val TEXT)`,
		`WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 50000)
		 INSERT INTO big(id, val) SELECT x, 'val-' || x FROM c`,
	)
	dbh := c.Env.OpenSQLite(t, dbPath)
	defer dbh.Close()
	if n, err := dbCount(dbh, `SELECT COUNT(*) FROM big`); err != nil || n != 50000 {
		c.Fail("fixture must hold 50000 rows: count=%d err=%v", n, err)
	}

	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("large-sql", "SELECT id, val FROM big ORDER BY id;")
	started := time.Now()
	c.Keys("run-large", harness.KeyF5)
	c.WaitFor("1000 rows", 30*time.Second)
	elapsed := time.Since(started)
	c.RequireContains("showing the first 1000 rows")
	if elapsed > 20*time.Second {
		c.Fail("the 50k-row result took %s; the grid must stay responsive", elapsed)
	}
	c.Logf("50k-row SELECT rendered in %s (driver cap 1000 rows)", elapsed)
	c.Shot("large-result")

	// Scroll to the end: the caret lands on the last capped row.
	c.FocusResults()
	send(c, harness.KeyPgDn)
	send(c, harness.KeyPgDn)
	c.Keys("end", harness.KeyEnd)
	c.WaitFor("val-1000", 10*time.Second)
	c.RequireContains("1000") // the gutter row number
	c.Shot("large-result-end")

	// ...and Home brings the first rows back.
	c.Keys("home", harness.KeyHome)
	c.WaitFor("val-1 ", 10*time.Second)
	c.Shot("large-result-home")
}

// ---- defect documentation ----

// TestGridEditPendingValueShown: after a cell edit is committed with Enter the
// grid draws the value the user typed (the database still holds the old one),
// together with the pending marker and the count in the summary.
func TestGridEditPendingValueShown(t *testing.T) {
	c := startGridShop(t)
	gridRun(c, "SELECT * FROM users ORDER BY id;")
	c.WaitFor("Ada Lovelace", 25*time.Second)
	c.FocusResults()
	send(c, harness.KeyRight) // id -> email
	send(c, harness.KeyRight) // email -> name (row 1)
	c.Keys("edit-name", "e")
	c.RequireContains("Edit cell Ada Lovelace")
	send(c, harness.KeyEnd)
	send(c, strings.Repeat(harness.KeyBackspace, 20))
	c.Type("new-name", "Zed")
	c.Keys("commit-name", harness.KeyEnter)
	c.RequireContains("1 pending change")
	shot := c.Shot("pending-edit-committed")

	if !strings.Contains(c.Screen(), "Zed") {
		c.Fail("the grid does not draw the pending value after the edit (screenshot %s)", shot)
	}
	// The database still holds the old value: the grid shows the edit, not a
	// saved change.
	dbPath := filepath.Join(c.ArtifactDir(), "shop.db")
	if got := scenario.SQLiteCount(t, dbPath, "SELECT COUNT(*) FROM users WHERE name = 'Ada Lovelace' AND id = 1"); got != 1 {
		c.Fail("the database changed before the change was applied: Ada rows = %d", got)
	}
}

// TestGridExportKeyboardSubmit: the export dialog advertises "⏎ export" and
// Enter on the path field exports, so a keyboard user never needs the mouse.
func TestGridExportKeyboardSubmit(t *testing.T) {
	c := startGridShop(t)
	gridRun(c, "SELECT id, email FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)

	target := filepath.Join(c.ArtifactDir(), "keyboard-export.csv")
	_ = os.Remove(target) // a stale file would hide the bug

	c.Keys("open-export", harness.KeyF7)
	c.RequireContains("⏎ export")
	clearPathField(c)
	c.Type("export-path", target)
	c.Shot("export-before-enter")

	c.Keys("press-enter", harness.KeyEnter)
	c.Shot("export-after-enter")
	c.WaitGone("Export results", 10*time.Second)

	data, err := os.ReadFile(target)
	if err != nil {
		c.Fail("Enter did not submit the export dialog: %v", err)
	}
	if !strings.Contains(string(data), "ada@example.com") {
		c.Fail("the exported file holds %q", data)
	}
}
