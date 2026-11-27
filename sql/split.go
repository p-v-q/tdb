package sql

// split.go turns a script into single statements.
//
// Splitting on every semicolon would be wrong: semicolons appear inside
// string literals, quoted identifiers, comments and the bodies of compound
// statements (triggers, routines, DO blocks). The lexer already knows about
// the first three, so the split runs over its tokens; block depth covers the
// fourth — a semicolon only ends a statement at depth zero.

import "strings"

// Statement is one statement of a script, with the rune offsets of its text
// inside the script (so a caret position can be mapped onto a statement).
type Statement struct {
	Start, End int // rune offsets into the script, End exclusive
	Text       string
}

// Statements splits a script into single statements. Surrounding whitespace and
// the trailing semicolon are not part of a statement, and empty pieces are
// dropped, so `;;` yields nothing at all.
//
// Semicolons inside a compound-statement body (BEGIN … END, including the
// CASE … END and MySQL END IF / END LOOP / END WHILE / END REPEAT variants)
// and inside dollar-quoted text do not end a statement; only a semicolon at
// block depth zero does.
func Statements(text string, kw KeywordLookup) []Statement {
	tokens := Lex(text, kw)
	runes := []rune(text)
	starts := lineOffsets(text)
	at := func(p Pos) int { return runeOffset(starts, p, len(runes)) }

	var (
		out   []Statement
		start = -1 // start of the statement being scanned
		last  = -1 // end of the last significant token
		depth int  // BEGIN/CASE nesting inside the statement being scanned
	)
	flush := func(end int) {
		if start < 0 {
			return
		}
		if end < start {
			end = start
		}
		out = append(out, Statement{
			Start: start,
			End:   end,
			Text:  strings.TrimSpace(string(runes[start:end])),
		})
		start, last, depth = -1, -1, 0
	}

	for i, tok := range tokens {
		switch {
		case tok.Kind == KindEOF:
			flush(last + 1)
		case tok.Kind == KindWhitespace:
			continue // whitespace never extends a statement
		case tok.Kind == KindComment && start < 0:
			continue // leading comments are not part of a statement
		case tok.Kind == KindPunct && tok.Text == ";" && depth == 0:
			flush(last + 1)
		default:
			if start < 0 {
				start = at(tok.Pos)
			}
			last = at(tok.End) - 1
			// Inside a body the semicolon (also a default case above) simply
			// extends the statement; words update the block depth.
			depth += blockDelta(tokens, i)
			if depth < 0 {
				depth = 0 // a stray END never hides later statements
			}
		}
	}
	if start >= 0 {
		flush(len(runes))
	}

	clean := out[:0]
	for _, s := range out {
		if s.Text != "" {
			clean = append(clean, s)
		}
	}
	return clean
}

// StatementAt returns the statement containing the given rune offset, or false
// when the offset sits between two statements.
func StatementAt(text string, offset int, kw KeywordLookup) (Statement, bool) {
	for _, s := range Statements(text, kw) {
		if offset >= s.Start && offset <= s.End {
			return s, true
		}
	}
	return Statement{}, false
}

// ---- compound-statement structure (shared with format.go) ----

// wordOf returns the upper-cased text of a word token and reports whether the
// token is one. Only keywords and plain identifiers count: a quoted
// identifier, string, comment or dollar-quoted body spelling "begin" or "end"
// must never influence block depth.
func wordOf(t Token) (string, bool) {
	if t.Kind != KindKeyword && t.Kind != KindIdent {
		return "", false
	}
	return strings.ToUpper(t.Text), true
}

// nextSignificant returns the index of the next token after i that is neither
// whitespace nor a comment, or -1 when the stream ends.
func nextSignificant(tokens []Token, i int) int {
	for j := i + 1; j < len(tokens); j++ {
		switch tokens[j].Kind {
		case KindWhitespace, KindComment:
			continue
		case KindEOF:
			return -1
		default:
			return j
		}
	}
	return -1
}

// previousWord returns the upper-cased word immediately before tokens[i],
// skipping whitespace and comments, or false when there is none.
func previousWord(tokens []Token, i int) (string, bool) {
	for j := i - 1; j >= 0; j-- {
		switch tokens[j].Kind {
		case KindWhitespace, KindComment:
			continue
		case KindEOF:
			return "", false
		}
		return wordOf(tokens[j])
	}
	return "", false
}

// transactionWords are the words that make a BEGIN a transaction statement
// rather than a block start: `BEGIN;`, `BEGIN TRANSACTION`, `BEGIN IMMEDIATE`
// (SQLite), `BEGIN ISOLATION LEVEL …` / `BEGIN READ WRITE …` (PostgreSQL).
var transactionWords = map[string]bool{
	"TRANSACTION": true, "WORK": true, "TXN": true,
	"DEFERRED": true, "IMMEDIATE": true, "EXCLUSIVE": true,
	"ISOLATION": true, "READ": true,
}

// blockBegin reports whether the BEGIN at tokens[i] opens a compound-statement
// body. Trade-off: BEGIN counts as a transaction start when the next
// significant token is ';' or a transaction word, and as a block otherwise.
// That keeps `BEGIN; … COMMIT;` scripts splittable while bare blocks
// (`BEGIN … END;`), `CREATE TRIGGER/PROCEDURE … BEGIN … END` and
// `BEGIN ATOMIC … END` bodies all nest. The residual ambiguities — an
// unquoted identifier spelled `begin` mid-statement, or a transaction BEGIN
// glued to another statement without a semicolon — err towards not splitting,
// which fails on execution as one statement instead of running fragments.
func blockBegin(tokens []Token, i int) bool {
	j := nextSignificant(tokens, i)
	if j < 0 {
		return false
	}
	if tokens[j].Kind == KindPunct && tokens[j].Text == ";" {
		return false
	}
	if w, ok := wordOf(tokens[j]); ok && transactionWords[w] {
		return false
	}
	return true
}

// variantEnd reports whether the END at tokens[i] closes an IF, LOOP, WHILE or
// REPEAT construct (`END IF`, `END LOOP`, …). Those openers are deliberately
// not counted — a plain IF also appears as the MySQL IF() function, and
// counting it would never balance — so their ENDs are neutral to match.
//
// CASE is not listed here: it opens a block (it also appears in ordinary CASE
// expressions), so `END CASE` must decrement. blockDelta recognizes the CASE in
// `END CASE` and does not count it as a new opener, leaving that pair balanced.
func variantEnd(tokens []Token, i int) bool {
	j := nextSignificant(tokens, i)
	if j < 0 {
		return false
	}
	w, ok := wordOf(tokens[j])
	return ok && (w == "IF" || w == "LOOP" || w == "WHILE" || w == "REPEAT")
}

// blockDelta returns how the token at tokens[i] changes block depth: a block
// BEGIN and a CASE open (+1), a plain END closes (-1), END IF/LOOP/WHILE/
// REPEAT and everything else are neutral (0). The splitter clamps the
// running depth at zero, and the formatter's matchBlockEnd pairs BEGIN and
// CASE with their ENDs under the same rules, so formatting and splitting can
// never disagree about where a body is.
func blockDelta(tokens []Token, i int) int {
	w, ok := wordOf(tokens[i])
	if !ok {
		return 0
	}
	switch w {
	case "BEGIN":
		if blockBegin(tokens, i) {
			return 1
		}
		return 0
	case "CASE":
		// `END CASE` closes a MySQL compound CASE statement (the stored-program
		// form `CASE … END CASE;`). The END before it is the closing END
		// (delta -1); this CASE is the trailing word of that closer, not a new
		// opener. Counting it again would leave the CASE block open, and the
		// rest of the script would merge into the body.
		if prev, ok := previousWord(tokens, i); ok && prev == "END" {
			return 0
		}
		return 1
	case "END":
		if variantEnd(tokens, i) {
			return 0
		}
		return -1
	}
	return 0
}

// matchBlockEnd returns the index of the END that closes the block BEGIN at
// tokens[i], or -1 when the block is still open (the user is mid-edit or the
// BEGIN is a false positive). The depth rules mirror blockDelta exactly.
func matchBlockEnd(tokens []Token, i int) int {
	depth := 1
	for j := i + 1; j < len(tokens); j++ {
		if tokens[j].Kind == KindEOF {
			return -1
		}
		depth += blockDelta(tokens, j)
		if depth <= 0 {
			return j
		}
	}
	return -1
}

// lineOffsets returns the rune offset of the first character of each line.
// It must count runes, not bytes: the lexer's Pos.Col is a rune column and
// runeOffset adds it to a line start, so a byte-based start here would shift
// every position after a multi-byte character (Chinese, emoji) and slice
// statements and verbatim block bodies at the wrong place.
func lineOffsets(text string) []int {
	starts := []int{0}
	off := 0
	for _, r := range text {
		off++ // range over a string yields one item per rune
		if r == '\n' {
			starts = append(starts, off)
		}
	}
	return starts
}

// runeOffset maps a lexer position to a rune offset into a text of n runes,
// clamped to the end of the text for positions on or past the last line.
func runeOffset(starts []int, p Pos, n int) int {
	if p.Row < 0 || p.Row >= len(starts) {
		return n
	}
	return min(starts[p.Row]+p.Col, n)
}
