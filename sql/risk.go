package sql

import "strings"

// risk.go classifies a statement by what it can do to the database: read it,
// write rows, change the schema, or do so without a safety net (an UPDATE or
// DELETE without WHERE, a DROP, a TRUNCATE). The UI uses the classification
// to block writes in read-only mode and to ask for confirmation before the
// destructive ones; the classifier itself is a pure function over the
// lexer's tokens, so it never pattern-matches raw SQL text.

// RiskLevel is how dangerous a statement is. The constants are in severity
// order, so a higher level is always at least as dangerous as a lower one.
type RiskLevel int

const (
	// RiskReadOnly reads data or only touches the current transaction; it
	// can never change stored rows or schema.
	RiskReadOnly RiskLevel = iota
	// RiskWrite changes rows or server state (INSERT, UPDATE/DELETE with
	// WHERE, CALL, VACUUM, GRANT, …) but is scoped enough to run without a
	// prompt.
	RiskWrite
	// RiskDDL changes the schema (CREATE, ALTER, RENAME).
	RiskDDL
	// RiskDangerousWrite can destroy or overwrite large amounts of data: an
	// UPDATE or DELETE without WHERE, or any DROP / TRUNCATE.
	RiskDangerousWrite
)

// String returns a short name for the level.
func (r RiskLevel) String() string {
	switch r {
	case RiskReadOnly:
		return "readonly"
	case RiskWrite:
		return "write"
	case RiskDDL:
		return "ddl"
	case RiskDangerousWrite:
		return "dangerous"
	default:
		return "unknown"
	}
}

// Writes reports whether the statement can change data or schema, i.e.
// whether read-only mode must block it.
func (r RiskLevel) Writes() bool { return r > RiskReadOnly }

// NeedsConfirmation reports whether the UI should ask before running the
// statement: DDL and destructive writes yes, a scoped INSERT or UPDATE with
// WHERE no (prompting for every write would get tedious fast).
func (r RiskLevel) NeedsConfirmation() bool { return r >= RiskDDL }

// MaxRisk returns the highest level of the list, or RiskReadOnly for an
// empty one. A script is as dangerous as its most dangerous statement.
func MaxRisk(rs []RiskLevel) RiskLevel {
	max := RiskReadOnly
	for _, r := range rs {
		if r > max {
			max = r
		}
	}
	return max
}

// Risk classifies a single statement. kw is the dialect keyword lookup used
// by the lexer; a nil lookup degrades gracefully (verbs are recognized as
// plain identifiers instead of keywords, which changes nothing here).
//
// The classification is deliberately conservative where it is unsure:
// unknown verbs count as writes, so read-only mode fails closed rather than
// open.
func Risk(stmt string, kw KeywordLookup) RiskLevel {
	toks := Lex(stmt, kw)
	// MySQL/MariaDB executable comments are code, not commentary: the server
	// splices /*! … */ (optionally version-gated, /*!50000 … */) or MariaDB's
	// /*M! … */ into the statement before parsing, so
	// `WITH x AS (SELECT 1) /*!50000 DELETE FROM users */` really runs the
	// DELETE. The spliced text depends on the server version and we do not
	// expand it, so any statement carrying such a comment fails closed as a
	// plain write: read-only mode blocks it without a confirmation prompt —
	// the same trade-off as unknown verbs below. Detection is a prefix match
	// on the comment token, so ordinary -- and /* … */ comments (even ones
	// containing keywords) never trip it.
	for _, t := range toks {
		if executableComment(t) {
			return RiskWrite
		}
	}
	return riskOf(sigTokens(toks))
}

// executableComment reports whether the token is an executable block comment
// — MySQL's /*! … */ or MariaDB's /*M! … */ — whose body the server runs.
func executableComment(t Token) bool {
	if t.Kind != KindComment {
		return false
	}
	return strings.HasPrefix(t.Text, "/*!") || strings.HasPrefix(t.Text, "/*M!")
}

// riskOf classifies a statement from its significant tokens (whitespace,
// comments and EOF already removed).
func riskOf(toks []Token) RiskLevel {
	if len(toks) == 0 {
		return RiskReadOnly
	}
	word, ok := wordOf(toks[0])
	if !ok {
		// The statement starts with punctuation or an operator; its intent
		// is unknown, so treat it as a write to stay on the safe side.
		return RiskWrite
	}
	switch word {
	case "SELECT", "SHOW", "VALUES", "TABLE", "DESC", "DESCRIBE":
		return RiskReadOnly
	case "WITH":
		main, body := cteMainVerb(toks)
		if main < 0 {
			// No main verb yet: only CTE definitions, which return rows —
			// unless a body itself modifies data.
			return body
		}
		// The statement is as dangerous as its main verb or the most
		// dangerous CTE body, whichever is worse.
		if r := riskOf(toks[main:]); r > body {
			return r
		}
		return body
	case "EXPLAIN":
		return explainRisk(toks)
	case "BEGIN", "START":
		// split.go's blockBegin decides whether this BEGIN opens a
		// compound-statement body and matchBlockEnd where its END is, so
		// risk classification and statement splitting can never disagree
		// about what is a block. A block's body really executes: classify
		// by the most dangerous inner statement rather than calling the
		// whole compound statement read-only.
		if blockBegin(toks, 0) {
			return blockBodyRisk(toks)
		}
		// Transaction control (BEGIN;, BEGIN TRANSACTION, START
		// TRANSACTION, …) changes no stored data; blocking it in read-only
		// mode would only break transactional read scripts.
		return RiskReadOnly
	case "COMMIT", "ROLLBACK", "ABORT", "END",
		"SAVEPOINT", "RELEASE":
		// Transaction control changes no stored data; blocking it in
		// read-only mode would only break transactional read scripts.
		return RiskReadOnly
	case "INSERT", "REPLACE", "MERGE":
		return RiskWrite
	case "UPDATE", "DELETE":
		if !hasTopLevelWhere(toks) {
			return RiskDangerousWrite
		}
		return RiskWrite
	case "CREATE", "ALTER", "RENAME":
		return RiskDDL
	case "DROP", "TRUNCATE":
		return RiskDangerousWrite
	default:
		// Unknown or dialect-specific verbs (CALL, DO, COPY, VACUUM, GRANT,
		// REVOKE, SET, USE, …) count as writes: read-only mode must fail
		// closed. They stay unprompted because they are not obviously
		// destructive.
		return RiskWrite
	}
}

// sigTokens drops every token that does not participate in semantic
// analysis (whitespace, comments and the end marker).
func sigTokens(tokens []Token) []Token {
	out := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		if t.Kind.IsSignificant() {
			out = append(out, t)
		}
	}
	return out
}

// isPunct reports whether the token is the given punctuation mark.
func isPunct(t Token, s string) bool {
	return t.Kind == KindPunct && t.Text == s
}

// cteMainVerb returns the index of the main statement verb that follows the
// common-table-expression list of a WITH statement, or -1 when no top-level
// verb is found, plus the risk of the most dangerous CTE body. Only words at
// parenthesis depth zero count as the main verb, so the SELECTs inside the
// CTE bodies are skipped; every parenthesised group directly after AS is a
// CTE body and is classified on its own with riskOf, because PostgreSQL
// really executes data-modifying bodies — `WITH x AS (DELETE FROM users
// RETURNING *) SELECT * FROM x` deletes rows although the main verb is a
// SELECT. Column lists (`WITH x (id) AS …`) and other parentheses are not
// bodies and stay neutral.
func cteMainVerb(toks []Token) (int, RiskLevel) {
	depth := 0
	body := RiskReadOnly
	start := -1 // first token inside the CTE body being scanned, -1 if none
	afterAS := false
	for i := 1; i < len(toks); i++ {
		switch {
		case isPunct(toks[i], "("):
			if depth == 0 && afterAS {
				start = i + 1
			}
			depth++
		case isPunct(toks[i], ")"):
			if depth > 0 {
				depth--
			}
			if depth == 0 && start >= 0 {
				if r := riskOf(toks[start:i]); r > body {
					body = r
				}
				start, afterAS = -1, false
			}
		case depth == 0:
			if w, ok := wordOf(toks[i]); ok {
				switch w {
				case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE",
					"VALUES", "TABLE":
					return i, body
				case "AS":
					afterAS = true
				}
			}
		}
	}
	if start >= 0 { // unterminated body (mid-edit): classify what exists
		if r := riskOf(toks[start:]); r > body {
			body = r
		}
	}
	return -1, body
}

// blockBodyRisk classifies a compound statement opened by the block BEGIN at
// toks[0]: as dangerous as its most dangerous inner statement, because every
// inner statement really runs (MariaDB's `BEGIN NOT ATOMIC DELETE …; END`,
// SQLite's bare `BEGIN INSERT …; END`). Inner statements start at the body
// itself and after each semicolon; riskOf classifies each one and recurses
// for nested blocks. An unterminated body (the user is mid-edit) fails closed
// as a write.
func blockBodyRisk(toks []Token) RiskLevel {
	end := matchBlockEnd(toks, 0)
	if end < 0 {
		return RiskWrite
	}
	risk := RiskReadOnly
	classify := func(from int) {
		if from >= end {
			return
		}
		if r := riskOf(toks[from:end]); r > risk {
			risk = r
		}
	}
	classify(blockStmtStart(toks, 1, end))
	for i := 1; i < end; i++ {
		if isPunct(toks[i], ";") {
			classify(blockStmtStart(toks, i+1, end))
		}
	}
	return risk
}

// blockStmtStart returns the index of the first real statement token at i
// inside a block body, stepping over MariaDB's `NOT ATOMIC` block modifier so
// the modifier words are not mistaken for an unknown (and therefore write)
// verb.
func blockStmtStart(toks []Token, i, end int) int {
	if i+1 < end {
		if w, ok := wordOf(toks[i]); ok && w == "NOT" {
			if w, ok := wordOf(toks[i+1]); ok && w == "ATOMIC" {
				return i + 2
			}
		}
	}
	return i
}

// hasTopLevelWhere reports whether the statement has a WHERE keyword at
// parenthesis depth zero. A WHERE inside a string literal or a subquery
// does not count: `UPDATE t SET x = (SELECT … WHERE …)` still updates every
// row.
func hasTopLevelWhere(toks []Token) bool {
	depth := 0
	for _, t := range toks {
		switch {
		case isPunct(t, "("):
			depth++
		case isPunct(t, ")"):
			if depth > 0 {
				depth--
			}
		case depth == 0:
			if w, ok := wordOf(t); ok && w == "WHERE" {
				return true
			}
		}
	}
	return false
}

// explainRisk classifies an EXPLAIN statement. Plain EXPLAIN only reads the
// plan and never touches data, whatever it explains. EXPLAIN ANALYZE,
// however, really runs the statement, so the inner verb decides the level.
func explainRisk(toks []Token) RiskLevel {
	analyze := false
	i := 1
	for i < len(toks) {
		if w, ok := wordOf(toks[i]); ok && (w == "ANALYZE" || w == "VERBOSE") {
			analyze = analyze || w == "ANALYZE"
			i++
			continue
		}
		if isPunct(toks[i], "(") { // EXPLAIN (ANALYZE, VERBOSE …) stmt
			j, inner := parenEnd(toks, i)
			analyze = analyze || inner
			i = j
			continue
		}
		break
	}
	if !analyze || i >= len(toks) {
		return RiskReadOnly
	}
	return riskOf(toks[i:])
}

// parenEnd returns the index just past the parenthesised group starting at
// i, and reports whether the word ANALYZE appeared inside it.
func parenEnd(toks []Token, i int) (int, bool) {
	depth, analyze := 0, false
	for ; i < len(toks); i++ {
		switch {
		case isPunct(toks[i], "("):
			depth++
		case isPunct(toks[i], ")"):
			depth--
			if depth == 0 {
				return i + 1, analyze
			}
		default:
			if w, ok := wordOf(toks[i]); ok && w == "ANALYZE" {
				analyze = true
			}
		}
	}
	return i, analyze
}
