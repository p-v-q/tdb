package scenarios

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// TestSmokeStartup: the binary paints the full interface with the demo
// connection and exits cleanly on Ctrl+Q.
func TestSmokeStartup(t *testing.T) {
	c := scenario.New(t)
	c.Start("-d", "demo")

	c.Shot("startup")
	c.RequireContains(
		"TDB", "File", "Edit", "View", "Query", "Settings", "Help",
		"Objects", "Welcome to TDB",
		"conn demo",
	)
	c.RequireContains("F10 menu")

	c.Keys("f5-opens-tab", harness.KeyF5)
	c.RequireContains("SQL Editor")

	c.Keys("quit", harness.KeyCtrlQ)
	if err := c.S.WaitExit(10 * time.Second); err != nil {
		c.Fail("process did not exit after Ctrl+Q: %v", err)
	}
}

// TestSmokeSQLiteQuery: a file-backed SQLite database produces a result grid
// with rows and NULL rendering.
func TestSmokeSQLiteQuery(t *testing.T) {
	c := scenario.New(t)
	dbPath := filepath.Join(c.ArtifactDir(), "smoke.db")
	_ = os.Remove(dbPath) // a leftover from an earlier run must not break setup

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT, age INTEGER)`,
		`INSERT INTO users VALUES (1, 'ada@example.com', 'Ada', 36)`,
		`INSERT INTO users VALUES (2, 'bob@example.com', NULL, NULL)`,
		`INSERT INTO users VALUES (3, 'lin@example.com', 'Lin', 41)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn smoke.db", 20*time.Second)
	c.Shot("connected")

	// Replace the starter statement with the query through the real editor.
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("query", "SELECT id, email, name, age FROM users ORDER BY id;")
	c.Keys("run", harness.KeyF5)

	c.WaitFor("ada@example.com", 20*time.Second)
	c.Shot("results")
	c.RequireContains("3 rows", "bob@example.com", "lin@example.com", "NULL")
	c.RequireContains("INTEGER", "TEXT")
}
