package driver

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// registry holds all registered driver factories.
var registry = struct {
	sync.RWMutex
	byName map[string]Factory
}{byName: map[string]Factory{}}

// Register registers a driver factory (overwriting any factory with the same name).
func Register(f Factory) {
	registry.Lock()
	defer registry.Unlock()
	registry.byName[f.Name()] = f
}

// Get returns the driver factory with the given name.
func Get(name string) (Factory, error) {
	registry.RLock()
	defer registry.RUnlock()
	f, ok := registry.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown driver %q; available: %v", name, namesLocked())
	}
	return f, nil
}

// Names returns all registered driver names (sorted).
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

// Implemented reports whether the named driver is a real one. The registry also
// holds placeholders for the databases that are planned, and the CLI uses this
// to tell "available" from "not yet".
func Implemented(name string) bool {
	f, err := Get(name)
	if err != nil {
		return false
	}
	_, isStub := f.(stubFactory)
	return !isStub
}

// Open creates a connection using the named driver.
func Open(ctx context.Context, name string, cfg Config) (Connection, error) {
	f, err := Get(name)
	if err != nil {
		return nil, err
	}
	cfg.Name = name
	return f.Open(ctx, cfg)
}
