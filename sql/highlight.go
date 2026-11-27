package sql

// Span is a run of text of one lexical kind, used for syntax highlighting.
type Span struct {
	Text string
	Kind TokenKind
}

// Spans turns a token stream into a sequence of spans grouped by line.
// Tokens that cross lines (strings, block comments) are split across multiple
// lines so that line numbers match the editor's.
func Spans(tokens []Token) [][]Span {
	var lines [][]Span
	cur := []Span{}
	flush := func() {
		lines = append(lines, cur)
		cur = []Span{}
	}
	for _, t := range tokens {
		if t.Kind == KindEOF {
			continue
		}
		// Split the token text on '\n'.
		text := t.Text
		for {
			i := indexRune(text, '\n')
			if i < 0 {
				if text != "" {
					cur = append(cur, Span{Text: text, Kind: t.Kind})
				}
				break
			}
			if i > 0 {
				cur = append(cur, Span{Text: text[:i], Kind: t.Kind})
			} else {
				cur = append(cur, Span{Text: "", Kind: t.Kind})
			}
			flush()
			text = text[i+1:]
		}
	}
	flush()
	return lines
}

func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}
