package complete

import (
	"fmt"
	"strings"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/sql"
)

// ---- keyword source ----

type keywordSource struct{ d dialect.Dialect }

func (s keywordSource) Complete(req Request) []Candidate {
	ctx := req.Context
	// After a qualifier (schema./alias./table.) only columns or tables are
	// possible, so do not suggest keywords.
	if ctx.Qualifier != "" {
		return nil
	}
	// In a position that explicitly expects a table/column/qualifier or a DDL
	// object name (DROP TRIGGER |, …), listing hundreds of keywords with an
	// empty prefix only creates noise. With a prefix the keyword list stays
	// available (DROP VIEW cas → CASCADE).
	if ctx.Prefix == "" {
		switch ctx.Kind {
		case sql.CtxTable, sql.CtxColumn, sql.CtxQualified, sql.CtxSchema,
			sql.CtxTrigger, sql.CtxIndex, sql.CtxView, sql.CtxFunction,
			sql.CtxProcedure, sql.CtxSequence:
			return nil
		}
	}
	keywords := s.d.Keywords()
	out := make([]Candidate, 0, len(keywords))
	for _, k := range keywords {
		out = append(out, Candidate{Kind: KindKeyword, Label: k})
	}
	return out
}

// ---- function source ----

type functionSource struct{ d dialect.Dialect }

func (s functionSource) Complete(req Request) []Candidate {
	ctx := req.Context
	switch ctx.Kind {
	case sql.CtxDefault, sql.CtxColumn, sql.CtxNone:
		// General expression positions list the dialect's built-in functions.
	case sql.CtxFunction:
		// DROP/ALTER FUNCTION names a stored function: built-ins cannot be
		// dropped, so only catalog functions are offered below.
	default:
		// Do not suggest functions in table/qualified-name positions or where
		// another object type is expected.
		return nil
	}
	funcs := s.d.Functions()
	out := make([]Candidate, 0, len(funcs))
	if ctx.Kind != sql.CtxFunction {
		for _, f := range funcs {
			doc := f.Doc
			switch {
			case f.Aggregate:
				doc = "[aggregate function] " + doc
			case f.Window:
				doc = "[window function] " + doc
			}
			out = append(out, Candidate{
				Kind:   KindFunction,
				Label:  f.Name,
				Detail: f.Signature,
				Doc:    doc,
			})
		}
	}
	// Catalog functions are user-defined or extension-provided (pg_trgm,
	// PostGIS, …): the catalog lists everything callable outside the system
	// schemas. They are stored separately per schema by the driver, so no
	// database-specific assumption leaks into the completion engine.
	if req.Catalog != nil {
		// The engine scores and sorts the final list, so walking the snapshot in
		// place avoids sorting every resource on each keystroke.
		for _, schema := range req.Catalog.Schemas {
			for _, r := range schema.Resources {
				if r.Kind != catalog.KindFunction {
					continue
				}
				detail := r.Detail
				if detail == "" {
					detail = "function"
				}
				out = append(out, Candidate{
					Kind:   KindFunction,
					Label:  dialect.QuoteIfNeeded(req.Dialect, r.Name),
					Detail: detail,
					Doc:    r.Comment,
				})
			}
		}
	}
	return out
}

// ---- non-relation resource source ----

// resourceSource offers the object names of catalog resources that are neither
// relations nor functions: stored procedures, triggers, indexes and sequences.
// Relations stay in tableSource, catalog functions stay in functionSource, so
// neither of those sources changes behaviour.
//
// Kind trade-off: a procedure gets its own KindProcedure instead of being
// folded into KindFunction. Both are callable and share the same priority
// tier, but the separate kind keeps a CALL target distinguishable in the popup
// (ui/icons.go maps it to the same glyph the object browser uses for
// procedures) and keeps deduplication from swallowing a procedure that happens
// to share a name with a function. Triggers, indexes and sequences must never
// be mistaken for table names, so they get their own kinds as well.
//
// Candidates carry no i18n keys: Detail and Doc are plain English, rendered in
// the " · " style the other sources use.
type resourceSource struct{}

func (resourceSource) Complete(req Request) []Candidate {
	cat := req.Catalog
	if cat == nil {
		return nil
	}
	ctx := req.Context
	// After a qualifier the answer is a relation (tableSource) or a column of
	// it, and positions that expect a table (FROM/JOIN …) or a column
	// (SELECT/WHERE …) must keep listing relations and columns. General
	// positions (statement start, CALL-style statements — sql reports both as
	// CtxDefault/CtxNone) offer every resource kind, while a DDL object-name
	// position (DROP TRIGGER |, …) offers only the kind the statement names.
	if ctx.Qualifier != "" {
		return nil
	}
	want, all, ok := resourceContextKind(ctx.Kind)
	if !ok {
		return nil
	}
	// The engine scores and sorts the final list, so walking the snapshot in
	// place avoids sorting every resource on each keystroke.
	var out []Candidate
	for _, r := range cat.AllResources() {
		kind, ok := resourceCompletionKind(r.Kind)
		if !ok || (!all && r.Kind != want) {
			continue
		}
		out = append(out, Candidate{
			Kind:   kind,
			Label:  dialect.QuoteIfNeeded(req.Dialect, r.Name),
			Detail: resourceDetail(r),
			Doc:    resourceDoc(r),
		})
	}
	return out
}

// resourceCompletionKind maps a catalog resource kind onto a completion kind.
// Relations belong to tableSource and functions to functionSource, so they are
// reported as "not handled here" rather than suggested twice.
func resourceCompletionKind(k catalog.ResourceKind) (Kind, bool) {
	switch k {
	case catalog.KindProcedure:
		return KindProcedure, true
	case catalog.KindTrigger:
		return KindTrigger, true
	case catalog.KindIndex:
		return KindIndex, true
	case catalog.KindSequence:
		return KindSequence, true
	default:
		return 0, false
	}
}

// resourceContextKind reports which catalog resource kind a completion context
// expects. all is true in general positions where every resource kind is
// welcome; ok is false when the position must stay free of resource names —
// relation/column/qualifier positions, and DDL targets owned by another source
// (tables and views by tableSource, stored functions by functionSource).
func resourceContextKind(k sql.ContextKind) (want catalog.ResourceKind, all, ok bool) {
	switch k {
	case sql.CtxDefault, sql.CtxNone:
		return "", true, true
	case sql.CtxTrigger:
		return catalog.KindTrigger, false, true
	case sql.CtxIndex:
		return catalog.KindIndex, false, true
	case sql.CtxProcedure:
		return catalog.KindProcedure, false, true
	case sql.CtxSequence:
		return catalog.KindSequence, false, true
	default:
		return "", false, false
	}
}

// resourceDetail renders "<kind> · <driver detail> · on <owner>" in the same
// " · " style as the other sources. The kind word comes first so an object the
// driver left without a Detail string (common for sequences) still explains
// itself, and the owner tells which table a trigger or index belongs to.
func resourceDetail(r *catalog.Resource) string {
	parts := []string{string(r.Kind)}
	if r.Detail != "" {
		parts = append(parts, r.Detail)
	}
	if r.Owner != "" {
		parts = append(parts, "on "+r.Owner)
	}
	return strings.Join(parts, " · ")
}

// resourceDoc prefers the driver's comment and falls back to a short English
// description of the object.
func resourceDoc(r *catalog.Resource) string {
	if r.Comment != "" {
		return r.Comment
	}
	if r.Owner != "" {
		return fmt.Sprintf("%s %s on %s", r.Kind, r.QualifiedName(), r.Owner)
	}
	return fmt.Sprintf("%s %s", r.Kind, r.QualifiedName())
}

// ---- table source ----

type tableSource struct{}

func (tableSource) Complete(req Request) []Candidate {
	cat := req.Catalog
	if cat == nil {
		return nil
	}
	ctx := req.Context

	// Qualified-name case: the qualifier is a schema, so list that schema's tables.
	if ctx.Qualifier != "" {
		s := cat.FindSchema(ctx.Qualifier)
		if s == nil {
			return nil
		}
		out := make([]Candidate, 0, len(s.Tables))
		for _, t := range s.Tables {
			out = append(out, tableCandidate(t, dialect.QuoteIfNeeded(req.Dialect, t.Name), ""))
		}
		return out
	}

	switch ctx.Kind {
	case sql.CtxTable, sql.CtxDefault, sql.CtxNone:
		// General relation positions list everything below (tables, views and
		// schema names).
	case sql.CtxView:
		// DROP/ALTER VIEW names a view (materialized views included); plain
		// tables would be noise here.
		var out []Candidate
		for _, t := range cat.AllTables() {
			if t.Type != catalog.TypeView && t.Type != catalog.TypeMaterializedView {
				continue
			}
			out = append(out, tableCandidate(t, dialect.QuoteIfNeeded(req.Dialect, t.Name), ""))
		}
		return out
	default:
		return nil // do not suggest tables in column/qualified-name positions
	}

	var out []Candidate
	for _, t := range cat.AllTables() {
		out = append(out, tableCandidate(t, dialect.QuoteIfNeeded(req.Dialect, t.Name), ""))
	}
	for _, s := range cat.Schemas {
		out = append(out, Candidate{
			Kind:   KindSchema,
			Label:  s.Name,
			Detail: fmt.Sprintf("schema · %d tables", len(s.Tables)),
			Doc:    "Namespace; use the " + s.Name + ". prefix to list its tables",
		})
	}
	return out
}

func tableCandidate(t *catalog.Table, label, qualifier string) Candidate {
	kind := KindTable
	if t.Type == catalog.TypeView || t.Type == catalog.TypeMaterializedView {
		kind = KindView
	}
	doc := t.Comment
	if doc == "" {
		doc = fmt.Sprintf("%s (%s), %d columns", t.Name, t.Type, len(t.Columns))
	}
	insert := label
	if qualifier != "" {
		insert = qualifier + "." + label
	}
	return Candidate{
		Kind:   kind,
		Label:  label,
		Insert: insert,
		Detail: fmt.Sprintf("%s · %s · %d columns", t.Type, t.Schema, len(t.Columns)),
		Doc:    doc,
	}
}

// ---- column source ----

type columnSource struct{}

func (columnSource) Complete(req Request) []Candidate {
	cat := req.Catalog
	if cat == nil {
		return nil
	}
	ctx := req.Context

	// Qualified-name case: alias. / table. / schema.table.
	if ctx.Qualifier != "" {
		// 1) An alias or table in the current statement scope.
		for _, e := range ctx.Scope {
			if !e.Match(ctx.Qualifier) {
				continue
			}
			if t := findTableForScope(cat, e); t != nil {
				return columnCandidates(t, req.Dialect, quoteAlias(req.Dialect, e), true)
			}
		}
		// 2) The qualifier is a schema name: column completion does not apply
		// (table completion is handled by tableSource).
		if cat.FindSchema(ctx.Qualifier) != nil {
			return nil
		}
		// 3) Otherwise treat it as a table name.
		if t := cat.FindTable(ctx.Qualifier); t != nil {
			return columnCandidates(t, req.Dialect, dialect.QuoteIfNeeded(req.Dialect, ctx.Qualifier), true)
		}
		return nil
	}

	switch ctx.Kind {
	case sql.CtxColumn:
		if len(ctx.Scope) > 0 {
			var out []Candidate
			for _, e := range ctx.Scope {
				if t := findTableForScope(cat, e); t != nil {
					out = append(out, columnCandidates(t, req.Dialect, quoteAlias(req.Dialect, e), true)...)
				}
			}
			return out
		}
		// With no FROM clause, fall back to all columns (annotated with their
		// table name).
		return allColumnCandidates(cat, req.Dialect)
	case sql.CtxDefault, sql.CtxNone:
		// General position: suggest only columns of tables in scope, to help
		// write expressions such as u.id quickly.
		var out []Candidate
		for _, e := range ctx.Scope {
			if t := findTableForScope(cat, e); t != nil {
				out = append(out, columnCandidates(t, req.Dialect, quoteAlias(req.Dialect, e), true)...)
			}
		}
		return out
	}
	return nil
}

// quoteAlias returns the qualifier prefix for a scope entry (preferring the
// alias), quoting it when necessary.
func quoteAlias(d dialect.Dialect, e sql.ScopeEntry) string {
	name := e.Alias
	if name == "" {
		name = e.Table
	}
	return dialect.QuoteIfNeeded(d, name)
}

// findTableForScope resolves a scope entry to a table in the catalog.
func findTableForScope(cat *catalog.Catalog, e sql.ScopeEntry) *catalog.Table {
	if e.Schema != "" {
		if t := cat.FindTable(e.Schema + "." + e.Table); t != nil {
			return t
		}
	}
	return cat.FindTable(e.Table)
}

// columnCandidates builds the column candidates for a table.
// When qualified is true, it inserts them in `alias.column` form.
func columnCandidates(t *catalog.Table, d dialect.Dialect, qualifier string, qualified bool) []Candidate {
	out := make([]Candidate, 0, len(t.Columns))
	for _, col := range t.Columns {
		label := dialect.QuoteIfNeeded(d, col.Name)
		insert := label
		if qualified && qualifier != "" {
			insert = qualifier + "." + label
		}
		out = append(out, Candidate{
			Kind:   KindColumn,
			Label:  label,
			Insert: insert,
			Detail: columnDetail(col),
			Doc:    columnDoc(t, col),
		})
	}
	return out
}

func allColumnCandidates(cat *catalog.Catalog, d dialect.Dialect) []Candidate {
	var out []Candidate
	for _, ref := range cat.AllColumns() {
		label := dialect.QuoteIfNeeded(d, ref.Column.Name)
		out = append(out, Candidate{
			Kind:   KindColumn,
			Label:  label,
			Insert: ref.Table.Name + "." + label,
			Detail: fmt.Sprintf("%s · %s", ref.Table.Name, ref.Column.Type),
			Doc:    columnDoc(ref.Table, ref.Column),
		})
	}
	return out
}

func columnDetail(col *catalog.Column) string {
	var parts []string
	if col.Type != "" {
		parts = append(parts, col.Type)
	}
	if col.PK {
		parts = append(parts, "PK")
	}
	if col.Unique {
		parts = append(parts, "UQ")
	}
	if col.Nullable {
		parts = append(parts, "NULL")
	}
	return strings.Join(parts, " · ")
}

func columnDoc(t *catalog.Table, col *catalog.Column) string {
	if col.Comment != "" {
		return fmt.Sprintf("%s.%s: %s", t.Name, col.Name, col.Comment)
	}
	return fmt.Sprintf("%s.%s (%s)", t.Name, col.Name, col.Type)
}

// ---- snippet source ----

type snippet struct {
	trigger string
	title   string
	body    string // contains the cursor marker '\x00'
}

var snippets = []snippet{
	{trigger: "sel", title: "SELECT ... FROM ...", body: "SELECT \x00*\nFROM "},
	{trigger: "selw", title: "SELECT ... FROM ... WHERE ...", body: "SELECT \x00*\nFROM \nWHERE "},
	{trigger: "ins", title: "INSERT INTO ... VALUES ...", body: "INSERT INTO \x00\nVALUES ()"},
	{trigger: "upd", title: "UPDATE ... SET ... WHERE ...", body: "UPDATE \x00\nSET \nWHERE "},
	{trigger: "cte", title: "WITH cte AS (...) SELECT ...", body: "WITH \x00 AS (\n  SELECT *\n  FROM \n)\nSELECT *\nFROM "},
	{trigger: "join", title: "JOIN ... ON ...", body: "JOIN \x00\nON "},
	{trigger: "case", title: "CASE WHEN ... THEN ... END", body: "CASE\n  WHEN \x00 THEN \n  ELSE \nEND"},
}

type snippetSource struct{}

func (snippetSource) Complete(req Request) []Candidate {
	ctx := req.Context
	if ctx.Qualifier != "" {
		return nil
	}
	prefix := strings.ToUpper(ctx.Prefix)
	out := make([]Candidate, 0, len(snippets))
	for _, s := range snippets {
		if prefix != "" && !strings.HasPrefix(strings.ToUpper(s.trigger), prefix) {
			continue
		}
		insert, tail := stripCursorMarker(s.body)
		out = append(out, Candidate{
			Kind:       KindSnippet,
			Label:      s.title,
			Insert:     insert,
			Detail:     "snippet " + s.trigger,
			Doc:        "Type " + s.trigger + " and press Tab to insert the template",
			TailOffset: tail,
		})
	}
	return out
}

// stripCursorMarker removes the caret marker and returns the text to insert plus
// how many runes of it end up after the caret (the marker position).
func stripCursorMarker(body string) (string, int) {
	i := strings.IndexRune(body, '\x00')
	if i < 0 {
		return body, 0 // no marker: the caret sits at the end of the text
	}
	insert := body[:i] + body[i+1:]
	return insert, len([]rune(body[i+1:]))
}
