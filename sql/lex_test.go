package sql

import (
	"strings"
	"testing"
)

var testKeywords = NewKeywords("SELECT", "FROM", "WHERE", "JOIN", "ON", "AS", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE", "GROUP", "ORDER", "BY", "AND", "OR", "CREATE", "TABLE", "LEFT", "INNER")

func kinds(tokens []Token) []TokenKind {
	out := make([]TokenKind, 0, len(tokens))
	for _, t := range tokens {
		if t.Kind == KindEOF {
			continue
		}
		out = append(out, t.Kind)
	}
	return out
}

func TestLexKeywordsAndIdents(t *testing.T) {
	toks := Lex("select * from users", testKeywords)
	want := []TokenKind{KindKeyword, KindWhitespace, KindOperator, KindWhitespace, KindKeyword, KindWhitespace, KindIdent}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d kind = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexPositions(t *testing.T) {
	toks := Lex("select a\nfrom b", testKeywords)
	var found bool
	for _, tok := range toks {
		if tok.Text == "from" {
			found = true
			if tok.Pos != (Pos{1, 0}) {
				t.Fatalf("from pos = %v, want 1:0", tok.Pos)
			}
			if tok.End != (Pos{1, 4}) {
				t.Fatalf("from end = %v, want 1:4", tok.End)
			}
		}
	}
	if !found {
		t.Fatal("from token not found")
	}
}

func TestLexStringWithEscapedQuote(t *testing.T) {
	toks := Lex("where name = 'it''s'", testKeywords)
	last := toks[len(toks)-2]
	if last.Kind != KindString || last.Text != "'it''s'" {
		t.Fatalf("last token = %+v", last)
	}
}

func TestLexMultilineStringAndComment(t *testing.T) {
	toks := Lex("/* a\nb */ select 'x\ny'", testKeywords)
	if toks[0].Kind != KindComment || toks[0].Pos != (Pos{0, 0}) || toks[0].End != (Pos{1, 4}) {
		t.Fatalf("comment token = %+v", toks[0])
	}
	var str Token
	for _, tok := range toks {
		if tok.Kind == KindString {
			str = tok
		}
	}
	if str.Text != "'x\ny'" || str.End.Row != 2 {
		t.Fatalf("string token = %+v", str)
	}
}

func TestLexOperators(t *testing.T) {
	toks := Lex("a <> b || c :: int <= 1", testKeywords)
	ops := []string{}
	for _, tok := range toks {
		if tok.Kind == KindOperator {
			ops = append(ops, tok.Text)
		}
	}
	want := []string{"<>", "||", "::", "<="}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
}

func TestLexParams(t *testing.T) {
	toks := Lex("where id = ? or id = :uid or id = $1", testKeywords)
	params := 0
	for _, tok := range toks {
		if tok.Kind == KindParam {
			params++
		}
	}
	if params != 3 {
		t.Fatalf("params = %d, want 3", params)
	}
}

func TestLexQuotedIdent(t *testing.T) {
	toks := Lex(`select "Weird Name", `+"`back`"+` from t`, testKeywords)
	quoted := 0
	for _, tok := range toks {
		if tok.Kind == KindQuotedIdent {
			quoted++
		}
	}
	if quoted != 2 {
		t.Fatalf("quoted = %d, want 2", quoted)
	}
}

func TestSpansSplitMultiline(t *testing.T) {
	lines := Spans(Lex("/* a\nb */ x", testKeywords))
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if lines[0][0].Text != "/* a" || lines[1][0].Text != "b */" {
		t.Fatalf("lines = %v", lines)
	}
	if lines[1][1].Text != " " || lines[1][2].Text != "x" {
		t.Fatalf("line 1 spans = %v", lines[1])
	}
}

// joinTokens concatenates all token texts, which must always reproduce the
// input exactly — lexing never loses or reorders text.
func joinTokens(toks []Token) string {
	var b strings.Builder
	for _, tok := range toks {
		b.WriteString(tok.Text)
	}
	return b.String()
}

func TestLexDollarQuotedString(t *testing.T) {
	toks := Lex("SELECT $$a;\nb$$", testKeywords)
	var str Token
	for _, tok := range toks {
		if tok.Kind == KindString {
			str = tok
		}
	}
	if str.Text != "$$a;\nb$$" {
		t.Fatalf("string token = %+v", str)
	}
	if str.Pos != (Pos{0, 7}) || str.End != (Pos{1, 3}) {
		t.Fatalf("string position = %v..%v, want 0:7..1:3", str.Pos, str.End)
	}
	if got := joinTokens(toks); got != "SELECT $$a;\nb$$" {
		t.Fatalf("tokens joined = %q", got)
	}
}

func TestLexDollarQuotedTagged(t *testing.T) {
	for _, in := range []string{
		"$body$ SELECT 1; $body$",
		"$body$ SELECT 'no; match'; $body$",
		"$$nested $body$ stays one token$$",
	} {
		toks := Lex(in, testKeywords)
		var str Token
		n := 0
		for _, tok := range toks {
			if tok.Kind == KindString {
				str = tok
				n++
			}
		}
		if n != 1 || str.Text != in {
			t.Errorf("Lex(%q): %d string tokens, last = %+v; want exactly one covering the input", in, n, str)
		}
	}
}

func TestLexDollarQuoteVersusParams(t *testing.T) {
	toks := Lex("WHERE id = $1 AND x = $tag$y$tag$", testKeywords)
	var params, strings int
	for _, tok := range toks {
		switch tok.Kind {
		case KindParam:
			params++
		case KindString:
			strings++
			if tok.Text != "$tag$y$tag$" {
				t.Errorf("string token = %+v", tok)
			}
		}
	}
	if params != 1 || strings != 1 {
		t.Fatalf("params = %d, strings = %d, want 1 and 1", params, strings)
	}
}

func TestLexDollarQuoteUnterminatedDegrades(t *testing.T) {
	// An opening delimiter with no close must not swallow the rest of the
	// text: the input degrades to ordinary tokens and stays reversible.
	for _, in := range []string{
		"SELECT $$a; b",
		"SELECT $tag$ a; SELECT 2",
		"SELECT $$",
		"SELECT $tag$",
	} {
		toks := Lex(in, testKeywords)
		for _, tok := range toks {
			if tok.Kind == KindString {
				t.Errorf("Lex(%q): unterminated dollar quote became a string token %+v", in, tok)
			}
		}
		if got := joinTokens(toks); got != in {
			t.Errorf("Lex(%q): tokens joined = %q", in, got)
		}
	}
}

func TestLexDollarQuoteInsideStringAndComment(t *testing.T) {
	// The lexer is the single place that knows quoted regions, so a dollar
	// quote inside them is not special.
	toks := Lex("SELECT '$$x; y' /* $$ z */", testKeywords)
	if got := joinTokens(toks); got != "SELECT '$$x; y' /* $$ z */" {
		t.Fatalf("tokens joined = %q", got)
	}
	for _, tok := range toks {
		if tok.Kind == KindString && tok.Text != "'$$x; y'" {
			t.Fatalf("string token = %+v", tok)
		}
	}
}

func TestSpansDollarQuoteMultiline(t *testing.T) {
	lines := Spans(Lex("DO $$\nbegin\nend$$;", testKeywords))
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3: %v", len(lines), lines)
	}
	// The body lines are opaque string text, so highlighting cannot break it.
	if lines[0][2] != (Span{"$$", KindString}) {
		t.Fatalf("line 0 = %v", lines[0])
	}
	if lines[1][0] != (Span{"begin", KindString}) {
		t.Fatalf("line 1 = %v", lines[1])
	}
	if lines[2][0] != (Span{"end$$", KindString}) || lines[2][1] != (Span{";", KindPunct}) {
		t.Fatalf("line 2 = %v", lines[2])
	}
}

// ---- MySQL/MariaDB backslash escapes ----

// mysqlKeywords models the MySQL dialect for the lexer: it advertises backslash
// string escapes through the optional StringEscapes extension.
type mysqlKeywords struct{ Keywords }

func (mysqlKeywords) BackslashEscapes() bool { return true }

// namedKeywords models the full dialect values the editor passes as
// KeywordLookup: they only have Name/QuoteIdent/IsKeyword/Keywords/Functions.
type namedKeywords struct {
	Keywords
	name string
}

func (n namedKeywords) Name() string { return n.name }

// sqlStrings returns the text of every string token.
func sqlStrings(toks []Token) []string {
	var out []string
	for _, tok := range toks {
		if tok.Kind == KindString {
			out = append(out, tok.Text)
		}
	}
	return out
}

// checkTokenPositions verifies that the tokens tile text exactly: every token
// starts where the previous one ended and Pos/End match the rune offsets implied
// by the text. A uniform column shift (the invalid-exponent bug shifted every
// later token on the line by one) still passes a plain contiguity check, so the
// expected positions are recomputed from the text instead of from the tokens.
func checkTokenPositions(t *testing.T, text string, toks []Token) {
	t.Helper()
	row, col := 0, 0
	for i, tok := range toks {
		if tok.Pos != (Pos{row, col}) {
			t.Errorf("Lex(%q): token %d (%q) starts at %v, want %v", text, i, tok.Text, tok.Pos, Pos{row, col})
		}
		for _, r := range tok.Text {
			if r == '\n' {
				row, col = row+1, 0
			} else {
				col++
			}
		}
		if tok.End != (Pos{row, col}) {
			t.Errorf("Lex(%q): token %d (%q) ends at %v, want %v", text, i, tok.Text, tok.End, Pos{row, col})
		}
	}
}

// TestLexBackslashEscapedStringsMySQL covers MySQL/MariaDB literals: a
// backslash escapes the next character, so \' does not end the literal and \n
// stays one string. Doubled quotes stay one string as well.
func TestLexBackslashEscapedStringsMySQL(t *testing.T) {
	cases := []string{
		`'it\'s'`,
		`'a\nb'`,
		`'a\\b'`,
		`'it''s'`,
		`'\\'`,
	}
	for _, in := range cases {
		text := "SELECT " + in + " FROM t"
		for _, kw := range []struct {
			name string
			look KeywordLookup
		}{{"explicit StringEscapes", mysqlKeywords{testKeywords}}, {"dialect name", namedKeywords{Keywords: testKeywords, name: "mysql"}}} {
			toks := Lex(text, kw.look)
			strs := sqlStrings(toks)
			if len(strs) != 1 || strs[0] != in {
				t.Errorf("%s, %s: strings = %q, want exactly %q", in, kw.name, strs, in)
			}
			if got := joinTokens(toks); got != text {
				t.Errorf("%s, %s: tokens joined = %q", in, kw.name, got)
			}
		}
	}
}

// TestLexBackslashEscapesNotEnabledForPostgreSQL pins that the escape handling
// is dialect-gated: PostgreSQL (standard_conforming_strings=on), SQLite and the
// default dialect-agnostic lexer keep treating \ as an ordinary character, so
// a plain '…' literal still ends at the next quote.
func TestLexBackslashEscapesNotEnabledForPostgreSQL(t *testing.T) {
	text := `SELECT 'it\'s'`
	// 'it\' is the literal, then the identifier s, then an unterminated '…'
	// string: the backslash must not swallow the closing quote.
	want := []string{`'it\'`, `'`}
	lookups := []struct {
		name string
		look KeywordLookup
	}{
		{"postgres", namedKeywords{Keywords: testKeywords, name: "postgres"}},
		{"sqlite", namedKeywords{Keywords: testKeywords, name: "sqlite"}},
		{"explicit false", falseEscapes{testKeywords}},
		{"nil", nil},
		{"plain keywords", testKeywords},
	}
	for _, kw := range lookups {
		got := sqlStrings(Lex(text, kw.look))
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s: strings = %q, want %q", kw.name, got, want)
		}
	}
}

// falseEscapes is a KeywordLookup that explicitly reports standard-conforming
// strings, overriding the name-based dialect detection.
type falseEscapes struct{ Keywords }

func (falseEscapes) BackslashEscapes() bool { return false }

// TestLexBackslashEscapedQuoteKeepsPositions checks the token positions of a
// MySQL escaped string: the whole literal is one token, so the following tokens
// keep their true columns.
func TestLexBackslashEscapedQuoteKeepsPositions(t *testing.T) {
	toks := Lex(`SELECT 'it\'s' FROM t`, mysqlKeywords{testKeywords})
	checkTokenPositions(t, `SELECT 'it\'s' FROM t`, toks)
	// FROM sits at rune column 15: SELECT(6) + space + 'it\'s' (7 runes) + space.
	for _, tok := range toks {
		if tok.Text == "FROM" {
			if tok.Pos != (Pos{0, 15}) {
				t.Fatalf("FROM pos = %v, want 0:15", tok.Pos)
			}
			return
		}
	}
	t.Fatal("FROM token not found")
}

// ---- numeric exponents ----

// TestLexInvalidExponentKeepsFollowingTokensInPlace is the MEDIUM lexer
// regression: an incomplete exponent (`1e`, `1e+`) used to restore only one
// column, shifting the token end and every later token on the line by one, so
// the next statement lost its first rune.
func TestLexInvalidExponentKeepsFollowingTokensInPlace(t *testing.T) {
	for _, in := range []string{
		"SELECT 1e+ FROM t",
		"SELECT 1e- FROM t",
		"SELECT 1e FROM t",
		"SELECT 1E+ FROM t",
		"SELECT 1e+; DELETE FROM users",
		"SELECT 1.5e+ FROM t",
	} {
		toks := Lex(in, testKeywords)
		if got := joinTokens(toks); got != in {
			t.Errorf("Lex(%q): tokens joined = %q", in, got)
		}
		// Positions are recomputed from the text and compared with the reported
		// ones: a one-column shift of every later token used to slip through.
		checkTokenPositions(t, in, toks)
	}
}

// TestLexInvalidExponentSplitsNumber pins that only the mantissa becomes the
// number token and e/+ stay separate tokens starting at the right column.
func TestLexInvalidExponentSplitsNumber(t *testing.T) {
	toks := Lex("SELECT 1e+ FROM t", testKeywords)
	// SELECT(0:0-0:6) ws number '1'(0:7-0:8) ident 'e'(0:8-0:9) op '+'(0:9-0:10) …
	if toks[2].Kind != KindNumber || toks[2].Text != "1" || toks[2].Pos != (Pos{0, 7}) || toks[2].End != (Pos{0, 8}) {
		t.Fatalf("number token = %+v, want '1' at 0:7-0:8", toks[2])
	}
	if toks[3].Kind != KindIdent || toks[3].Text != "e" || toks[3].Pos != (Pos{0, 8}) {
		t.Fatalf("exponent token = %+v, want ident 'e' at 0:8", toks[3])
	}
	for _, tok := range toks {
		if tok.Text == "FROM" && tok.Pos != (Pos{0, 11}) {
			t.Fatalf("FROM pos = %v, want 0:11", tok.Pos)
		}
	}
}

// TestLexValidExponents confirms the validation does not reject complete
// exponents.
func TestLexValidExponents(t *testing.T) {
	for _, in := range []string{"1e10", "1E+10", "1.5e-3", "0.5E2"} {
		toks := Lex("SELECT "+in, testKeywords)
		num := toks[2]
		if num.Kind != KindNumber || num.Text != in {
			t.Errorf("Lex(%q): number token = %+v, want %q", in, num, in)
		}
	}
}
