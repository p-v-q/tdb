package ui

// context_menu.go implements the object browser's context menu: right-clicking
// a row (or pressing Shift+F10 while the tree has the focus) offers the actions
// that make sense for that row.
//
// The menu is an overlay like the menu-bar dropdown: its geometry is one pure
// function shared by rendering and hit testing, and its actions are dispatched
// from one place so the mouse and the keyboard cannot drift apart.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
)

// objectsMenuAction is one action of the object browser context menu.
type objectsMenuAction int

// The context menu actions.
const (
	objActNone objectsMenuAction = iota
	objActNewConn
	objActConnect
	objActTest
	objActEdit
	objActDelete
	objActRefresh
	objActInsertName
	objActCopyName
	objActGenSelect
	objActGenInsert
	objActGenUpdate
	objActGenDelete
	objActViewData
	objActViewDef
	objActReconnect
	objActDisconnect
)

// objectsMenuState is the open context menu.
type objectsMenuState struct {
	open    bool
	x, y    int // screen coordinate of the click that opened it
	row     objectsRow
	conn    settings.Connection // rowConn only
	name    string              // the label the row stands for
	actions []objectsMenuAction
	sel     int
}

// connTestTimeout bounds a "test connection", so a dead host cannot keep the
// background command alive forever.
const connTestTimeout = 10 * time.Second

// connTestMsg is the outcome of testing a connection without switching to it.
type connTestMsg struct {
	cfg settings.Connection
	err error
}

// definitionTimeout bounds a definition fetch, so a dead or slow server
// cannot keep the background command alive forever.
const definitionTimeout = 10 * time.Second

// definitionMsg is the outcome of fetching an object's DDL in the background.
// conn is the live connection the fetch was started on: the update loop drops
// the message when the live connection changed meanwhile, so a definition
// from the old database cannot open a tab (and steal the focus) in the new
// session. ref is the object the DDL belongs to; its schema-qualified name
// becomes the name of the tab the DDL opens in.
type definitionMsg struct {
	conn driver.Connection
	ref  driver.DefinitionRef
	ddl  string
	err  error
}

// openObjectsMenu opens the context menu for a tree row at a screen point.
func (m *Model) openObjectsMenu(x, y int, row objectsRow) {
	actions := make([]objectsMenuAction, 0, 4)
	conn := settings.Connection{}
	switch row.kind {
	case rowConn:
		conn = m.connectionList()[row.conn]
		live := m.isLiveConnRow(row) // only a live driver connection has a lifecycle
		session := m.isSessionConnRow(row)
		if !live {
			// A saved connection can be opened, and so can the last session
			// after it has been disconnected.
			actions = append(actions, objActConnect)
		}
		actions = append(actions, objActTest, objActEdit)
		if live {
			// The lifecycle of the live connection: it can be opened again
			// or closed altogether.
			actions = append(actions, objActReconnect, objActDisconnect)
			actions = append(actions, objActRefresh)
		}
		if !session {
			actions = append(actions, objActDelete)
		}
		actions = append(actions, objActNewConn)
	case rowSchema, rowTable, rowColumn, rowResource:
		actions = append(actions, objActInsertName, objActCopyName)
		if row.kind == rowTable {
			actions = append(actions, objectSQLActions(row.table)...)
		}
		if (row.kind == rowTable || row.kind == rowResource) && m.canViewDefinition() {
			actions = append(actions, objActViewDef)
		}
		if m.connOpen != nil {
			actions = append(actions, objActRefresh)
		}
	case rowGroup:
		if m.connOpen != nil {
			actions = append(actions, objActRefresh)
		}
	}
	if len(actions) == 0 {
		return
	}
	name := objectRowName(row)
	if row.kind == rowConn {
		name = conn.Name
	}
	m.closeMenu()
	m.closeResultMenu()
	m.popup.close()
	m.ctx = objectsMenuState{
		open:    true,
		x:       x,
		y:       y,
		row:     row,
		conn:    conn,
		name:    name,
		actions: actions,
	}
}

// openObjectsNewConnMenu opens the context menu for the object browser's empty
// area. The old permanent "New connection…" row is gone; a right click on empty
// space is now the discoverable path to the connection form.
func (m *Model) openObjectsNewConnMenu(x, y int) {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.popup.close()
	m.ctx = objectsMenuState{
		open:    true,
		x:       x,
		y:       y,
		row:     objectsRow{kind: rowNone},
		actions: []objectsMenuAction{objActNewConn},
	}
}

// openObjectsMenuForSelection opens the context menu at the selected row; this
// is the keyboard entry point (Shift+F10).
func (m *Model) openObjectsMenuForSelection() {
	rows := m.objectsRows()
	if m.objects.sel < 0 || m.objects.sel >= len(rows) {
		return
	}
	r := m.paneRects().objects
	y := r.Y + 1 + (m.objects.sel - m.objects.scroll)
	if y < r.Y+1 || y >= r.Y+r.H {
		return
	}
	m.openObjectsMenu(r.X+2, y, rows[m.objects.sel])
}

// closeObjectsMenu closes the context menu.
func (m *Model) closeObjectsMenu() { m.ctx.open = false }

// objectsMenuLabelKey maps an action to its localized label.
func objectsMenuLabelKey(a objectsMenuAction) i18n.Key {
	switch a {
	case objActNewConn:
		return i18n.KeyObjMenuNewConn
	case objActConnect:
		return i18n.KeyObjMenuConnect
	case objActTest:
		return i18n.KeyObjMenuTest
	case objActEdit:
		return i18n.KeyObjMenuEdit
	case objActDelete:
		return i18n.KeyObjMenuDelete
	case objActRefresh:
		return i18n.KeyObjMenuRefresh
	case objActInsertName:
		return i18n.KeyObjMenuInsertName
	case objActCopyName:
		return i18n.KeyObjMenuCopyName
	case objActGenSelect:
		return i18n.KeyObjMenuGenSelect
	case objActGenInsert:
		return i18n.KeyObjMenuGenInsert
	case objActGenUpdate:
		return i18n.KeyObjMenuGenUpdate
	case objActGenDelete:
		return i18n.KeyObjMenuGenDelete
	case objActViewData:
		return i18n.KeyObjMenuViewData
	case objActViewDef:
		return i18n.KeyObjMenuViewDef
	case objActReconnect:
		return i18n.KeyConnReconnect
	case objActDisconnect:
		return i18n.KeyConnDisconnect
	}
	return i18n.KeyObjMenuNewConn
}

// objectSQLActions returns the statement templates a relation row offers.
// Views and materialized views are read-only, so they only get SELECT and
// "View data"; a plain table also gets the INSERT/UPDATE/DELETE templates.
func objectSQLActions(t *catalog.Table) []objectsMenuAction {
	if t == nil {
		return nil
	}
	actions := []objectsMenuAction{objActGenSelect}
	if t.Type == catalog.TypeTable {
		actions = append(actions, objActGenInsert, objActGenUpdate, objActGenDelete)
	}
	return append(actions, objActViewData)
}

// viewObjectData is the "View data" action: it opens the bounded SELECT of a
// relation in a tab of its own and runs it right away. The context menu and the
// pointer's double click on a relation both land here, so the two gestures can
// never diverge.
func (m *Model) viewObjectData(row objectsRow) tea.Cmd {
	text := generateViewData(m.cfg.Dialect, row.table)
	if text == "" {
		return m.setMessage(m.t(i18n.KeyMsgGenerateFailed))
	}
	m.newTabNamed(m.tabNameForStatement(row, objActViewData), text)
	return m.executeText(text, sql.Statements(text, m.cfg.Dialect))
}

// generateTableSQL dispatches a context-menu action to its pure generator.
func generateTableSQL(d dialect.Dialect, t *catalog.Table, a objectsMenuAction) string {
	switch a {
	case objActGenSelect:
		return generateSelect(d, t)
	case objActGenInsert:
		return generateInsert(d, t)
	case objActGenUpdate:
		return generateUpdate(d, t)
	case objActGenDelete:
		return generateDelete(d, t)
	}
	return ""
}

// tabNameForStatement is the name of the tab a generated statement opens in:
// the object's name with the kind of statement in brackets ("public.users
// [SELECT]"). The kind tells two tabs of the same table apart.
//
// The object's name comes from the catalog, where an identifier may carry
// control characters; the tab bar draws the name as plain text, so they are
// dropped.
func (m Model) tabNameForStatement(row objectsRow, a objectsMenuAction) string {
	return m.tabNameWithKind(stripControl(objectQualifiedName(row)), m.t(tabKindKey(a)))
}

// tabKindKey maps a statement action to the label of its kind.
func tabKindKey(a objectsMenuAction) i18n.Key {
	switch a {
	case objActGenSelect:
		return i18n.KeyTabKindSelect
	case objActGenInsert:
		return i18n.KeyTabKindInsert
	case objActGenUpdate:
		return i18n.KeyTabKindUpdate
	case objActGenDelete:
		return i18n.KeyTabKindDelete
	case objActViewData:
		return i18n.KeyTabKindData
	}
	return i18n.KeyTabKindSelect
}

// ---- Geometry ----

// objectsMenuInnerWidth is the content width of the context menu.
func (m Model) objectsMenuInnerWidth() int {
	w := 0
	for _, a := range m.ctx.actions {
		w = max(w, lipgloss.Width(m.t(objectsMenuLabelKey(a))))
	}
	return max(w+2, 12)
}

// objectsMenuRect is the on-screen rectangle of the open context menu.
func (m Model) objectsMenuRect() (Rect, bool) {
	if !m.ctx.open || len(m.ctx.actions) == 0 {
		return Rect{}, false
	}
	w := m.objectsMenuInnerWidth() + 2
	h := len(m.ctx.actions) + 2
	x := clamp(m.ctx.x, 0, max(0, m.width-w))
	y := m.ctx.y + 1
	if y+h > m.height {
		y = m.ctx.y - h
	}
	y = clamp(y, 0, max(0, m.height-h))
	return Rect{X: x, Y: y, W: w, H: h}, true
}

// objectsMenuItemAt reports whether a coordinate hits an item of the open
// context menu.
func (m Model) objectsMenuItemAt(x, y int) (int, bool) {
	r, ok := m.objectsMenuRect()
	if !ok || !r.Contains(x, y) {
		return 0, false
	}
	idx := y - r.Y - 1
	if idx < 0 || idx >= len(m.ctx.actions) {
		return 0, false
	}
	return idx, true
}

// ---- Interaction ----

// handleObjectsMenuKey handles a key press while the context menu is open and
// reports whether it consumed the key.
func (m Model) handleObjectsMenuKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "esc", "shift+f10":
		m.closeObjectsMenu()
		return m, nil, true
	case "up":
		n := len(m.ctx.actions)
		m.ctx.sel = (m.ctx.sel - 1 + n) % n
		return m, nil, true
	case "down":
		n := len(m.ctx.actions)
		m.ctx.sel = (m.ctx.sel + 1) % n
		return m, nil, true
	case "home":
		m.ctx.sel = 0
		return m, nil, true
	case "end":
		m.ctx.sel = len(m.ctx.actions) - 1
		return m, nil, true
	case "enter", "space":
		if m.ctx.sel < 0 || m.ctx.sel >= len(m.ctx.actions) {
			m.closeObjectsMenu()
			return m, nil, true
		}
		return m, m.runObjectsMenuAction(m.ctx.actions[m.ctx.sel]), true
	}
	// Printable input dismisses the menu without leaking into the editor.
	if len(msg.Runes) > 0 {
		m.closeObjectsMenu()
		return m, nil, true
	}
	m.closeObjectsMenu()
	return m, nil, false
}

// runObjectsMenuAction performs a context menu action.
func (m *Model) runObjectsMenuAction(a objectsMenuAction) tea.Cmd {
	// A locked vault keeps its connections out of reach: none of these
	// actions may open, create, edit or test a connection — the passphrase
	// comes first, and the status bar says so. The tree replaces its rows
	// with the locked notice while locked, so the menu can only be reached
	// for them through stale state; the guard makes that harmless anyway.
	if m.settings.Locked() {
		switch a {
		case objActNewConn, objActConnect, objActTest, objActEdit,
			objActDelete, objActReconnect, objActDisconnect:
			return m.setMessage(m.t(i18n.KeyVaultUnlockFirst))
		}
	}
	row, conn, name := m.ctx.row, m.ctx.conn, m.ctx.name
	m.closeObjectsMenu()
	switch a {
	case objActNewConn:
		m.openConnDialog()
	case objActConnect:
		return m.connectTo(conn)
	case objActTest:
		return tea.Batch(
			m.setMessage(m.t(i18n.KeyConnTesting, conn.Name)),
			m.testConnection(conn, m.isLiveConnRow(row)),
		)
	case objActEdit:
		m.openConnDialogFor(conn)
	case objActDelete:
		return m.deleteConnection(conn.Name)
	case objActRefresh:
		return m.refreshCatalog()
	case objActInsertName:
		m.insertObjectName(row)
	case objActCopyName:
		return m.copyObjectName(name)
	case objActGenSelect, objActGenInsert, objActGenUpdate, objActGenDelete:
		text := generateTableSQL(m.cfg.Dialect, row.table, a)
		if text == "" {
			return m.setMessage(m.t(i18n.KeyMsgGenerateFailed))
		}
		m.newTabNamed(m.tabNameForStatement(row, a), text)
		return nil
	case objActViewData:
		return m.viewObjectData(row)
	case objActViewDef:
		return m.fetchDefinition(row)
	case objActReconnect:
		return m.reconnect()
	case objActDisconnect:
		return m.disconnect()
	}
	return nil
}

// testConnection checks that a connection can be opened and introspected. The
// active session is tested as it is; any other connection is opened in the
// background and closed again, so testing never switches sessions.
//
// The receiver is a pointer: the method reports a missing password through
// the status bar, and a value receiver would set the message on a copy —
// the model would keep saying "Testing connection…" forever.
func (m *Model) testConnection(c settings.Connection, active bool) tea.Cmd {
	// Locked vault: refuse before any driver work (see vaultBlocked). The
	// context menu already intercepts its Test action while locked; this
	// guard makes the entry point itself safe for any other caller.
	if cmd, blocked := m.vaultBlocked(); blocked {
		return cmd
	}
	password, err := resolveConnectionPassword(c)
	if err != nil {
		return m.setMessage(m.t(i18n.KeyConnPasswordEnvMissing, c.PasswordEnv))
	}
	cfg := driverConfig(c, password)
	live := m.connOpen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connTestTimeout)
		defer cancel()
		if active && live != nil {
			if _, err := live.Catalog(ctx); err != nil {
				return connTestMsg{cfg: c, err: err}
			}
			return connTestMsg{cfg: c}
		}
		conn, err := driver.Open(ctx, c.Driver, cfg)
		if err != nil {
			return connTestMsg{cfg: c, err: err}
		}
		defer conn.Close()
		if _, err := conn.Catalog(ctx); err != nil {
			return connTestMsg{cfg: c, err: err}
		}
		return connTestMsg{cfg: c}
	}
}

// refreshCatalog reloads the metadata of the active connection.
func (m *Model) refreshCatalog() tea.Cmd {
	conn := m.connOpen
	if conn == nil {
		return m.setMessage(m.t(i18n.KeyMsgNoConnection))
	}
	m.catalogSeq++
	seq := m.catalogSeq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connTestTimeout)
		defer cancel()
		cat, err := conn.Catalog(ctx)
		return catalogMsg{cat: cat, err: err, conn: conn, seq: seq}
	}
}

// deleteConnection removes a saved connection and persists the settings.
func (m *Model) deleteConnection(name string) tea.Cmd {
	// Locked vault: the list in memory is empty (the connections live in
	// the envelope), so deleting would persist nothing anyway — but the
	// refusal must be explicit, not an accident of state (see
	// vaultBlocked).
	if cmd, blocked := m.vaultBlocked(); blocked {
		return cmd
	}
	kept := m.settings.Connections[:0]
	for _, c := range m.settings.Connections {
		if c.Name != name {
			kept = append(kept, c)
		}
	}
	m.settings.Connections = kept
	return tea.Batch(
		m.setMessage(m.t(i18n.KeyConnDeleted, name)),
		m.persistSettings(),
	)
}

// copyObjectName copies a tree row's name to the system clipboard.
func (m *Model) copyObjectName(name string) tea.Cmd {
	if name == "" {
		return nil
	}
	if err := copyToClipboard(name); err != nil {
		return m.setMessage(m.t(i18n.KeyMsgCopyFailed, err))
	}
	return m.setMessage(m.t(i18n.KeyMsgNameCopied, name))
}

// canViewDefinition reports whether the live connection can produce object
// DDL. The capability is discovered by type assertion on the optional
// driver.DefinitionProvider interface — never by checking which database is
// connected — so the context menu offers "View definition" exactly when the
// action can work, and a database without stored DDL hides it instead of
// failing.
func (m Model) canViewDefinition() bool {
	_, ok := m.connOpen.(driver.DefinitionProvider)
	return ok
}

// definitionRef maps a tree row onto the object a driver should describe:
// a relation (table, view) or a non-relation resource (function, trigger, …).
func definitionRef(row objectsRow) (driver.DefinitionRef, bool) {
	switch row.kind {
	case rowTable:
		return driver.DefinitionRef{
			Kind: row.table.Kind(), Schema: row.table.Schema, Name: row.table.Name,
		}, true
	case rowResource:
		return driver.DefinitionRef{
			Kind: row.resource.Kind, Schema: row.resource.Schema,
			Name: row.resource.Name, Owner: row.resource.Owner,
		}, true
	}
	return driver.DefinitionRef{}, false
}

// definitionTabName is the name of the tab a definition opens in: the object's
// schema-qualified name ("public.users"), because a bare name is ambiguous in
// a database with several schemas.
//
// The name comes from the catalog and may carry control characters, so they
// are dropped: the tab bar draws the name as plain text.
func definitionTabName(ref driver.DefinitionRef) string {
	if ref.Schema == "" {
		return stripControl(ref.Name)
	}
	return stripControl(ref.Schema + "." + ref.Name)
}

// fetchDefinition loads a tree object's DDL in the background and reports it
// back as a definitionMsg; the update loop opens it in a new tab — or drops
// it when the connection was switched meanwhile (see definitionMsg). The menu
// only offers the action when the live connection implements
// driver.DefinitionProvider (see openObjectsMenu), so this path needs no
// per-database branch.
func (m *Model) fetchDefinition(row objectsRow) tea.Cmd {
	conn := m.connOpen
	prov, ok := conn.(driver.DefinitionProvider)
	if !ok {
		return nil
	}
	ref, ok := definitionRef(row)
	if !ok {
		return nil
	}
	return tea.Batch(
		m.setMessage(m.t(i18n.KeyMsgDefinitionLoading, ref.Name)),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), definitionTimeout)
			defer cancel()
			ddl, err := prov.Definition(ctx, ref)
			return definitionMsg{conn: conn, ref: ref, ddl: ddl, err: err}
		},
	)
}

// objectRowName returns the name a tree row stands for (connections are named
// by the caller, which holds the connection list).
func objectRowName(row objectsRow) string {
	switch row.kind {
	case rowSchema:
		return row.schema
	case rowTable:
		return row.table.Name
	case rowColumn:
		return row.column.Name
	case rowResource:
		return row.resource.Name
	}
	return ""
}

// ---- Rendering ----

// renderObjectsMenu renders the context menu with a border, on top of the panes.
func (m Model) renderObjectsMenu() (Rect, []string, bool) {
	r, ok := m.objectsMenuRect()
	if !ok || r.H < 3 || r.W < 4 {
		return Rect{}, nil, false
	}
	inner := r.W - 2
	border := m.theme.menuBorder

	out := []string{border.Render("┌" + strings.Repeat("─", inner) + "┐")}
	for i, a := range m.ctx.actions {
		style := m.theme.menuBox
		if i == m.ctx.sel {
			style = m.theme.menuItemSel
		}
		label := m.t(objectsMenuLabelKey(a))
		pad := max(inner-1-lipgloss.Width(label), 1)
		line := style.Render(" " + label + strings.Repeat(" ", pad))
		out = append(out, border.Render("│")+line+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return r, out, true
}
