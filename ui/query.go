package ui

// query.go turns editor text into statements, executes them outside the UI
// goroutine, and files each outcome under the tab that started it.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
)

// ---- Query execution ----

// execStmt is a statement to run: its text and the arguments it is bound to.
// The confirmation dialog carries them, so the statements the user approves
// are exactly the statements that run, with the values they were built with.
type execStmt struct {
	Text string
	Args []any
}

// queryMsg is the outcome of a background execution. It carries the tab that
// started it, so switching tabs meanwhile cannot file the rows under the wrong
// one.
type queryMsg struct {
	sess *session
	res  *driver.Result
	// results holds one outcome per statement, in execution order. res is the
	// last row-returning outcome (or the aggregate for a script that only
	// changed rows), which is what history records.
	results []*driver.Result
	err     error
	stmt    string
	conn    string
	// open is the live connection the batch ran on; update.go compares it
	// with m.connOpen so an outcome from a connection that was switched away
	// from cannot re-mark the wrong session as having uncommitted work.
	open driver.Connection
	// origin describes the connection the batch ran on. The result belongs
	// to that connection, not to whatever is connected when the outcome
	// arrives: a query that was started before a switch produced rows of
	// the old database.
	origin resultOrigin
	// txOpen reports whether an uncommitted transaction exists once the batch
	// finished (manual commit mode opened one, and neither Commit nor
	// Rollback has ended it yet).
	txOpen bool
	// affected holds the number of rows each statement changed, in execution
	// order. The grid apply checks them: an UPDATE that addresses a row by
	// its primary key has to change exactly one row.
	affected []int64
	// rolledBack reports that the batch was rolled back, so that nothing of
	// it reached the database.
	rolledBack bool
	// apply marks the batch as the application of pending grid changes, so
	// the outcome is handled apart from an ordinary query: the grid must
	// survive a failure and be refreshed after a success.
	apply *gridApply
	// gen is the number of the batch that produced the outcome. Only the
	// outcome of the batch that owns the query state may clear the running
	// flag or file its rows: a late outcome of a batch a newer one replaced
	// is stale (see update.go).
	gen  int64
	took time.Duration
	at   time.Time
}

// queryText returns the SQL the active tab should run: the selection when there
// is one, the statement under the caret, or the whole buffer.
func (m *Model) queryText() string {
	text := m.selectedText()
	if strings.TrimSpace(text) == "" {
		all := m.buf.Text()
		if stmt, ok := sql.StatementAt(all, m.caretOffset(), m.cfg.Dialect); ok {
			text = stmt.Text
		} else {
			text = all
		}
	}
	return text
}

// runQuery executes the SQL of the active tab. A script runs statement by
// statement; every result is kept so the grid can switch between them, and the
// last row set is shown first.
func (m *Model) runQuery() tea.Cmd {
	text := m.queryText()
	return m.executeText(text, sql.Statements(text, m.cfg.Dialect))
}

// explainQuery runs EXPLAIN for the statement(s) the active tab would run, so
// the plan appears in the ordinary result grid.
func (m *Model) explainQuery() tea.Cmd {
	text := m.queryText()
	stmts := sql.Statements(text, m.cfg.Dialect)
	for i := range stmts {
		stmts[i].Text = "EXPLAIN " + stmts[i].Text
	}
	return m.executeText("EXPLAIN "+text, stmts)
}

// executeText runs already-split statements and files the outcome under the
// tab that started it. label is what the result pane remembers as the query.
//
// Every batch passes the safety guard first: read-only mode blocks writing
// statements before they reach the database, and DDL or destructive writes
// open the confirmation dialog instead of running immediately.
func (m *Model) executeText(label string, stmts []sql.Statement) tea.Cmd {
	conn := m.connOpen // the database that is connected now
	if conn == nil {
		return m.setMessage(m.t(i18n.KeyMsgNoConnection))
	}
	if len(stmts) == 0 {
		return m.setMessage(m.t(i18n.KeyMsgNothingToRun))
	}

	risks := make([]sql.RiskLevel, len(stmts))
	writes := 0
	for i, stmt := range stmts {
		risks[i] = sql.Risk(stmt.Text, m.cfg.Dialect)
		if risks[i].Writes() {
			writes++
		}
	}
	if m.settings.ReadOnly && writes > 0 {
		// Read-only mode is a hard gate: nothing write-shaped is sent.
		return m.setMessage(m.readOnlyBlockedText(writes))
	}
	if sql.MaxRisk(risks).NeedsConfirmation() {
		m.openConfirm(label, execStatements(stmts), risks)
		return nil
	}
	return m.executeStatements(label, execStatements(stmts), nil)
}

// execStatements turns parsed statements into the executable form the
// confirmation dialog carries: the text and the arguments it is bound to.
// The statements of a script carry no arguments.
func execStatements(stmts []sql.Statement) []execStmt {
	out := make([]execStmt, 0, len(stmts))
	for _, s := range stmts {
		out = append(out, execStmt{Text: s.Text})
	}
	return out
}

// executeStatements is the unguarded path behind executeText: it runs the
// statements one by one outside the UI goroutine and files each outcome under
// the tab that started it. Callers are responsible for the safety guard (or
// for the user having just confirmed the batch). apply, when not nil, marks
// the batch as the application of pending grid changes and binds it to the tab
// and connection it was built for.
//
// The grid apply is atomic: its UPDATEs either all reach the database or
// none of them do. A batch that stops halfway would leave the grid showing
// something the database does not hold, so a driver that can open a
// transaction gets one for the batch — unless the user is in manual commit
// mode, where the transaction is already the user's own and the batch joins
// it instead of nesting another one.
func (m *Model) executeStatements(label string, stmts []execStmt, apply *gridApply) tea.Cmd {
	conn := m.connOpen
	connName := m.conn.Name
	if apply != nil {
		// A grid apply runs on the connection the changes were built for, not
		// on whatever happens to be connected when the batch is dispatched.
		conn, connName = apply.conn.conn, apply.conn.name
	}
	if conn == nil {
		return m.setMessage(m.t(i18n.KeyMsgNoConnection))
	}

	// One query at a time; Esc cancels it while it runs. The batch gets its
	// own number: an outcome of a batch that a newer one replaced must not
	// clear the newer batch's running flag or overwrite its rows.
	m.cancelQuery()
	m.querySeq++
	gen := m.querySeq
	m.queryGen = gen
	ctx, cancel := context.WithCancel(context.Background())
	m.queryCancel, m.queryRunning = cancel, true

	// Captured here, outside the closure: it runs on a background goroutine
	// while the UI goroutine may switch connections (applyConnection writes
	// m.conn), which would be a data race — and could file the outcome under
	// the name of the connection that was not used.
	sess := m.session
	if apply != nil && apply.sess != nil {
		sess = apply.sess
	}
	// The origin of the result: the connection the batch runs on and the
	// schema its unqualified names resolve in. It is captured here, on the
	// UI goroutine, because the background goroutine must not read the
	// model.
	origin := resultOrigin{conn: connRef{name: connName, conn: conn}, schema: m.defaultSchema()}
	manual := m.txManual
	// Manual commit mode wraps the batch in a transaction the user commits
	// or rolls back later. A connection without transaction support (the
	// demo) has no manual mode to begin with — the menu items are hidden —
	// so it keeps auto-commit semantics.
	txm, hasTx := conn.(driver.TransactionManager)
	// atomic is the grid apply in auto-commit mode: the batch gets a
	// transaction of its own, so a failure rolls the whole thing back.
	atomic := apply != nil && hasTx && !manual
	return func() tea.Msg {
		defer cancel()
		start := time.Now()
		// Every outcome carries the same batch context. In particular, a
		// failure before the first statement is still a grid apply when apply
		// is set, so update.go sends it to finishApply instead of replacing the
		// grid and dropping the pending edits.
		outcome := func(err error, took time.Duration) queryMsg {
			return queryMsg{
				sess: sess, err: err, stmt: label, conn: connName, open: conn,
				origin: origin, gen: gen, txOpen: txm != nil && txm.InTx(),
				apply: apply, took: took, at: start,
			}
		}
		// The batch joins an already-open transaction or opens one; either
		// way nothing is committed behind the user's back.
		began := false
		if hasTx && !txm.InTx() && (manual || atomic) {
			if err := txm.Begin(ctx); err != nil {
				return outcome(err, time.Since(start))
			}
			began = true
		}
		var (
			results  []*driver.Result
			lastRow  *driver.Result
			affected int64
			counts   []int64
		)
		for i, stmt := range stmts {
			res, err := conn.Query(ctx, stmt.Text, stmt.Args...)
			if err != nil {
				// The transaction stays open on purpose: a batch that failed
				// halfway leaves its partial work for the user to commit or
				// roll back, visible through the uncommitted indicator.
				// The grid apply is the exception: it is all-or-nothing, so
				// it is rolled back and nothing of it reaches the database.
				rolled := false
				if atomic && began {
					_ = txm.Rollback(ctx)
					rolled = true
				}
				msg := outcome(err, time.Since(start))
				msg.rolledBack = rolled
				msg.affected = counts
				return msg
			}
			if res == nil {
				res = &driver.Result{}
			}
			results = append(results, res)
			affected += res.Affected
			if apply != nil {
				// Only the grid apply needs the per-statement count: it
				// checks that every UPDATE addressed exactly one row.
				counts = append(counts, res.Affected)
			}
			if len(res.Columns) > 0 {
				lastRow = res
			}
			if apply != nil && !apply.affectedOK(i, res.Affected) {
				// Every UPDATE of a grid apply addresses one row by its
				// primary key, so it has to change exactly one row. A
				// statement that changed none (the row is gone) or more
				// than one (the key is not unique after all) did not do
				// what the grid promised. An update that writes the value
				// the grid already holds is the exception: a driver that
				// counts only changed rows reports 0 there (MySQL).
				mismatch := &rowCountError{index: i, affected: res.Affected}
				rolled := false
				if atomic && began {
					_ = txm.Rollback(ctx)
					rolled = true
				}
				msg := outcome(mismatch, time.Since(start))
				msg.rolledBack = rolled
				msg.affected = counts
				return msg
			}
		}
		took := time.Since(start)
		if atomic && began {
			// The whole batch succeeded: it becomes visible at once.
			if err := txm.Commit(ctx); err != nil {
				msg := outcome(err, time.Since(start))
				msg.affected = counts
				return msg
			}
		}
		summary := lastRow
		if summary == nil {
			// A script that only changed rows keeps one aggregate result, just
			// as it did before result sets existed.
			summary = &driver.Result{Affected: affected, Duration: took}
			results = []*driver.Result{summary}
		}
		msg := outcome(nil, took)
		msg.res = summary
		msg.results = results
		msg.affected = counts
		return msg
	}
}

// clearResults puts the active tab's result pane back to its empty state while
// keeping the manually chosen column widths.
func (m *Model) clearResults() {
	m.closeResultMenu()
	m.session.setResults(nil, nil, "", m.cat, m.resultOrigin())
	m.layout.focus = paneEditor
}

// cancelQuery aborts the running statement, if any.
func (m *Model) cancelQuery() {
	if m.queryCancel != nil {
		m.queryCancel()
		m.queryCancel = nil
	}
	m.queryRunning = false
}

// ---- Transaction control ----

// txOutcomeMsg reports the result of a background Commit or Rollback.
type txOutcomeMsg struct {
	rollback bool
	err      error
}

// supportsTransactions reports whether the connected database implements
// driver.TransactionManager. Without it the transaction controls are hidden
// (see menuItem.needsTx) and the actions are inert.
func (m Model) supportsTransactions() bool {
	_, ok := m.connOpen.(driver.TransactionManager)
	return ok
}

// toggleManualCommit switches between auto-commit (every batch commits on its
// own, the historical behaviour) and manual commit (batches run inside a
// transaction until the user commits or rolls it back).
func (m *Model) toggleManualCommit() tea.Cmd {
	if !m.supportsTransactions() {
		return nil // the item is hidden without driver support; stay inert
	}
	if m.txManual && m.txOpen {
		// Dropping back to auto-commit with uncommitted work would discard it
		// silently; make the user choose Commit or Rollback first.
		return m.setMessage(m.t(i18n.KeyMsgTxSwitchBlocked))
	}
	m.txManual = !m.txManual
	return m.setMessage(m.t(txModeStateKey(m.txManual)))
}

// commitTx commits the open transaction outside the UI goroutine.
func (m *Model) commitTx() tea.Cmd { return m.txAction(false) }

// rollbackTx rolls the open transaction back outside the UI goroutine.
func (m *Model) rollbackTx() tea.Cmd { return m.txAction(true) }

// txAction ends the open transaction. The indicator goes out immediately: the
// driver closes the transaction even when the server refuses the command, so
// there is never uncommitted work left after the action was accepted.
func (m *Model) txAction(rollback bool) tea.Cmd {
	txm, ok := m.connOpen.(driver.TransactionManager)
	if !ok {
		return nil // the items are hidden without driver support; stay inert
	}
	if m.queryRunning {
		return m.setMessage(m.t(i18n.KeyMsgTxBusy))
	}
	if !m.txOpen {
		return m.setMessage(m.t(i18n.KeyMsgNoTransaction))
	}
	m.txOpen = false
	return func() tea.Msg {
		var err error
		if rollback {
			err = txm.Rollback(context.Background())
		} else {
			err = txm.Commit(context.Background())
		}
		return txOutcomeMsg{rollback: rollback, err: err}
	}
}

// rollbackOpenTx ends a transaction whose connection is about to go away
// (the user switches databases), discarding its work loudly: the returned
// warning goes to the status bar instead of letting uncommitted changes
// disappear silently. An empty result means there was nothing to roll back.
func (m *Model) rollbackOpenTx() string {
	if !m.txOpen {
		return ""
	}
	m.txOpen = false
	txm, ok := m.connOpen.(driver.TransactionManager)
	if !ok {
		return "" // nothing the UI could have opened
	}
	if err := txm.Rollback(context.Background()); err != nil {
		// The error comes from the server, so it is cleaned before it goes
		// to the status bar.
		return m.t(i18n.KeyMsgTxRollbackFailed, errText(err))
	}
	return m.t(i18n.KeyMsgTxRolledBackOnSwitch)
}

// txModeStateKey returns the message key describing the transaction mode.
func txModeStateKey(manual bool) i18n.Key {
	if manual {
		return i18n.KeyMsgManualCommitOn
	}
	return i18n.KeyMsgManualCommitOff
}

// readOnlyStateKey returns the message key describing the read-only state.
func readOnlyStateKey(on bool) i18n.Key {
	if on {
		return i18n.KeyMsgReadOnlyEnabled
	}
	return i18n.KeyMsgReadOnlyDisabled
}

// caretOffset returns the caret position as a rune offset into the buffer text,
// which is the coordinate system the SQL splitter works in.
func (m Model) caretOffset() int {
	c := m.buf.Cursor()
	offset := 0
	for row := 0; row < c.Row && row < m.buf.Lines(); row++ {
		offset += len(m.buf.Line(row)) + 1 // the newline
	}
	return offset + c.Col
}
