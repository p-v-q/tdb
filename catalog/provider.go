package catalog

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Provider supplies catalog data. Once a driver implements this interface, its
// connection can be used directly as a completion data source.
type Provider interface {
	// Catalog returns the current catalog snapshot; implementations may trigger
	// introspection.
	Catalog(ctx context.Context) (*Catalog, error)
}

// Static is a Provider with fixed content, used for tests and demos.
type Static struct {
	C *Catalog
}

// Catalog implements Provider.
func (s Static) Catalog(context.Context) (*Catalog, error) { return s.C, nil }

// Cache adds a TTL cache around a Provider so that not every completion triggers
// an introspection.
type Cache struct {
	inner Provider
	ttl   time.Duration

	mu       sync.RWMutex
	cached   *Catalog
	loadTime time.Time
}

// NewCache wraps provider; a ttl of 0 means never refresh (fixed after the first
// load).
func NewCache(inner Provider, ttl time.Duration) *Cache {
	return &Cache{inner: inner, ttl: ttl}
}

// Catalog implements Provider.
func (c *Cache) Catalog(ctx context.Context) (*Catalog, error) {
	c.mu.RLock()
	cached, loadedAt := c.cached, c.loadTime
	c.mu.RUnlock()

	if cached != nil && (c.ttl == 0 || time.Since(loadedAt) < c.ttl) {
		return cached, nil
	}

	cat, err := c.inner.Catalog(ctx)
	if err != nil {
		// Fall back to the previous snapshot if introspection fails (better than
		// nothing).
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}
	c.mu.Lock()
	c.cached, c.loadTime = cat, time.Now()
	c.mu.Unlock()
	return cat, nil
}

// Invalidate expires the cache; the next access re-introspects.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}
