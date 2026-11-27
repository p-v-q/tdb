package dialect

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds all registered dialects.
var registry = struct {
	sync.RWMutex
	byName map[string]Dialect
}{byName: map[string]Dialect{}}

// Register registers a dialect (overwriting any dialect with the same name).
func Register(d Dialect) {
	registry.Lock()
	defer registry.Unlock()
	registry.byName[d.Name()] = d
}

// Get returns the dialect with the given name, or an error if it does not exist.
func Get(name string) (Dialect, error) {
	registry.RLock()
	defer registry.RUnlock()
	d, ok := registry.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown dialect %q; available: %v", name, namesLocked())
	}
	return d, nil
}

// MustGet is like Get but panics when the dialect is missing (used for built-in
// dialects).
func MustGet(name string) Dialect {
	d, err := Get(name)
	if err != nil {
		panic(err)
	}
	return d
}

// Names returns all registered dialect names (sorted).
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()
	return namesLocked()
}

func namesLocked() []string {
	out := make([]string, 0, len(registry.byName))
	for name := range registry.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func init() {
	Register(NewPostgres())
	Register(NewMySQL())
	Register(NewSQLite())
}
