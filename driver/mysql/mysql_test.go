package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// ---- DSN handling ----

func TestDataSourcePrefersADSN(t *testing.T) {
	cfg := driver.Config{
		DSN:     "ada:secret@tcp(db.example:3307)/shop",
		Options: map[string]string{"host": "ignored", "database": "ignored"},
	}
	mc, err := dataSource(cfg)
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.User != "ada" || mc.Passwd != "secret" || mc.Addr != "db.example:3307" || mc.DBName != "shop" {
		t.Fatalf("dataSource = %+v, want ada:secret@db.example:3307/shop", mc)
	}
}

func TestDataSourceAppliesClientDefaults(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "ada@tcp(db.example)/shop"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if !mc.ParseTime {
		t.Error("parseTime must default to true so dates arrive as dates")
	}
	dsn := mc.FormatDSN()
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("DSN %q must default to charset=utf8mb4", dsn)
	}
}

func TestDataSourceKeepsExplicitParams(t *testing.T) {
	mc, err := dataSource(driver.Config{
		DSN: "ada@tcp(db.example)/shop?parseTime=false&collation=utf8mb4_general_ci",
	})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.ParseTime {
		t.Error("parseTime=false set by the user must not be overridden")
	}
	if mc.Collation != "utf8mb4_general_ci" {
		t.Errorf("collation = %q, want the one from the DSN", mc.Collation)
	}
	if got := mc.FormatDSN(); strings.Contains(got, "charset=") {
		t.Errorf("DSN %q must not add a charset next to an explicit collation", got)
	}
}

func TestDataSourceConvertsAURLDSN(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "mariadb://ada:p%40ss@db.example/shop?parseTime=false"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.User != "ada" || mc.Passwd != "p@ss" {
		t.Errorf("user/password = %q/%q, want ada/p@ss", mc.User, mc.Passwd)
	}
	if mc.Addr != "db.example:3306" {
		t.Errorf("addr = %q, want the default port 3306", mc.Addr)
	}
	if mc.DBName != "shop" {
		t.Errorf("dbname = %q, want shop", mc.DBName)
	}
	if mc.ParseTime {
		t.Error("parseTime=false in the URL must survive the conversion")
	}
}

func TestDataSourceURLDefaultsThePort(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "mysql://localhost/shop"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.Addr != "localhost:3306" {
		t.Errorf("addr = %q, want localhost:3306", mc.Addr)
	}
	if !mc.ParseTime {
		t.Error("the URL form must apply the same parseTime default")
	}
}

func TestDataSourceComposesTheFormFields(t *testing.T) {
	cfg := driver.Config{Options: map[string]string{
		"host":     "db.example",
		"port":     "3307",
		"user":     "ada",
		"password": "p@ss word",
		"database": "shop",
	}}
	mc, err := dataSource(cfg)
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.User != "ada" || mc.Passwd != "p@ss word" || mc.Addr != "db.example:3307" || mc.DBName != "shop" {
		t.Fatalf("dataSource = %+v, want ada:p@ss word@db.example:3307/shop", mc)
	}
	// The composed DSN must survive the driver's own parser unchanged.
	round, err := gomysql.ParseDSN(mc.FormatDSN())
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round.User != mc.User || round.Passwd != mc.Passwd || round.Addr != mc.Addr || round.DBName != mc.DBName {
		t.Fatalf("round-trip = %+v, want %+v", round, mc)
	}
}

func TestDataSourceWithoutConfigurationUsesLocalDefaults(t *testing.T) {
	mc, err := dataSource(driver.Config{})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.User != "root" || mc.Addr != "localhost:3306" {
		t.Fatalf("dataSource = %+v, want root@localhost:3306", mc)
	}
}

func TestDataSourceRejectsAnInvalidDSN(t *testing.T) {
	if _, err := dataSource(driver.Config{DSN: "not a dsn"}); err == nil {
		t.Fatal("an invalid DSN must be reported, not silently ignored")
	}
}

// TestDataSourceURLParseErrorsDoNotLeakThePassword pins the fix for the
// review finding that url.Parse failures were wrapped with %w: the message
// of *url.Error echoes the whole URL — "parse \"mysql://ada:secret@…\"" —
// which travelled through the i18n connection-failure text into the UI and
// the logs. URL-shaped parse errors must never carry the original text.
func TestDataSourceURLParseErrorsDoNotLeakThePassword(t *testing.T) {
	const pass = "sup3rs3cret"
	cases := []struct{ name, dsn string }{
		{"control character", "mysql://ada:" + pass + "@db.example/shop\x00"},
		{"invalid port", "mysql://ada:" + pass + "@db.example:badport/shop"},
		{"invalid path escape", "mariadb://ada:" + pass + "@db.example/sho%zzp"},
		{"unterminated host", "mysql://ada:" + pass + "@[::1/shop"},
		{"invalid query escape", "mariadb://ada:" + pass + "@db.example/shop?sslmode=require%zz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dataSource(driver.Config{DSN: tc.dsn})
			if err == nil {
				t.Fatal("a malformed connection URL must be reported")
			}
			if strings.Contains(err.Error(), pass) {
				t.Fatalf("error %q must not contain the password", err)
			}
		})
	}
}

// mappedCfgParams lists every URL parameter the mapping claims for a Config
// field; none of them may survive as a connection attribute in mc.Params,
// because the driver sends Params to the server as opaque key/value pairs
// during the handshake — they configure nothing.
var mappedCfgParams = []string{
	"tls", "parseTime", "loc", "timeout", "readTimeout", "writeTimeout",
	"interpolateParams", "multiStatements", "clientFoundRows",
	"allowNativePasswords", "allowCleartextPasswords", "allowOldPasswords",
	"allowAllFiles", "checkConnLiveness", "maxAllowedPacket", "rejectReadOnly",
	"collation", "charset", "connectionAttributes", "serverPubKey",
	"allowFallbackToPlaintext", "columnsWithAlias", "compress", "timeTruncate",
}

// TestDataSourceURLMapsTLS pins the bug fix: tls=require used to land in
// mc.Params (a connection attribute) and TLS silently stayed off.
func TestDataSourceURLMapsTLS(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"plain bool false", "false", "false"},
		{"plain bool true", "true", "true"},
		{"skip-verify preset", "skip-verify", "skip-verify"},
		{"preferred preset", "preferred", "preferred"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := dataSource(driver.Config{DSN: "mysql://ada:secret@db.example/shop?tls=" + tc.value})
			if err != nil {
				t.Fatalf("dataSource: %v", err)
			}
			if mc.TLSConfig != tc.want {
				t.Errorf("TLSConfig = %q, want %q", mc.TLSConfig, tc.want)
			}
			if _, ok := mc.Params["tls"]; ok {
				t.Error("tls must not travel as a connection attribute in Params")
			}
			// End to end: the composed DSN must come back with TLS really
			// enabled, the way the driver's own parser resolves it.
			parsed, err := gomysql.ParseDSN(mc.FormatDSN())
			if err != nil {
				t.Fatalf("round-trip: %v", err)
			}
			if tc.want == "false" && parsed.TLS != nil {
				t.Errorf("tls=false resolved to TLS %+v, want none", parsed.TLS)
			}
			if tc.want != "false" && parsed.TLS == nil {
				t.Error("the composed DSN must resolve to TLS encryption enabled")
			}
		})
	}
}

// TestDataSourceURLMapsTLSRequireToTheLibrary documents the "require" value:
// go-sql-driver knows no "require" keyword, so it is carried as a custom TLS
// configuration name (the registry lives in the driver package) and the
// driver rejects it at connect time.
func TestDataSourceURLMapsTLSRequireToTheLibrary(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "mysql://ada:secret@db.example/shop?tls=require"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.TLSConfig != "require" {
		t.Errorf("TLSConfig = %q, want the custom name require", mc.TLSConfig)
	}
	if _, ok := mc.Params["tls"]; ok {
		t.Error("tls must not travel as a connection attribute in Params")
	}
	if _, err := gomysql.ParseDSN(mc.FormatDSN()); err == nil {
		t.Error("an unregistered custom TLS name must be rejected by the driver")
	}
}

func TestDataSourceURLMapsFieldLevelParams(t *testing.T) {
	dsn := "mysql://ada:secret@db.example/shop" +
		"?parseTime=false&loc=UTC&timeout=5s&readTimeout=2s&writeTimeout=1s" +
		"&interpolateParams=true&multiStatements=true&clientFoundRows=true" +
		"&allowNativePasswords=false&allowCleartextPasswords=true&allowOldPasswords=true" +
		"&allowAllFiles=true&checkConnLiveness=false&allowFallbackToPlaintext=true" +
		"&columnsWithAlias=true&compress=true&rejectReadOnly=true" +
		"&maxAllowedPacket=1024&timeTruncate=15m" +
		"&charset=utf8mb4&collation=utf8mb4_general_ci" +
		"&connectionAttributes=app:tdb&serverPubKey=path/to/pubkey"
	mc, err := dataSource(driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.ParseTime {
		t.Error("parseTime = true, want the URL's false")
	}
	if mc.Loc != time.UTC {
		t.Errorf("loc = %v, want UTC", mc.Loc)
	}
	if mc.Timeout != 5*time.Second || mc.ReadTimeout != 2*time.Second || mc.WriteTimeout != time.Second {
		t.Errorf("timeouts = %v/%v/%v, want 5s/2s/1s", mc.Timeout, mc.ReadTimeout, mc.WriteTimeout)
	}
	if !mc.InterpolateParams || !mc.MultiStatements || !mc.ClientFoundRows {
		t.Error("interpolateParams/multiStatements/clientFoundRows = false, want true")
	}
	if !mc.AllowCleartextPasswords || !mc.AllowOldPasswords || !mc.AllowAllFiles {
		t.Error("allow cleartext/old/allfiles = false, want true")
	}
	if mc.AllowNativePasswords || mc.CheckConnLiveness {
		t.Error("allowNativePasswords/checkConnLiveness = true, want the URL's false")
	}
	if !mc.AllowFallbackToPlaintext || !mc.ColumnsWithAlias || !mc.RejectReadOnly {
		t.Error("allowFallbackToPlaintext/columnsWithAlias/rejectReadOnly = false, want true")
	}
	if mc.MaxAllowedPacket != 1024 {
		t.Errorf("maxAllowedPacket = %d, want 1024", mc.MaxAllowedPacket)
	}
	if mc.ConnectionAttributes != "app:tdb" {
		t.Errorf("connectionAttributes = %q, want app:tdb", mc.ConnectionAttributes)
	}
	if mc.ServerPubKey != "path/to/pubkey" {
		t.Errorf("serverPubKey = %q, want path/to/pubkey", mc.ServerPubKey)
	}
	if mc.Collation != "utf8mb4_general_ci" {
		t.Errorf("collation = %q, want the one from the URL", mc.Collation)
	}
	if got := mc.FormatDSN(); !strings.Contains(got, "charset=utf8mb4") {
		t.Errorf("DSN %q must keep the URL's charset", got)
	}
	for _, key := range mappedCfgParams {
		if _, ok := mc.Params[key]; ok {
			t.Errorf("Params[%q] = %q: a Config-level parameter must not become a connection attribute", key, mc.Params[key])
		}
	}
}

func TestDataSourceURLMapsLocSpecialCases(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "mysql://ada:secret@db.example/shop?loc=Local&timeTruncate=1h"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.Loc != time.Local {
		t.Errorf("loc = %v, want Local", mc.Loc)
	}
}

func TestDataSourceURLRejectsInvalidParamValues(t *testing.T) {
	const pass = "sup3rs3cret"
	cases := []struct{ name, param string }{
		{"bad timeout", "timeout=abc"},
		{"bad parseTime", "parseTime=maybe"},
		{"bad loc", "loc=Atlantis"},
		{"bad maxAllowedPacket", "maxAllowedPacket=big"},
		{"bad readTimeout", "readTimeout=5s and a bit"},
		{"empty tls", "tls="},
		{"tls with whitespace", "tls=skip verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dataSource(driver.Config{DSN: "mysql://ada:" + pass + "@db.example/shop?" + tc.param})
			if err == nil {
				t.Fatal("an invalid parameter value must be reported, not silently ignored")
			}
			if strings.Contains(err.Error(), pass) || strings.Contains(err.Error(), "//") {
				t.Fatalf("error %q must not echo the URL or the password", err)
			}
		})
	}
}

func TestDataSourceURLKeepsUnknownParamsAsConnectionAttributes(t *testing.T) {
	mc, err := dataSource(driver.Config{DSN: "mysql://ada:secret@db.example/shop?foo=bar&n=1&tls=skip-verify"})
	if err != nil {
		t.Fatalf("dataSource: %v", err)
	}
	if mc.Params["foo"] != "bar" || mc.Params["n"] != "1" {
		t.Fatalf("Params = %v, want foo=bar and n=1 kept", mc.Params)
	}
	for _, key := range mappedCfgParams {
		if _, ok := mc.Params[key]; ok {
			t.Errorf("Params[%q] must not exist", key)
		}
	}
}

func TestHostPort(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"localhost", "3306", "localhost:3306"},
		{"db.example", "3307", "db.example:3307"},
		{"db.example:3308", "3306", "db.example:3308"},
		{"::1", "3306", "[::1]:3306"},
		{"[::1]:3308", "3306", "[::1]:3308"},
	}
	for _, tc := range cases {
		if got := hostPort(tc.host, tc.port); got != tc.want {
			t.Errorf("hostPort(%q, %q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

// ---- Values ----

func TestFormatValueRendersTextForms(t *testing.T) {
	cases := []struct {
		name, dbType string
		v            any
		want         string
	}{
		{"varchar as bytes", "VARCHAR", []byte("hello"), "hello"},
		{"string", "TEXT", "hello", "hello"},
		{"json stays text", "JSON", []byte(`{"a":1}`), `{"a":1}`},
		{"blob is hex", "BLOB", []byte{0xde, 0xad}, "0xdead"},
		{"varbinary is hex", "VARBINARY", []byte{0x00, 0xff}, "0x00ff"},
		{"bit is hex", "BIT", []byte{0x01}, "0x01"},
		{"bigint", "BIGINT", int64(42), "42"},
		{"double", "DOUBLE", 1.5, "1.5"},
		{"datetime", "DATETIME", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), "2024-01-02 03:04:05"},
		{"date", "DATE", time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), "2024-01-02"},
		{"time", "TIME", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), "03:04:05"},
	}
	for _, tc := range cases {
		if got := formatValue(tc.dbType, tc.v); got != tc.want {
			t.Errorf("%s: formatValue = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBinaryTextIsCapped(t *testing.T) {
	big := make([]byte, maxCellBytes+44)
	got := binaryText(big)
	if !strings.HasSuffix(got, fmt.Sprintf("… (%d bytes)", len(big))) {
		t.Fatalf("binaryText of a big blob = %q, want a length note", got)
	}
	if head, _, _ := strings.Cut(got, "…"); len(head) != 2+2*maxCellBytes {
		t.Fatalf("shown hex = %d chars, want %d", len(head), 2+maxCellBytes)
	}
	if got := binaryText([]byte{0xab}); got != "0xab" {
		t.Fatalf("binaryText = %q, want 0xab", got)
	}
}

// ---- Statement routing ----

func TestReturnsRows(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"SELECT 1", true},
		{"  \n select 1", true},
		{"-- comment\nSELECT 1", true},
		{"# comment\nSHOW TABLES", true},
		{"/* block */ EXPLAIN SELECT 1", true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"WITH RECURSIVE x AS (SELECT 1 UNION ALL SELECT * FROM x) SELECT * FROM x", true},
		{"WITH x AS (SELECT 1) TABLE x", true},
		{"WITH x AS (SELECT 1) VALUES ROW(1)", true},
		{"DESCRIBE t", true},
		// A stored procedure returns result sets: the exec path would discard
		// them without an error.
		{"CALL refresh_stats()", true},
		{"  call refresh_stats()", true},
		// MySQL returns result sets for these administrative statements.
		{"ANALYZE TABLE t", true},
		{"OPTIMIZE TABLE t", true},
		{"REPAIR TABLE t", true},
		{"CHECKSUM TABLE t", true},
		{"CHECK TABLE t", true},
		// A parenthesised query expression starts with "(".
		{"(SELECT 1) UNION (SELECT 2)", true},
		{"  /*x*/ (SELECT 1) UNION (SELECT 2)", true},
		// MariaDB's INSERT/UPDATE/DELETE/REPLACE … RETURNING produce rows.
		{"INSERT INTO t (a) VALUES (1) RETURNING id", true},
		{"UPDATE t SET a = 1 RETURNING id", true},
		{"DELETE FROM t WHERE a = 1 RETURNING id", true},
		{"REPLACE INTO t (a) VALUES (1) RETURNING id", true},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x RETURNING id", true},
		// A data-modifying CTE without RETURNING has an affected count, which
		// only the exec path reports.
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", false},
		{"WITH x AS (SELECT 1) UPDATE t SET a = 1", false},
		{"WITH x AS (SELECT 1) DELETE FROM t", false},
		{"WITH x AS (SELECT 1) REPLACE INTO t SELECT * FROM x", false},
		{"insert into t values (1)", false},
		{"UPDATE t SET a = 1", false},
		{"delete from t", false},
		{"CREATE TABLE t (a int)", false},
		{"SET @a = 1", false},
		{"USE shop", false},
		// A RETURNING inside a string or comment does not make the statement a
		// row-returning one.
		{"INSERT INTO t (a) VALUES ('RETURNING')", false},
		{"INSERT INTO t (a) VALUES (1) -- RETURNING id", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := returnsRows(tc.query); got != tc.want {
			t.Errorf("returnsRows(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

// TestWithMainVerb pins the CTE main-verb scan: the CTE definitions live inside
// parentheses, so a SELECT inside them must not be mistaken for the statement's
// verb.
func TestWithMainVerb(t *testing.T) {
	cases := map[string]string{
		"WITH x AS (SELECT 1) SELECT * FROM x":                           "SELECT",
		"WITH x AS (SELECT 1), y AS (UPDATE t SET a = 1) DELETE FROM t":  "DELETE",
		"WITH x (a, b) AS (VALUES (1, 2)) INSERT INTO t SELECT * FROM x": "INSERT",
		"WITH x AS (SELECT 'DELETE') SELECT * FROM x":                    "SELECT",
		"SELECT 1": "SELECT",
	}
	for query, want := range cases {
		if got := withMainVerb(query); got != want {
			t.Errorf("withMainVerb(%q) = %q, want %q", query, got, want)
		}
	}
}

// TestFormatTimeZeroDate pins that MySQL's zero date is printed the way the
// server spells it instead of as Go's zero time (0001-01-01).
func TestFormatTimeZeroDate(t *testing.T) {
	var zero time.Time
	cases := []struct {
		dbType, want string
	}{
		{"DATE", "0000-00-00"},
		{"DATETIME", "0000-00-00 00:00:00"},
		{"TIMESTAMP", "0000-00-00 00:00:00"},
		{"TIME", "00:00:00"},
	}
	for _, tc := range cases {
		if got := formatTime(tc.dbType, zero); got != tc.want {
			t.Errorf("formatTime(%s, zero) = %q, want %q", tc.dbType, got, tc.want)
		}
	}
	if got := formatTime("DATE", time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)); got != "2024-01-02" {
		t.Errorf("a real date must not be changed, got %q", got)
	}
}

// ---- Introspection merge logic ----

func TestMergeTriggers(t *testing.T) {
	rows := []triggerRow{
		{schema: "shop", name: "trg", table: "orders", timing: "BEFORE", event: "INSERT"},
		{schema: "shop", name: "trg", table: "orders", timing: "BEFORE", event: "UPDATE"},
		{schema: "shop", name: "trg", table: "orders", timing: "BEFORE", event: "INSERT"}, // dedup
		{schema: "shop", name: "del", table: "orders", timing: "AFTER", event: "DELETE"},
	}
	out := mergeTriggers(rows)
	if len(out) != 2 {
		t.Fatalf("mergeTriggers = %d entries, want 2", len(out))
	}
	if got := out[0].detail(); got != "BEFORE INSERT OR UPDATE" {
		t.Errorf("detail = %q, want BEFORE INSERT OR UPDATE", got)
	}
	if got := out[1].detail(); got != "AFTER DELETE" {
		t.Errorf("detail = %q, want AFTER DELETE", got)
	}
	if out[0].table != "orders" {
		t.Errorf("owner = %q, want orders", out[0].table)
	}
}

func TestTriggerDetailWithoutTiming(t *testing.T) {
	e := triggerEntry{events: []string{"DELETE"}}
	if got := e.detail(); got != "DELETE" {
		t.Fatalf("detail = %q, want DELETE", got)
	}
}

func TestMergeIndexes(t *testing.T) {
	rows := []indexRow{
		{schema: "shop", table: "orders", name: "PRIMARY", column: "id", indexType: "BTREE"},
		{schema: "shop", table: "orders", name: "uq_email", column: "email", indexType: "BTREE"},
		{schema: "shop", table: "orders", name: "idx_np", column: "note", nonUnique: true, indexType: "BTREE"},
		{schema: "shop", table: "orders", name: "idx_np", column: "price", nonUnique: true, indexType: "BTREE"},
	}
	out := mergeIndexes(rows)
	if len(out) != 3 {
		t.Fatalf("mergeIndexes = %d entries, want 3", len(out))
	}
	if got := out[0].detail; got != "primary key · (id)" {
		t.Errorf("PRIMARY detail = %q", got)
	}
	if got := out[1].detail; got != "unique · (email)" {
		t.Errorf("unique detail = %q", got)
	}
	if got := out[2].detail; got != "(note, price)" {
		t.Errorf("multi-column detail = %q, want the columns in index order", got)
	}
}

func TestIndexDetailWithNonBTreeType(t *testing.T) {
	if got := indexDetail("ft_body", true, "FULLTEXT"); got != "FULLTEXT" {
		t.Fatalf("fulltext detail = %q, want FULLTEXT", got)
	}
	e := indexEntry{detail: indexDetail("ft_body", true, "FULLTEXT")}
	e.addColumn("body")
	if got := e.detail; got != "FULLTEXT · (body)" {
		t.Fatalf("detail = %q, want FULLTEXT · (body)", got)
	}
}

func TestRoutineDetail(t *testing.T) {
	if got := routineDetail("int", "SQL"); got != "→ int · SQL" {
		t.Errorf("function detail = %q", got)
	}
	if got := routineDetail("", "SQL"); got != "SQL" {
		t.Errorf("procedure detail = %q", got)
	}
	if got := routineDetail("", ""); got != "" {
		t.Errorf("empty detail = %q, want empty", got)
	}
}

func TestIndexDDL(t *testing.T) {
	cases := []struct {
		name       string
		cols       []indexColumn
		uniq       bool
		typ        string
		visibility string
		want       string
	}{
		{"idx_name", []indexColumn{{name: "name"}}, false, "BTREE", "",
			"CREATE INDEX `idx_name` ON `shop`.`users` (`name`)"},
		{"users_email_key", []indexColumn{{name: "email"}}, true, "BTREE", "",
			"CREATE UNIQUE INDEX `users_email_key` ON `shop`.`users` (`email`)"},
		{"idx_multi", []indexColumn{{name: "a"}, {name: "b"}}, false, "BTREE", "",
			"CREATE INDEX `idx_multi` ON `shop`.`orders` (`a`, `b`)"},
		// A prefix index keeps its length; the PRIMARY index needs ALTER TABLE.
		{"idx_email_10", []indexColumn{{name: "email", subPart: sql.NullInt64{Int64: 10, Valid: true}}}, false, "BTREE", "",
			"CREATE INDEX `idx_email_10` ON `shop`.`users` (`email`(10))"},
		{"PRIMARY", []indexColumn{{name: "id"}}, true, "BTREE", "",
			"ALTER TABLE `shop`.`users` ADD PRIMARY KEY (`id`)"},
		// FULLTEXT/SPATIAL belong in the CREATE keyword position, not in a
		// trailing USING clause.
		{"ft_body", []indexColumn{{name: "body"}}, false, "FULLTEXT", "",
			"CREATE FULLTEXT INDEX `ft_body` ON `shop`.`docs` (`body`)"},
		{"sp_geom", []indexColumn{{name: "geom", subPart: sql.NullInt64{Int64: 32, Valid: true}}}, false, "SPATIAL", "",
			"CREATE SPATIAL INDEX `sp_geom` ON `shop`.`docs` (`geom`)"},
		// A functional index part is an expression, not a quoted column, and a
		// descending part keeps its direction.
		{"idx_expr", []indexColumn{{expression: "(`a` + 1)"}}, false, "BTREE", "",
			"CREATE INDEX `idx_expr` ON `shop`.`orders` ((`a` + 1))"},
		{"idx_desc", []indexColumn{{name: "b", descending: true}}, false, "BTREE", "",
			"CREATE INDEX `idx_desc` ON `shop`.`orders` (`b` DESC)"},
		{"idx_inv", []indexColumn{{name: "a"}}, false, "BTREE", "INVISIBLE",
			"CREATE INDEX `idx_inv` ON `shop`.`orders` (`a`) INVISIBLE"},
		{"idx_ign", []indexColumn{{name: "a"}}, false, "BTREE", "IGNORED",
			"CREATE INDEX `idx_ign` ON `shop`.`orders` (`a`) IGNORED"},
		// A non-BTREE index type is still an index option.
		{"idx_hash", []indexColumn{{name: "a"}}, false, "HASH", "",
			"CREATE INDEX `idx_hash` ON `shop`.`orders` (`a`) USING HASH"},
	}
	for _, tc := range cases {
		table := "users"
		switch {
		case strings.Contains(tc.name, "multi"):
			table = "orders"
		case strings.Contains(tc.name, "ft_"), strings.Contains(tc.name, "sp_"):
			table = "docs"
		case tc.name == "idx_expr" || tc.name == "idx_desc" || tc.name == "idx_inv" ||
			tc.name == "idx_ign" || tc.name == "idx_hash":
			table = "orders"
		}
		got := indexDDL("shop", table, tc.name, tc.uniq, tc.typ, tc.visibility, tc.cols)
		if got != tc.want {
			t.Errorf("indexDDL(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestIndexVisibility pins the mapping of the server's visibility column to
// the keyword the statement needs.
func TestIndexVisibility(t *testing.T) {
	cases := []struct {
		column string
		value  sql.NullString
		want   string
	}{
		{"IS_VISIBLE", sql.NullString{String: "NO", Valid: true}, "INVISIBLE"},
		{"IS_VISIBLE", sql.NullString{String: "YES", Valid: true}, ""},
		{"IGNORED", sql.NullString{String: "YES", Valid: true}, "IGNORED"},
		{"IGNORED", sql.NullString{String: "NO", Valid: true}, ""},
		{"", sql.NullString{String: "NO", Valid: true}, ""},
		{"IS_VISIBLE", sql.NullString{}, ""},
	}
	for _, tc := range cases {
		if got := indexVisibility(tc.column, tc.value); got != tc.want {
			t.Errorf("indexVisibility(%q, %+v) = %q, want %q", tc.column, tc.value, got, tc.want)
		}
	}
}

func TestExpressionPart(t *testing.T) {
	if got := expressionPart("(`a` + 1)"); got != "(`a` + 1)" {
		t.Errorf("a parenthesised expression must stay as it is, got %q", got)
	}
	if got := expressionPart("`a` + 1"); got != "(`a` + 1)" {
		t.Errorf("a bare expression must gain its parentheses, got %q", got)
	}
}

// A quoted identifier must escape its own quoting character.
func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent("we`ird"); got != "`we``ird`" {
		t.Fatalf("quoteIdent = %q", got)
	}
}

// ---- Object definitions ----

// TestStripEnvironmentClauses pins the shape of the DDL the driver hands out.
// MySQL 8 prints the environment of a stored object in front of its name —
// the account that owns it, the algorithm of a view, its security context —
// and a statement that carries an account of another server does not run
// there. The clauses are cut from the prefix; the body of the object stays
// untouched.
func TestStripEnvironmentClauses(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "view",
			in:   "CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY DEFINER VIEW `v` AS select 1",
			want: "CREATE VIEW `v` AS select 1",
		},
		{
			name: "function",
			in:   "CREATE DEFINER=`root`@`%` FUNCTION `f`(x int) RETURNS int\nRETURN (x + 1)",
			want: "CREATE FUNCTION `f`(x int) RETURNS int\nRETURN (x + 1)",
		},
		{
			name: "procedure",
			in:   "CREATE DEFINER=`root`@`localhost` PROCEDURE `p`()\nSELECT 42",
			want: "CREATE PROCEDURE `p`()\nSELECT 42",
		},
		{
			name: "trigger",
			in:   "CREATE DEFINER=`root`@`%` TRIGGER `t` BEFORE INSERT ON `x` FOR EACH ROW SET @a = 1",
			want: "CREATE TRIGGER `t` BEFORE INSERT ON `x` FOR EACH ROW SET @a = 1",
		},
		{
			// MariaDB writes the account without backticks.
			name: "definer without quotes",
			in:   "CREATE DEFINER=root@localhost PROCEDURE `p`() SELECT 42",
			want: "CREATE PROCEDURE `p`() SELECT 42",
		},
		{
			// A table carries no such clause; it must stay as it is.
			name: "table",
			in:   "CREATE TABLE `t` (\n  `id` int NOT NULL\n) ENGINE=InnoDB",
			want: "CREATE TABLE `t` (\n  `id` int NOT NULL\n) ENGINE=InnoDB",
		},
		{
			// SQL SECURITY INVOKER names no account and is not the default:
			// dropping it would turn an invoker object into a definer one.
			name: "invoker keeps its security context",
			in:   "CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY INVOKER VIEW `v` AS select 1",
			want: "CREATE SQL SECURITY INVOKER VIEW `v` AS select 1",
		},
		{
			name: "no create statement",
			in:   "SELECT 1",
			want: "SELECT 1",
		},
		{
			name: "create without a clause",
			in:   "CREATE OR REPLACE VIEW `v` AS select 1",
			want: "CREATE OR REPLACE VIEW `v` AS select 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripEnvironmentClauses(tc.in); got != tc.want {
				t.Fatalf("stripEnvironmentClauses(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripEnvironmentClausesKeepsTheBody pins that only the prefix is
// touched: the body of a view may hold spaces and quotes that must survive.
func TestStripEnvironmentClausesKeepsTheBody(t *testing.T) {
	in := "CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY DEFINER VIEW `v` AS select 'a  b' AS `c`"
	want := "CREATE VIEW `v` AS select 'a  b' AS `c`"
	if got := stripEnvironmentClauses(in); got != want {
		t.Fatalf("stripEnvironmentClauses(%q) = %q, want %q", in, got, want)
	}
}

// TestFindCreateColumn pins the column layout of every SHOW CREATE … flavour.
// The regression this guards: the DDL used to be taken from the second column
// unconditionally, so functions, procedures and triggers showed their
// sql_mode string instead of their CREATE statement — and an empty sql_mode
// was even reported as "no CREATE statement".
func TestFindCreateColumn(t *testing.T) {
	cases := []struct {
		name   string
		cols   []string
		vals   []string
		want   int
		wantOK bool
	}{
		{
			name:   "table",
			cols:   []string{"Table", "Create Table"},
			vals:   []string{"users", "CREATE TABLE `users` (`id` int)"},
			want:   1,
			wantOK: true,
		},
		{
			name:   "view",
			cols:   []string{"View", "Create View"},
			vals:   []string{"active", "CREATE VIEW `active` AS SELECT 1"},
			want:   1,
			wantOK: true,
		},
		{
			name: "function with a sql_mode in the second column",
			cols: []string{"Function", "sql_mode", "Create Function",
				"character_set_client", "collation_connection", "Database Collation"},
			vals: []string{"f", "ONLY_FULL_GROUP_BY",
				"CREATE DEFINER=`ada`@`%` FUNCTION `f`(x int) RETURNS int RETURN (x + 1)",
				"utf8mb4", "utf8mb4_0900_ai_ci", "utf8mb4_0900_ai_ci"},
			want:   2,
			wantOK: true,
		},
		{
			name:   "function with an empty sql_mode",
			cols:   []string{"Function", "sql_mode", "Create Function"},
			vals:   []string{"f", "", "CREATE DEFINER=`ada`@`%` FUNCTION `f`() RETURNS int RETURN 1"},
			want:   2,
			wantOK: true,
		},
		{
			name: "procedure",
			cols: []string{"Procedure", "sql_mode", "Create Procedure",
				"character_set_client", "collation_connection", "Database Collation"},
			vals: []string{"p", "", "CREATE DEFINER=`ada`@`%` PROCEDURE `p`() SELECT 42",
				"utf8mb4", "utf8mb4_general_ci", "utf8mb4_general_ci"},
			want:   2,
			wantOK: true,
		},
		{
			name: "trigger on MySQL",
			cols: []string{"Trigger", "sql_mode", "SQL Original Statement",
				"character_set_client", "collation_connection", "Database Collation"},
			vals: []string{"trg", "", "CREATE TRIGGER `trg` BEFORE INSERT ON `t` FOR EACH ROW SET @a = 1",
				"utf8mb4", "utf8mb4_general_ci", "utf8mb4_general_ci"},
			want:   2,
			wantOK: true,
		},
		{
			// MariaDB prints the statement as written *and* a normalized
			// CREATE TRIGGER; the normalized one is the better definition.
			name: "trigger on MariaDB prefers Create Trigger",
			cols: []string{"Trigger", "sql_mode", "SQL Original Statement", "Create Trigger",
				"character_set_client", "collation_connection", "Database Collation"},
			vals: []string{"trg", "", "set @a = 1",
				"CREATE DEFINER=`root`@`%` TRIGGER `trg` BEFORE INSERT ON `t` FOR EACH ROW SET @a = 1",
				"utf8mb4", "utf8mb4_general_ci", "utf8mb4_general_ci"},
			want:   3,
			wantOK: true,
		},
		{
			name:   "unknown column names fall back to the CREATE-looking value",
			cols:   []string{"col_a", "col_b"},
			vals:   []string{"trg", "CREATE TRIGGER `trg` BEFORE INSERT ON `t` FOR EACH ROW SET @a = 1"},
			want:   1,
			wantOK: true,
		},
		{
			name:   "no CREATE statement at all",
			cols:   []string{"Function", "sql_mode"},
			vals:   []string{"f", ""},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vals := make([]sql.NullString, len(tc.vals))
			for i, v := range tc.vals {
				vals[i] = sql.NullString{String: v, Valid: v != ""}
			}
			got, ok := findCreateColumn(tc.cols, vals)
			if ok != tc.wantOK {
				t.Fatalf("findCreateColumn ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("findCreateColumn = %d (%q), want %d (%q)",
					got, tc.cols[got], tc.want, tc.cols[tc.want])
			}
		})
	}
}

// TestIsMissingObject pins how SHOW CREATE reports a missing object: as a
// server error (1146 for tables and views, 1305 for routines and triggers),
// never as sql.ErrNoRows — the branch the "not found" mapping used to wait
// for, which let the raw driver error reach the user.
func TestIsMissingObject(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"table does not exist", &gomysql.MySQLError{Number: 1146, Message: "Table 'shop.missing' doesn't exist"}, true},
		{"unknown table", &gomysql.MySQLError{Number: 1051, Message: "Unknown table 'shop.missing'"}, true},
		{"routine does not exist", &gomysql.MySQLError{Number: 1305, Message: "FUNCTION shop.f does not exist"}, true},
		{"wrapped server error", fmt.Errorf("mysql: query: %w", &gomysql.MySQLError{Number: 1146}), true},
		{"access denied", &gomysql.MySQLError{Number: 1045, Message: "Access denied"}, false},
		{"syntax error", &gomysql.MySQLError{Number: 1064, Message: "You have an error"}, false},
		{"no rows", sql.ErrNoRows, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingObject(tc.err); got != tc.want {
				t.Fatalf("isMissingObject(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---- Registration ----

func TestFactoriesAreRegistered(t *testing.T) {
	for _, name := range []string{mysqlName, mariadbName} {
		if !driver.Implemented(name) {
			t.Errorf("the %s driver must replace the placeholder factory", name)
		}
	}
}

// TestIntegrationCatalogAndQuery runs against a real server when a DSN is
// provided. It is skipped in the normal test suite because MySQL is not a
// dependency of the project. The DSN must name a database (…/dbname); the
// created objects are dropped again.
func TestIntegrationCatalogAndQuery(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TDB_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("set TDB_MYSQL_DSN to run the MySQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := (factory{name: mysqlName}).Open(ctx, driver.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	dbName := c.Name()
	if dbName == mysqlName {
		t.Fatal("the DSN must name a database: …/dbname")
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	table := "tdb_it_" + suffix
	viewName := table + "_v"
	funcName := table + "_fn"
	procName := table + "_proc"
	triggerName := table + "_trg"
	indexName := table + "_idx"
	if _, err := c.Query(ctx, "CREATE TABLE "+table+
		" (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50) NOT NULL COMMENT 'a name',"+
		" note VARCHAR(50), bin VARBINARY(16), price DECIMAL(8,2) DEFAULT 1.5)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	// One cleanup closure keeps the dependency order explicit: the view,
	// trigger and index all depend on the table.
	defer func() {
		ctx := context.Background()
		c.Query(ctx, "DROP TABLE IF EXISTS "+table)
		c.Query(ctx, "DROP VIEW IF EXISTS "+viewName)
		c.Query(ctx, "DROP FUNCTION IF EXISTS "+funcName)
		c.Query(ctx, "DROP PROCEDURE IF EXISTS "+procName)
	}()
	resourceSQL := []string{
		"CREATE INDEX " + indexName + " ON " + table + " (name)",
		"CREATE VIEW " + viewName + " AS SELECT id, name FROM " + table,
		"CREATE FUNCTION " + funcName + "(x INT) RETURNS INT RETURN x + 1",
		"CREATE PROCEDURE " + procName + "() SELECT 42",
		// The trigger fires on every insert but only rewrites the row it
		// matches: the rows it does not match must keep their own value, so
		// the test below really shows that the trigger did not touch them.
		"CREATE TRIGGER " + triggerName + " BEFORE INSERT ON " + table +
			" FOR EACH ROW SET NEW.note = IF(NEW.name = 'ada', 'from trigger', NEW.note)",
	}
	for _, stmt := range resourceSQL {
		if _, err := c.Query(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if _, err := c.Query(ctx, "INSERT INTO "+table+" (name, note, bin) VALUES ('ada', NULL, X'DEAD')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	ins, err := c.Query(ctx, "INSERT INTO "+table+" (name) VALUES ('bob')")
	if err != nil {
		t.Fatalf("INSERT with affected count: %v", err)
	}
	if ins.Affected != 1 {
		t.Fatalf("INSERT affected = %d, want 1", ins.Affected)
	}
	if _, err := c.Query(ctx, "INSERT INTO "+table+" (name, note) VALUES ('cy', 'kept')"); err != nil {
		t.Fatalf("INSERT with a note: %v", err)
	}
	res, err := c.Query(ctx, "SELECT id, name, note, bin, price FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(res.Columns) != 5 || len(res.Rows) != 3 {
		t.Fatalf("result = %d columns, %d rows", len(res.Columns), len(res.Rows))
	}
	// The trigger filled note on the row it matches; the rows it does not
	// match keep their own value — NULL stays NULL.
	if got := res.Rows[0][2]; got == nil || *got != "from trigger" {
		t.Fatalf("note = %v, want the trigger's value", got)
	}
	if res.Rows[1][2] != nil {
		t.Fatalf("NULL must stay NULL, got %q", *res.Rows[1][2])
	}
	if got := res.Rows[2][2]; got == nil || *got != "kept" {
		t.Fatalf("note = %v, want the value the row was inserted with", got)
	}
	if got := res.Rows[0][3]; got == nil || *got != "0xdead" {
		t.Fatalf("bin = %v, want hex 0xdead", got)
	}
	if got := res.Rows[0][4]; got == nil || *got != "1.50" {
		t.Fatalf("price = %v, want 1.50", got)
	}

	// A running statement must be cancellable through the context.
	slowCtx, slowCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	if _, err := c.Query(slowCtx, "SELECT SLEEP(5)"); err == nil {
		t.Fatal("a cancelled statement must fail, not succeed")
	}
	slowCancel()

	cat, err := c.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	schema := cat.FindSchema(dbName)
	if schema == nil {
		t.Fatalf("the catalog has no schema %q: %+v", dbName, cat.Schemas)
	}
	tbl := cat.FindTable(dbName + "." + table)
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
	if name := gotCols["name"]; name == nil || name.Nullable || name.Comment != "a name" {
		t.Fatalf("name column = %+v, want NOT NULL with its comment", name)
	}
	if note := gotCols["note"]; note == nil || !note.Nullable {
		t.Fatalf("note column = %+v, want nullable", note)
	}
	if price := gotCols["price"]; price == nil || price.Default == "" {
		t.Fatalf("price column = %+v, want its default", price)
	}

	if !cat.Capabilities.Supports(catalog.KindTable) ||
		!cat.Capabilities.Supports(catalog.KindView) ||
		!cat.Capabilities.Supports(catalog.KindFunction) ||
		!cat.Capabilities.Supports(catalog.KindProcedure) ||
		!cat.Capabilities.Supports(catalog.KindTrigger) ||
		!cat.Capabilities.Supports(catalog.KindIndex) {
		t.Fatalf("capabilities = %v, want every MySQL resource kind", cat.Capabilities)
	}
	// The driver serves MySQL and MariaDB: MariaDB has sequences, so the
	// capability is advertised even against a MySQL server (the group stays
	// empty and the object browser hides it). Neither server has materialized
	// views, so that capability stays out.
	if cat.Capabilities.Supports(catalog.KindMaterializedView) {
		t.Fatalf("capabilities = %v, the MySQL driver has no materialized views", cat.Capabilities)
	}
	if !cat.Capabilities.Supports(catalog.KindSequence) {
		t.Fatalf("capabilities = %v, MariaDB sequences need the sequence capability", cat.Capabilities)
	}
	if n := schema.Count(catalog.KindSequence); n != 0 {
		t.Fatalf("a MySQL server must list no sequences, got %d", n)
	}
	if v := cat.FindTable(dbName + "." + viewName); v == nil || v.Type != catalog.TypeView {
		t.Fatalf("view = %+v, want a view in the catalog", v)
	}
	resources := make(map[string]*catalog.Resource)
	for _, r := range schema.Resources {
		resources[r.Name] = r
	}
	for name, want := range map[string]catalog.ResourceKind{
		funcName:    catalog.KindFunction,
		procName:    catalog.KindProcedure,
		triggerName: catalog.KindTrigger,
		indexName:   catalog.KindIndex,
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
		if !strings.Contains(trg.Detail, "BEFORE") || !strings.Contains(trg.Detail, "INSERT") {
			t.Errorf("trigger detail = %q, want BEFORE INSERT", trg.Detail)
		}
	}
	if idx := resources[indexName]; idx != nil {
		if !strings.Contains(idx.Detail, "(name)") {
			t.Errorf("index detail = %q, want the indexed column", idx.Detail)
		}
	}

	// The DefinitionProvider must reproduce the DDL of every object kind.
	prov, ok := c.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the mysql connection must implement driver.DefinitionProvider")
	}
	for _, tc := range []struct {
		ref  driver.DefinitionRef
		want []string
		// notWant names the clauses of the environment the object was
		// created in: they belong to the server, not to the object, and a
		// statement that carries them does not run somewhere else.
		notWant []string
	}{
		{
			ref: driver.DefinitionRef{Kind: catalog.KindTable, Schema: dbName, Name: table},
			want: []string{
				"CREATE TABLE `" + table + "`",
				"`id` int NOT NULL AUTO_INCREMENT",
				"`name` varchar(50) NOT NULL COMMENT 'a name'",
				"`note` varchar(50) DEFAULT NULL",
				"`price` decimal(8,2) DEFAULT '1.50'",
				"PRIMARY KEY (`id`)",
			},
			notWant: []string{"DEFINER="},
		},
		{
			ref: driver.DefinitionRef{Kind: catalog.KindView, Schema: dbName, Name: viewName},
			want: []string{
				"CREATE VIEW `" + viewName + "` AS select",
				"`id` AS `id`",
				"`name` AS `name`",
				"from `" + table + "`",
			},
			notWant: []string{"DEFINER=", "ALGORITHM=", "SQL SECURITY"},
		},
		{
			ref: driver.DefinitionRef{Kind: catalog.KindFunction, Schema: dbName, Name: funcName},
			want: []string{
				"CREATE FUNCTION `" + funcName + "`(",
				"RETURNS int",
				"RETURN x + 1",
			},
			notWant: []string{"DEFINER="},
		},
		{
			ref: driver.DefinitionRef{Kind: catalog.KindProcedure, Schema: dbName, Name: procName},
			want: []string{
				"CREATE PROCEDURE `" + procName + "`()",
				"SELECT 42",
			},
			notWant: []string{"DEFINER="},
		},
		{
			ref: driver.DefinitionRef{Kind: catalog.KindTrigger, Schema: dbName, Name: triggerName, Owner: table},
			want: []string{
				"CREATE TRIGGER `" + triggerName + "` BEFORE INSERT ON `" + table + "`",
				"FOR EACH ROW",
				"IF(NEW.name = 'ada'",
			},
			notWant: []string{"DEFINER="},
		},
		{
			ref: driver.DefinitionRef{Kind: catalog.KindIndex, Schema: dbName, Name: indexName, Owner: table},
			want: []string{
				"CREATE INDEX `" + indexName + "` ON `" + dbName + "`.`" + table + "` (`name`)",
			},
			notWant: []string{"DEFINER="},
		},
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
		for _, unwanted := range tc.notWant {
			if strings.Contains(ddl, unwanted) {
				t.Errorf("Definition(%s %s.%s) = %q, want it not to contain %q",
					tc.ref.Kind, tc.ref.Schema, tc.ref.Name, ddl, unwanted)
			}
		}
	}
	// A missing object arrives as server error 1146 (not sql.ErrNoRows) and
	// must be mapped to the uniform "not found" message of the other drivers.
	_, err = prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: dbName, Name: table + "_missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing object must report not found, got %v", err)
	}
	_, err = prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindSequence, Schema: dbName, Name: "any"})
	if err == nil {
		t.Error("MySQL has no sequences; asking for one must fail")
	}
}
