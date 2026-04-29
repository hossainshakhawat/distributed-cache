// Package localcache provides a simple in-process LRU cache for hot keys.
package localcache

import (
	"sync"
	"time"
)

type entry struct {
	value     []byte
	expiresAt time.Time
}

// Cache is a small, fixed-capacity in-process cache.
type Cache struct {
	mu      sync.Mutex
	items   map[string]*entry
	maxKeys int
}

// New creates a local in-process cache.
func New(maxKeys int) *Cache {
	if maxKeys <= 0 {
		maxKeys = 512
	}
	return &Cache{items: make(map[string]*entry, maxKeys), maxKeys: maxKeys}
}

// Get retrieves a value from the local cache.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(c.items, key)
		return nil, false
	}
	return e.value, true
}

// Set stores a value with the given TTL.
func (c *Cache) Set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxKeys {
		// Evict a random key.
		for k := range c.items {
			delete(c.items, k)
			break
		}
	}
	e := &entry{value: value}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	c.items[key] = e
}

// Delete removes a key from the local cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}
