package ui

// connection.go owns saved connection settings, background connection
// opening, and the switch to a newly opened database.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// connectTimeout bounds the opening of a connection: a host that accepts the
// TCP connection but never finishes the handshake must not keep the
// background command alive forever. It is longer than connTestTimeout (a
// liveness check) because a real handshake and the first metadata snapshot
// take longer than a ping.
const (
	connectTimeout         = 15 * time.Second
	connectionCloseTimeout = 3 * time.Second
)

// connMsg is the outcome of opening a connection in the background.
type connMsg struct {
	cfg  settings.Connection
	conn driver.Connection
	cat  *catalog.Catalog
	err  error
	// seq identifies the request that produced this outcome. Zero is accepted
	// for compatibility with directly constructed messages in embedders/tests.
	seq int64
	// reconnect marks the message as the outcome of a reconnect, so the
	// status bar can say "reconnected" instead of "connected".
	reconnect bool
	// warn carries a warning that has to survive until the outcome is
	// reported: the rollback of an open transaction, for instance.
	warn string
}

// invalidateQuery cancels the current batch and advances the query generation.
// Its eventual queryMsg is then stale even if the driver returns successfully
// after cancellation.
func (m *Model) invalidateQuery() {
	m.cancelQuery()
	m.querySeq++
	m.queryGen = m.querySeq
}

// invalidateConnectionRequests makes every in-flight connection and metadata
// request stale. It is called before a connection is opened, replaced, closed or
// disconnected.
func (m *Model) invalidateConnectionRequests() {
	m.connSeq++
	if m.connCancel != nil {
		m.connCancel()
		m.connCancel = nil
	}
	m.catalogSeq++
	m.invalidateQuery()
}

// connCloseMsg reports a rollback/close warning after the background shutdown
// finishes. Close itself is best effort, but a failed rollback is important:
// uncommitted work may have reached the server's disconnect handling.
type connCloseMsg struct {
	warn string
	next tea.Cmd // continuation (reconnect), run only after shutdown completes
	quit bool    // only the shutdown requested by quitNow may end the program
}

func (m *Model) takeConnCloseWarn() string {
	warn := m.connCloseWarn
	m.connCloseWarn = ""
	return warn
}

// connectTo saves the connection and opens it in the background, so the UI
// never blocks on a network round trip.
//
// Switching the connection drops the pending grid changes of every tab (they
// address tables of the database that is being left), so the switch asks
// first when there is any pending work.
func (m *Model) connectTo(c settings.Connection) tea.Cmd {
	// A locked vault refuses to form connections. The tree and the context
	// menu already keep this path hidden while locked, but the contract
	// belongs at the entry point itself: any future shortcut or caller must
	// not slip past it (see vaultBlocked).
	if cmd, blocked := m.vaultBlocked(); blocked {
		return cmd
	}
	if n := m.pendingGridChangesAll(); n > 0 {
		m.openLossConfirm(confirmLossConnect, n, -1, c)
		return nil
	}
	return m.connectToAs(c, false, "")
}

// connectToAs opens a connection in the background. reconnect marks the
// attempt as a reconnect (the status bar words it differently) and warn
// carries a warning that must not be lost on the way to the outcome.
func (m *Model) connectToAs(c settings.Connection, reconnect bool, warn string) tea.Cmd {
	if c.Name == "" {
		return m.setMessage(m.t(i18n.KeyConnNameRequired))
	}
	// The password is resolved before the connection is saved: a connection
	// whose secret is missing cannot be opened, and saving it anyway would
	// put a broken entry into the settings, from where the next save would
	// carry it into the file.
	password, err := resolveConnectionPassword(c)
	if err != nil {
		return m.setMessage(m.t(i18n.KeyConnPasswordEnvMissing, c.PasswordEnv))
	}
	m.saveConnection(c)
	cfg := driverConfig(c, password)
	// A newly issued request supersedes the previous handshake and every
	// metadata/query response belonging to the session it was opened from.
	m.invalidateConnectionRequests()
	seq := m.connSeq
	// The whole handshake shares one deadline: a server that accepts the
	// connection but never answers must not pin the background command. The
	// cancel function is retained so a newer request can abort it immediately.
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	m.connCancel = cancel
	// The translator is captured by value: the bundle is immutable, and the
	// background command must not read the model, which the UI goroutine owns.
	translator := m.translator
	open := func() tea.Msg {
		defer cancel()
		conn, err := driver.Open(ctx, c.Driver, cfg)
		if err != nil {
			return connMsg{cfg: c, err: err, reconnect: reconnect, warn: warn, seq: seq}
		}
		cat, catErr := conn.Catalog(ctx)
		if catErr != nil {
			return connMsg{cfg: c, conn: conn, err: catErr, reconnect: reconnect, warn: warn, seq: seq}
		}
		if warning := schemaWarning(c, cat, translator); warning != "" {
			// The database has no such schema: the driver left the server
			// default in place, so the connection is usable but unqualified
			// names resolve somewhere else. The user is told about it.
			warn = joinWarning(warn, warning)
		}
		return connMsg{cfg: c, conn: conn, cat: cat, reconnect: reconnect, warn: warn, seq: seq}
	}
	return tea.Batch(m.persistSettings(), open)
}

// schemaWarning returns the message for a default schema the connected
// database does not have. The connection stays usable: the driver leaves the
// server default in place, and the user is told why unqualified names resolve
// somewhere else.
func schemaWarning(c settings.Connection, cat *catalog.Catalog, t i18n.Translator) string {
	s := strings.TrimSpace(c.Schema)
	if s == "" {
		return ""
	}
	if catalogHasSchema(cat, s) {
		return ""
	}
	return t.T(i18n.KeyConnSchemaMissing, s)
}

// defaultSchema returns the schema the active connection works in, or "" when
// it works in the server's default schema.
func (m Model) defaultSchema() string {
	return strings.TrimSpace(m.conn.Schema)
}

// connRef identifies the connection that is live now.
func (m Model) connRef() connRef {
	return connRef{name: m.conn.Name, conn: m.connOpen}
}

// resultOrigin describes the connection that is live now: the connection
// itself and the schema its unqualified names resolve in.
func (m Model) resultOrigin() resultOrigin {
	return resultOrigin{conn: m.connRef(), schema: m.defaultSchema()}
}

// catalogHasSchema reports whether the catalog lists a schema of exactly that
// name. The comparison is exact because the driver quotes the name as an
// identifier: PostgreSQL folds an unquoted name to lower case, so "Public"
// and "public" are two different schemas there.
func catalogHasSchema(cat *catalog.Catalog, name string) bool {
	if cat == nil {
		return false
	}
	for _, s := range cat.Schemas {
		if s.Name == name {
			return true
		}
	}
	return false
}

// joinWarning puts two warnings side by side.
func joinWarning(first, second string) string {
	switch {
	case first == "":
		return second
	case second == "":
		return first
	}
	return first + " · " + second
}

// closeLiveConnection detaches the live connection and returns a background
// command that rolls back uncommitted work with a bounded context, then closes
// the connection. Detaching and cancelling queries happen synchronously; no
// driver call that may wait for a pooled connection runs on the UI goroutine.
func (m *Model) closeLiveConnection() tea.Cmd {
	m.invalidateConnectionRequests()
	conn := m.connOpen
	m.connOpen = nil
	if conn == nil {
		return nil
	}
	txm, hasTx := conn.(driver.TransactionManager)
	rollback := hasTx && m.txOpen
	m.txOpen = false
	translator := m.translator
	return func() tea.Msg {
		warn := ""
		if rollback {
			ctx, cancel := context.WithTimeout(context.Background(), connectionCloseTimeout)
			err := txm.Rollback(ctx)
			cancel()
			if err != nil {
				warn = translator.T(i18n.KeyMsgTxRollbackFailed, errText(err))
			} else {
				warn = translator.T(i18n.KeyMsgTxRolledBackOnSwitch)
			}
		}
		if err := conn.Close(); err != nil {
			warn = joinWarning(warn, translator.T(i18n.KeyConnCloseFailed, errText(err)))
		}
		return connCloseMsg{warn: warn}
	}
}

// closeOpenedConnection disposes of a connection that lost its request race.
// It never belongs to the live session, so its close outcome must not change the
// current status bar or any other session state.
func closeOpenedConnection(conn driver.Connection) tea.Cmd {
	if conn == nil {
		return nil
	}
	return func() tea.Msg {
		_ = conn.Close() // stale connections have no live session to report against
		return noopMsg{}
	}
}

// dropSession forgets what belonged to the live connection: the metadata
// snapshot and the provider completion reads from. The tree is rebuilt empty,
// because it described a database that is no longer reachable.
//
// The pending grid changes go with it: they were built from the tables of
// that database, and applying them through another connection would write to
// a table of the same name somewhere else. A confirmation dialog still open
// for that session is dropped too, so it can never be accepted against the
// dead one; its message travels back to the caller's status line.
func (m *Model) dropSession() string {
	m.catalogSeq++
	warn := m.abortConfirmOnConnectionChange()
	m.cat = nil
	m.cfg.Provider = nil
	m.resetObjects()
	m.clearGridEdits()
	return warn
}

// abortConfirmOnConnectionChange closes a confirmation whose session and
// connection are going away and reports it to the status bar. The batch it
// carried is dropped: running it after the switch would execute SQL built for
// one database against another.
func (m *Model) abortConfirmOnConnectionChange() string {
	if !m.confirm.open {
		return ""
	}
	apply := m.confirm.apply != nil
	m.closeConfirm()
	if apply {
		return m.t(i18n.KeyResultEditWrongConnection)
	}
	return ""
}

// disconnect closes the live connection and forgets it. The connection's own
// settings stay, so it can be opened again from the object browser.
func (m *Model) disconnect() tea.Cmd {
	name := m.conn.Name
	// Queries and in-flight requests are invalidated before the driver is
	// touched. Rollback and Close then run in the returned background command.
	closeCmd := m.closeLiveConnection()
	dropWarn := m.dropSession()
	m.cfg.ConnName = ""
	text := m.t(i18n.KeyConnDisconnected, name)
	if dropWarn != "" {
		text = dropWarn + " · " + text
	}
	return tea.Batch(m.setMessage(text), closeCmd)
}

// reconnect closes the live connection and opens it again with the saved
// settings. The old connection goes first: the database should see one
// session at a time, and a half-dead socket must not linger.
//
// A reconnect is a new session: the pending grid changes of the old one cannot
// be applied through it, so it asks first when there is any pending work.
func (m *Model) reconnect() tea.Cmd {
	if m.connOpen == nil {
		return m.setMessage(m.t(i18n.KeyMsgNoConnection))
	}
	if n := m.pendingGridChangesAll(); n > 0 {
		m.openLossConfirm(confirmLossReconnect, n, -1, settings.Connection{})
		return nil
	}
	return m.reconnectNow()
}

// reconnectNow performs the reconnect a loss confirmation protected.
func (m *Model) reconnectNow() tea.Cmd {
	c := m.conn
	closeCmd := m.closeLiveConnection()
	openCmd := m.connectToAs(c, true, "")
	if closeCmd == nil {
		return openCmd
	}
	// The old session is fully rolled back and closed before the replacement
	// handshake starts, but both operations remain off the UI goroutine. The
	// completion message carries the continuation back to the event loop.
	return func() tea.Msg {
		if msg, ok := closeCmd().(connCloseMsg); ok {
			msg.next = openCmd
			return msg
		}
		return openCmd()
	}
}

// saveConnection stores a connection in the settings, replacing one with the
// same name.
func (m *Model) saveConnection(c settings.Connection) {
	for i := range m.settings.Connections {
		if m.settings.Connections[i].Name == c.Name {
			m.settings.Connections[i] = c
			return
		}
	}
	m.settings.Connections = append(m.settings.Connections, c)
}

// replaceSavedConnection saves c and drops the saved entry that used to carry
// the old name, if the name changed.
func (m *Model) replaceSavedConnection(old string, c settings.Connection) {
	if old != "" && old != c.Name {
		kept := m.settings.Connections[:0]
		for _, existing := range m.settings.Connections {
			if existing.Name != old {
				kept = append(kept, existing)
			}
		}
		m.settings.Connections = kept
	}
	m.saveConnection(c)
}

// applyConnection makes an opened connection the active session. It returns a
// warning for the status bar when a confirmation dialog had to be dropped
// because its batch belonged to the connection that is going away.
func (m *Model) applyConnection(c settings.Connection, conn driver.Connection, cat *catalog.Catalog) string {
	// Any metadata request for the session that is ending is now stale.
	m.catalogSeq++
	// An open confirmation (a grid apply included) is bound to the session
	// that is ending: accepting it afterwards would run its statements — and
	// its UPDATEs — against the new database.
	warn := m.abortConfirmOnConnectionChange()
	m.connOpen = conn
	m.conn = c
	m.cfg.ConnName = c.Name
	m.cfg.Provider = conn
	if d := conn.Dialect(); d != nil {
		m.cfg.Dialect = d
		m.cfg.Engine = complete.NewDefaultEngine(d)
	}
	m.cat = cat
	// The rows on screen came from the previous database: their pending
	// changes address its tables and must not be applied here.
	m.clearGridEdits()
	m.resetObjects() // the tree now shows another database, with it folded
	m.popup.close()
	m.updateContext()
	return warn
}
