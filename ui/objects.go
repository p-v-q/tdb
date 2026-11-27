package ui

// objects.go implements the left-hand object browser: a tree that starts with
// the database connections and continues with schemas, resource groups
// (tables, views, functions, triggers, indexes, …), their members and table
// columns. The groups come from catalog.ResourceKinds and the connected
// catalog's capabilities, so a database only shows the resources it really has.
// It supports keyboard navigation, mouse selection and double-click/Enter to
// act on a row: open the connection form, connect, expand, or insert a name
// into the SQL editor. With "/" it also filters the tree incrementally, so a
// schema with hundreds of objects stays navigable.

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// rowKind tells what an object tree row stands for.
type rowKind int

// The row kinds.
const (
	rowNone rowKind = iota
	rowConn         // a database connection
	rowSchema
	rowGroup    // a resource group inside a schema (Tables, Functions, …)
	rowTable    // a relation: table, view or materialized view
	rowColumn   // a column of a relation
	rowResource // a non-relation resource (function, trigger, …)
	rowLocked   // the "connections are locked" notice that replaces the tree
)

// objectsState is the mutable state of the object browser.
type objectsState struct {
	sel       int             // index of the selected row (-1 for none)
	hover     int             // row under the pointer (-1 for none; hover never moves sel)
	scroll    int             // vertical scroll offset
	collapsed map[string]bool // schemas folded away
	expanded  map[string]bool // connections, groups and tables opened by hand
	filter    string          // active filter text ("" = no filtering)
	filtering bool            // the filter input is being typed in
}

// objectsRow is one row of the object tree.
type objectsRow struct {
	kind     rowKind
	conn     int                  // index into connectionList() for rowConn
	schema   string               // owning schema name (rowSchema and below)
	group    catalog.ResourceKind // rowGroup
	table    *catalog.Table       // rowTable, rowColumn
	column   *catalog.Column      // rowColumn
	resource *catalog.Resource    // rowResource
	indent   int
	// count is how many member rows a group shows right now. It is filled in
	// while the rows are built, so the number in the group label can never
	// drift away from the rows underneath it while a filter is active.
	count int
	// hasChildren reports whether child rows follow this row in the list the
	// tree was just built with. While a filter is active the rows are a
	// projection of the matches and the stored expansion state says nothing
	// about them, so the twisty is drawn from this flag instead.
	hasChildren bool
}

// newObjectsState returns the initial state: connections, schemas, resource
// groups and tables are folded away unless the user opens them. Opening the
// application therefore never floods the browser with a large schema before it
// is asked for.
func newObjectsState() objectsState {
	return objectsState{
		sel:       -1,
		hover:     -1,
		collapsed: map[string]bool{},
		expanded:  map[string]bool{},
	}
}

// resetObjects rebuilds the tree for the active connection, leaving every
// connection and schema folded. Connecting or reloading metadata must not
// expand the tree behind the user's back; large PostgreSQL catalogs otherwise
// open \"public\" and every table without being asked.
func (m *Model) resetObjects() {
	m.objects = newObjectsState()
	if m.cat != nil {
		for _, s := range m.cat.Schemas {
			m.objects.collapsed[s.Name] = true
		}
	}
}

// connKey returns the collapse key of a connection node. Nodes with the same
// default share the expanded map, so their keys must not collide.
func connKey(name string) string { return "conn:" + name }

// tableKey returns the collapse key of a table node.
func tableKey(schema, table string) string { return "table:" + schema + "." + table }

// groupKey returns the collapse key of a resource group. Groups use the same
// default as connections and tables: absent from the expanded map means folded.
func groupKey(schema string, kind catalog.ResourceKind) string {
	return "group:" + schema + ":" + kind.String()
}

// connectionList returns the connections shown in the object browser in the
// order the settings file holds them. The live session keeps its place in that
// list too: connecting to another saved connection must not teleport its row to
// the top while the user is looking at it. Only a session that has no saved
// entry (the connection given on the command line) is prepended, because there
// is no saved position to keep.
func (m Model) connectionList() []settings.Connection {
	out := make([]settings.Connection, 0, len(m.settings.Connections)+1)
	saved := false
	for _, c := range m.settings.Connections {
		if c.Name == m.conn.Name {
			saved = true
			break
		}
	}
	if !saved && m.conn.Name != "" {
		out = append(out, m.conn)
	}
	out = append(out, m.settings.Connections...)
	return out
}

// isSessionConnRow reports whether a connection row is the last opened session,
// connected or not (a disconnected session still offers "Connect").
func (m Model) isSessionConnRow(row objectsRow) bool {
	conns := m.connectionList()
	if row.kind != rowConn || row.conn < 0 || row.conn >= len(conns) {
		return false
	}
	return conns[row.conn].Name == m.conn.Name
}

// hasSession reports whether the connection has a session behind it: a live
// driver connection, or a provider (the tests' static catalog, the startup
// catalog before the pool is handed over). Without one the connection row is
// just a saved entry the user can open.
func (m Model) hasSession() bool {
	return m.connOpen != nil || m.cfg.Provider != nil
}

// isLiveConnRow reports whether a connection row is a live driver connection:
// only that row has a lifecycle to manage (Reconnect, Disconnect).
func (m Model) isLiveConnRow(row objectsRow) bool {
	return m.connOpen != nil && m.isSessionConnRow(row)
}

// isSessionRow reports whether a connection row belongs to the session being
// worked on (connected or not): it is the row whose schemas the tree shows and
// whose Enter folds/unfolds when a session exists.
func (m Model) isSessionRow(row objectsRow) bool {
	return m.hasSession() && m.isSessionConnRow(row)
}

// selectConnRowInTree moves the tree's caret to the row of the named connection
// and scrolls only as far as needed to show it. It runs after a connect, when
// the tree has been rebuilt: the connection the user opened stays the row they
// were looking at instead of the caret snapping back to the first row.
func (m *Model) selectConnRowInTree(name string) {
	if name == "" {
		return
	}
	conns := m.connectionList()
	for i, row := range m.objectsRows() {
		if row.kind != rowConn || row.conn < 0 || row.conn >= len(conns) {
			continue
		}
		if conns[row.conn].Name != name {
			continue
		}
		m.objects.sel = i
		m.ensureObjectsVisible(m.objectsBodyHeight())
		return
	}
}

// objectGroup is one resource-kind folder in a schema, already filtered to the
// connected database's capabilities and sorted for stable display.
type objectGroup struct {
	kind      catalog.ResourceKind
	tables    []*catalog.Table
	resources []*catalog.Resource
}

func (g objectGroup) count() int { return len(g.tables) + len(g.resources) }

// objectGroups builds the folders a schema shows. The canonical order lives in
// catalog.ResourceKinds; adding a new kind there makes it appear here without a
// new branch in the tree.
func (m Model) objectGroups(s *catalog.Schema) []objectGroup {
	if s == nil || m.cat == nil {
		return nil
	}
	out := make([]objectGroup, 0, len(catalog.ResourceKinds))
	for _, kind := range catalog.ResourceKinds {
		if !m.cat.Supports(kind) {
			continue
		}
		g := objectGroup{kind: kind}
		if kind.IsRelation() {
			g.tables = append(g.tables, s.TablesOf(kind)...)
			sort.SliceStable(g.tables, func(i, j int) bool {
				return strings.ToLower(g.tables[i].Name) < strings.ToLower(g.tables[j].Name)
			})
		} else {
			g.resources = append(g.resources, s.ResourcesOf(kind)...)
			sort.SliceStable(g.resources, func(i, j int) bool {
				return strings.ToLower(g.resources[i].Name) < strings.ToLower(g.resources[j].Name)
			})
		}
		if g.count() > 0 {
			out = append(out, g)
		}
	}
	return out
}

// objectsFilterQuery is the active object-tree filter, lowercased once so the
// matching below can compare case-insensitively. It is empty when no filter is
// set, which is what keeps the unfiltered tree on its stored expansion state.
func (m Model) objectsFilterQuery() string {
	if m.objects.filter == "" {
		return ""
	}
	return strings.ToLower(m.objects.filter)
}

// objectFilterMatch reports whether text contains the lowercased filter query.
func objectFilterMatch(text, q string) bool {
	return q != "" && strings.Contains(strings.ToLower(text), q)
}

// groupFilterMatch reports whether a resource-group row answers to the filter:
// by its localized label ("Tables", "函数") or by the stable English kind word
// ("function", "materialized view"), so an English query keeps working in a
// localized UI and vice versa.
func (m Model) groupFilterMatch(kind catalog.ResourceKind, q string) bool {
	return objectFilterMatch(m.resourceGroupLabel(kind), q) ||
		objectFilterMatch(kind.String(), q) ||
		objectFilterMatch(strings.ReplaceAll(kind.String(), "_", " "), q)
}

// tableFilterMatch reports whether a relation matches the filter by name, by
// schema-qualified name or by its comment — comments often carry what the
// name does not ("archived orders").
func tableFilterMatch(t *catalog.Table, q string) bool {
	return t != nil && (objectFilterMatch(t.Name, q) ||
		objectFilterMatch(t.QualifiedName(), q) ||
		objectFilterMatch(t.Comment, q))
}

// tableOwnFilterMatch reports whether the match belongs to the table itself
// rather than to something above it. Every qualified name of a schema starts
// with the schema name, so a schema query would otherwise count as a hit on
// each of its tables and open every column list at once — the reason a schema
// or group hit only shows one level of members. A qualified query that reaches
// past the schema part ("public.users") is the table's own hit.
func tableOwnFilterMatch(t *catalog.Table, q string) bool {
	if t == nil {
		return false
	}
	if objectFilterMatch(t.Name, q) || objectFilterMatch(t.Comment, q) {
		return true
	}
	return objectFilterMatch(t.QualifiedName(), q) && !objectFilterMatch(t.Schema, q)
}

// columnFilterMatch reports whether a column matches the filter by name or by
// its comment. Both fields are read through nil-safe helpers, so a catalog
// without comments behaves like before.
func columnFilterMatch(c *catalog.Column, q string) bool {
	return c != nil && (objectFilterMatch(c.Name, q) || objectFilterMatch(c.Comment, q))
}

// resourceFilterMatch reports whether a non-relation resource matches the
// filter: by name, by schema-qualified name, by its kind-specific detail (a
// function signature, a trigger's timing) or by its comment.
func resourceFilterMatch(r *catalog.Resource, q string) bool {
	if r == nil {
		return false
	}
	return objectFilterMatch(r.Name, q) ||
		objectFilterMatch(r.QualifiedName(), q) ||
		objectFilterMatch(r.Detail, q) ||
		objectFilterMatch(r.Comment, q)
}

// objectsRows flattens the tree into the navigable row list. Only the connected
// session has metadata underneath it; the saved ones wait to be connected.
//
// With an active filter the tree is rebuilt from the matches instead of the
// stored expansion state: the connection and every schema/group/table on the
// way to a match is kept, a matched schema or group shows its members, and
// nothing else appears. The expanded/collapsed maps are only read when no
// filter is set, so filtering never changes how the tree looks afterwards.
// Every row records whether child rows follow it (hasChildren), which is what
// draws the twisty while a filter is active.
func (m Model) objectsRows() []objectsRow {
	// A locked vault shows no connection at all — settings.Connections is
	// empty by construction — and the live session must not advertise its
	// name either: the tree is a single notice row whose Enter opens the
	// unlock dialog. It also keeps every other tree action out of reach,
	// because there are simply no other rows.
	if m.settings.Locked() {
		return []objectsRow{{kind: rowLocked, indent: 0}}
	}
	var rows []objectsRow
	conns := m.connectionList()
	q := m.objectsFilterQuery()
	for i := range conns {
		rows = append(rows, objectsRow{kind: rowConn, conn: i, indent: 0})
		if !m.isSessionRow(objectsRow{kind: rowConn, conn: i}) || m.cat == nil {
			continue
		}
		if q == "" && !m.objects.expanded[connKey(conns[i].Name)] {
			continue
		}
		connIdx := len(rows) - 1
		bodyStart := len(rows)
		for _, s := range m.cat.Schemas {
			if q != "" {
				rows = m.appendSchemaRowsFiltered(rows, s, q)
				continue
			}
			schemaIdx := len(rows)
			rows = append(rows, objectsRow{kind: rowSchema, schema: s.Name, indent: 1})
			if m.objects.collapsed[s.Name] {
				continue
			}
			groupStart := len(rows)
			for _, g := range m.objectGroups(s) {
				groupIdx := len(rows)
				rows = append(rows, objectsRow{
					kind: rowGroup, schema: s.Name, group: g.kind, indent: 2, count: g.count(),
				})
				if !m.objects.expanded[groupKey(s.Name, g.kind)] {
					continue
				}
				for _, t := range g.tables {
					tableIdx := len(rows)
					rows = append(rows, objectsRow{kind: rowTable, schema: s.Name, table: t, indent: 3})
					if !m.objects.expanded[tableKey(s.Name, t.Name)] {
						continue
					}
					for _, c := range t.Columns {
						rows = append(rows, objectsRow{
							kind: rowColumn, schema: s.Name, table: t, column: c, indent: 4,
						})
					}
					rows[tableIdx].hasChildren = len(rows) > tableIdx+1
				}
				for _, r := range g.resources {
					rows = append(rows, objectsRow{
						kind: rowResource, schema: s.Name, resource: r, indent: 3,
					})
				}
				rows[groupIdx].hasChildren = len(rows) > groupIdx+1
			}
			rows[schemaIdx].hasChildren = len(rows) > groupStart
		}
		rows[connIdx].hasChildren = len(rows) > bodyStart
	}
	return rows
}

// appendSchemaRowsFiltered appends one schema and the parts of it the filter
// keeps. A schema that answers to the query itself shows its members (tables
// and resources); its columns open per table below, not wholesale. Otherwise
// it only appears when at least one group below it has a match.
func (m Model) appendSchemaRowsFiltered(rows []objectsRow, s *catalog.Schema, q string) []objectsRow {
	schemaHit := objectFilterMatch(s.Name, q)
	var body []objectsRow
	for _, g := range m.objectGroups(s) {
		body = m.appendGroupRowsFiltered(body, s.Name, g, q, schemaHit)
	}
	if !schemaHit && len(body) == 0 {
		return rows
	}
	rows = append(rows, objectsRow{
		kind: rowSchema, schema: s.Name, indent: 1, hasChildren: len(body) > 0,
	})
	return append(rows, body...)
}

// appendGroupRowsFiltered appends a resource group and the member rows the
// filter keeps, indented below it. A group hit (by label or kind word) or a
// schema hit shows every member of the group — one level: the columns of a
// table only appear when the table itself matched (see tableOwnFilterMatch)
// or when a column matched, so a broad hit does not drown the tree in column
// rows. A lone member hit brings in its ancestors only.
func (m Model) appendGroupRowsFiltered(rows []objectsRow, schema string, g objectGroup, q string, schemaHit bool) []objectsRow {
	groupHit := m.groupFilterMatch(g.kind, q)
	expand := schemaHit || groupHit // the whole group is visible
	var members []objectsRow
	count := 0 // members the group shows: tables/resources, never their columns
	for _, t := range g.tables {
		tableHit := tableFilterMatch(t, q)
		var cols []*catalog.Column
		if tableOwnFilterMatch(t, q) {
			cols = t.Columns // a table that matched itself opens completely
		} else {
			for _, c := range t.Columns {
				if columnFilterMatch(c, q) {
					cols = append(cols, c)
				}
			}
		}
		if !expand && !tableHit && len(cols) == 0 {
			continue // neither the table nor one of its columns matched
		}
		members = append(members, objectsRow{
			kind: rowTable, schema: schema, table: t, indent: 3, hasChildren: len(cols) > 0,
		})
		count++
		for _, c := range cols {
			members = append(members, objectsRow{
				kind: rowColumn, schema: schema, table: t, column: c, indent: 4,
			})
		}
	}
	for _, r := range g.resources {
		if !expand && !resourceFilterMatch(r, q) {
			continue
		}
		members = append(members, objectsRow{kind: rowResource, schema: schema, resource: r, indent: 3})
		count++
	}
	if count == 0 {
		return rows
	}
	rows = append(rows, objectsRow{
		kind: rowGroup, schema: schema, group: g.kind, indent: 2, count: count, hasChildren: true,
	})
	return append(rows, members...)
}

// objectsMove moves the selection by delta rows.
func (m *Model) objectsMove(delta int) {
	rows := m.objectsRows()
	if len(rows) == 0 {
		return
	}
	m.objects.sel = clamp(m.objects.sel+delta, 0, len(rows)-1)
	m.ensureObjectsVisible(m.objectsBodyHeight())
}

// objectsBodyHeight is the number of visible rows in the object tree.
func (m Model) objectsBodyHeight() int {
	h := m.paneRects().objects.H - 1 // minus the title row
	if h < 1 {
		return 1
	}
	return h
}

// scrollObjectsToSelection moves the scroll offset so the selected row is
// visible. It neither clamps the selection nor touches the filter input, but
// it always keeps the offset inside the tree: a filter can shrink the row list
// far below a scroll offset that was valid before, and without the upper
// bound the surviving rows would be parked above the viewport.
func (m *Model) scrollObjectsToSelection(visible int) {
	if m.objects.sel < m.objects.scroll {
		m.objects.scroll = m.objects.sel
	}
	if m.objects.sel >= m.objects.scroll+visible {
		m.objects.scroll = m.objects.sel - visible + 1
	}
	m.objects.scroll = max(m.objects.scroll, 0)
	if maxScroll := len(m.objectsRows()) - visible; m.objects.scroll > maxScroll {
		m.objects.scroll = max(maxScroll, 0)
	}
}

// ensureObjectsVisible adjusts the scroll offset so the selected row is
// visible. It is a pure scroll: ending the filter input is the job of the
// keys and pointer actions that actually pick a row.
func (m *Model) ensureObjectsVisible(visible int) {
	m.scrollObjectsToSelection(visible)
}

// firstObjectRowIndex returns the first row that is not a connection. The
// connection rows are always kept while a filter is active, so the caret skips
// over them and lands on the first real object; it falls back to the first row
// when nothing but connections survived.
func firstObjectRowIndex(rows []objectsRow) int {
	for i, row := range rows {
		if row.kind != rowConn {
			return i
		}
	}
	return 0
}

// objectsFilterSync re-selects and scrolls after the filter text changed: the
// row list shrank or grew, so the caret must stay inside it and on screen. A
// caret that fell out of the new list lands on the first object instead of
// being pushed to the bottom; a caret that is still valid stays where it is.
// It deliberately never ends the input — only Enter, Esc and a click do.
func (m *Model) objectsFilterSync() {
	rows := m.objectsRows()
	switch {
	case len(rows) == 0:
		m.objects.sel = -1
	case m.objects.sel < 0 || m.objects.sel >= len(rows):
		m.objects.sel = firstObjectRowIndex(rows)
	}
	m.scrollObjectsToSelection(m.objectsBodyHeight())
}

// endObjectsFilterInput leaves the filter input but keeps the query in effect.
// Focus can move away without going through Enter or Esc (a pane shortcut or a
// pointer click), and the input must not stay armed: otherwise it would grab
// the keyboard again as soon as the tree is focused.
func (m *Model) endObjectsFilterInput() {
	m.objects.filtering = false
}

// objectsFilterKey edits the object-tree filter while its input owns the
// keyboard. Keys that neither edit the filter nor move the tree selection are
// swallowed, so nothing typed here can reach the editor or the global
// shortcuts: the branch runs before them (see handleKey). Left/right never
// fold anything either — see objectsToggle.
func (m *Model) objectsFilterKey(msg tea.KeyMsg) {
	key := msg.String()
	if msg.Type == tea.KeySpace {
		// Match handleKey's normalization: the key name, not the character.
		key = "space"
	}
	if msg.Paste {
		// A paste arrives as one KeyRunes message whose String() is the bracketed
		// text, so the plain-key check below would swallow it. It is inserted
		// whole, with the control characters dropped first the way the
		// results-grid filter does.
		m.objects.filter += stripControl(string(msg.Runes))
		m.objectsFilterSync()
		return
	}
	runes := msg.Runes
	switch key {
	case "up":
		m.objectsMove(-1)
		return
	case "down":
		m.objectsMove(1)
		return
	case "pgup":
		m.objectsMove(-m.objectsBodyHeight())
		return
	case "pgdown":
		m.objectsMove(m.objectsBodyHeight())
		return
	case "home":
		if rows := m.objectsRows(); len(rows) > 0 {
			m.objects.sel = 0
			m.scrollObjectsToSelection(m.objectsBodyHeight())
		}
		return
	case "end":
		if rows := m.objectsRows(); len(rows) > 0 {
			m.objects.sel = len(rows) - 1
			m.scrollObjectsToSelection(m.objectsBodyHeight())
		}
		return
	case "enter":
		// Keep the filter and leave the input.
		m.objects.filtering = false
		return
	case "esc":
		// Esc cancels a running query before this branch is reached (like in
		// the results grid); on its own it clears the filter.
		m.objects.filter, m.objects.filtering = "", false
	case "ctrl+u":
		m.objects.filter = ""
	case "backspace":
		if text := []rune(m.objects.filter); len(text) > 0 {
			m.objects.filter = string(text[:len(text)-1])
		}
	case "/":
		// The key that opens the input is a mode switch, never filter text.
		return
	default:
		// Only keys whose key name IS the text they carry are filter text:
		// modified keys (Alt+2, Ctrl+whatever) may carry runes as well, and
		// those belong to the shortcut layer that this branch is shielding.
		if len(runes) == 0 || (key != string(runes) && key != "space") {
			return // a key that does not edit the filter: swallow it
		}
		// A paste that arrived without the bracketed-paste marker falls
		// through to here as plain runes, so it gets the same treatment as
		// the marked one: inserted whole, control characters dropped.
		m.objects.filter += stripControl(string(runes))
	}
	m.objectsFilterSync()
}

// objectsAccept acts on the selected row: a connection toggle or connect, a
// node toggle, or inserting a name into the editor. The returned command is the
// asynchronous connection open, when there is one. Folding is skipped while a
// filter is in effect (see objectsFilterActive); connecting and inserting a
// name still work.
// objectsDoubleClick is the pointer's default action on a row. A double click
// on a table or view opens its data ("View data"): grabbing a look at a
// relation is what a second click means, and it is the action users expect from
// a table name. Every other row keeps the Enter behaviour (fold/unfold, insert a
// name, connect to a saved connection).
func (m *Model) objectsDoubleClick() tea.Cmd {
	rows := m.objectsRows()
	if m.objects.sel < 0 || m.objects.sel >= len(rows) {
		return nil
	}
	row := rows[m.objects.sel]
	if row.kind == rowTable && row.table != nil {
		return m.viewObjectData(row)
	}
	return m.objectsAccept()
}

// objectsAccept runs the keyboard's Enter action for the selected row.
func (m *Model) objectsAccept() tea.Cmd {
	rows := m.objectsRows()
	if m.objects.sel < 0 || m.objects.sel >= len(rows) {
		return nil
	}
	row := rows[m.objects.sel]
	switch row.kind {
	case rowLocked:
		// The notice row is itself the unlock button.
		m.openVaultUnlock()
	case rowConn:
		conn := m.connectionList()[row.conn]
		if m.isSessionRow(row) {
			// The connected session: fold its schemas away or bring them back.
			m.objectsSetOpenIfUnfiltered(row, !m.objectsOpen(row))
			return nil
		}
		return m.connectTo(conn)
	case rowSchema, rowGroup:
		// Enter folds or unfolds a schema/group; an object is a name to
		// insert, and folds with the arrow keys.
		m.objectsSetOpenIfUnfiltered(row, !m.objectsOpen(row))
	case rowTable, rowColumn, rowResource:
		m.insertObjectName(row)
	}
	return nil
}

// objectsFilterActive reports whether the tree is currently a filtered
// projection: the filter text is set or its input is open. The projection is
// rebuilt from the matches and never reads the stored expansion state, so
// expanding or folding there would change nothing visible now and only show up
// after the filter is cleared — user toggles are therefore not persisted.
func (m Model) objectsFilterActive() bool {
	return m.objects.filtering || m.objects.filter != ""
}

// collapseKey returns the map key that folds a node away.
func (m Model) collapseKey(row objectsRow) string {
	switch row.kind {
	case rowConn:
		return connKey(m.connectionList()[row.conn].Name)
	case rowSchema:
		return row.schema
	case rowGroup:
		return groupKey(row.schema, row.group)
	case rowTable:
		return tableKey(row.schema, row.table.Name)
	}
	return ""
}

// objectsOpen reports whether a tree node currently shows its children.
// Connections, groups and tables are closed until the user opens them; schemas
// are open unless they were folded. While a filter is active those maps say
// nothing about the projected rows, so the row's own hasChildren flag decides
// and the twisty always agrees with the rows underneath it.
func (m Model) objectsOpen(row objectsRow) bool {
	if m.objects.filter != "" {
		return row.hasChildren
	}
	switch row.kind {
	case rowConn, rowGroup, rowTable:
		return m.objects.expanded[m.collapseKey(row)]
	case rowSchema:
		return !m.objects.collapsed[m.collapseKey(row)]
	}
	return false
}

// objectsSetOpen opens or closes a node. The two maps keep the defaults
// different for connections/groups/tables and schemas, so absent keys are not
// ambiguous.
func (m *Model) objectsSetOpen(row objectsRow, open bool) {
	key := m.collapseKey(row)
	if key == "" {
		return
	}
	switch row.kind {
	case rowConn, rowGroup, rowTable:
		if open {
			m.objects.expanded[key] = true
		} else {
			delete(m.objects.expanded, key)
		}
	case rowSchema:
		if open {
			delete(m.objects.collapsed, key)
		} else {
			m.objects.collapsed[key] = true
		}
	}
}

// objectsSetOpenIfUnfiltered applies a user-requested expand/collapse, but
// only while no filter is in effect: the filtered tree is a projection of the
// matches, so writing the stored state there would silently change how the
// tree looks after the filter is cleared.
func (m *Model) objectsSetOpenIfUnfiltered(row objectsRow, open bool) {
	if m.objectsFilterActive() {
		return
	}
	m.objectsSetOpen(row, open)
}

// objectsCanToggle reports whether a row draws an expand/collapse twisty.
func (m Model) objectsCanToggle(row objectsRow) bool {
	switch row.kind {
	case rowConn:
		return m.isSessionRow(row) // only the session row has metadata below it
	case rowSchema, rowGroup, rowTable:
		return true
	}
	return false
}

// insertObjectName types the name of a tree row into the editor.
func (m *Model) insertObjectName(row objectsRow) {
	name := ""
	switch row.kind {
	case rowTable:
		name = row.table.Name
	case rowColumn:
		name = row.column.Name
	case rowResource:
		name = row.resource.Name
	case rowSchema:
		name = row.schema
	}
	// The name comes from the catalog, and the editor draws its buffer
	// verbatim: a control character in an identifier would be executed by the
	// terminal as soon as it is typed in (see sanitize.go), so it is dropped
	// here, before the insert.
	name = stripControl(name)
	if name == "" {
		return
	}
	// The name lands where the caret is, and the welcome page draws no caret:
	// the page steps aside first so the insert is visible and goes to the
	// caret the user can see (behind the page it would appear out of nowhere
	// once the tab converts, at whatever position was hidden underneath).
	m.revealEditor()
	m.typeSeq++ // its own undo group
	m.buf.InsertStringGrouped(name, m.typeSeq)
	m.layout.focus = paneEditor
	m.popup.close()
	m.ensureCursorVisible()
}

// objectsToggle folds the selected node open or closed (the left/right keys).
// A connection that is not connected yet is opened instead of folded. While a
// filter is in effect folding is a no-op; connecting still works.
func (m *Model) objectsToggle(open bool) tea.Cmd {
	rows := m.objectsRows()
	if m.objects.sel < 0 || m.objects.sel >= len(rows) {
		return nil
	}
	row := rows[m.objects.sel]
	if row.kind == rowConn && !m.isSessionRow(row) && open {
		return m.connectTo(m.connectionList()[row.conn])
	}
	m.objectsSetOpenIfUnfiltered(row, open)
	return nil
}

// objectsPointerToggle flips the node whose twisty was clicked. It is the
// pointer twin of objectsToggle and shares its rule: while a filter is in
// effect the click only selects the row (the caller ends the filter input),
// it never writes the stored expansion state.
func (m *Model) objectsPointerToggle(idx int) {
	if m.objectsFilterActive() {
		return
	}
	rows := m.objectsRows()
	if idx < 0 || idx >= len(rows) {
		return
	}
	m.objectsSetOpen(rows[idx], !m.objectsOpen(rows[idx]))
}

// objectsHoverAt refreshes the pointer highlight of the tree: the row under
// the point, or -1 when the point is on no row (outside the pane, on the
// title, past the last row). It only draws: m.objects.sel, the row Enter
// acts on, is never touched (round 7 §1).
func (m *Model) objectsHoverAt(rect Rect, x, y int) {
	m.objects.hover = -1
	if idx, ok := m.objectsItemAt(rect, x, y); ok {
		m.objects.hover = idx
	}
}

// objectsCaretAt reports whether a click landed on a row's expand/collapse
// twisty (the caret and its type icon). The whole icon is a comfortable target
// without stealing clicks from the label.
func (m Model) objectsCaretAt(rect Rect, x, y int) (int, bool) {
	idx, ok := m.objectsItemAt(rect, x, y)
	if !ok {
		return 0, false
	}
	rows := m.objectsRows()
	if idx < 0 || idx >= len(rows) || !m.objectsCanToggle(rows[idx]) {
		return 0, false
	}
	start := rect.X + 2 + rows[idx].indent*2
	if x >= start && x < start+2 {
		return idx, true
	}
	return 0, false
}

// ---- Rendering ----

// resourceGroupLabel returns the localized label of a resource group.
func (m Model) resourceGroupLabel(kind catalog.ResourceKind) string {
	switch kind {
	case catalog.KindTable:
		return m.t(i18n.KeyObjGroupTables)
	case catalog.KindView:
		return m.t(i18n.KeyObjGroupViews)
	case catalog.KindMaterializedView:
		return m.t(i18n.KeyObjGroupMaterializedViews)
	case catalog.KindFunction:
		return m.t(i18n.KeyObjGroupFunctions)
	case catalog.KindProcedure:
		return m.t(i18n.KeyObjGroupProcedures)
	case catalog.KindTrigger:
		return m.t(i18n.KeyObjGroupTriggers)
	case catalog.KindIndex:
		return m.t(i18n.KeyObjGroupIndexes)
	case catalog.KindSequence:
		return m.t(i18n.KeyObjGroupSequences)
	}
	return kind.String()
}

// objectGroupCount returns how many objects a group currently has.
func (m Model) objectGroupCount(schema string, kind catalog.ResourceKind) int {
	s := m.cat.FindSchema(schema)
	if s == nil {
		return 0
	}
	return s.Count(kind)
}

// resourceRowDetail combines ownership and kind-specific detail (signature,
// trigger timing, uniqueness) with the object's comment for one tree line.
func (m Model) resourceRowDetail(r *catalog.Resource) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if r.Owner != "" {
		parts = append(parts, m.t(i18n.KeyObjOn, r.Owner))
	}
	if r.Detail != "" {
		parts = append(parts, r.Detail)
	}
	if r.Comment != "" {
		parts = append(parts, r.Comment)
	}
	return strings.Join(parts, "  ")
}

// objectQualifiedName returns the schema-qualified name of the object a tree
// row stands for ("public.users"), or the plain name when the object lives in
// no schema. It is the name a tab opened on that object gets: a bare name is
// ambiguous in a database with several schemas.
func objectQualifiedName(row objectsRow) string {
	switch row.kind {
	case rowTable:
		if row.table != nil {
			return row.table.QualifiedName()
		}
	case rowResource:
		if row.resource != nil {
			return row.resource.QualifiedName()
		}
	case rowColumn:
		if row.table != nil && row.column != nil {
			return row.table.QualifiedName() + "." + row.column.Name
		}
	}
	return objectRowName(row)
}

// objectsFilterTitle renders the filter indicator glued behind the pane title:
// the query behind a slash, with a reverse caret while the input is open, e.g.
// "  /public▏". It is empty when no filter is active, so the title looks
// exactly like before.
func (m Model) objectsFilterTitle() string {
	if m.objects.filter == "" && !m.objects.filtering {
		return ""
	}
	// The filter is drawn as plain text, and it may hold a pasted escape
	// sequence: the characters are dropped.
	out := m.paneTitleStyle(paneObjects).Render("  /" + stripControl(m.objects.filter))
	if m.objects.filtering && m.layout.focus == paneObjects {
		out += m.theme.cursor.Render("▏")
	}
	return out
}

// objectsHasMatch reports whether any row below the connections survived the
// filter. Connection rows are always kept, so an empty tree still says what
// happened instead of leaving the pane blank.
func objectsHasMatch(rows []objectsRow) bool {
	for _, row := range rows {
		if row.kind != rowConn {
			return true
		}
	}
	return false
}

// renderObjects renders the objects pane: a title row plus the tree.
func (m Model) renderObjects(rect Rect) []string {
	if rect.H < 1 || rect.W < 1 {
		return nil
	}
	title := m.renderPaneTitleWith(rect, paneObjects, true, m.objectsFilterTitle())
	if len(title) == 0 {
		return nil
	}
	out := append([]string{}, title...)

	visible := rect.H - 1
	if visible < 1 {
		return out
	}

	rows := m.objectsRows()
	empty := m.objects.filter != "" && !objectsHasMatch(rows)
	for i := 0; i < visible; i++ {
		idx := m.objects.scroll + i
		switch {
		case idx < len(rows):
			out = append(out, padANSI(m.renderObjectRow(rows[idx], idx, rect.W), rect.W))
		case empty && idx == len(rows):
			out = append(out, padANSI(" "+m.theme.paneHint.Render(m.t(i18n.KeyObjNoMatches)), rect.W))
		default:
			out = append(out, padANSI("", rect.W))
		}
	}
	return out
}

// renderObjectRow renders one tree row: the indentation, an expand caret, a
// type icon, the label and, when the catalog has one, the object's comment in a
// quieter style. Selection is a full-row background, not an extra arrow; the
// row under the pointer wears the same background, and when both land on the
// same row the selection is what the style describes anyway.
func (m Model) renderObjectRow(row objectsRow, idx, width int) string {
	// sel (what Enter/clicks act on) and hover (where the pointer is) share
	// the one highlight the row is drawn with.
	highlighted := idx == m.objects.sel || idx == m.objects.hover
	focused := m.layout.focus == paneObjects
	ic := m.icons()

	// Selection is shown by the row background. Keeping the prefix blank avoids
	// a second arrow next to the expand/collapse twisty.
	prefix := "  "
	caret := " "
	if m.objectsCanToggle(row) {
		caret = ic.expandClosed
		if m.objectsOpen(row) {
			caret = ic.expandOpen
		}
	}
	icon, label := "", ""
	style := m.theme.objTable
	if focused {
		style = m.theme.objSchema
	}

	switch row.kind {
	case rowLocked:
		// The locked vault speaks in one quiet line, in place of the whole
		// connection list: no name leaks until the passphrase is proven.
		icon, label, style = "", m.t(i18n.KeyVaultLockedTree), m.theme.paneHint
	case rowConn:
		conn := m.connectionList()[row.conn]
		label = conn.Name + " (" + conn.Driver + ")"
		// A PostgreSQL connection is attached to one database: on a server
		// with several, the connection name alone does not say which one the
		// schemas below belong to, so the database is part of the row.
		if conn.Driver == "postgres" && strings.TrimSpace(conn.Database) != "" {
			label += " · " + strings.TrimSpace(conn.Database)
		}
		if m.isSessionRow(row) {
			icon, style = ic.connActive, m.theme.objSchema
		} else {
			icon = ic.connIdle
		}
	case rowSchema:
		icon, label, style = ic.schema, row.schema, m.theme.objSchema
	case rowGroup:
		icon, style = ic.resource(row.group), m.theme.objSchema
		count := row.count
		if count == 0 { // rows built by hand (tests) fall back to the catalog
			count = m.objectGroupCount(row.schema, row.group)
		}
		label = m.t(i18n.KeyObjGroupCount, m.resourceGroupLabel(row.group), count)
	case rowTable:
		icon, label = ic.resource(row.table.Kind()), row.table.Name
	case rowColumn:
		icon = ic.column
		// Name and type in one style: a row must not nest styles, or the
		// selection highlight breaks halfway through it.
		label = row.column.Name + "  " + row.column.Type
	case rowResource:
		icon, label = ic.resource(row.resource.Kind), row.resource.Name
	}
	// The label comes from the catalog, and a database may put control
	// characters into an identifier (PostgreSQL allows them in a quoted
	// one). The tree draws the name as plain text, so the characters are
	// dropped: a name must not be able to repaint the terminal.
	label = stripControl(label)

	comment := ""
	switch row.kind {
	case rowTable:
		comment = row.table.Comment
	case rowColumn:
		comment = row.column.Comment
	case rowResource:
		comment = m.resourceRowDetail(row.resource)
	case rowSchema:
		// The schema the connection works in is marked: the user has to see
		// at a glance where unqualified names resolve.
		if row.schema == m.defaultSchema() {
			comment = m.t(i18n.KeyObjSchemaDefault)
		}
	}
	comment = oneLine(comment)

	base := prefix + strings.Repeat(" ", row.indent*2) + caret + icon + " " + label
	if highlighted {
		text := base
		if comment != "" {
			text += "  " + comment
		}
		text = padANSI(clipEllipsis(text, width), width)
		return m.theme.popupSel.Render(text)
	}

	base = clipEllipsis(base, width)
	line := style.Render(base)
	if comment != "" {
		if avail := width - lipgloss.Width(base) - 2; avail > 0 {
			line += m.theme.objComment.Render("  " + clipEllipsis(comment, avail))
		}
	}
	return padANSI(line, width)
}
