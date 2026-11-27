package ui

// confirm_preview.go builds the content of the execution confirmation: the
// values a statement is bound to and the relations a batch touches.
//
// The statement alone does not tell the user what will happen: an UPDATE
// shows its placeholders ("SET email = $1"), not the value that goes into
// them, and a batch of statements does not say which table it writes to.
// Both are read here and shown next to the statement, so the user can read
// what the batch does before it runs.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/sql"
)

// maxConfirmArgLen is how much of a bound value the dialog shows: long
// enough to recognize it, short enough to keep the dialog readable. The full
// value is in the statement the user wrote.
const maxConfirmArgLen = 40

// confirmArgsLine renders the values a statement is bound to, as one line
// under the statement: "$1 = 'new@example.com'  $2 = '1'".
//
// It is empty for a statement without arguments: a plain script has none.
func confirmArgsLine(d dialect.Dialect, stmt execStmt) string {
	if len(stmt.Args) == 0 {
		return ""
	}
	labels := confirmArgLabels(d, len(stmt.Args))
	parts := make([]string, 0, len(stmt.Args))
	for i, arg := range stmt.Args {
		parts = append(parts, labels[i]+" = "+confirmArgValue(arg))
	}
	return strings.Join(parts, "  ")
}

// confirmArgLabels returns a label for every bound argument.
//
// The dialect's placeholder is used when it identifies the argument on its
// own (PostgreSQL numbers them: $1, $2). The other dialects use a bare "?",
// which says nothing about which argument it is, so the position of the
// argument is used instead.
func confirmArgLabels(d dialect.Dialect, n int) []string {
	labels := make([]string, 0, n)
	numbered := false
	if d != nil {
		numbered = placeholder(d, 1) != placeholder(d, 2)
	}
	for i := 1; i <= n; i++ {
		if numbered {
			labels = append(labels, placeholder(d, i))
			continue
		}
		labels = append(labels, strconv.Itoa(i))
	}
	return labels
}

// confirmArgValue renders one bound value.
//
// A string is quoted, so that an empty value is visible, and a nil is NULL:
// NULL and the empty string are not the same thing. The control characters
// are dropped — the value may come from the database — and a long value is
// cut, because the dialog is a summary of what will be written.
func confirmArgValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteConfirmValue(t)
	case []byte:
		return quoteConfirmValue(string(t))
	default:
		return truncateConfirmValue(fmt.Sprintf("%v", v))
	}
}

// quoteConfirmValue renders a text value as a quoted and bounded string.
func quoteConfirmValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	// A quote inside the value is doubled, the way SQL spells it, so the
	// quoted text reads back unambiguously.
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + truncateConfirmValue(s) + "'"
}

// truncateConfirmValue shortens a value to what the dialog can show.
func truncateConfirmValue(s string) string {
	runes := []rune(s)
	if len(runes) <= maxConfirmArgLen {
		return s
	}
	return string(runes[:maxConfirmArgLen]) + "…"
}

// confirmTargets returns the relations the batch touches, in the order they
// appear and without duplicates.
func confirmTargets(stmts []execStmt) []string {
	var out []string
	seen := map[string]bool{}
	for _, stmt := range stmts {
		name := confirmTargetTable(stmt.Text)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// confirmTargetTable returns the relation a statement acts on, when it can be
// read from the statement: the table of an UPDATE, the relation of an INSERT,
// the table of a DELETE, DROP, ALTER or TRUNCATE.
//
// It reads the statement, it does not parse it: a statement whose target
// cannot be found simply has none, and the dialog then says nothing about it
// rather than something wrong.
func confirmTargetTable(stmt string) string {
	toks := make([]sql.Token, 0, 16)
	for _, t := range sql.Lex(stmt, nil) {
		if t.Kind.IsSignificant() {
			toks = append(toks, t)
		}
	}
	if len(toks) == 0 {
		return ""
	}
	switch strings.ToUpper(toks[0].Text) {
	case "UPDATE":
		return relationAt(toks, 1)
	case "DELETE":
		return relationAfterWord(toks, "FROM")
	case "INSERT", "REPLACE":
		if name := relationAfterWord(toks, "INTO"); name != "" {
			return name
		}
		return relationAt(toks, 1)
	case "DROP", "ALTER":
		for _, word := range []string{"TABLE", "VIEW"} {
			if name := relationAfterWord(toks, word); name != "" {
				return name
			}
		}
	case "TRUNCATE":
		if name := relationAfterWord(toks, "TABLE"); name != "" {
			return name
		}
		return relationAt(toks, 1)
	case "CREATE":
		// CREATE INDEX name ON table
		if name := relationAfterWord(toks, "ON"); name != "" {
			return name
		}
		return relationAfterWord(toks, "TABLE")
	}
	return ""
}

// relationAfterWord reads the relation that follows a keyword: the table of
// a DELETE ("DELETE FROM users") or of a DROP ("DROP TABLE users").
func relationAfterWord(toks []sql.Token, word string) string {
	for i, t := range toks {
		if !confirmIsWord(t, word) {
			continue
		}
		if name := relationAt(toks, i+1); name != "" {
			return name
		}
	}
	return ""
}

// relationAt reads the relation named at index i: an identifier, optionally
// qualified with a schema ("main"."users").
func relationAt(toks []sql.Token, i int) string {
	first, ok := confirmIdentAt(toks, i)
	if !ok {
		return ""
	}
	if i+2 < len(toks) && toks[i+1].Kind == sql.KindPunct && toks[i+1].Text == "." {
		if second, ok := confirmIdentAt(toks, i+2); ok {
			return first + "." + second
		}
	}
	return first
}

// confirmIdentAt returns the name of the identifier at index i, without its
// quotes.
func confirmIdentAt(toks []sql.Token, i int) (string, bool) {
	if i < 0 || i >= len(toks) {
		return "", false
	}
	t := toks[i]
	if t.Kind != sql.KindIdent && t.Kind != sql.KindQuotedIdent {
		return "", false
	}
	return unquoteIdent(t), true
}

// confirmIsWord reports whether the token is the given word. A quoted
// identifier is never a word: "from" is a column called from, not the FROM
// keyword.
func confirmIsWord(t sql.Token, word string) bool {
	if t.Kind != sql.KindIdent && t.Kind != sql.KindKeyword {
		return false
	}
	return strings.EqualFold(t.Text, word)
}
