// Package sql provides SQL lexical analysis, syntax highlighting, and
// completion-context analysis.
//
// It is a deliberately lightweight "good enough" implementation: rather than
// aiming for a complete syntax tree, it focuses on the capabilities an editor
// actually needs—highlighting, cursor-context detection, statement-range
// recognition, and token-preserving formatting. A full parser (static checks,
// semantic analysis) can be introduced in a later version as needed.
package sql

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind identifies the type of a lexical token.
type TokenKind int

const (
	KindWhitespace TokenKind = iota
	KindComment
	KindKeyword
	KindIdent       // plain identifier
	KindQuotedIdent // quoted identifier "users" / `users`
	KindString
	KindNumber
	KindOperator
	KindPunct
	KindParam // ? :name $1
	KindEOF
)

// String returns the display name of the kind.
func (k TokenKind) String() string {
	switch k {
	case KindWhitespace:
		return "whitespace"
	case KindComment:
		return "comment"
	case KindKeyword:
		return "keyword"
	case KindIdent:
		return "identifier"
	case KindQuotedIdent:
		return "quoted identifier"
	case KindString:
		return "string"
	case KindNumber:
		return "number"
	case KindOperator:
		return "operator"
	case KindPunct:
		return "punctuation"
	case KindParam:
		return "parameter"
	default:
		return "eof"
	}
}

// IsSignificant reports whether the token participates in semantic analysis
// (excluding whitespace and comments).
func (k TokenKind) IsSignificant() bool {
	return k != KindWhitespace && k != KindComment && k != KindEOF
}

// Pos is a position in the text; Row and Col are both 0-based.
type Pos struct {
	Row int
	Col int
}

// Less reports whether p is strictly before q.
func (p Pos) Less(q Pos) bool {
	return p.Row < q.Row || (p.Row == q.Row && p.Col < q.Col)
}

// LessEqual reports whether p is before q or equal to it.
func (p Pos) LessEqual(q Pos) bool { return p.Less(q) || p == q }

// String returns "row:col", which is handy for debugging.
func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Row, p.Col) }

// Token is a lexical token.
type Token struct {
	Kind TokenKind
	Text string
	Pos  Pos // start position
	End  Pos // end position (exclusive)
}

// KeywordLookup reports whether a word is a keyword; dialects provide the
// implementation.
type KeywordLookup interface {
	IsKeyword(word string) bool
}

// StringEscapes is an optional extension to KeywordLookup. A dialect whose
// string literals use backslash escapes (MySQL/MariaDB: \' \" \\ \n …) reports
// true. PostgreSQL must report false: with standard_conforming_strings=on (the
// default since 9.1) a plain '…' literal is standard-conforming and a backslash
// is an ordinary character, so treating \' as an escape there would mis-lex
// valid PostgreSQL text. SQLite likewise has no backslash escapes.
type StringEscapes interface {
	BackslashEscapes() bool
}

// Keywords is a map-based set of keywords that implements KeywordLookup.
type Keywords map[string]bool

// IsKeyword implements KeywordLookup (case-insensitive).
func (k Keywords) IsKeyword(word string) bool {
	return k[strings.ToUpper(word)]
}

// NewKeywords builds a case-insensitive set of keywords.
func NewKeywords(words ...string) Keywords {
	set := make(Keywords, len(words))
	for _, w := range words {
		set[strings.ToUpper(w)] = true
	}
	return set
}

// multiCharOps are multi-character operators, which must be matched before
// single-character ones.
var multiCharOps = []string{
	"->>", "->", "#>>", "#>", "<<=", ">>=", "!==", "!==", "!<=", "!>=",
	"::", "<>", "!=", "<=", ">=", "||", "&&", ":=", "<<", ">>", "|/", "||/",
	"!~", "!~*", "~*", "=>", "@>", "<@", "?|", "?&", "#-",
}

var singleCharOps = "+-*/%=<>!~|&^"

// Lex tokenizes text. When kw is nil, no keywords are recognized (every word is
// treated as an identifier).
func Lex(text string, kw KeywordLookup) []Token {
	l := &lexer{input: []rune(text), kw: kw, backslashEscapes: backslashEscapes(kw)}
	l.run()
	return l.tokens
}

// backslashEscapes reports whether string literals of the dialect behind kw use
// backslash escapes. An explicit StringEscapes implementation wins; otherwise
// the dialect Name is consulted, because the editor passes the full dialect
// value (which also satisfies KeywordLookup) and MySQL/MariaDB scripts must lex
// correctly without threading a new option through every call site.
func backslashEscapes(kw KeywordLookup) bool {
	if kw == nil {
		return false
	}
	if se, ok := kw.(StringEscapes); ok {
		return se.BackslashEscapes()
	}
	if d, ok := kw.(interface{ Name() string }); ok {
		switch strings.ToLower(d.Name()) {
		case "mysql", "mariadb":
			return true
		}
	}
	return false
}

type lexer struct {
	input  []rune
	pos    int // rune index
	row    int
	col    int
	kw     KeywordLookup
	tokens []Token

	// backslashEscapes enables MySQL/MariaDB string escape rules (see
	// backslashEscapes).
	backslashEscapes bool
}

func (l *lexer) run() {
	for l.pos < len(l.input) {
		r := l.input[l.pos]
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			l.lexWhitespace()
		case r == '-' && l.peek(1) == '-':
			l.lexLineComment()
		case r == '/' && l.peek(1) == '*':
			l.lexBlockComment()
		case r == '\'':
			l.lexString('\'')
		case r == '"':
			l.lexQuotedIdent('"')
		case r == '`':
			l.lexQuotedIdent('`')
		case r == '[':
			l.lexBracketIdent()
		case isDigit(r) || (r == '.' && isDigit(l.peek(1))):
			l.lexNumber()
		case isIdentStart(r):
			l.lexIdent()
		case r == '$':
			// PostgreSQL dollar quoting is tried before parameters so that
			// $$ … $$ and $tag$ … $tag$ bodies stay a single string token;
			// a $1 parameter or a bare $ falls through unchanged.
			if l.lexDollarQuote() {
				continue
			}
			if l.lexParam() {
				continue
			}
			fallthrough
		case r == '?' || r == ':':
			if l.lexParam() {
				continue
			}
			fallthrough
		default:
			if !l.lexOperatorOrPunct() {
				l.emitRune()
			}
		}
	}
	end := Pos{l.row, l.col}
	l.tokens = append(l.tokens, Token{Kind: KindEOF, Pos: end, End: end})
}

func (l *lexer) peek(offset int) rune {
	if l.pos+offset < len(l.input) {
		return l.input[l.pos+offset]
	}
	return 0
}

func (l *lexer) advance() rune {
	r := l.input[l.pos]
	l.pos++
	if r == '\n' {
		l.row++
		l.col = 0
	} else {
		l.col++
	}
	return r
}

// emit outputs the text from start (an index) up to the current pos as a token.
func (l *lexer) emit(kind TokenKind, start int, row, col int) {
	text := string(l.input[start:l.pos])
	l.tokens = append(l.tokens, Token{Kind: kind, Text: text, Pos: Pos{row, col}, End: Pos{l.row, l.col}})
}

func (l *lexer) emitRune() {
	start, row, col := l.pos, l.row, l.col
	l.advance()
	l.emit(KindOperator, start, row, col)
}

func (l *lexer) lexWhitespace() {
	start, row, col := l.pos, l.row, l.col
	for l.pos < len(l.input) {
		r := l.input[l.pos]
		if r != ' ' && r != '\t' && r != '\r' && r != '\n' {
			break
		}
		l.advance()
	}
	l.emit(KindWhitespace, start, row, col)
}

func (l *lexer) lexLineComment() {
	start, row, col := l.pos, l.row, l.col
	for l.pos < len(l.input) && l.input[l.pos] != '\n' {
		l.advance()
	}
	l.emit(KindComment, start, row, col)
}

func (l *lexer) lexBlockComment() {
	start, row, col := l.pos, l.row, l.col
	l.advance() // /
	l.advance() // *
	for l.pos < len(l.input) {
		if l.input[l.pos] == '*' && l.peek(1) == '/' {
			l.advance()
			l.advance()
			break
		}
		l.advance()
	}
	l.emit(KindComment, start, row, col)
}

func (l *lexer) lexString(quote rune) {
	start, row, col := l.pos, l.row, l.col
	l.advance()
	for l.pos < len(l.input) {
		r := l.input[l.pos]
		if l.backslashEscapes && r == '\\' {
			// MySQL/MariaDB: a backslash escapes the next character, including
			// the closing quote (and another backslash), so only an unescaped
			// quote can end the literal. The escaped text is kept verbatim —
			// the server is what interprets the escape sequence.
			l.advance()
			if l.pos < len(l.input) {
				l.advance()
			}
			continue
		}
		if r == quote {
			l.advance()
			if l.peek(0) == quote { // '' escape
				l.advance()
				continue
			}
			break
		}
		l.advance()
	}
	l.emit(KindString, start, row, col)
}

func (l *lexer) lexQuotedIdent(quote rune) {
	start, row, col := l.pos, l.row, l.col
	l.advance()
	for l.pos < len(l.input) {
		r := l.input[l.pos]
		l.advance()
		if r == quote {
			break
		}
	}
	l.emit(KindQuotedIdent, start, row, col)
}

func (l *lexer) lexBracketIdent() {
	start, row, col := l.pos, l.row, l.col
	l.advance()
	for l.pos < len(l.input) && l.input[l.pos] != ']' {
		l.advance()
	}
	if l.pos < len(l.input) {
		l.advance()
	}
	l.emit(KindQuotedIdent, start, row, col)
}

func (l *lexer) lexNumber() {
	start, row, col := l.pos, l.row, l.col
	if l.input[l.pos] == '0' && l.peek(1) == 'x' {
		l.advance()
		l.advance()
		for l.pos < len(l.input) && isHex(l.input[l.pos]) {
			l.advance()
		}
		l.emit(KindNumber, start, row, col)
		return
	}
	for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		l.advance()
	}
	if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		// An exponent is only valid with at least one digit after e/E and an
		// optional sign. When it is incomplete (`1e`, `1e+`) the whole exponent
		// is un-consumed, restoring pos/row/col exactly: restoring only the
		// column (the old `l.col -= 1`) left the token end (and every later
		// token on the line) shifted by one for signed exponents.
		savePos, saveRow, saveCol := l.pos, l.row, l.col
		l.advance() // e/E
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			l.advance()
		}
		if l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.advance()
			}
		} else { // invalid exponent, back up
			l.pos, l.row, l.col = savePos, saveRow, saveCol
		}
	}
	l.emit(KindNumber, start, row, col)
}

func (l *lexer) lexIdent() {
	start, row, col := l.pos, l.row, l.col
	for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
		l.advance()
	}
	text := string(l.input[start:l.pos])
	kind := KindIdent
	if l.kw != nil && l.kw.IsKeyword(text) {
		kind = KindKeyword
	}
	l.tokens = append(l.tokens, Token{Kind: kind, Text: text, Pos: Pos{row, col}, End: Pos{l.row, l.col}})
}

// lexDollarQuote consumes a PostgreSQL dollar-quoted string — $$ … $$ or
// $tag$ … $tag$ — and emits it, delimiters included, as one KindString token.
// The body then contains no visible ';' or quote for the splitter, the
// highlighter or the formatter, which is what keeps function, procedure and
// DO bodies in one piece.
//
// When the input only looks like a dollar quote (a $1 parameter, a bare $
// operator, or an opening delimiter with no matching close) it consumes
// nothing and reports false: the text degrades to ordinary tokens, so a
// half-typed body never swallows the rest of the script.
func (l *lexer) lexDollarQuote() bool {
	start, row, col := l.pos, l.row, l.col
	// Parse the opening delimiter: "$" [tag] "$", tag empty or an identifier.
	j := l.pos + 1
	if j >= len(l.input) {
		return false
	}
	if l.input[j] != '$' {
		if !isIdentStart(l.input[j]) { // $1 parameters start with a digit
			return false
		}
		for j < len(l.input) && isDollarTagPart(l.input[j]) {
			j++
		}
		if j >= len(l.input) || l.input[j] != '$' {
			return false
		}
	}
	delim := l.input[l.pos : j+1]
	rest := string(l.input[j+1:])
	idx := strings.Index(rest, string(delim))
	if idx < 0 { // unterminated: degrade to plain characters
		return false
	}
	closed := len([]rune(rest[:idx])) // byte offset to rune count
	total := len(delim) + closed + len(delim)
	for k := 0; k < total; k++ {
		l.advance()
	}
	l.emit(KindString, start, row, col)
	return true
}

// isDollarTagPart reports whether r may appear inside a dollar-quote tag. It
// follows identifier rules minus '$', so `$body$` is not read as `body$`.
func isDollarTagPart(r rune) bool { return isIdentStart(r) || isDigit(r) }

// lexParam recognizes a parameter placeholder and reports whether it matched.
func (l *lexer) lexParam() bool {
	start, row, col := l.pos, l.row, l.col
	r := l.input[l.pos]
	switch r {
	case '?':
		l.advance()
		l.emit(KindParam, start, row, col)
		return true
	case ':':
		if l.peek(1) == ':' { // :: cast is an operator
			return false
		}
		if isIdentStart(l.peek(1)) {
			l.advance()
			for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
				l.advance()
			}
			l.emit(KindParam, start, row, col)
			return true
		}
	case '$':
		if isDigit(l.peek(1)) {
			l.advance()
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.advance()
			}
			l.emit(KindParam, start, row, col)
			return true
		}
	}
	return false
}

func (l *lexer) lexOperatorOrPunct() bool {
	rest := string(l.input[l.pos:])
	for _, op := range multiCharOps {
		if strings.HasPrefix(rest, op) {
			start, row, col := l.pos, l.row, l.col
			for range op {
				l.advance()
			}
			l.emit(KindOperator, start, row, col)
			return true
		}
	}
	r := l.input[l.pos]
	if strings.ContainsRune("()[];,.", r) {
		start, row, col := l.pos, l.row, l.col
		l.advance()
		l.emit(KindPunct, start, row, col)
		return true
	}
	if strings.ContainsRune(singleCharOps, r) {
		start, row, col := l.pos, l.row, l.col
		l.advance()
		l.emit(KindOperator, start, row, col)
		return true
	}
	return false
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isHex(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || r >= utf8.RuneSelf
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || isDigit(r) || r == '$'
}
