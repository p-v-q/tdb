package ui

import (
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
)

// sqlTestDialect loads a registered dialect for the generator tests.
func sqlTestDialect(t *testing.T, name string) dialect.Dialect {
	t.Helper()
	d, err := dialect.Get(name)
	if err != nil {
		t.Fatalf("dialect %q: %v", name, err)
	}
	return d
}

// sqlTestTable builds the canonical users relation: one primary key, one
// ordinary column. The generators must not care about anything else.
func sqlTestTable(kind catalog.TableType) *catalog.Table {
	return &catalog.Table{
		Schema: "public", Name: "users", Type: kind,
		Columns: []*catalog.Column{
			{Name: "id", Type: "bigint", PK: true},
			{Name: "email", Type: "text"},
		},
	}
}

func TestGenerateSelectQuotesPerDialect(t *testing.T) {
	tests := map[string]string{
		"postgres": `SELECT "id", "email"
FROM "public"."users";`,
		"mysql": "SELECT `id`, `email`\nFROM `public`.`users`;",
		"sqlite": `SELECT "id", "email"
FROM "public"."users";`,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := generateSelect(sqlTestDialect(t, name), sqlTestTable(catalog.TypeTable)); got != want {
				t.Fatalf("generateSelect = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerateInsertPlaceholdersPerDialect(t *testing.T) {
	tests := map[string]string{
		"postgres": `INSERT INTO "public"."users" ("id", "email")
VALUES ($1, $2);`,
		"mysql": "INSERT INTO `public`.`users` (`id`, `email`)\nVALUES (?, ?);",
		"sqlite": `INSERT INTO "public"."users" ("id", "email")
VALUES (?, ?);`,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := generateInsert(sqlTestDialect(t, name), sqlTestTable(catalog.TypeTable)); got != want {
				t.Fatalf("generateInsert = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerateUpdateUsesPrimaryKey(t *testing.T) {
	tests := map[string]string{
		"postgres": `UPDATE "public"."users"
SET "email" = $1
WHERE "id" = $2;`,
		"mysql": "UPDATE `public`.`users`\nSET `email` = ?\nWHERE `id` = ?;",
		"sqlite": `UPDATE "public"."users"
SET "email" = ?
WHERE "id" = ?;`,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := generateUpdate(sqlTestDialect(t, name), sqlTestTable(catalog.TypeTable)); got != want {
				t.Fatalf("generateUpdate = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerateUpdateOnlyPrimaryKeyColumnsFallsBackToThemForSet(t *testing.T) {
	table := &catalog.Table{
		Schema: "public", Name: "memberships", Type: catalog.TypeTable,
		Columns: []*catalog.Column{
			{Name: "user_id", PK: true},
			{Name: "group_id", PK: true},
		},
	}
	want := `UPDATE "public"."memberships"
SET "user_id" = $1, "group_id" = $2
WHERE "user_id" = $3 AND "group_id" = $4;`
	if got := generateUpdate(sqlTestDialect(t, "postgres"), table); got != want {
		t.Fatalf("generateUpdate = %q, want %q", got, want)
	}
}

func TestGenerateUpdateWithoutPrimaryKeyWarnsAndUsesAllColumns(t *testing.T) {
	table := &catalog.Table{
		Schema: "public", Name: "events", Type: catalog.TypeTable,
		Columns: []*catalog.Column{{Name: "a"}, {Name: "b"}},
	}
	tests := map[string]string{
		"postgres": noPrimaryKeyWarning + `
UPDATE "public"."events"
SET "a" = $1, "b" = $2
WHERE "a" = $3 AND "b" = $4;`,
		"mysql": noPrimaryKeyWarning + "\nUPDATE `public`.`events`\nSET `a` = ?, `b` = ?\nWHERE `a` = ? AND `b` = ?;",
		"sqlite": noPrimaryKeyWarning + `
UPDATE "public"."events"
SET "a" = ?, "b" = ?
WHERE "a" = ? AND "b" = ?;`,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := generateUpdate(sqlTestDialect(t, name), table); got != want {
				t.Fatalf("generateUpdate = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerateDeleteUsesPrimaryKey(t *testing.T) {
	tests := map[string]string{
		"postgres": `DELETE FROM "public"."users"
WHERE "id" = $1;`,
		"mysql": "DELETE FROM `public`.`users`\nWHERE `id` = ?;",
		"sqlite": `DELETE FROM "public"."users"
WHERE "id" = ?;`,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := generateDelete(sqlTestDialect(t, name), sqlTestTable(catalog.TypeTable)); got != want {
				t.Fatalf("generateDelete = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerateDeleteWithoutPrimaryKeyWarns(t *testing.T) {
	table := &catalog.Table{
		Schema: "public", Name: "events", Type: catalog.TypeTable,
		Columns: []*catalog.Column{{Name: "a"}, {Name: "b"}},
	}
	want := noPrimaryKeyWarning + "\nDELETE FROM \"public\".\"events\"\nWHERE \"a\" = $1 AND \"b\" = $2;"
	if got := generateDelete(sqlTestDialect(t, "postgres"), table); got != want {
		t.Fatalf("generateDelete = %q, want %q", got, want)
	}
}

func TestGenerateDeleteWithCompositePrimaryKey(t *testing.T) {
	table := &catalog.Table{
		Schema: "public", Name: "memberships", Type: catalog.TypeTable,
		Columns: []*catalog.Column{{Name: "user_id", PK: true}, {Name: "group_id", PK: true}},
	}
	want := "DELETE FROM `public`.`memberships`\nWHERE `user_id` = ? AND `group_id` = ?;"
	if got := generateDelete(sqlTestDialect(t, "mysql"), table); got != want {
		t.Fatalf("generateDelete = %q, want %q", got, want)
	}
}

func TestGenerateViewDataIsBounded(t *testing.T) {
	want := `SELECT * FROM "public"."users" LIMIT 200;`
	if got := generateViewData(sqlTestDialect(t, "postgres"), sqlTestTable(catalog.TypeTable)); got != want {
		t.Fatalf("generateViewData = %q, want %q", got, want)
	}
	if tableDataLimit != 200 {
		t.Fatalf("tableDataLimit = %d, want 200", tableDataLimit)
	}
}

func TestGenerateEmptyColumnBoundaries(t *testing.T) {
	table := &catalog.Table{Schema: "public", Name: "empty", Type: catalog.TypeTable}
	d := sqlTestDialect(t, "postgres")
	if got, want := generateSelect(d, table), "SELECT *\nFROM \"public\".\"empty\";"; got != want {
		t.Fatalf("generateSelect = %q, want %q", got, want)
	}
	if got, want := generateViewData(d, table), "SELECT * FROM \"public\".\"empty\" LIMIT 200;"; got != want {
		t.Fatalf("generateViewData = %q, want %q", got, want)
	}
	if got, want := generateInsert(d, table), "INSERT INTO \"public\".\"empty\" DEFAULT VALUES;"; got != want {
		t.Fatalf("generateInsert = %q, want %q", got, want)
	}
	if got := generateUpdate(d, table); got != "" {
		t.Fatalf("generateUpdate without columns = %q, want empty", got)
	}
	if got := generateDelete(d, table); got != "" {
		t.Fatalf("generateDelete without columns = %q, want empty", got)
	}
}

func TestGenerateWithoutSchemaQualifiesOnlyTheTable(t *testing.T) {
	table := &catalog.Table{Name: "users", Type: catalog.TypeTable, Columns: []*catalog.Column{{Name: "id"}}}
	if got, want := generateSelect(sqlTestDialect(t, "postgres"), table), "SELECT \"id\"\nFROM \"users\";"; got != want {
		t.Fatalf("generateSelect = %q, want %q", got, want)
	}
}

func TestGenerateWritesAreNotOfferedForViews(t *testing.T) {
	for _, kind := range []catalog.TableType{catalog.TypeView, catalog.TypeMaterializedView} {
		table := sqlTestTable(kind)
		d := sqlTestDialect(t, "postgres")
		if got := generateSelect(d, table); got == "" {
			t.Fatalf("%s must still generate SELECT", kind)
		}
		if got := generateViewData(d, table); got == "" {
			t.Fatalf("%s must still generate view data", kind)
		}
		for name, generate := range map[string]func(dialect.Dialect, *catalog.Table) string{
			"insert": generateInsert, "update": generateUpdate, "delete": generateDelete,
		} {
			if got := generate(d, table); got != "" {
				t.Fatalf("generate %s for %s = %q, want empty", name, kind, got)
			}
		}
	}
}

func TestGenerateNilInputsReturnEmpty(t *testing.T) {
	d := sqlTestDialect(t, "postgres")
	for name, generate := range map[string]func(dialect.Dialect, *catalog.Table) string{
		"select": generateSelect, "insert": generateInsert, "update": generateUpdate,
		"delete": generateDelete, "viewData": generateViewData,
	} {
		if got := generate(d, nil); got != "" {
			t.Fatalf("generate %s(nil table) = %q, want empty", name, got)
		}
		if got := generate(nil, sqlTestTable(catalog.TypeTable)); got != "" {
			t.Fatalf("generate %s(nil dialect) = %q, want empty", name, got)
		}
	}
}
