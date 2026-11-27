package scenarios

// conn_picker_test.go covers the server-fed database list on the MySQL/
// MariaDB side of the connection form: the same ↓ list as PostgreSQL, without a
// separate schema field (the database is the namespace there).

import (
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// openServerConnectionForm opens the connection form, sets the type to the
// driver whose label is given (Demo, MariaDB, MySQL, PostgreSQL, …) and returns
// the context with the caret on the type field.
func openServerConnectionForm(t *testing.T, name, driverLabel string) *scenario.Ctx {
	t.Helper()
	c := scenario.New(t)
	c.Start("-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	if err := c.S.MouseClickButton(10, 4, harness.MouseRight); err != nil {
		t.Fatalf("right click: %v", err)
	}
	c.WaitFor("New connection", 10*time.Second)
	c.ClickText("New connection")
	c.WaitFor("Database type", 10*time.Second)

	c.Type("name", name)
	c.Keys("to-driver", harness.KeyTab)
	// ←/→ cycle the type while the list is closed; stop on the wanted label.
	for i := 0; i < 8; i++ {
		row := c.RowOf("Database type")
		if strings.Contains(c.S.Line(row), driverLabel) {
			break
		}
		c.Keys("next-driver", harness.KeyRight)
	}
	if !strings.Contains(c.S.Line(c.RowOf("Database type")), driverLabel) {
		c.Fail("could not select the %s driver", driverLabel)
	}
	return c
}

// TestMySQLConnectionPickerShowsDatabases: the MySQL/MariaDB form reads the
// databases from the server and the picked one is used by the session.
func TestMySQLConnectionPickerShowsDatabases(t *testing.T) {
	c := openServerConnectionForm(t, "mysql-form", "MySQL")

	// Server coordinates: the defaults are localhost:3306, replaced here.
	// Field order: Host, Port, SSL mode, User, Password, Password env.
	c.Keys("to-host", harness.KeyTab)
	c.ReplaceField("host", "127.0.0.1")
	c.Keys("to-port", harness.KeyTab)
	c.ReplaceField("port", "53306")
	c.Keys("to-ssl", harness.KeyTab) // SSL mode is a choice: Tab moves past it
	c.Keys("to-user", harness.KeyTab)
	c.Type("user", "tdb")
	c.Keys("to-password", harness.KeyTab)
	c.Type("password", "tdbpass")
	c.Keys("to-env", harness.KeyTab) // password env row

	db := c.Env.OpenMySQL(t)
	databases := scenario.QueryStrings(t, db,
		`SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
		  WHERE SCHEMA_NAME NOT IN ('information_schema','performance_schema','mysql','sys')
		  ORDER BY SCHEMA_NAME`)

	// Database: ↓ reads the list from the server.
	c.Keys("to-database", harness.KeyTab)
	c.Shot("mysql-database-field")
	c.Keys("open-databases", harness.KeyDown)
	c.WaitFor(databases[len(databases)-1], 15*time.Second)
	for _, want := range databases {
		c.RequireContains(want)
	}
	c.Shot("mysql-database-list")
	c.PickFromList("databases", databases, "shop")
	c.RequireLineContains(c.RowOf("Database name"), "shop")
	c.Shot("mysql-database-chosen")

	// Connect and check the session landed in shop.
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn mysql-form", 30*time.Second)
	runSQL(c, "SELECT count(*) AS customers FROM customers;")
	c.WaitFor("150000", 60*time.Second)
	c.Shot("mysql-connected-with-pick")
}
