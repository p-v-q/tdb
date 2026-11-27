// Package catalog defines the database metadata model: schemas, relations,
// columns, non-relation resources and the capability list each driver declares.
// It also defines the read interfaces.
//
// All of the completion engine's database-aware features originate here: drivers
// introspect a *Catalog, and the UI and completion engine depend only on that
// snapshot, keeping them decoupled from any specific database.
package catalog

import (
	"sort"
	"strings"
	"time"
)

// TableType is a relation type.
type TableType string

// Supported relation types.
const (
	TypeTable            TableType = "table"
	TypeView             TableType = "view"
	TypeMaterializedView TableType = "materialized_view"
)

// Catalog is the complete result of a single introspection.
type Catalog struct {
	Dialect  string
	Schemas  []*Schema
	LoadedAt time.Time
	// Capabilities lists the resource kinds this driver can introspect. It is
	// deliberately data, not an interface: adding a database never adds a
	// switch to the UI.
	Capabilities Capabilities
}

// Schema corresponds to a namespace (a PostgreSQL schema or MySQL database).
type Schema struct {
	Name string
	// Tables holds relations: tables, views and materialized views. Columns and
	// completion use this list.
	Tables []*Table
	// Resources holds the non-relation objects (functions, triggers, indexes,
	// sequences, …) that the driver can introspect.
	Resources []*Resource
}

// Table is a table, view or materialized view.
type Table struct {
	Schema  string
	Name    string
	Type    TableType
	Comment string
	Columns []*Column
}

// Column is the metadata for a single column.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	PK       bool
	Unique   bool
	Default  string
	Comment  string
	Ordinal  int
}

// QualifiedName returns the name in schema.table form.
func (t *Table) QualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// FindSchema looks up a schema by name (case-insensitive).
func (c *Catalog) FindSchema(name string) *Schema {
	if c == nil {
		return nil
	}
	for _, s := range c.Schemas {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

// FindTable looks up a relation by name or schema.name (case-insensitive).
func (c *Catalog) FindTable(name string) *Table {
	if c == nil {
		return nil
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		s := c.FindSchema(name[:i])
		if s == nil {
			return nil
		}
		return findTableIn(s, name[i+1:])
	}
	for _, s := range c.Schemas {
		if t := findTableIn(s, name); t != nil {
			return t
		}
	}
	return nil
}

// FindTableInSchema looks up a relation by schema and name
// (case-insensitive). It is the exact lookup: a name that is not qualified
// does not say which schema it belongs to, and FindTable answers that
// question with the first match it finds.
func (c *Catalog) FindTableInSchema(schema, name string) *Table {
	if c == nil {
		return nil
	}
	s := c.FindSchema(schema)
	if s == nil {
		return nil
	}
	return findTableIn(s, name)
}

// TablesNamed returns every relation with that name, one per schema. It is
// how a caller finds out whether an unqualified name is ambiguous: a server
// resolves such a name through its search path, which is not the same thing
// as "the first table of that name in the catalog".
func (c *Catalog) TablesNamed(name string) []*Table {
	if c == nil {
		return nil
	}
	var out []*Table
	for _, s := range c.Schemas {
		if t := findTableIn(s, name); t != nil {
			out = append(out, t)
		}
	}
	return out
}

func findTableIn(s *Schema, name string) *Table {
	for _, t := range s.Tables {
		if strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return nil
}

// AllTables returns every relation (table, view or materialized view), sorted
// by schema.name.
func (c *Catalog) AllTables() []*Table {
	if c == nil {
		return nil
	}
	out := make([]*Table, 0, 16)
	for _, s := range c.Schemas {
		out = append(out, s.Tables...)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].QualifiedName()) < strings.ToLower(out[j].QualifiedName())
	})
	return out
}

// AllColumns returns every column together with its owning table.
func (c *Catalog) AllColumns() []ColumnRef {
	if c == nil {
		return nil
	}
	out := make([]ColumnRef, 0, 64)
	for _, t := range c.AllTables() {
		for _, col := range t.Columns {
			out = append(out, ColumnRef{Table: t, Column: col})
		}
	}
	return out
}

// ColumnRef pairs a column with the table it belongs to.
type ColumnRef struct {
	Table  *Table
	Column *Column
}
