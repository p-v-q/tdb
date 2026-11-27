package sql

import "testing"

// riskKeywords is a realistic dialect keyword set for the risk classifier;
// with a nil lookup every word degrades to an identifier and the verdicts
// must not change, which TestRiskWithoutKeywordLookup pins.
var riskKeywords = NewKeywords(
	"SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE", "SET",
	"DELETE", "MERGE", "USING", "CREATE", "ALTER", "DROP", "TRUNCATE",
	"RENAME", "TABLE", "INDEX", "VIEW", "WITH", "RECURSIVE", "UNION", "EXPLAIN",
	"ANALYZE", "VERBOSE", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT",
	"RELEASE", "START", "TRANSACTION", "END", "SHOW", "DESCRIBE", "DESC",
	"GRANT", "REVOKE", "VACUUM", "CALL", "REPLACE", "EXISTS", "INTO",
)

func TestRiskClassification(t *testing.T) {
	cases := []struct {
		stmt string
		want RiskLevel
	}{
		// Read-only statements.
		{"SELECT * FROM users", RiskReadOnly},
		{"select 1", RiskReadOnly},
		{"  SELECT 'WHERE' AS s", RiskReadOnly},
		{"-- a leading comment\nSELECT 1", RiskReadOnly},
		{"SHOW TABLES", RiskReadOnly},
		{"DESCRIBE users", RiskReadOnly},
		{"EXPLAIN SELECT * FROM users", RiskReadOnly},
		{"EXPLAIN QUERY PLAN SELECT 1", RiskReadOnly},
		{"VALUES (1), (2)", RiskReadOnly},
		{"WITH x AS (SELECT 1) SELECT * FROM x", RiskReadOnly},
		{"WITH RECURSIVE n(i) AS (SELECT 1) SELECT i FROM n", RiskReadOnly},
		{"BEGIN", RiskReadOnly},
		{"START TRANSACTION", RiskReadOnly},
		{"COMMIT", RiskReadOnly},
		{"ROLLBACK", RiskReadOnly},
		{"SAVEPOINT sp1", RiskReadOnly},
		{"", RiskReadOnly},
		{"-- just a comment", RiskReadOnly},

		// Scoped writes run without a prompt.
		{"INSERT INTO users VALUES (1, 'a@b.c', 'A', 1)", RiskWrite},
		{"insert into t (a) select a from b", RiskWrite},
		{"REPLACE INTO t VALUES (1)", RiskWrite},
		{"UPDATE users SET age = 1 WHERE id = 2", RiskWrite},
		{"update users set age = 1\nwhere id = 2", RiskWrite},
		{"DELETE FROM orders WHERE id = 1", RiskWrite},
		{"DELETE FROM orders WHERE note LIKE '%where%'", RiskWrite},
		{"DELETE t1 FROM t1 JOIN t2 ON t1.id = t2.id WHERE t1.x = 1", RiskWrite},
		{"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET x = 1", RiskWrite},
		{"CALL refresh_stats()", RiskWrite},
		{"VACUUM", RiskWrite},
		{"GRANT SELECT ON users TO alice", RiskWrite},

		// Data-modifying CTEs: the main verb decides.
		{"WITH x AS (SELECT id FROM t) DELETE FROM t WHERE id IN (SELECT id FROM x)", RiskWrite},
		{"WITH x AS (SELECT 1 FROM t) UPDATE t SET a = 1", RiskDangerousWrite},

		// DDL asks for confirmation.
		{"CREATE TABLE t (id INT)", RiskDDL},
		{"create table if not exists t (id int)", RiskDDL},
		{"ALTER TABLE t ADD COLUMN c INT", RiskDDL},
		{"CREATE INDEX ix ON t (a)", RiskDDL},
		{"RENAME TABLE a TO b", RiskDDL},

		// Destructive statements ask, loudly.
		{"DROP TABLE users", RiskDangerousWrite},
		{"DROP TABLE IF EXISTS users", RiskDangerousWrite},
		{"drop view v", RiskDangerousWrite},
		{"TRUNCATE TABLE users", RiskDangerousWrite},
		{"DELETE FROM orders", RiskDangerousWrite},
		{"UPDATE users SET active = 0", RiskDangerousWrite},
		{"update users\nset active = 0", RiskDangerousWrite},

		// WHERE inside a string or a subquery is not a WHERE clause.
		{"UPDATE t SET note = 'where'", RiskDangerousWrite},
		{"DELETE FROM t WHERE note = 'no where clause here'", RiskWrite},
		{"UPDATE t SET x = (SELECT max(y) FROM z WHERE a = b)", RiskDangerousWrite},
		{"DELETE FROM t WHERE id IN (SELECT id FROM u WHERE u.x = 1)", RiskWrite},

		// EXPLAIN ANALYZE runs what it explains.
		{"EXPLAIN ANALYZE DELETE FROM users", RiskDangerousWrite},
		{"EXPLAIN (ANALYZE, VERBOSE) UPDATE t SET a = 1", RiskDangerousWrite},
		{"EXPLAIN ANALYZE SELECT 1", RiskReadOnly},
		{"EXPLAIN VERBOSE DELETE FROM users", RiskReadOnly},

		// EXPLAIN of a destructive statement is still just a plan.
		{"EXPLAIN DROP TABLE users", RiskReadOnly},
	}
	for _, c := range cases {
		if got := Risk(c.stmt, riskKeywords); got != c.want {
			t.Errorf("Risk(%q) = %v, want %v", c.stmt, got, c.want)
		}
	}
}

func TestRiskWithoutKeywordLookup(t *testing.T) {
	// With a nil lookup no word is a keyword, yet the verdicts must hold:
	// the classifier works on the words themselves, not on keyword kinds.
	cases := map[string]RiskLevel{
		"SELECT * FROM users":      RiskReadOnly,
		"UPDATE users SET a = 1":   RiskDangerousWrite,
		"DELETE FROM orders":       RiskDangerousWrite,
		"DROP TABLE users":         RiskDangerousWrite,
		"INSERT INTO t VALUES (1)": RiskWrite,
	}
	for stmt, want := range cases {
		if got := Risk(stmt, nil); got != want {
			t.Errorf("Risk(%q, nil) = %v, want %v", stmt, got, want)
		}
	}
}

func TestRiskPerStatementOfScript(t *testing.T) {
	script := "SELECT 1;\nDELETE FROM users;\nCREATE TABLE t (x INT);\nUPDATE t SET x = 1 WHERE x = 2"
	want := []RiskLevel{RiskReadOnly, RiskDangerousWrite, RiskDDL, RiskWrite}
	stmts := Statements(script, riskKeywords)
	if len(stmts) != len(want) {
		t.Fatalf("statements = %d, want %d", len(stmts), len(want))
	}
	for i, stmt := range stmts {
		if got := Risk(stmt.Text, riskKeywords); got != want[i] {
			t.Errorf("statement %d (%q) = %v, want %v", i, stmt.Text, got, want[i])
		}
	}
	if got := MaxRisk([]RiskLevel{
		Risk(stmts[0].Text, riskKeywords),
		Risk(stmts[1].Text, riskKeywords),
		Risk(stmts[2].Text, riskKeywords),
		Risk(stmts[3].Text, riskKeywords),
	}); got != RiskDangerousWrite {
		t.Fatalf("MaxRisk of the script = %v, want %v", got, RiskDangerousWrite)
	}
}

// TestRiskMySQLExecutableComments pins that MySQL's /*! … */ and MariaDB's
// /*M! … */ are treated as code: the server splices their body into the
// statement, so a DELETE hiding in one really runs and read-only mode must
// block it. Before the fix such statements carried no significant token at
// all besides the read-only head and were classified RiskReadOnly.
func TestRiskMySQLExecutableComments(t *testing.T) {
	cases := []struct {
		stmt string
		want RiskLevel
	}{
		// The spliced body runs even though the visible main verb reads.
		// The body depends on the server version, so any statement
		// carrying an executable comment fails closed as a plain write.
		{"WITH x AS (SELECT 1) /*!50000 DELETE FROM users */", RiskWrite},
		{"/*!50000 DELETE FROM users */", RiskWrite},
		{"/*M! TRUNCATE TABLE users */", RiskWrite},
		{"/*! SELECT 1 */", RiskWrite}, // fail closed even when the body looks harmless

		// Ordinary comments stay commentary: keywords inside them never
		// turn a read into a write.
		{"SELECT 1 -- DELETE FROM users", RiskReadOnly},
		{"SELECT 1 /* DELETE FROM users */", RiskReadOnly},
		{"/* plain comment */ SELECT 1", RiskReadOnly},
		{"WITH x AS (SELECT 1) /* DELETE FROM users */", RiskReadOnly},
		{"SELECT /*+ MAX_EXECUTION_TIME(1000) */ 1", RiskReadOnly}, // optimizer hint, not executable
	}
	for _, c := range cases {
		if got := Risk(c.stmt, riskKeywords); got != c.want {
			t.Errorf("Risk(%q) = %v, want %v", c.stmt, got, c.want)
		}
		if c.want == RiskReadOnly && Risk(c.stmt, nil) != RiskReadOnly {
			t.Errorf("Risk(%q, nil) changed the verdict", c.stmt)
		}
	}
}

// TestRiskCompoundBeginBlocks pins that a block-style BEGIN (MariaDB's
// BEGIN NOT ATOMIC, SQLite's bare BEGIN … END) is classified by what its body
// does, sharing split.go's blockBegin so the two can never disagree. Before
// the fix every BEGIN was waved through as transaction control and
// `BEGIN NOT ATOMIC DELETE …; END` reached the database from read-only mode.
func TestRiskCompoundBeginBlocks(t *testing.T) {
	cases := []struct {
		name, stmt string
		want       RiskLevel
	}{
		// Bodies really execute: the most dangerous inner statement
		// decides the level of the compound statement.
		{"mariadb not atomic", "BEGIN NOT ATOMIC DELETE FROM users; END", RiskDangerousWrite},
		{"sqlite bare block", "BEGIN INSERT INTO t VALUES (1); END", RiskWrite},
		{"nested blocks", "BEGIN NOT ATOMIC BEGIN DELETE FROM users; END; END", RiskDangerousWrite},
		// An unterminated body (mid-edit) fails closed as a write.
		{"unterminated block", "BEGIN DELETE FROM users", RiskWrite},

		// Transaction control is unaffected by the block detection.
		{"bare begin", "BEGIN", RiskReadOnly},
		{"begin transaction", "BEGIN TRANSACTION", RiskReadOnly},
		{"begin immediate", "BEGIN IMMEDIATE", RiskReadOnly},
		{"begin isolation", "BEGIN ISOLATION LEVEL READ COMMITTED", RiskReadOnly},
		{"start transaction", "START TRANSACTION", RiskReadOnly},
		{"rollback", "ROLLBACK", RiskReadOnly},
	}
	for _, c := range cases {
		if got := Risk(c.stmt, riskKeywords); got != c.want {
			t.Errorf("%s: Risk(%q) = %v, want %v", c.name, c.stmt, got, c.want)
		}
	}

	// Splitter and classifier agree on the block: it is one statement
	// classified as the inner write, the trailing SELECT stays read-only.
	script := "BEGIN INSERT INTO t VALUES (1); END; SELECT 1"
	want := []RiskLevel{RiskWrite, RiskReadOnly}
	stmts := Statements(script, riskKeywords)
	if len(stmts) != len(want) {
		t.Fatalf("script split into %d statements %+v, want %d", len(stmts), stmts, len(want))
	}
	for i, stmt := range stmts {
		if got := Risk(stmt.Text, riskKeywords); got != want[i] {
			t.Errorf("script statement %d (%q) = %v, want %v", i, stmt.Text, got, want[i])
		}
	}
}

// TestRiskDataModifyingCTE pins that a data-modifying PostgreSQL CTE body
// counts as a write even when the main verb is a SELECT: the body really
// runs. Before the fix only the main verb was inspected and
// `WITH x AS (DELETE … RETURNING *) SELECT * FROM x` reached the database
// from read-only mode.
func TestRiskDataModifyingCTE(t *testing.T) {
	cases := []struct {
		stmt string
		want RiskLevel
	}{
		{"WITH x AS (DELETE FROM users WHERE id = 1 RETURNING *) SELECT * FROM x", RiskWrite},
		{"WITH x AS (DELETE FROM users RETURNING *) SELECT * FROM x", RiskDangerousWrite}, // no WHERE
		{"WITH x AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM x", RiskWrite},
		{"WITH x AS (UPDATE t SET a = 1 WHERE id = 2 RETURNING *) SELECT * FROM x", RiskWrite},
		// Control: a read-only body stays read-only.
		{"WITH x AS (SELECT 1) SELECT * FROM x", RiskReadOnly},
	}
	for _, c := range cases {
		if got := Risk(c.stmt, riskKeywords); got != c.want {
			t.Errorf("Risk(%q) = %v, want %v", c.stmt, got, c.want)
		}
	}
}

func TestRiskLevelProperties(t *testing.T) {
	if RiskReadOnly.Writes() || !RiskWrite.Writes() || !RiskDDL.Writes() {
		t.Fatal("everything above RiskReadOnly writes")
	}
	if RiskReadOnly.NeedsConfirmation() || RiskWrite.NeedsConfirmation() {
		t.Fatal("reads and scoped writes must not ask for confirmation")
	}
	if !RiskDDL.NeedsConfirmation() || !RiskDangerousWrite.NeedsConfirmation() {
		t.Fatal("DDL and destructive writes must ask for confirmation")
	}
	if got := MaxRisk(nil); got != RiskReadOnly {
		t.Fatalf("MaxRisk(nil) = %v, want %v", got, RiskReadOnly)
	}
	if got := MaxRisk([]RiskLevel{RiskWrite, RiskDDL, RiskReadOnly}); got != RiskDDL {
		t.Fatalf("MaxRisk = %v, want %v", got, RiskDDL)
	}
	for r := RiskReadOnly; r <= RiskDangerousWrite; r++ {
		if r.String() == "unknown" {
			t.Fatalf("level %d has no name", r)
		}
	}
}
