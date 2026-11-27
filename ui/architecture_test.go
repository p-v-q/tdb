package ui

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestUIImportsNoConcreteDriver enforces the package boundary described in
// docs/architecture.md: the UI programs against driver.Connection, and only
// cmd/tdb/links concrete implementations. Tests may blank-import drivers to
// provide a database; production files may not.
func TestUIImportsNoConcreteDriver(t *testing.T) {
	forbidden := []string{
		"github.com/p-v-q/tdb/driver/demo",
		"github.com/p-v-q/tdb/driver/mysql",
		"github.com/p-v-q/tdb/driver/postgres",
		"github.com/p-v-q/tdb/driver/sqlite",
		"github.com/p-v-q/tdb/cmd/tdb",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %s; link concrete drivers in cmd/tdb instead", name, path)
				}
			}
		}
	}
}
