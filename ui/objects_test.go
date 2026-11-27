package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// objectRowY returns the screen row of an object-tree row.
func objectRowY(m Model, idx int) int {
	r := m.paneRects().objects
	return r.Y + 1 + (idx - m.objects.scroll)
}

// objectRowByIndex returns the row at idx, failing the test when it is gone.
func objectRowByIndex(t *testing.T, m Model, idx int) objectsRow {
	t.Helper()
	rows := m.objectsRows()
	if idx < 0 || idx >= len(rows) {
		t.Fatalf("object row %d out of range (rows = %d)", idx, len(rows))
	}
	return rows[idx]
}

func TestObjectBrowserColumnsStartCollapsed(t *testing.T) {
	m := sized("")
	for _, row := range m.objectsRows() {
		if row.kind == rowColumn {
			t.Fatalf("columns must start folded, found %q", row.column.Name)
		}
	}

	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("no users table in the tree")
	}
	row := objectRowByIndex(t, m, idx)
	r := m.paneRects()
	caretX := r.objects.X + 2 + row.indent*2
	y := objectRowY(m, idx)

	// Clicking the label selects the table but must not unfold it: only the
	// twisty is a button.
	m = send(m, mousePress(r.objects.X+20, y))
	if m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("clicking the label must not unfold a table")
	}

	// The twisty (caret plus icon) unfolds it.
	m = send(m, mousePress(caretX, y))
	if !m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("clicking the twisty must unfold the table")
	}
	var columns int
	for _, row := range m.objectsRows() {
		if row.kind == rowColumn {
			columns++
		}
	}
	if columns == 0 {
		t.Fatal("unfolding the table must reveal its columns")
	}

	// Clicking it again folds the columns away.
	m = send(m, mousePress(caretX, y))
	if m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("clicking the twisty again must fold the table")
	}
	for _, row := range m.objectsRows() {
		if row.kind == rowColumn {
			t.Fatalf("folded table must hide %q", row.column.Name)
		}
	}
}

func TestObjectBrowserShowsComments(t *testing.T) {
	m := sized("")
	m.layout.objectsWidth = 60 // wide enough for names and comments
	m.objects.expanded[tableKey("public", "users")] = true

	view := stripANSI(m.View())
	for _, want := range []string{"registered users", "login email", "primary key"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the object browser is missing the comment %q:\n%s", want, view)
		}
	}
}

func TestObjectBrowserSchemaTwistyFoldsTables(t *testing.T) {
	m := sized("")
	idx := schemaRowIndex(m, "public")
	if idx < 0 {
		t.Fatal("no public schema in the tree")
	}
	row := objectRowByIndex(t, m, idx)
	r := m.paneRects()
	x := r.objects.X + 2 + row.indent*2
	y := objectRowY(m, idx)

	m = send(m, mousePress(x, y))
	if !m.objects.collapsed["public"] {
		t.Fatal("clicking the schema twisty must fold it")
	}
	if objectRowIndex(m, "users") >= 0 {
		t.Fatal("a folded schema must hide its tables")
	}

	m = send(m, mousePress(x, y))
	if m.objects.collapsed["public"] {
		t.Fatal("clicking the schema twisty again must unfold it")
	}
	if objectRowIndex(m, "users") < 0 {
		t.Fatal("an unfolded schema must show its tables")
	}
}

func TestObjectBrowserKeyboardUnfoldsTable(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = objectRowIndex(m, "users")
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})

	if !m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("the right arrow must unfold the selected table")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("the left arrow must fold the selected table")
	}
}

func TestObjectCommentIsOneSafeLine(t *testing.T) {
	got := oneLine(" first line\nsecond\tline\x1b[31m")
	if got != "first line second line [31m" {
		t.Fatalf("oneLine = %q", got)
	}
}

func TestObjectBrowserSchemasStartCollapsed(t *testing.T) {
	m := newTestModel("")
	// newTestModel opens the tree for the other tests; put it back in the
	// production startup state to verify the default.
	m.resetObjects()
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)

	rows := m.objectsRows()
	var schemas, tables int
	for _, row := range rows {
		switch row.kind {
		case rowSchema:
			schemas++
		case rowTable, rowColumn:
			tables++
		}
	}
	if schemas == 0 {
		t.Fatal("opening a connection must show its schema names")
	}
	if tables != 0 {
		t.Fatalf("schemas must start folded, but %d table/column rows are visible", tables)
	}
	if !m.objects.collapsed["public"] {
		t.Fatal("the public schema must start folded")
	}
}

func TestObjectBrowserStartupFoldsTheConnection(t *testing.T) {
	d := dialect.MustGet("postgres")
	m := New(Config{
		ConnName:       "demo",
		Dialect:        d,
		Provider:       catalog.Static{C: catalog.DemoCatalog()},
		Translator:     i18n.New(i18n.En),
		InitialCatalog: catalog.DemoCatalog(),
	})
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})

	if m.objects.expanded[connKey("demo")] {
		t.Fatal("the active connection must start folded")
	}
	for _, row := range m.objectsRows() {
		if row.kind == rowSchema || row.kind == rowTable || row.kind == rowColumn {
			t.Fatalf("a folded connection must hide its metadata, got %+v", row)
		}
	}

	// Saved connections start folded too, not just the active one.
	m.settings.Connections = append(m.settings.Connections,
		settings.Connection{Name: "analytics", Driver: "postgres"})
	m.resetObjects()
	if m.objects.expanded[connKey("analytics")] {
		t.Fatal("a saved connection must start folded")
	}
	var connections int
	for _, row := range m.objectsRows() {
		if row.kind != rowConn {
			t.Fatalf("a folded saved connection must not show metadata, got %+v", row)
		}
		connections++
	}
	if connections != 2 {
		t.Fatalf("connection rows = %d, want 2", connections)
	}
}

// findObjectRow returns the first row matching kind and a name-like field.
func findObjectRow(t *testing.T, m Model, kind rowKind, name string) (int, objectsRow) {
	t.Helper()
	for i, row := range m.objectsRows() {
		switch kind {
		case rowGroup:
			if row.kind == kind && row.group.String() == name {
				return i, row
			}
		case rowResource:
			if row.kind == kind && row.resource != nil && row.resource.Name == name {
				return i, row
			}
		case rowTable:
			if row.kind == kind && row.table != nil && row.table.Name == name {
				return i, row
			}
		}
	}
	t.Fatalf("object row kind=%d name=%q not found", kind, name)
	return 0, objectsRow{}
}

func TestObjectBrowserShowsResourceGroups(t *testing.T) {
	m := sized("")
	groups := map[catalog.ResourceKind]bool{}
	for _, row := range m.objectsRows() {
		if row.kind == rowGroup {
			groups[row.group] = true
		}
	}
	for _, kind := range []catalog.ResourceKind{
		catalog.KindTable, catalog.KindView, catalog.KindMaterializedView,
		catalog.KindFunction, catalog.KindProcedure, catalog.KindTrigger,
		catalog.KindIndex, catalog.KindSequence,
	} {
		if !groups[kind] {
			t.Errorf("the demo catalog should show a %s group", kind)
		}
	}

	_, row := findObjectRow(t, m, rowGroup, string(catalog.KindFunction))
	label := stripANSI(m.renderObjectRow(row, -1, 100))
	if !strings.Contains(label, "Functions (2)") {
		t.Fatalf("functions group label = %q, want a count", label)
	}

	for name, want := range map[string]catalog.ResourceKind{
		"order_total":        catalog.KindFunction,
		"archive_orders":     catalog.KindProcedure,
		"users_audit":        catalog.KindTrigger,
		"idx_orders_user_id": catalog.KindIndex,
		"order_number_seq":   catalog.KindSequence,
	} {
		_, resourceRow := findObjectRow(t, m, rowResource, name)
		if got := resourceRow.resource.Kind; got != want {
			t.Errorf("%s kind = %s, want %s", name, got, want)
		}
		line := stripANSI(m.renderObjectRow(resourceRow, -1, 120))
		if !strings.Contains(line, name) {
			t.Errorf("rendered row for %s = %q", name, line)
		}
	}
}

func TestObjectBrowserFoldsResourceGroups(t *testing.T) {
	m := sized("")
	idx, row := findObjectRow(t, m, rowGroup, string(catalog.KindFunction))
	before := len(m.objectsRows())
	if !m.objectsOpen(row) {
		t.Fatal("newTestModel opens resource groups for tests")
	}
	m.layout.focus = paneObjects
	m.objects.sel = idx
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	after := len(m.objectsRows())
	if after >= before {
		t.Fatalf("folding functions must remove rows: before=%d after=%d", before, after)
	}
	for _, r := range m.objectsRows() {
		if r.kind == rowResource && r.schema == "public" && r.resource.Kind == catalog.KindFunction {
			t.Fatalf("a folded functions group still shows %q", r.resource.Name)
		}
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := len(m.objectsRows()); got != before {
		t.Fatalf("reopening functions restored %d rows, want %d", got, before)
	}
}

func TestObjectBrowserHidesUnsupportedResourceKinds(t *testing.T) {
	m := sized("")
	// Pretend this catalog was produced by a database that only has tables and
	// views, even though the snapshot still contains demo resources.
	m.cat.Capabilities = catalog.Capable(catalog.KindTable, catalog.KindView)
	for _, row := range m.objectsRows() {
		if row.kind == rowGroup {
			if row.group != catalog.KindTable && row.group != catalog.KindView {
				t.Fatalf("unsupported group %s is visible", row.group)
			}
		}
		if row.kind == rowResource {
			t.Fatalf("unsupported resource %q is visible", row.resource.Name)
		}
	}
}

func TestObjectBrowserInsertsResourceName(t *testing.T) {
	m := sized("")
	_, row := findObjectRow(t, m, rowResource, "order_total")
	m.insertObjectName(row)
	if got := m.buf.Text(); got != "order_total" {
		t.Fatalf("inserted text = %q, want the function name", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatal("inserting a resource must focus the editor")
	}
}

func TestObjectBrowserResourceGroupsStartCollapsed(t *testing.T) {
	m := sized("")
	m.resetObjects()
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "public"}, true)

	var groups, members int
	for _, row := range m.objectsRows() {
		switch row.kind {
		case rowGroup:
			groups++
			if m.objectsOpen(row) {
				t.Fatalf("group %s must start folded", row.group)
			}
		case rowTable, rowResource, rowColumn:
			members++
		}
	}
	if groups == 0 {
		t.Fatal("opening a schema must show its resource groups")
	}
	if members != 0 {
		t.Fatalf("folded groups must not show members, found %d", members)
	}
}

// ---- Incremental filtering ("/") ----

// objectFilterStart focuses the object tree and presses "/" to open the filter
// input, asserting that it opened with an empty query.
func objectFilterStart(t *testing.T, m Model) Model {
	t.Helper()
	m.layout.focus = paneObjects
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.objects.filtering {
		t.Fatal("`/` must open the object filter")
	}
	if m.objects.filter != "" {
		t.Fatalf("filter = %q, entering must start from an empty query", m.objects.filter)
	}
	return m
}

// objectRowSignatures flattens rows into comparable names, so a test can prove
// that a cleared filter grew the tree back into exactly what it was.
func objectRowSignatures(rows []objectsRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		switch row.kind {
		case rowConn:
			out = append(out, fmt.Sprintf("conn:%d", row.conn))
		case rowSchema:
			out = append(out, "schema:"+row.schema)
		case rowGroup:
			out = append(out, "group:"+row.schema+"."+row.group.String())
		case rowTable:
			out = append(out, "table:"+row.schema+"."+row.table.Name)
		case rowColumn:
			out = append(out, "column:"+row.schema+"."+row.table.Name+"."+row.column.Name)
		case rowResource:
			out = append(out, "resource:"+row.schema+"."+row.resource.Kind.String()+"."+row.resource.Name)
		default:
			out = append(out, "none")
		}
	}
	return out
}

// copyBoolMap snapshots a collapse/expand map: filtering must never write to
// it, so the test compares a copy instead of the live map.
func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// hasTableRow / hasColumnRow / hasResourceRow / hasGroupRow look rows up by
// identity, which is what "this object survived the filter" means.
func hasTableRow(rows []objectsRow, schema, table string) bool {
	for _, row := range rows {
		if row.kind == rowTable && row.schema == schema && row.table.Name == table {
			return true
		}
	}
	return false
}

func hasColumnRow(rows []objectsRow, schema, table, column string) bool {
	for _, row := range rows {
		if row.kind == rowColumn && row.schema == schema &&
			row.table.Name == table && row.column.Name == column {
			return true
		}
	}
	return false
}

func hasResourceRow(rows []objectsRow, name string) bool {
	for _, row := range rows {
		if row.kind == rowResource && row.resource.Name == name {
			return true
		}
	}
	return false
}

func hasGroupRow(rows []objectsRow, schema string, kind catalog.ResourceKind) bool {
	for _, row := range rows {
		if row.kind == rowGroup && row.schema == schema && row.group == kind {
			return true
		}
	}
	return false
}

// countObjectRows counts rows of one kind, optionally limited to one schema.
func countObjectRows(rows []objectsRow, kind rowKind, schema string) int {
	var n int
	for _, row := range rows {
		if row.kind == kind && (schema == "" || row.schema == schema) {
			n++
		}
	}
	return n
}

func TestObjectBrowserFilterFindsTablesAndAncestors(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")

	// The input belongs to the tree: nothing reaches the editor behind it.
	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter typing leaked into the editor: %q", got)
	}

	rows := m.objectsRows()
	if !hasTableRow(rows, "public", "order_items") {
		t.Fatalf("the filtered table is missing from %v", objectRowSignatures(rows))
	}
	if schemaRowIndex(m, "public") < 0 {
		t.Fatal("a matched table must keep its schema on the way")
	}
	if !hasGroupRow(rows, "public", catalog.KindTable) {
		t.Fatal("a matched table must keep its resource group on the way")
	}
	if hasTableRow(rows, "public", "users") {
		t.Fatal("an unmatched table must stay hidden")
	}
	for _, row := range rows {
		if row.kind == rowSchema && row.schema != "public" {
			t.Fatalf("unmatched schema %q is visible", row.schema)
		}
	}

	// The query is visible in the pane title while it is being typed.
	view := stripANSI(m.View())
	if !strings.Contains(view, "/order_items") || !strings.Contains(view, "▏") {
		t.Fatalf("the filter must show in the objects title:\n%s", view)
	}
}

func TestObjectBrowserFilterFindsColumnsWithTheirTable(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "full_name")

	rows := m.objectsRows()
	if !hasColumnRow(rows, "public", "users", "full_name") {
		t.Fatalf("the matched column is missing from %v", objectRowSignatures(rows))
	}
	if !hasTableRow(rows, "public", "users") {
		t.Fatal("a matched column must show its owning table")
	}
	if hasTableRow(rows, "public", "orders") {
		t.Fatal("an unmatched table must stay hidden")
	}
	for _, row := range rows {
		if row.kind == rowColumn &&
			!(row.table.Name == "users" && row.column.Name == "full_name") {
			t.Fatalf("unexpected column %s.%s in the filtered tree", row.table.Name, row.column.Name)
		}
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter typing leaked into the editor: %q", got)
	}
}

func TestObjectBrowserFilterFindsFunctionsBySignatureAndGroup(t *testing.T) {
	m := sized("")

	// A fragment of the signature (the Detail of the function).
	m = objectFilterStart(t, m)
	m = typeText(m, "bigint")
	rows := m.objectsRows()
	if !hasResourceRow(rows, "order_total") {
		t.Fatalf("a function matching on its signature is missing: %v", objectRowSignatures(rows))
	}
	if hasResourceRow(rows, "user_status_label") {
		t.Fatal("an unmatched function must stay hidden")
	}
	if countObjectRows(rows, rowTable, "") != 0 {
		t.Fatal("the signature fragment must not surface any table")
	}

	// The group label (here: the English one).
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "Functions")
	rows = m.objectsRows()
	if !hasResourceRow(rows, "order_total") || !hasResourceRow(rows, "refresh_daily_sales") {
		t.Fatalf("a matched group must show all of its functions: %v", objectRowSignatures(rows))
	}
	if countObjectRows(rows, rowTable, "") != 0 {
		t.Fatal("the Functions group must not surface any table")
	}

	// The stable English kind word, which also covers a localized UI.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "materialized_view")
	rows = m.objectsRows()
	if !hasTableRow(rows, "reporting", "mv_sales_daily") {
		t.Fatalf("the kind word must match its group: %v", objectRowSignatures(rows))
	}
	if hasTableRow(rows, "reporting", "v_daily_sales") {
		t.Fatal("an unmatched group must stay hidden")
	}

	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter typing leaked into the editor: %q", got)
	}
}

func TestObjectBrowserFilterMatchesLocalizedGroupLabel(t *testing.T) {
	m := sized("")
	m.translator = i18n.New(i18n.ZhHans)
	m = objectFilterStart(t, m)
	m = typeText(m, "函数")

	rows := m.objectsRows()
	if !hasResourceRow(rows, "order_total") || !hasResourceRow(rows, "refresh_daily_sales") {
		t.Fatalf("the localized group label must match: %v", objectRowSignatures(rows))
	}
	if countObjectRows(rows, rowTable, "") != 0 {
		t.Fatal("the functions group must not surface any table")
	}
}

func TestObjectBrowserFilterEditingKeys(t *testing.T) {
	m := sized("")
	before := objectRowSignatures(m.objectsRows())
	m = objectFilterStart(t, m)

	// Backspace removes one rune and the tree follows.
	m = typeText(m, "usersx")
	if got := countObjectRows(m.objectsRows(), rowTable, ""); got != 0 {
		t.Fatalf("usersx must match nothing, found %d tables", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, want users after Backspace", m.objects.filter)
	}
	if objectRowIndex(m, "users") < 0 {
		t.Fatal("Backspace must bring the rows back")
	}

	// Ctrl+U clears the text but keeps the input open.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.objects.filter != "" {
		t.Fatalf("filter = %q, Ctrl+U must clear it", m.objects.filter)
	}
	if !m.objects.filtering {
		t.Fatal("Ctrl+U must not close the filter input")
	}
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, before) {
		t.Fatal("clearing the filter must restore the original tree")
	}

	// Enter keeps the query and leaves the input.
	m = typeText(m, "users")
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.objects.filtering {
		t.Fatal("Enter must close the filter input")
	}
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, Enter must keep the query", m.objects.filter)
	}
	if objectRowIndex(m, "users") < 0 {
		t.Fatal("the filter must stay in effect after Enter")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "/users") {
		t.Fatalf("the query must stay visible in the title:\n%s", view)
	}
	if strings.Contains(view, "▏") {
		t.Fatalf("the caret belongs to the open input only:\n%s", view)
	}

	// A new "/" starts over with an empty query.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.objects.filtering || m.objects.filter != "" {
		t.Fatalf("filter = %q, filtering = %v, want a fresh empty input",
			m.objects.filter, m.objects.filtering)
	}

	// Esc clears the query and leaves the input.
	m = typeText(m, "zzz")
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.objects.filtering || m.objects.filter != "" {
		t.Fatalf("filter = %q, filtering = %v, Esc must clear both",
			m.objects.filter, m.objects.filtering)
	}
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, before) {
		t.Fatal("Esc must restore the original tree")
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter keys leaked into the editor: %q", got)
	}
}

func TestObjectBrowserFilterNavigationSwallowsTreeKeys(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "order")
	rows := m.objectsRows()
	if len(rows) < 3 {
		t.Fatalf("precondition failed: %d rows for the query", len(rows))
	}

	// Up/down still move the selection, and it always stays on screen.
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	sel := m.objects.sel
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.objects.sel != sel+1 {
		t.Fatalf("sel = %d, want %d", m.objects.sel, sel+1)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.objects.sel != len(m.objectsRows())-1 {
		t.Fatalf("End must jump to the last filtered row, sel = %d", m.objects.sel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.objects.sel != 0 {
		t.Fatalf("Home must jump to the first filtered row, sel = %d", m.objects.sel)
	}
	if m.objects.sel < m.objects.scroll ||
		m.objects.sel >= m.objects.scroll+m.objectsBodyHeight() {
		t.Fatalf("sel = %d out of view (scroll = %d, height = %d)",
			m.objects.sel, m.objects.scroll, m.objectsBodyHeight())
	}

	// Fold keys are swallowed: filtering never writes the collapse maps.
	expanded := copyBoolMap(m.objects.expanded)
	collapsed := copyBoolMap(m.objects.collapsed)
	before := objectRowSignatures(m.objectsRows())
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if !m.objects.filtering {
		t.Fatal("left/right must not leave the filter input")
	}
	if !slices.Equal(objectRowSignatures(m.objectsRows()), before) {
		t.Fatal("fold keys must not change the filtered tree")
	}
	if !maps.Equal(expanded, m.objects.expanded) || !maps.Equal(collapsed, m.objects.collapsed) {
		t.Fatal("filtering must not write the collapse maps")
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter keys leaked into the editor: %q", got)
	}
}

func TestObjectBrowserFilterShowsNoMatchHint(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "zzz_nothing")

	rows := m.objectsRows()
	if len(rows) != len(m.connectionList()) {
		t.Fatalf("only the connection rows may remain, got %v", objectRowSignatures(rows))
	}
	if rows[0].kind != rowConn {
		t.Fatalf("the connection row must stay first, got %+v", rows[0])
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "No matching objects") {
		t.Fatalf("an empty filter must say so:\n%s", view)
	}
	if !strings.Contains(view, "/zzz_nothing") {
		t.Fatalf("the query must stay visible in the title:\n%s", view)
	}
}

func TestObjectBrowserFilterLeavesExpansionStateAlone(t *testing.T) {
	m := sized("")
	// Fold a schema and a group so the stored state is not the trivial one.
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "public"}, false)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "reporting", group: catalog.KindTable}, false)
	before := objectRowSignatures(m.objectsRows())
	beforeExpanded := copyBoolMap(m.objects.expanded)
	beforeCollapsed := copyBoolMap(m.objects.collapsed)
	if objectRowIndex(m, "users") >= 0 {
		t.Fatal("precondition failed: the folded schema must hide its tables")
	}

	// The filter digs the table out of the folded schema…
	m = objectFilterStart(t, m)
	m = typeText(m, "users")
	if objectRowIndex(m, "users") < 0 {
		t.Fatal("the filter must reach into a folded schema")
	}
	if !maps.Equal(beforeExpanded, m.objects.expanded) ||
		!maps.Equal(beforeCollapsed, m.objects.collapsed) {
		t.Fatal("filtering must not write the collapse maps")
	}

	// … and clearing it puts the tree back exactly as it was.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, before) {
		t.Fatalf("clearing the filter must restore the tree:\ngot  %v\nwant %v",
			got, before)
	}
	if !maps.Equal(beforeExpanded, m.objects.expanded) ||
		!maps.Equal(beforeCollapsed, m.objects.collapsed) {
		t.Fatal("clearing the filter must not write the collapse maps")
	}
}

func TestObjectBrowserFilterClampsSelection(t *testing.T) {
	m := sized("")
	rows := m.objectsRows()
	m.objects.sel = len(rows) - 1 // start at the very bottom of the tree
	m.objects.scroll = 0

	m = objectFilterStart(t, m)
	m = typeText(m, "refresh_daily_sales")

	filtered := m.objectsRows()
	if len(filtered) >= len(rows) {
		t.Fatalf("precondition failed: the filter did not shrink the tree (%d >= %d)",
			len(filtered), len(rows))
	}
	if m.objects.sel < 0 || m.objects.sel >= len(filtered) {
		t.Fatalf("sel = %d out of range for %d filtered rows", m.objects.sel, len(filtered))
	}
	if m.objects.sel < m.objects.scroll ||
		m.objects.sel >= m.objects.scroll+m.objectsBodyHeight() {
		t.Fatalf("sel = %d out of view (scroll = %d, height = %d)",
			m.objects.sel, m.objects.scroll, m.objectsBodyHeight())
	}
}

func TestObjectBrowserFilterClickEndsInputAndKeepsText(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "users")

	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("precondition failed: no users row in the filtered tree")
	}
	r := m.paneRects()
	x, y := r.objects.X+20, objectRowY(m, idx) // the label, not the twisty
	m = send(m, mousePress(x, y))

	if m.objects.filtering {
		t.Fatal("clicking a tree row must end the filter input")
	}
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, the click must keep the query", m.objects.filter)
	}
	if m.objects.sel != idx {
		t.Fatalf("sel = %d, want the clicked row %d", m.objects.sel, idx)
	}

	// The hit test runs on the filtered rows, so a double click on the visible
	// row acts on exactly that row — a relation opens its data now.
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, a filtered table double click must open its data", len(m.tabs))
	}
	if got := m.buf.Text(); !strings.Contains(got, `SELECT * FROM "public"."users" LIMIT 200;`) {
		t.Fatalf("double click opened %q, want the visible row's data", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatal("the data tab must take the focus")
	}
}

func TestObjectBrowserFilterRightClickEndsInputAndOpensMenu(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")

	idx := objectRowIndex(m, "order_items")
	if idx < 0 {
		t.Fatal("precondition failed: no order_items row in the filtered tree")
	}
	m = rightClickObjectsRow(m, idx)

	if m.objects.filtering {
		t.Fatal("right-clicking a tree row must end the filter input")
	}
	if m.objects.filter != "order_items" {
		t.Fatalf("filter = %q, the right click must keep the query", m.objects.filter)
	}
	if !m.ctx.open {
		t.Fatal("a right click must open the row's context menu")
	}
	if m.objects.sel != idx {
		t.Fatalf("sel = %d, want the right-clicked row %d", m.objects.sel, idx)
	}
}

func TestObjectBrowserFilterGroupCountMatchesVisibleRows(t *testing.T) {
	m := sized("")
	_, group := findObjectRow(t, m, rowGroup, string(catalog.KindTable))
	if label := stripANSI(m.renderObjectRow(group, -1, 120)); !strings.Contains(label, "Tables (6)") {
		t.Fatalf("unfiltered group label = %q, want the total", label)
	}

	m = objectFilterStart(t, m)
	m = typeText(m, "order")

	_, filtered := findObjectRow(t, m, rowGroup, string(catalog.KindTable))
	label := stripANSI(m.renderObjectRow(filtered, -1, 120))
	if !strings.Contains(label, "Tables (3)") {
		t.Fatalf("filtered group label = %q, want the 3 visible tables", label)
	}
	if visible := countObjectRows(m.objectsRows(), rowTable, "public"); visible != 3 {
		t.Fatalf("visible public tables = %d, want 3", visible)
	}
	if filtered.count != 3 {
		t.Fatalf("group count = %d, want the number of visible members", filtered.count)
	}
}

// TestObjectBrowserFilterSwallowsGlobalShortcuts proves the filter input owns
// the keyboard completely: no shortcut, menu accelerator or focus key may slip
// through while a query is half-typed.
func TestObjectBrowserFilterSwallowsGlobalShortcuts(t *testing.T) {
	m := sized("SELECT 1 FROM users")
	m = objectFilterStart(t, m)
	m = typeText(m, "use")

	// Ctrl+A would select the whole buffer and the next typed character would
	// replace the SQL behind the input's back.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, Ctrl+A must not reach the editor while filtering", m.layout.focus)
	}
	if m.hasSelection() {
		t.Fatal("Ctrl+A must not select the editor buffer while filtering")
	}

	// Ctrl+X (cut) must not touch the buffer either.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if got := m.buf.Text(); got != "SELECT 1 FROM users" {
		t.Fatalf("editor text = %q, Ctrl+X must not cut while filtering", got)
	}

	// Ctrl+B (toggle the objects pane) is a global shortcut as well.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !m.layout.objectsVisible {
		t.Fatal("Ctrl+B must not hide the objects pane while filtering")
	}

	// Alt+2 (focus the results pane) must not steal the focus mid-word; the
	// same holds for Alt+1/Alt+3. Esc leaves the input first.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, Alt+2 must not steal the focus while filtering", m.layout.focus)
	}
	if !m.objects.filtering || m.objects.filter != "use" {
		t.Fatalf("filter = %q, filtering = %v, want the input still open",
			m.objects.filter, m.objects.filtering)
	}

	// Typing continues where it left off, and never into the editor.
	m = typeText(m, "rs")
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, want users", m.objects.filter)
	}
	if objectRowIndex(m, "users") < 0 {
		t.Fatal("the continued query must still filter the tree")
	}
	if got := m.buf.Text(); got != "SELECT 1 FROM users" {
		t.Fatalf("filter keys leaked into the editor: %q", got)
	}
}

// TestObjectBrowserFilterClampsScrollToShrunkTree is the regression test for
// a scroll offset that was valid on the full tree but parks the few surviving
// rows of a filter above the viewport: after filtering, the top of the (short)
// tree must be on screen again.
func TestObjectBrowserFilterClampsScrollToShrunkTree(t *testing.T) {
	m := sized("")
	rows := m.objectsRows()
	m.objects.sel = len(rows) - 1
	m.objects.scroll = len(rows) // browsed far down before filtering

	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")

	filtered := m.objectsRows()
	if len(filtered) >= len(rows) {
		t.Fatalf("precondition failed: the filter did not shrink the tree (%d >= %d)",
			len(filtered), len(rows))
	}
	visible := m.objectsBodyHeight()
	if len(filtered) > visible {
		t.Fatalf("precondition failed: %d filtered rows fit in %d lines, so no scroll bound is exercised",
			len(filtered), visible)
	}
	if maxScroll := max(0, len(filtered)-visible); m.objects.scroll > maxScroll {
		t.Fatalf("scroll = %d exceeds the tree's maximum of %d (rows = %d, visible = %d)",
			m.objects.scroll, maxScroll, len(filtered), visible)
	}
	if m.objects.scroll != 0 {
		t.Fatalf("scroll = %d, want 0 so the first surviving row stays visible", m.objects.scroll)
	}
	if m.objects.sel < m.objects.scroll ||
		m.objects.sel >= m.objects.scroll+visible {
		t.Fatalf("sel = %d out of view (scroll = %d, height = %d)",
			m.objects.sel, m.objects.scroll, visible)
	}
}

// TestObjectBrowserFilterTwistyFollowsVisibleChildren pins the twisty to the
// rows that are actually on screen while a filter is active: a node whose
// children are listed must render open, whatever the stored expansion state
// says, and a node without children below it must render closed.
func TestObjectBrowserFilterTwistyFollowsVisibleChildren(t *testing.T) {
	m := sized("")
	// Stored state: the schema is folded, no table is open. The filter shows
	// their children anyway, so the maps alone would draw the wrong twisty.
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "public"}, false)
	if m.objectsOpen(objectsRow{kind: rowSchema, schema: "public"}) {
		t.Fatal("precondition failed: the public schema must be folded")
	}

	m = objectFilterStart(t, m)
	m = typeText(m, "users")

	ic := m.icons()
	rows := m.objectsRows()
	var checked int
	for i, row := range rows {
		if !m.objectsCanToggle(row) {
			continue
		}
		children := i+1 < len(rows) && rows[i+1].indent > row.indent
		if got := m.objectsOpen(row); got != children {
			t.Fatalf("row %d (kind %d): objectsOpen = %v, but children follow = %v",
				i, row.kind, got, children)
		}
		line := stripANSI(m.renderObjectRow(row, i, 200))
		if children && !strings.Contains(line, ic.expandOpen) {
			t.Errorf("row %d lists children but renders a closed twisty: %q", i, line)
		}
		if !children && !strings.Contains(line, ic.expandClosed) {
			t.Errorf("row %d lists no children but renders an open twisty: %q", i, line)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("precondition failed: only %d toggleable rows for the query", checked)
	}
}

// TestObjectBrowserFilterFoldKeysDoNotPersist covers the keyboard fold entry
// points while a filter is in effect — Left/Right with the input open, Enter
// leaving the input, and Left/Right/Enter with the query still active. None of
// them may write the collapse maps.
func TestObjectBrowserFilterFoldKeysDoNotPersist(t *testing.T) {
	m := sized("")
	// A stored state that is not the trivial one: the schema is folded.
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "public"}, false)
	beforeExpanded := copyBoolMap(m.objects.expanded)
	beforeCollapsed := copyBoolMap(m.objects.collapsed)
	before := objectRowSignatures(m.objectsRows())

	// While the input is open: Left/Right are swallowed, Enter only leaves the
	// input (the query stays). The maps are checked after every single key —
	// a fold and its unfold must not be able to cancel each other out.
	m = objectFilterStart(t, m)
	m = typeText(m, "users")
	filtered := objectRowSignatures(m.objectsRows())
	if !hasTableRow(m.objectsRows(), "public", "users") {
		t.Fatal("precondition failed: the query must match the users table")
	}
	assertMapsIntact := func(when string) {
		t.Helper()
		if !maps.Equal(beforeExpanded, m.objects.expanded) ||
			!maps.Equal(beforeCollapsed, m.objects.collapsed) {
			t.Fatalf("%s wrote the collapse maps (expanded %v, collapsed %v)",
				when, m.objects.expanded, m.objects.collapsed)
		}
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	assertMapsIntact("Right")
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	assertMapsIntact("Left")
	if !m.objects.filtering {
		t.Fatal("left/right must not leave the filter input")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	assertMapsIntact("Enter")
	if m.objects.filtering {
		t.Fatal("Enter must leave the filter input")
	}
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, Enter must keep the query", m.objects.filter)
	}

	// Input closed, query still active: the tree is still a projection, so
	// Left/Right and Enter (fold) on the schema keep their hands off the maps.
	schemaIdx := schemaRowIndex(m, "public")
	if schemaIdx < 0 {
		t.Fatal("precondition failed: no public schema in the filtered tree")
	}
	m.objects.sel = schemaIdx
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	assertMapsIntact("Left with the query active")
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	assertMapsIntact("Right with the query active")
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	assertMapsIntact("Enter with the query active")
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, filtered) {
		t.Fatalf("the filtered tree changed:\ngot  %v\nwant %v", got, filtered)
	}

	// Clearing the query puts the tree back exactly as it was.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.objects.filtering || m.objects.filter != "" {
		t.Fatalf("filter = %q, filtering = %v, want both cleared",
			m.objects.filter, m.objects.filtering)
	}
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, before) {
		t.Fatalf("clearing the filter must restore the tree:\ngot  %v\nwant %v", got, before)
	}
	if !maps.Equal(beforeExpanded, m.objects.expanded) ||
		!maps.Equal(beforeCollapsed, m.objects.collapsed) {
		t.Fatal("clearing the filter must not write the collapse maps")
	}
}

// TestObjectBrowserFilterTwistyClickDoesNotPersist is the pointer twin of the
// fold keys: clicking a twisty with the input open ends the input (the query
// stays), must not fold anything while the query is active, and clearing the
// query afterwards restores the tree exactly as it was.
func TestObjectBrowserFilterTwistyClickDoesNotPersist(t *testing.T) {
	m := sized("")
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "public"}, false)
	beforeExpanded := copyBoolMap(m.objects.expanded)
	beforeCollapsed := copyBoolMap(m.objects.collapsed)
	before := objectRowSignatures(m.objectsRows())

	m = objectFilterStart(t, m)
	m = typeText(m, "users")
	idx := schemaRowIndex(m, "public")
	if idx < 0 {
		t.Fatal("precondition failed: no public schema in the filtered tree")
	}
	row := objectRowByIndex(t, m, idx)
	r := m.paneRects()
	x := r.objects.X + 2 + row.indent*2
	y := objectRowY(m, idx)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))

	if m.objects.filtering {
		t.Fatal("clicking a tree row must end the filter input")
	}
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, the click must keep the query", m.objects.filter)
	}
	if !maps.Equal(beforeExpanded, m.objects.expanded) ||
		!maps.Equal(beforeCollapsed, m.objects.collapsed) {
		t.Fatal("clicking the twisty while filtering must not write the collapse maps")
	}

	// Clearing the query puts the tree back exactly as it was.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.objects.filtering || m.objects.filter != "" {
		t.Fatalf("filter = %q, filtering = %v, want both cleared",
			m.objects.filter, m.objects.filtering)
	}
	if got := objectRowSignatures(m.objectsRows()); !slices.Equal(got, before) {
		t.Fatalf("clearing the filter must restore the tree:\ngot  %v\nwant %v", got, before)
	}
	if !maps.Equal(beforeExpanded, m.objects.expanded) ||
		!maps.Equal(beforeCollapsed, m.objects.collapsed) {
		t.Fatal("clearing the filter must not write the collapse maps")
	}
}

// TestEnsureObjectsVisibleOnlyScrolls guards against side effects creeping
// back into the scroll helper: ending the filter input belongs to the keys and
// pointer actions that pick a row, not to scrolling.
func TestEnsureObjectsVisibleOnlyScrolls(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")
	if !m.objects.filtering {
		t.Fatal("precondition failed: the filter input must be open")
	}
	m.ensureObjectsVisible(m.objectsBodyHeight())
	if !m.objects.filtering {
		t.Fatal("ensureObjectsVisible must be a pure scroll and not end the filter input")
	}
	if m.objects.filter != "order_items" {
		t.Fatalf("filter = %q, scrolling must not touch the query", m.objects.filter)
	}
}

// TestObjectBrowserFilterMatchesComments checks that comments are searchable:
// they often say what the name cannot ("registered users").
func TestObjectBrowserFilterMatchesComments(t *testing.T) {
	m := sized("")

	// A table found by its comment opens with its columns (it matched itself).
	m = objectFilterStart(t, m)
	m = typeText(m, "registered users")
	rows := m.objectsRows()
	if !hasTableRow(rows, "public", "users") {
		t.Fatalf("a table must be found by its comment: %v", objectRowSignatures(rows))
	}
	if got := countObjectRows(rows, rowTable, ""); got != 1 {
		t.Fatalf("matched tables = %d, want only users: %v", got, objectRowSignatures(rows))
	}
	if !hasColumnRow(rows, "public", "users", "id") {
		t.Fatal("a table matched by its comment must open its columns")
	}

	// A column found by its comment: only that column shows, under its table.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "display name")
	rows = m.objectsRows()
	if !hasColumnRow(rows, "public", "users", "full_name") {
		t.Fatalf("a column must be found by its comment: %v", objectRowSignatures(rows))
	}
	if got := countObjectRows(rows, rowTable, ""); got != 1 {
		t.Fatalf("matched tables = %d, want only users: %v", got, objectRowSignatures(rows))
	}
	for _, row := range rows {
		if row.kind == rowColumn &&
			!(row.table.Name == "users" && row.column.Name == "full_name") {
			t.Fatalf("unexpected column %s.%s in the filtered tree", row.table.Name, row.column.Name)
		}
	}

	// Both matchers stay nil-safe for catalogs without these objects.
	if tableFilterMatch(nil, "x") || columnFilterMatch(nil, "x") {
		t.Fatal("matching against nil must not report a hit")
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("filter typing leaked into the editor: %q", got)
	}
}

// TestObjectBrowserFilterBroadHitsStayOneLevel pins the noise rule: a schema
// or group hit lists its members (tables, resources) but not their columns;
// columns appear when a table itself matched or when the column matched.
func TestObjectBrowserFilterBroadHitsStayOneLevel(t *testing.T) {
	m := sized("")

	// Schema hit: every member of "public" shows, no column row at all.
	m = objectFilterStart(t, m)
	m = typeText(m, "public")
	rows := m.objectsRows()
	if schemaRowIndex(m, "public") < 0 {
		t.Fatal("the matched schema must be visible")
	}
	if got := countObjectRows(rows, rowTable, ""); got == 0 {
		t.Fatal("a matched schema must show its tables")
	}
	if got := countObjectRows(rows, rowColumn, ""); got != 0 {
		t.Fatalf("a schema hit must stay one level deep, found %d column rows", got)
	}

	// Group hit: the same rule.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "Tables")
	rows = m.objectsRows()
	if got := countObjectRows(rows, rowTable, ""); got == 0 {
		t.Fatal("a matched group must show its tables")
	}
	if got := countObjectRows(rows, rowColumn, ""); got != 0 {
		t.Fatalf("a group hit must stay one level deep, found %d column rows", got)
	}

	// A table that matched itself still opens completely, and a matched column
	// still shows up under its table.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")
	rows = m.objectsRows()
	if got := countObjectRows(rows, rowColumn, "public"); got == 0 {
		t.Fatal("a table matched by name must open its columns")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = objectFilterStart(t, m)
	m = typeText(m, "unit_price")
	rows = m.objectsRows()
	if !hasColumnRow(rows, "public", "order_items", "unit_price") {
		t.Fatalf("a matched column must show: %v", objectRowSignatures(rows))
	}
	if got := countObjectRows(rows, rowColumn, ""); got != 1 {
		t.Fatalf("a lone column match must not open other columns, found %d", got)
	}
}

// TestObjectBrowserFilterSelectsFirstMatchAfterShrink: when the row list
// shrinks under the caret, the caret lands on the first object instead of
// being pushed to the last surviving row.
func TestObjectBrowserFilterSelectsFirstMatchAfterShrink(t *testing.T) {
	m := sized("")
	rows := m.objectsRows()
	m.objects.sel = len(rows) - 1 // caret at the very bottom of the full tree

	m = objectFilterStart(t, m)
	m = typeText(m, "order_items")

	filtered := m.objectsRows()
	if len(filtered) >= len(rows) {
		t.Fatalf("precondition failed: the filter did not shrink the tree (%d >= %d)",
			len(filtered), len(rows))
	}
	if m.objects.sel < 0 || m.objects.sel >= len(filtered) {
		t.Fatalf("sel = %d out of range for %d filtered rows", m.objects.sel, len(filtered))
	}
	if filtered[m.objects.sel].kind == rowConn {
		t.Fatal("the caret must skip the connection rows")
	}
	if filtered[m.objects.sel].kind != rowSchema || filtered[m.objects.sel].schema != "public" {
		t.Fatalf("sel = %d points at %+v, want the first object (the public schema)",
			m.objects.sel, filtered[m.objects.sel])
	}
	if m.objects.sel < m.objects.scroll ||
		m.objects.sel >= m.objects.scroll+m.objectsBodyHeight() {
		t.Fatalf("sel = %d out of view (scroll = %d, height = %d)",
			m.objects.sel, m.objects.scroll, m.objectsBodyHeight())
	}

	// A caret that is still valid is kept: shortening the query must not jump
	// the selection somewhere else.
	m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.objectsRows(); m.objects.sel >= len(got) {
		t.Fatalf("sel = %d out of range for %d rows after one Backspace", m.objects.sel, len(got))
	} else if row := got[m.objects.sel]; row.kind != rowSchema || row.schema != "public" {
		t.Fatalf("sel = %d points at %+v after Backspace, want it to stay on the public schema",
			m.objects.sel, row)
	}
}

// TestObjectBrowserFilterPaste pins the bracketed-paste path: one KeyMsg with
// Paste set carries the whole text, whose String() is not the text itself, so
// it must be inserted as-is like in the results grid.
func TestObjectBrowserFilterPaste(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("public.users"), Paste: true})
	if !m.objects.filtering {
		t.Fatal("pasting must not close the filter input")
	}
	if m.objects.filter != "public.users" {
		t.Fatalf("filter = %q, want the pasted text", m.objects.filter)
	}
	rows := m.objectsRows()
	if objectRowIndex(m, "users") < 0 {
		t.Fatalf("the pasted query must match users: %v", objectRowSignatures(rows))
	}
	if m.objects.sel < 0 || m.objects.sel >= len(rows) {
		t.Fatalf("sel = %d out of range (rows = %d)", m.objects.sel, len(rows))
	}
	if m.objects.sel < m.objects.scroll ||
		m.objects.sel >= m.objects.scroll+m.objectsBodyHeight() {
		t.Fatalf("sel = %d out of view (scroll = %d, height = %d)",
			m.objects.sel, m.objects.scroll, m.objectsBodyHeight())
	}

	// Ordinary keys keep their one-rune-at-a-time behavior.
	m = typeText(m, "x")
	if m.objects.filter != "public.usersx" {
		t.Fatalf("filter = %q, a typed key must still append one rune", m.objects.filter)
	}
}

// TestObjectBrowserFilterEmptyInputTwistyClicks covers a filter input that was
// just opened: an empty query is not a real filter, so the first click on a
// twisty must fold the node instead of only ending the input.
func TestObjectBrowserFilterEmptyInputTwistyClicks(t *testing.T) {
	m := sized("")
	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("precondition failed: no users row in the tree")
	}
	row := objectRowByIndex(t, m, idx)
	if m.objectsOpen(row) {
		t.Fatal("precondition failed: users must start folded")
	}

	m = objectFilterStart(t, m)
	idx = objectRowIndex(m, "users")
	row = objectRowByIndex(t, m, idx)
	r := m.paneRects()
	x := r.objects.X + 2 + row.indent*2
	y := objectRowY(m, idx)

	m = send(m, mousePress(x, y))
	if m.objects.filtering {
		t.Fatal("clicking a tree row must end the filter input")
	}
	if !m.objects.expanded[tableKey("public", "users")] {
		t.Fatal("the first twisty click must unfold the table")
	}
	if m.objects.filter != "" {
		t.Fatalf("filter = %q, an empty input must stay empty", m.objects.filter)
	}
}

// TestObjectBrowserHoverHighlightsRowWithoutSelecting pins the pointer
// semantics of the object tree (round 7 §1): motion highlights the row under
// the pointer without moving sel and without stealing the keyboard focus,
// leaving the row clears the highlight only, and a click still selects.
func TestObjectBrowserHoverHighlightsRowWithoutSelecting(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the highlight
	// style is really in the rendered row, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("")
	r := m.paneRects()
	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("no users table in the tree")
	}
	if idx >= m.objectsBodyHeight() {
		t.Fatalf("precondition: row %d is below the visible tree", idx)
	}
	selBefore := m.objects.sel
	if selBefore == idx {
		t.Fatalf("precondition: the row must start unselected, sel = %d", selBefore)
	}
	focusBefore := m.layout.focus
	y := objectRowY(m, idx)
	x := r.objects.X + 20

	// Motion onto the row highlights it and changes nothing else.
	m = send(m, mouseMotion(x, y))
	if m.objects.hover != idx {
		t.Fatalf("objects.hover = %d, want %d (the row under the pointer)", m.objects.hover, idx)
	}
	if m.objects.sel != selBefore {
		t.Fatalf("objects.sel = %d, hovering must not move the selection (%d)", m.objects.sel, selBefore)
	}
	if m.layout.focus != focusBefore {
		t.Fatalf("focus = %v, hovering must not steal the keyboard focus", m.layout.focus)
	}

	// The hovered row is drawn exactly like the selected one, and differently
	// from a row that is neither selected nor hovered.
	rows := m.objectsRows()
	width := r.objects.W
	hovered := m.renderObjectRow(rows[idx], idx, width)
	m.objects.hover = -1
	plain := m.renderObjectRow(rows[idx], idx, width)
	m.objects.sel = idx
	selected := m.renderObjectRow(rows[idx], idx, width)
	m.objects.sel, m.objects.hover = selBefore, idx
	if hovered != selected {
		t.Fatalf("the hovered row must be drawn like the selected one:\nhover:   %q\nselect: %q", hovered, selected)
	}
	if hovered == plain {
		t.Fatalf("the hovered row must be drawn differently from a plain one: %q", hovered)
	}

	// Leaving the row clears the highlight only.
	m = send(m, mouseMotion(r.objects.X+3, r.objects.Y)) // the pane title row
	if m.objects.hover != -1 {
		t.Fatalf("objects.hover = %d, want -1 away from the rows", m.objects.hover)
	}
	if m.objects.sel != selBefore {
		t.Fatalf("objects.sel = %d, want it unchanged (%d)", m.objects.sel, selBefore)
	}

	// A click still selects the row: hovering changed nothing about it.
	m = send(m, mousePress(x, y))
	if m.objects.sel != idx {
		t.Fatalf("objects.sel = %d, want %d after the click", m.objects.sel, idx)
	}
}

// TestObjectBrowserFilterPasteDropsControlCharacters pins the cleaning of a
// bracketed paste before it becomes the filter query: the pane title and the
// status bar draw the query as plain text, so an escape sequence in a pasted
// fragment must never make it into the stored text. Ordinary typing keeps
// working afterwards — the cleaned paste is still an ordinary query, and the
// input still appends rune by rune.
func TestObjectBrowserFilterPasteDropsControlCharacters(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes,
		Runes: []rune(seqClearScreen + "users"), Paste: true})
	if strings.ContainsRune(m.objects.filter, '\x1b') {
		t.Fatalf("filter = %q, want no escape character", m.objects.filter)
	}
	// Only the control characters drop: the printable residue of the
	// sequence stays, exactly like every other one-line input does it.
	if m.objects.filter != "[2Jusers" {
		t.Fatalf("filter = %q, want the cleaned pasted text", m.objects.filter)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}
	assertNoPayload(t, m.View(), "the pasted object filter")

	// Ordinary typing still appends to what was pasted, and the tree still
	// matches on the result.
	m = typeText(m, "2")
	if m.objects.filter != "[2Jusers2" {
		t.Fatalf("filter = %q, want the pasted text plus the typed rune", m.objects.filter)
	}
}

// TestObjectBrowserFilterUnmarkedPasteDropsControlCharacters pins the same
// rule for a paste that arrives without the bracketed-paste marker: it is not
// flagged, so it falls through to the plain-runes branch and has to be
// cleaned there as well.
func TestObjectBrowserFilterUnmarkedPasteDropsControlCharacters(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes,
		Runes: []rune(seqClearScreen + "users")})
	if strings.ContainsRune(m.objects.filter, '\x1b') {
		t.Fatalf("filter = %q, want no escape character", m.objects.filter)
	}
	if m.objects.filter != "[2Jusers" {
		t.Fatalf("filter = %q, want the cleaned pasted text", m.objects.filter)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}

	// A plain keystroke still reaches the input and stays visible.
	m = typeText(m, "!")
	if m.objects.filter != "[2Jusers!" {
		t.Fatalf("filter = %q, want the cleaned text plus the typed rune", m.objects.filter)
	}
}

// TestObjectBrowserInsertDropsControlCharacters: the name of a catalog
// object may carry escape sequences (PostgreSQL allows control characters
// inside a quoted identifier), and the editor draws its buffer verbatim — so
// inserting a name from the tree must write its cleaned form.
func TestObjectBrowserInsertDropsControlCharacters(t *testing.T) {
	m := sized("")
	name := "odd" + escPayload + "table"
	row := objectsRow{kind: rowTable, table: &catalog.Table{
		Schema: "public", Name: name, Type: catalog.TypeTable,
	}}

	m.insertObjectName(row)
	got := m.buf.Text()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the buffer = %q, want no escape character", got)
	}
	if want := "odd[31mXtable"; got != want {
		t.Fatalf("the buffer = %q, want %q", got, want)
	}
	if m.layout.focus != paneEditor {
		t.Fatal("inserting a name must focus the editor")
	}
}
