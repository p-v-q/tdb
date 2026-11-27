package postgres

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

func TestDataSourcePrefersADSN(t *testing.T) {
	cfg := driver.Config{
		DSN:     "postgres://ada@example.test:5433/warehouse",
		Options: map[string]string{"host": "ignored", "database": "ignored"},
	}
	if got := dataSource(cfg); got != cfg.DSN {
		t.Fatalf("dataSource = %q, want the DSN %q", got, cfg.DSN)
	}
}

func TestDataSourceComposesTheFormFields(t *testing.T) {
	cfg := driver.Config{Options: map[string]string{
		"host":     "db.example",
		"port":     "5433",
		"user":     "ada",
		"password": "p@ss word",
		"database": "shop",
	}}
	want := "postgres://ada:p%40ss%20word@db.example:5433/shop"
	if got := dataSource(cfg); got != want {
		t.Fatalf("dataSource = %q, want %q", got, want)
	}
}

func TestDataSourceWithoutConfigurationLetsPGXUseDefaults(t *testing.T) {
	if got := dataSource(driver.Config{}); got != "" {
		t.Fatalf("dataSource = %q, want an empty string", got)
	}
	if got := dataSource(driver.Config{Options: map[string]string{}}); got != "" {
		t.Fatalf("dataSource = %q, want an empty string", got)
	}
}

func TestHostPort(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"localhost", "5432", "localhost:5432"},
		{"db.example", "5433", "db.example:5433"},
		{"db.example:5434", "5433", "db.example:5434"},
		{"::1", "5432", "[::1]:5432"},
		{"[::1]:5434", "5432", "[::1]:5434"},
	}
	for _, tc := range cases {
		if got := hostPort(tc.host, tc.port); got != tc.want {
			t.Errorf("hostPort(%q, %q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestDisplayNameAndEndpoint(t *testing.T) {
	pc, err := pgxpool.ParseConfig("postgres://ada:secret@db.example:5433/shop")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := displayName(pc); got != "shop" {
		t.Fatalf("displayName = %q, want shop", got)
	}
	if got := endpoint(pc); got != "db.example:5433/shop" {
		t.Fatalf("endpoint = %q", got)
	}
	if strings.Contains(endpoint(pc), "secret") {
		t.Fatal("the endpoint shown in errors must not contain the password")
	}
}

func TestFormatValueUsesPostgresTextForm(t *testing.T) {
	c := &conn{types: pgtype.NewMap()}
	cases := []struct {
		name string
		oid  uint32
		v    any
		want string
	}{
		{"int", pgtype.Int4OID, int32(42), "42"},
		{"bool", pgtype.BoolOID, true, "true"},
		{"text", pgtype.TextOID, "hello", "hello"},
		{"numeric", pgtype.NumericOID, pgtype.Numeric{Int: big.NewInt(12345), Exp: -2, Valid: true}, "123.45"},
		{"date", pgtype.DateOID, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), "2024-01-02"},
	}
	for _, tc := range cases {
		if got := c.formatValue(tc.oid, tc.v); got != tc.want {
			t.Errorf("%s: formatValue = %q, want %q", tc.name, got, tc.want)
		}
	}
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := c.formatValue(pgtype.TimestamptzOID, ts); !strings.Contains(got, "2024-01-02") {
		t.Errorf("timestamp = %q, want a 2024-01-02 text form", got)
	}
}

// TestFormatValueMapsInfinities pins that float infinity is printed the way
// PostgreSQL prints it, not the way Go's fmt prints it ("+Inf"/"-Inf").
func TestFormatValueMapsInfinities(t *testing.T) {
	c := &conn{types: pgtype.NewMap()}
	cases := []struct {
		name string
		oid  uint32
		v    any
		want string
	}{
		{"float8 +Infinity", pgtype.Float8OID, math.Inf(1), "Infinity"},
		{"float8 -Infinity", pgtype.Float8OID, math.Inf(-1), "-Infinity"},
		{"float8 NaN", pgtype.Float8OID, math.NaN(), "NaN"},
		{"float4 +Infinity", pgtype.Float4OID, float32(math.Inf(1)), "Infinity"},
		{"float4 -Infinity", pgtype.Float4OID, float32(math.Inf(-1)), "-Infinity"},
		{"float8 finite", pgtype.Float8OID, 1.5, "1.5"},
	}
	for _, tc := range cases {
		if got := c.formatValue(tc.oid, tc.v); got != tc.want {
			t.Errorf("%s: formatValue = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPrefersRawText pins which OIDs are taken from the server's text form:
// json/jsonb, every array type and timestamptz. A decode/re-encode round trip
// corrupts JSON numbers, array dimensions and the session's time zone.
func TestPrefersRawText(t *testing.T) {
	c := &conn{types: pgtype.NewMap()}
	raw := []uint32{
		pgtype.JSONOID,
		pgtype.JSONBOID,
		pgtype.TimestamptzOID,
		pgtype.Int4ArrayOID,
		pgtype.TextArrayOID,
		pgtype.JSONBArrayOID,
		pgtype.NumericArrayOID,
	}
	for _, oid := range raw {
		if !c.prefersRawText(oid) {
			t.Errorf("prefersRawText(%d) = false, want true", oid)
		}
	}
	decoded := []uint32{
		pgtype.Int4OID,
		pgtype.TextOID,
		pgtype.NumericOID,
		pgtype.TimestampOID,
		pgtype.DateOID,
		pgtype.BoolOID,
	}
	for _, oid := range decoded {
		if c.prefersRawText(oid) {
			t.Errorf("prefersRawText(%d) = true, want false", oid)
		}
	}
}

func TestFactoryIsRegistered(t *testing.T) {
	if !driver.Implemented(driverName) {
		t.Fatal("the postgres driver must replace the placeholder factory")
	}
}

// TestSearchPathStatementQuotesTheSchema pins that the default schema
// reaches PostgreSQL as an identifier: a name with a quote, a space or a
// semicolon must not be able to break out of the statement. The statement also
// keeps the server's default fallbacks ("$user" and public) after the chosen
// schema, so unqualified public tables still resolve.
func TestSearchPathStatementQuotesTheSchema(t *testing.T) {
	cases := []struct {
		schema string
		want   string
	}{
		{"public", `SET search_path TO "public", "$user"`},
		{`we"ird`, `SET search_path TO "we""ird", "$user", public`},
		{"with space", `SET search_path TO "with space", "$user", public`},
		{"drop; --", `SET search_path TO "drop; --", "$user", public`},
		{"mixed\"Case", `SET search_path TO "mixed""Case", "$user", public`},
		{"$user", `SET search_path TO "$user", public`},
	}
	for _, tc := range cases {
		if got := searchPathStatement(tc.schema); got != tc.want {
			t.Errorf("searchPathStatement(%q) = %q, want %q", tc.schema, got, tc.want)
		}
	}
}

func TestSequenceDDL(t *testing.T) {
	got := sequenceDDL("public.order_number_seq", "bigint", 1, 1, 9223372036854775807, 1, false, 1, "")
	for _, want := range []string{
		"CREATE SEQUENCE public.order_number_seq",
		" AS bigint",
		"    START WITH 1",
		"    INCREMENT BY 1",
		"    MINVALUE 1",
		"    MAXVALUE 9223372036854775807",
		"    CACHE 1",
		"    NO CYCLE;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sequenceDDL = %q, want it to contain %q", got, want)
		}
	}
	if got := sequenceDDL("s", "smallint", 5, 1, 100, 2, true, 2, ""); !strings.Contains(got, "    CYCLE;") {
		t.Errorf("a cycling sequence must say CYCLE, got %q", got)
	}
	// An owned sequence keeps its ownership: dropping the table would
	// otherwise leave the sequence behind.
	got = sequenceDDL("public.t_id_seq", "integer", 1, 1, 2147483647, 1, false, 1, "public.t.id")
	if !strings.Contains(got, "OWNED BY public.t.id;") {
		t.Errorf("owned sequence must say OWNED BY, got %q", got)
	}
}

// TestTableDefinitionQueryStaysCatalogQualified pins the assembled CREATE
// TABLE query text where a real server is not needed: catalog helpers must be
// schema-qualified so a search_path that names pg_catalog after a user schema
// cannot shadow them, exclusion constraints (contype 'x') must survive and
// sort after foreign keys, and only a real column (attgenerated = ”) may fall
// through to DEFAULT — generated columns are their own branch, STORED and
// (PostgreSQL 18+) VIRTUAL.
func TestTableDefinitionQueryStaysCatalogQualified(t *testing.T) {
	stripped := strings.ReplaceAll(tableDefinitionQuery, "pg_catalog.format(", "")
	if strings.Contains(stripped, "format(") {
		t.Error("tableDefinitionQuery calls format( unqualified; use pg_catalog.format(")
	}
	for _, want := range []string{
		"contype IN ('p', 'u', 'c', 'f', 'x')",
		"WHEN 'f' THEN 3 ELSE 4 END",
		") STORED",
		") VIRTUAL",
		"WHEN a.attgenerated = '' THEN",
	} {
		if !strings.Contains(tableDefinitionQuery, want) {
			t.Errorf("tableDefinitionQuery must contain %q, got:\n%s", want, tableDefinitionQuery)
		}
	}
}

// TestIntegrationCatalogAndQuery runs against a real server when a DSN is
// provided. It is skipped in the normal test suite because PostgreSQL is not a
// dependency of the project.
func TestIntegrationCatalogAndQuery(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	cat, err := c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if cat.FindSchema("public") == nil {
		t.Fatalf("the catalog has no public schema: %+v", cat.Schemas)
	}

	table := fmt.Sprintf("tdb_it_%d", time.Now().UnixNano())
	// Exercise every PostgreSQL resource kind the catalog exposes: indexes,
	// sequences, functions, procedures, triggers, views and materialized views.
	// Every object is schema-qualified: the test must not depend on the
	// server's search_path, which can point at a schema that does not exist.
	viewName := table + "_v"
	matViewName := table + "_mv"
	funcName := table + "_fn"
	procName := table + "_proc"
	triggerFunc := table + "_trigger_fn"
	triggerName := table + "_trg"
	seqName := table + "_seq"
	indexName := table + "_idx"
	// The definition fixture spans two tables so foreign keys, checks and
	// defaults have something real to point at, plus an empty table for the
	// no-columns-no-constraints edge.
	parentTable := table + "_parent"
	childTable := table + "_child"
	emptyTable := table + "_empty"
	if _, err := c.Query(ctx, "CREATE TABLE public."+table+" (id int PRIMARY KEY, name text NOT NULL, note text)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	// One cleanup closure keeps the dependency order explicit: the child table
	// references the parent, the trigger and view depend on the main table, and
	// the trigger depends on its trigger function.
	defer func() {
		ctx := context.Background()
		c.Query(ctx, "DROP TABLE IF EXISTS public."+childTable)
		c.Query(ctx, "DROP TABLE IF EXISTS public."+parentTable)
		c.Query(ctx, "DROP TABLE IF EXISTS public."+emptyTable)
		c.Query(ctx, "DROP TABLE IF EXISTS public."+table+" CASCADE")
		c.Query(ctx, "DROP MATERIALIZED VIEW IF EXISTS public."+matViewName)
		c.Query(ctx, "DROP FUNCTION IF EXISTS public."+triggerFunc+"()")
		c.Query(ctx, "DROP FUNCTION IF EXISTS public."+funcName+"(integer)")
		c.Query(ctx, "DROP PROCEDURE IF EXISTS public."+procName+"()")
		c.Query(ctx, "DROP SEQUENCE IF EXISTS public."+seqName)
	}()
	definitionSQL := []string{
		"CREATE TABLE public." + parentTable + " (" +
			" id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY" +
			", code text NOT NULL" +
			", email text" +
			", note text DEFAULT 'n/a'" +
			", CONSTRAINT " + parentTable + "_code_key UNIQUE (code)" +
			", CONSTRAINT " + parentTable + "_code_email_key UNIQUE (code, email)" +
			")",
		"CREATE TABLE public." + childTable + " (" +
			" id int PRIMARY KEY" +
			", parent_id bigint NOT NULL REFERENCES public." + parentTable + " (id) ON DELETE CASCADE ON UPDATE RESTRICT" +
			", qty numeric NOT NULL DEFAULT 1 CHECK (qty > 0)" +
			", price numeric(10,2) NOT NULL DEFAULT 0" +
			", total numeric GENERATED ALWAYS AS (qty * price) STORED" +
			")",
		"CREATE TABLE public." + emptyTable + " ()",
	}
	for _, stmt := range definitionSQL {
		if _, err := c.Query(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	resourceSQL := []string{
		"CREATE INDEX " + indexName + " ON public." + table + " (name)",
		"CREATE SEQUENCE public." + seqName,
		"CREATE VIEW public." + viewName + " AS SELECT id, name FROM public." + table,
		"CREATE MATERIALIZED VIEW public." + matViewName + " AS SELECT count(*) AS n FROM public." + table,
		"CREATE FUNCTION public." + funcName + "(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$ SELECT $1 + 1 $$",
		"CREATE PROCEDURE public." + procName + "() LANGUAGE sql AS $$ SELECT 1 $$",
		"CREATE FUNCTION public." + triggerFunc + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
		"CREATE TRIGGER " + triggerName + " BEFORE UPDATE ON public." + table + " FOR EACH ROW EXECUTE FUNCTION public." + triggerFunc + "()",
	}
	for _, stmt := range resourceSQL {
		if _, err := c.Query(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := c.Query(ctx, "INSERT INTO public."+table+" (id, name, note) VALUES (1, 'ada', NULL), (2, 'bob', 'hi')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	ins, err := c.Query(ctx, "INSERT INTO public."+table+" (id, name) VALUES (3, 'cy')")
	if err != nil {
		t.Fatalf("INSERT with affected count: %v", err)
	}
	if ins.Affected != 1 {
		t.Fatalf("INSERT affected = %d, want 1", ins.Affected)
	}
	if _, err := c.Query(ctx, "DELETE FROM public."+table+" WHERE id = 3"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	res, err := c.Query(ctx, "SELECT id, name, note FROM public."+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(res.Columns) != 3 || len(res.Rows) != 2 {
		t.Fatalf("result = %d columns, %d rows", len(res.Columns), len(res.Rows))
	}
	if res.Columns[0].Type != "int4" || res.Columns[1].Type != "text" {
		t.Fatalf("column types = %q, %q; want int4, text", res.Columns[0].Type, res.Columns[1].Type)
	}
	if res.Rows[0][2] != nil {
		t.Fatalf("NULL must stay NULL, got %q", *res.Rows[0][2])
	}
	if got := *res.Rows[1][1]; got != "bob" {
		t.Fatalf("name = %q, want bob", got)
	}
	if got, err := c.Query(ctx, "SELECT $1::int4 AS n", int32(7)); err != nil {
		t.Fatalf("parameterized query: %v", err)
	} else if len(got.Rows) != 1 || got.Rows[0][0] == nil || *got.Rows[0][0] != "7" {
		t.Fatalf("parameterized result = %+v", got.Rows)
	}

	// A client changes the schema while it is running. A cached statement would
	// still describe the old table here; the driver must not cache plans.
	if _, err := c.Query(ctx, "ALTER TABLE public."+table+" ADD COLUMN extra text"); err != nil {
		t.Fatalf("ALTER TABLE: %v", err)
	}
	altered, err := c.Query(ctx, "SELECT * FROM public."+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT after ALTER TABLE: %v", err)
	}
	if len(altered.Columns) != 4 {
		t.Fatalf("SELECT * after ALTER TABLE has %d columns, want 4", len(altered.Columns))
	}

	cat, err = c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog after create: %v", err)
	}
	tbl := cat.FindTable(table)
	if tbl == nil {
		t.Fatalf("the new table is missing from the catalog")
	}
	gotCols := make(map[string]*catalog.Column, len(tbl.Columns))
	for _, col := range tbl.Columns {
		gotCols[col.Name] = col
	}
	if id := gotCols["id"]; id == nil || !id.PK || id.Nullable {
		t.Fatalf("id column = %+v, want a non-null primary key", id)
	}
	if name := gotCols["name"]; name == nil || name.Nullable {
		t.Fatalf("name column = %+v, want NOT NULL", name)
	}
	if note := gotCols["note"]; note == nil || !note.Nullable {
		t.Fatalf("note column = %+v, want nullable", note)
	}

	if !cat.Capabilities.Supports(catalog.KindFunction) ||
		!cat.Capabilities.Supports(catalog.KindProcedure) ||
		!cat.Capabilities.Supports(catalog.KindTrigger) ||
		!cat.Capabilities.Supports(catalog.KindIndex) ||
		!cat.Capabilities.Supports(catalog.KindSequence) ||
		!cat.Capabilities.Supports(catalog.KindMaterializedView) {
		t.Fatalf("capabilities = %v, want every PostgreSQL resource kind", cat.Capabilities)
	}
	public := cat.FindSchema("public")
	if public == nil {
		t.Fatal("public schema disappeared")
	}
	for _, name := range []string{viewName, matViewName} {
		tbl := cat.FindTable(name)
		if tbl == nil {
			t.Fatalf("%s is missing from the relation catalog", name)
		}
		want := catalog.TypeView
		if name == matViewName {
			want = catalog.TypeMaterializedView
		}
		if tbl.Type != want {
			t.Errorf("%s type = %s, want %s", name, tbl.Type, want)
		}
	}
	resources := make(map[string]*catalog.Resource)
	for _, r := range public.Resources {
		resources[r.Name] = r
	}
	for name, want := range map[string]catalog.ResourceKind{
		funcName:    catalog.KindFunction,
		procName:    catalog.KindProcedure,
		triggerName: catalog.KindTrigger,
		indexName:   catalog.KindIndex,
		seqName:     catalog.KindSequence,
	} {
		r := resources[name]
		if r == nil {
			t.Errorf("%s is missing from the resource catalog", name)
			continue
		}
		if r.Kind != want {
			t.Errorf("%s kind = %s, want %s", name, r.Kind, want)
		}
	}
	if trg := resources[triggerName]; trg != nil {
		if trg.Owner != table {
			t.Errorf("trigger owner = %q, want %q", trg.Owner, table)
		}
		if !strings.Contains(trg.Detail, "BEFORE") || !strings.Contains(trg.Detail, "UPDATE") {
			t.Errorf("trigger detail = %q, want BEFORE UPDATE", trg.Detail)
		}
	}
	if fn := resources[funcName]; fn != nil {
		if !strings.Contains(fn.Detail, "integer") || !strings.Contains(fn.Detail, "sql") {
			t.Errorf("function detail = %q, want arguments/result/language", fn.Detail)
		}
	}

	// The DefinitionProvider must reproduce the DDL of every object kind.
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the postgres connection must implement driver.DefinitionProvider")
	}
	for _, tc := range []struct {
		ref  driver.DefinitionRef
		want []string
	}{
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: table},
			[]string{"CREATE TABLE public." + table, "PRIMARY KEY (id)", "NOT NULL"}},
		// The child table carries every constraint kind a table can name:
		// a primary key, a foreign key with its actions, a check, defaults
		// and a generated column.
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: childTable},
			[]string{
				"CREATE TABLE public." + childTable,
				"PRIMARY KEY (id)",
				"FOREIGN KEY (parent_id) REFERENCES public." + parentTable + "(id) ON UPDATE RESTRICT ON DELETE CASCADE",
				"CHECK ((qty > (0)::numeric))",
				"DEFAULT 1",
				"DEFAULT 0",
				"GENERATED ALWAYS AS ((qty * price)) STORED",
			}},
		// The parent table has an identity primary key and two unique
		// constraints, one of them composite: column order must be kept.
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: parentTable},
			[]string{
				"GENERATED BY DEFAULT AS IDENTITY",
				"PRIMARY KEY (id)",
				"UNIQUE (code, email)",
				"DEFAULT 'n/a'::text",
			}},
		// A table without columns or constraints must still print a
		// definition instead of reporting "not found".
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: emptyTable},
			[]string{"CREATE TABLE public." + emptyTable}},
		// Views are wrapped in the CREATE statement pg_get_viewdef omits,
		// so the definition is a re-executable statement. The body names
		// its table with its schema, whatever the search_path of the
		// session is.
		{driver.DefinitionRef{Kind: catalog.KindView, Schema: "public", Name: viewName},
			[]string{"CREATE VIEW " + "public." + viewName, "FROM public." + table}},
		{driver.DefinitionRef{Kind: catalog.KindMaterializedView, Schema: "public", Name: matViewName},
			[]string{"CREATE MATERIALIZED VIEW " + "public." + matViewName, "FROM public." + table}},
		// pg_get_functiondef always prints CREATE OR REPLACE, for
		// procedures too — asserting plain "CREATE FUNCTION" would fail
		// against a real server.
		{driver.DefinitionRef{Kind: catalog.KindFunction, Schema: "public", Name: funcName}, []string{"CREATE OR REPLACE FUNCTION " + "public." + funcName}},
		{driver.DefinitionRef{Kind: catalog.KindProcedure, Schema: "public", Name: procName}, []string{"CREATE OR REPLACE PROCEDURE " + "public." + procName}},
		{driver.DefinitionRef{Kind: catalog.KindTrigger, Schema: "public", Name: triggerName, Owner: table}, []string{"CREATE TRIGGER " + triggerName}},
		{driver.DefinitionRef{Kind: catalog.KindIndex, Schema: "public", Name: indexName}, []string{"CREATE INDEX " + indexName}},
		{driver.DefinitionRef{Kind: catalog.KindSequence, Schema: "public", Name: seqName}, []string{"CREATE SEQUENCE " + "public." + seqName}},
	} {
		ddl, err := prov.Definition(ctx, tc.ref)
		if err != nil {
			t.Errorf("Definition(%s %s.%s): %v", tc.ref.Kind, tc.ref.Schema, tc.ref.Name, err)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(ddl, want) {
				t.Errorf("Definition(%s %s.%s) = %q, want it to contain %q",
					tc.ref.Kind, tc.ref.Schema, tc.ref.Name, ddl, want)
			}
		}
	}
	// The materialized view is created empty: rebuilding its contents at
	// definition time could be expensive, and the reader can REFRESH later.
	if ddl, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindMaterializedView, Schema: "public", Name: matViewName}); err == nil {
		if !strings.HasSuffix(strings.TrimSpace(ddl), "WITH NO DATA") {
			t.Errorf("Definition(materialized view) = %q, want it to end with WITH NO DATA", ddl)
		}
	}
	_, err = prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: table + "_missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing object must report not found, got %v", err)
	}
}

// TestIntegrationTableDefinitionIgnoresTheSearchPath pins that the assembled
// CREATE TABLE does not change shape with the session's search_path.
//
// The regression this guards: pg_get_constraintdef qualifies the referenced
// relation only when it is not visible in the search_path. With the default
// search_path ("$user", public) a table in public printed as "REFERENCES
// parent(id)", the same table in another schema printed as "REFERENCES
// other.parent(id)" — one table, two different statements, and the
// unqualified one does not run outside the default schema. The definition
// promises a re-executable statement, so the referenced table must always
// carry its schema.
func TestIntegrationTableDefinitionIgnoresTheSearchPath(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	// A scratch schema keeps the test independent of the privileges of the
	// connected user and leaves nothing behind.
	schema := fmt.Sprintf("tdb_it_path_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("cannot create a scratch schema for the definition test: %v", err)
	}
	defer func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("DROP SCHEMA %s: %v", schema, err)
		}
	}()

	parent := "t_parent"
	child := "t_child"
	for _, stmt := range []string{
		"CREATE TABLE " + schema + "." + parent + " (id bigint PRIMARY KEY)",
		"CREATE TABLE " + schema + "." + child + " (" +
			" id int PRIMARY KEY" +
			", parent_id bigint NOT NULL REFERENCES " + schema + "." + parent + " (id) ON DELETE CASCADE" +
			")",
	} {
		if _, err := c.Query(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	definition := func(t *testing.T, conn driver.Connection) string {
		t.Helper()
		prov, ok := conn.(driver.DefinitionProvider)
		if !ok {
			t.Fatal("the postgres connection must implement driver.DefinitionProvider")
		}
		ddl, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: schema, Name: child})
		if err != nil {
			t.Fatalf("Definition(%s.%s): %v", schema, child, err)
		}
		return ddl
	}
	want := "FOREIGN KEY (parent_id) REFERENCES " + schema + "." + parent + "(id)"

	// The default search_path: the scratch schema is not in it, so the server
	// would have printed the referenced table with its schema.
	ddl := definition(t, c)
	if !strings.Contains(ddl, want) {
		t.Errorf("Definition(%s.%s) = %q, want it to contain %q", schema, child, ddl, want)
	}

	// A session whose search_path holds the scratch schema: the referenced
	// table is visible there, so the server would have printed it unqualified.
	withPath, err := (factory{}).Open(ctx, driver.Config{DSN: withSearchPath(dsn, schema)})
	if err != nil {
		t.Fatalf("Open with a search_path: %v", err)
	}
	defer withPath.Close()
	res, err := withPath.Query(ctx, "SHOW search_path")
	if err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0][0] == nil || !strings.Contains(*res.Rows[0][0], schema) {
		t.Fatalf("search_path = %+v, want it to hold the scratch schema", res.Rows)
	}
	pathDDL := definition(t, withPath)
	if !strings.Contains(pathDDL, want) {
		t.Errorf("Definition(%s.%s) with the schema in the search_path = %q, want it to contain %q",
			schema, child, pathDDL, want)
	}
	if pathDDL != ddl {
		t.Errorf("the definition depends on the search_path:\nwith the default: %q\nwith the schema: %q", ddl, pathDDL)
	}
}

// withSearchPath returns the DSN with a search_path parameter added, so a
// second connection runs with a different schema in its search path.
func withSearchPath(dsn, path string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + url.QueryEscape(path)
}

// TestIntegrationTableDefinitionLeavesTheSessionAlone pins that reading a
// definition does not change the session the other statements run in.
//
// The definition query needs an empty search_path; if that setting were left
// behind on the pooled connection, every later statement of the client would
// run with an empty search_path too — unqualified names would resolve to
// nothing, and the session would silently behave differently from the one the
// user opened.
func TestIntegrationTableDefinitionLeavesTheSessionAlone(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the postgres connection must implement driver.DefinitionProvider")
	}
	table := fmt.Sprintf("tdb_it_sess_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE TABLE public."+table+" (id int PRIMARY KEY)"); err != nil {
		t.Skipf("cannot create a table for the definition test: %v", err)
	}
	defer func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS public."+table); err != nil {
			t.Errorf("DROP TABLE %s: %v", table, err)
		}
	}()
	searchPath := func(t *testing.T) string {
		t.Helper()
		res, err := c.Query(ctx, "SHOW search_path")
		if err != nil {
			t.Fatalf("SHOW search_path: %v", err)
		}
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
			t.Fatalf("SHOW search_path returned %+v, want one text row", res.Rows)
		}
		return *res.Rows[0][0]
	}
	before := searchPath(t)
	// Read the definition a few times: the pool hands its connections around,
	// so every connection that serves a definition must give it back
	// unchanged.
	for i := 0; i < 5; i++ {
		if _, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "public", Name: table}); err != nil {
			t.Fatalf("Definition(public.%s): %v", table, err)
		}
	}
	for i := 0; i < 5; i++ {
		if after := searchPath(t); after != before {
			t.Fatalf("search_path = %q after reading a definition, want %q", after, before)
		}
	}
}

// TestIntegrationTableDefinitionEdges runs the boundary cases of the assembled
// CREATE TABLE against a real server: exclusion constraints (contype 'x'),
// tables declared without columns, and — on PostgreSQL 18 or newer — virtual
// generated columns. Every object lives in a scratch schema that is dropped in
// the end, so the test needs no privilege on a shared schema and leaves
// nothing behind. Subtests skip individually; an older server only skips the
// PostgreSQL 18 case.
func TestIntegrationTableDefinitionEdges(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the postgres connection must implement driver.DefinitionProvider")
	}

	schema := fmt.Sprintf("tdb_it_ddl_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("cannot create a scratch schema for the definition test: %v", err)
	}
	defer func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("DROP SCHEMA %s: %v", schema, err)
		}
	}()

	// definition reads the assembled DDL of a scratch table.
	definition := func(t *testing.T, name string) string {
		t.Helper()
		ddl, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: schema, Name: name})
		if err != nil {
			t.Fatalf("Definition(%s.%s): %v", schema, name, err)
		}
		return ddl
	}
	// mustExec runs a statement and reports it as context when it fails.
	mustExec := func(t *testing.T, stmt string) {
		t.Helper()
		if _, err := c.Query(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// reexecute drops the table and runs the assembled definition again: the
	// contract of driver.DefinitionProvider is a re-executable statement, so
	// the server must accept the text verbatim.
	reexecute := func(t *testing.T, name, ddl string) {
		t.Helper()
		mustExec(t, "DROP TABLE "+schema+"."+name)
		mustExec(t, ddl)
	}

	t.Run("exclusion constraint", func(t *testing.T) {
		// GiST ships a built-in operator class for range types, so the
		// exclusion constraint needs no btree_gist extension here.
		mustExec(t, "CREATE TABLE "+schema+".t_excl_parent (id int PRIMARY KEY)")
		mustExec(t, "CREATE TABLE "+schema+".t_excl ("+
			" id int PRIMARY KEY"+
			", parent_id int REFERENCES "+schema+".t_excl_parent (id)"+
			", span int4range"+
			", CONSTRAINT t_excl_span_excl EXCLUDE USING gist (span WITH &&)"+
			")")
		ddl := definition(t, "t_excl")
		for _, want := range []string{
			"PRIMARY KEY (id)",
			"FOREIGN KEY (parent_id) REFERENCES " + schema + ".t_excl_parent(id)",
			"EXCLUDE USING gist",
			"span WITH &&",
		} {
			if !strings.Contains(ddl, want) {
				t.Errorf("Definition(t_excl) = %q, want it to contain %q", ddl, want)
			}
		}
		// Exclusion constraints sort after foreign keys, so the statement
		// reads columns, then keys, then the more exotic constraints.
		if i, j := strings.Index(ddl, "FOREIGN KEY"), strings.Index(ddl, "EXCLUDE USING"); i < 0 || j < i {
			t.Errorf("Definition(t_excl) = %q, want EXCLUDE after FOREIGN KEY", ddl)
		}
		reexecute(t, "t_excl", ddl)
	})

	t.Run("tables without columns", func(t *testing.T) {
		for _, tc := range []struct{ name, create string }{
			{"t_no_cols", "CREATE TABLE " + schema + ".t_no_cols ()"},
			{"t_check_only", "CREATE TABLE " + schema + ".t_check_only (CHECK (true))"},
		} {
			mustExec(t, tc.create)
			ddl := definition(t, tc.name)
			// Before the fix the constraint part started with ",\n" even
			// when the column list was empty, rendering "(\n,\n    CONSTRAINT".
			if strings.Contains(ddl, "(\n,\n") {
				t.Errorf("Definition(%s) = %q, want no comma after the opening parenthesis", tc.name, ddl)
			}
			reexecute(t, tc.name, ddl)
		}
		if ddl := definition(t, "t_check_only"); !strings.Contains(ddl, "CHECK (true)") {
			t.Errorf("Definition(t_check_only) = %q, want the table-level CHECK", ddl)
		}
	})

	t.Run("virtual generated column", func(t *testing.T) {
		// Virtual generated columns arrived in PostgreSQL 18; the branch is
		// exercised there and only this subtest skips on older servers. On
		// servers below 18 the attgenerated='v' path in
		// tableDefinitionQuery is not executed, so its correctness there
		// rests on the documented catalog shape (attgenerated 'v',
		// expression in pg_attrdef) rather than this run.
		if v := serverVersionNum(ctx, t, c); v < 180000 {
			t.Skipf("virtual generated columns need PostgreSQL 18, the server reports %d", v)
		}
		mustExec(t, "CREATE TABLE "+schema+".t_virtual ("+
			" qty int NOT NULL"+
			", price int NOT NULL"+
			", total int GENERATED ALWAYS AS (qty * price) VIRTUAL"+
			")")
		ddl := definition(t, "t_virtual")
		if !strings.Contains(ddl, "VIRTUAL") || !strings.Contains(ddl, "qty * price") {
			t.Errorf("Definition(t_virtual) = %q, want the VIRTUAL generation expression", ddl)
		}
		if strings.Contains(ddl, "DEFAULT (qty * price)") {
			t.Errorf("Definition(t_virtual) = %q, a virtual column must not fall through to DEFAULT", ddl)
		}
		reexecute(t, "t_virtual", ddl)
	})
}

// serverVersionNum reads current_setting('server_version_num'), which encodes
// the server version as e.g. 160009 for 16.9.
func serverVersionNum(ctx context.Context, t *testing.T, c driver.Connection) int {
	t.Helper()
	res, err := c.Query(ctx, "SELECT current_setting('server_version_num')::int")
	if err != nil {
		t.Fatalf("read server_version_num: %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0] == nil {
		t.Fatalf("server_version_num = %+v, want one integer row", res.Rows)
	}
	n, err := strconv.Atoi(*res.Rows[0][0])
	if err != nil {
		t.Fatalf("server_version_num = %q: %v", *res.Rows[0][0], err)
	}
	return n
}

// TestIntegrationExtensionFunctionsAreVisible checks that functions shipped by
// an extension (pg_trgm's similarity, …) show up in the object tree: a schema
// installed with extensions must not look empty. The extension is created with
// an explicit schema because the server's search_path is not the test's to
// assume, and it is only dropped when this run created it, so an environment
// that pre-installs pg_trgm is left untouched.
func TestIntegrationExtensionFunctionsAreVisible(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	const ext = "pg_trgm"
	installed, extSchema := extensionLocation(ctx, t, c, ext)
	if !installed {
		if _, err := c.Query(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext+" SCHEMA public"); err != nil {
			t.Skipf("cannot create the %s extension (missing privileges or files?): %v", ext, err)
		}
		installed, extSchema = extensionLocation(ctx, t, c, ext)
		if !installed {
			t.Fatalf("CREATE EXTENSION %s succeeded but the extension is not in pg_extension", ext)
		}
		// The extension was created by this run: remove it afterwards.
		defer func() {
			ctx := context.Background()
			if _, err := c.Query(ctx, "DROP EXTENSION "+ext); err != nil {
				t.Errorf("DROP EXTENSION %s: %v", ext, err)
			}
		}()
	}

	cat, err := c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	s := cat.FindSchema(extSchema)
	if s == nil {
		t.Fatalf("the catalog has no schema %q where %s was installed", extSchema, ext)
	}
	found := make(map[string]*catalog.Resource)
	for _, r := range s.Resources {
		if r.Kind == catalog.KindFunction {
			found[r.Name] = r
		}
	}
	// pg_trgm's text-similarity functions are the extension's signature
	// members; word_similarity arrived in PostgreSQL 9.6, so any supported
	// server has it.
	for _, name := range []string{"similarity", "word_similarity"} {
		r := found[name]
		if r == nil {
			t.Errorf("extension function %s.%s is missing from the catalog", extSchema, name)
			continue
		}
		if !strings.Contains(r.Detail, "text") {
			t.Errorf("%s detail = %q, want it to mention the text arguments", name, r.Detail)
		}
	}
}

// extensionLocation reports whether an extension is installed and the schema
// its objects live in.
func extensionLocation(ctx context.Context, t *testing.T, c driver.Connection, ext string) (bool, string) {
	t.Helper()
	res, err := c.Query(ctx, `
SELECT n.nspname
FROM pg_catalog.pg_extension e
JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace
WHERE e.extname = $1`, ext)
	if err != nil {
		t.Fatalf("look up extension %s: %v", ext, err)
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
		return false, ""
	}
	return true, *res.Rows[0][0]
}

// queryText runs a query that returns one text value.
func queryText(t *testing.T, ctx context.Context, c driver.Connection, query string) string {
	t.Helper()
	res, err := c.Query(ctx, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
		t.Fatalf("%s returned %+v, want one text row", query, res.Rows)
	}
	return *res.Rows[0][0]
}

// TestIntegrationDefaultSchema runs against a real server when a DSN is
// provided. It pins that the connection's default schema decides where
// unqualified names resolve — on every connection of the pool, not only on the
// first one — and that a schema the database does not have leaves the
// connection on the server default instead of breaking it.
func TestIntegrationDefaultSchema(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TDB_POSTGRES_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	base, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer base.Close()

	// A scratch schema keeps the test independent of the privileges of the
	// connected user and leaves nothing behind.
	schema := fmt.Sprintf("tdb_it_schema_%d", time.Now().UnixNano())
	if _, err := base.Query(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("cannot create a scratch schema for the default-schema test: %v", err)
	}
	defer func() {
		ctx := context.Background()
		if _, err := base.Query(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("DROP SCHEMA %s: %v", schema, err)
		}
	}()
	if _, err := base.Query(ctx, "CREATE TABLE "+schema+".t_probe (id int PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := base.Query(ctx, "INSERT INTO "+schema+".t_probe VALUES (7)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	// The connection with the default schema.
	c, err := (factory{}).Open(ctx, driver.Config{
		DSN:     dsn,
		Options: map[string]string{schemaOption: schema},
	})
	if err != nil {
		t.Fatalf("Open with a default schema: %v", err)
	}
	defer c.Close()

	if got := queryText(t, ctx, c, "SELECT current_schema()"); got != schema {
		t.Fatalf("current_schema() = %q, want %q", got, schema)
	}
	// An unqualified name resolves in the default schema.
	if got := queryText(t, ctx, c, "SELECT id::text FROM t_probe"); got != "7" {
		t.Fatalf("the unqualified query returned %q, want the row of %s.t_probe", got, schema)
	}
	if got := queryText(t, ctx, c, "SHOW search_path"); !strings.Contains(got, schema) {
		t.Fatalf("search_path = %q, want it to hold %q", got, schema)
	}

	// The pool opens more connections as the workload grows, and every one of
	// them has to resolve unqualified names in the same schema.
	var wg sync.WaitGroup
	seen := make([]string, 4)
	for i := range seen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := c.Query(ctx, "SELECT current_schema() FROM pg_sleep(0.2)")
			if err != nil || len(res.Rows) == 0 || res.Rows[0][0] == nil {
				return
			}
			seen[i] = *res.Rows[0][0]
		}(i)
	}
	wg.Wait()
	for i, got := range seen {
		if got != schema {
			t.Fatalf("the session %d resolves in %q, want %q", i, got, schema)
		}
	}

	// Without the setting nothing changes: the session keeps the server's
	// search_path.
	plain, err := (factory{}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open without a default schema: %v", err)
	}
	defer plain.Close()
	if got := queryText(t, ctx, plain, "SHOW search_path"); strings.Contains(got, schema) {
		t.Fatalf("search_path = %q, want it to hold the server default", got)
	}

	// A schema the database does not have leaves the connection on the server
	// default: it stays usable, and the client reports the missing schema.
	missing := schema + "_missing"
	withMissing, err := (factory{}).Open(ctx, driver.Config{
		DSN:     dsn,
		Options: map[string]string{schemaOption: missing},
	})
	if err != nil {
		t.Fatalf("Open with a missing default schema: %v", err)
	}
	defer withMissing.Close()
	if got := queryText(t, ctx, withMissing, "SELECT 1::text"); got != "1" {
		t.Fatalf("the connection must stay usable, got %q", got)
	}
	if got := queryText(t, ctx, withMissing, "SHOW search_path"); strings.Contains(got, missing) {
		t.Fatalf("search_path = %q, want the server default", got)
	}
}
