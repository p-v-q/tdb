package sql

import "strings"

// ContextKind identifies the semantic context for completion.
type ContextKind int

const (
	// CtxNone is used when the position cannot be determined, such as at the
	// start of a statement.
	CtxNone ContextKind = iota
	// CtxDefault is a general expression position (keywords, functions, tables,
	// and columns are all valid).
	CtxDefault
	// CtxTable expects a table name: after FROM/JOIN/INTO/UPDATE/TABLE.
	CtxTable
	// CtxColumn expects a column name: after SELECT/WHERE/ON/SET/GROUP/ORDER/HAVING.
	CtxColumn
	// CtxQualified expects the continuation of a qualified name: after table. / alias.
	CtxQualified
	// CtxSchema expects a schema name.
	CtxSchema
	// CtxTrigger expects a trigger name: after DROP/ALTER TRIGGER.
	CtxTrigger
	// CtxIndex expects an index name: after DROP/ALTER INDEX.
	CtxIndex
	// CtxView expects a view name: after DROP/ALTER [MATERIALIZED] VIEW.
	CtxView
	// CtxFunction expects a stored-function name: after DROP/ALTER FUNCTION.
	CtxFunction
	// CtxProcedure expects a stored-procedure name: after DROP/ALTER PROCEDURE.
	CtxProcedure
	// CtxSequence expects a sequence name: after DROP/ALTER SEQUENCE.
	CtxSequence
)

// String returns the context name (used for debugging and the status bar).
func (k ContextKind) String() string {
	switch k {
	case CtxTable:
		return "table"
	case CtxColumn:
		return "column"
	case CtxQualified:
		return "qualified"
	case CtxSchema:
		return "schema"
	case CtxTrigger:
		return "trigger"
	case CtxIndex:
		return "index"
	case CtxView:
		return "view"
	case CtxFunction:
		return "function"
	case CtxProcedure:
		return "procedure"
	case CtxSequence:
		return "sequence"
	case CtxDefault:
		return "default"
	default:
		return "none"
	}
}

// ScopeEntry is a table reference introduced by a FROM/JOIN clause, along with
// its alias.
type ScopeEntry struct {
	Schema string // may be empty (for example public in public.users)
	Table  string
	Alias  string // may be empty
}

// Match reports whether name can serve as the qualifier for this entry (its
// alias, table name, or schema.table).
func (e ScopeEntry) Match(name string) bool {
	if e.Alias != "" && strings.EqualFold(e.Alias, name) {
		return true
	}
	if strings.EqualFold(e.Table, name) {
		return true
	}
	return e.Schema != "" && strings.EqualFold(e.Schema+"."+e.Table, name)
}

// Qualified returns the reference in schema.table form (without the alias).
func (e ScopeEntry) Qualified() string {
	if e.Schema == "" {
		return e.Table
	}
	return e.Schema + "." + e.Table
}

// Context is the completion context at the cursor.
type Context struct {
	Kind ContextKind

	// Prefix is the word being typed before the cursor (without the dot).
	Prefix string
	// PrefixStart is the start position of Prefix (the range replaced on completion).
	PrefixStart Pos

	// Qualifier is the name before the dot (an alias, table name, or schema name).
	Qualifier string

	// Scope is the table references introduced by FROM/JOIN in the current
	// statement, in order of appearance.
	Scope []ScopeEntry

	// StatementStart is the start of the current statement (after the last semicolon).
	StatementStart Pos
}

// tableKeywords are the keywords after which a table name is expected.
var tableKeywords = map[string]bool{
	"FROM": true, "JOIN": true, "INTO": true, "UPDATE": true, "TABLE": true,
	"DESCRIBE": true, "ANALYZE": true, "TRUNCATE": true,
}

// columnKeywords are the keywords after which a column name is expected.
var columnKeywords = map[string]bool{
	"SELECT": true, "WHERE": true, "ON": true, "SET": true, "HAVING": true,
	"GROUP": true, "ORDER": true, "VALUES": true, "AND": true, "OR": true,
	"USING": true, "PARTITION": true, "DISTINCT": true,
}

// fromLikeKeywords are followed by table references and are used to collect the scope.
var fromLikeKeywords = map[string]bool{
	"FROM": true, "JOIN": true, "INTO": true, "UPDATE": true,
}

// ddlVerbs are the DDL verbs after which the object type selects the name
// expected at the cursor. The verb must be the first significant token of the
// statement; CREATE is deliberately not handled because it names a new object
// rather than referencing an existing one.
var ddlVerbs = map[string]bool{
	"DROP": true, "ALTER": true,
}

// ddlObjectTypes maps the object-type token following a DDL verb onto the
// context that expects such an object name (DROP TRIGGER | → CtxTrigger).
// The token is matched by text rather than by KindKeyword because dialects
// disagree on whether words such as FUNCTION or TRIGGER are keywords.
var ddlObjectTypes = map[string]ContextKind{
	"TABLE":     CtxTable,
	"VIEW":      CtxView,
	"TRIGGER":   CtxTrigger,
	"INDEX":     CtxIndex,
	"FUNCTION":  CtxFunction,
	"PROCEDURE": CtxProcedure,
	"SEQUENCE":  CtxSequence,
}

// ddlColumnClauses are the ALTER TABLE clauses after which COLUMN introduces a
// column-name position (ALTER TABLE t ADD COLUMN | → CtxColumn).
var ddlColumnClauses = map[string]bool{
	"ADD": true, "DROP": true, "ALTER": true, "RENAME": true, "CHANGE": true,
}

// AnalyzeContext analyzes the completion context at the cursor position.
func AnalyzeContext(text string, cursor Pos, kw KeywordLookup) Context {
	return analyze(Lex(text, kw), cursor)
}

// analyze is the core implementation; toks is the full token stream (including
// whitespace and comments).
func analyze(tokens []Token, cursor Pos) Context {
	ctx := Context{Kind: CtxDefault, PrefixStart: cursor}
	toks := significant(tokens)

	// 1. Locate the statement range [start, end): after the previous ';' and
	// before the next ';'.
	start, end := statementRange(toks, cursor)
	ctx.StatementStart = statementPos(toks, start)

	// 2. Extract the prefix word before the cursor.
	idx := firstAtOrAfter(toks, cursor) // index of the token at (or after) the cursor
	ctx.Prefix, ctx.PrefixStart = wordBeforeCursor(toks, idx, cursor)

	// baseIdx skips the prefix token itself and points to the token after the last
	// completion-irrelevant token before the cursor.
	baseIdx := idx
	if ctx.Prefix != "" && baseIdx-1 >= 0 && toks[baseIdx-1].Pos == ctx.PrefixStart {
		baseIdx--
	}

	// 3. Collect the table scope (table names + aliases) within the statement,
	// including the part after the cursor, so that cases like
	// `SELECT u. |FROM users u`, where the select list is written first, can
	// still resolve aliases.
	ctx.Scope = collectScope(toks, start, end)

	// 4. Detect dot qualification (schema. / table. / alias.).
	if q, ok := qualifierBeforeCursor(toks, baseIdx, cursor); ok {
		ctx.Qualifier = q
		if inScope(q, ctx.Scope) {
			ctx.Kind = CtxQualified
		} else {
			ctx.Kind = CtxSchema
		}
		return ctx
	}
	// 5. Determine the expected kind from the previous significant token.
	prev := tokenAt(toks, baseIdx-1)
	if prev == nil {
		return ctx
	}
	// DDL object-name positions (DROP/ALTER TRIGGER |, …) name catalog
	// objects rather than relations, so they are recognized before the generic
	// keyword rules.
	if kind, ok := ddlContext(toks, start, baseIdx); ok {
		ctx.Kind = kind
		return ctx
	}
	up := strings.ToUpper(prev.Text)
	switch {
	case prev.Kind == KindKeyword && tableKeywords[up]:
		ctx.Kind = CtxTable
	case prev.Kind == KindKeyword && columnKeywords[up]:
		ctx.Kind = CtxColumn
	case prev.Kind == KindPunct && prev.Text == ",":
		// After a comma, use the governing keyword (SELECT a, | is still a
		// column context).
		if g := governingKeyword(toks, baseIdx-1); g != nil && strings.EqualFold(g.Text, "SELECT") {
			ctx.Kind = CtxColumn
		}
	case prev.Kind == KindKeyword && up == "DELETE":
		ctx.Kind = CtxTable
	}
	return ctx
}

// ddlContext recognizes the object-name and column-name positions of DDL
// statements among the tokens before the cursor ([start, baseIdx)) and reports
// the expected context. ok is false when the cursor is not in such a position,
// so the caller falls back to the generic keyword rules.
func ddlContext(toks []Token, start, baseIdx int) (ContextKind, bool) {
	if start >= baseIdx {
		return 0, false
	}
	verb := strings.ToUpper(toks[start].Text)
	if !ddlVerbs[verb] {
		return 0, false
	}
	i := baseIdx - 1
	// A trailing IF EXISTS still precedes the object name
	// (DROP TABLE IF EXISTS |, DROP TRIGGER IF EXISTS |).
	if i-1 >= start && strings.EqualFold(toks[i].Text, "EXISTS") &&
		strings.EqualFold(toks[i-1].Text, "IF") {
		i -= 2
		if i < start {
			return 0, false
		}
	}
	prev := strings.ToUpper(toks[i].Text)
	// ALTER TABLE t ADD COLUMN | — a column of the altered table.
	if prev == "COLUMN" {
		if verb == "ALTER" && i-1 >= start && ddlColumnClauses[strings.ToUpper(toks[i-1].Text)] {
			return CtxColumn, true
		}
		return 0, false
	}
	// DROP/ALTER [MATERIALIZED] <type> | — an object name of that type. The
	// type token must directly follow the verb, so `DROP FUNCTION trigger |`
	// (a function named "trigger") does not match.
	kind, ok := ddlObjectTypes[prev]
	if !ok {
		return 0, false
	}
	j := i - 1
	if j >= start && strings.EqualFold(toks[j].Text, "MATERIALIZED") {
		j--
	}
	if j == start {
		return kind, true
	}
	return 0, false
}

// statementRange returns the [start, end) range of the statement within toks.
func statementRange(toks []Token, cursor Pos) (int, int) {
	start := 0
	for i := range toks {
		if cursor.Less(toks[i].Pos) {
			break
		}
		if toks[i].Kind == KindPunct && toks[i].Text == ";" && toks[i].End.LessEqual(cursor) {
			start = i + 1
		}
	}
	end := len(toks)
	for i := start; i < len(toks); i++ {
		if cursor.Less(toks[i].Pos) && toks[i].Kind == KindPunct && toks[i].Text == ";" {
			end = i
			break
		}
	}
	return start, end
}

// governingKeyword searches backward for the keyword governing the current list
// item (skipping identifiers, dots, operators, and so on).
func governingKeyword(toks []Token, idx int) *Token {
	for i := idx - 1; i >= 0; i-- {
		t := toks[i]
		switch t.Kind {
		case KindKeyword:
			return &t
		case KindIdent, KindQuotedIdent, KindNumber, KindString, KindParam, KindOperator:
			continue
		case KindPunct:
			if t.Text == "." || t.Text == "," {
				continue
			}
			return &t
		}
	}
	return nil
}

// tokenAt returns a pointer to the token at index idx, or nil if out of range.
func tokenAt(toks []Token, idx int) *Token {
	if idx < 0 || idx >= len(toks) {
		return nil
	}
	t := toks[idx]
	return &t
}

func inScope(name string, scope []ScopeEntry) bool {
	for _, e := range scope {
		if e.Match(name) {
			return true
		}
	}
	return false
}

// significant returns all significant tokens (excluding whitespace and comments).
func significant(tokens []Token) []Token {
	out := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		if t.Kind.IsSignificant() {
			out = append(out, t)
		}
	}
	return out
}

// firstAtOrAfter returns the index of the first significant token whose start
// position is >= cursor.
func firstAtOrAfter(toks []Token, cursor Pos) int {
	for i, t := range toks {
		if !t.Pos.Less(cursor) {
			return i
		}
	}
	return len(toks)
}

func statementPos(toks []Token, start int) Pos {
	if start < len(toks) {
		return toks[start].Pos
	}
	if len(toks) > 0 {
		return toks[len(toks)-1].End
	}
	return Pos{0, 0}
}

// wordBeforeCursor returns the word being typed before the cursor and its start
// position. In particular, if the immediately preceding keyword has been typed in
// full (the cursor is exactly at the end of the keyword), the prefix is empty, so
// that completing FROM immediately offers the table list rather than the keyword
// list.
func wordBeforeCursor(toks []Token, idx int, cursor Pos) (string, Pos) {
	for i := idx - 1; i >= 0; i-- {
		t := toks[i]
		if cursor.Less(t.Pos) {
			continue
		}
		if t.End.Less(cursor) {
			break // separated from the cursor by whitespace/punctuation
		}
		if t.Kind != KindIdent && t.Kind != KindQuotedIdent && t.Kind != KindKeyword {
			break
		}
		if t.Kind == KindKeyword && t.End == cursor {
			return "", cursor // keyword just typed in full
		}
		// The cursor may be in the middle of an identifier; take only the part
		// before it. Positions are (row, col) pairs and a quoted identifier may
		// span lines (the lexer lets "…", `…` and […] contain newlines), so
		// column arithmetic must never cross a row: when the token starts on an
		// earlier row the caret sits after a newline inside the token, and
		// cursor.Col - t.Pos.Col can be negative. There is no single-line
		// prefix to extract then; report an empty word at the cursor. The
		// offset is clamped below as well, so a caret before the token start
		// can never slice out of range.
		if cursor.Row != t.Pos.Row {
			return "", cursor
		}
		n := cursor.Col - t.Pos.Col
		runes := []rune(t.Text)
		if n < 0 {
			n = 0
		}
		if n > len(runes) {
			n = len(runes)
		}
		return string(runes[:n]), t.Pos
	}
	return "", cursor
}

// qualifierBeforeCursor reports whether the cursor is right after `name.`.
func qualifierBeforeCursor(toks []Token, idx int, cursor Pos) (string, bool) {
	// The '.' must immediately precede the cursor.
	i := idx - 1
	if i < 0 {
		return "", false
	}
	dot := toks[i]
	if dot.Kind != KindPunct || dot.Text != "." || cursor.Less(dot.End) {
		return "", false
	}
	// An identifier must precede the dot.
	j := i - 1
	if j < 0 {
		return "", false
	}
	name := toks[j]
	if name.Kind != KindIdent && name.Kind != KindQuotedIdent && name.Kind != KindKeyword {
		return "", false
	}
	return name.Text, true
}

// collectScope collects the tables and aliases introduced by FROM/JOIN in the
// statement range [start, end).
func collectScope(toks []Token, start, end int) []ScopeEntry {
	var scope []ScopeEntry
	seen := map[string]bool{}
	add := func(e ScopeEntry) {
		if e.Table == "" {
			return
		}
		key := strings.ToLower(e.Qualified() + "\x00" + e.Alias)
		if seen[key] {
			return
		}
		seen[key] = true
		scope = append(scope, e)
	}

	for i := start; i < end; i++ {
		t := toks[i]
		if t.Kind != KindKeyword || !fromLikeKeywords[strings.ToUpper(t.Text)] {
			continue
		}
		j := i + 1
		if strings.EqualFold(t.Text, "INTO") && j < end && strings.EqualFold(toks[j].Text, "SELECT") {
			continue // INSERT INTO SELECT ... introduces no table reference
		}
		// Read the table list in a FROM clause: t1 [AS a1|a1], t2 [AS a2|a2], ...
		for j < end {
			ref, next, ok := readTableRef(toks, j, end)
			if !ok {
				break
			}
			j = next
			alias := ""
			if j < end-1 && strings.EqualFold(toks[j].Text, "AS") &&
				(toks[j+1].Kind == KindIdent || toks[j+1].Kind == KindQuotedIdent) {
				alias = toks[j+1].Text
				j += 2
			} else if j < end && toks[j].Kind == KindIdent && !isReservedForAlias(toks[j].Text) {
				alias = toks[j].Text
				j++
			}
			add(ScopeEntry{Schema: ref.schema, Table: ref.table, Alias: alias})

			if j < end && toks[j].Kind == KindPunct && toks[j].Text == "," {
				j++ // next comma-separated table
				continue
			}
			break
		}
	}
	return scope
}

// isReservedForAlias reports whether an identifier might actually be the keyword
// of the next clause.
func isReservedForAlias(text string) bool {
	switch strings.ToUpper(text) {
	case "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "FULL", "CROSS", "NATURAL",
		"WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "ON", "USING",
		"UNION", "INTERSECT", "EXCEPT", "SET", "SELECT", "AND", "OR", "WINDOW",
		"RETURNING", "VALUES", "FOR", "STRAIGHT_JOIN":
		return true
	}
	return false
}

type tableRef struct {
	schema string // may be empty
	table  string // unqualified table name
}

// readTableRef reads a table reference starting at toks[i] (supporting a.b chains
// of qualifiers).
func readTableRef(toks []Token, i, end int) (tableRef, int, bool) {
	if i >= end {
		return tableRef{}, i, false
	}
	t := toks[i]
	if t.Kind != KindIdent && t.Kind != KindQuotedIdent {
		return tableRef{}, i, false
	}
	parts := []string{t.Text}
	j := i + 1
	for j+1 < end && toks[j].Kind == KindPunct && toks[j].Text == "." &&
		(toks[j+1].Kind == KindIdent || toks[j+1].Kind == KindQuotedIdent) {
		parts = append(parts, toks[j+1].Text)
		j += 2
	}
	ref := tableRef{table: parts[len(parts)-1]}
	if len(parts) > 1 {
		ref.schema = strings.Join(parts[:len(parts)-1], ".")
	}
	return ref, j, true
}
