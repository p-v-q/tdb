package ui

// grid_edit_column_case_test.go pins dialect-aware result-column mapping. A
// case-insensitive match is safe only when it identifies one catalog column;
// PostgreSQL identifiers, by contrast, must match exactly.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
)

func columnCaseCatalog(name string, columns ...*catalog.Column) *catalog.Catalog {
	return &catalog.Catalog{
		Dialect: name,
		Schemas: []*catalog.Schema{{
			Name: "main",
			Tables: []*catalog.Table{{
				Schema: "main", Name: "items", Type: catalog.TypeTable,
				Columns: columns,
			}},
		}},
	}
}

func columnCaseOrigin(d dialect.Dialect) resultOrigin {
	conn := newFakeConn()
	conn.dialect = d
	return resultOrigin{conn: connRef{name: "case-test", conn: conn}}
}

// TestGridEditPostgresColumnMappingIsCaseSensitive reproduces the blocker:
// "FOO" used to win over the exact "foo" match merely because it appeared first
// in the catalog, so the generated UPDATE wrote to the wrong column.
func TestGridEditPostgresColumnMappingIsCaseSensitive(t *testing.T) {
	upper := &catalog.Column{Name: "FOO", Type: "TEXT", Ordinal: 2}
	lower := &catalog.Column{Name: "foo", Type: "TEXT", Ordinal: 3}
	cat := columnCaseCatalog("postgres",
		&catalog.Column{Name: "id", Type: "INTEGER", PK: true, Ordinal: 1}, upper, lower)
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "foo"}}
	pg := dialect.MustGet("postgres")

	src, reason := resolveSource(`SELECT id, "foo" FROM items`, cat, cols, columnCaseOrigin(pg))
	if reason != editAllowed || src == nil {
		t.Fatalf("source = %+v, reason = %v, want an editable exact match", src, reason)
	}
	if got := src.cols[1]; got != lower {
		t.Fatalf("foo mapped to %+v, want the exact lowercase catalog column", got)
	}
	text, _ := generateCellUpdate(pg, src.table, src.cols[1], src.pk, gridEditString("new"), []string{"1"})
	want := `UPDATE "main"."items" SET "foo" = $1 WHERE "id" = $2`
	if text != want {
		t.Fatalf("statement = %q, want %q", text, want)
	}

	upperSrc, reason := resolveSource(`SELECT id, "FOO" FROM items`, cat,
		[]driver.ResultColumn{{Name: "id"}, {Name: "FOO"}}, columnCaseOrigin(pg))
	if reason != editAllowed || upperSrc == nil || upperSrc.cols[1] != upper {
		t.Fatalf("FOO source = %+v, reason = %v, want the exact uppercase catalog column", upperSrc, reason)
	}

	// A spelling that is not exact is not mapped to either case variant.
	mixed, reason := resolveSource(`SELECT id, "FoO" FROM items`, cat,
		[]driver.ResultColumn{{Name: "id"}, {Name: "FoO"}}, columnCaseOrigin(pg))
	if reason != editAllowed || mixed == nil {
		t.Fatalf("source = %+v, reason = %v, want an editable key-only result", mixed, reason)
	}
	if got := mixed.cols[1]; got != nil {
		t.Fatalf("FoO mapped to %+v, want no case-folded PostgreSQL match", got)
	}
}

// TestGridEditFoldedColumnMappingRejectsAmbiguity covers MySQL and SQLite: a
// unique case-insensitive match keeps working, but multiple matches must make
// the result read-only and say why instead of choosing the first column.
func TestGridEditFoldedColumnMappingRejectsAmbiguity(t *testing.T) {
	for _, name := range []string{"mysql", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			d := dialect.MustGet(name)
			cat := columnCaseCatalog(name,
				&catalog.Column{Name: "id", Type: "INTEGER", PK: true, Ordinal: 1},
				&catalog.Column{Name: "FOO", Type: "TEXT", Ordinal: 2},
				&catalog.Column{Name: "foo", Type: "TEXT", Ordinal: 3})
			cols := []driver.ResultColumn{{Name: "id"}, {Name: "foo"}}
			origin := columnCaseOrigin(d)

			src, reason := resolveSource("SELECT id, foo FROM items", cat, cols, origin)
			if reason != editBlockedAmbiguousColumn || src != nil {
				t.Fatalf("source = %+v, reason = %v, want ambiguous folded columns refused", src, reason)
			}
			if got := editBlockedAmbiguousColumn.key(); got != i18n.KeyResultNotEditableReasonAmbiguousColumn {
				t.Fatalf("ambiguity key = %q, want the column-ambiguity message", got)
			}
			if got := i18n.New(i18n.ZhHans).T(i18n.KeyResultNotEditableReasonAmbiguousColumn); !strings.Contains(got, "歧义") {
				t.Fatalf("Chinese ambiguity message = %q, want an explicit reason", got)
			}

			m := New(Config{Title: "TDB", Dialect: d, Translator: i18n.New(i18n.En)})
			m.cat = cat
			m.session.result.res = &driver.Result{Columns: cols}
			m.session.result.stmt = "SELECT id, foo FROM items"
			m.session.result.origin = origin
			m.layout.focus = paneResults
			m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
			if m.session.result.editing != nil {
				t.Fatal("an ambiguous folded column must not open the cell editor")
			}
			if !strings.Contains(m.status.message, "ambiguous") {
				t.Fatalf("status = %q, want an explicit column-ambiguity error", m.status.message)
			}
		})
	}
}

// TestGridEditFoldedColumnMappingKeepsUniqueMatches preserves the existing
// MySQL/SQLite behaviour when case folding identifies exactly one column.
func TestGridEditFoldedColumnMappingKeepsUniqueMatches(t *testing.T) {
	for _, name := range []string{"mysql", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			email := &catalog.Column{Name: "Email", Type: "TEXT", Ordinal: 2}
			cat := columnCaseCatalog(name,
				&catalog.Column{Name: "id", Type: "INTEGER", PK: true, Ordinal: 1}, email)
			src, reason := resolveSource("SELECT id, email FROM items", cat,
				[]driver.ResultColumn{{Name: "id"}, {Name: "email"}},
				columnCaseOrigin(dialect.MustGet(name)))
			if reason != editAllowed || src == nil {
				t.Fatalf("source = %+v, reason = %v, want the unique folded match", src, reason)
			}
			if got := src.cols[1]; got != email {
				t.Fatalf("email mapped to %+v, want %+v", got, email)
			}
		})
	}
}
