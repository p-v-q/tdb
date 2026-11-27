package ui

// sqlgen.go builds starter SQL for a relation picked in the object browser.
//
// The generators are pure functions of a dialect and a catalog table: they hold
// no Model state, so the context menu can stay a thin dispatcher and every
// dialect can be tested without a terminal. They only produce templates — no
// statement is executed here. Identifiers always go through
// dialect.QuoteIdent; placeholders are $1..$n for PostgreSQL and ? for the
// other dialects, which keeps dialect.Dialect free of a placeholder method.

import (
	"strconv"
	"strings"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
)

// tableDataLimit bounds the rows "View data" fetches. A table can be huge, so
// opening it must not pull the whole relation into the result grid.
const tableDataLimit = 200

// noPrimaryKeyWarning heads generated UPDATE/DELETE statements for a relation
// without a primary key. The SQL text stays English on purpose: the statement
// is meant to be run outside tdb and must not depend on the UI language.
const noPrimaryKeyWarning = "-- no primary key: the WHERE clause matches every column; review it before running"

// generateSelect lists every column of the relation.
func generateSelect(d dialect.Dialect, t *catalog.Table) string {
	if d == nil || t == nil {
		return ""
	}
	cols := quotedColumnNames(d, relationColumns(t))
	list := "*"
	if len(cols) > 0 {
		list = strings.Join(cols, ", ")
	}
	return "SELECT " + list + "\nFROM " + qualifiedRelation(d, t) + ";"
}

// generateInsert inserts one row with a placeholder per column.
func generateInsert(d dialect.Dialect, t *catalog.Table) string {
	if d == nil || !writableRelation(t) {
		return ""
	}
	target := qualifiedRelation(d, t)
	cols := quotedColumnNames(d, relationColumns(t))
	if len(cols) == 0 {
		return "INSERT INTO " + target + " DEFAULT VALUES;"
	}
	values := make([]string, len(cols))
	for i := range cols {
		values[i] = placeholder(d, i+1)
	}
	return "INSERT INTO " + target + " (" + strings.Join(cols, ", ") + ")\n" +
		"VALUES (" + strings.Join(values, ", ") + ");"
}

// generateUpdate updates every non-primary-key column and matches the row on
// the primary key. Without a primary key the WHERE clause has to fall back to
// every column, which may match more than one row; the SQL starts with a
// warning so that is impossible to miss. If the relation is all primary key,
// the SET list falls back to those columns.
func generateUpdate(d dialect.Dialect, t *catalog.Table) string {
	if d == nil || !writableRelation(t) {
		return ""
	}
	cols := relationColumns(t)
	if len(cols) == 0 {
		return ""
	}
	pk := primaryKeyColumns(cols)
	set, where := cols, pk
	if len(pk) > 0 && len(pk) < len(cols) {
		set = nonPrimaryKeyColumns(cols)
	}
	if len(pk) == 0 {
		where = cols // the warning above explains the consequence
	}

	var b strings.Builder
	if len(pk) == 0 {
		b.WriteString(noPrimaryKeyWarning)
		b.WriteByte('\n')
	}
	b.WriteString("UPDATE ")
	b.WriteString(qualifiedRelation(d, t))
	b.WriteString("\nSET ")
	n := 0
	for i, col := range set {
		if i > 0 {
			b.WriteString(", ")
		}
		n++
		b.WriteString(d.QuoteIdent(col.Name))
		b.WriteString(" = ")
		b.WriteString(placeholder(d, n))
	}
	b.WriteString("\nWHERE ")
	b.WriteString(whereClause(d, where, &n))
	b.WriteByte(';')
	return b.String()
}

// generateCellUpdate builds the UPDATE that changes one cell of one row.
//
// The value and the key are arguments, never text: the caller binds them, so
// nothing the user typed can end up inside the statement, and a value with a
// quote in it cannot break the SQL. The primary key addresses the row, so the
// statement touches exactly one row of exactly one table.
//
// A primary-key column is never set: the key is what addresses the row, and
// rewriting it would move the change to another row (or to none). A database
// that coerces the value silently — MySQL turns "42abc" into 42 — could
// even land on a row the user never looked at. Such a column produces no
// statement at all.
//
// The values are sent as text, because that is what a result grid holds. The
// database converts them to the type of the column, and its error message is
// what the user sees when the text does not fit.
func generateCellUpdate(d dialect.Dialect, t *catalog.Table, setCol *catalog.Column, pk []*catalog.Column, value *string, keys []string) (string, []any) {
	if d == nil || t == nil || setCol == nil || len(pk) == 0 || len(keys) != len(pk) {
		return "", nil
	}
	if setCol.PK {
		return "", nil // the key is not editable
	}
	var b strings.Builder
	b.WriteString("UPDATE ")
	b.WriteString(qualifiedRelation(d, t))
	b.WriteString(" SET ")
	b.WriteString(d.QuoteIdent(setCol.Name))
	b.WriteString(" = ")
	b.WriteString(placeholder(d, 1))
	b.WriteString(" WHERE ")

	args := make([]any, 0, len(pk)+1)
	if value == nil {
		args = append(args, nil) // SQL NULL
	} else {
		args = append(args, *value)
	}
	parts := make([]string, 0, len(pk))
	n := 1
	for i, c := range pk {
		n++
		parts = append(parts, d.QuoteIdent(c.Name)+" = "+placeholder(d, n))
		args = append(args, keys[i])
	}
	b.WriteString(strings.Join(parts, " AND "))
	return b.String(), args
}

// generateDelete deletes the rows matching the primary key, or every column
// when there is no primary key (again preceded by the warning).
func generateDelete(d dialect.Dialect, t *catalog.Table) string {
	if d == nil || !writableRelation(t) {
		return ""
	}
	cols := relationColumns(t)
	if len(cols) == 0 {
		return ""
	}
	pk := primaryKeyColumns(cols)
	where := pk
	var b strings.Builder
	if len(pk) == 0 {
		where = cols
		b.WriteString(noPrimaryKeyWarning)
		b.WriteByte('\n')
	}
	b.WriteString("DELETE FROM ")
	b.WriteString(qualifiedRelation(d, t))
	b.WriteString("\nWHERE ")
	n := 0
	b.WriteString(whereClause(d, where, &n))
	b.WriteByte(';')
	return b.String()
}

// generateViewData is the bounded "show me the rows" query: it always selects
// * so it also works for a relation whose columns are not known yet.
func generateViewData(d dialect.Dialect, t *catalog.Table) string {
	if d == nil || t == nil {
		return ""
	}
	return "SELECT * FROM " + qualifiedRelation(d, t) +
		" LIMIT " + strconv.Itoa(tableDataLimit) + ";"
}

// writableRelation reports whether a relation accepts INSERT/UPDATE/DELETE.
// Views and materialized views are read-only in the generated templates; an
// empty Type (a hand-built catalog in tests) is treated as a table.
func writableRelation(t *catalog.Table) bool {
	return t != nil && t.Type != catalog.TypeView && t.Type != catalog.TypeMaterializedView
}

// qualifiedRelation quotes schema.table, or just table when the schema is
// empty (a driver without namespaces).
func qualifiedRelation(d dialect.Dialect, t *catalog.Table) string {
	name := d.QuoteIdent(t.Name)
	if t.Schema == "" {
		return name
	}
	return d.QuoteIdent(t.Schema) + "." + name
}

// relationColumns drops nil column entries so a partially built catalog cannot
// panic the generators.
func relationColumns(t *catalog.Table) []*catalog.Column {
	if t == nil {
		return nil
	}
	out := make([]*catalog.Column, 0, len(t.Columns))
	for _, c := range t.Columns {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// quotedColumnNames quotes a list of columns for a SELECT/SET list.
func quotedColumnNames(d dialect.Dialect, cols []*catalog.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, d.QuoteIdent(c.Name))
	}
	return out
}

// primaryKeyColumns returns the columns flagged as primary key, in catalog
// order (which is ordinal order from introspection).
func primaryKeyColumns(cols []*catalog.Column) []*catalog.Column {
	out := make([]*catalog.Column, 0, len(cols))
	for _, c := range cols {
		if c.PK {
			out = append(out, c)
		}
	}
	return out
}

// nonPrimaryKeyColumns returns every column that is not part of the primary
// key, in catalog order.
func nonPrimaryKeyColumns(cols []*catalog.Column) []*catalog.Column {
	out := make([]*catalog.Column, 0, len(cols))
	for _, c := range cols {
		if !c.PK {
			out = append(out, c)
		}
	}
	return out
}

// whereClause renders "col = placeholder AND ..." and advances *n, so the
// placeholders continue the numbering started by the SET list.
func whereClause(d dialect.Dialect, cols []*catalog.Column, n *int) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		*n = *n + 1
		parts = append(parts, d.QuoteIdent(c.Name)+" = "+placeholder(d, *n))
	}
	return strings.Join(parts, " AND ")
}

// placeholder returns the dialect's positional placeholder. PostgreSQL numbers
// them; MySQL/MariaDB and SQLite accept a plain ? and do not care about order.
func placeholder(d dialect.Dialect, n int) string {
	if strings.EqualFold(d.Name(), "postgres") {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}
