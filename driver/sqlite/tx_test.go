package sqlite

// tx_test.go pins driver.TransactionManager: statements join the open
// transaction, Commit persists its work, Rollback discards it, and neither
// leaks into the pooled connections that serve everything else.

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/p-v-q/tdb/driver"
)

// txTestCatalogTimeout bounds the catalog refresh during an open transaction:
// the test must fail fast instead of hanging when introspection and the
// transaction fight over the in-memory database's single connection.
const txTestCatalogTimeout = 5 * time.Second

// countUsers reads the real row count through a connection, so every
// assertion sees what the database actually holds.
func countUsers(t *testing.T, c driver.Connection) int {
	t.Helper()
	res, err := c.Query(context.Background(), "SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if res == nil || len(res.Rows) != 1 || res.Rows[0][0] == nil {
		t.Fatalf("unexpected count result: %+v", res)
	}
	n, err := strconv.Atoi(*res.Rows[0][0])
	if err != nil {
		t.Fatalf("count value %q: %v", *res.Rows[0][0], err)
	}
	return n
}

func TestTransactionManagerCommitAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tx.db")
	cfg := driver.Config{Name: "sqlite", DSN: path}
	conn, err := open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Query(context.Background(),
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := conn.Query(context.Background(),
		"INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	txm, ok := conn.(driver.TransactionManager)
	if !ok {
		t.Fatal("the sqlite connection must implement driver.TransactionManager")
	}
	ctx := context.Background()

	// A transaction holds its work back from other connections: a second
	// handle on the same file keeps reading the committed state.
	observer, err := open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open observer: %v", err)
	}
	t.Cleanup(func() { observer.Close() })

	if txm.InTx() {
		t.Fatal("a fresh connection has no transaction")
	}
	if err := txm.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !txm.InTx() {
		t.Fatal("Begin must open a transaction")
	}
	// A second Begin is a no-op, so a batch never has to special-case.
	if err := txm.Begin(ctx); err != nil {
		t.Fatalf("Begin on an open transaction: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (2, 'b@example.com')"); err != nil {
		t.Fatalf("insert in transaction: %v", err)
	}
	if got := countUsers(t, conn); got != 2 {
		t.Fatalf("inside the transaction the writer sees %d users, want 2", got)
	}
	if got := countUsers(t, observer); got != 1 {
		t.Fatalf("before Commit another connection sees %d users, want 1: the insert must not be committed", got)
	}
	if err := txm.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if txm.InTx() {
		t.Fatal("Commit must close the transaction")
	}
	if got := countUsers(t, observer); got != 2 {
		t.Fatalf("after Commit another connection sees %d users, want 2", got)
	}

	// Rollback discards instead.
	if err := txm.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (3, 'c@example.com')"); err != nil {
		t.Fatalf("insert in transaction: %v", err)
	}
	if err := txm.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if txm.InTx() {
		t.Fatal("Rollback must close the transaction")
	}
	if got := countUsers(t, conn); got != 2 {
		t.Fatalf("after Rollback the writer sees %d users, want 2", got)
	}

	// A mis-sequenced action surfaces instead of pretending to have worked.
	if err := txm.Commit(ctx); err == nil {
		t.Fatal("Commit without an open transaction must fail")
	}
	if err := txm.Rollback(ctx); err == nil {
		t.Fatal("Rollback without an open transaction must fail")
	}
}

// TestIntrospectionDuringOpenTransaction pins the in-memory special case: the
// pool holds a single connection, so a transaction must carry introspection
// as well or a catalog refresh would block until the context expires.
func TestIntrospectionDuringOpenTransaction(t *testing.T) {
	conn, err := open(context.Background(), driver.Config{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Query(context.Background(),
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	txm := conn.(driver.TransactionManager)
	ctx := context.Background()
	if err := txm.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := conn.Query(ctx, "INSERT INTO users VALUES (1, 'a@example.com')"); err != nil {
		t.Fatalf("insert in transaction: %v", err)
	}

	bounded, cancel := context.WithTimeout(ctx, txTestCatalogTimeout)
	defer cancel()
	cat, err := conn.Catalog(bounded)
	if err != nil {
		t.Fatalf("Catalog during an open transaction: %v", err)
	}
	if cat.FindTable("main.users") == nil {
		t.Fatal("the catalog must still list the table")
	}
	if err := txm.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}
