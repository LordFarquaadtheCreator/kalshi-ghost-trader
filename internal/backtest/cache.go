package backtest

import (
	"sync"
	"time"
)

// cacheEntry holds a cached strategy result with expiry.
type cacheEntry struct {
	result  *StrategyResult
	expires time.Time
}

// Cache provides an in-memory TTL cache for backtest strategy results.
// Keyed by strategy name + minPrice. Thread-safe.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// NewCache creates a backtest result cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

func cacheKey(name string, minPrice float64) string {
	return name + "|" + formatPrice(minPrice)
}

func formatPrice(p float64) string {
	if p == 0 {
		return "0"
	}
	return string([]byte{byte('0' + int(p*100)/10), byte('0' + int(p*100)%10)})
}

// Get returns a cached result if fresh, nil otherwise.
func (c *Cache) Get(name string, minPrice float64) *StrategyResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[cacheKey(name, minPrice)]
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	return entry.result
}

// Put stores a strategy result in the cache.
func (c *Cache) Put(name string, minPrice float64, result *StrategyResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(name, minPrice)] = cacheEntry{
		result:  result,
		expires: time.Now().Add(c.ttl),
	}
}

// Invalidate removes all cached entries.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}

// Cleanup removes expired entries.
func (c *Cache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expires) {
			delete(c.entries, k)
		}
	}
}
