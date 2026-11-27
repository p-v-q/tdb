package complete

import (
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/sql"
)

func TestStripCursorMarker(t *testing.T) {
	cases := []struct {
		body string
		want string
		tail int
	}{
		// Without a marker the caret sits at the end of the inserted text, so
		// the tail is zero.
		{"CASE WHEN | THEN ", "CASE WHEN | THEN ", 0},
		{"\x00SELECT", "SELECT", len([]rune("SELECT"))},
		{"SELECT\x00", "SELECT", 0},
		{"CASE WHEN \x00THEN | END", "CASE WHEN THEN | END", len([]rune("THEN | END"))},
	}
	for _, c := range cases {
		got, tail := stripCursorMarker(c.body)
		if got != c.want || tail != c.tail {
			t.Errorf("stripCursorMarker(%q) = (%q, %d), want (%q, %d)", c.body, got, tail, c.want, c.tail)
		}
	}
}

// TestSnippetTailOffsetInRange pins the candidate contract that the caret rests
// at the end of an inserted candidate unless a snippet explicitly asks for a
// placeholder: TailOffset must always count runes that really follow the caret.
func TestSnippetTailOffsetInRange(t *testing.T) {
	cands := (snippetSource{}).Complete(Request{Context: sql.Context{}})
	if len(cands) == 0 {
		t.Fatal("expected at least one snippet candidate")
	}
	for _, c := range cands {
		insert := c.Insert
		if insert == "" {
			insert = c.Label
		}
		runes := len([]rune(insert))
		if c.TailOffset < 0 || c.TailOffset > runes {
			t.Errorf("snippet %q: TailOffset %d is out of range for %d runes",
				c.Label, c.TailOffset, runes)
		}
	}
}

// TestResourceSourceWithoutResources pins that the non-relation resource
// source stays silent when there is nothing to offer: no catalog, a catalog
// without schemas, or a catalog whose only resources are functions (those
// belong to functionSource).
func TestResourceSourceWithoutResources(t *testing.T) {
	d := dialect.MustGet("postgres")
	cases := []struct {
		name string
		cat  *catalog.Catalog
	}{
		{name: "nil catalog", cat: nil},
		{name: "no schemas", cat: &catalog.Catalog{}},
		{name: "functions only", cat: &catalog.Catalog{Schemas: []*catalog.Schema{{
			Name:   "public",
			Tables: []*catalog.Table{{Schema: "public", Name: "users"}},
			Resources: []*catalog.Resource{
				{Kind: catalog.KindFunction, Schema: "public", Name: "order_total", Detail: "(order_id bigint) → numeric"},
			},
		}}}},
	}
	for _, c := range cases {
		req := Request{Context: sql.Context{Kind: sql.CtxDefault}, Catalog: c.cat, Dialect: d}
		if cs := (resourceSource{}).Complete(req); len(cs) != 0 {
			t.Errorf("%s: got %d candidates, want none (%v)", c.name, len(cs), labels(cs))
		}
	}
}

// TestResourceSourceDetailFormat pins the rendered Detail of a trigger: kind
// word first, then the driver's Resource.Detail, then the owning table.
func TestResourceSourceDetailFormat(t *testing.T) {
	d := dialect.MustGet("postgres")
	cat := &catalog.Catalog{Schemas: []*catalog.Schema{{
		Name: "public",
		Resources: []*catalog.Resource{
			{Kind: catalog.KindTrigger, Schema: "public", Name: "order", Owner: "orders", Detail: "BEFORE INSERT"},
		},
	}}}
	cs := (resourceSource{}).Complete(Request{Context: sql.Context{Kind: sql.CtxDefault}, Catalog: cat, Dialect: d})
	if len(cs) != 1 {
		t.Fatalf("candidates = %v", labels(cs))
	}
	c := cs[0]
	if want := dialect.QuoteIfNeeded(d, "order"); c.Label != want {
		t.Errorf("label = %q, want the quoted form %q", c.Label, want)
	}
	if c.Kind != KindTrigger {
		t.Errorf("kind = %v, want trigger", c.Kind)
	}
	if c.Detail != "trigger · BEFORE INSERT · on orders" {
		t.Errorf("detail = %q, want %q", c.Detail, "trigger · BEFORE INSERT · on orders")
	}
}

// kindSet builds a set of candidate kinds for the DDL filtering tests.
func kindSet(ks ...Kind) map[Kind]bool {
	out := make(map[Kind]bool, len(ks))
	for _, k := range ks {
		out[k] = true
	}
	return out
}

// TestCompleteDDLObjectPositions pins the candidate filtering of the DDL
// object-name positions end to end (engine + all sources): each position must
// offer the names of the expected object type and nothing unrelated — no
// tables in a trigger position, no columns or resources in a table position,
// no keywords dumped on an empty prefix. Snippets keep their existing
// behaviour of appearing in every position.
func TestCompleteDDLObjectPositions(t *testing.T) {
	cases := []struct {
		name    string
		kind    sql.ContextKind
		want    string        // a label that must be offered
		allowed map[Kind]bool // kinds that may be offered
	}{
		{name: "drop table", kind: sql.CtxTable, want: "users",
			allowed: kindSet(KindTable, KindView, KindSchema, KindSnippet)},
		{name: "drop view", kind: sql.CtxView, want: "v_top_products",
			allowed: kindSet(KindView, KindSnippet)},
		{name: "drop trigger", kind: sql.CtxTrigger, want: "orders_touch_updated",
			allowed: kindSet(KindTrigger, KindSnippet)},
		{name: "drop index", kind: sql.CtxIndex, want: "idx_orders_user_id",
			allowed: kindSet(KindIndex, KindSnippet)},
		{name: "drop function", kind: sql.CtxFunction, want: "order_total",
			allowed: kindSet(KindFunction, KindSnippet)},
		{name: "drop procedure", kind: sql.CtxProcedure, want: "archive_orders",
			allowed: kindSet(KindProcedure, KindSnippet)},
		{name: "drop sequence", kind: sql.CtxSequence, want: "order_number_seq",
			allowed: kindSet(KindSequence, KindSnippet)},
	}
	for _, c := range cases {
		req := newRequest("", 0, c.kind)
		cs := newEngine().Complete(req)
		found := false
		for _, cand := range cs {
			if !c.allowed[cand.Kind] {
				t.Errorf("%s: %v candidate %q leaked into the list", c.name, cand.Kind, cand.Label)
			}
			if cand.Label == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %s missing from %v", c.name, c.want, labels(cs))
		}
	}
}

// TestFunctionSourceDropFunctionCatalogOnly pins that a DROP/ALTER FUNCTION
// position offers the stored functions of the catalog and none of the
// dialect's built-in functions (those cannot be dropped).
func TestFunctionSourceDropFunctionCatalogOnly(t *testing.T) {
	d := dialect.MustGet("postgres")
	req := Request{Context: sql.Context{Kind: sql.CtxFunction}, Catalog: catalog.DemoCatalog(), Dialect: d}
	cs := (functionSource{d: d}).Complete(req)
	got := map[string]bool{}
	for _, c := range cs {
		if c.Kind != KindFunction {
			t.Errorf("kind = %v, want function", c.Kind)
		}
		got[c.Label] = true
	}
	for _, name := range []string{"order_total", "user_status_label", "refresh_daily_sales"} {
		if !got[name] {
			t.Errorf("catalog function %q missing from %v", name, labels(cs))
		}
	}
	for _, f := range d.Functions() {
		if got[f.Name] {
			t.Errorf("built-in function %s must not appear in a DROP FUNCTION position", f.Name)
		}
	}
}

// TestResourceSourceDDLKinds pins that each DDL object-name position offers
// exactly the resource kind the statement names.
func TestResourceSourceDDLKinds(t *testing.T) {
	d := dialect.MustGet("postgres")
	cat := catalog.DemoCatalog()
	cases := []struct {
		name string
		kind sql.ContextKind
		want []string
	}{
		{"trigger position", sql.CtxTrigger, []string{"orders_touch_updated", "users_audit"}},
		{"index position", sql.CtxIndex, []string{"idx_orders_user_id", "users_email_key"}},
		{"procedure position", sql.CtxProcedure, []string{"archive_orders"}},
		{"sequence position", sql.CtxSequence, []string{"order_number_seq"}},
	}
	for _, c := range cases {
		req := Request{Context: sql.Context{Kind: c.kind}, Catalog: cat, Dialect: d}
		got := map[string]bool{}
		for _, l := range labels((resourceSource{}).Complete(req)) {
			got[l] = true
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: candidates = %v, want exactly %v", c.name, got, c.want)
			continue
		}
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("%s: %q missing from %v", c.name, w, got)
			}
		}
	}
	// Views belong to tableSource and stored functions to functionSource.
	for _, kind := range []sql.ContextKind{sql.CtxView, sql.CtxFunction} {
		req := Request{Context: sql.Context{Kind: kind}, Catalog: cat, Dialect: d}
		if cs := (resourceSource{}).Complete(req); len(cs) != 0 {
			t.Errorf("%v position must not offer resource names: %v", kind, labels(cs))
		}
	}
}

// TestTableViewPositionOnlyViews pins that a DROP/ALTER VIEW position offers
// views (materialized views included) and no plain tables.
func TestTableViewPositionOnlyViews(t *testing.T) {
	d := dialect.MustGet("postgres")
	req := Request{Context: sql.Context{Kind: sql.CtxView}, Catalog: catalog.DemoCatalog(), Dialect: d}
	cs := (tableSource{}).Complete(req)
	found := false
	for _, c := range cs {
		if c.Kind != KindView {
			t.Errorf("candidate %q has kind %v, want view", c.Label, c.Kind)
		}
		switch c.Label {
		case "v_top_products", "mv_sales_daily":
			found = true
		case "users", "orders":
			t.Errorf("plain table %q leaked into a view position", c.Label)
		}
	}
	if !found {
		t.Fatalf("no view offered in a view position: %v", labels(cs))
	}
}

// TestKeywordSourceDDLPositions pins that an object-name position stays quiet
// with an empty prefix (no keyword dump, like the table position) while a
// typed prefix still reaches the keyword list (DROP VIEW cas → CASCADE).
func TestKeywordSourceDDLPositions(t *testing.T) {
	d := dialect.MustGet("postgres")
	kinds := []sql.ContextKind{sql.CtxTable, sql.CtxTrigger, sql.CtxIndex,
		sql.CtxView, sql.CtxFunction, sql.CtxProcedure, sql.CtxSequence}
	for _, kind := range kinds {
		req := Request{Context: sql.Context{Kind: kind}, Dialect: d}
		if cs := (keywordSource{d: d}).Complete(req); len(cs) != 0 {
			t.Errorf("%v with an empty prefix: got %d keyword candidates, want none", kind, len(cs))
		}
	}
	req := Request{Context: sql.Context{Kind: sql.CtxTrigger, Prefix: "cas"}, Dialect: d}
	var hasCascade bool
	for _, c := range (keywordSource{d: d}).Complete(req) {
		if c.Label == "CASCADE" {
			hasCascade = true
		}
	}
	if !hasCascade {
		t.Fatal("CASCADE should remain reachable with a typed prefix in a DDL position")
	}
}
