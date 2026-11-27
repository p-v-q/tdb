package ui

// update.go is the message loop. It routes asynchronous connection, catalog
// and query results back to the model (and to the tab that started them) and
// keeps derived state coherent.

import (
	"context"
	gosql "database/sql"
	"errors"
	"io"
	"net"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// catalogMsg is the result of the asynchronous metadata load.
type catalogMsg struct {
	cat  *catalog.Catalog
	err  error
	conn driver.Connection // source identity; nil is valid for a static startup provider
	seq  int64             // newest refresh generation for that source
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.cat == nil && m.cfg.Provider != nil {
		provider := m.cfg.Provider
		return func() tea.Msg {
			// The metadata load is bounded like a connection: a server that
			// accepts the connection but never answers must not pin the
			// startup.
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			defer cancel()
			cat, err := provider.Catalog(ctx)
			return catalogMsg{cat: cat, err: err, conn: m.connOpen, seq: m.catalogSeq}
		}
	}
	return nil
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ensureCursorVisible()
		return m, nil

	case catalogMsg:
		if msg.conn != m.connOpen || msg.seq != m.catalogSeq {
			// The refresh belongs to a connection/session that has been replaced,
			// or a newer refresh already superseded it. Its errors are stale too.
			return m, nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.DeadlineExceeded) {
				return m, m.setMessage(m.t(i18n.KeyConnTimeout, connectTimeout.String()))
			}
			return m, m.setMessage(m.t(i18n.KeyMsgCatalogFailed, errText(msg.err)))
		}
		m.cat = msg.cat
		m.resetObjects() // reset the tree when the metadata changes
		return m, m.setMessage(m.t(i18n.KeyMsgCatalogLoaded,
			m.schemaCountText(len(msg.cat.Schemas)), m.tableCountText(len(msg.cat.AllTables()))))

	case connOptionsMsg:
		// A server-provided list belongs to one form instance and one exact
		// parameter snapshot. Closing the form or editing a server/credential
		// field advances the generation and drops the response silently.
		if !m.connDlg.open || m.connDlg.instance != msg.instance ||
			m.connDlg.listSeq != msg.seq || m.connDlg.listKey != msg.key ||
			m.connDlg.listParams != msg.params {
			return m, nil
		}
		if msg.err != nil {
			m.connDlg.listKey = ""
			m.connDlg.listParams = settings.Connection{}
			return m, m.setMessage(m.t(i18n.KeyConnListFailed, errText(msg.err)))
		}
		m.applyConnOptions(msg)
		return m, nil

	case connMsg:
		if msg.seq != 0 && msg.seq != m.connSeq {
			// A newer connect/reconnect/disconnect superseded this request. A
			// successful stale open still owns a real connection and must be
			// closed, but neither success nor failure may touch the live session.
			var closeCmd tea.Cmd
			if msg.conn != nil && msg.conn != m.connOpen {
				closeCmd = closeOpenedConnection(msg.conn)
			}
			return m, closeCmd
		}
		if msg.seq != 0 {
			m.connCancel = nil
		}
		pendingWarn := m.takeConnCloseWarn()
		if msg.err != nil {
			// The connection was saved before it was opened, so it stays in
			// the settings either way; report why it did not open.
			dropWarn := ""
			if msg.reconnect {
				// The old connection is gone and the new one did not open:
				// what the tree shows belongs to a dead session.
				dropWarn = m.dropSession()
			}
			// The handshake may have succeeded before metadata loading failed.
			// Dispose of that uninstalled connection off the UI goroutine.
			var closeCmd tea.Cmd
			if msg.conn != nil && msg.conn != m.connOpen {
				closeCmd = closeOpenedConnection(msg.conn)
			}
			text := ""
			switch {
			case errors.Is(msg.err, driver.ErrNotImplemented):
				text = m.t(i18n.KeyConnDriverPending, msg.cfg.Driver)
			case errors.Is(msg.err, context.DeadlineExceeded):
				text = m.t(i18n.KeyConnTimeout, connectTimeout.String())
			default:
				text = m.t(i18n.KeyConnFailed, errText(msg.err))
			}
			if dropWarn != "" {
				// The dropped confirmation matters as much as the failure: it
				// explains why a batch the user was about to confirm is gone.
				text = dropWarn + " · " + text
			}
			text = joinWarning(joinWarning(pendingWarn, msg.warn), text)
			return m, tea.Batch(closeCmd, m.setMessage(text))
		}
		// Detach and invalidate the old session synchronously; its rollback and
		// Close run in the background while the new connection becomes live.
		closeCmd := m.closeLiveConnection()
		// A confirmation dialog opened for the old session is closed here: its
		// batch (grid apply included) belongs to the connection that is going
		// away and must never run against the new one.
		confirmWarn := m.applyConnection(msg.cfg, msg.conn, msg.cat)
		// The connection the user opened keeps its row selected: connecting
		// rebuilds the tree (the old metadata is gone), and leaving the caret
		// on row 0 would make the tree look like it jumped to the top even
		// when the connection itself kept its place in the list.
		m.selectConnRowInTree(msg.cfg.Name)
		text := m.t(i18n.KeyConnConnected, msg.cfg.Name)
		if msg.reconnect {
			text = m.t(i18n.KeyConnReconnected, msg.cfg.Name)
		}
		if confirmWarn != "" {
			text = confirmWarn + " · " + text
		}
		text = joinWarning(joinWarning(pendingWarn, msg.warn), text)
		return m, tea.Batch(closeCmd, m.setMessage(text))

	case connCloseMsg:
		var statusCmd tea.Cmd
		if msg.warn != "" {
			if msg.next != nil {
				m.connCloseWarn = joinWarning(m.connCloseWarn, msg.warn)
			}
			text := msg.warn
			if m.status.message != "" {
				text = joinWarning(text, m.status.message)
			}
			statusCmd = m.setMessage(text)
		}
		if msg.quit {
			return m, tea.Quit
		}
		return m, tea.Batch(msg.next, statusCmd)

	case connTestMsg:
		if msg.err != nil {
			if errors.Is(msg.err, driver.ErrNotImplemented) {
				return m, m.setMessage(m.t(i18n.KeyConnTestPending, msg.cfg.Driver))
			}
			return m, m.setMessage(m.t(i18n.KeyConnTestFailed, errText(msg.err)))
		}
		return m, m.setMessage(m.t(i18n.KeyConnTestOK, msg.cfg.Name))

	case definitionMsg:
		// The fetch runs in the background (bounded by definitionTimeout)
		// while the UI goroutine may disconnect or switch databases. A
		// definition from a connection that is no longer the live one is
		// dropped — success and failure alike — instead of opening a tab
		// with DDL the user might run against the wrong database. Driver
		// connections are pointer-based, so comparing the interface values
		// is object identity, and applyConnection always assigns a freshly
		// opened connection (the old one is closed).
		if msg.conn != m.connOpen {
			return m, nil
		}
		if msg.err != nil {
			return m, m.setMessage(m.t(i18n.KeyMsgDefinitionFailed, errText(msg.err)))
		}
		m.newTabNamed(definitionTabName(msg.ref), msg.ddl)
		return m, m.setMessage(m.t(i18n.KeyMsgDefinitionOpened, msg.ref.Name))

	case queryMsg:
		if msg.gen != 0 && msg.gen != m.queryGen {
			// A late outcome of a batch that a newer one replaced: it must
			// not clear the running flag (or the cancel function) of the
			// newer batch, and its rows must not replace the newer result.
			return m, nil
		}
		m.queryRunning = false
		m.queryCancel = nil
		// Track uncommitted work for the indicator, but only while the
		// connection that ran the batch is still the live one: a switch
		// already rolled that state back (see connMsg).
		if msg.open == m.connOpen {
			m.txOpen = msg.txOpen
		}
		historyCmd := m.recordHistory(msg)
		if msg.apply != nil {
			// The batch was the application of pending grid changes: it has
			// its own outcome, because the grid must survive a failure and
			// be refreshed after a success.
			return m.finishApply(msg, historyCmd)
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				// The user aborted it: whatever was on screen stays.
				return m, m.setMessage(m.t(i18n.KeyMsgQueryCancelled))
			}
			if errors.Is(msg.err, driver.ErrNotImplemented) {
				return m, m.setMessage(m.t(i18n.KeyMsgNotImplemented))
			}
			msg.sess.setResults(nil, msg.err, msg.stmt, m.cat, msg.origin)
			if isConnectionLost(msg.err) {
				// The connection is gone: say so instead of a plain "query
				// failed", and keep the driver's own words, which tell the
				// user what to reconnect to.
				return m, tea.Batch(
					m.setMessage(m.t(i18n.KeyConnLost)+": "+errText(msg.err)),
					historyCmd,
				)
			}
			return m, tea.Batch(m.setMessage(m.t(i18n.KeyResultsError)), historyCmd)
		}
		results := msg.results
		if len(results) == 0 && msg.res != nil {
			results = []*driver.Result{msg.res}
		}
		// The notice of an applied grid edit belongs to the edit, not to the
		// rows, so it is read before the new result replaces the old one.
		notice := msg.sess.result.applyNotice
		msg.sess.setResults(results, nil, msg.stmt, m.cat, msg.origin)
		if msg.sess == m.session {
			m.ensureResultVisible() // keep the visible tab's grid in step
		}
		if notice != "" {
			return m, tea.Batch(m.setMessage(notice), historyCmd)
		}
		return m, tea.Batch(m.setMessage(m.resultSetSummary(msg.sess)), historyCmd)

	case txOutcomeMsg:
		if msg.err != nil {
			if msg.rollback {
				return m, m.setMessage(m.t(i18n.KeyMsgTxRollbackFailed, errText(msg.err)))
			}
			return m, m.setMessage(m.t(i18n.KeyMsgTxCommitFailed, errText(msg.err)))
		}
		if msg.rollback {
			return m, m.setMessage(m.t(i18n.KeyMsgTxRolledBack))
		}
		return m, m.setMessage(m.t(i18n.KeyMsgTxCommitted))

	case statusTimeoutMsg:
		if int(msg) == m.status.id {
			m.status.message = ""
		}
		return m, nil

	case statusMessageMsg:
		return m, m.setMessage(string(msg))

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		var cmd tea.Cmd
		m, cmd = m.handleKey(msg)
		m.updateContext()
		return m, cmd
	}
	return m, nil
}

// isConnectionLost reports whether err says that the connection to the
// database is gone.
//
// This is a heuristic, and deliberately so: the drivers wrap their errors
// differently, and the database/sql layer only promises "this connection is
// bad" through sql.ErrConnDone. A stream that ended (io.EOF), a network
// error, or one of the well-known "the server is not there any more"
// messages counts as a lost connection; everything else keeps the ordinary
// error handling. A false positive only changes the wording of the status
// message — the error itself is always shown — so the heuristic errs on the
// side of telling the user that the connection is gone.
func isConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, gosql.ErrConnDone) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, hint := range []string{
		"connection refused",
		"closed connection",
		"broken pipe",
		"connection reset",
	} {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}
