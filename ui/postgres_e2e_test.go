package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"

	// The optional end-to-end test links the real PostgreSQL driver, just like
	// the binary does. It is skipped unless TDB_POSTGRES_DSN is set.
	_ "github.com/p-v-q/tdb/driver/postgres"
)

// TestPostgresEndToEndQuery runs the editor → driver → result grid loop against
// a real server. It is the PostgreSQL counterpart of ui/e2e_test.go.
func TestPostgresEndToEndQuery(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL end-to-end test")
	}
	conn, err := driver.Open(context.Background(), "postgres", driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	m := e2eModel(t, conn, "SELECT 1::int4 AS n, 'ada'::text AS who, NULL::text AS missing")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"n", "who", "missing", "ada", "NULL", "1 row"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the grid is missing %q:\n%s", want, view)
		}
	}
}

// TestPostgresEndToEndDefaultSchema runs the connection's default schema
// against a real server: the client works in the schema the connection names,
// the header says so, and a schema the database does not have is reported
// without breaking the connection.
func TestPostgresEndToEndDefaultSchema(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL end-to-end test")
	}
	ctx := context.Background()

	// A scratch schema with a table in it: the test must not depend on the
	// privileges of the connected user, and it leaves nothing behind.
	admin, err := driver.Open(ctx, "postgres", driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("tdb_ui_schema_%d", time.Now().UnixNano())
	if _, err := admin.Query(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("cannot create a scratch schema for the default-schema test: %v", err)
	}
	defer func() {
		if _, err := admin.Query(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("DROP SCHEMA %s: %v", schema, err)
		}
	}()
	if _, err := admin.Query(ctx, "CREATE TABLE "+schema+".t_probe (id int PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := admin.Query(ctx, "INSERT INTO "+schema+".t_probe VALUES (7)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	// Connect through the client, the way the connection form does.
	m := sized("")
	m.settingsPath = t.TempDir() + "/settings.json"
	msg := connectForTest(t, &m, settings.Connection{
		Name:   "shop",
		Driver: "postgres",
		DSN:    dsn,
		Schema: schema,
	})
	if msg.err != nil {
		t.Fatalf("connect: %v", msg.err)
	}
	if msg.warn != "" {
		t.Fatalf("the schema exists, so there must be no warning: %q", msg.warn)
	}
	m = send(m, msg)
	if !strings.Contains(m.status.message, "Connected") {
		t.Fatalf("status = %q", m.status.message)
	}
	// The header says where the session works.
	if !strings.Contains(m.sessionInfo(), schema) {
		t.Fatalf("the session info = %q, want the default schema", m.sessionInfo())
	}
	// An unqualified name resolves in the schema.
	m.buf = textbuf.New("SELECT id::text FROM t_probe")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	if len(m.session.result.res.Rows) != 1 || m.session.result.res.Rows[0][0] == nil ||
		*m.session.result.res.Rows[0][0] != "7" {
		t.Fatalf("rows = %+v, want the row of %s.t_probe", m.session.result.res.Rows, schema)
	}

	// A schema the database does not have: the client says so and the
	// connection stays usable.
	m2 := sized("")
	m2.settingsPath = t.TempDir() + "/settings.json"
	msg = connectForTest(t, &m2, settings.Connection{
		Name:   "shop",
		Driver: "postgres",
		DSN:    dsn,
		Schema: schema + "_missing",
	})
	if msg.err != nil {
		t.Fatalf("connect: %v", msg.err)
	}
	if !strings.Contains(msg.warn, "does not exist") {
		t.Fatalf("the warning = %q, want it to name the missing schema", msg.warn)
	}
	m2 = send(m2, msg)
	if !strings.Contains(m2.status.message, "does not exist") {
		t.Fatalf("status = %q, want the warning", m2.status.message)
	}
	m2.buf = textbuf.New("SELECT 1")
	m2 = runToCompletion(m2, tea.KeyMsg{Type: tea.KeyF5})
	if m2.session.result.err != nil {
		t.Fatalf("the connection must stay usable: %v", m2.session.result.err)
	}
}

// connectForTest opens a connection in the background and returns the message
// the client reports.
func connectForTest(t *testing.T, m *Model, c settings.Connection) connMsg {
	t.Helper()
	cmd := m.connectTo(c)
	for _, out := range collectCmds(cmd) {
		if cm, ok := out.(connMsg); ok {
			return cm
		}
	}
	t.Fatal("the connection must report its outcome")
	return connMsg{}
}
