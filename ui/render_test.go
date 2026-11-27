package ui

// render_test.go covers the status-bar texts that come from outside the
// program: the name of the selected object (a catalog identifier or a
// connection name may carry control characters) and the object browser's
// filter (typed or pasted). Both are drawn as plain text, so both have to be
// cleaned on the way to the terminal — while the text itself stays readable.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
)

// assertViewLinesFit pins that every line of the view stays inside the
// terminal the test set, that no escape payload leaked into it and that the
// stripped view holds no escape character at all.
func assertViewLinesFit(t *testing.T, m Model, width int) {
	t.Helper()
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		// The styles of the view are escape sequences themselves, so the
		// check names the payload instead of any escape character; a partial
		// leftover of it turns up in the stripped view below.
		if strings.Contains(line, escPayload) {
			t.Fatalf("view line carries an escape payload:\n%q", line)
		}
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("view line is %d columns wide, want at most %d:\n%q", w, width, line)
		}
	}
	if plain := stripANSI(view); strings.ContainsRune(plain, '\x1b') {
		t.Fatalf("the view must hold no escape character:\n%q", plain)
	}
}

// TestStatusBarObjectNameDropsControlCharacters: a database may put control
// characters into an identifier (PostgreSQL allows them inside a quoted
// name), and the status bar draws the selected object's name as plain text.
func TestStatusBarObjectNameDropsControlCharacters(t *testing.T) {
	m := sized("")
	name := "odd" + escPayload + "table"
	m.cat.Schemas[0].Tables = append(m.cat.Schemas[0].Tables, &catalog.Table{
		Schema: "public", Name: name, Type: catalog.TypeTable,
		Columns: []*catalog.Column{{Name: "id", Type: "int"}},
	})
	m.layout.focus = paneObjects
	idx := objectRowIndex(m, name)
	if idx < 0 {
		t.Fatalf("no %q row in the object tree", name)
	}
	m.objects.sel = idx

	status := stripANSI(m.renderStatus())
	if strings.ContainsRune(status, '\x1b') {
		t.Fatalf("the status carries an escape character: %q", status)
	}
	// The name stays on screen, cleaned: the assertions above must not pass
	// because the bar showed nothing.
	if !strings.Contains(status, "odd[31mXtable") {
		t.Fatalf("the cleaned name must stay on screen: %q", status)
	}
	assertViewLinesFit(t, m, 100)
}

// TestStatusBarConnectionNameDropsControlCharacters: a connection name is
// typed into the dialog, so it may carry control characters as well; the
// status bar draws it together with the selected connection row.
func TestStatusBarConnectionNameDropsControlCharacters(t *testing.T) {
	m := sized("")
	m.conn.Name = "conn" + escPayload
	m.layout.focus = paneObjects
	idx := -1
	for i, row := range m.objectsRows() {
		if row.kind == rowConn {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("the tree must show a connection row")
	}
	m.objects.sel = idx

	status := stripANSI(m.renderStatus())
	if strings.ContainsRune(status, '\x1b') {
		t.Fatalf("the status carries an escape character: %q", status)
	}
	if !strings.Contains(status, "conn[31mX") {
		t.Fatalf("the cleaned connection name must stay on screen: %q", status)
	}
	assertViewLinesFit(t, m, 100)
}

// TestStatusBarObjectFilterDropsControlCharacters: the filter query is typed
// or pasted, never the program's own text, and the status bar draws it as
// plain text — the same rule the pane title above the tree follows (see
// objectsFilterTitle). Ordinary text keeps working: the query stays on
// screen, and a narrow terminal clips the bar instead of overflowing it.
func TestStatusBarObjectFilterDropsControlCharacters(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.filtering = true
	m.objects.filter = escPayload + "users"

	status := stripANSI(m.renderStatus())
	if strings.ContainsRune(status, '\x1b') {
		t.Fatalf("the status carries an escape character: %q", status)
	}
	if !strings.Contains(status, "users") {
		t.Fatalf("the cleaned query must stay on screen: %q", status)
	}
	assertViewLinesFit(t, m, 100)

	// A 40-column terminal clips the bar; it never widens the view.
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	assertViewLinesFit(t, m, 40)
}
