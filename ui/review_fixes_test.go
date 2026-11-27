package ui

// review_fixes_test.go pins the release-blocking fixes of the pre-release UI
// review (see .artifacts/review/ui.md), one test per finding:
//
//   - the grid-apply confirmation is bound to the connection and the batch it
//     was built for, so a connection switch cannot write it elsewhere;
//   - a background query outcome no longer takes the transaction and
//     row-count safety checks of an open apply confirmation away;
//   - closing a tab, closing tabs in bulk and quitting ask before dropping
//     pending grid changes (tab close and quit paths tested here);
//   - a late outcome of a replaced query batch is ignored;
//   - a tab switch resets the pointer drags that live in the model;
//   - an UPDATE that writes the value the grid already holds is not treated
//     as a failure when the driver reports 0 affected rows.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
)

// recConn wraps a driver.Connection and records the statements and the
// transaction calls the UI made through it, so a test can prove what reached
// the database and what did not.
type recConn struct {
	driver.Connection

	mu        sync.Mutex
	queries   []string
	begins    int
	commits   int
	rollbacks int
}

// Query runs the statement on the wrapped connection, recording it first.
func (c *recConn) Query(ctx context.Context, query string, args ...any) (*driver.Result, error) {
	c.mu.Lock()
	c.queries = append(c.queries, strings.TrimSpace(query))
	c.mu.Unlock()
	return c.Connection.Query(ctx, query, args...)
}

// Begin opens a transaction on the wrapped connection.
func (c *recConn) Begin(ctx context.Context) error {
	c.mu.Lock()
	c.begins++
	c.mu.Unlock()
	tm, ok := c.Connection.(driver.TransactionManager)
	if !ok {
		return errors.New("the wrapped connection has no transactions")
	}
	return tm.Begin(ctx)
}

// Commit commits the transaction of the wrapped connection.
func (c *recConn) Commit(ctx context.Context) error {
	c.mu.Lock()
	c.commits++
	c.mu.Unlock()
	tm, ok := c.Connection.(driver.TransactionManager)
	if !ok {
		return errors.New("the wrapped connection has no transactions")
	}
	return tm.Commit(ctx)
}

// Rollback rolls the transaction of the wrapped connection back.
func (c *recConn) Rollback(ctx context.Context) error {
	c.mu.Lock()
	c.rollbacks++
	c.mu.Unlock()
	tm, ok := c.Connection.(driver.TransactionManager)
	if !ok {
		return errors.New("the wrapped connection has no transactions")
	}
	return tm.Rollback(ctx)
}

// InTx reports whether the wrapped connection has uncommitted work.
func (c *recConn) InTx() bool {
	tm, ok := c.Connection.(driver.TransactionManager)
	return ok && tm.InTx()
}

// updateCount is how many UPDATE statements reached the connection.
func (c *recConn) updateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, q := range c.queries {
		if strings.HasPrefix(strings.ToUpper(q), "UPDATE") {
			n++
		}
	}
	return n
}

// txCounts returns how often Begin, Commit and Rollback were called.
func (c *recConn) txCounts() (begins, commits, rollbacks int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.begins, c.commits, c.rollbacks
}

// pendingGridEditModel is a model on a recording connection with one pending
// cell edit in the grid of the first tab.
func pendingGridEditModel(t *testing.T) (*recConn, Model) {
	t.Helper()
	rec := &recConn{Connection: e2eConn(t)}
	m := gridEditModel(t, rec, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column
	m = editCell(t, m, "hacked@example.com")
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1", got)
	}
	return rec, m
}

// openApplyDialog opens the apply confirmation for the pending edit.
func openApplyDialog(t *testing.T, m Model) Model {
	t.Helper()
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatalf("applying the changes must ask for confirmation: %q", m.status.message)
	}
	if m.confirm.apply == nil {
		t.Fatal("the confirmation must carry the pending grid batch")
	}
	return m
}

// ---- 1. BLOCKER: the apply confirmation is bound to its connection ----

// TestApplyConfirmIsDroppedWhenAnotherConnectionBecomesLive reproduces review
// scenario A: the dialog is open, a connection opened earlier finishes and
// becomes live, and the pending UPDATEs of the old database must never run on
// the new one. The dialog is closed, the batch dropped, and the user told.
func TestApplyConfirmIsDroppedWhenAnotherConnectionBecomesLive(t *testing.T) {
	one, m := pendingGridEditModel(t)
	m = openApplyDialog(t, m)

	// The connection of the object browser finishes opening while the dialog
	// is up: applyConnection makes two live and clears the grid edits.
	two := &recConn{Connection: e2eConn(t)}
	twoCat, err := two.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	m = send(m, connMsg{
		cfg:  settings.Connection{Name: "other.db", Driver: "sqlite"},
		conn: two,
		cat:  twoCat,
	})

	if m.confirm.open {
		t.Fatal("the dialog must be closed when the connection changes")
	}
	if !strings.Contains(m.status.message, "another database connection") {
		t.Fatalf("status = %q, want the user to be told why the batch was dropped", m.status.message)
	}
	if got := two.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the new connection = %d, want 0", got)
	}
	if got := one.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the old connection = %d, want 0", got)
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0: the batch belongs to the dead connection", got)
	}

	// A stray Enter afterwards must not run the frozen batch either.
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := two.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the new connection after Enter = %d, want 0", got)
	}
	if got := one.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the old connection after Enter = %d, want 0", got)
	}
}

// TestApplyConfirmRefusesAConnectionChangedAtAccept is the second line of
// defence: even when a code path replaced the live connection without closing
// the dialog, accepting it must re-check the binding and refuse.
func TestApplyConfirmRefusesAConnectionChangedAtAccept(t *testing.T) {
	one, m := pendingGridEditModel(t)
	m = openApplyDialog(t, m)

	other := &recConn{Connection: e2eConn(t)}
	m.connOpen = other
	m.conn = settings.Connection{Name: "other.db", Driver: "sqlite"}

	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.confirm.open {
		t.Fatal("the dialog must be closed")
	}
	if !strings.Contains(m.status.message, "another database connection") {
		t.Fatalf("status = %q, want the binding failure to be reported", m.status.message)
	}
	if got := other.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the new connection = %d, want 0", got)
	}
	if got := one.updateCount(); got != 0 {
		t.Fatalf("UPDATEs on the old connection = %d, want 0", got)
	}
}

// ---- 2. BLOCKER: a background outcome cannot disable the safety checks ----

// TestApplyDialogKeepsItsTransactionWhenABackgroundResultArrives reproduces
// review scenario B: a statement that was running when the dialog opened
// finishes while it is up. Its result replaces the grid (and clears the
// pending changes), but the confirmed batch must still run inside its own
// transaction: Begin and Commit are invoked.
func TestApplyDialogKeepsItsTransactionWhenABackgroundResultArrives(t *testing.T) {
	rec, m := pendingGridEditModel(t)
	m = openApplyDialog(t, m)

	// A background F5 finishes: its queryMsg resets the result state.
	const selectSQL = "SELECT id, email FROM users ORDER BY id"
	bgCmd := m.executeStatements(selectSQL, execStatements(sql.Statements(selectSQL, m.cfg.Dialect)), nil)
	bg, ok := bgCmd().(queryMsg)
	if !ok {
		t.Fatalf("background command returned %T, want queryMsg", bgCmd())
	}
	m = send(m, bg)
	if !m.confirm.open {
		t.Fatal("the apply confirmation must survive a background result")
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0: the background result replaced the grid", got)
	}

	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		t.Fatal("confirming must run the batch")
	}
	qm, ok := cmd().(queryMsg)
	if !ok {
		t.Fatal("the apply command must return a queryMsg")
	}
	var after tea.Cmd
	m, after = sendCmd(next, qm)
	// The grid the batch belonged to is refreshed after a success; the notice
	// rides on the refresh outcome, so hand it to the model like Bubble Tea.
	for _, msg := range collectCmds(after) {
		m = send(m, msg)
	}

	begins, commits, rollbacks := rec.txCounts()
	if begins != 1 {
		t.Fatalf("Begin() calls = %d, want 1: the confirmed batch must stay transactional", begins)
	}
	if commits != 1 {
		t.Fatalf("Commit() calls = %d, want 1", commits)
	}
	if rollbacks != 0 {
		t.Fatalf("Rollback() calls = %d, want 0", rollbacks)
	}
	if got := userEmail(t, rec, 1); got != "hacked@example.com" {
		t.Fatalf("the database holds %q, want the confirmed value", got)
	}
	if !strings.Contains(m.status.message, "Applied 1 change") {
		t.Fatalf("status = %q, want the applied notice", m.status.message)
	}
}

// TestApplyDialogStillChecksRowCountsWhenABackgroundResultArrives makes sure
// the transaction is used for its purpose: a row that disappeared still makes
// the batch fail and roll back, even though the grid that held the pending
// changes was replaced while the dialog was open.
func TestApplyDialogStillChecksRowCountsWhenABackgroundResultArrives(t *testing.T) {
	rec, m := pendingGridEditModel(t)
	m = openApplyDialog(t, m)

	const selectSQL = "SELECT id, email FROM users ORDER BY id"
	bgCmd := m.executeStatements(selectSQL, execStatements(sql.Statements(selectSQL, m.cfg.Dialect)), nil)
	bg, ok := bgCmd().(queryMsg)
	if !ok {
		t.Fatalf("background command returned %T, want queryMsg", bgCmd())
	}
	m = send(m, bg)

	// The row is gone before the batch runs.
	if _, err := rec.Query(context.Background(), "DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		t.Fatal("confirming must run the batch")
	}
	qm, ok := cmd().(queryMsg)
	if !ok {
		t.Fatal("the apply command must return a queryMsg")
	}
	m = send(next, qm)

	begins, commits, rollbacks := rec.txCounts()
	if begins != 1 {
		t.Fatalf("Begin() calls = %d, want 1", begins)
	}
	if commits != 0 {
		t.Fatalf("Commit() calls = %d, want 0: the batch must not be committed", commits)
	}
	if rollbacks != 1 {
		t.Fatalf("Rollback() calls = %d, want 1", rollbacks)
	}
	if !strings.Contains(m.status.message, "did not affect any row") {
		t.Fatalf("status = %q, want the row-count message", m.status.message)
	}
}

// ---- 3. HIGH: closing and quitting ask before dropping pending edits ----

// TestCloseTabAsksWhenPendingChangesWouldBeLost pins the close-tab guard: the
// question names the pending work, Esc returns to the same state, and only
// confirming closes the tab.
func TestCloseTabAsksWhenPendingChangesWouldBeLost(t *testing.T) {
	_, m := pendingGridEditModel(t)
	// A second tab: closing the edited one is a real loss and the workspace
	// keeps a tab to fall back on.
	m.newTab()
	m.switchTab(0)
	tabs := len(m.tabs)

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if !m.confirm.open {
		t.Fatal("closing a tab with pending changes must ask first")
	}
	if m.confirm.kind != confirmLoss || m.confirm.then != confirmLossCloseTab {
		t.Fatalf("dialog kind = %v/%v, want a close-tab loss question", m.confirm.kind, m.confirm.then)
	}
	if got := len(m.tabs); got != tabs {
		t.Fatalf("tabs = %d, want %d: nothing may close before the answer", got, tabs)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "pending change") {
		t.Fatalf("the question does not name the pending changes:\n%s", view)
	}

	// Cancel returns to the same state: same tabs, same pending edit.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm.open {
		t.Fatal("Esc must close the question")
	}
	if got := len(m.tabs); got != tabs {
		t.Fatalf("tabs = %d, want %d after cancel", got, tabs)
	}
	if got := m.tabs[0].result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1 after cancel", got)
	}

	// Confirming closes the tab.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if !m.confirm.open {
		t.Fatal("the guard must ask again")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(m.tabs); got != tabs-1 {
		t.Fatalf("tabs = %d, want %d after confirming", got, tabs-1)
	}
}

// TestCloseOtherTabsAsksWhenPendingChangesWouldBeLost covers the bulk close
// path: a pending edit in a tab that would be closed is protected too.
func TestCloseOtherTabsAsksWhenPendingChangesWouldBeLost(t *testing.T) {
	_, m := pendingGridEditModel(t)
	m.newTab() // tab 1 is active, tab 0 holds the pending change

	if m.closeTabsExcept(1) {
		t.Fatal("closing the other tabs must ask first")
	}
	if !m.confirm.open || m.confirm.then != confirmLossCloseOthers {
		t.Fatalf("dialog = %+v, want a close-others loss question", m.confirm)
	}
	if got := len(m.tabs); got != 2 {
		t.Fatalf("tabs = %d, want 2 before the answer", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := len(m.tabs); got != 2 || m.tabs[0].result.pendingCount() != 1 {
		t.Fatalf("cancel changed the state: %d tabs, %d pending",
			len(m.tabs), m.tabs[0].result.pendingCount())
	}
}

// TestQuitAsksWhenPendingChangesWouldBeLost pins the quit guard: Ctrl+Q asks,
// Esc keeps the session alive, and the confirmed quit reports tea.Quit.
func TestQuitAsksWhenPendingChangesWouldBeLost(t *testing.T) {
	_, m := pendingGridEditModel(t)

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !m.confirm.open {
		t.Fatal("quitting with pending changes must ask first")
	}
	if m.quitting {
		t.Fatal("nothing may quit before the answer")
	}
	if m.confirm.then != confirmLossQuit {
		t.Fatalf("dialog then = %v, want quit", m.confirm.then)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quitting {
		t.Fatal("Esc must keep the session alive")
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1 after cancel", got)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !m.confirm.open {
		t.Fatal("the guard must ask again")
	}
	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !next.quitting {
		t.Fatal("confirming must quit the program")
	}
	if cmd == nil {
		t.Fatal("the quit must produce a command")
	}
	outcome := cmd()
	if _, ok := outcome.(connCloseMsg); ok {
		updated, quitCmd := next.Update(outcome)
		next = updated.(Model)
		cmd = quitCmd
	}
	if cmd == nil {
		t.Fatal("the completed shutdown must produce a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("the quit command must be tea.Quit")
	}
}

// TestSwitchingTheConnectionAsksWhenPendingChangesWouldBeLost covers the
// connection-switch guard: the pending changes of the current database are not
// dropped before the user agrees.
func TestSwitchingTheConnectionAsksWhenPendingChangesWouldBeLost(t *testing.T) {
	_, m := pendingGridEditModel(t)

	cmd := m.connectTo(settings.Connection{Name: "other.db", Driver: "sqlite"})
	if cmd != nil {
		t.Fatal("the switch must ask first, not connect")
	}
	if !m.confirm.open || m.confirm.then != confirmLossConnect {
		t.Fatalf("dialog = %+v, want a connect loss question", m.confirm)
	}
	if got := m.connRef().name; got != "shop.db" {
		t.Fatalf("live connection = %q, want it unchanged before the answer", got)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := m.connRef().name; got != "shop.db" {
		t.Fatalf("live connection = %q, want it unchanged after cancel", got)
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1 after cancel", got)
	}
}

// ---- 4. MEDIUM: stale query outcomes are ignored ----

// TestStaleQueryOutcomeDoesNotClobberTheNewerBatch pins the query generation:
// the late outcome of a batch that a newer one replaced must not clear the
// newer batch's running flag or file its rows into the tab.
func TestStaleQueryOutcomeDoesNotClobberTheNewerBatch(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1")

	const first = "SELECT 1"
	cmdA := m.executeStatements(first, execStatements(sql.Statements(first, m.cfg.Dialect)), nil)
	genA := m.queryGen
	msgA := cmdA()

	// A second batch starts and replaces the first one before its outcome is
	// delivered.
	const second = "SELECT 2 AS two"
	cmdB := m.executeStatements(second, execStatements(sql.Statements(second, m.cfg.Dialect)), nil)
	if m.queryGen == genA {
		t.Fatal("starting a batch must give it a new generation")
	}
	msgB := cmdB()

	// The first batch's outcome arrives late.
	m = send(m, msgA)
	if !m.queryRunning {
		t.Fatal("the newer batch must still be running")
	}
	if m.queryCancel == nil {
		t.Fatal("the newer batch must keep its cancel function")
	}
	if got := m.session.result.stmt; got != "" {
		t.Fatalf("grid statement = %q, want it untouched by the stale outcome", got)
	}

	// The current batch's outcome is processed normally.
	m = send(m, msgB)
	if m.queryRunning {
		t.Fatal("the current outcome must clear the running flag")
	}
	if got := m.session.result.stmt; got != second {
		t.Fatalf("grid statement = %q, want the current batch's statement", got)
	}
}

// ---- 5. MEDIUM: a tab switch resets the pointer drags ----

// TestTabSwitchResetsPointerDrags pins that the drag state kept in the model
// (the horizontal result scrollbar, the divider drag, the editor drag
// selection and the multi-click counter) is reset like the other gestures:
// none of it may survive into another tab's grid.
func TestTabSwitchResetsPointerDrags(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 30})
	m.newTab()

	m.hScrollDrag = hScrollbarDrag{active: true, startX: 40, startOffset: 3}
	m.layout.dragging = dividerHorizontal
	m.layout.dragMoved = true
	m.dragging = true
	m.clicks = 2

	m.switchTab(0)

	if m.hScrollDrag.active {
		t.Fatal("the horizontal scrollbar drag must not survive a tab switch")
	}
	if m.layout.dragging != dividerNone || m.layout.dragMoved {
		t.Fatalf("divider drag = %v/%v, want none/false", m.layout.dragging, m.layout.dragMoved)
	}
	if m.dragging {
		t.Fatal("the editor drag selection must not survive a tab switch")
	}
	if m.clicks != 0 {
		t.Fatalf("clicks = %d, want 0", m.clicks)
	}
}

// ---- 6. MEDIUM: an unchanged value may report 0 affected rows ----

// TestGridApplyAcceptsZeroAffectedForAnUnchangedValue covers the MySQL
// clientFoundRows side of the apply: an UPDATE that writes the value the grid
// already holds matches the row but reports 0 affected rows (MySQL counts
// changed rows only). That is not a failure; a changed value still is.
func TestGridApplyAcceptsZeroAffectedForAnUnchangedValue(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	// Type the stored value back: the grid already holds it.
	m = editCell(t, m, "ada@example.com")

	stmts, unchanged, err := m.pendingBatch()
	if err != nil {
		t.Fatalf("pendingBatch: %v", err)
	}
	if len(stmts) != 1 || len(unchanged) != 1 {
		t.Fatalf("statements/unchanged = %d/%d, want 1/1", len(stmts), len(unchanged))
	}
	if !unchanged[0] {
		t.Fatal("writing the grid's own value must be marked unchanged")
	}
	apply := &gridApply{unchanged: unchanged}
	if !apply.affectedOK(0, 0) {
		t.Fatal("0 affected rows for an unchanged value must be accepted")
	}
	if apply.affectedOK(0, 2) {
		t.Fatal("2 affected rows must stay a hard failure")
	}

	// A value that really changes keeps the strict check.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = clearCellEdit(t, m)
	m = typeText(m, "changed@example.com")
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, unchanged, err = m.pendingBatch()
	if err != nil {
		t.Fatalf("pendingBatch: %v", err)
	}
	if len(unchanged) != 1 || unchanged[0] {
		t.Fatalf("unchanged = %v, want the changed value flagged as changed", unchanged)
	}
	apply = &gridApply{unchanged: unchanged}
	if apply.affectedOK(0, 0) {
		t.Fatal("0 affected rows for a changed value must stay a failure")
	}
}

// TestApplyFailureInAnOpenTransactionSaysSo pins the wording of a failed batch
// in manual-commit mode: earlier statements of the batch are already pending
// in the user's transaction, and the message must not read like "nothing was
// applied".
func TestApplyFailureInAnOpenTransactionSaysSo(t *testing.T) {
	m := New(Config{Title: "TDB", Translator: i18n.New(i18n.En)})
	text := m.applyFailureText(queryMsg{err: errors.New("boom"), txOpen: true})
	if !strings.Contains(text, "transaction") || !strings.Contains(text, "roll back") {
		t.Fatalf("message = %q, want it to point at the open transaction", text)
	}
	// A rolled-back batch keeps its own, stronger wording.
	rolled := m.applyFailureText(queryMsg{err: errors.New("boom"), txOpen: true, rolledBack: true})
	if !strings.Contains(rolled, "rolled back") {
		t.Fatalf("message = %q, want the rollback wording", rolled)
	}
}

// TestGridApplyIsNotRecordedInTheHistory pins the low finding that history
// must not carry non-runnable apply batches: the generated UPDATE text has
// unbound placeholders ($1/$2) and could not be re-run from the list.
func TestGridApplyIsNotRecordedInTheHistory(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")
	m = openApplyDialog(t, m)
	// The SELECT that produced the grid is in the history already; only the
	// apply is under test.
	m.historyEntries = nil
	m = confirmAndApply(m)

	if got := userEmail(t, conn, 1); got != "new@example.com" {
		t.Fatalf("the database holds %q, want the applied value", got)
	}
	if len(m.historyEntries) != 0 {
		t.Fatalf("history = %+v, want no entry for a grid apply", m.historyEntries)
	}
}
