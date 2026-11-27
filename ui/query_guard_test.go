package ui

// query_guard_test.go pins the execution safety guard: read-only mode blocks
// write and DDL statements before they reach the database, and destructive
// statements ask for confirmation first (Esc cancels, Enter runs).

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
)

// userCount reads the real row count from the database, so the tests verify
// what actually happened, not what the UI believes happened.
func userCount(t *testing.T, conn driver.Connection) int {
	t.Helper()
	res, err := conn.Query(context.Background(), "SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if res == nil || len(res.Rows) != 1 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
		t.Fatalf("unexpected count result: %+v", res)
	}
	n, err := strconv.Atoi(*res.Rows[0][0])
	if err != nil {
		t.Fatalf("count value %q: %v", *res.Rows[0][0], err)
	}
	return n
}

func TestReadOnlyModeBlocksWrites(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "UPDATE users SET age = 99")
	m.settings.ReadOnly = true

	// send (not runToCompletion) keeps the status message: draining the
	// command queue would also run the message-expiry timer.
	m = send(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.confirm.open {
		t.Fatal("read-only mode must block outright, not ask")
	}
	if !strings.Contains(m.status.message, "Read-only mode") {
		t.Fatalf("status = %q, want the read-only message", m.status.message)
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3: the UPDATE must not have run", got)
	}
	if m.session.result.res != nil {
		t.Fatal("a blocked statement must not produce a result")
	}
}

func TestReadOnlyModeBlocksDDL(t *testing.T) {
	conn := e2eConn(t)
	// A script whose first statement is harmless: the whole batch is blocked.
	m := e2eModel(t, conn, "SELECT 1;\nDROP TABLE users")
	m.settings.ReadOnly = true
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.confirm.open {
		t.Fatal("read-only mode must block DDL without asking")
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3: the DROP must not have run", got)
	}
}

func TestReadOnlyModeAllowsSelects(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users ORDER BY id")
	m.settings.ReadOnly = true

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err != nil {
		t.Fatalf("a read must run in read-only mode: %v", m.session.result.err)
	}
	if got := len(m.session.result.res.Rows); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
}

func TestDangerousStatementAsksAndEscCancels(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "DELETE FROM users")

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if !m.confirm.open {
		t.Fatal("DELETE without WHERE must ask for confirmation")
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3 while the dialog is open", got)
	}

	m = keyByName(m, "esc")
	if m.confirm.open {
		t.Fatal("Esc must close the dialog")
	}
	if !strings.Contains(m.status.message, "cancelled") {
		t.Fatalf("status = %q, want the cancellation message", m.status.message)
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3 after cancelling", got)
	}
}

func TestDangerousStatementEnterRuns(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "DELETE FROM users")

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if !m.confirm.open {
		t.Fatal("DELETE without WHERE must ask for confirmation")
	}
	// runToCompletion (not keyByName): confirming returns the execution
	// command, which the Bubble Tea loop would drain.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.confirm.open {
		t.Fatal("Enter must close the dialog and run the statement")
	}
	if m.session.result.err != nil {
		t.Fatalf("the confirmed statement must run: %v", m.session.result.err)
	}
	if got := userCount(t, conn); got != 0 {
		t.Fatalf("users = %d, want 0 after the confirmed DELETE", got)
	}
}

func TestDDLAsksForConfirmation(t *testing.T) {
	for _, text := range []string{
		"DROP TABLE users",
		"TRUNCATE TABLE users",
		"CREATE TABLE backup AS SELECT * FROM users",
	} {
		conn := e2eConn(t)
		m := e2eModel(t, conn, text)
		m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
		if !m.confirm.open {
			t.Fatalf("%q must ask for confirmation", text)
		}
		m = keyByName(m, "esc")
		if got := userCount(t, conn); got != 3 {
			t.Fatalf("%q ran despite Esc: users = %d", text, got)
		}
	}
}

func TestPlainInsertRunsWithoutPrompt(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "INSERT INTO users VALUES (4, 'x@example.com', 'X', 20)")

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.confirm.open {
		t.Fatal("a scoped INSERT must not interrupt the user")
	}
	if m.session.result.err != nil {
		t.Fatalf("insert failed: %v", m.session.result.err)
	}
	if got := userCount(t, conn); got != 4 {
		t.Fatalf("users = %d, want 4", got)
	}
}

func TestScopedUpdateAndDeleteRunWithoutPrompt(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "UPDATE users SET age = 99 WHERE id = 1;\nDELETE FROM users WHERE id = 2")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.confirm.open {
		t.Fatal("writes with WHERE must not ask for confirmation")
	}
	if m.session.result.err != nil {
		t.Fatalf("script failed: %v", m.session.result.err)
	}
	if got := userCount(t, conn); got != 2 {
		t.Fatalf("users = %d, want 2", got)
	}
}

func TestConfirmDialogPreviewsAndIsModal(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1;\nDELETE FROM users")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if !m.confirm.open {
		t.Fatal("the script holds a destructive statement and must ask")
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		"Confirm execution", "2 statement(s)", "DELETE FROM users",
		"Run anyway", "Cancel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the dialog is missing %q:\n%s", want, view)
		}
	}

	// The dialog is modal: typing must not reach the editor behind it.
	before := m.buf.Text()
	m = typeText(m, "x")
	if m.buf.Text() != before {
		t.Fatalf("typing leaked into the editor: %q", m.buf.Text())
	}

	// A click outside the box must never confirm a destructive statement.
	l := m.confirmLayout()
	m = send(m, tea.MouseMsg{X: l.x + l.w + 5, Y: l.y + l.h + 5,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !m.confirm.open {
		t.Fatal("clicking outside the dialog must not confirm")
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3", got)
	}
}

func TestConfirmDialogButtons(t *testing.T) {
	// The cancel button cancels…
	conn := e2eConn(t)
	m := e2eModel(t, conn, "DELETE FROM users")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	l := m.confirmLayout()
	m = runToCompletion(m, tea.MouseMsg{X: l.cancel.X + 1, Y: l.cancel.Y,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.confirm.open {
		t.Fatal("the cancel button must close the dialog")
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3 after cancelling", got)
	}

	// …and the run button runs.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	l = m.confirmLayout()
	m = runToCompletion(m, tea.MouseMsg{X: l.run.X + 1, Y: l.run.Y,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.confirm.open {
		t.Fatal("the run button must close the dialog")
	}
	if m.session.result.err != nil {
		t.Fatalf("the confirmed statement must run: %v", m.session.result.err)
	}
	if got := userCount(t, conn); got != 0 {
		t.Fatalf("users = %d, want 0 after clicking run", got)
	}
}

func TestExplainOfDestructiveStatementDoesNotAsk(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "DELETE FROM users")

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF6})
	if m.confirm.open {
		t.Fatal("plain EXPLAIN is read-only and must not ask")
	}
	if m.session.result.err != nil {
		t.Fatalf("explain failed: %v", m.session.result.err)
	}
	if len(m.session.result.res.Columns) == 0 {
		t.Fatal("EXPLAIN must produce a row set")
	}
	if got := userCount(t, conn); got != 3 {
		t.Fatalf("users = %d, want 3: EXPLAIN must not delete", got)
	}
}

func TestReadOnlyToggleViaQueryMenuPersists(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := newTestModel("SELECT 1")
	m.settingsPath = path
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Alt+Q opens the Query menu; "o" activates the read-only item. send
	// keeps the status message (runToCompletion would drain the expiry
	// timer); persistence is exercised through the same command the menu
	// batches, like the settings-dialog tests do.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true})
	if m.menu.open != 4 {
		t.Fatalf("menu.open = %d, want the Query menu (4)", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if !m.settings.ReadOnly {
		t.Fatal("the menu item must switch read-only mode on")
	}
	if !strings.Contains(m.status.message, "Read-only mode on") {
		t.Fatalf("status = %q, want the toggle message", m.status.message)
	}
	if cmd := m.persistSettings(); cmd != nil {
		cmd()
	}
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.ReadOnly {
		t.Fatal("read-only mode must be persisted")
	}

	// The same item switches it back off.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if m.settings.ReadOnly {
		t.Fatal("the menu item must switch read-only mode off again")
	}

	// Drain the writer before TempDir cleanup: the toggle above enqueued a
	// snapshot whose command send discarded, so the background write could
	// still be running when the test directory is removed.
	if cmd := m.persistSettings(); cmd != nil {
		cmd()
	}
}

func TestReadOnlyToggleViaSettingsDialog(t *testing.T) {
	m := openSettingsDialog(t)
	m = selectSettingRow(t, m, i18n.KeySettingsReadOnly)
	m = keyByName(m, "space")
	if !m.settings.ReadOnly {
		t.Fatal("the settings row must switch read-only mode on")
	}
	m = keyByName(m, "space")
	if m.settings.ReadOnly {
		t.Fatal("the settings row must switch read-only mode off")
	}
}

// ---- Background execution bookkeeping ----

// TestQueryOutcomeFilesUnderTheConnectionThatStartedIt pins the fix for the
// data race in executeStatements: the connection name was read from m.conn
// inside the command closure, so a connection switch between dispatch and
// execution both raced with the UI goroutine and renamed the outcome.
func TestQueryOutcomeFilesUnderTheConnectionThatStartedIt(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1")
	cmd := m.executeStatements("SELECT 1", execStatements(sql.Statements("SELECT 1", m.cfg.Dialect)), nil)
	if cmd == nil {
		t.Fatal("executeStatements must return a command")
	}

	// The UI goroutine switches connections while the batch is in flight;
	// applyConnection writes m.conn exactly like this.
	m.conn = settings.Connection{Name: "other.db"}

	msg := cmd()
	q, ok := msg.(queryMsg)
	if !ok {
		t.Fatalf("command returned %T, want queryMsg", msg)
	}
	if q.conn != "shop.db" {
		t.Fatalf("outcome filed under %q, want the connection that started it", q.conn)
	}
}

// TestExecuteStatementsDoesNotRaceWithConnectionSwitch is the -race twin of
// the test above: the command runs on a background goroutine while another
// goroutine writes m.conn — the access pair the review flagged. It fails
// under `go test -race` unless the connection name is captured outside the
// closure.
func TestExecuteStatementsDoesNotRaceWithConnectionSwitch(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1")
	cmd := m.executeStatements("SELECT 1", execStatements(sql.Statements("SELECT 1", m.cfg.Dialect)), nil)
	if cmd == nil {
		t.Fatal("executeStatements must return a command")
	}

	var (
		wg  sync.WaitGroup
		msg tea.Msg
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		msg = cmd()
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			m.conn = settings.Connection{Name: "switching"}
		}
	}()
	wg.Wait()

	q, ok := msg.(queryMsg)
	if !ok {
		t.Fatalf("command returned %T, want queryMsg", msg)
	}
	if q.conn != "shop.db" {
		t.Fatalf("outcome filed under %q, want the connection that started it", q.conn)
	}
}

// ---- Confirm dialog geometry and modal order ----

// TestConfirmHitRectsStayInsideTheBoxOnNarrowScreens pins the fix for the
// review finding that the button hit rectangles kept their full width while
// the rendered row is truncated to innerW: a click outside the box could
// then reach a button, contradicting the dialog's promise that "a stray
// click can never confirm a DROP".
func TestConfirmHitRectsStayInsideTheBoxOnNarrowScreens(t *testing.T) {
	for _, width := range []int{6, 12, 20, 24, 26} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			conn := e2eConn(t)
			m := e2eModel(t, conn, "DELETE FROM users")
			m = send(m, tea.WindowSizeMsg{Width: width, Height: 24})
			m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
			if !m.confirm.open {
				t.Fatalf("width %d: DELETE must ask for confirmation", width)
			}

			l := m.confirmLayout()
			// The row the renderer draws: innerW columns between the
			// borders. Every width in this list truncates the natural row
			// (which is at least 1+runW+2+cancelW columns wide).
			rowX, rowEnd := l.x+1, l.x+1+l.innerW
			for _, tc := range []struct {
				name string
				r    Rect
			}{{"run", l.run}, {"cancel", l.cancel}} {
				if tc.r.X < rowX || tc.r.X+tc.r.W > rowEnd {
					t.Errorf("width %d: %s hit rect %+v leaves the drawn row [%d, %d)",
						width, tc.name, tc.r, rowX, rowEnd)
				}
			}

			// A click on the right border, where no button is drawn, must
			// neither confirm nor cancel the DROP.
			m = runToCompletion(m, tea.MouseMsg{X: l.x + l.w - 1, Y: l.run.Y,
				Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if !m.confirm.open {
				t.Fatalf("width %d: a click on the border reached a button", width)
			}
			if got := userCount(t, conn); got != 3 {
				t.Fatalf("width %d: users = %d, want 3", width, got)
			}
		})
	}
}

// TestConfirmTakesPriorityOverOverlappingDialogs pins the unified modal
// order: when several modal flags overlap, the keyboard and the mouse paths
// must dispatch to the same dialog — the confirmation, checked right after
// the settings dialog in editor_keys.go and mouse.go alike.
func TestConfirmTakesPriorityOverOverlappingDialogs(t *testing.T) {
	base := func(t *testing.T) Model {
		t.Helper()
		conn := e2eConn(t)
		m := e2eModel(t, conn, "DELETE FROM users")
		m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
		if !m.confirm.open {
			t.Fatal("DELETE must ask for confirmation")
		}
		// Overlap the states by hand: openConfirm closes these dialogs (and
		// now the history as well), so a stale dispatch is the only way to
		// reach a state where they are open together with the confirmation.
		m.connDlg.open = true
		m.exportDlg.open = true
		m.historyDlg.open = true
		return m
	}

	t.Run("keyboard", func(t *testing.T) {
		m := base(t)
		m = keyByName(m, "esc")
		if m.confirm.open {
			t.Fatal("Esc must reach the confirmation dialog, not an overlapping one")
		}
	})

	t.Run("mouse", func(t *testing.T) {
		m := base(t)
		l := m.confirmLayout()
		m = runToCompletion(m, tea.MouseMsg{X: l.cancel.X + 1, Y: l.cancel.Y,
			Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if m.confirm.open {
			t.Fatal("a click on the confirm dialog must reach it, not an overlapping dialog")
		}
	})
}

// TestOpenConfirmClosesTheHistoryDialog: the history list closes itself when
// opened elsewhere, but openConfirm must close it too so the two modal flags
// can never overlap behind each other's back.
func TestOpenConfirmClosesTheHistoryDialog(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "DELETE FROM users")
	m.historyDlg.open = true

	m.openConfirm("DELETE FROM users", execStatements(sql.Statements("DELETE FROM users", m.cfg.Dialect)),
		[]sql.RiskLevel{sql.RiskDangerousWrite})

	if m.historyDlg.open {
		t.Fatal("openConfirm must close the history dialog")
	}
	if !m.confirm.open {
		t.Fatal("openConfirm must open the confirmation dialog")
	}
}
