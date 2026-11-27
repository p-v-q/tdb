package scenarios

// pg_picker_test.go covers how a PostgreSQL connection is created and how its
// database and default schema are chosen: both must be selectable from what the
// server actually holds, not typed from memory.

import (
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// pgNewConnectionForm opens the connection form, picks the PostgreSQL driver
// and returns the context once the server fields are on screen.
func pgNewConnectionForm(t *testing.T) *scenario.Ctx {
	t.Helper()
	c := scenario.New(t)
	c.Start("-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	// The object browser menu holds "New connection…".
	if err := c.S.MouseClickButton(10, 4, harness.MouseRight); err != nil {
		t.Fatalf("right click: %v", err)
	}
	c.WaitFor("New connection", 10*time.Second)
	c.ClickText("New connection")
	c.WaitFor("Database type", 10*time.Second)

	// New connection -> Name, then Tab to the type field and walk to
	// PostgreSQL: Demo, MariaDB, MySQL, PostgreSQL, …
	c.Type("name", "pg-form")
	c.Keys("to-driver", harness.KeyTab)
	c.Keys("open-types", harness.KeyEnter)
	c.WaitFor("PostgreSQL", 10*time.Second)
	for i := 0; i < 3; i++ {
		c.Keys("next-type", harness.KeyDown)
	}
	c.Keys("pick-type", harness.KeyEnter)
	c.WaitFor("Host", 10*time.Second)
	return c
}

// pgFillServerFields types the server coordinates into the form. Host and port
// come prefilled (localhost / 5432), so they are replaced rather than appended.
func pgFillServerFields(c *scenario.Ctx) {
	c.T.Helper()
	c.Keys("to-host", harness.KeyTab)
	c.ReplaceField("host", "127.0.0.1")
	c.Keys("to-port", harness.KeyTab)
	c.ReplaceField("port", "55432")
	c.Keys("to-ssl", harness.KeyTab) // SSL mode is a choice row: Tab moves past it
	c.Keys("to-user", harness.KeyTab)
	c.Type("user", "tdb")
	c.Keys("to-password", harness.KeyTab)
	c.Type("password", "tdbpass")
	c.Keys("to-env", harness.KeyTab) // password env row
}

// TestPGConnectionPickerShowsDatabasesAndSchemas is the regression scenario:
// the Database field offers the databases the server holds, and the Schema
// field offers the schemas of the chosen database (including "use the
// database default"), with the chosen values visible in the form.
func TestPGConnectionPickerShowsDatabasesAndSchemas(t *testing.T) {
	c := pgNewConnectionForm(t)
	pgFillServerFields(c)
	c.Shot("pg-server-fields")

	// Database: the field is a list fed by the server.
	db := c.Env.OpenPG(t)
	databases := scenario.QueryStrings(t, db,
		`SELECT datname FROM pg_catalog.pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname`)
	schemas := scenario.QueryStrings(t, db,
		`SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema' ORDER BY nspname`)

	c.Keys("to-database", harness.KeyTab)
	c.Shot("database-field")
	c.Keys("open-databases", harness.KeyDown)
	c.WaitFor(databases[len(databases)-1], 15*time.Second)
	for _, want := range databases {
		c.RequireContains(want)
	}
	c.Shot("database-list")
	c.PickFromList("databases", databases, "shop")
	c.RequireLineContains(c.RowOf("Database name"), "shop")
	c.Shot("database-chosen")

	// Schema: after choosing the database the schema list holds its schemas.
	c.Keys("to-schema", harness.KeyTab)
	c.Shot("schema-field")
	c.Keys("open-schemas", harness.KeyDown)
	c.WaitFor("public", 15*time.Second)
	for _, want := range schemas {
		c.RequireContains(want)
	}
	c.RequireContains("database default") // the "no schema named" entry
	c.Shot("schema-list")
	c.PickFromList("schemas", append([]string{""}, schemas...), "sales")
	c.Shot("schema-chosen")

	// Connect with the picked values and verify where the session works.
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn pg-form", 25*time.Second)
	c.RequireContains("db shop", "Default schema: sales")
	runSQL(c, "SELECT count(*) AS orders FROM orders;")
	c.WaitFor("500000", 60*time.Second)
	c.Shot("connected-with-picks")
}

// TestPGConnectionFormKeepsTypedValues: the lists must not be mandatory — a
// database or schema typed by hand still works (and an unknown schema is
// reported the way it was before the picker existed).
func TestPGConnectionFormKeepsTypedValues(t *testing.T) {
	c := pgNewConnectionForm(t)
	pgFillServerFields(c)
	c.Keys("to-database", harness.KeyTab)
	c.Type("database", "shop")
	c.Keys("to-schema", harness.KeyTab)
	c.Type("schema", "sales")
	c.Shot("typed-values")
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn pg-form", 25*time.Second)
	c.RequireContains("db shop", "Default schema: sales")
	// Unqualified names resolve in the typed schema.
	runSQL(c, "SELECT count(*) AS orders FROM orders;")
	c.WaitFor("500000", 60*time.Second)
	c.Shot("typed-connection")
}

// TestPGSchemelessConnectionStartsWithTheTree: with no schema chosen the session
// uses the server default and the object browser lists every user schema.
func TestPGSchemelessConnectionStartsWithTheTree(t *testing.T) {
	c := pgNewConnectionForm(t)
	pgFillServerFields(c)
	c.Keys("to-database", harness.KeyTab)
	c.Keys("open-databases", harness.KeyDown)
	c.WaitFor("shop", 15*time.Second)
	c.PickFromList("databases", []string{"legacy", "postgres", "shop"}, "shop")
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn pg-form", 25*time.Second)
	c.FocusObjects()
	c.Keys("unfold", harness.KeyRight)
	c.WaitFor("sales", 15*time.Second)
	c.RequireContains("public", "analytics", "reporting")
	c.RequireNotContains("Default schema:")
	c.Shot("tree-all-schemas")
}
