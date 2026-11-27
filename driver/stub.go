package driver

import (
	"context"
	"fmt"
)

// stubFactory is a placeholder for a driver name that is reserved but not
// implemented yet: asking for it produces an actionable error instead of
// "unknown driver".
type stubFactory struct{ name string }

// Name implements Factory.
func (s stubFactory) Name() string { return s.name }

// Open implements Factory.
func (s stubFactory) Open(context.Context, Config) (Connection, error) {
	return nil, fmt.Errorf("driver %q is not implemented yet; see \"Adding a database\" in the README: %w", s.name, ErrNotImplemented)
}

func init() {
	// Reserved names. Imported packages are initialised after this one, so a
	// real driver package (driver/sqlite, driver/postgres, driver/demo, …)
	// always replaces its placeholder once the binary links it in.
	for _, name := range []string{"postgres", "mysql", "mariadb", "sqlite", "sqlserver"} {
		Register(stubFactory{name: name})
	}
}
