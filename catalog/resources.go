package catalog

import (
	"sort"
	"strings"
)

// ResourceKind identifies a kind of database object. Drivers decide which
// kinds they can introspect; the UI and completion engine only interpret the
// catalog snapshot, never a specific database's system tables.
type ResourceKind string

// The resource kinds tdb knows how to display. Not every database supports
// every kind: SQLite has triggers and indexes but no stored functions or
// sequences, while PostgreSQL has functions, procedures, sequences and
// materialized views as well.
const (
	KindTable            ResourceKind = "table"
	KindView             ResourceKind = "view"
	KindMaterializedView ResourceKind = "materialized_view"
	KindFunction         ResourceKind = "function"
	KindProcedure        ResourceKind = "procedure"
	KindTrigger          ResourceKind = "trigger"
	KindIndex            ResourceKind = "index"
	KindSequence         ResourceKind = "sequence"
)

// ResourceKinds is the display order used by the object browser. Keeping the
// order here means every database gets the same hierarchy without the UI
// knowing which driver is connected.
var ResourceKinds = []ResourceKind{
	KindTable,
	KindView,
	KindMaterializedView,
	KindFunction,
	KindProcedure,
	KindTrigger,
	KindIndex,
	KindSequence,
}

// IsRelation reports whether kind owns columns and can be queried as a table.
func (k ResourceKind) IsRelation() bool {
	switch k {
	case KindTable, KindView, KindMaterializedView:
		return true
	}
	return false
}

// Resource is a catalog object that is not a table or view: a function,
// procedure, trigger, index or sequence. Keeping the common fields together
// lets the tree render new resource kinds without a new row type for each one.
type Resource struct {
	Kind    ResourceKind
	Schema  string
	Name    string
	Owner   string // table or view a trigger/index belongs to
	Detail  string // signature, timing/event, uniqueness, language, …
	Comment string
}

// QualifiedName returns the resource name in schema.name form.
func (r *Resource) QualifiedName() string {
	if r == nil {
		return ""
	}
	if r.Schema == "" {
		return r.Name
	}
	return r.Schema + "." + r.Name
}

// Capabilities is the explicit list of resource kinds a database driver
// supports. A driver fills it during introspection, so the UI never assumes
// that, for example, every connection has stored procedures.
type Capabilities []ResourceKind

// Capable builds a capability list.
func Capable(kinds ...ResourceKind) Capabilities {
	return append(Capabilities(nil), kinds...)
}

// Supports reports whether the capability list contains kind.
func (c Capabilities) Supports(kind ResourceKind) bool {
	for _, k := range c {
		if k == kind {
			return true
		}
	}
	return false
}

// Supports reports whether the catalog says the connected database has the
// given kind. An empty capability list means the driver did not declare one
// (hand-built catalogs and tests); in that case callers should display what is
// actually present instead of hiding it.
func (c *Catalog) Supports(kind ResourceKind) bool {
	if c == nil || len(c.Capabilities) == 0 {
		return true
	}
	return c.Capabilities.Supports(kind)
}

// TablesOf returns the relations of one kind in a schema.
func (s *Schema) TablesOf(kind ResourceKind) []*Table {
	if s == nil {
		return nil
	}
	var out []*Table
	for _, t := range s.Tables {
		if t.Kind() == kind {
			out = append(out, t)
		}
	}
	return out
}

// ResourcesOf returns the non-relation resources of one kind in a schema.
func (s *Schema) ResourcesOf(kind ResourceKind) []*Resource {
	if s == nil {
		return nil
	}
	var out []*Resource
	for _, r := range s.Resources {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// Count returns the number of objects of kind in the schema.
func (s *Schema) Count(kind ResourceKind) int {
	if kind.IsRelation() {
		return len(s.TablesOf(kind))
	}
	return len(s.ResourcesOf(kind))
}

// Kind maps a table type to the shared resource-kind vocabulary.
func (t *Table) Kind() ResourceKind {
	if t == nil {
		return KindTable
	}
	switch t.Type {
	case TypeView:
		return KindView
	case TypeMaterializedView:
		return KindMaterializedView
	default:
		return KindTable
	}
}

// AllResources returns every non-relation resource, sorted by schema and name.
// Tables and views are reached through AllTables and FindTable as before.
func (c *Catalog) AllResources() []*Resource {
	if c == nil {
		return nil
	}
	var out []*Resource
	for _, s := range c.Schemas {
		out = append(out, s.Resources...)
	}
	sortResources(out)
	return out
}

func sortResources(resources []*Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		a, b := resources[i], resources[j]
		if !strings.EqualFold(a.Schema, b.Schema) {
			return strings.ToLower(a.Schema) < strings.ToLower(b.Schema)
		}
		if !strings.EqualFold(a.Name, b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return strings.ToLower(a.Kind.String()) < strings.ToLower(b.Kind.String())
	})
}

// String returns the stable storage/debug name of a kind.
func (k ResourceKind) String() string { return string(k) }
