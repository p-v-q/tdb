package complete

import (
	"strings"
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/sql"
)

func newEngine() *Engine {
	return NewDefaultEngine(dialect.MustGet("postgres"))
}

func newRequest(text string, col int, kind sql.ContextKind) Request {
	return Request{
		Context: sql.Context{Kind: kind, PrefixStart: sql.Pos{Row: 0, Col: col}},
		Catalog: catalog.DemoCatalog(),
		Dialect: dialect.MustGet("postgres"),
	}
}

func labels(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

func TestCompleteTableAfterFrom(t *testing.T) {
	req := newRequest("", 14, sql.CtxTable)
	req.Context.Prefix = "us"
	cs := newEngine().Complete(req)
	if len(cs) == 0 || cs[0].Label != "users" {
		t.Fatalf("candidates = %v", labels(cs))
	}
	if cs[0].Kind != KindTable {
		t.Fatalf("kind = %v", cs[0].Kind)
	}
}

func TestCompleteNoKeywordNoiseAfterFrom(t *testing.T) {
	req := newRequest("", 14, sql.CtxTable) // empty prefix
	for _, c := range newEngine().Complete(req) {
		if c.Kind == KindKeyword {
			t.Fatalf("no keywords should appear with an empty prefix in table context: %v", c.Label)
		}
	}
}

func TestCompleteQualifiedColumns(t *testing.T) {
	req := newRequest("", 9, sql.CtxQualified)
	req.Context.Prefix = "id"
	req.Context.Qualifier = "u"
	req.Context.Scope = []sql.ScopeEntry{{Table: "users", Alias: "u"}}
	cs := newEngine().Complete(req)
	if len(cs) == 0 {
		t.Fatal("no candidates")
	}
	if cs[0].Kind != KindColumn || cs[0].Insert != "u.id" {
		t.Fatalf("first candidate = %+v", cs[0])
	}
	// The column Detail should include type information.
	if cs[0].Detail == "" {
		t.Fatal("column candidate is missing its type description")
	}
}

func TestCompleteSchemaQualifiedTables(t *testing.T) {
	req := newRequest("", 14, sql.CtxSchema)
	req.Context.Qualifier = "reporting"
	cs := newEngine().Complete(req)
	found := map[string]bool{}
	for _, c := range cs {
		found[c.Label] = true
		if c.Kind != KindTable && c.Kind != KindView {
			t.Fatalf("only tables/views should appear under a schema: %+v", c)
		}
	}
	if !found["v_top_products"] {
		t.Fatalf("candidates = %v", labels(cs))
	}
}

func TestCompleteColumnsInSelectWithScope(t *testing.T) {
	req := newRequest("", 7, sql.CtxColumn)
	req.Context.Prefix = "id"
	req.Context.Scope = []sql.ScopeEntry{{Table: "orders", Alias: "o"}}
	cs := newEngine().Complete(req)
	if len(cs) == 0 {
		t.Fatal("no candidates")
	}
	// Columns of orders in scope should be prefixed with o..
	if cs[0].Insert != "o.id" {
		t.Fatalf("first candidate = %+v", cs[0])
	}
	for _, c := range cs {
		if c.Kind == KindTable {
			t.Fatalf("tables should not appear in column context: %v", c.Label)
		}
	}
}

func TestCompleteFuzzyMatch(t *testing.T) {
	if s := MatchScore("pdt", "products"); s <= 0 {
		t.Fatalf("MatchScore(pdt, products) = %d", s)
	}
	if s := MatchScore("pdt", "categories"); s > 0 {
		t.Fatalf("MatchScore(pdt, categories) = %d, should not match", s)
	}
}

func TestCompletePrefixExactBeatsLonger(t *testing.T) {
	req := newRequest("", 0, sql.CtxDefault)
	req.Context.Prefix = "order"
	cs := newEngine().Complete(req)
	var tables []string
	for _, c := range cs {
		if c.Kind == KindTable || c.Kind == KindView {
			tables = append(tables, c.Label)
		}
	}
	if len(tables) < 2 {
		t.Fatalf("table candidates = %v", tables)
	}
	if tables[0] != "orders" {
		t.Fatalf("an exact table match should rank first: %v", tables)
	}
}

func TestCompleteSnippetAvailableInDefaultContext(t *testing.T) {
	req := newRequest("", 0, sql.CtxDefault)
	req.Context.Prefix = "sel"
	cs := newEngine().Complete(req)
	var hasSnippet, hasKeyword bool
	for _, c := range cs {
		switch c.Kind {
		case KindSnippet:
			hasSnippet = true
		case KindKeyword:
			if c.Label == "SELECT" {
				hasKeyword = true
			}
		}
	}
	if !hasSnippet || !hasKeyword {
		t.Fatalf("sel should match both a snippet and a keyword: %v", labels(cs))
	}
}

func TestCompleteLimit(t *testing.T) {
	req := newRequest("", 0, sql.CtxColumn)
	req.Limit = 5
	cs := newEngine().Complete(req)
	if len(cs) > 5 {
		t.Fatalf("len = %d, want <= 5", len(cs))
	}
}

func TestCandidateDedupeAcrossSources(t *testing.T) {
	req := newRequest("", 0, sql.CtxDefault)
	req.Context.Prefix = "count"
	for _, c := range newEngine().Complete(req) {
		if c.Label == "COUNT" {
			// The function source should appear only once (duplicate aggregate
			// overloads are deduplicated).
			continue
		}
	}
	seen := map[string]int{}
	for _, c := range newEngine().Complete(req) {
		seen[c.Label]++
	}
	if seen["COUNT"] > 1 {
		t.Fatalf("COUNT appeared %d times", seen["COUNT"])
	}
}

func TestMatchScoreEmptyPrefix(t *testing.T) {
	if MatchScore("", "users") != 500 {
		t.Fatal("an empty prefix should return the base score")
	}
	if MatchScore("users", "") != -1 {
		t.Fatal("an empty candidate should not match")
	}
}

func TestCompleteUserDefinedFunctionsFromCatalog(t *testing.T) {
	req := newRequest("", 0, sql.CtxDefault)
	req.Context.Prefix = "order_total"
	cs := newEngine().Complete(req)
	for _, c := range cs {
		if c.Kind == KindFunction && c.Label == "order_total" {
			if c.Detail == "" {
				t.Fatal("a user-defined function needs its signature as detail")
			}
			return
		}
	}
	t.Fatalf("catalog function order_total is missing from %v", labels(cs))
}

func TestCompleteMaterializedViewUnderSchema(t *testing.T) {
	req := newRequest("", 14, sql.CtxSchema)
	req.Context.Qualifier = "reporting"
	cs := newEngine().Complete(req)
	for _, c := range cs {
		if c.Label == "mv_sales_daily" {
			if c.Kind != KindView {
				t.Fatalf("materialized view kind = %v, want view", c.Kind)
			}
			return
		}
	}
	t.Fatalf("materialized view mv_sales_daily is missing from %v", labels(cs))
}

// TestCompleteNonRelationResourcesFromCatalog pins that the object names of
// every non-relation catalog resource show up in a general position, are
// classified by their own kind (never as a table/view), and carry the driver's
// Resource.Detail in the short Detail text.
func TestCompleteNonRelationResourcesFromCatalog(t *testing.T) {
	cases := []struct {
		prefix string
		name   string
		kind   Kind
		detail string // substring the Detail must carry (Resource.Detail)
	}{
		{"archive_orders", "archive_orders", KindProcedure, "(before date)"},
		{"orders_touch_updated", "orders_touch_updated", KindTrigger, "BEFORE UPDATE"},
		{"users_audit", "users_audit", KindTrigger, "AFTER INSERT OR UPDATE"},
		{"idx_orders_user_id", "idx_orders_user_id", KindIndex, "user_id"},
		{"users_email_key", "users_email_key", KindIndex, "email"},
		{"order_number_seq", "order_number_seq", KindSequence, "sequence"},
	}
	for _, c := range cases {
		req := newRequest("", 0, sql.CtxDefault)
		req.Context.Prefix = c.prefix
		cs := newEngine().Complete(req)
		var got *Candidate
		for i := range cs {
			if cs[i].Label == c.name {
				got = &cs[i]
				break
			}
		}
		if got == nil {
			t.Errorf("%s missing from %v", c.name, labels(cs))
			continue
		}
		if got.Kind != c.kind {
			t.Errorf("%s kind = %v, want %v", c.name, got.Kind, c.kind)
		}
		if got.Kind == KindTable || got.Kind == KindView {
			t.Errorf("%s must be distinguishable from a relation, kind = %v", c.name, got.Kind)
		}
		if !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s detail = %q, want it to contain %q", c.name, got.Detail, c.detail)
		}
	}
}

// TestCompleteResourcesStayOutOfRelationContexts guards the existing
// table/column/qualified/schema behaviour: positions that expect a relation or
// a column must never start offering triggers, indexes, sequences or
// procedures.
func TestCompleteResourcesStayOutOfRelationContexts(t *testing.T) {
	resourceKinds := map[Kind]bool{
		KindProcedure: true, KindTrigger: true, KindIndex: true, KindSequence: true,
	}
	contexts := []struct {
		name      string
		kind      sql.ContextKind
		qualifier string
		scope     []sql.ScopeEntry
	}{
		{name: "table", kind: sql.CtxTable},
		{name: "column", kind: sql.CtxColumn},
		{name: "qualified", kind: sql.CtxQualified, qualifier: "u",
			scope: []sql.ScopeEntry{{Table: "users", Alias: "u"}}},
		{name: "schema", kind: sql.CtxSchema, qualifier: "reporting"},
	}
	for _, c := range contexts {
		// "order" prefix-matches several catalog resources (orders_touch_updated,
		// order_number_seq, …), so a leaking source would be caught here.
		req := newRequest("", 0, c.kind)
		req.Context.Prefix = "order"
		req.Context.Qualifier = c.qualifier
		req.Context.Scope = c.scope
		for _, cand := range newEngine().Complete(req) {
			if resourceKinds[cand.Kind] {
				t.Errorf("%s context must not offer %v candidate %q", c.name, cand.Kind, cand.Label)
			}
		}
	}
}

// TestResourceCandidateRanking keeps the new kinds inside their priority band:
// above snippets/keywords, below relations, columns and schemas, with
// procedures sharing the callable tier with functions.
func TestResourceCandidateRanking(t *testing.T) {
	for _, k := range []Kind{KindProcedure, KindSequence, KindTrigger, KindIndex} {
		if k.priority() >= KindSchema.priority() {
			t.Errorf("%v priority = %d, must rank below schema/column/table", k, k.priority())
		}
		if k.priority() <= KindSnippet.priority() {
			t.Errorf("%v priority = %d, must rank above snippets/keywords", k, k.priority())
		}
	}
	if KindProcedure.priority() != KindFunction.priority() {
		t.Errorf("procedure priority = %d, want the callable tier shared with functions (%d)",
			KindProcedure.priority(), KindFunction.priority())
	}
}

// TestCompleteDDLStatementsEndToEnd runs the full chain (sql.AnalyzeContext →
// engine) on DDL statements, pinning that the object-name positions survive
// lexing — even in dialects where words such as TRIGGER or FUNCTION are plain
// identifiers — and yield candidates of the expected object type.
func TestCompleteDDLStatementsEndToEnd(t *testing.T) {
	d := dialect.MustGet("postgres")
	cases := []struct {
		name string
		text string
		want string // candidate label that must be offered
		kind Kind   // its completion kind
	}{
		{"drop trigger", "DROP TRIGGER ", "orders_touch_updated", KindTrigger},
		{"drop index", "DROP INDEX ", "idx_orders_user_id", KindIndex},
		{"drop view", "DROP VIEW ", "v_top_products", KindView},
		{"drop function", "DROP FUNCTION ", "order_total", KindFunction},
		{"drop procedure", "DROP PROCEDURE ", "archive_orders", KindProcedure},
		{"alter table", "ALTER TABLE ", "users", KindTable},
		{"alter table add column", "ALTER TABLE users ADD COLUMN ", "email", KindColumn},
	}
	for _, c := range cases {
		text := c.text
		ctx := sql.AnalyzeContext(text, sql.Pos{Row: 0, Col: len(text)}, d)
		req := Request{Context: ctx, Catalog: catalog.DemoCatalog(), Dialect: d}
		cs := newEngine().Complete(req)
		var got *Candidate
		for i := range cs {
			if cs[i].Label == c.want {
				got = &cs[i]
				break
			}
		}
		if got == nil {
			t.Errorf("%s: %s missing from %v", c.name, c.want, labels(cs))
			continue
		}
		if got.Kind != c.kind {
			t.Errorf("%s: %s kind = %v, want %v", c.name, c.want, got.Kind, c.kind)
		}
	}
}
