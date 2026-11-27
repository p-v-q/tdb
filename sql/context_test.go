package sql

import "testing"

func TestContextAtStatementStart(t *testing.T) {
	ctx := AnalyzeContext("", Pos{0, 0}, testKeywords)
	if ctx.Kind != CtxDefault {
		t.Fatalf("kind = %v, want default", ctx.Kind)
	}
	if ctx.Prefix != "" {
		t.Fatalf("prefix = %q", ctx.Prefix)
	}
}

func TestContextAfterFrom(t *testing.T) {
	ctx := AnalyzeContext("SELECT * FROM ", Pos{0, 14}, testKeywords)
	if ctx.Kind != CtxTable {
		t.Fatalf("kind = %v, want table", ctx.Kind)
	}
}

func TestContextAfterFromWithPrefix(t *testing.T) {
	ctx := AnalyzeContext("SELECT * FROM pro", Pos{0, 17}, testKeywords)
	if ctx.Kind != CtxTable {
		t.Fatalf("kind = %v, want table", ctx.Kind)
	}
	if ctx.Prefix != "pro" {
		t.Fatalf("prefix = %q, want pro", ctx.Prefix)
	}
	if ctx.PrefixStart != (Pos{0, 14}) {
		t.Fatalf("prefix start = %v, want 0:14", ctx.PrefixStart)
	}
}

func TestContextAfterJoin(t *testing.T) {
	ctx := AnalyzeContext("SELECT * FROM users u JOIN ", Pos{0, 27}, testKeywords)
	if ctx.Kind != CtxTable {
		t.Fatalf("kind = %v, want table", ctx.Kind)
	}
	if len(ctx.Scope) != 1 || ctx.Scope[0].Table != "users" || ctx.Scope[0].Alias != "u" {
		t.Fatalf("scope = %+v, want [{users u}]", ctx.Scope)
	}
}

func TestContextAliasInScope(t *testing.T) {
	text := "SELECT u.id FROM users AS u, orders o WHERE "
	ctx := AnalyzeContext(text, Pos{0, len(text)}, testKeywords)
	want := []ScopeEntry{{Table: "users", Alias: "u"}, {Table: "orders", Alias: "o"}}
	if len(ctx.Scope) != len(want) {
		t.Fatalf("scope = %+v, want %+v", ctx.Scope, want)
	}
	for i := range want {
		if ctx.Scope[i] != want[i] {
			t.Fatalf("scope = %+v, want %+v", ctx.Scope, want)
		}
	}
}

func TestContextAfterWhere(t *testing.T) {
	text := "SELECT * FROM users WHERE "
	ctx := AnalyzeContext(text, Pos{0, len(text)}, testKeywords)
	if ctx.Kind != CtxColumn {
		t.Fatalf("kind = %v, want column", ctx.Kind)
	}
}

func TestContextQualifiedAlias(t *testing.T) {
	text := "SELECT u. FROM users u"
	ctx := AnalyzeContext(text, Pos{0, 9}, testKeywords)
	if ctx.Kind != CtxQualified {
		t.Fatalf("kind = %v, want qualified", ctx.Kind)
	}
	if ctx.Qualifier != "u" {
		t.Fatalf("qualifier = %q, want u", ctx.Qualifier)
	}
	if ctx.Prefix != "" {
		t.Fatalf("prefix = %q, want empty", ctx.Prefix)
	}
}

func TestContextQualifiedWithPrefix(t *testing.T) {
	text := "SELECT u.em FROM users u"
	ctx := AnalyzeContext(text, Pos{0, 11}, testKeywords)
	if ctx.Kind != CtxQualified || ctx.Qualifier != "u" {
		t.Fatalf("ctx = %+v", ctx)
	}
	if ctx.Prefix != "em" {
		t.Fatalf("prefix = %q, want em", ctx.Prefix)
	}
}

func TestContextSchemaQualifier(t *testing.T) {
	text := "SELECT public. FROM users u"
	ctx := AnalyzeContext(text, Pos{0, 14}, testKeywords)
	if ctx.Kind != CtxSchema {
		t.Fatalf("kind = %v, want schema", ctx.Kind)
	}
	if ctx.Qualifier != "public" {
		t.Fatalf("qualifier = %q", ctx.Qualifier)
	}
}

func TestContextAfterSelectListComma(t *testing.T) {
	text := "SELECT id, FROM users"
	ctx := AnalyzeContext(text, Pos{0, 11}, testKeywords)
	if ctx.Kind != CtxColumn {
		t.Fatalf("kind = %v, want column", ctx.Kind)
	}
}

func TestContextStatementStartAfterSemicolon(t *testing.T) {
	text := "SELECT 1; SELECT * FROM "
	ctx := AnalyzeContext(text, Pos{0, len(text)}, testKeywords)
	if ctx.Kind != CtxTable {
		t.Fatalf("kind = %v, want table", ctx.Kind)
	}
	if ctx.StatementStart != (Pos{0, 10}) {
		t.Fatalf("statement start = %v, want 0:10", ctx.StatementStart)
	}
}

func TestContextKeywordsFinishedHaveEmptyPrefix(t *testing.T) {
	// When FROM has just been typed in full (the cursor is right after the
	// keyword), it should not be treated as the prefix.
	text := "SELECT * FROM"
	ctx := AnalyzeContext(text, Pos{0, 13}, testKeywords)
	if ctx.Prefix != "" {
		t.Fatalf("prefix = %q, want empty", ctx.Prefix)
	}
}

func TestContextInsideIdentifier(t *testing.T) {
	// Cursor in the middle of an identifier: SELECT use|rs FROM users
	text := "SELECT users FROM users"
	ctx := AnalyzeContext(text, Pos{0, 10}, testKeywords)
	if ctx.Prefix != "use" {
		t.Fatalf("prefix = %q, want use", ctx.Prefix)
	}
	if ctx.PrefixStart != (Pos{0, 7}) {
		t.Fatalf("prefix start = %v, want 0:7", ctx.PrefixStart)
	}
}

// ddlKeywords extends the shared test keyword set with the DDL words that
// dialects disagree on (TRIGGER/FUNCTION/PROCEDURE are keywords only in some
// dialects), so the DDL tests exercise both lexing paths: those words as
// keywords and as plain identifiers.
func ddlKeywords() Keywords {
	kw := NewKeywords(
		"DROP", "ALTER", "TRIGGER", "INDEX", "VIEW", "FUNCTION", "PROCEDURE",
		"SEQUENCE", "MATERIALIZED", "COLUMN", "ADD", "RENAME", "IF", "EXISTS",
	)
	for w := range testKeywords {
		kw[w] = true
	}
	return kw
}

func TestContextDDLObjectPositions(t *testing.T) {
	cases := []struct {
		name string
		text string
		want ContextKind
	}{
		{"drop table", "DROP TABLE ", CtxTable},
		{"drop view", "DROP VIEW ", CtxView},
		{"drop materialized view", "DROP MATERIALIZED VIEW ", CtxView},
		{"drop trigger", "DROP TRIGGER ", CtxTrigger},
		{"drop index", "DROP INDEX ", CtxIndex},
		{"drop function", "DROP FUNCTION ", CtxFunction},
		{"drop procedure", "DROP PROCEDURE ", CtxProcedure},
		{"drop sequence", "DROP SEQUENCE ", CtxSequence},
		{"drop trigger if exists", "DROP TRIGGER IF EXISTS ", CtxTrigger},
		{"drop table if exists", "DROP TABLE IF EXISTS ", CtxTable},
		{"alter table", "ALTER TABLE ", CtxTable},
		{"alter view", "ALTER VIEW ", CtxView},
		{"alter trigger", "ALTER TRIGGER ", CtxTrigger},
		{"alter index", "ALTER INDEX ", CtxIndex},
		{"alter function", "ALTER FUNCTION ", CtxFunction},
		{"alter procedure", "ALTER PROCEDURE ", CtxProcedure},
		{"alter sequence", "ALTER SEQUENCE ", CtxSequence},
		{"alter table add column", "ALTER TABLE users ADD COLUMN ", CtxColumn},
		{"alter table drop column", "ALTER TABLE users DROP COLUMN ", CtxColumn},
		{"alter table rename column", "ALTER TABLE users RENAME COLUMN ", CtxColumn},
		{"alter table alter column", "ALTER TABLE users ALTER COLUMN ", CtxColumn},
		{"after semicolon", "SELECT 1; DROP VIEW ", CtxView},
	}
	for _, c := range cases {
		for _, kw := range []struct {
			name string
			look KeywordLookup
		}{{"ddl words as identifiers", testKeywords}, {"ddl words as keywords", ddlKeywords()}} {
			text := c.text
			ctx := AnalyzeContext(text, Pos{0, len(text)}, kw.look)
			if ctx.Kind != c.want {
				t.Errorf("%s (%s): kind = %v, want %v", c.name, kw.name, ctx.Kind, c.want)
			}
			if ctx.Prefix != "" {
				t.Errorf("%s (%s): prefix = %q, want empty", c.name, kw.name, ctx.Prefix)
			}
		}
	}
}

func TestContextDDLObjectPositionWithPrefix(t *testing.T) {
	text := "DROP TRIGGER my_tri"
	ctx := AnalyzeContext(text, Pos{0, len(text)}, ddlKeywords())
	if ctx.Kind != CtxTrigger {
		t.Fatalf("kind = %v, want trigger", ctx.Kind)
	}
	if ctx.Prefix != "my_tri" {
		t.Fatalf("prefix = %q, want my_tri", ctx.Prefix)
	}
	if ctx.PrefixStart != (Pos{0, 13}) {
		t.Fatalf("prefix start = %v, want 0:13", ctx.PrefixStart)
	}
}

func TestContextDDLNotTriggeredByTypeWordAlone(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		// An object-type word without a statement-initial DROP/ALTER stays a
		// general position: "view" is an alias here, not a type selector.
		{"type word as alias", "SELECT view "},
		// CREATE names a new object and is deliberately not recognized.
		{"create verb", "CREATE VIEW "},
		// The object *name* may itself equal a type word; the type token must
		// directly follow the DDL verb.
		{"function named trigger", "DROP FUNCTION trigger "},
	}
	for _, c := range cases {
		for _, kw := range []struct {
			name string
			look KeywordLookup
		}{{"ddl words as identifiers", testKeywords}, {"ddl words as keywords", ddlKeywords()}} {
			text := c.text
			ctx := AnalyzeContext(text, Pos{0, len(text)}, kw.look)
			if ctx.Kind != CtxDefault {
				t.Errorf("%s (%s): kind = %v, want default", c.name, kw.name, ctx.Kind)
			}
		}
	}
}

// multilineQuotedIdentText builds a script whose quoted identifier spans two
// lines, with a FROM clause after it.
func multilineQuotedIdentText(open, close string) string {
	return "SELECT " + open + "abc\ndef" + close + " FROM t"
}

// TestContextMultilineQuotedIdentNoPanic is the BLOCKER regression: the lexer
// lets "…", `…` and […] identifiers span newlines, so a caret inside such a
// token on a later row made wordBeforeCursor compute a negative column offset
// (cursor.Col - t.Pos.Col) and slice runes[:n] out of range.
func TestContextMultilineQuotedIdentNoPanic(t *testing.T) {
	// The exact crashing input from the report: unterminated "... multi-line
	// quoted identifier, caret on the line after its start (row 0, col 7).
	if ctx := AnalyzeContext("SELECT \"abc\n", Pos{Row: 1, Col: 0}, nil); ctx.Prefix != "" {
		t.Fatalf("prefix = %q, want empty", ctx.Prefix)
	}

	quotes := []struct{ name, open, close string }{
		{"double quote", `"`, `"`},
		{"backtick", "`", "`"},
		{"bracket", "[", "]"},
	}
	for _, q := range quotes {
		text := multilineQuotedIdentText(q.open, q.close)
		// Caret on the middle (second) row, inside the identifier: no panic,
		// and no single-line prefix is claimed.
		ctx := AnalyzeContext(text, Pos{Row: 1, Col: 2}, testKeywords)
		if ctx.Prefix != "" {
			t.Errorf("%s, caret on middle row: prefix = %q, want empty", q.name, ctx.Prefix)
		}
		if ctx.PrefixStart != (Pos{1, 2}) {
			t.Errorf("%s, caret on middle row: prefix start = %v, want 1:2", q.name, ctx.PrefixStart)
		}
		// Caret on the token's own (start) row: only the same-row part before
		// the caret is taken, so the result stays correct on that row.
		ctx = AnalyzeContext(text, Pos{Row: 0, Col: 9}, testKeywords)
		wantPrefix := q.open + "a"
		if ctx.Prefix != wantPrefix {
			t.Errorf("%s, caret on start row: prefix = %q, want %q", q.name, ctx.Prefix, wantPrefix)
		}
		if ctx.PrefixStart != (Pos{0, 7}) {
			t.Errorf("%s, caret on start row: prefix start = %v, want 0:7", q.name, ctx.PrefixStart)
		}
	}
}

// TestWordBeforeCursorRobustPositions pins wordBeforeCursor itself: the caret
// may sit on a later row of a multi-line token, just before a token, or before
// the whole stream, and none of those may slice with a negative offset.
func TestWordBeforeCursorRobustPositions(t *testing.T) {
	toks := Lex("SELECT \"abc\ndef\" FROM t", testKeywords)
	quoted := -1
	for i, tok := range toks {
		if tok.Kind == KindQuotedIdent {
			quoted = i
			break
		}
	}
	if quoted < 0 {
		t.Fatal("quoted identifier token not found")
	}
	// Real call shape: idx is the first token at/after the caret, the loop then
	// examines the multi-line quoted identifier that starts on an earlier row.
	if word, start := wordBeforeCursor(toks, quoted+2, Pos{1, 2}); word != "" || start != (Pos{1, 2}) {
		t.Errorf("caret on later row: word = %q at %v, want empty at 1:2", word, start)
	}
	// A caret in the whitespace just before the token reports an empty word.
	if word, start := wordBeforeCursor(toks, quoted, Pos{0, 6}); word != "" || start != (Pos{0, 6}) {
		t.Errorf("caret before token: word = %q at %v, want empty at 0:6", word, start)
	}
	// A caret with no preceding token (idx 0) is safe too.
	if word, start := wordBeforeCursor(toks, 0, Pos{0, 0}); word != "" || start != (Pos{0, 0}) {
		t.Errorf("caret before stream: word = %q at %v, want empty at 0:0", word, start)
	}
}

// FuzzAnalyzeContextNoPanic feeds arbitrary scripts, seeded with the quote and
// newline shapes that used to crash, to AnalyzeContext at every rune position.
// No position may panic.
func FuzzAnalyzeContextNoPanic(f *testing.F) {
	for _, seed := range []string{
		`SELECT "abc` + "\n",
		"SELECT `abc\ndef` FROM t",
		"SELECT [abc\ndef] FROM t",
		"SELECT \"a\nb\" ; SELECT 2\n",
		"SELECT 'abc\ndef' FROM ",
		"\n\n\"`[",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		runes := []rune(text)
		row, col := 0, 0
		for i := 0; i <= len(runes); i++ {
			AnalyzeContext(text, Pos{Row: row, Col: col}, testKeywords)
			if i < len(runes) {
				if runes[i] == '\n' {
					row, col = row+1, 0
				} else {
					col++
				}
			}
		}
	})
}
