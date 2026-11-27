package sql

import (
	"strings"
	"testing"
)

// kw is a tiny keyword set: splitting must not depend on the dialect.
var kw = NewKeywords("SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES")

func TestStatementsSplitsOnSemicolons(t *testing.T) {
	got := Statements("SELECT 1; SELECT 2", kw)
	want := []string{"SELECT 1", "SELECT 2"}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %d statements", got, len(want))
	}
	for i := range want {
		if got[i].Text != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i].Text, want[i])
		}
	}
	// Offsets map back onto the script.
	if got[1].Start != 10 || got[1].End != 18 {
		t.Errorf("offsets = %d..%d, want 10..18", got[1].Start, got[1].End)
	}
}

func TestStatementsIgnoresSemicolonsInStringsAndComments(t *testing.T) {
	script := "SELECT 'a;b'; -- trailing; comment\nINSERT INTO t VALUES (1);"
	got := Statements(script, kw)
	if len(got) != 2 {
		t.Fatalf("got %d statements: %+v", len(got), got)
	}
	if got[0].Text != "SELECT 'a;b'" {
		t.Errorf("statement 0 = %q", got[0].Text)
	}
	// The comment is between the two statements and is not executed on its own.
	if got[1].Text != "INSERT INTO t VALUES (1)" {
		t.Errorf("statement 1 = %q", got[1].Text)
	}
}

func TestStatementsDropsEmptyPieces(t *testing.T) {
	if got := Statements(";;  ;\n-- only a comment\n;", kw); len(got) != 0 {
		t.Fatalf("got %+v, want no statements", got)
	}
	if got := Statements("", kw); len(got) != 0 {
		t.Fatalf("got %+v, want no statements", got)
	}
}

func TestStatementsKeepsTheLastOneWithoutSemicolon(t *testing.T) {
	got := Statements("SELECT 1;\nSELECT 2\n", kw)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[1].Text != "SELECT 2" {
		t.Fatalf("last statement = %q", got[1].Text)
	}
}

func TestStatementAt(t *testing.T) {
	script := "SELECT 1; SELECT 2; SELECT 3"
	cases := map[int]string{
		0:  "SELECT 1",
		7:  "SELECT 1", // the last character of the first statement
		11: "SELECT 2", // inside the second
		27: "SELECT 3", // the end of the last one
	}
	for offset, want := range cases {
		got, ok := StatementAt(script, offset, kw)
		if !ok {
			t.Fatalf("offset %d: no statement found", offset)
		}
		if got.Text != want {
			t.Errorf("offset %d = %q, want %q", offset, got.Text, want)
		}
	}
	// A caret between two statements (here: in the separating gap) belongs to
	// no statement at all.
	if _, ok := StatementAt(script, 9, kw); ok {
		t.Error("offset 9 sits between two statements and must not match one")
	}
}

func TestStatementsWithoutKeywords(t *testing.T) {
	// Splitting works even when nothing is recognized as a keyword.
	got := Statements("pragma table_info(users); select 1", nil)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
}

// ---- compound statements ----

func TestStatementsKeepsSQLiteTriggerBody(t *testing.T) {
	script := `CREATE TRIGGER users_audit AFTER INSERT ON users
BEGIN
  INSERT INTO audit_log(msg) VALUES (new.email);
  UPDATE stats SET n = n + 1;
END;
SELECT 1;`
	got := Statements(script, kw)
	if len(got) != 2 {
		t.Fatalf("got %d statements: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Text, "CREATE TRIGGER") || !strings.HasSuffix(got[0].Text, "END") {
		t.Errorf("statement 0 = %q", got[0].Text)
	}
	// The inner semicolons are part of the trigger, not split points.
	if n := strings.Count(got[0].Text, ";"); n != 2 {
		t.Errorf("statement 0 has %d semicolons, want 2", n)
	}
	if got[1].Text != "SELECT 1" {
		t.Errorf("statement 1 = %q", got[1].Text)
	}
	// Offsets still map back onto the script.
	if got[1].Start != strings.Index(script, "SELECT 1") {
		t.Errorf("statement 1 starts at %d", got[1].Start)
	}
}

func TestStatementsKeepsDollarQuotedBodies(t *testing.T) {
	cases := []struct {
		name, script string
		want         int
	}{
		{
			name: "function",
			script: `CREATE FUNCTION bump() RETURNS int AS $$
BEGIN
  RETURN 1; RETURN 2;
END;
$$ LANGUAGE plpgsql;
SELECT 1;`,
			want: 2,
		},
		{
			name:   "do block",
			script: "DO $$\nBEGIN\n  RAISE NOTICE 'a;b';\nEND;\n$$;\nSELECT 2;",
			want:   2,
		},
		{
			name:   "tagged body",
			script: "CREATE PROCEDURE p() AS $tag$ SELECT ';'; $tag$; SELECT 3;",
			want:   2,
		},
	}
	for _, tc := range cases {
		got := Statements(tc.script, kw)
		if len(got) != tc.want {
			t.Errorf("%s: got %d statements: %+v, want %d", tc.name, len(got), got, tc.want)
		}
	}
}

func TestStatementsTransactionBeginStillSplits(t *testing.T) {
	for _, script := range []string{
		"BEGIN; SELECT 1; COMMIT;",
		"BEGIN TRANSACTION; SELECT 1; COMMIT;",
		"begin immediate;\nselect 1;\ncommit;",
	} {
		got := Statements(script, kw)
		if len(got) != 3 {
			t.Errorf("%q: got %+v, want 3 statements", script, got)
		}
	}
}

func TestStatementsBlockNesting(t *testing.T) {
	cases := []struct {
		name, script string
		want         int
	}{
		{
			name: "nested begin end",
			script: `CREATE PROCEDURE p() BEGIN
  INSERT INTO a VALUES (1);
  BEGIN
    INSERT INTO b VALUES (2);
  END;
  UPDATE c SET x = 1;
END;`,
			want: 1,
		},
		{
			name: "case end inside body",
			script: `CREATE TRIGGER t AFTER INSERT ON u BEGIN
  INSERT INTO a VALUES (CASE WHEN new.x THEN 1 ELSE 2 END);
  INSERT INTO b VALUES (3);
END;`,
			want: 1,
		},
		{
			name: "mysql end variants",
			script: `CREATE PROCEDURE p() BEGIN
  IF x THEN
    INSERT INTO a VALUES (1);
  END IF;
  WHILE x DO
    INSERT INTO b VALUES (IF(x, 1, 2));
  END WHILE;
END;`,
			want: 1,
		},
		{
			name:   "balanced case outside a block",
			script: "SELECT CASE WHEN a THEN 1 END; SELECT 2",
			want:   2,
		},
		{
			name:   "bare begin end block",
			script: "BEGIN; INSERT INTO t VALUES (1); COMMIT; BEGIN INSERT INTO t VALUES (2); END; SELECT 3",
			want:   5,
		},
	}
	for _, tc := range cases {
		got := Statements(tc.script, kw)
		if len(got) != tc.want {
			t.Errorf("%s: got %d statements: %+v, want %d", tc.name, len(got), got, tc.want)
		}
	}
}

func TestStatementsSemicolonsInQuotedRegions(t *testing.T) {
	script := `SELECT "a;b"; SELECT ` + "`c;d`" + `; SELECT [e;f]; SELECT 'g;h'; -- ;`
	got := Statements(script, kw)
	if len(got) != 4 {
		t.Fatalf("got %+v, want 4 statements", got)
	}
}

// TestStatementsMysqlEndCase is the MEDIUM splitter regression: a MySQL
// compound CASE statement ends with `END CASE`, which used to decrement the
// depth for END and then increment it again for the CASE, so the enclosing
// BEGIN … END never closed and the rest of the script merged into one
// statement.
func TestStatementsMysqlEndCase(t *testing.T) {
	cases := []struct {
		name, script string
		want         int
		wantFirst    string
		wantLast     string
		wantSemis    int
	}{
		{
			name: "simple case statement inside a procedure",
			script: "CREATE PROCEDURE p() BEGIN\n" +
				"  CASE x\n    WHEN 1 THEN SELECT 1;\n  END CASE;\n  SELECT 2;\nEND;\n" +
				"SELECT 3;",
			want:      2,
			wantFirst: "CREATE PROCEDURE p() BEGIN",
			wantLast:  "SELECT 3",
			wantSemis: 3,
		},
		{
			name: "searched case statement",
			script: "CREATE PROCEDURE p() BEGIN\n" +
				"  CASE\n    WHEN x > 0 THEN SELECT 1;\n    ELSE SELECT 2;\n  END CASE;\nEND;\n" +
				"SELECT 3",
			want:      2,
			wantFirst: "CREATE PROCEDURE p()",
			wantLast:  "SELECT 3",
			wantSemis: 3,
		},
		{
			name:      "end case directly at top level",
			script:    "SELECT CASE x WHEN 1 THEN 1 END CASE; SELECT 2;",
			want:      2,
			wantFirst: "SELECT CASE x WHEN 1 THEN 1 END CASE",
			wantLast:  "SELECT 2",
			wantSemis: 0,
		},
	}
	for _, tc := range cases {
		got := Statements(tc.script, kw)
		if len(got) != tc.want {
			t.Errorf("%s: got %d statements %+v, want %d", tc.name, len(got), got, tc.want)
			continue
		}
		if got[0].Text != tc.wantFirst && !strings.HasPrefix(got[0].Text, tc.wantFirst) {
			t.Errorf("%s: first statement = %q, want it to start with %q", tc.name, got[0].Text, tc.wantFirst)
		}
		if got[len(got)-1].Text != tc.wantLast {
			t.Errorf("%s: last statement = %q, want %q", tc.name, got[len(got)-1].Text, tc.wantLast)
		}
		// The body's inner semicolons stay inside the statement; only the
		// statement after the closing END is split off.
		if n := strings.Count(got[0].Text, ";"); n != tc.wantSemis {
			t.Errorf("%s: first statement has %d semicolons, want %d", tc.name, n, tc.wantSemis)
		}
	}
}

// TestStatementsMysqlEndCaseKeepsBeginEndProtected checks that making
// `END CASE` balance did not weaken plain BEGIN … END and `END IF` protection:
// the following statement must still stay outside the body.
func TestStatementsMysqlEndCaseKeepsBeginEndProtected(t *testing.T) {
	scripts := []struct {
		name, script string
	}{
		{
			name:   "plain begin end",
			script: "CREATE PROCEDURE p() BEGIN INSERT INTO t VALUES (1); INSERT INTO t VALUES (2); END;\nSELECT 1;",
		},
		{
			name:   "end if still neutral",
			script: "CREATE PROCEDURE p() BEGIN IF x THEN INSERT INTO t VALUES (1); END IF; END;\nSELECT 1;",
		},
		{
			name: "case statement then case expression",
			script: "CREATE PROCEDURE p() BEGIN CASE WHEN x THEN SELECT 1; END CASE;\n" +
				"  SELECT CASE WHEN y THEN 2 ELSE 3 END;\nEND;\nSELECT 4;",
		},
	}
	for _, tc := range scripts {
		got := Statements(tc.script, kw)
		if len(got) != 2 {
			t.Errorf("%s: got %d statements %+v, want 2", tc.name, len(got), got)
			continue
		}
		if got[1].Text != "SELECT 1" && got[1].Text != "SELECT 4" {
			t.Errorf("%s: trailing statement = %q", tc.name, got[1].Text)
		}
	}
}

// TestStatementsMysqlBackslashEscapedQuote is the MEDIUM-2 downstream check:
// in MySQL a backslash-escaped quote stays inside the literal, so the ';' after
// it must not split the statement and the following statement must survive.
func TestStatementsMysqlBackslashEscapedQuote(t *testing.T) {
	script := "INSERT INTO t VALUES ('it\\'s; fine');\nSELECT 1;"
	got := Statements(script, mysqlKeywords{kw})
	if len(got) != 2 {
		t.Fatalf("got %d statements: %+v, want 2", len(got), got)
	}
	if got[0].Text != `INSERT INTO t VALUES ('it\'s; fine')` {
		t.Errorf("statement 0 = %q", got[0].Text)
	}
	if got[1].Text != "SELECT 1" {
		t.Errorf("statement 1 = %q", got[1].Text)
	}
	// The reported offsets still slice exactly the statement text back out.
	if got[1].Start != strings.Index(script, "SELECT 1") {
		t.Errorf("statement 1 starts at %d, want %d", got[1].Start, strings.Index(script, "SELECT 1"))
	}
}

// TestStatementsInvalidExponentKeepsStatementText is the MEDIUM-3 downstream
// check: after `1e+` the following statement must keep its first rune.
func TestStatementsInvalidExponentKeepsStatementText(t *testing.T) {
	script := "SELECT 1e+ FROM t; DELETE FROM users"
	got := Statements(script, kw)
	if len(got) != 2 {
		t.Fatalf("got %d statements: %+v, want 2", len(got), got)
	}
	if got[0].Text != "SELECT 1e+ FROM t" {
		t.Errorf("statement 0 = %q, want %q", got[0].Text, "SELECT 1e+ FROM t")
	}
	if got[1].Text != "DELETE FROM users" {
		t.Errorf("statement 1 = %q, want %q", got[1].Text, "DELETE FROM users")
	}
	if got[1].Start != strings.Index(script, "DELETE") {
		t.Errorf("statement 1 starts at %d, want %d", got[1].Start, strings.Index(script, "DELETE"))
	}
	if got[1].End != len([]rune(script)) {
		t.Errorf("statement 1 ends at %d, want %d", got[1].End, len([]rune(script)))
	}
}

func TestStatementsUnterminatedDollarQuoteDegrades(t *testing.T) {
	// A body the user is still typing (no closing delimiter yet) falls back
	// to plain semicolon splitting instead of merging the whole script.
	got := Statements("SELECT $tag$ a; SELECT 2", kw)
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 statements", got)
	}
	// A block whose END is not typed yet stays one statement.
	got = Statements("CREATE TRIGGER t AFTER INSERT ON u BEGIN INSERT INTO a VALUES (1); SELECT 2", kw)
	if len(got) != 1 {
		t.Fatalf("open block: got %+v, want 1 statement", got)
	}
}

// ---- multi-byte characters ----

// multibyteScript mixes a Chinese string literal into a two-line script.
// Line 2 starts at rune 23 but at byte 27 (each Chinese character takes three
// bytes), which is exactly the arithmetic a byte-based lineOffsets gets wrong.
const multibyteScript = "SELECT '中文'; SELECT 2;\nSELECT 'x'; SELECT 4"

// TestStatementsMultibyteOffsets pins the rune offsets of statements in
// scripts containing Chinese (3 bytes/rune) and emoji (4 bytes/rune). Every
// expected Start/End below is a rune offset; the comments record the byte
// offsets the old mixed byte/rune arithmetic produced, so reverting the fix
// turns these cases red.
func TestStatementsMultibyteOffsets(t *testing.T) {
	type stmt struct {
		start, end int
		text       string
	}
	cases := []struct {
		name   string
		script string
		want   []stmt
	}{
		{
			// Byte-based line starts would report stmt 2 at Start 27
			// instead of 23 (line 1 costs 4 extra bytes) and slice its
			// Text from the wrong runes.
			name:   "chinese literal, statement on next line",
			script: multibyteScript,
			want: []stmt{
				{0, 11, "SELECT '中文'"},
				{13, 21, "SELECT 2"},
				{23, 33, "SELECT 'x'"},
				{35, 43, "SELECT 4"},
			},
		},
		{
			// The emoji costs one rune but four bytes, so line 3 starts
			// at rune 34 while its byte offset is 41.
			name:   "emoji comment shifts following lines",
			script: "-- 🎉 party\nSELECT '中文'; SELECT 3;\nSELECT 5",
			want: []stmt{
				{11, 22, "SELECT '中文'"},
				{24, 32, "SELECT 3"},
				{34, 42, "SELECT 5"},
			},
		},
		{
			// The comment is not a statement of its own, but it still
			// shifts the line offsets: line 3 starts at rune 18, at
			// byte 32 under the old arithmetic (which would collapse
			// the second statement into an empty, dropped piece).
			name:   "chinese comment between statements",
			script: "SELECT 1;\n-- 中文注释\nSELECT 2",
			want: []stmt{
				{0, 8, "SELECT 1"},
				{18, 26, "SELECT 2"},
			},
		},
	}
	for _, tc := range cases {
		got := Statements(tc.script, kw)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d statements %+v, want %d", tc.name, len(got), got, len(tc.want))
			continue
		}
		for i, w := range tc.want {
			if got[i].Start != w.start || got[i].End != w.end || got[i].Text != w.text {
				t.Errorf("%s: statement %d = {Start:%d End:%d Text:%q}, want {Start:%d End:%d Text:%q}",
					tc.name, i, got[i].Start, got[i].End, got[i].Text, w.start, w.end, w.text)
			}
		}
	}
}

// TestStatementAtMultibyte checks that a caret offset (a rune offset) maps
// onto the right statement of a script containing multi-byte characters.
func TestStatementAtMultibyte(t *testing.T) {
	cases := map[int]string{
		8:  "SELECT '中文'", // inside the multi-byte literal (on the 中)
		11: "SELECT '中文'", // right behind the literal, still inside stmt 0
		24: "SELECT 'x'",  // on line 2, right after the multi-byte shift (byte offset would be 28)
		41: "SELECT 4",    // the last character of the script
	}
	for offset, want := range cases {
		got, ok := StatementAt(multibyteScript, offset, kw)
		if !ok {
			t.Fatalf("offset %d: no statement found", offset)
		}
		if got.Text != want {
			t.Errorf("offset %d = %q, want %q", offset, got.Text, want)
		}
	}
	// The single space at rune 12 sits between the two statements of line 1.
	if _, ok := StatementAt(multibyteScript, 12, kw); ok {
		t.Error("offset 12 sits between two statements and must not match one")
	}
}

// TestStatementsMultibyteTriggerBody splits a compound statement whose body
// contains a Chinese literal: the inner semicolons must not split it, and the
// statement after the block must keep its rune offsets (line starts in runes
// 0/45/81/103/108, in bytes 0/45/83/105/110 — the old arithmetic reported the
// trailing SELECT at Start 110 and sliced "lect 1").
func TestStatementsMultibyteTriggerBody(t *testing.T) {
	script := "create trigger t after insert on users begin\n" +
		"  insert into t(msg) values ('中文');\n" +
		"  update t set n = 1;\n" +
		"end;\nselect 1"
	got := Statements(script, kw)
	if len(got) != 2 {
		t.Fatalf("got %d statements: %+v, want 2", len(got), got)
	}
	if n := strings.Count(got[0].Text, ";"); n != 2 {
		t.Errorf("trigger statement has %d semicolons, want 2", n)
	}
	if !strings.Contains(got[0].Text, "'中文'") || !strings.HasSuffix(got[0].Text, "end") {
		t.Errorf("trigger statement = %q", got[0].Text)
	}
	if got[1].Text != "select 1" || got[1].Start != 108 || got[1].End != 116 {
		t.Errorf("statement 1 = {Start:%d End:%d Text:%q}, want {Start:108 End:116 Text:\"select 1\"}",
			got[1].Start, got[1].End, got[1].Text)
	}
}
