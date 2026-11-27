package sqlite

// rawtx_test.go pins the handling of transaction-control statements the user
// types: they must own a session exactly like the UI's manual-commit mode, so a
// BEGIN starts a transaction that the following INSERT/UPDATE joins and a
// COMMIT/ROLLBACK really commits or discards it. Before the fix each statement
// took a pooled connection, so the transaction was left behind on the pool and
// later statements committed on their own.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/p-v-q/tdb/driver"
)

func openRawTxTest(t *testing.T, dsn string) driver.Connection {
	t.Helper()
	conn, err := open(context.Background(), driver.Config{Name: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Query(context.Background(),
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return conn
}

func rawTxCount(t *testing.T, conn driver.Connection) int {
	t.Helper()
	res, err := conn.Query(context.Background(), "SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil {
		t.Fatalf("unexpected count result: %+v", res)
	}
	if *res.Rows[0][0] == "0" {
		return 0
	}
	return 1 // the tests only insert one row at a time
}

func TestUserTypedTransactionControlIsHonoured(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rawtx.db")
	conn := openRawTxTest(t, path)
	txm := conn.(driver.TransactionManager)

	if txm.InTx() {
		t.Fatal("a fresh connection has no transaction")
	}

	// BEGIN; INSERT; ROLLBACK must discard the insert and say so in InTx.
	if _, err := conn.Query(ctx, "BEGIN;"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("a user-typed BEGIN must open the driver transaction")
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("INSERT inside BEGIN: %v", err)
	}
	if _, err := conn.Query(ctx, "  -- roll it all back\n ROLLBACK ;"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if txm.InTx() {
		t.Fatal("ROLLBACK must close the transaction")
	}
	if got := rawTxCount(t, conn); got != 0 {
		t.Fatal("rows changed despite ROLLBACK: the user-typed transaction was neutralised")
	}

	// BEGIN; INSERT; COMMIT must persist the insert.
	if _, err := conn.Query(ctx, "begin"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (2, 'b@example.com')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if _, err := conn.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	if txm.InTx() {
		t.Fatal("COMMIT must close the transaction")
	}
	if got := rawTxCount(t, conn); got != 1 {
		t.Fatal("the committed row is missing")
	}

	// A SQLite COMMIT/ROLLBACK without a transaction is an error from the
	// server, and must be reported instead of silently succeeding.
	if _, err := conn.Query(ctx, "COMMIT"); err == nil {
		t.Fatal("COMMIT without a transaction must fail on SQLite")
	}
	if _, err := conn.Query(ctx, "ROLLBACK"); err == nil {
		t.Fatal("ROLLBACK without a transaction must fail on SQLite")
	}
}

// TestUserTypedTransactionIsIsolatedFromOtherConnections pins that the raw
// transaction really is a session transaction: another connection does not see
// the uncommitted row, and sees it after COMMIT.
func TestUserTypedTransactionIsIsolatedFromOtherConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "isolation.db")
	conn := openRawTxTest(t, path)
	observer, err := open(ctx, driver.Config{Name: "sqlite", DSN: path})
	if err != nil {
		t.Fatalf("open observer: %v", err)
	}
	t.Cleanup(func() { observer.Close() })

	if _, err := conn.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	// A catalog refresh while the transaction is open must not steal or end it.
	if _, err := conn.Catalog(ctx); err != nil {
		t.Fatalf("Catalog during a raw transaction: %v", err)
	}
	if _, err := conn.Query(ctx, "SELECT COUNT(*) FROM users"); err != nil {
		t.Fatalf("SELECT inside the transaction: %v", err)
	}
	if got := rawTxCount(t, observer); got != 0 {
		t.Fatal("another connection sees uncommitted work")
	}
	if _, err := conn.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	res, err := observer.Query(ctx, "SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("observer count: %v", err)
	}
	if got := *res.Rows[0][0]; got != "1" {
		t.Fatalf("observer count after COMMIT = %s, want 1", got)
	}
}

// TestSavepointsStayInsideTheTransaction covers SAVEPOINT, RELEASE and
// ROLLBACK TO: they must run on the open transaction, not on a pooled
// connection of their own.
func TestSavepointsStayInsideTheTransaction(t *testing.T) {
	ctx := context.Background()
	conn := openRawTxTest(t, filepath.Join(t.TempDir(), "savepoint.db"))
	txm := conn.(driver.TransactionManager)

	if _, err := conn.Query(ctx, "BEGIN"); err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if _, err := conn.Query(ctx, "SAVEPOINT keep"); err != nil {
		t.Fatalf("SAVEPOINT: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (2, 'b@example.com')"); err != nil {
		t.Fatalf("INSERT after SAVEPOINT: %v", err)
	}
	// ROLLBACK TO is not a transaction rollback: the transaction stays open.
	if _, err := conn.Query(ctx, "ROLLBACK TO SAVEPOINT keep"); err != nil {
		t.Fatalf("ROLLBACK TO: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("ROLLBACK TO SAVEPOINT must keep the transaction open")
	}
	if _, err := conn.Query(ctx, "RELEASE SAVEPOINT keep"); err != nil {
		t.Fatalf("RELEASE: %v", err)
	}
	if _, err := conn.Query(ctx, "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	res, err := conn.Query(ctx, "SELECT id FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(res.Rows) != 1 || *res.Rows[0][0] != "1" {
		t.Fatalf("rows after savepoint dance = %+v, want only the first insert", res.Rows)
	}
}

// TestSavepointWithoutTransactionStartsOne pins the SQLite-specific case: a
// SAVEPOINT outside a transaction starts one implicitly, and the driver opens it
// on the transaction manager instead of leaking it onto a pooled connection.
func TestSavepointWithoutTransactionStartsOne(t *testing.T) {
	ctx := context.Background()
	conn := openRawTxTest(t, filepath.Join(t.TempDir(), "savepoint_implicit.db"))
	txm := conn.(driver.TransactionManager)

	if txm.InTx() {
		t.Fatal("a fresh connection has no transaction")
	}
	if _, err := conn.Query(ctx, "SAVEPOINT implicit"); err != nil {
		t.Fatalf("SAVEPOINT: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("SQLite starts a transaction at SAVEPOINT; the driver must know about it")
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if _, err := conn.Query(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if txm.InTx() {
		t.Fatal("ROLLBACK must close the implicit transaction")
	}
	if got := rawTxCount(t, conn); got != 0 {
		t.Fatal("the insert inside the implicit transaction was not rolled back")
	}
}

// TestInMemoryDSNGetsASingleConnection pins the DSN forms that name an
// in-memory database: each connection would get its own private database, so
// the pool must be forced to one connection. Missing a spelling makes a CREATE
// TABLE invisible to the next statement.
func TestInMemoryDSNGetsASingleConnection(t *testing.T) {
	dsns := []string{
		":memory:",
		"file::memory:",
		":memory:?_pragma=busy_timeout(5000)",
		"file::memory:?cache=shared",
		"file:memdb1?mode=memory&cache=shared",
		"file:memdb2?mode=memory",
		"file:memdb3?cache=shared&mode=memory",
	}
	for _, dsn := range dsns {
		dsn := dsn
		t.Run(dsn, func(t *testing.T) {
			c, err := open(context.Background(), driver.Config{Name: "sqlite", DSN: dsn})
			if err != nil {
				t.Fatalf("Open(%q): %v", dsn, err)
			}
			defer c.Close()
			stats := c.(*conn).db.Stats()
			if stats.MaxOpenConnections != 1 {
				t.Fatalf("MaxOpenConnections = %d for %q, want 1: the pool would serve separate databases",
					stats.MaxOpenConnections, dsn)
			}
			// And the database really works across statements.
			if _, err := c.Query(context.Background(), "CREATE TABLE t (x INTEGER)"); err != nil {
				t.Fatalf("CREATE TABLE: %v", err)
			}
			if _, err := c.Query(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
				t.Fatalf("INSERT: %v", err)
			}
			res, err := c.Query(context.Background(), "SELECT COUNT(*) FROM t")
			if err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" {
				t.Fatalf("count = %+v, want 1", res.Rows)
			}
		})
	}
}

// TestInMemoryDSNClassification pins isInMemory directly, including spellings
// that must NOT be treated as in memory.
func TestInMemoryDSNClassification(t *testing.T) {
	inMemory := []string{
		":memory:",
		":MEMORY:",
		" :memory: ",
		"file::memory:",
		"file::MEMORY:",
		":memory:?_pragma=busy_timeout(5000)",
		"file::memory:?cache=shared",
		"file:memdb1?mode=memory",
		"file:memdb1?MODE=Memory&cache=shared",
		"file:memdb1?cache=shared&mode=memory;foo=bar",
	}
	for _, dsn := range inMemory {
		if !isInMemory(dsn) {
			t.Errorf("isInMemory(%q) = false, want true", dsn)
		}
	}
	onDisk := []string{
		"app.db",
		"file:app.db",
		"file:app.db?cache=shared",
		"file:memdb1?cache=shared", // memory only with mode=memory or the :memory: name
		"file:mydb?mode=rwc",
		"/tmp/app.db",
		"",
	}
	for _, dsn := range onDisk {
		if isInMemory(dsn) {
			t.Errorf("isInMemory(%q) = true, want false", dsn)
		}
	}
}

// TestInMemoryDSNSurvivesConcurrentStatements exercises the classification end
// to end: a slow statement holds the single connection, and a following
// statement must still see the table created before (the old 4-connection pool
// would open a second private database and fail with "no such table").
func TestInMemoryDSNSurvivesConcurrentStatements(t *testing.T) {
	ctx := context.Background()
	c, err := open(ctx, driver.Config{Name: "sqlite", DSN: "file::memory:?_pragma=busy_timeout(5000)"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	if _, err := c.Query(ctx, "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	slowDone := make(chan error, 1)
	go func() {
		_, err := c.Query(ctx,
			`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 2000000) SELECT count(*) FROM n`)
		slowDone <- err
	}()
	// Give the slow statement time to take the single connection.
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Query(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("INSERT while another statement runs: %v", err)
	}
	if err := <-slowDone; err != nil {
		t.Fatalf("slow statement: %v", err)
	}
	res, err := c.Query(ctx, "SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" {
		t.Fatalf("count = %+v, want 1", res.Rows)
	}
}

// TestDMLClassificationForAffectedRows pins that the affected-row count is only
// read for statements that actually change rows.
func TestDMLClassificationForAffectedRows(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO t VALUES (1)":                           true,
		"insert into t values (1)":                           true,
		"UPDATE t SET x = 1":                                 true,
		"DELETE FROM t":                                      true,
		"REPLACE INTO t VALUES (1)":                          true,
		"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x": true,
		`WITH x AS (SELECT 1) DELETE FROM t WHERE x IN (SELECT * FROM x)`: true,
		"CREATE TABLE t (x INTEGER)":                                      false,
		"PRAGMA user_version = 7":                                         false,
		"WITH x AS (SELECT 1) SELECT * FROM x":                            false,
		"SELECT changes()":                                                false,
		"BEGIN":                                                           false,
	}
	for statement, want := range cases {
		if got := isDMLStatement(statement); got != want {
			t.Errorf("isDMLStatement(%q) = %v, want %v", statement, got, want)
		}
	}
}

// TestNonDMLStatementReportsNoAffectedRows reproduces the review's stale
// changes() finding: DDL and PRAGMA statements must not report the previous
// INSERT's count as their own.
func TestNonDMLStatementReportsNoAffectedRows(t *testing.T) {
	ctx := context.Background()
	conn := openRawTxTest(t, filepath.Join(t.TempDir(), "affected.db"))
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	for _, statement := range []string{
		"CREATE TABLE t2 (x INTEGER)",
		"PRAGMA user_version = 7",
		"BEGIN",
		"COMMIT",
	} {
		res, err := conn.Query(ctx, statement)
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		if res.Affected != 0 {
			t.Errorf("%s reported Affected=%d, want 0 (stale changes())", statement, res.Affected)
		}
	}
}
