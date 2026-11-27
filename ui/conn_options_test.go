package ui

// conn_options_test.go covers the server-fed database and schema lists of the
// connection form: ↓ asks the server (through an injected opener), the list
// opens on the field, a picked database resets the schema choices, and the
// schema list starts with the database default.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// listerConn is the connection the injected opener returns: the form only calls
// the two listing methods on it.
type listerConn struct {
	*fakeConn
	databases []string
	schemas   []string
	err       error
}

func (c *listerConn) Databases(context.Context) ([]string, error) {
	return c.databases, c.err
}

func (c *listerConn) Schemas(context.Context) ([]string, error) {
	return c.schemas, c.err
}

// useConnLister makes the form's list loader return the given connection.
func useConnLister(t *testing.T, conn *listerConn) {
	t.Helper()
	old := connListerOpen
	connListerOpen = func(context.Context, string, driver.Config) (driver.Connection, error) {
		if conn == nil {
			return nil, errors.New("no server")
		}
		return conn, nil
	}
	t.Cleanup(func() { connListerOpen = old })
}

// postgresForm builds the form for a PostgreSQL connection with the server
// fields filled, the way a user typing them would leave it.
func postgresForm(t *testing.T) Model {
	t.Helper()
	m := New(Config{Title: "TDB", ConnName: "demo", Translator: i18n.New(i18n.En)})
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m.connDlg = connDialog{
		open:   true,
		fields: buildConnFields("postgres", nil),
		focus:  0,
	}
	for i := range m.connDlg.fields {
		f := &m.connDlg.fields[i]
		switch f.key {
		case i18n.KeyConnName:
			f.value = "pg-picked"
		case i18n.KeyConnHost:
			f.value = "127.0.0.1"
		case i18n.KeyConnPort:
			f.value = "5432"
		case i18n.KeyConnUser:
			f.value = "ada"
		case i18n.KeyConnPassword:
			f.value = "secret"
		}
		f.caret = len([]rune(f.value))
	}
	return m
}

// focusConnField moves the form caret to the field with key.
func focusConnField(m Model, key i18n.Key) Model {
	if i, ok := m.connFocusField(key); ok {
		m.connDlg.focus = i
	}
	return m
}

// drainToCompletion feeds every message a command batch produced back into the
// model, the way the event loop would.
func drainToCompletion(m Model, cmd tea.Cmd) Model {
	for _, msg := range collectCmds(cmd) {
		m = send(m, msg)
	}
	return m
}

func TestConnFormDatabaseListOpensFromTheServer(t *testing.T) {
	useConnLister(t, &listerConn{
		fakeConn:  newFakeConn(),
		databases: []string{"legacy", "postgres", "shop"},
		schemas:   []string{"public", "reporting", "sales"},
	})
	m := focusConnField(postgresForm(t), i18n.KeyConnDatabase)

	var cmd tea.Cmd
	var handled bool
	m, cmd, handled = m.handleConnKey("down", nil)
	if !handled || cmd == nil {
		t.Fatal("↓ on the database field must ask the server")
	}
	m = drainToCompletion(m, cmd)

	if got, want := m.connDlg.fields[m.connDlg.focus].options, []string{"legacy", "postgres", "shop"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("database options = %v, want %v", got, want)
	}
	if !m.connDlg.dropdown {
		t.Fatal("the database list must open on the field it belongs to")
	}
	// A picked database lands in the form; the schema is reset because the old
	// one says nothing about the new database.
	m.connDlg.dropSel = 2 // shop
	m, _, _ = m.handleConnKey("enter", nil)
	if got := m.connDlg.fields[m.connDlg.focus].value; got != "shop" {
		t.Fatalf("database = %q, want shop", got)
	}
	schemaIdx, _ := m.connFocusField(i18n.KeyConnSchema)
	if m.connDlg.fields[schemaIdx].value != "" || m.connDlg.fields[schemaIdx].options != nil {
		t.Fatalf("the schema choice must be reset when the database changes: %+v", m.connDlg.fields[schemaIdx])
	}
}

func TestConnFormSchemaListStartsWithTheDatabaseDefault(t *testing.T) {
	useConnLister(t, &listerConn{
		fakeConn: newFakeConn(),
		schemas:  []string{"public", "reporting", "sales"},
	})
	m := focusConnField(postgresForm(t), i18n.KeyConnSchema)

	var cmd tea.Cmd
	var handled bool
	m, cmd, handled = m.handleConnKey("down", nil)
	if !handled || cmd == nil {
		t.Fatal("↓ on the schema field must ask the server")
	}
	m = drainToCompletion(m, cmd)

	options := m.connDlg.fields[m.connDlg.focus].options
	if len(options) != 4 || options[0] != "" {
		t.Fatalf("schema options = %v, want the database default first", options)
	}
	if got := m.connChoiceLabel(m.connDlg.fields[m.connDlg.focus], ""); got != m.t(i18n.KeyConnSchemaDefault) {
		t.Fatalf("empty schema label = %q, want %q", got, m.t(i18n.KeyConnSchemaDefault))
	}
	m.connDlg.dropSel = 3 // sales
	m, _, _ = m.handleConnKey("enter", nil)
	if got := m.connDlg.fields[m.connDlg.focus].value; got != "sales" {
		t.Fatalf("schema = %q, want sales", got)
	}
}

func TestConnFormListFailureIsReported(t *testing.T) {
	useConnLister(t, &listerConn{fakeConn: newFakeConn(), err: errors.New("permission denied")})
	m := focusConnField(postgresForm(t), i18n.KeyConnDatabase)

	var cmd tea.Cmd
	m, cmd, _ = m.handleConnKey("down", nil)
	m = drainToCompletion(m, cmd)

	if m.connDlg.dropdown {
		t.Fatal("a failed list must not open a dropdown")
	}
	if m.connDlg.listKey != "" {
		t.Fatalf("listKey = %q, want it cleared after the failure", m.connDlg.listKey)
	}
	if !strings.Contains(m.status.message, "permission denied") {
		t.Fatalf("status = %q, want the server's error", m.status.message)
	}
}

// TestConnFormDropsListResponseAfterConnectionParametersChange pins the form
// version binding: a database list read from the old host must not populate the
// form after the user edits that host or its credentials.
func TestConnFormDropsListResponseAfterConnectionParametersChange(t *testing.T) {
	useConnLister(t, &listerConn{
		fakeConn:  newFakeConn(),
		databases: []string{"old_server_db"},
	})
	m := focusConnField(postgresForm(t), i18n.KeyConnDatabase)
	var cmd tea.Cmd
	m, cmd, _ = m.handleConnKey("down", nil)
	if cmd == nil {
		t.Fatal("opening the database list must start a request")
	}

	// Edit a parameter used by the request before its response is delivered.
	m = focusConnField(m, i18n.KeyConnHost)
	m, _, _ = m.handleConnKey("x", []rune{'x'})
	m = drainToCompletion(m, cmd)
	dbIdx, _ := m.connFocusField(i18n.KeyConnDatabase)
	if got := m.connDlg.fields[dbIdx].options; len(got) != 0 {
		t.Fatalf("stale server options were applied after the host changed: %v", got)
	}
	if m.connDlg.dropdown {
		t.Fatal("a stale response must not open the database dropdown")
	}
}

// TestConnFormDropsListResponseFromAnOlderFormInstance covers closing and
// reopening the form while both instances have the same field request in flight.
func TestConnFormDropsListResponseFromAnOlderFormInstance(t *testing.T) {
	useConnLister(t, &listerConn{
		fakeConn:  newFakeConn(),
		databases: []string{"stale_instance_db"},
	})
	m := focusConnField(postgresForm(t), i18n.KeyConnDatabase)
	var oldCmd tea.Cmd
	m, oldCmd, _ = m.handleConnKey("down", nil)
	if oldCmd == nil {
		t.Fatal("opening the database list must start a request")
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m.openConnDialogFor(settings.Connection{Name: "new", Driver: "postgres", Host: "new.example"})
	m = focusConnField(m, i18n.KeyConnDatabase)
	var newCmd tea.Cmd
	m, newCmd, _ = m.handleConnKey("down", nil)
	if newCmd == nil {
		t.Fatal("opening the new form's database list must start a request")
	}

	m = drainToCompletion(m, oldCmd)
	dbIdx, _ := m.connFocusField(i18n.KeyConnDatabase)
	if got := m.connDlg.fields[dbIdx].options; len(got) != 0 {
		t.Fatalf("the old form instance supplied options to the new form: %v", got)
	}
	// The current instance's response is still accepted afterwards.
	m = drainToCompletion(m, newCmd)
	if got := m.connDlg.fields[dbIdx].options; len(got) != 1 || got[0] != "stale_instance_db" {
		t.Fatalf("the current form response was lost: %v", got)
	}
}

func TestConnFormTypedValuesStillSubmit(t *testing.T) {
	// The lists are an aid, not a requirement: a database and schema typed by
	// hand reach the connection exactly like picked ones.
	m := postgresForm(t)
	db := focusConnField(m, i18n.KeyConnDatabase)
	db.connDlg.fields[db.connDlg.focus].value = "shop"
	db = focusConnField(db, i18n.KeyConnSchema)
	db.connDlg.fields[db.connDlg.focus].value = "sales"

	c := db.connDlg.toConnection()
	if c.Database != "shop" || c.Schema != "sales" {
		t.Fatalf("connection = %+v, want the typed database and schema", c)
	}
}

// TestHeaderNamesThePostgresDatabase: a PostgreSQL session's header says which
// database it is attached to, so the schemas below the connection have a home.
func TestHeaderNamesThePostgresDatabase(t *testing.T) {
	m := New(Config{
		Title:      "TDB",
		ConnName:   "pg-form",
		Dialect:    dialect.MustGet("postgres"),
		Translator: i18n.New(i18n.En),
	})
	m.conn = settings.Connection{Name: "pg-form", Driver: "postgres", Database: "shop"}
	if got := m.sessionInfo(); !strings.Contains(got, "db shop") {
		t.Fatalf("session info = %q, want the database name", got)
	}
	// The object browser row names it too: the tree's schemas belong to shop.
	m.cat = nil
	m.settings.Connections = nil
	m.connOpen = nil
	row := m.renderObjectRow(objectsRow{kind: rowConn, conn: 0}, 0, 60)
	if got := stripANSI(row); !strings.Contains(got, "shop") {
		t.Fatalf("connection row = %q, want the database name", got)
	}
}
