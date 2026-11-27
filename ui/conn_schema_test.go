package ui

// conn_schema_test.go covers the default schema of a connection: the field in
// the connection form, the way it reaches the driver, the place where the
// user sees it and the message for a schema the database does not have.

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// connFieldHas reports whether the form has a field of that kind.
func connFieldHas(fields []connField, key i18n.Key) bool {
	for _, f := range fields {
		if f.key == key {
			return true
		}
	}
	return false
}

// connFieldIndex returns the position of a field, or -1.
func connFieldIndex(fields []connField, key i18n.Key) int {
	for i, f := range fields {
		if f.key == key {
			return i
		}
	}
	return -1
}

// connFieldValue returns the value of a field of the open form.
func connFieldValue(m Model, key i18n.Key) string {
	idx := connFieldIndex(m.connDlg.fields, key)
	if idx < 0 {
		return ""
	}
	return m.connDlg.fields[idx].value
}

// TestConnDialogShowsTheSchemaOnlyForPostgres pins that the default schema is
// offered where it means something: PostgreSQL resolves unqualified names in a
// search_path, while MySQL has the database and SQLite only main.
func TestConnDialogShowsTheSchemaOnlyForPostgres(t *testing.T) {
	if !connFieldHas(buildConnFields("postgres", nil), i18n.KeyConnSchema) {
		t.Fatal("the postgres form must ask for a default schema")
	}
	for _, driverName := range []string{"demo", "sqlite", "mysql", "mariadb", "sqlserver"} {
		if connFieldHas(buildConnFields(driverName, nil), i18n.KeyConnSchema) {
			t.Fatalf("the %s form must not ask for a default schema", driverName)
		}
	}
}

// TestConnDialogFillsTheSchemaWhenEditing pins that an edited connection
// shows the schema it was saved with.
func TestConnDialogFillsTheSchemaWhenEditing(t *testing.T) {
	m := sized("")
	m.openConnDialogFor(settings.Connection{
		Name:   "shop",
		Driver: "postgres",
		Schema: "sales",
	})
	if got := connFieldValue(m, i18n.KeyConnSchema); got != "sales" {
		t.Fatalf("the schema field = %q, want sales", got)
	}
	// Saving the form keeps it.
	if got := m.connDlg.toConnection().Schema; got != "sales" {
		t.Fatalf("the saved schema = %q, want sales", got)
	}
}

// TestConnDialogSavesTheTypedSchema pins that what the user types reaches the
// connection and the driver configuration.
func TestConnDialogSavesTheTypedSchema(t *testing.T) {
	m := sized("")
	m.openConnDialog()
	m = setConnDriver(m, "postgres")
	m.connDlg.focus = connFieldIndex(m.connDlg.fields, i18n.KeyConnSchema)
	if m.connDlg.focus < 0 {
		t.Fatal("the postgres form must have a schema field")
	}
	m = typeText(m, "sales")

	conn := m.connDlg.toConnection()
	if conn.Schema != "sales" {
		t.Fatalf("the schema = %q, want sales", conn.Schema)
	}
	cfg := driverConfig(conn, "")
	if got := cfg.Options["schema"]; got != "sales" {
		t.Fatalf("the driver option = %q, want sales", got)
	}
}

// TestDriverConfigCarriesTheSchemaWithADSN pins that the default schema
// travels even when the DSN carries everything else: it is a session setting,
// not part of the connection string.
func TestDriverConfigCarriesTheSchemaWithADSN(t *testing.T) {
	cfg := driverConfig(settings.Connection{
		Driver: "postgres",
		DSN:    "postgres://ada@example.test/shop",
		Schema: "sales",
	}, "")
	if got := cfg.Options["schema"]; got != "sales" {
		t.Fatalf("the driver option = %q, want sales", got)
	}
	// A driver without a default schema of its own gets no option: its
	// database is the schema already.
	cfg = driverConfig(settings.Connection{Driver: "mysql", Schema: "shop"}, "")
	if _, ok := cfg.Options["schema"]; ok {
		t.Fatalf("the mysql driver must not get a schema option: %v", cfg.Options)
	}
}

// TestHeaderShowsTheDefaultSchema pins that the session description says
// where unqualified names resolve.
func TestHeaderShowsTheDefaultSchema(t *testing.T) {
	m := sized("")
	m.cfg.ConnName = "shop"
	m.conn = settings.Connection{Name: "shop", Driver: "postgres", Schema: "sales"}

	info := m.sessionInfo()
	if !strings.Contains(info, "Default schema: sales") {
		t.Fatalf("the session info = %q, want the default schema", info)
	}
	// The compact form keeps it: it is the first thing to survive when the
	// bar is too narrow for the whole description.
	if !strings.Contains(m.sessionInfoShort(), "sales") {
		t.Fatalf("the short session info = %q, want the default schema", m.sessionInfoShort())
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Default schema: sales") {
		t.Fatalf("the view is missing the default schema:\n%s", view)
	}
}

// TestObjectTreeMarksTheDefaultSchema pins that the tree says which schema
// the connection works in.
func TestObjectTreeMarksTheDefaultSchema(t *testing.T) {
	m := sized("")
	m.conn = settings.Connection{Name: "demo", Driver: "postgres", Schema: "public"}

	var marked, plain int
	for i, row := range m.objectsRows() {
		if row.kind != rowSchema {
			continue
		}
		line := stripANSI(m.renderObjectRow(row, i, 60))
		if row.schema == m.defaultSchema() {
			marked++
			if !strings.Contains(line, "default") {
				t.Fatalf("the default schema row = %q, want a marker", line)
			}
			continue
		}
		plain++
		if strings.Contains(line, "default") {
			t.Fatalf("the row of another schema must not be marked: %q", line)
		}
	}
	if marked != 1 || plain == 0 {
		t.Fatalf("marked = %d, plain = %d, want exactly one marked schema", marked, plain)
	}
}

// TestMissingSchemaIsReported pins that a default schema the database does
// not have is reported, and that the connection stays usable.
func TestMissingSchemaIsReported(t *testing.T) {
	m := sized("")
	m.settingsPath = t.TempDir() + "/settings.json"

	cmd := m.connectTo(settings.Connection{
		Name:   "shop",
		Driver: fakeDriverName,
		Schema: "nowhere",
	})
	var msg connMsg
	var found bool
	for _, out := range collectCmds(cmd) {
		if cm, ok := out.(connMsg); ok {
			msg, found = cm, true
		}
	}
	if !found {
		t.Fatal("the connection must report its outcome")
	}
	if !strings.Contains(msg.warn, "nowhere") || !strings.Contains(msg.warn, "does not exist") {
		t.Fatalf("the warning = %q, want it to name the missing schema", msg.warn)
	}
	// The status bar shows it, and the connection is the active session.
	next := send(m, msg)
	if !strings.Contains(next.status.message, "does not exist") {
		t.Fatalf("the status = %q, want the warning", next.status.message)
	}
	if next.connOpen == nil {
		t.Fatal("the connection must stay usable")
	}
}

// TestExistingSchemaIsNotReported pins that a schema the database has is not
// reported: the warning is for the schemas that are missing, not for every
// connection that names one.
func TestExistingSchemaIsNotReported(t *testing.T) {
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		c := newFakeConn()
		c.cat = &catalog.Catalog{Schemas: []*catalog.Schema{{Name: "sales"}}}
		return c, nil
	})
	m := sized("")
	cmd := m.connectTo(settings.Connection{
		Name:   "shop",
		Driver: fakeDriverName,
		Schema: "sales",
	})
	for _, out := range collectCmds(cmd) {
		if cm, ok := out.(connMsg); ok {
			if cm.warn != "" {
				t.Fatalf("the warning = %q, want none", cm.warn)
			}
			if cm.err != nil {
				t.Fatalf("the connection failed: %v", cm.err)
			}
			return
		}
	}
	t.Fatal("the connection must report its outcome")
}

// ---- The schema on screen ----

// TestConnDialogSchemaEscapesNeverReachTheView pins that a schema read from
// a settings file (or a catalog) cannot repaint the terminal from inside the
// form: the value is cleaned on the way to the screen while the field keeps
// the bytes it stores, and the caret — which counts runes of the stored
// value — is mapped onto the cleaned text, so the cursor cell sits right
// after the drawn value instead of landing past it or splitting it.
func TestConnDialogSchemaEscapesNeverReachTheView(t *testing.T) {
	m := dialogModel(t, 120, 40)
	raw := "pub" + seqOSC52 + seqClearScreen
	m.openConnDialogFor(settings.Connection{Name: "shop", Driver: "postgres", Schema: raw})
	idx := connFieldIndex(m.connDlg.fields, i18n.KeyConnSchema)
	if idx < 0 {
		t.Fatal("the postgres form must have a schema field")
	}
	m.connDlg.focus = idx // the value is drawn with the caret on it

	assertNoPayload(t, m.View(), "the schema of the form")

	// The cleaned value is what the form draws: the assertions above must
	// not pass because the field showed nothing.
	cleaned := "pub]52;c;aaaa[2J"
	if plain := stripANSI(m.View()); !strings.Contains(plain, cleaned) {
		t.Fatalf("the cleaned schema must stay on screen:\n%s", plain)
	}
	// The field keeps the stored bytes: only the drawing is cleaned, so
	// saving the form writes back what it was given.
	if got := m.connDlg.fields[idx].value; got != raw {
		t.Fatalf("the stored schema = %q, want the original %q", got, raw)
	}
	// The drawn row carries the whole cleaned value, with the cursor cell
	// right behind it: a caret counted on the raw text would sit in the
	// middle of the drawn value or beyond its end.
	l := m.connLayout()
	row := stripANSI(m.renderConnField(idx, m.connDlg.fields[idx], l))
	at := strings.Index(row, cleaned)
	if at < 0 {
		t.Fatalf("the drawn row must carry the cleaned schema: %q", row)
	}
	if rest := row[at+len(cleaned):]; !strings.HasPrefix(rest, " ") {
		t.Fatalf("the cursor must sit right after the cleaned schema: %q", row)
	}
}

// TestConnDialogSchemaFitsANarrowScreen: at 40 columns the form with the
// same tainted schema neither escapes the screen nor leaks a sequence — the
// value column scrolls instead of widening the box.
func TestConnDialogSchemaFitsANarrowScreen(t *testing.T) {
	m := dialogModel(t, 40, 80)
	raw := "pub" + seqOSC52 + seqClearScreen
	m.openConnDialogFor(settings.Connection{Name: "shop", Driver: "postgres", Schema: raw})
	idx := connFieldIndex(m.connDlg.fields, i18n.KeyConnSchema)
	if idx < 0 {
		t.Fatal("the postgres form must have a schema field")
	}
	m.connDlg.focus = idx

	area := m.paneRects().area
	x, y, block, ok := m.renderConnDialog()
	if !ok {
		t.Fatal("the form must render")
	}
	blockInsideRect(t, "connection form", x, y, block, area)

	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("view line %d is %d columns wide, want at most 40:\n%q", i, w, line)
		}
	}
	assertNoPayload(t, view, "the schema of the form")
}
