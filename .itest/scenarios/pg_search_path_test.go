package scenarios

// pg_search_path_test.go covers the PostgreSQL default schema as the industry
// clients treat the search_path: the chosen schema comes first, the server's
// own fallbacks stay behind it, so unqualified queries find tables in the
// chosen schema *and* in public.

import (
	"testing"
	"time"

	"tdb-itest/harness"
)

func TestPGDefaultSchemaKeepsPublicFallback(t *testing.T) {
	c := pgNewConnectionForm(t)
	pgFillServerFields(c)

	// Database and default schema, typed the way a user who knows the names
	// would: shop (the database) and sales (the schema to work in).
	c.Keys("to-database", harness.KeyTab)
	c.Type("database", "shop")
	c.Keys("to-schema", harness.KeyTab)
	c.Type("schema", "sales")
	c.Shot("form-filled")
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn pg-form", 30*time.Second)
	c.RequireContains("Default schema: sales")
	c.Shot("connected")

	// A table of the chosen schema resolves unqualified.
	runSQL(c, "SELECT count(*) AS orders FROM orders;")
	c.WaitFor("500000", 60*time.Second)
	c.Shot("sales-table")

	// So does a public table: the search_path keeps the server's fallbacks
	// behind the chosen schema instead of replacing them.
	runSQL(c, "SELECT count(*) AS customers FROM customers;")
	c.WaitFor("200000", 60*time.Second)
	c.Shot("public-table")

	// The session really works in sales; the fallback did not change it.
	runSQL(c, "SELECT current_schema() AS current;")
	c.WaitFor("sales", 30*time.Second)
	c.Shot("current-schema")
}
