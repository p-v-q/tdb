package mysql

// mysql_it_test.go holds the live-server integration tests for the pre-release
// fixes: the row cap cancelling the query, the statement classifier (CALL and
// WITH-DML), index definitions (functional, descending, invisible, FULLTEXT,
// SPATIAL), and the MariaDB-specific sequence definition and RETURNING support.
//
// TDB_MYSQL_DSN points at MySQL; TDB_MARIADB_DSN (optional) at MariaDB.

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

func openMySQLIntegration(t *testing.T, ctx context.Context, dsn string) driver.Connection {
	t.Helper()
	c, err := (factory{name: mysqlName}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func mysqlIntegrationDSN(t *testing.T, env string) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(env))
	if dsn == "" {
		t.Skipf("set %s to run the integration test", env)
	}
	return dsn
}

// TestIntegrationUserTypedTransactionControl is the live proof that a
// user-typed BEGIN owns a session: BEGIN; UPDATE; ROLLBACK leaves the data
// unchanged and BEGIN; INSERT; COMMIT persists. Before the fix the BEGIN was
// handed back to the pool between statements and each DML autocommitted.
func TestIntegrationUserTypedTransactionControl(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)
	txm, ok := c.(driver.TransactionManager)
	if !ok {
		t.Fatal("the mysql connection must implement driver.TransactionManager")
	}

	table := fmt.Sprintf("tdb_it_rawtx_%d", time.Now().UnixNano())
	qualified := quoteIdent(table)
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+" (id INT PRIMARY KEY, note VARCHAR(40))"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE: %v", err)
		}
	})
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" VALUES (1, 'original')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	note := func() string {
		t.Helper()
		res, err := c.Query(ctx, "SELECT note FROM "+qualified+" WHERE id = 1")
		if err != nil {
			t.Fatalf("read note: %v", err)
		}
		if len(res.Rows) != 1 || res.Rows[0][0] == nil {
			t.Fatalf("note result = %+v", res.Rows)
		}
		return *res.Rows[0][0]
	}

	if _, err := c.Query(ctx, "BEGIN;"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("a user-typed BEGIN must open the driver transaction")
	}
	if _, err := c.Query(ctx, "UPDATE "+qualified+" SET note = 'rolled back' WHERE id = 1"); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if _, err := c.Query(ctx, "  -- discard\n ROLLBACK ;"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if txm.InTx() {
		t.Fatal("ROLLBACK must close the transaction")
	}
	if got := note(); got != "original" {
		t.Fatalf("note after BEGIN/UPDATE/ROLLBACK = %q, want original: the transaction was neutralised", got)
	}

	if _, err := c.Query(ctx, "START TRANSACTION"); err != nil {
		t.Fatalf("START TRANSACTION: %v", err)
	}
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" VALUES (2, 'committed')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if _, err := c.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	if txm.InTx() {
		t.Fatal("COMMIT must close the transaction")
	}
	res, err := c.Query(ctx, "SELECT COUNT(*) FROM "+qualified)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "2" {
		t.Fatalf("row count after COMMIT = %+v, want 2", res.Rows)
	}

	// COMMIT/ROLLBACK outside a transaction are accepted by MySQL.
	for _, statement := range []string{"COMMIT", "ROLLBACK"} {
		if _, err := c.Query(ctx, statement); err != nil {
			t.Errorf("%s outside a transaction: %v", statement, err)
		}
		if txm.InTx() {
			t.Errorf("%s outside a transaction must not open one", statement)
		}
	}

	// A savepoint lets a user undo part of the transaction.
	if _, err := c.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := c.Query(ctx, "SAVEPOINT keep"); err != nil {
		t.Fatalf("SAVEPOINT: %v", err)
	}
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" VALUES (3, 'savepoint')"); err != nil {
		t.Fatalf("INSERT after SAVEPOINT: %v", err)
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
	if _, err := c.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	res, err = c.Query(ctx, "SELECT COUNT(*) FROM "+qualified)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "2" {
		t.Fatalf("row count after savepoint dance = %+v, want 2 (the savepoint insert was discarded)", res.Rows)
	}
}

// TestIntegrationRowCapCancelsTheQuery proves that a capped result stops at the
// server instead of being drained: a cross join of a thousand-row table with
// itself three times would not finish draining inside the test's deadline.
func TestIntegrationRowCapCancelsTheQuery(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)

	// A real table is needed: MySQL materialises information_schema joins, so
	// a cross join over information_schema would not stream its first row.
	table := fmt.Sprintf("tdb_it_big_%d", time.Now().UnixNano())
	qualified := quoteIdent(table)
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+
		" AS SELECT ROW_NUMBER() OVER () AS id FROM information_schema.COLUMNS LIMIT 1000"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE: %v", err)
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
	// The pool must recover from the cancelled connection.
	res, err = c.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("connection unusable after the capped query: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" {
		t.Fatalf("SELECT 1 after the capped query = %+v", res.Rows)
	}
}

// TestIntegrationCallReturnsRows pins that CALL goes through the query path: a
// stored procedure's result set must reach the grid instead of being discarded
// by the exec path.
func TestIntegrationCallReturnsRows(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)

	proc := fmt.Sprintf("tdb_it_proc_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE PROCEDURE "+quoteIdent(proc)+"() SELECT 42 AS answer"); err != nil {
		t.Skipf("cannot create a scratch procedure: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP PROCEDURE IF EXISTS "+quoteIdent(proc)); err != nil {
			t.Errorf("DROP PROCEDURE: %v", err)
		}
	})
	res, err := c.Query(ctx, "CALL "+quoteIdent(proc)+"()")
	if err != nil {
		t.Fatalf("CALL: %v", err)
	}
	if len(res.Columns) == 0 || len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "42" {
		t.Fatalf("CALL result = columns %+v rows %+v, want one 42", res.Columns, res.Rows)
	}

	// A parenthesised query expression starts with "(", not with SELECT: it
	// must still take the query path.
	res, err = c.Query(ctx, "(SELECT 1) UNION (SELECT 2)")
	if err != nil {
		t.Fatalf("parenthesised query: %v", err)
	}
	if len(res.Rows) != 2 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" ||
		res.Rows[1][0] == nil || *res.Rows[1][0] != "2" {
		t.Fatalf("parenthesised query rows = %+v, want 1 and 2", res.Rows)
	}
}

// TestIntegrationWithDMLKeepsAffectedRows pins that a data-modifying CTE is
// executed on the exec path, where the affected-row count is available.
func TestIntegrationWithDMLKeepsAffectedRows(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)

	table := fmt.Sprintf("tdb_it_with_%d", time.Now().UnixNano())
	qualified := quoteIdent(table)
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+" (a INT)"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE: %v", err)
		}
	})
	if _, err := c.Query(ctx, "INSERT INTO "+qualified+" (a) VALUES (1)"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := c.Query(ctx, "WITH x AS (SELECT 1 AS a) UPDATE "+qualified+" SET a = a + (SELECT a FROM x)")
	if err != nil {
		t.Fatalf("WITH UPDATE: %v", err)
	}
	if res.Affected != 1 {
		t.Errorf("WITH UPDATE affected = %d, want 1", res.Affected)
	}
	res, err = c.Query(ctx, "WITH x AS (SELECT 1 AS a) DELETE FROM "+qualified+" WHERE a IN (SELECT a + 1 FROM x)")
	if err != nil {
		t.Fatalf("WITH DELETE: %v", err)
	}
	if res.Affected != 1 {
		t.Errorf("WITH DELETE affected = %d, want 1", res.Affected)
	}
	// MySQL spells a data-modifying CTE for INSERT as INSERT … WITH …, so it
	// starts with INSERT and takes the exec path anyway.
	res, err = c.Query(ctx, "INSERT INTO "+qualified+" WITH x AS (SELECT 3 AS a) SELECT a FROM x")
	if err != nil {
		t.Fatalf("INSERT ... WITH: %v", err)
	}
	if res.Affected != 1 {
		t.Errorf("INSERT ... WITH affected = %d, want 1", res.Affected)
	}
}

// TestIntegrationIndexDefinitionLive exercises indexDefinition against a real
// server: a functional index, a descending index, an invisible index, a
// FULLTEXT index and a SPATIAL index must all produce a statement that the
// server accepts again (the definition is dropped and re-created from it).
func TestIntegrationIndexDefinitionLive(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)
	schema := c.(*conn).dbName
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the mysql connection must implement driver.DefinitionProvider")
	}

	table := fmt.Sprintf("tdb_it_idx_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE TABLE "+quoteIdent(table)+` (
		a INT,
		b VARCHAR(20),
		c TEXT,
		g GEOMETRY NOT NULL,
		KEY idx_func ((a + 1)),
		KEY idx_desc (b DESC),
		KEY idx_inv (a) INVISIBLE,
		FULLTEXT KEY ft_c (c),
		SPATIAL KEY sp_g (g)
	)`); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+quoteIdent(table)); err != nil {
			t.Errorf("DROP TABLE: %v", err)
		}
	})

	cases := []struct {
		index string
		want  []string
	}{
		{"idx_func", []string{"CREATE INDEX `idx_func`", "((`a` + 1))"}},
		{"idx_desc", []string{"CREATE INDEX `idx_desc`", "`b` DESC"}},
		{"idx_inv", []string{"CREATE INDEX `idx_inv`", "INVISIBLE"}},
		{"ft_c", []string{"CREATE FULLTEXT INDEX `ft_c`", "(`c`)"}},
		{"sp_g", []string{"CREATE SPATIAL INDEX `sp_g`", "(`g`)"}},
	}
	for _, tc := range cases {
		ddl, err := prov.Definition(ctx, driver.DefinitionRef{
			Kind: catalog.KindIndex, Schema: schema, Name: tc.index, Owner: table,
		})
		if err != nil {
			t.Errorf("Definition(%s): %v", tc.index, err)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(ddl, want) {
				t.Errorf("Definition(%s) = %q, want it to contain %q", tc.index, ddl, want)
			}
		}
		// The definition must be re-executable: drop the index, run the
		// returned statement, and check the index is back.
		if _, err := c.Query(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP INDEX "+quoteIdent(tc.index)); err != nil {
			t.Fatalf("DROP INDEX %s: %v", tc.index, err)
		}
		if _, err := c.Query(ctx, ddl); err != nil {
			t.Errorf("re-executing Definition(%s) %q: %v", tc.index, ddl, err)
			continue
		}
		res, err := c.Query(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND INDEX_NAME = ?`, schema, table, tc.index)
		if err != nil {
			t.Fatalf("verify %s: %v", tc.index, err)
		}
		if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] == "0" {
			t.Errorf("index %s was not re-created", tc.index)
		}
	}

	// The re-created invisible index must still be invisible.
	res, err := c.Query(ctx, `SELECT IS_VISIBLE FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND INDEX_NAME = 'idx_inv'`, schema, table)
	if err != nil {
		t.Fatalf("IS_VISIBLE: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "NO" {
		t.Errorf("re-created idx_inv visibility = %+v, want NO", res.Rows)
	}
	// The re-created descending part must still descend.
	res, err = c.Query(ctx, `SELECT COLLATION FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND INDEX_NAME = 'idx_desc'`, schema, table)
	if err != nil {
		t.Fatalf("COLLATION: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "D" {
		t.Errorf("re-created idx_desc collation = %+v, want D", res.Rows)
	}
}

// TestIntegrationMariaDBSequenceDefinition pins the MariaDB sequence
// DefinitionProvider branch: SHOW CREATE SEQUENCE supplies the statement, so
// "View definition" works for a sequence instead of reporting that the kind
// has no stored definitions.
func TestIntegrationMariaDBSequenceDefinition(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MARIADB_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)
	schema := c.(*conn).dbName
	prov := c.(driver.DefinitionProvider)

	seq := fmt.Sprintf("tdb_it_seq_%d", time.Now().UnixNano())
	if _, err := c.Query(ctx, "CREATE SEQUENCE "+quoteIdent(seq)+" START WITH 5 INCREMENT BY 2"); err != nil {
		t.Skipf("cannot create a scratch sequence: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP SEQUENCE IF EXISTS "+quoteIdent(seq)); err != nil {
			t.Errorf("DROP SEQUENCE: %v", err)
		}
	})

	cat, err := c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	s := cat.FindSchema(schema)
	if s == nil {
		t.Fatalf("schema %s missing", schema)
	}
	var found *catalog.Resource
	for _, r := range s.ResourcesOf(catalog.KindSequence) {
		if r.Name == seq {
			found = r
			break
		}
	}
	if found == nil {
		t.Fatalf("sequence %s is missing from the catalog", seq)
	}

	ddl, err := prov.Definition(ctx, driver.DefinitionRef{
		Kind: catalog.KindSequence, Schema: schema, Name: seq,
	})
	if err != nil {
		t.Fatalf("Definition(sequence): %v", err)
	}
	for _, want := range []string{"CREATE SEQUENCE", "start with 5", "increment by 2"} {
		if !strings.Contains(strings.ToLower(ddl), strings.ToLower(want)) {
			t.Errorf("Definition(sequence) = %q, want it to contain %q", ddl, want)
		}
	}
}

// TestIntegrationMariaDBReturningAndIgnoredIndex pins the MariaDB-specific
// statement forms the classifier has to route: INSERT … RETURNING must go
// through the query path, and an IGNORED index must keep that attribute in its
// definition.
func TestIntegrationMariaDBReturningAndIgnoredIndex(t *testing.T) {
	dsn := mysqlIntegrationDSN(t, "TDB_MARIADB_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := openMySQLIntegration(t, ctx, dsn)
	schema := c.(*conn).dbName
	prov := c.(driver.DefinitionProvider)

	table := fmt.Sprintf("tdb_it_mdb_%d", time.Now().UnixNano())
	qualified := quoteIdent(table)
	if _, err := c.Query(ctx, "CREATE TABLE "+qualified+" (id INT PRIMARY KEY, note VARCHAR(20))"); err != nil {
		t.Skipf("cannot create a scratch table: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := c.Query(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
			t.Errorf("DROP TABLE: %v", err)
		}
	})

	res, err := c.Query(ctx, "INSERT INTO "+qualified+" (id, note) VALUES (1, 'a') RETURNING id, note")
	if err != nil {
		t.Fatalf("INSERT ... RETURNING: %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 2 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" {
		t.Fatalf("INSERT ... RETURNING rows = %+v, want the returned row", res.Rows)
	}
	res, err = c.Query(ctx, "DELETE FROM "+qualified+" WHERE id = 1 RETURNING note")
	if err != nil {
		t.Fatalf("DELETE ... RETURNING: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "a" {
		t.Fatalf("DELETE ... RETURNING rows = %+v, want the deleted row's note", res.Rows)
	}

	if _, err := c.Query(ctx, "ALTER TABLE "+qualified+" ADD INDEX `idx_ign` (note) IGNORED"); err != nil {
		t.Skipf("this MariaDB does not support IGNORED indexes: %v", err)
	}
	ddl, err := prov.Definition(ctx, driver.DefinitionRef{
		Kind: catalog.KindIndex, Schema: schema, Name: "idx_ign", Owner: table,
	})
	if err != nil {
		t.Fatalf("Definition(ignored index): %v", err)
	}
	if !strings.Contains(ddl, "IGNORED") {
		t.Errorf("Definition(ignored index) = %q, want it to keep IGNORED", ddl)
	}
}
