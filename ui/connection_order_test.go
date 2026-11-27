package ui

// connection_order_test.go pins how the object browser orders connections: the
// saved order is stable, so opening a connection does not make its row jump to
// the top of the tree (or make the pane scroll there). The caret stays on the
// row the user opened.

import (
	"context"
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/settings"

	tea "github.com/charmbracelet/bubbletea"
)

// connNames returns the connection names in tree order.
func connNames(m Model) []string {
	conns := m.connectionList()
	out := make([]string, len(conns))
	for i := range conns {
		out[i] = conns[i].Name
	}
	return out
}

func TestConnectionListKeepsSavedOrderForTheLiveConnection(t *testing.T) {
	m := sized("")
	m.settings.Connections = []settings.Connection{
		{Name: "alpha", Driver: "demo"},
		{Name: "beta", Driver: "demo"},
		{Name: "gamma", Driver: "demo"},
	}
	m.conn = settings.Connection{Name: "demo", Driver: "demo"} // unsaved: prepended

	if got := connNames(m); !equalStrings(got, []string{"demo", "alpha", "beta", "gamma"}) {
		t.Fatalf("connections = %v, the unsaved session must be prepended", got)
	}

	// Opening beta keeps the saved order: the live row stays in its place.
	m.applyConnection(settings.Connection{Name: "beta", Driver: "demo"}, newFakeConn(), &catalog.Catalog{})
	if got := connNames(m); !equalStrings(got, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("connections after connecting = %v, want the saved order", got)
	}
	m.selectConnRowInTree("beta")
	var liveRow int = -1
	for i, row := range m.objectsRows() {
		if row.kind == rowConn && m.connectionList()[row.conn].Name == "beta" {
			liveRow = i
		}
	}
	if liveRow != 1 {
		t.Fatalf("the beta row = %d, want it in saved position 1", liveRow)
	}
	if m.objects.sel != liveRow {
		t.Fatalf("selection = %d, want the opened connection's row %d", m.objects.sel, liveRow)
	}
	if !m.isLiveConnRow(objectsRow{kind: rowConn, conn: liveRow}) {
		t.Fatal("beta must be the live connection row after applying it")
	}
}

func TestDoubleClickSavedConnectionKeepsItsPositionAndScroll(t *testing.T) {
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return newFakeConn(), nil
	})
	m := sized("")
	const saved = 40
	for i := 0; i < saved; i++ {
		m.settings.Connections = append(m.settings.Connections, settings.Connection{
			Name:   "conn-" + itoa2(i),
			Driver: "demo",
		})
	}
	// Keep the tree off the connection list: the pane only shows connections.
	idx := savedConnRowIndex(m, "conn-39")
	if idx < 0 {
		t.Fatal("setup: conn-39 must be in the tree")
	}
	// The last connection is below the fold: scroll the pane the way the user
	// would before clicking its row.
	m.objects.scroll = max(idx-m.objectsBodyHeight()+1, 0)
	x, y := objectsRowPoint(m, idx)
	next, cmd := doubleClick(m, x, y)
	m = next

	// The connect is asynchronous: feed the messages it produced (the settings
	// write and the connMsg) back into the model.
	for _, msg := range collectCmds(cmd) {
		m = send(m, msg)
	}

	if m.conn.Name != "conn-39" {
		t.Fatalf("live connection = %q, want conn-39", m.conn.Name)
	}
	// The unsaved demo session left the list with its session; the saved
	// connections keep their order, so the opened one is still last.
	if got := connNames(m); len(got) != saved || got[0] != "conn-00" || got[saved-1] != "conn-39" {
		t.Fatalf("connections = %v, want the saved order with conn-39 last", got)
	}
	newIdx := savedConnRowIndex(m, "conn-39")
	if m.objects.sel != newIdx {
		t.Fatalf("selection = %d, want the opened connection's row %d", m.objects.sel, newIdx)
	}
	_ = idx
	if m.objects.scroll == 0 {
		t.Fatalf("scroll = 0, the tree must not jump to the top (selection %d)", m.objects.sel)
	}
	if m.objects.sel < m.objects.scroll {
		t.Fatalf("selection %d is above scroll %d", m.objects.sel, m.objects.scroll)
	}
	_ = tea.KeyMsg{}
}

func itoa2(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
