package sql

// format.go implements the editor's SQL formatter. It is deliberately
// token-based: strings, quoted identifiers and comments are copied verbatim, so
// formatting can never change the meaning of a statement. The layout follows
// the conventions most SQL developers expect: keywords in upper case, one major
// clause per line, and one select-list item per line.
//
// Compound-statement bodies are exempt from all of that: a dollar-quoted body
// is a single string token, and a BEGIN … END body is copied byte for byte
// between BEGIN and its matching END (see blockBegin/matchBlockEnd), because
// reformatting inside a body would move semicolons and layout the database
// parses.

import (
	"strings"
)

// Format pretty-prints SQL using the keyword set of the active dialect.
func Format(text string, kw KeywordLookup) string {
	tokens := Lex(text, kw)
	runes := []rune(text)
	starts := lineOffsets(text)
	at := func(p Pos) int { return runeOffset(starts, p, len(runes)) }
	f := formatter{lineStart: true, stmtStart: true}

	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.Kind {
		case KindWhitespace, KindEOF:
			continue
		case KindComment:
			f.comment(t)
			continue
		}

		word := strings.ToUpper(t.Text)
		isKeyword := t.Kind == KindKeyword

		if t.Kind == KindPunct {
			switch t.Text {
			case "(", "[":
				f.writeToken(t)
				f.depth++
				continue
			case ")", "]":
				if f.depth > 0 {
					f.depth--
				}
				f.writeToken(t)
				continue
			case ";":
				f.writeRaw(";")
				f.last = &Token{Kind: KindPunct, Text: ";"}
				f.selectList, f.setList, f.stmtStart = false, false, true
				if hasMore(tokens, i+1) {
					f.blankLine()
				}
				continue
			}
		}

		// A compound-statement body is emitted verbatim: layout inside
		// BEGIN … END is part of what the database parses. The head before
		// BEGIN is still formatted normally, and the block detection is the
		// splitter's, so formatting never changes where statements break.
		if word == "BEGIN" && (t.Kind == KindKeyword || t.Kind == KindIdent) &&
			blockBegin(tokens, i) {
			f.writeToken(t)
			f.stmtStart = false
			endIdx := matchBlockEnd(tokens, i)
			bodyEnd := len(runes)
			if endIdx >= 0 {
				bodyEnd = at(tokens[endIdx].Pos)
			}
			// Raw write: the body's own whitespace, indentation and
			// case are copied unchanged, up to the end of the input
			// when no END matches yet.
			f.out.WriteString(string(runes[at(t.End):bodyEnd]))
			f.lineStart = false
			f.tightNext = false
			if endIdx >= 0 {
				f.writeToken(tokens[endIdx])
				f.stmtStart = false
				i = endIdx
			} else {
				i = len(tokens) - 1 // nothing left to format
			}
			continue
		}

		if f.depth == 0 {
			// SELECT gets special treatment so its list can be laid out one
			// item per line. It is also a clause break except at the start of a
			// statement.
			if isKeyword && word == "SELECT" {
				if !f.stmtStart {
					f.newline(0)
				}
				f.selectList, f.setList = false, false
				f.space()
				f.writeRaw("SELECT")
				f.last = &Token{Kind: KindKeyword, Text: "SELECT"}
				f.stmtStart = false
				// Keep DISTINCT/ALL on the SELECT line.
				if next, ok := nextWord(tokens, i+1); ok && (next == "DISTINCT" || next == "ALL") {
					i++
					f.space()
					f.writeRaw(next)
					f.last = &Token{Kind: KindKeyword, Text: next}
				}
				f.newline(2)
				f.selectList = true
				continue
			}

			if isKeyword && word == "BETWEEN" {
				f.between = true
			}
			if isKeyword {
				if indent, ok := clauseBreak(word); ok {
					switch {
					case word == "FROM" && f.lastWord() == "DELETE":
						// DELETE FROM is one phrase.
					case word == "JOIN" && isJoinModifier(f.lastWord()):
						// LEFT JOIN / INNER JOIN stay together.
					case word == "AND" && f.between:
						// BETWEEN ... AND ... is one expression, not a new
						// boolean condition.
						f.between = false
					default:
						f.selectList, f.setList = false, false
						f.newline(indent)
					}
				}
				if (word == "LEFT" || word == "RIGHT" || word == "FULL" ||
					word == "INNER" || word == "CROSS" || word == "NATURAL") &&
					nextIsJoin(tokens, i+1) {
					f.selectList, f.setList = false, false
					f.newline(0)
				}
				if word == "SET" {
					f.setList = true
				}
			}
		}

		if t.Kind == KindPunct && t.Text == "," && f.depth == 0 {
			if f.selectList || f.setList {
				f.writeRaw(",")
				f.last = &Token{Kind: KindPunct, Text: ","}
				indent := 2
				if f.setList {
					indent = 2
				}
				f.newline(indent)
				continue
			}
		}

		f.writeToken(t)
		if t.Kind.IsSignificant() {
			f.stmtStart = false
		}
	}

	out := strings.TrimRight(f.out.String(), " \t\r\n")
	if out == "" {
		return ""
	}
	if strings.HasSuffix(text, "\n") {
		out += "\n"
	}
	return out
}

// formatter holds the output state while tokens are laid out.
type formatter struct {
	out strings.Builder

	lineStart bool
	indent    int
	depth     int

	selectList bool
	setList    bool
	stmtStart  bool
	between    bool

	last      *Token
	tightNext bool
}

// lastWord returns the text of the previous token, upper-cased for keywords.
func (f *formatter) lastWord() string {
	if f.last == nil {
		return ""
	}
	if f.last.Kind == KindKeyword {
		return strings.ToUpper(f.last.Text)
	}
	return strings.ToUpper(f.last.Text)
}

// write appends text, inserting the current indentation at the start of a line.
func (f *formatter) write(s string) {
	if f.lineStart {
		if f.indent > 0 {
			f.out.WriteString(strings.Repeat(" ", f.indent))
		}
		f.lineStart = false
	}
	f.out.WriteString(s)
}

// writeRaw writes a token's text after applying the current indentation.
func (f *formatter) writeRaw(s string) {
	f.write(s)
}

// space emits exactly one space unless the line just started or the last byte is
// already whitespace. '\r' counts as whitespace too, so a verbatim body ending
// in a carriage return is not padded before its END.
func (f *formatter) space() {
	if f.lineStart {
		return
	}
	s := f.out.String()
	if s == "" {
		return
	}
	last := s[len(s)-1]
	if last != ' ' && last != '\t' && last != '\n' && last != '\r' {
		f.out.WriteByte(' ')
	}
}

// newline ends the current line and sets the indentation of the next one.
func (f *formatter) newline(indent int) {
	if !f.lineStart {
		f.out.WriteByte('\n')
	}
	f.lineStart = true
	f.indent = indent
	f.tightNext = false
}

// blankLine ends the current line and leaves one empty row before the next.
func (f *formatter) blankLine() {
	f.newline(0)
	f.out.WriteByte('\n')
	f.lineStart = true
	f.indent = 0
}

// writeToken writes a significant token with normalized spacing.
func (f *formatter) writeToken(t Token) {
	text := t.Text
	if t.Kind == KindKeyword {
		text = strings.ToUpper(text)
	}
	if f.needSpace(t) {
		f.space()
	}
	f.write(text)

	unary := false
	if t.Kind == KindOperator {
		if t.Text == "::" {
			f.tightNext = true
		}
		if (t.Text == "-" || t.Text == "+") && f.lastIsUnaryContext() {
			unary = true
			f.tightNext = true
		}
	}
	f.last = &Token{Kind: t.Kind, Text: t.Text}
	if !unary && t.Kind != KindOperator {
		f.tightNext = false
	}
}

// needSpace decides whether a token needs a separating space from its
// predecessor.
func (f *formatter) needSpace(t Token) bool {
	if f.lineStart || f.last == nil || f.tightNext {
		return false
	}
	// The lexer treats SQL Server-style [name] identifiers and PostgreSQL array
	// subscripts as one quoted-identifier token. A subscript hugs its base
	// expression; a quoted identifier after a keyword keeps its space.
	if t.Kind == KindQuotedIdent && strings.HasPrefix(t.Text, "[") {
		switch f.last.Kind {
		case KindIdent, KindQuotedIdent, KindNumber, KindString:
			return false
		}
	}
	if t.Kind == KindPunct {
		switch t.Text {
		case ")", "]", ",", ";", ".", "[":
			return false
		case "(":
			switch f.last.Kind {
			case KindIdent, KindQuotedIdent, KindNumber, KindString:
				return false
			case KindPunct:
				return f.last.Text != ")" && f.last.Text != "]"
			default:
				return true
			}
		}
	}
	if f.last.Kind == KindOperator && f.last.Text == "::" {
		return false
	}
	if t.Kind == KindOperator && t.Text == "::" {
		return false
	}
	if f.last.Kind == KindPunct {
		switch f.last.Text {
		case "(", "[", ".":
			return false
		}
	}
	return true
}

// lastIsUnaryContext reports whether a +/- operator is unary.
func (f *formatter) lastIsUnaryContext() bool {
	if f.last == nil {
		return true
	}
	switch f.last.Kind {
	case KindOperator:
		return true
	case KindPunct:
		return f.last.Text == "(" || f.last.Text == "[" || f.last.Text == ","
	case KindKeyword:
		switch strings.ToUpper(f.last.Text) {
		case "SELECT", "WHERE", "AND", "OR", "NOT", "THEN", "ELSE",
			"WHEN", "BY", "SET", "VALUES", "RETURNING", "LIMIT", "OFFSET":
			return true
		}
	}
	return false
}

// comment writes a comment, preserving its text and ending a line comment at
// the end of the line.
func (f *formatter) comment(t Token) {
	text := strings.TrimRight(t.Text, " \t")
	if !f.lineStart {
		f.space()
	}
	f.write(text)
	indent := f.indent
	if strings.HasPrefix(text, "--") || strings.Contains(text, "\n") {
		f.newline(indent)
	}
	f.last = &Token{Kind: KindComment, Text: text}
}

// hasMore reports whether any significant token follows index i.
func hasMore(tokens []Token, i int) bool {
	for ; i < len(tokens); i++ {
		if tokens[i].Kind != KindWhitespace && tokens[i].Kind != KindEOF {
			return true
		}
	}
	return false
}

// nextWord returns the next keyword-like token, skipping only whitespace.
func nextWord(tokens []Token, i int) (string, bool) {
	for ; i < len(tokens); i++ {
		t := tokens[i]
		if t.Kind == KindWhitespace {
			continue
		}
		if t.Kind != KindKeyword {
			return "", false
		}
		return strings.ToUpper(t.Text), true
	}
	return "", false
}

// nextIsJoin reports whether a join modifier is followed by JOIN or OUTER.
func nextIsJoin(tokens []Token, i int) bool {
	w, ok := nextWord(tokens, i)
	return ok && (w == "JOIN" || w == "OUTER")
}

// clauseBreak reports the indentation for a keyword that starts a new clause.
func clauseBreak(word string) (int, bool) {
	switch word {
	case "FROM", "WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET",
		"RETURNING", "WINDOW", "UNION", "INTERSECT", "EXCEPT", "VALUES",
		"SET", "JOIN":
		return 0, true
	case "ON", "USING", "AND", "OR":
		return 2, true
	}
	return 0, false
}

// isJoinModifier reports whether a word is one of the modifiers that stay on
// the same line as JOIN.
func isJoinModifier(word string) bool {
	switch word {
	case "LEFT", "RIGHT", "FULL", "INNER", "OUTER", "CROSS", "NATURAL":
		return true
	}
	return false
}
