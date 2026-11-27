package ui

// grid_edit.go implements editing a cell of the result grid: it decides
// whether a result set can be edited at all, keeps the pending changes in
// memory, and turns them into parameterized UPDATE statements.
//
// The rules are deliberately strict, because the generated UPDATE has to
// name exactly one table and address exactly one row:
//
//   - the statement must be a single SELECT over exactly one table (no join,
//     no second table in the FROM clause, no sub-query, no set operation, no
//     common table expression), and its select list must consist of plain
//     column references, so that every cell can be traced back to one column;
//   - the table must exist in the live catalog and have a primary key of
//     which every column is part of the result, because the key is what
//     addresses a single row;
//   - the client must not be in read-only mode.
//
// Anything else keeps the grid read-only, and the status bar says why.
//
// Nothing reaches the database before the user has seen the exact
// statements: the pending changes live in memory, the confirmation dialog
// shows the SQL, and only then does it run.

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
)

// nullMarker is how a NULL value is typed, the way COPY spells it. An empty
// cell stays the empty string: NULL and "" are not the same thing.
const nullMarker = `\N`

// gridSource describes the table a result set came from. It is set only for a
// result that can be edited.
type gridSource struct {
	// table is the relation the rows were read from.
	table *catalog.Table
	// pk holds the primary-key columns of the table, in catalog order.
	pk []*catalog.Column
	// keyIdx holds the result column index of every primary-key column, in
	// the order of pk. It is what addresses a single row.
	keyIdx []int
	// cols maps a result column index to the catalog column it mirrors. A
	// result column with no counterpart is not in the map and cannot be
	// edited: there would be nothing to write to.
	cols map[int]*catalog.Column
	// stmt is the statement that produced the result. It runs again after
	// the changes were applied, so the grid shows what the database holds.
	stmt string
	// conn identifies the connection the rows were read from. A change built
	// from this table belongs to that connection: applying it through
	// another one would write to a different database.
	conn connRef
}

// pendingEdit is one edited cell that has not reached the database yet.
type pendingEdit struct {
	// row is the index of the row in the original result, not in the current
	// view: sorting and filtering change the view, not the data.
	row int
	// col is the index of the column in the result.
	col int
	// value is the new value; nil is SQL NULL.
	value *string
}

// cellEdit is the cell that is being edited right now. The grid owns the
// keyboard until the edit ends.
type cellEdit struct {
	row  int    // the row of the original result
	col  int    // the result column
	text string // what has been typed so far
	at   int    // the caret inside text, in runes
	// orig is the value a commit writes back when text was not typed over:
	// the bytes the cell holds (or the pending value of an earlier edit).
	// text is only the cleaned copy of them — foldBreaks, exactly what the
	// grid drew — so that an edit which changes nothing can put the stored
	// bytes back instead of writing the cleaning into the database.
	orig string
}

// gridApply is a batch of pending changes on its way to the database.
type gridApply struct {
	// sess is the tab the changes belong to.
	sess *session
	// count is how many changes the batch carries.
	count int
	// conn is the connection the batch was built for. The statements run on
	// it (and never on whatever is connected when the user confirms), so a
	// switch while the dialog is open cannot write to another database.
	conn connRef
	// unchanged holds, per statement, whether the batch writes the value the
	// grid already holds: a driver that counts only changed rows reports 0
	// affected for such a matched row.
	unchanged []bool
}

// affectedOK reports whether an UPDATE of the batch did what the grid
// promised. Every statement addresses one row by its primary key, so it has
// to change exactly one row — except a statement that writes the value the
// grid already holds: MySQL counts only changed rows, so 0 is the correct
// outcome for a matched row that kept its value. More than one row is always
// a failure: the key did not address a single row.
func (a *gridApply) affectedOK(index int, affected int64) bool {
	if affected == 1 {
		return true
	}
	if affected == 0 && index >= 0 && index < len(a.unchanged) && a.unchanged[index] {
		return true
	}
	return false
}

// editBlockReason says why a result cannot be edited.
type editBlockReason int

const (
	// editAllowed means the result can be edited.
	editAllowed editBlockReason = iota
	// editBlockedQuery means the result is not a plain single-table select,
	// or its table is unknown: in every one of those cases a row cannot be
	// addressed safely.
	editBlockedQuery
	// editBlockedNoKey means the table has no primary key.
	editBlockedNoKey
	// editBlockedNoKeyColumn means the primary key exists but is not part of
	// the result, so the rows cannot be addressed.
	editBlockedNoKeyColumn
	// editBlockedReadOnly means the client is in read-only mode.
	editBlockedReadOnly
	// editBlockedAmbiguousTable means the table name is not qualified and
	// several schemas hold a table of that name: the server may have read
	// another one than the catalog lists.
	editBlockedAmbiguousTable
	// editBlockedAmbiguousColumn means a result column matches several catalog
	// columns under the dialect's case-folding rules, so the column to update
	// cannot be chosen safely.
	editBlockedAmbiguousColumn
)

// key returns the message key that names the reason.
func (r editBlockReason) key() i18n.Key {
	switch r {
	case editBlockedNoKey:
		return i18n.KeyResultNotEditableReasonNoKey
	case editBlockedNoKeyColumn:
		return i18n.KeyResultNotEditableReasonKeyNotIn
	case editBlockedReadOnly:
		return i18n.KeyResultNotEditableReasonReadOnly
	case editBlockedAmbiguousTable:
		return i18n.KeyResultNotEditableReasonAmbiguousTable
	case editBlockedAmbiguousColumn:
		return i18n.KeyResultNotEditableReasonAmbiguousColumn
	}
	return i18n.KeyResultNotEditableReasonQuery
}

// ---- The source of a result ----

// editableSource works out whether a result set can be edited and which table
// it came from.
//
// The statement has to be a single SELECT over exactly one table, and the
// table has to have a primary key of which every column is part of the
// result: the key is what addresses a single row, and without it the UPDATE
// would hit more than one row.
//
// The second result reports whether the result is editable at all;
// resolveSource says why it is not.
func editableSource(stmt string, cat *catalog.Catalog, cols []driver.ResultColumn, origin resultOrigin) (*gridSource, bool) {
	src, reason := resolveSource(stmt, cat, cols, origin)
	if reason != editAllowed {
		return nil, false
	}
	return src, true
}

// resolveSource is editableSource with the reason for a refusal.
func resolveSource(stmt string, cat *catalog.Catalog, cols []driver.ResultColumn, origin resultOrigin) (*gridSource, editBlockReason) {
	if cat == nil || len(cols) == 0 {
		return nil, editBlockedQuery
	}
	ref, ok := singleTableSelect(stmt)
	if !ok {
		return nil, editBlockedQuery
	}
	table, reason := resolveTable(cat, ref, origin.schema)
	if reason != editAllowed {
		return nil, reason
	}
	pk := primaryKeyColumns(table.Columns)
	if len(pk) == 0 {
		return nil, editBlockedNoKey
	}

	// Map every result column to the catalog column it mirrors, and find the
	// result column of every key column. PostgreSQL matches identifier case
	// exactly; the other built-in dialects fold case and must reject a folded
	// name that identifies more than one column.
	foldCase := !dialect.IdentifiersCaseSensitive(sourceDialect(cat, origin))
	mapped := make(map[int]*catalog.Column, len(cols))
	keyIdx := make([]int, len(pk))
	for i := range keyIdx {
		keyIdx[i] = -1
	}
	for i, c := range cols {
		col, ambiguous := findColumn(table.Columns, c.Name, foldCase)
		if ambiguous {
			return nil, editBlockedAmbiguousColumn
		}
		if col == nil {
			continue
		}
		mapped[i] = col
		if !col.PK {
			continue
		}
		for k, p := range pk {
			if p == col {
				keyIdx[k] = i
			}
		}
	}
	for _, idx := range keyIdx {
		if idx < 0 {
			// The key is not part of the result, so a row cannot be
			// addressed: the update would have to guess. The query itself
			// is fine, so it gets its own reason.
			return nil, editBlockedNoKeyColumn
		}
	}
	return &gridSource{table: table, pk: pk, keyIdx: keyIdx, cols: mapped, stmt: stmt, conn: origin.conn}, editAllowed
}

// resolveTable finds the relation a FROM clause names.
//
// The catalog lists every schema of the database, but an unqualified name
// does not say which of them the server picked: PostgreSQL resolves it
// through the search_path, MySQL through the default database. The first
// table of that name in the catalog is NOT the table the server read — it is
// whichever schema happens to come first in the snapshot — and an UPDATE
// built from it would silently write to a table the query never read. That
// is exactly what happens in a multi-tenant database, where every tenant
// has its own schema and all of them have the same table names.
//
// So the name is resolved the way the server would resolve it:
//
//   - a qualified name names one schema, and only that schema is looked at;
//   - an unqualified name resolves in the connection's own default schema
//     (the first entry of the search_path) when the catalog has the table
//     there;
//   - otherwise the name has to be unique in the catalog. When it is not,
//     the result is refused: the user has to write schema.table, and the
//     statement then says plainly which table is written to.
func resolveTable(cat *catalog.Catalog, ref tableRef, defaultSchema string) (*catalog.Table, editBlockReason) {
	if cat == nil {
		return nil, editBlockedQuery
	}
	if ref.schema != "" {
		t := cat.FindTableInSchema(ref.schema, ref.name)
		if t == nil {
			return nil, editBlockedQuery
		}
		return t, editAllowed
	}
	if defaultSchema != "" {
		// The connection works in that schema, so the server resolves the
		// name there first.
		if t := cat.FindTableInSchema(defaultSchema, ref.name); t != nil {
			return t, editAllowed
		}
	}
	switch matches := cat.TablesNamed(ref.name); len(matches) {
	case 0:
		return nil, editBlockedQuery
	case 1:
		return matches[0], editAllowed
	default:
		return nil, editBlockedAmbiguousTable
	}
}

// resultSource is the source of a result, or nil when it cannot be edited.
func resultSource(stmt string, cat *catalog.Catalog, res *driver.Result, origin resultOrigin) *gridSource {
	if res == nil || len(res.Columns) == 0 {
		return nil
	}
	src, _ := editableSource(stmt, cat, res.Columns, origin)
	return src
}

// sourceDialect returns the dialect that produced the result. The connection is
// authoritative (MariaDB, for example, uses the MySQL dialect); the catalog name
// is a fallback for source analysis that has no live connection, such as tests.
func sourceDialect(cat *catalog.Catalog, origin resultOrigin) dialect.Dialect {
	if origin.conn.conn != nil {
		if d := origin.conn.conn.Dialect(); d != nil {
			return d
		}
	}
	if cat != nil {
		if d, err := dialect.Get(cat.Dialect); err == nil {
			return d
		}
	}
	return nil
}

// findColumn returns the catalog column matching name. When foldCase is true,
// matching ignores case; more than one match is ambiguous and must never be
// resolved by catalog order. The second result reports that ambiguity.
func findColumn(cols []*catalog.Column, name string, foldCase bool) (*catalog.Column, bool) {
	var found *catalog.Column
	for _, c := range cols {
		if c == nil {
			continue
		}
		if c.Name != name && !(foldCase && strings.EqualFold(c.Name, name)) {
			continue
		}
		if found != nil {
			return nil, true
		}
		found = c
	}
	return found, false
}

// ---- Reading the statement ----

// tableRef is the relation a FROM clause names: the schema and the name.
// The schema is empty when the name was not qualified — which is the case
// that needs the care described in resolveTable.
type tableRef struct {
	schema string
	name   string
}

// singleTableSelect returns the table a single-table SELECT reads from.
//
// The rules are strict on purpose: the UPDATE a cell edit generates has to
// name exactly one relation and address exactly one row, so everything that
// hides the relation — a join, a second table in the FROM clause, a
// sub-query, a set operation, a common table expression — disqualifies the
// statement. The select list must consist of plain column references (or a
// star), so that every cell of the result can be traced back to exactly one
// column of the table.
func singleTableSelect(stmt string) (tableRef, bool) {
	toks := selectTokens(stmt)
	// A trailing semicolon belongs to every statement the editor produces.
	if n := len(toks); n > 0 && toks[n-1].Kind == sql.KindPunct && toks[n-1].Text == ";" {
		toks = toks[:n-1]
	}
	// More than one statement: the grid shows one of them, but the rows
	// could come from any of them.
	for _, t := range toks {
		if t.Kind == sql.KindPunct && t.Text == ";" {
			return tableRef{}, false
		}
	}
	if len(toks) == 0 || !isWord(toks[0], "SELECT") {
		return tableRef{}, false
	}

	from := -1
	for i, t := range toks {
		switch {
		case isWord(t, "FROM"):
			if from >= 0 {
				return tableRef{}, false // a second FROM: a set operation or a sub-query
			}
			from = i
		case isWord(t, "JOIN"), isWord(t, "UNION"), isWord(t, "INTERSECT"), isWord(t, "EXCEPT"):
			return tableRef{}, false
		}
	}
	if from < 0 {
		return tableRef{}, false
	}
	if !plainSelectList(toks[1:from]) {
		return tableRef{}, false
	}
	return fromTable(toks[from+1:])
}

// selectTokens returns the significant tokens of a statement: the lexer drops
// the whitespace and the comments.
func selectTokens(stmt string) []sql.Token {
	var out []sql.Token
	for _, t := range sql.Lex(stmt, nil) {
		if t.Kind.IsSignificant() {
			out = append(out, t)
		}
	}
	return out
}

// isWord reports whether a token is the given word. A quoted identifier is
// never a word: "from" is a column called from, not the FROM keyword.
func isWord(t sql.Token, word string) bool {
	if t.Kind != sql.KindIdent && t.Kind != sql.KindKeyword {
		return false
	}
	return strings.EqualFold(t.Text, word)
}

// plainSelectList reports whether the select list consists only of plain
// column references (col, t.col) or a star. An expression or an alias would
// make the value in a cell something other than the column it is named
// after, and the mapping from a result column to a table column would be a
// guess.
func plainSelectList(toks []sql.Token) bool {
	item := make([]sql.Token, 0, 4)
	flush := func() bool {
		ok := plainColumnRef(item)
		item = item[:0]
		return ok
	}
	for _, t := range toks {
		if t.Kind == sql.KindPunct && t.Text == "," {
			if !flush() {
				return false
			}
			continue
		}
		item = append(item, t)
	}
	return flush()
}

// plainColumnRef reports whether the tokens are a column reference: a star, a
// column name, or a qualifier and a column name.
func plainColumnRef(toks []sql.Token) bool {
	name := func(t sql.Token) bool {
		return t.Kind == sql.KindIdent || t.Kind == sql.KindQuotedIdent
	}
	switch len(toks) {
	case 1:
		return name(toks[0]) || isStar(toks[0])
	case 3:
		return name(toks[0]) &&
			toks[1].Kind == sql.KindPunct && toks[1].Text == "." &&
			(name(toks[2]) || isStar(toks[2]))
	}
	return false
}

// isStar reports whether a token is the multiplication star.
func isStar(t sql.Token) bool {
	return t.Kind == sql.KindOperator && t.Text == "*"
}

// fromTable returns the table the FROM clause names, when it names exactly
// one table.
func fromTable(toks []sql.Token) (tableRef, bool) {
	first, ok := identAt(toks, 0)
	if !ok {
		return tableRef{}, false
	}
	i := 1
	ref := tableRef{name: first}
	if i < len(toks) && toks[i].Kind == sql.KindPunct && toks[i].Text == "." {
		second, ok := identAt(toks, i+1)
		if !ok {
			return tableRef{}, false
		}
		i += 2
		ref = tableRef{schema: first, name: second}
	}
	// An optional alias follows the table name. A clause keyword does not:
	// it ends the table reference, so "FROM users ORDER BY id" must not read
	// ORDER as an alias and then trip over BY.
	if i < len(toks) {
		switch {
		case isWord(toks[i], "AS"):
			if _, ok := identAt(toks, i+1); !ok {
				return tableRef{}, false
			}
			i += 2
		case (toks[i].Kind == sql.KindIdent || toks[i].Kind == sql.KindQuotedIdent) && !isClauseWord(toks[i]):
			i++
		}
	}
	// Whatever follows must start a clause, not name a second table.
	if i < len(toks) {
		t := toks[i]
		if t.Kind == sql.KindPunct && (t.Text == "," || t.Text == "(") {
			return tableRef{}, false
		}
		if !isClauseWord(t) {
			return tableRef{}, false
		}
	}
	return ref, true
}

// identAt returns the name of the identifier at index i.
func identAt(toks []sql.Token, i int) (string, bool) {
	if i < 0 || i >= len(toks) {
		return "", false
	}
	t := toks[i]
	if t.Kind != sql.KindIdent && t.Kind != sql.KindQuotedIdent {
		return "", false
	}
	return unquoteIdent(t), true
}

// isClauseWord reports whether a token ends the table reference and starts a
// clause of the statement.
func isClauseWord(t sql.Token) bool {
	if t.Kind != sql.KindIdent && t.Kind != sql.KindKeyword {
		return false
	}
	switch strings.ToUpper(t.Text) {
	case "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "WINDOW",
		"FETCH", "FOR", "UNION", "INTERSECT", "EXCEPT", "JOIN", "ON", "USING",
		"RETURNING", "INTO":
		return true
	}
	return false
}

// unquoteIdent returns the name of an identifier token without its quotes.
func unquoteIdent(t sql.Token) string {
	if t.Kind != sql.KindQuotedIdent || len(t.Text) < 2 {
		return t.Text
	}
	switch t.Text[0] {
	case '"', '`':
		return t.Text[1 : len(t.Text)-1]
	case '[':
		return strings.TrimSuffix(t.Text[1:], "]")
	}
	return t.Text
}

// ---- The pending changes ----

// pendingCount returns the number of pending changes.
func (r resultState) pendingCount() int { return len(r.pending) }

// pendingGridChanges is how many edited cells of the session have not reached
// the database yet: the active result set and the inactive ones (switching
// result sets keeps their pending changes).
func (s *session) pendingGridChanges() int {
	n := s.result.pendingCount()
	for i := range s.resultSets {
		if i == s.resultIdx {
			continue // the active value is s.result, kept in step by selectResultSet
		}
		n += s.resultSets[i].pendingCount()
	}
	return n
}

// pendingGridChangesAll is how many pending grid changes the whole workspace
// holds: every tab, so quitting and switching the connection can tell what
// would be lost.
func (m Model) pendingGridChangesAll() int {
	n := 0
	for _, s := range m.tabs {
		n += s.pendingGridChanges()
	}
	return n
}

// pendingAt returns the pending change of a cell, if there is one.
func (r resultState) pendingAt(row, col int) (pendingEdit, bool) {
	for _, p := range r.pending {
		if p.row == row && p.col == col {
			return p, true
		}
	}
	return pendingEdit{}, false
}

// rowPending reports whether a row holds at least one pending change.
func (r resultState) rowPending(row int) bool {
	for _, p := range r.pending {
		if p.row == row {
			return true
		}
	}
	return false
}

// setPending stores a change, replacing an earlier one for the same cell.
// The slice is rebuilt instead of edited in place, so that the copies of the
// result state (one per result set) never share a change.
func (r *resultState) setPending(row, col int, value *string) {
	next := make([]pendingEdit, 0, len(r.pending)+1)
	replaced := false
	for _, p := range r.pending {
		if p.row == row && p.col == col {
			p.value = value
			replaced = true
		}
		next = append(next, p)
	}
	if !replaced {
		next = append(next, pendingEdit{row: row, col: col, value: value})
	}
	r.pending = next
}

// ---- Editing a cell ----

// editBlockReason says why the current result cannot be edited.
func (m Model) editBlockReason() editBlockReason {
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return editBlockedQuery
	}
	if r.src == nil {
		// The source could not be resolved: find out why, so the status bar
		// can say it instead of failing later.
		return m.sourceBlockReason(r)
	}
	if m.settings.ReadOnly {
		return editBlockedReadOnly
	}
	return editAllowed
}

// sourceBlockReason re-runs the source analysis to find out what is missing
// from a result that cannot be edited.
func (m Model) sourceBlockReason(r resultState) editBlockReason {
	if m.cat == nil {
		return editBlockedQuery
	}
	_, reason := resolveSource(r.stmt, m.cat, r.res.Columns, r.origin)
	if reason == editAllowed {
		// The source is fine, so something else keeps the grid read-only.
		return editBlockedQuery
	}
	return reason
}

// startCellEdit begins editing the cell under the caret. It reports why the
// cell cannot be edited when the result does not allow it.
func (m *Model) startCellEdit() tea.Cmd {
	r := &m.session.result
	if reason := m.editBlockReason(); reason != editAllowed {
		return m.setMessage(m.t(i18n.KeyResultNotEditable, m.t(reason.key())))
	}
	view := r.viewRows()
	if r.cursorRow < 0 || r.cursorRow >= len(view) {
		return nil
	}
	row := view[r.cursorRow]
	col := r.cursorCol
	column, ok := r.src.cols[col]
	if !ok || column == nil {
		// The column has no counterpart in the table, so there is nothing to
		// write to.
		return m.setMessage(m.t(i18n.KeyResultNotEditable,
			m.t(i18n.KeyResultNotEditableReasonQuery)))
	}
	if column.PK {
		// The primary key is what addresses the row. Rewriting it would
		// move the change to another row — or to none — and a database
		// that coerces the value silently (MySQL turns "42abc" into 42)
		// can hit a row the user never looked at. The key is not editable.
		return m.setMessage(m.t(i18n.KeyResultEditKeyColumn))
	}
	if _, ok := m.rowKeyValues(row); !ok {
		// The row cannot be addressed: a key value is missing.
		return m.setMessage(m.t(i18n.KeyResultNotEditable,
			m.t(i18n.KeyResultNotEditableReasonQuery)))
	}
	text := ""
	if p, ok := r.pendingAt(row, col); ok {
		// A pending change is what the user typed last: the next edit
		// continues from it instead of throwing it away. A cell that is
		// pending as NULL shows the marker, so the NULL stays visible.
		if p.value == nil {
			text = nullMarker
		} else {
			text = *p.value
		}
	} else if cell := cellAt(r.res.Rows[row], col); cell != nil {
		text = *cell
	} else {
		// The cell holds NULL. The editor starts with the NULL marker, so
		// the user sees that the cell is empty in the database sense: an
		// empty text would silently turn the NULL into the empty string
		// when the edit is committed.
		text = nullMarker
	}
	// The editor seeds with the same cleaned copy the grid draws (see
	// resultRow): a stored value may hold line breaks and escape sequences,
	// the row the editor takes over is a single line, and the terminal must
	// not execute any of it while the edit is open. The user edits the text
	// they see; the bytes they did not touch stay in orig, so committing an
	// edit that changed nothing cannot rewrite the value in the database.
	orig := text
	text = foldBreaks(text)
	r.editing = &cellEdit{row: row, col: col, text: text, at: len([]rune(text)), orig: orig}
	m.layout.focus = paneResults
	return nil
}

// commitCellEdit stores the edited value as a pending change. Nothing
// reaches the database yet: the change waits in memory until the user
// applies it.
//
// A commit that did not type over the editor's text writes orig — the bytes
// the cell holds — because text was only their cleaned copy: opening a cell
// to look at it must never strip the control characters out of the stored
// value. What the user typed is written as typed, so the grid, the editor
// and the database all show the same text afterwards.
func (m *Model) commitCellEdit() {
	r := &m.session.result
	e := r.editing
	if e == nil {
		return
	}
	r.editing = nil
	value := e.text
	if value == foldBreaks(e.orig) {
		value = e.orig
	}
	r.setPending(e.row, e.col, editValue(value))
}

// editValue turns the typed text into the value of the cell. The literal \N
// means SQL NULL, the way COPY spells it; an empty text stays the empty
// string, because NULL and "" are not the same thing.
func editValue(text string) *string {
	if text == nullMarker {
		return nil
	}
	v := text
	return &v
}

// rowKeyValues returns the primary-key values of a row, in the order of the
// key columns. A row whose key is missing or NULL cannot be addressed.
func (m Model) rowKeyValues(row int) ([]string, bool) {
	r := m.session.result
	if r.src == nil || r.res == nil || row < 0 || row >= len(r.res.Rows) {
		return nil, false
	}
	vals := make([]string, 0, len(r.src.pk))
	for _, idx := range r.src.keyIdx {
		cell := cellAt(r.res.Rows[row], idx)
		if cell == nil {
			return nil, false
		}
		vals = append(vals, *cell)
	}
	return vals, true
}

// ---- Applying the changes ----

// applyPendingChanges turns the pending changes into UPDATE statements and
// shows them in the confirmation dialog. Nothing is written before the user
// confirms.
func (m *Model) applyPendingChanges() tea.Cmd {
	r := &m.session.result
	if len(r.pending) == 0 {
		return nil
	}
	if m.settings.ReadOnly {
		return m.setMessage(m.readOnlyBlockedText(len(r.pending)))
	}
	if r.src != nil && !r.src.conn.same(m.connRef()) {
		// The rows were read through another connection, so the table the
		// changes were built from belongs to another database. Applying
		// them now would write to a table the user never looked at.
		return m.setMessage(m.t(i18n.KeyResultEditWrongConnection))
	}
	stmts, unchanged, err := m.pendingBatch()
	if err != nil {
		return m.setMessage(m.t(i18n.KeyResultEditFailed) + ": " + errText(err))
	}
	if len(stmts) == 0 {
		return m.setMessage(m.t(i18n.KeyResultEditFailed))
	}

	// The batch is bound to the dialog until the user answers it: the outcome
	// can be told apart from an ordinary query, and a background result
	// arriving meanwhile cannot take the transaction and row-count checks
	// away (see openApplyConfirm).
	apply := &gridApply{sess: m.session, count: len(r.pending), unchanged: unchanged}
	risks := make([]sql.RiskLevel, len(stmts))
	for i := range risks {
		// The statements are ordinary scoped UPDATEs, but the point of the
		// dialog is that the user reads them before they run.
		risks[i] = sql.RiskWrite
	}
	m.openApplyConfirm(stmts[0].Text, stmts, risks, apply)
	return nil
}

// discardPendingChanges drops the pending changes without touching the
// database.
func (m *Model) discardPendingChanges() tea.Cmd {
	r := &m.session.result
	if len(r.pending) == 0 && r.editing == nil {
		return nil
	}
	r.pending = nil
	r.editing = nil
	return m.setMessage(m.t(i18n.KeyResultChangesDiscarded))
}

// clearGridEdits drops the pending changes, the open cell editor and the
// source of every tab's result.
//
// A pending change belongs to the connection that produced the rows: the
// table it addresses lives in that database. Once the connection is switched,
// applying the change would write to the same table name in the *new*
// database, which is a cross-database write the user never asked for. The
// changes are therefore dropped when the connection changes — the grid stays
// readable, it just cannot be edited any more.
func (m *Model) clearGridEdits() {
	for _, s := range m.tabs {
		clearResultEdits(&s.result)
		for i := range s.resultSets {
			clearResultEdits(&s.resultSets[i])
		}
	}
}

// clearResultEdits drops the editable state of one result set.
func clearResultEdits(r *resultState) {
	r.src = nil
	r.pending = nil
	r.editing = nil
	r.applyNotice = ""
}

// pendingStatements turns the pending changes into parameterized UPDATE
// statements: one per changed cell, each addressed by the primary key of its
// row.
func (m Model) pendingStatements() ([]execStmt, error) {
	stmts, _, err := m.pendingBatch()
	return stmts, err
}

// pendingBatch is pendingStatements plus, per statement, whether it writes the
// value the grid already holds. That flag is what lets a driver which counts
// only changed rows (MySQL) report 0 affected for a matched row without the
// apply treating it as a failure.
func (m Model) pendingBatch() ([]execStmt, []bool, error) {
	r := m.session.result
	if r.src == nil {
		return nil, nil, errors.New("the result cannot be edited")
	}
	d := m.cfg.Dialect
	if d == nil {
		return nil, nil, errors.New("the connection has no dialect")
	}
	var (
		out       []execStmt
		unchanged []bool
	)
	for _, p := range r.pending {
		col, ok := r.src.cols[p.col]
		if !ok || col == nil {
			continue
		}
		keys, ok := m.rowKeyValues(p.row)
		if !ok {
			continue
		}
		text, args := generateCellUpdate(d, r.src.table, col, r.src.pk, p.value, keys)
		if text == "" {
			continue
		}
		out = append(out, execStmt{Text: text, Args: args})
		unchanged = append(unchanged, cellValueEquals(r.res, p))
	}
	return out, unchanged, nil
}

// cellValueEquals reports whether a pending change writes the value the grid
// already holds (NULL included).
func cellValueEquals(res *driver.Result, p pendingEdit) bool {
	if res == nil || p.row < 0 || p.row >= len(res.Rows) {
		return false
	}
	orig := cellAt(res.Rows[p.row], p.col)
	switch {
	case orig == nil && p.value == nil:
		return true
	case orig == nil || p.value == nil:
		return false
	default:
		return *orig == *p.value
	}
}

// rowCountError says that an UPDATE of a grid apply did not address exactly
// one row: the row was gone (nothing changed), or the key turned out not to
// be unique (more than one row changed). Either way the statement did not do
// what the grid promised.
type rowCountError struct {
	// index is the position of the statement in the batch.
	index int
	// affected is the number of rows the statement changed.
	affected int64
}

// Error implements error.
func (e *rowCountError) Error() string {
	return fmt.Sprintf("the update %d changed %d rows, want exactly 1", e.index+1, e.affected)
}

// finishApply reports the outcome of applying pending grid changes.
//
// On success the pending changes are cleared and the statement that produced
// the grid runs again, so the grid shows what the database holds now. On
// failure the changes stay pending: dropping them would lose what the user
// typed, and the error says what went wrong.
func (m Model) finishApply(msg queryMsg, historyCmd tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, tea.Batch(
			m.setMessage(m.applyFailureText(msg)),
			historyCmd,
		)
	}
	sess := msg.sess
	if sess == nil {
		return m, historyCmd
	}
	sess.result.pending = nil
	sess.result.editing = nil
	// The number of applied changes is what the database reports, not what
	// the user typed: a statement that changed no row changed nothing.
	applied := int64(0)
	for _, n := range msg.affected {
		applied += n
	}
	if applied == 0 {
		// Nothing was reported: fall back to the number of changes the
		// batch carried.
		applied = int64(msg.apply.count)
	}
	notice := m.t(i18n.KeyResultChangesApplied, applied)
	if applied == 1 {
		notice = m.t(i18n.KeyResultOneChangeApplied)
	}
	sess.result.applyNotice = notice
	if sess != m.session {
		return m, tea.Batch(m.setMessage(notice), historyCmd)
	}
	refresh := m.refreshEditedGrid(sess)
	if refresh == nil {
		// A background result replaced the grid while the confirmation was
		// open (and with it the source the refresh would re-run): there is
		// nothing to refresh, so report the outcome right away instead of
		// leaving the notice to attach to some later result.
		sess.result.applyNotice = ""
		return m, tea.Batch(m.setMessage(notice), historyCmd)
	}
	return m, tea.Batch(refresh, historyCmd)
}

// applyFailureText is the message for a batch that did not reach the
// database. A rolled-back batch says so, because "the change could not be
// applied" would leave the user wondering what the earlier statements of the
// batch did. A batch that failed inside a transaction that is still open (the
// manual-commit mode) says that too: part of it is already pending there, and
// only Commit or Rollback decides its fate.
func (m Model) applyFailureText(msg queryMsg) string {
	var mismatch *rowCountError
	var text string
	if errors.As(msg.err, &mismatch) {
		key := i18n.KeyResultEditNoRows
		if mismatch.affected > 1 {
			key = i18n.KeyResultEditTooManyRows
		}
		text = m.t(key)
	} else {
		text = m.t(i18n.KeyResultEditFailed) + ": " + errText(msg.err)
	}
	switch {
	case msg.rolledBack:
		return m.t(i18n.KeyResultEditRolledBack) + " · " + text
	case msg.txOpen:
		// The user's own transaction is still open: earlier statements of
		// the batch are already pending in it.
		return m.t(i18n.KeyMsgManualCommitOn) + " · " + text
	}
	return text
}

// refreshEditedGrid runs the statement that produced the grid again, so the
// grid shows the rows the database holds after the changes were applied.
func (m *Model) refreshEditedGrid(sess *session) tea.Cmd {
	r := &sess.result
	if r.src == nil || r.src.stmt == "" {
		return nil
	}
	return m.executeText(r.src.stmt, sql.Statements(r.src.stmt, m.cfg.Dialect))
}
