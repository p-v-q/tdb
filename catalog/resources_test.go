package catalog

import "testing"

func TestCapabilitiesOnlyClaimSupportedKinds(t *testing.T) {
	c := Capable(KindTable, KindView, KindTrigger)
	if !c.Supports(KindTable) || !c.Supports(KindTrigger) {
		t.Fatalf("capabilities = %v must include table and trigger", c)
	}
	if c.Supports(KindFunction) || c.Supports(KindSequence) {
		t.Fatalf("capabilities = %v must not claim unsupported kinds", c)
	}
}

func TestCatalogWithoutCapabilitiesDoesNotHideObjects(t *testing.T) {
	cat := &Catalog{}
	if !cat.Supports(KindFunction) {
		t.Fatal("an undeclared catalog should display whatever it contains")
	}
	cat.Capabilities = Capable(KindTable)
	if cat.Supports(KindFunction) {
		t.Fatal("a declared catalog must filter unsupported kinds")
	}
}

func TestTableKindMapping(t *testing.T) {
	tests := []struct {
		typ  TableType
		want ResourceKind
	}{
		{TypeTable, KindTable},
		{TypeView, KindView},
		{TypeMaterializedView, KindMaterializedView},
		{"", KindTable},
	}
	for _, tt := range tests {
		tbl := &Table{Type: tt.typ}
		if got := tbl.Kind(); got != tt.want {
			t.Errorf("Table{Type:%q}.Kind() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestSchemaCountsRelationsAndResourcesSeparately(t *testing.T) {
	s := &Schema{
		Name: "public",
		Tables: []*Table{
			{Name: "users", Type: TypeTable},
			{Name: "adults", Type: TypeView},
			{Name: "daily", Type: TypeMaterializedView},
		},
		Resources: []*Resource{
			{Kind: KindFunction, Name: "order_total"},
			{Kind: KindFunction, Name: "label"},
			{Kind: KindTrigger, Name: "audit"},
		},
	}
	if got := s.Count(KindTable); got != 1 {
		t.Errorf("tables = %d, want 1", got)
	}
	if got := s.Count(KindView); got != 1 {
		t.Errorf("views = %d, want 1", got)
	}
	if got := s.Count(KindMaterializedView); got != 1 {
		t.Errorf("materialized views = %d, want 1", got)
	}
	if got := s.Count(KindFunction); got != 2 {
		t.Errorf("functions = %d, want 2", got)
	}
	if got := s.Count(KindTrigger); got != 1 {
		t.Errorf("triggers = %d, want 1", got)
	}
	if got := s.Count(KindSequence); got != 0 {
		t.Errorf("sequences = %d, want 0", got)
	}
}

func TestAllResourcesSortsBySchemaAndName(t *testing.T) {
	cat := &Catalog{Schemas: []*Schema{
		{Name: "z", Resources: []*Resource{{Kind: KindFunction, Schema: "z", Name: "b"}}},
		{Name: "a", Resources: []*Resource{
			{Kind: KindFunction, Schema: "a", Name: "z"},
			{Kind: KindTrigger, Schema: "a", Name: "a"},
		}},
	}}
	got := cat.AllResources()
	if len(got) != 3 {
		t.Fatalf("resources = %d, want 3", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "z" || got[2].Name != "b" {
		t.Fatalf("resources out of order: %q, %q, %q", got[0].Name, got[1].Name, got[2].Name)
	}
}
