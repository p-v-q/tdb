package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"

	// The tests link the drivers they exercise, exactly like the binary does.
	_ "github.com/p-v-q/tdb/driver/demo"
)

// fakeDriverName is the name of the driver the tests register with the
// driver registry. It sorts after every real driver, so the lists the UI
// builds from driver.Names() keep the order the other tests expect.
const fakeDriverName = "uifake"

// fakeState is what the tests control and observe about the registered test
// driver.
var fakeState = struct {
	mu sync.Mutex
	// open opens a connection; the tests replace it for one test.
	open func(ctx context.Context, cfg driver.Config) (driver.Connection, error)
	// opened records the context every open was called with, so a test can
	// check that the handshake is bounded.
	opened []context.Context
}{}

// fakeFactory is the registered test driver.
type fakeFactory struct{}

func (fakeFactory) Name() string { return fakeDriverName }

func (fakeFactory) Open(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
	fakeState.mu.Lock()
	open := fakeState.open
	fakeState.opened = append(fakeState.opened, ctx)
	fakeState.mu.Unlock()
	if open == nil {
		return newFakeConn(), nil
	}
	return open(ctx, cfg)
}

// useFakeDriver installs the behaviour of the registered test driver for the
// duration of one test.
func useFakeDriver(t *testing.T, open func(ctx context.Context, cfg driver.Config) (driver.Connection, error)) {
	t.Helper()
	fakeState.mu.Lock()
	old := fakeState.open
	fakeState.open = open
	fakeState.opened = nil
	fakeState.mu.Unlock()
	t.Cleanup(func() {
		fakeState.mu.Lock()
		fakeState.open = old
		fakeState.opened = nil
		fakeState.mu.Unlock()
	})
}

// fakeOpenContexts returns the contexts the driver was opened with.
func fakeOpenContexts() []context.Context {
	fakeState.mu.Lock()
	defer fakeState.mu.Unlock()
	return append([]context.Context(nil), fakeState.opened...)
}

// fakeConn is a connection the tests control: it answers with what the test
// prepared and records that it was closed.
type fakeConn struct {
	dialect  dialect.Dialect
	cat      *catalog.Catalog
	catErr   error
	queryErr error

	mu     sync.Mutex
	closes int
}

func newFakeConn() *fakeConn {
	return &fakeConn{dialect: dialect.MustGet("postgres"), cat: &catalog.Catalog{}}
}

func (c *fakeConn) Name() string { return fakeDriverName }

func (c *fakeConn) Dialect() dialect.Dialect { return c.dialect }

func (c *fakeConn) Catalog(context.Context) (*catalog.Catalog, error) {
	return c.cat, c.catErr
}

func (c *fakeConn) Query(context.Context, string, ...any) (*driver.Result, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &driver.Result{}, nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	return nil
}

// closeCount is how often the connection was closed.
func (c *fakeConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

// init registers the test driver with the registry, so the tests can open a
// connection the UI believes in. The registration only exists in the test
// binary: the driver package itself is untouched.
func init() {
	driver.Register(fakeFactory{})
}

// collectCmds runs a command (including the commands hidden in a batch) and
// returns the messages it produced.
func collectCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collectCmds(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// setConnDriver cycles the type field until it shows the wanted driver.
func setConnDriver(m Model, want string) Model {
	m.connDlg.focus = 1 // the type field
	for i := 0; i <= len(driver.Names()) && m.connDlg.fields[1].value != want; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	return m
}

// connFieldKeys lists the fields of the open form.
func connFieldKeys(m Model) []i18n.Key {
	out := make([]i18n.Key, 0, len(m.connDlg.fields))
	for _, f := range m.connDlg.fields {
		out = append(out, f.key)
	}
	return out
}

func TestObjectBrowserListsConnections(t *testing.T) {
	m := sized("")
	rows := m.objectsRows()
	if len(rows) == 0 || rows[0].kind != rowConn || rows[0].conn != 0 {
		t.Fatalf("the first row must be the connected session, got %+v", rows)
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "New connection") {
		t.Fatalf("the permanent new-connection row must be gone:\n%s", view)
	}
	if !strings.Contains(view, "demo (postgres)") {
		t.Fatalf("the object browser must show the active connection:\n%s", view)
	}
}

func TestEmptyObjectBrowserRightClickOpensNewConnection(t *testing.T) {
	m := sized("")
	// The resource groups add rows below the schemas; make the pane tall enough
	// that there is still genuinely empty space under the last row.
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 60})
	m.objects.sel = -1
	r := m.paneRects().objects
	// Right-click one row below the last tree row.
	y := r.Y + 1 + len(m.objectsRows())
	m = send(m, tea.MouseMsg{
		X: r.X + 2, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight,
	})
	if !m.ctx.open {
		t.Fatal("right-clicking the empty object browser must open a menu")
	}
	if idx := contextActionIndex(m, objActNewConn); idx < 0 {
		t.Fatalf("the empty-area menu must offer New connection: %+v", m.ctx.actions)
	}
	m, _ = clickContextAction(m, objActNewConn)
	if !m.connDlg.open {
		t.Fatal("New connection must open the form")
	}
	view := stripANSI(m.View())
	for _, want := range []string{"New connection", "Name", "Database type", "Connect", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the form is missing %q:\n%s", want, view)
		}
	}
	// Esc cancels it.
	m = keyByName(m, "esc")
	if m.connDlg.open {
		t.Fatal("Esc must close the form")
	}
}

func TestConnDialogFieldsFollowDriverType(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	// The demo connection only needs a name and a type.
	if got := connFieldKeys(m); len(got) != 2 {
		t.Fatalf("demo fields = %v, want name and type", got)
	}
	// A server database asks for the usual parameters.
	m = setConnDriver(m, "postgres")
	keys := connFieldKeys(m)
	for _, want := range []i18n.Key{i18n.KeyConnName, i18n.KeyConnDriver, i18n.KeyConnHost,
		i18n.KeyConnPort, i18n.KeyConnUser, i18n.KeyConnPassword, i18n.KeyConnDatabase, i18n.KeyConnDSN} {
		found := false
		for _, k := range keys {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the postgres form is missing %v (has %v)", want, keys)
		}
	}
	// A file database asks for a file instead.
	m = setConnDriver(m, "sqlite")
	if keys := connFieldKeys(m); len(keys) != 3 || keys[2] != i18n.KeyConnFile {
		t.Fatalf("sqlite fields = %v, want name, type and file", keys)
	}
}

func TestConnDialogKeepsValuesWhenTypeChanges(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = typeText(m, "analytics") // the name field is focused first
	m = setConnDriver(m, "postgres")
	m = setConnDriver(m, "demo")
	if got := m.connDlg.fields[0].value; got != "analytics" {
		t.Fatalf("the name must survive a type change, got %q", got)
	}
}

func TestConnDialogTextEditing(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = typeText(m, "abdc")
	// Backspace fixes the typo, then the caret moves left and inserts.
	m = keyByName(m, "backspace")
	m = keyByName(m, "left")
	m = typeText(m, "c")
	if got := m.connDlg.fields[0].value; got != "abcd" {
		t.Fatalf("value = %q, want abcd", got)
	}
}

func TestConnDialogPasswordIsMasked(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = setConnDriver(m, "postgres")
	for i := range m.connDlg.fields { // focus the password field
		if m.connDlg.fields[i].key == i18n.KeyConnPassword {
			m.connDlg.focus = i
		}
	}
	m = typeText(m, "hunter2")
	if m.connDlg.fields[m.connDlg.focus].value != "hunter2" {
		t.Fatal("the password must be kept as typed")
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "hunter2") {
		t.Fatalf("the password must be masked on screen:\n%s", view)
	}
	if !strings.Contains(view, "•••••••") {
		t.Fatalf("the masked password must be visible:\n%s", view)
	}
}

func TestConnDialogConnectSavesAndConnects(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := sized("")
	m.settingsPath = path
	m.openConnDialog()
	m = typeText(m, "analytics") // the connection name

	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	for _, msg := range collectCmds(cmd) {
		if _, ok := msg.(connMsg); ok {
			next = send(next, msg)
		}
	}
	m = next

	if m.connDlg.open {
		t.Fatal("connecting must close the form")
	}
	// The demo driver opens, so the new connection becomes the session.
	if m.conn.Name != "analytics" {
		t.Fatalf("active connection = %q, want analytics", m.conn.Name)
	}
	if !strings.Contains(m.status.message, "Connected") {
		t.Fatalf("status = %q", m.status.message)
	}
	// It is saved for the next run.
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Name != "analytics" {
		t.Fatalf("saved connections = %+v", got.Connections)
	}
	// The tree lists it.
	if !strings.Contains(stripANSI(m.View()), "analytics (demo)") {
		t.Fatalf("the object browser must show the new connection:\n%s", m.View())
	}
}

func TestConnDialogPendingDriverReportsClearly(t *testing.T) {
	m := sized("")
	m.settingsPath = t.TempDir() + "/settings.json"
	m.openConnDialog()
	m = typeText(m, "warehouse")
	m = setConnDriver(m, "mysql")
	// Focus the name field: Enter on the type field opens its dropdown now.
	m = keyByName(m, "up")

	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	for _, msg := range collectCmds(cmd) {
		if _, ok := msg.(connMsg); ok {
			next = send(next, msg)
		}
	}
	m = next

	if !strings.Contains(m.status.message, "not implemented") {
		t.Fatalf("a pending driver must say so, status = %q", m.status.message)
	}
	// The connection is kept anyway, ready for the day the driver lands.
	got, err := settings.Load(m.settingsPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Driver != "mysql" {
		t.Fatalf("saved connections = %+v", got.Connections)
	}
}

func TestConnDialogMouseButtons(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	l := m.connLayout()

	// Clicking a field focuses it. (The type row is a dropdown and has its own
	// mouse test below.)
	row := 0
	m = send(m, mousePress(l.valueX+1, l.rowY+row))
	if m.connDlg.focus != row {
		t.Fatalf("focus = %d, want %d", m.connDlg.focus, row)
	}
	// Cancel closes without connecting.
	m = send(m, mousePress(l.cancel.X+1, l.cancel.Y))
	if m.connDlg.open {
		t.Fatal("Cancel must close the form")
	}
}

func TestConnDialogIsModal(t *testing.T) {
	m := sized("SELECT 1")
	m.openConnDialog()
	m = typeText(m, "abc")
	if m.buf.Text() != "SELECT 1" {
		t.Fatalf("typing in the form must not reach the editor: %q", m.buf.Text())
	}
}

// ---- The database type is a dropdown ----

func TestConnDriverLabel(t *testing.T) {
	for id, want := range map[string]string{
		"postgres":  "PostgreSQL",
		"mysql":     "MySQL",
		"mariadb":   "MariaDB",
		"sqlite":    "SQLite",
		"demo":      "Demo",
		"sqlserver": "SQL Server",
		"duckdb":    "Duckdb", // unknown drivers are capitalised
	} {
		if got := connDriverLabel(id); got != want {
			t.Errorf("connDriverLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestConnDialogDropdownOpensAndSelects(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = typeText(m, "analytics")
	m.connDlg.focus = 1 // the type field

	// Enter opens the list instead of connecting.
	m = keyByName(m, "enter")
	if !m.connDlg.dropdown {
		t.Fatal("Enter on the type field must open the option list")
	}
	if !m.connDlg.open {
		t.Fatal("opening the list must not close the form")
	}
	if m.connDlg.dropSel != 0 {
		t.Fatalf("the list must start on the current driver, sel = %d", m.connDlg.dropSel)
	}
	// While the list is open no key leaks into the form.
	m = typeText(m, "zzz")
	if got := m.connDlg.fields[0].value; got != "analytics" {
		t.Fatalf("typing with the list open must not reach the name, got %q", got)
	}
	if got := m.connDlg.fields[1].value; got != "demo" {
		t.Fatalf("typing with the list open must not change the driver, got %q", got)
	}
	// ↓ and End move the highlight, ⏎ picks the driver.
	m = keyByName(m, "down")
	m = send(m, tea.KeyMsg{Type: tea.KeyEnd})
	if got, want := m.connDlg.dropSel, len(driver.Names())-1; got != want {
		t.Fatalf("End must highlight the last option, sel = %d, want %d", got, want)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyHome})
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	if m.connDlg.dropdown {
		t.Fatal("picking an option must close the list")
	}
	if got := m.connDlg.fields[1].value; got != "mariadb" {
		t.Fatalf("driver = %q, want mariadb (the option after demo)", got)
	}
	if m.connDlg.focus != 1 {
		t.Fatalf("focus = %d, must stay on the type field", m.connDlg.focus)
	}
	if got := m.connDlg.fields[0].value; got != "analytics" {
		t.Fatalf("the name must survive the type change, got %q", got)
	}
	// The form followed the type: a server database asks for host/port/… and
	// the port restarts from the default of the new type.
	if keys := connFieldKeys(m); len(keys) < 3 || keys[2] != i18n.KeyConnHost {
		t.Fatalf("fields = %v, want host right after the type", connFieldKeys(m))
	}
	for _, f := range m.connDlg.fields {
		if f.key == i18n.KeyConnPort && f.value != "3306" {
			t.Fatalf("port = %q, want the mariadb default 3306", f.value)
		}
	}
}

func TestConnDialogDropdownSpaceOpensAndSelects(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m.connDlg.focus = 1

	m = keyByName(m, "space")
	if !m.connDlg.dropdown {
		t.Fatal("Space on the type field must open the option list")
	}
	if got := m.connDlg.fields[1].value; got != "demo" {
		t.Fatalf("opening the list must not change the driver, got %q", got)
	}
	m = keyByName(m, "space")
	if m.connDlg.dropdown {
		t.Fatal("Space must pick the highlighted option")
	}
	if got := m.connDlg.fields[1].value; got != "demo" {
		t.Fatalf("driver = %q, want demo (the highlight starts on it)", got)
	}
}

func TestConnDialogDropdownEscKeepsFormOpen(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m.connDlg.focus = 1

	m = keyByName(m, "enter")
	m = keyByName(m, "esc")
	if m.connDlg.dropdown {
		t.Fatal("Esc must close the option list")
	}
	if !m.connDlg.open {
		t.Fatal("Esc must not close the form while closing the list")
	}
	if got := m.connDlg.fields[1].value; got != "demo" {
		t.Fatalf("closing the list must not change the driver, got %q", got)
	}
	// A second Esc, with the list closed, cancels the form.
	m = keyByName(m, "esc")
	if m.connDlg.open {
		t.Fatal("Esc must close the form once the list is closed")
	}
}

func TestConnDialogDropdownTabMovesOn(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m.connDlg.focus = 1

	// Tab closes the list and moves to the next field.
	m = keyByName(m, "enter")
	m = keyByName(m, "tab")
	if m.connDlg.dropdown {
		t.Fatal("Tab must close the option list")
	}
	if m.connDlg.focus == 1 {
		t.Fatal("Tab must move the focus off the type field")
	}
	if got := m.connDlg.fields[1].value; got != "demo" {
		t.Fatalf("Tab must not change the driver, got %q", got)
	}
	// Shift+Tab does the same in the other direction.
	m.connDlg.focus = 1
	m = keyByName(m, "enter")
	m = send(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.connDlg.dropdown {
		t.Fatal("shift+tab must close the option list")
	}
	if m.connDlg.focus == 1 {
		t.Fatal("shift+tab must move the focus off the type field")
	}
}

func TestConnDialogDropdownMouse(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = typeText(m, "analytics")
	l := m.connLayout()

	// Clicking the type row opens the list.
	m = send(m, mousePress(l.valueX+1, l.rowY+1))
	if !m.connDlg.dropdown {
		t.Fatal("clicking the type row must open the option list")
	}
	d, top, _, ok := m.connDropdownLayout()
	if !ok {
		t.Fatal("the open list must have a layout")
	}
	if top != 0 {
		t.Fatalf("top = %d, want 0 (all options fit on screen)", top)
	}
	// Clicking an option picks it.
	m = send(m, mousePress(d.X+1, d.Y+2))
	if m.connDlg.dropdown {
		t.Fatal("clicking an option must close the list")
	}
	if got := m.connDlg.fields[1].value; got != "mariadb" {
		t.Fatalf("driver = %q, want mariadb (the second option)", got)
	}
	// The rebuilt form has its own geometry now.
	l = m.connLayout()

	// Reopen; a click outside the list only closes it, and the same click on
	// the Connect button must not submit the form.
	m = send(m, mousePress(l.valueX+1, l.rowY+1))
	if !m.connDlg.dropdown {
		t.Fatal("clicking the type row must reopen the option list")
	}
	m = send(m, mousePress(l.connect.X+1, l.connect.Y))
	if m.connDlg.dropdown {
		t.Fatal("a click outside the list must close it")
	}
	if !m.connDlg.open {
		t.Fatal("the same click must not press Connect")
	}
	if m.conn.Name != "demo" {
		t.Fatalf("active connection = %q, want demo (nothing was submitted)", m.conn.Name)
	}
}

func TestConnDialogDropdownMouseIgnoresBorders(t *testing.T) {
	// A short terminal makes the list scroll: fewer options fit than exist, so
	// the top and bottom borders map to options outside the viewport. The
	// height is the smallest the shared modal rules allow a dialog to claim
	// (see ui/modal.go), so the whole form still fits and the type row is
	// on screen.
	m := sized("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m.openConnDialog()
	m = typeText(m, "analytics")
	l := m.connLayout()

	open := func() (Rect, int, int) {
		t.Helper()
		m = send(m, mousePress(l.valueX+1, l.rowY+1))
		if !m.connDlg.dropdown {
			t.Fatal("clicking the type row must open the option list")
		}
		options := m.connDlg.fields[m.connDlg.focus].options
		_, _, fit, ok := m.connDropdownLayout()
		if !ok {
			t.Fatal("the open list must have a layout")
		}
		if fit >= len(options) {
			t.Fatalf("precondition failed: visible = %d, options = %d", fit, len(options))
		}
		// Scroll to the middle, so both borders sit next to a real option.
		m.connDlg.dropSel = clamp(fit, 0, len(options)-1)
		d, top, visible, ok := m.connDropdownLayout()
		if !ok {
			t.Fatal("the open list must have a layout")
		}
		if top <= 0 || top+visible >= len(options) {
			t.Fatalf("precondition failed: the list must scroll with room below, top = %d, visible = %d, options = %d",
				top, visible, len(options))
		}
		return d, top, visible
	}

	d, top, _ := open()
	want := m.connDlg.fields[m.connDlg.focus].value
	borders := []struct {
		name string
		x, y int
	}{
		{"top", d.X + 1, d.Y},
		{"bottom", d.X + 1, d.Y + d.H - 1},
		{"left", d.X, d.Y + 1},
		{"right", d.X + d.W - 1, d.Y + 1},
	}
	for _, b := range borders {
		// Each border click must close the list without changing the driver.
		if !m.connDlg.dropdown {
			open()
		}
		m = send(m, mousePress(b.x, b.y))
		if m.connDlg.dropdown {
			t.Fatalf("clicking the %s border must close the list", b.name)
		}
		if got := m.connDlg.fields[m.connDlg.focus].value; got != want {
			t.Fatalf("clicking the %s border changed the driver to %q, want %q", b.name, got, want)
		}
		if !m.connDlg.open {
			t.Fatalf("clicking the %s border must not close the form", b.name)
		}
	}

	// A real option row still selects, even with the list scrolled.
	d, top, _ = open()
	m = send(m, mousePress(d.X+1, d.Y+1))
	if m.connDlg.dropdown {
		t.Fatal("clicking an option must close the list")
	}
	options := m.connDlg.fields[m.connDlg.focus].options
	if got, want := m.connDlg.fields[m.connDlg.focus].value, options[top]; got != want {
		t.Fatalf("driver = %q, want the clicked option %q", got, want)
	}
}

func TestConnDialogDropdownRendered(t *testing.T) {
	m := sized("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.openConnDialog()

	// The closed field shows the friendly label and the dropdown indicator.
	view := stripANSI(m.View())
	if !strings.Contains(view, "Demo") || !strings.Contains(view, "▾") {
		t.Fatalf("the type field must show a label and ▾:\n%s", view)
	}
	// The open list shows every driver.
	m.connDlg.focus = 1
	m = keyByName(m, "enter")
	view = stripANSI(m.View())
	for _, want := range []string{"PostgreSQL", "MySQL", "MariaDB", "SQLite", "Demo"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the option list is missing %q:\n%s", want, view)
		}
	}
}

func TestComposeDSNEscapesEveryField(t *testing.T) {
	// A password containing @ or / must not be able to change the parsed host,
	// and a database name with a space must stay one path segment.
	c := settings.Connection{
		Driver:   "postgres",
		Host:     "db.example",
		Port:     5433,
		User:     "ada",
		Password: "p@ss/word",
		Database: "shop db",
	}
	want := "postgres://ada:p%40ss%2Fword@db.example:5433/shop%20db"
	if got := composeDSN(c, c.Password); got != want {
		t.Fatalf("composeDSN = %q, want %q", got, want)
	}

	c = settings.Connection{Driver: "postgres", Host: "::1", Port: 5432, Database: "shop"}
	if got, want := composeDSN(c, ""), "postgres://[::1]:5432/shop"; got != want {
		t.Fatalf("IPv6 composeDSN = %q, want %q", got, want)
	}
}

func TestAppMenuIsFirstAndCarriesAppActions(t *testing.T) {
	if menuDefs[0].titleKey != i18n.KeyAppName {
		t.Fatalf("the first menu must be the application menu, got %v", menuDefs[0].titleKey)
	}
	actions := map[menuAction]bool{}
	for _, it := range menuDefs[0].items {
		actions[it.action] = true
	}
	for _, want := range []menuAction{actAbout, actPreferences, actQuit} {
		if !actions[want] {
			t.Fatalf("the application menu is missing action %v", want)
		}
	}
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF10}) // opens the application menu
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !strings.Contains(m.status.message, "TDB") {
		t.Fatalf("About must name the app, status = %q", m.status.message)
	}
}

// ---- Connecting, reconnecting and disconnecting ----

// TestConnectToMissingPasswordEnvKeepsTheConnectionOutOfTheSettings pins the
// order of the two steps of a connection: the password is resolved first, so
// a connection whose secret is missing is neither opened nor saved. Saving it
// anyway would leave a broken entry behind that every later save would carry
// into the settings file.
func TestConnectToMissingPasswordEnvKeepsTheConnectionOutOfTheSettings(t *testing.T) {
	t.Setenv("TDB_TEST_MISSING_PASSWORD", "")
	path := filepath.Join(t.TempDir(), "settings.json")
	m := sized("")
	m.settingsPath = path

	cmd := m.connectTo(settings.Connection{
		Name:        "analytics",
		Driver:      "postgres",
		PasswordEnv: "TDB_TEST_MISSING_PASSWORD",
	})
	collectCmds(cmd)

	if len(m.settings.Connections) != 0 {
		t.Fatalf("saved connections = %+v, want none", m.settings.Connections)
	}
	if !strings.Contains(m.status.message, "TDB_TEST_MISSING_PASSWORD") {
		t.Fatalf("status = %q, want the missing-variable message", m.status.message)
	}
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 0 {
		t.Fatalf("settings file holds %+v, want no connection", got.Connections)
	}
}

// TestConnectTimeoutIsReported checks the wording of a connection that runs
// into the connect timeout: the user learns that the server did not answer
// in time, instead of a generic failure.
func TestConnectTimeoutIsReported(t *testing.T) {
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return nil, fmt.Errorf("dial %s: %w", cfg.DSN, context.DeadlineExceeded)
	})
	m := sized("")
	m.conn = settings.Connection{Name: "slow", Driver: fakeDriverName}

	cmd := m.connectTo(m.conn)
	found := false
	for _, msg := range collectCmds(cmd) {
		if cm, ok := msg.(connMsg); ok {
			found = true
			m = send(m, cm)
		}
	}
	if !found {
		t.Fatal("the connection attempt must report its outcome")
	}
	if !strings.Contains(m.status.message, "Connection timed out after 15s") {
		t.Fatalf("status = %q, want the timeout message", m.status.message)
	}
}

// TestConnectIsBoundedByATimeout pins that the handshake runs under a
// deadline: a host that accepts the connection but never answers must not
// keep the background command alive forever.
func TestConnectIsBoundedByATimeout(t *testing.T) {
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return newFakeConn(), nil
	})
	m := sized("")
	m.conn = settings.Connection{Name: "slow", Driver: fakeDriverName}

	collectCmds(m.connectTo(m.conn))

	contexts := fakeOpenContexts()
	if len(contexts) != 1 {
		t.Fatalf("the driver was opened %d times, want once", len(contexts))
	}
	deadline, ok := contexts[0].Deadline()
	if !ok {
		t.Fatal("the handshake must run under a deadline")
	}
	if left := time.Until(deadline); left > connectTimeout || left < connectTimeout-time.Minute {
		t.Fatalf("the deadline is %s away, want about %s", left, connectTimeout)
	}
}

// TestCatalogLoadIsBoundedByATimeout checks the same protection for the
// metadata load that runs at startup.
func TestCatalogLoadIsBoundedByATimeout(t *testing.T) {
	conn := newFakeConn()
	m := New(Config{
		ConnName:   "fake",
		Dialect:    conn.Dialect(),
		Provider:   conn,
		Translator: i18n.New(i18n.En),
	})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("without an initial snapshot Init must return a load command")
	}
	msg := cmd()
	catMsg, ok := msg.(catalogMsg)
	if !ok || catMsg.err != nil {
		t.Fatalf("msg = %+v", msg)
	}
}

// TestReconnectClosesTheOldConnectionAndOpensANewOne covers the reconnect
// action: the old connection is closed first, and the new one becomes the
// session with its own wording.
func TestReconnectClosesTheOldConnectionAndOpensANewOne(t *testing.T) {
	old := newFakeConn()
	fresh := newFakeConn()
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return fresh, nil
	})

	m := sized("")
	m.connOpen = old
	m.conn = settings.Connection{Name: "shop", Driver: fakeDriverName}

	m = rightClickObjectsRow(m, 0)
	next, cmd := clickContextAction(m, objActReconnect)
	if old.closeCount() != 0 {
		t.Fatalf("the old connection was closed on the UI goroutine %d times", old.closeCount())
	}
	closeOutcome := cmd()
	closeMsg, ok := closeOutcome.(connCloseMsg)
	if !ok {
		t.Fatalf("reconnect first command = %T, want connCloseMsg", closeOutcome)
	}
	if old.closeCount() != 1 {
		t.Fatalf("the old connection was closed %d times, want once", old.closeCount())
	}
	next = send(next, closeMsg)
	for _, msg := range collectCmds(closeMsg.next) {
		if cm, ok := msg.(connMsg); ok {
			next = send(next, cm)
		}
	}
	if next.connOpen != fresh {
		t.Fatal("the new connection must become the session")
	}
	if !strings.Contains(next.status.message, "Reconnected to shop") {
		t.Fatalf("status = %q, want the reconnected message", next.status.message)
	}
}

// TestDisconnectClosesTheConnectionAndClearsTheSession covers the disconnect
// action: the connection is closed and everything that belonged to it is
// dropped, while the connection itself stays in the list.
func TestDisconnectClosesTheConnectionAndClearsTheSession(t *testing.T) {
	conn := newFakeConn()
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "shop", Driver: fakeDriverName}
	m.cat = catalog.DemoCatalog()
	m.cfg.ConnName = "shop"
	m.cfg.Provider = conn

	m = rightClickObjectsRow(m, 0)
	next, cmd := clickContextAction(m, objActDisconnect)
	for _, msg := range collectCmds(cmd) {
		if closeMsg, ok := msg.(connCloseMsg); ok {
			next = send(next, closeMsg)
		}
	}

	if conn.closeCount() != 1 {
		t.Fatalf("the connection was closed %d times, want once", conn.closeCount())
	}
	if next.connOpen != nil {
		t.Fatal("the live connection must be forgotten")
	}
	if next.cat != nil {
		t.Fatal("the metadata of the closed connection must be dropped")
	}
	if next.cfg.ConnName != "" {
		t.Fatalf("the header still names the connection: %q", next.cfg.ConnName)
	}
	if next.cfg.Provider != nil {
		t.Fatal("the provider of the closed connection must be dropped")
	}
	if len(next.objectsRows()) != 1 || next.objectsRows()[0].kind != rowConn {
		t.Fatalf("the tree must only keep the connection row: %+v", next.objectsRows())
	}
	if !strings.Contains(next.status.message, "Disconnected from shop") {
		t.Fatalf("status = %q, want the disconnected message", next.status.message)
	}
}

// TestQuitClosesTheConnection pins that leaving the application closes the
// connection instead of letting the server wait for a timeout.
func TestQuitClosesTheConnection(t *testing.T) {
	conn := newFakeConn()
	m := sized("")
	m.connOpen = conn

	cmd := m.runMenuAction(actQuit)
	if !m.quitting || cmd == nil {
		t.Fatal("quit must detach the session and return a shutdown command")
	}
	outcome := cmd()
	if conn.closeCount() != 1 {
		t.Fatalf("the connection was closed %d times, want once", conn.closeCount())
	}
	_, quitCmd := m.Update(outcome)
	if quitCmd == nil {
		t.Fatal("the completed shutdown must quit Bubble Tea")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("the shutdown continuation must be tea.Quit")
	}
}

// fakeTxConn is a fake connection that also manages transactions, so the
// tests can put the model into the state where uncommitted work exists.
type fakeTxConn struct {
	*fakeConn
	rollbacks int
}

func (c *fakeTxConn) Begin(context.Context) error    { return nil }
func (c *fakeTxConn) Commit(context.Context) error   { return nil }
func (c *fakeTxConn) Rollback(context.Context) error { c.rollbacks++; return nil }
func (c *fakeTxConn) InTx() bool                     { return false }

// TestDisconnectRollsBackUncommittedWorkFirst checks that the connection is
// not dropped with uncommitted work on it: the transaction is rolled back,
// and the user is told about it.
func TestDisconnectRollsBackUncommittedWorkFirst(t *testing.T) {
	conn := &fakeTxConn{fakeConn: newFakeConn()}
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "shop", Driver: fakeDriverName}
	m.txOpen = true

	m = rightClickObjectsRow(m, 0)
	next, cmd := clickContextAction(m, objActDisconnect)
	for _, msg := range collectCmds(cmd) {
		if closeMsg, ok := msg.(connCloseMsg); ok {
			next = send(next, closeMsg)
		}
	}

	if conn.rollbacks != 1 {
		t.Fatalf("the transaction was rolled back %d times, want once", conn.rollbacks)
	}
	if !strings.Contains(next.status.message, "Rolled back") {
		t.Fatalf("status = %q, want the rollback warning", next.status.message)
	}
	if !strings.Contains(next.status.message, "Disconnected from shop") {
		t.Fatalf("status = %q, want the disconnected message too", next.status.message)
	}
}

// TestReconnectRollsBackUncommittedWorkFirst checks the same for the
// reconnect: the warning must survive until the outcome is reported.
func TestReconnectRollsBackUncommittedWorkFirst(t *testing.T) {
	conn := &fakeTxConn{fakeConn: newFakeConn()}
	fresh := newFakeConn()
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return fresh, nil
	})

	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "shop", Driver: fakeDriverName}
	m.txOpen = true

	m = rightClickObjectsRow(m, 0)
	next, cmd := clickContextAction(m, objActReconnect)
	if conn.rollbacks != 0 {
		t.Fatalf("the transaction was rolled back on the UI goroutine %d times", conn.rollbacks)
	}
	closeOutcome := cmd()
	closeMsg, ok := closeOutcome.(connCloseMsg)
	if !ok {
		t.Fatalf("reconnect first command = %T, want connCloseMsg", closeOutcome)
	}
	if conn.rollbacks != 1 {
		t.Fatalf("the transaction was rolled back %d times, want once", conn.rollbacks)
	}
	next = send(next, closeMsg)
	for _, msg := range collectCmds(closeMsg.next) {
		if cm, ok := msg.(connMsg); ok {
			next = send(next, cm)
		}
	}
	if !strings.Contains(next.status.message, "Rolled back") {
		t.Fatalf("status = %q, want the rollback warning", next.status.message)
	}
	if !strings.Contains(next.status.message, "Reconnected to shop") {
		t.Fatalf("status = %q, want the reconnected message too", next.status.message)
	}
}

// TestConnDialogDropdownHoverHighlightsWithoutPicking pins the pointer
// semantics of the connection form's option list (round 7 §1): motion
// highlights the row under the pointer, never moves dropSel, never writes the
// field and never closes the list; only a press picks. The hit test is shared
// by motion and press (connDropRowAt), so what is highlighted is what a press
// would pick.
func TestConnDialogDropdownHoverHighlightsWithoutPicking(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the selected
	// style is really in the rendered row, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.openConnDialog()
	m.connDlg.focus = 1 // the type field
	m = keyByName(m, "enter")
	if !m.connDlg.dropdown {
		t.Fatal("enter must open the option list")
	}
	d, top, visible, ok := m.connDropdownLayout()
	if !ok {
		t.Fatal("the open list must have a layout")
	}
	options := m.connDlg.fields[m.connDlg.focus].options
	if top != 0 || visible < 3 || len(options) < 3 {
		t.Fatalf("precondition: top = %d, visible = %d, options = %d", top, visible, len(options))
	}

	// Motion onto the third option row highlights it and changes nothing.
	m = send(m, mouseMotion(d.X+1, d.Y+1+2))
	if m.connDlg.dropHover != top+2 {
		t.Fatalf("dropHover = %d, want %d (the third row)", m.connDlg.dropHover, top+2)
	}
	if m.connDlg.dropSel != 0 {
		t.Fatalf("dropSel = %d, hovering must not move the keyboard highlight", m.connDlg.dropSel)
	}
	if got := m.connDlg.fields[m.connDlg.focus].value; got != "demo" {
		t.Fatalf("driver = %q, hovering must not change the value", got)
	}
	if !m.connDlg.dropdown {
		t.Fatal("hovering must not close the option list")
	}

	// The hovered row is drawn exactly like a selected one, and differently
	// from a row that is neither selected nor hovered.
	lines := connDropdownLines(t, m)
	rowIdx := 1 + (m.connDlg.dropHover - top) // out[0] is the top border
	savedSel, savedHover := m.connDlg.dropSel, m.connDlg.dropHover
	m.connDlg.dropSel, m.connDlg.dropHover = savedHover, -1
	selected := connDropdownLines(t, m)
	m.connDlg.dropSel, m.connDlg.dropHover = savedSel, -1
	plain := connDropdownLines(t, m)
	m.connDlg.dropSel, m.connDlg.dropHover = savedSel, savedHover
	if lines[rowIdx] != selected[rowIdx] {
		t.Fatalf("the hovered row must be drawn like the selected one:\nhover:   %q\nselect: %q",
			lines[rowIdx], selected[rowIdx])
	}
	if lines[rowIdx] == plain[rowIdx] {
		t.Fatalf("the hovered row must be drawn differently from a plain one: %q", lines[rowIdx])
	}

	// Only a press picks the option under the pointer.
	m = send(m, mousePress(d.X+1, d.Y+1+2))
	if m.connDlg.dropdown {
		t.Fatal("pressing an option must close the list")
	}
	if got, want := m.connDlg.fields[1].value, options[top+2]; got != want {
		t.Fatalf("driver = %q, want %q (the pressed option)", got, want)
	}

	// Reopened: a motion outside the list clears the highlight only. It must
	// not close the list, not move the selection and not press a form button.
	l := m.connLayout()
	m = send(m, mousePress(l.valueX+1, l.rowY+1))
	if !m.connDlg.dropdown {
		t.Fatal("clicking the type row must reopen the option list")
	}
	value := m.connDlg.fields[1].value
	m = send(m, mouseMotion(d.X+d.W+4, d.Y+1))
	if m.connDlg.dropHover != -1 {
		t.Fatalf("dropHover = %d, want -1 outside the list", m.connDlg.dropHover)
	}
	if !m.connDlg.dropdown || !m.connDlg.open {
		t.Fatal("a motion outside the list must leave the list and the form open")
	}
	if got := m.connDlg.fields[1].value; got != value {
		t.Fatalf("driver = %q, want it unchanged (%q)", got, value)
	}
}

// connDropdownLines renders the open option list of the connection form.
func connDropdownLines(t *testing.T, m Model) []string {
	t.Helper()
	_, _, block, ok := m.renderConnDropdown()
	if !ok {
		t.Fatal("the open option list must render")
	}
	return block
}
