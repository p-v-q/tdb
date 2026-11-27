package scenario

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// shopSchema is the small but realistic schema used by scenarios that need a
// local database without depending on the big server datasets.
var shopSchema = []string{
	`CREATE TABLE users (
		id      INTEGER PRIMARY KEY,
		email   TEXT NOT NULL UNIQUE,
		name    TEXT,
		age     INTEGER,
		city    TEXT,
		created INTEGER
	)`,
	`CREATE TABLE products (
		id       INTEGER PRIMARY KEY,
		sku      TEXT NOT NULL,
		name     TEXT NOT NULL,
		price    REAL NOT NULL CHECK (price >= 0),
		in_stock INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE orders (
		id         INTEGER PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		total      REAL NOT NULL,
		status     TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE audit_log (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		action  TEXT NOT NULL,
		payload TEXT
	)`,
	`CREATE VIEW v_order_totals AS
		SELECT u.email, COUNT(o.id) AS orders, SUM(o.total) AS spent
		FROM users u JOIN orders o ON o.user_id = u.id
		GROUP BY u.email`,
	`CREATE INDEX idx_users_email ON users(email)`,
	`CREATE INDEX idx_orders_user ON orders(user_id)`,
	`CREATE TRIGGER trg_orders_audit AFTER INSERT ON orders
		BEGIN
			INSERT INTO audit_log(action, payload) VALUES ('insert_order', NEW.id);
		END`,
	`INSERT INTO users(id, email, name, age, city, created) VALUES
		(1, 'ada@example.com',  'Ada Lovelace', 36, 'London',   1704067200),
		(2, 'bob@example.com',  NULL,           NULL, 'Berlin',  1704153600),
		(3, 'lin@example.com',  'Lin Wei',      41, 'Shanghai', 1704240000),
		(4, 'yuan@example.com', 'Yuan Zhang',   29, 'Shenzhen', 1704326400),
		(5, 'grace@example.com','Grace Hopper', 85, NULL,       1704412800)`,
	`INSERT INTO products(id, sku, name, price, in_stock) VALUES
		(1, 'SKU-001', 'Keyboard', 49.9, 120),
		(2, 'SKU-002', 'Monitor', 229.0, 15),
		(3, 'SKU-003', 'Mouse', 19.5, 300),
		(4, 'SKU-004', 'Dock', 89.0, 0)`,
	`INSERT INTO orders(id, user_id, total, status, created_at) VALUES
		(1, 1, 49.9,  'paid',     '2024-01-02T10:00:00Z'),
		(2, 1, 229.0, 'paid',     '2024-01-03T11:00:00Z'),
		(3, 2, 19.5,  'pending',  '2024-01-04T12:00:00Z'),
		(4, 3, 89.0,  'paid',     '2024-01-05T13:00:00Z'),
		(5, 3, 49.9,  'refunded', '2024-01-06T14:00:00Z'),
		(6, 4, 19.5,  'paid',     '2024-01-07T15:00:00Z'),
		(7, 4, 229.0, 'paid',     '2024-01-08T16:00:00Z'),
		(8, 5, 49.9,  'pending',  '2024-01-09T17:00:00Z')`,
}

// CreateSQLite creates a SQLite database with the given statements and returns
// its path. The database is closed before returning so TDB can open it.
func CreateSQLite(t *testing.T, dir, name string, stmts ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	path := filepath.Join(dir, name)
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			t.Fatalf("sqlite setup %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	return path
}

// ShopSQLite creates the standard scenario database in the artifact directory.
func ShopSQLite(t *testing.T, c *Ctx) string {
	t.Helper()
	return CreateSQLite(t, c.ArtifactDir(), "shop.db", shopSchema...)
}

// FreshSQLite creates an empty SQLite file.
func FreshSQLite(t *testing.T, dir, name string) string {
	t.Helper()
	return CreateSQLite(t, dir, name)
}

// SQLiteCount returns a row count from a SQLite database, for verification.
func SQLiteCount(t *testing.T, path, query string) int64 {
	t.Helper()
	db := openDB(t, "sqlite", path)
	return ScalarInt(t, db, query)
}

// MustExec runs a statement and fails the test on error.
func MustExec(t *testing.T, db *sql.DB, query string) sql.Result {
	t.Helper()
	res, err := db.Exec(query)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	return res
}

// LogSchema writes the schema of a SQLite database into the artifacts for
// debugging a failing scenario.
func LogSchema(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Sprint(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, name FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		return fmt.Sprint(err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err == nil {
			out += fmt.Sprintf("%s %s\n", typ, name)
		}
	}
	return out
}
