// Package scenario provides the shared plumbing for TDB integration scenarios:
// environment discovery, session startup with seeded settings, artifact
// snapshots, assertions that show the screen on failure, and independent
// database verification connections.
package scenario

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Env describes one test environment: where the binary is, where artifacts go,
// and how to reach the databases.
type Env struct {
	Root        string
	Bin         string
	ArtRoot     string
	Datasets    string
	PGDSN       string
	MySQLDSN    string
	MariaDSN    string
	SQLiteShop  string
	SQLiteWeird string
}

// LoadEnv discovers the repository root and database endpoints. DSNs can be
// overridden with TDB_IT_* variables.
func LoadEnv() (*Env, error) {
	root := os.Getenv("TDB_IT_ROOT")
	if root == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		for {
			_, main := os.Stat(filepath.Join(dir, "cmd", "tdb", "main.go"))
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && main == nil {
				root = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return nil, fmt.Errorf("repository root (with cmd/tdb/main.go) not found from %s", dir)
			}
			dir = parent
		}
	}
	e := &Env{
		Root:        root,
		Bin:         envOr("TDB_IT_BIN", filepath.Join(root, "bin", "tdb.exe")),
		ArtRoot:     envOr("TDB_IT_ART", filepath.Join(root, ".artifacts", "itest")),
		Datasets:    filepath.Join(root, ".itest", "datasets"),
		PGDSN:       envOr("TDB_IT_PG_DSN", "postgres://tdb:tdbpass@127.0.0.1:55432/shop?sslmode=disable"),
		MySQLDSN:    envOr("TDB_IT_MYSQL_DSN", "mysql://tdb:tdbpass@127.0.0.1:53306/shop"),
		MariaDSN:    envOr("TDB_IT_MARIA_DSN", "mysql://tdb:tdbpass@127.0.0.1:53307/shop"),
		SQLiteShop:  filepath.Join(root, ".dbs", "sqlite", "shop.db"),
		SQLiteWeird: filepath.Join(root, ".dbs", "sqlite", "weird.db"),
	}
	if err := os.MkdirAll(e.ArtRoot, 0o755); err != nil {
		return nil, err
	}
	return e, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// SavedConnection is a settings.Connection shaped record for seeded settings.
type SavedConnection struct {
	Name        string `json:"name"`
	Driver      string `json:"driver"`
	DSN         string `json:"dsn,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	PasswordEnv string `json:"passwordEnv,omitempty"`
	Database    string `json:"database,omitempty"`
	File        string `json:"file,omitempty"`
	Schema      string `json:"schema,omitempty"`
	SSLMode     string `json:"sslMode,omitempty"`
}

// settingsDoc is a minimal but valid settings file.
type settingsDoc struct {
	Version     int               `json:"version"`
	Locale      string            `json:"locale"`
	Theme       string            `json:"theme"`
	Mouse       bool              `json:"mouse"`
	Gutter      bool              `json:"gutter"`
	Icons       bool              `json:"icons"`
	ReadOnly    bool              `json:"readOnly"`
	Layout      map[string]any    `json:"layout"`
	Connections []SavedConnection `json:"connections,omitempty"`
}

// WriteSettings writes a settings file and returns its path.
func (e *Env) WriteSettings(t *testing.T, name string, conns ...SavedConnection) string {
	t.Helper()
	dir := filepath.Join(e.ArtRoot, t.Name(), "settings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	doc := settingsDoc{
		Version: 1,
		Locale:  "en",
		Theme:   "dark",
		Mouse:   true,
		Gutter:  true,
		Layout: map[string]any{
			"objectsVisible": true,
			"objectsWidth":   34,
			"resultsVisible": true,
			"resultsHeight":  14,
		},
		Connections: conns,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

// StandardConnections returns saved connections for every test database.
func (e *Env) StandardConnections() []SavedConnection {
	return []SavedConnection{
		{Name: "pg-shop", Driver: "postgres", DSN: e.PGDSN},
		{Name: "mysql-shop", Driver: "mysql", DSN: e.MySQLDSN},
		{Name: "maria-shop", Driver: "mysql", DSN: e.MariaDSN},
		{Name: "sqlite-shop", Driver: "sqlite", File: e.SQLiteShop},
	}
}

// OpenPG opens an independent verification connection to PostgreSQL.
func (e *Env) OpenPG(t *testing.T) *sql.DB {
	t.Helper()
	return openDB(t, "pgx", nativePG(e.PGDSN))
}

// OpenMySQL opens an independent verification connection to MySQL.
func (e *Env) OpenMySQL(t *testing.T) *sql.DB {
	t.Helper()
	return openDB(t, "mysql", nativeMySQL(e.MySQLDSN))
}

// OpenMaria opens an independent verification connection to MariaDB.
func (e *Env) OpenMaria(t *testing.T) *sql.DB {
	t.Helper()
	return openDB(t, "mysql", nativeMySQL(e.MariaDSN))
}

// OpenSQLite opens an independent verification connection to a SQLite file.
func (e *Env) OpenSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	return openDB(t, "sqlite", path)
}

func openDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", driver, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping %s: %v", driver, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// nativePG converts a postgres:// URL into a pgx stdlib DSN (the URL itself).
func nativePG(url string) string { return url }

// nativeMySQL converts a TDB mysql:// URL into the go-sql-driver form.
func nativeMySQL(url string) string {
	s := strings.TrimPrefix(url, "mysql://")
	// user:pass@host:port/db
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return s
	}
	userinfo, hostpart := s[:at], s[at+1:]
	slash := strings.Index(hostpart, "/")
	if slash < 0 {
		return userinfo + "@tcp(" + hostpart + ")/"
	}
	return userinfo + "@tcp(" + hostpart[:slash] + ")/" + hostpart[slash+1:]
}

// QueryStrings runs a one-column query and returns its values in order.
func QueryStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows %q: %v", query, err)
	}
	return out
}

// ScalarString runs a one-row, one-column query and returns its text value.
func ScalarString(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(query).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if !s.Valid {
		return ""
	}
	return s.String
}

// ScalarInt runs a one-row, one-column numeric query.
func ScalarInt(t *testing.T, db *sql.DB, query string) int64 {
	t.Helper()
	var n sql.NullInt64
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n.Int64
}
