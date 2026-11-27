package postgres

// postgres_it_test.go holds the live-server integration tests for the
// pre-release fixes: user-typed transaction control, verbatim json/jsonb and
// array text, timestamptz in the session's zone, the row cap cancelling the
// query, and schema-qualified index/trigger definitions.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// openPGIntegration opens the configured PostgreSQL server, skipping when the
// integration DSN is not set.
func openPGIntegration(t *testing.T, ctx context.Context, dsn string) driver.Connection {
	t.Helper()
	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func pgIntegrationDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	return dsn
}

// pgText returns the server's own text form of an expression, which is what
// the grid must show.
func pgText(t *testing.T, ctx context.Context, c driver.Connection, expression string) string {
	t.Helper()
	res, err := c.Query(ctx, "SELECT ("+expression+")::text")
	if err != nil {
		t.Fatalf("SELECT (%s)::text: %v", expression, err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil {
		t.Fatalf("text form of %s = %+v", expression, res.Rows)
	}
	return *res.Rows[0][0]
}

// pgCell runs a one-value SELECT and returns the grid text.
func pgCell(t *testing.T, ctx context.Context, c driver.Connection, query string) *string {
	t.Helper()
	res, err := c.Query(ctx, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("%s returned %d rows", query, len(res.Rows))
	}
	return res.Rows[0][0]
}

// TestIntegrationUserTypedTransactionControl is the live proof of the release
// blocker: BEGIN; UPDATE/DELETE; ROLLBACK leaves the data unchanged, and
// BEGIN; INSERT; COMMIT persists. Before the fix the BEGIN was rolled back by
// the pool and the DML committed immediately.
func TestIntegrationUserTypedTransactionControl(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)
	txm, ok := c.(driver.TransactionManager)
	if !ok {
		t.Fatal("the postgres connection must implement driver.TransactionManager")
	}

	table := fmt.Sprintf("tdb_it_rawtx_%d", time.Now().UnixNano())
	qualified := "public." + table
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+" (id int PRIMARY KEY, note text)"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE %s: %v", qualified, err)
		}
	})
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" VALUES (1, 'original')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if txm.InTx() {
		t.Fatal("a fresh connection has no transaction")
	}

	// BEGIN; UPDATE; ROLLBACK must discard the update.
	if _, err := c.Query(ctx, "BEGIN;"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("a user-typed BEGIN must open the driver transaction")
	}
	upd, err := c.Query(ctx, "UPDATE "+qualified+" SET note = 'rolled back' WHERE id = 1")
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if upd.Affected != 1 {
		t.Fatalf("UPDATE affected = %d, want 1", upd.Affected)
	}
	if _, err := c.Query(ctx, "  -- discard it\n ROLLBACK ;"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if txm.InTx() {
		t.Fatal("ROLLBACK must close the transaction")
	}
	if got := *pgCell(t, ctx, c, "SELECT note FROM "+qualified+" WHERE id = 1"); got != "original" {
		t.Fatalf("note after BEGIN/UPDATE/ROLLBACK = %q, want original: the transaction was neutralised", got)
	}

	// BEGIN; DELETE; ROLLBACK must keep the row.
	if _, err := c.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := c.Query(ctx, "DELETE FROM "+qualified+" WHERE id = 1"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if _, err := c.Query(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if got := *pgCell(t, ctx, c, "SELECT count(*)::text FROM "+qualified); got != "1" {
		t.Fatalf("row count after BEGIN/DELETE/ROLLBACK = %s, want 1", got)
	}

	// BEGIN; INSERT; COMMIT must persist.
	if _, err := c.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" VALUES (2, 'committed')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("the insert belongs to the open transaction")
	}
	if _, err := c.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	if txm.InTx() {
		t.Fatal("COMMIT must close the transaction")
	}
	if got := *pgCell(t, ctx, c, "SELECT count(*)::text FROM "+qualified); got != "2" {
		t.Fatalf("row count after BEGIN/INSERT/COMMIT = %s, want 2", got)
	}

	// START TRANSACTION and END are the same boundaries under other names.
	if _, err := c.Query(ctx, "START TRANSACTION"); err != nil {
		t.Fatalf("START TRANSACTION: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("START TRANSACTION must open a transaction")
	}
	if _, err := c.Query(ctx, "END"); err != nil {
		t.Fatalf("END: %v", err)
	}
	if txm.InTx() {
		t.Fatal("END must commit and close the transaction")
	}

	// Outside a transaction PostgreSQL accepts COMMIT/ROLLBACK with a warning;
	// neither is an error and neither may open a transaction.
	for _, statement := range []string{"COMMIT", "ROLLBACK"} {
		if _, err := c.Query(ctx, statement); err != nil {
			t.Errorf("%s outside a transaction: %v", statement, err)
		}
		if txm.InTx() {
			t.Errorf("%s outside a transaction must not open one", statement)
		}
	}

	// A SAVEPOINT outside a transaction is the server's error to report.
	if _, err := c.Query(ctx, "SAVEPOINT orphan"); err == nil {
		t.Error("SAVEPOINT outside a transaction must fail")
	}
	if txm.InTx() {
		t.Error("a failed SAVEPOINT must not leave a transaction open")
	}

	// A savepoint inside the transaction does not close it.
	if _, err := c.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := c.Query(ctx, "SAVEPOINT keep"); err != nil {
		t.Fatalf("SAVEPOINT: %v", err)
	}
	if _, err := c.Query(ctx, "ROLLBACK TO SAVEPOINT keep"); err != nil {
		t.Fatalf("ROLLBACK TO: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("ROLLBACK TO SAVEPOINT must keep the transaction open")
	}
	if _, err := c.Query(ctx, "RELEASE SAVEPOINT keep"); err != nil {
		t.Fatalf("RELEASE: %v", err)
	}
	if _, err := c.Query(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
}

// TestIntegrationValuesKeepTheServerText pins that json/jsonb and array values
// reach the grid as the server's text, not as a decode/re-encode round trip
// through Go: big integers, 1.0, 1e2, key order and array dimensions/bounds all
// survive, and NULL stays NULL.
func TestIntegrationValuesKeepTheServerText(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)

	expressions := []string{
		`'{"big":12345678901234567890,"one":1.0,"exp":1e2}'::jsonb`,
		`'{"z": 1, "a": [true, null], "big": 12345678901234567890, "one": 1.0}'::json`,
		`'{{1,2},{3,4}}'::int[]`,
		`'[0:1]={5,6}'::int[]`,
		`'{"a",NULL,"c,d"}'::text[]`,
		`'{"{\"big\":12345678901234567890}"}'::jsonb[]`,
	}
	for _, expression := range expressions {
		want := pgText(t, ctx, c, expression)
		got := pgCell(t, ctx, c, "SELECT "+expression)
		if got == nil {
			t.Fatalf("%s came back NULL", expression)
		}
		if *got != want {
			t.Errorf("%s: grid = %q, server text = %q", expression, *got, want)
		}
	}

	// SQL NULL stays NULL for a jsonb column.
	if got := pgCell(t, ctx, c, "SELECT NULL::jsonb"); got != nil {
		t.Fatalf("NULL::jsonb = %q, want NULL", *got)
	}

	// float8 Infinity/NaN use PostgreSQL's spelling, not Go's +Inf.
	for expression, want := range map[string]string{
		`'Infinity'::float8`:  "Infinity",
		`'-Infinity'::float8`: "-Infinity",
		`'NaN'::float8`:       "NaN",
	} {
		got := pgCell(t, ctx, c, "SELECT "+expression)
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %q", expression, got, want)
		}
	}
}

// TestIntegrationTimestamptzUsesTheSessionZone pins that a timestamptz is
// rendered the way the server prints it in the session's time zone, with an
// explicit offset, instead of always in UTC with a Z suffix.
func TestIntegrationTimestamptzUsesTheSessionZone(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set the session TimeZone for every connection of the pool through a
	// startup parameter, exactly like a user connecting with a zone option.
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	c := openPGIntegration(t, ctx, dsn+sep+"timezone=Asia%2FShanghai")
	zone := pgCell(t, ctx, c, "SHOW TimeZone")
	if zone == nil || !strings.Contains(*zone, "Shanghai") {
		t.Fatalf("TimeZone = %v, want the session zone to be Asia/Shanghai", zone)
	}

	const expression = `'2024-01-02 11:00:00+08'::timestamptz`
	want := pgText(t, ctx, c, expression)
	got := pgCell(t, ctx, c, "SELECT "+expression)
	if got == nil {
		t.Fatal("timestamptz came back NULL")
	}
	if *got != want {
		t.Errorf("timestamptz grid = %q, server text = %q", *got, want)
	}
	if !strings.Contains(*got, "+08") {
		t.Errorf("timestamptz grid = %q, want an explicit +08 offset", *got)
	}
	if strings.HasSuffix(*got, "Z") {
		t.Errorf("timestamptz grid = %q, must not be forced to UTC", *got)
	}
}

// TestIntegrationRowCapCancelsTheQuery proves that hitting the row cap stops
// the server-side statement instead of draining it: a cross join of a
// ten-thousand-row table with itself three times produces effectively unbounded
// rows, and draining it would not finish inside the test's deadline.
func TestIntegrationRowCapCancelsTheQuery(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)

	table := fmt.Sprintf("tdb_it_big_%d", time.Now().UnixNano())
	qualified := "public." + table
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+" AS SELECT g AS id FROM generate_series(1, 10000) g"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE %s: %v", qualified, err)
		}
	})

	start := time.Now()
	res, err := c.Query(ctx, "SELECT a.id FROM "+qualified+" a, "+qualified+" b, "+qualified+" c")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("capped query: %v", err)
	}
	if !res.Truncated {
		t.Fatal("an effectively unbounded result must be truncated")
	}
	if len(res.Rows) != maxRows {
		t.Fatalf("rows = %d, want the cap %d", len(res.Rows), maxRows)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the capped query took %v: the rest of the result was drained", elapsed)
	}
	// The connection must be usable again after the cancellation.
	if got := pgCell(t, ctx, c, "SELECT 'ok'::text"); got == nil || *got != "ok" {
		t.Fatalf("connection unusable after the capped query: %v", got)
	}
}

// TestIntegrationAggregatesAreNotOfferedAsFunctions pins the capability fix
// for report §9: an aggregate cannot be printed by pg_get_functiondef, so it is
// left out of the catalog instead of offering a "View definition" that always
// fails. Ordinary functions are still listed.
func TestIntegrationAggregatesAreNotOfferedAsFunctions(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)

	base := fmt.Sprintf("tdb_it_agg_%d", time.Now().UnixNano())
	sfunc := base + "_sfunc"
	agg := base + "_agg"
	if _, err := c.Query(ctx, "CREATE FUNCTION public."+sfunc+"(int, int) RETURNS int LANGUAGE sql AS $$ SELECT $1 + $2 $$"); err != nil {
		t.Skipf("cannot create a scratch function: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		c.Query(ctx, "DROP AGGREGATE IF EXISTS public."+agg+"(int)")
		c.Query(ctx, "DROP FUNCTION IF EXISTS public."+sfunc+"(int, int)")
	})
	if _, err := c.Query(ctx, "CREATE AGGREGATE public."+agg+"(int) (SFUNC = public."+sfunc+", STYPE = int)"); err != nil {
		t.Skipf("cannot create a scratch aggregate: %v", err)
	}

	cat, err := c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	public := cat.FindSchema("public")
	if public == nil {
		t.Fatal("public schema is missing")
	}
	seen := make(map[string]bool)
	for _, r := range public.Resources {
		seen[r.Name] = true
	}
	if seen[agg] {
		t.Error("an aggregate must not be listed as a function: its definition cannot be printed")
	}
	if !seen[sfunc] {
		t.Error("the ordinary function must still be listed")
	}
}

// TestIntegrationUnloggedTableAndOwnedSequence pins two low-severity
// definition-fidelity fixes: an UNLOGGED table keeps its persistence, and a
// sequence owned by a column keeps its OWNED BY clause.
func TestIntegrationUnloggedTableAndOwnedSequence(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the postgres connection must implement driver.DefinitionProvider")
	}

	table := fmt.Sprintf("tdb_it_unlogged_%d", time.Now().UnixNano())
	qualified := "public." + table
	if _, err := c.Query(ctx, "CREATE UNLOGGED TABLE "+qualified+" (id bigserial PRIMARY KEY, note text)"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE %s: %v", qualified, err)
		}
	})

	ddl, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: table})
	if err != nil {
		t.Fatalf("Definition(table): %v", err)
	}
	if !strings.HasPrefix(ddl, "CREATE UNLOGGED TABLE ") {
		t.Errorf("Definition(unlogged table) = %q, want it to start with CREATE UNLOGGED TABLE", ddl)
	}

	// The serial column's sequence carries its ownership.
	seq := table + "_id_seq"
	seqDDL, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindSequence, Schema: "public", Name: seq})
	if err != nil {
		t.Fatalf("Definition(sequence): %v", err)
	}
	if want := "OWNED BY public." + table + ".id"; !strings.Contains(seqDDL, want) {
		t.Errorf("Definition(owned sequence) = %q, want it to contain %q", seqDDL, want)
	}
}

// TestIntegrationDefinitionQualifiesIndexesAndTriggers pins the
// DefinitionProvider contract for indexes and triggers: like table and view
// definitions they must be schema-qualified even when the session's
// search_path makes the objects visible.
func TestIntegrationDefinitionQualifiesIndexesAndTriggers(t *testing.T) {
	dsn := pgIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openPGIntegration(t, ctx, dsn)
	if _, ok := c.(driver.DefinitionProvider); !ok {
		t.Fatal("the postgres connection must implement driver.DefinitionProvider")
	}

	schema := fmt.Sprintf("tdb_it_def_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("cannot create a scratch schema: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("DROP SCHEMA %s: %v", schema, err)
		}
	})
	table := "t"
	index := "t_idx"
	triggerFn := "t_touch"
	trigger := "t_trg"
	for _, statement := range []string{
		"CREATE TABLE " + schema + "." + table + " (id int PRIMARY KEY, note text)",
		"CREATE INDEX " + index + " ON " + schema + "." + table + " (note)",
		"CREATE FUNCTION " + schema + "." + triggerFn + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
		"CREATE TRIGGER " + trigger + " BEFORE UPDATE ON " + schema + "." + table +
			" FOR EACH ROW EXECUTE FUNCTION " + schema + "." + triggerFn + "()",
	} {
		if _, err := c.Query(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	// The default search_path does not hold the scratch schema, so the server
	// qualifies the relation anyway; the real regression is the second
	// connection, whose search_path makes the objects visible.
	withPath, err := (factory{}).Open(ctx, driver.Config{DSN: withSearchPath(dsn, schema)})
	if err != nil {
		t.Fatalf("Open with a search_path: %v", err)
	}
	defer withPath.Close()
	pathProv := withPath.(driver.DefinitionProvider)
	res, err := withPath.Query(ctx, "SHOW search_path")
	if err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0][0] == nil || !strings.Contains(*res.Rows[0][0], schema) {
		t.Fatalf("search_path = %+v, want it to hold the scratch schema", res.Rows)
	}

	indexDDL, err := pathProv.Definition(ctx, driver.DefinitionRef{
		Kind: catalog.KindIndex, Schema: schema, Name: index, Owner: table,
	})
	if err != nil {
		t.Fatalf("Definition(index): %v", err)
	}
	if want := "ON " + schema + "." + table; !strings.Contains(indexDDL, want) {
		t.Errorf("index definition = %q, want it to contain %q", indexDDL, want)
	}
	triggerDDL, err := pathProv.Definition(ctx, driver.DefinitionRef{
		Kind: catalog.KindTrigger, Schema: schema, Name: trigger, Owner: table,
	})
	if err != nil {
		t.Fatalf("Definition(trigger): %v", err)
	}
	if want := "ON " + schema + "." + table; !strings.Contains(triggerDDL, want) {
		t.Errorf("trigger definition = %q, want it to contain %q", triggerDDL, want)
	}
	if want := schema + "." + triggerFn + "()"; !strings.Contains(triggerDDL, want) {
		t.Errorf("trigger definition = %q, want the trigger function qualified as %q", triggerDDL, want)
	}
}
