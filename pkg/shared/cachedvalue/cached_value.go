package cachedvalue

import (
	"sync"
	"time"
)

// CachedValue is a thread-safe, TTL-based cache for a single value.
// It supports lazy fetching and returns stale values on fetch failure.
type CachedValue[T any] struct {
	mu        sync.RWMutex
	value     T
	fetchedAt time.Time
	ttl       time.Duration
	hasValue  bool
}

// New creates a CachedValue with the given TTL.
func New[T any](ttl time.Duration) *CachedValue[T] {
	return &CachedValue[T]{ttl: ttl}
}

// GetOrFetch returns the cached value if fresh, otherwise calls fetch and
// updates the cache. On fetch error, returns stale cached data (if any) along
// with the error. The clone function is applied to the returned value to
// prevent callers from mutating the cache.
func (c *CachedValue[T]) GetOrFetch(force bool, clone func(T) T, fetch func() (T, error)) (T, error) {
	if clone == nil {
		clone = func(v T) T { return v }
	}
	c.mu.RLock()
	if !force && c.hasValue && time.Since(c.fetchedAt) < c.ttl {
		val := clone(c.value)
		c.mu.RUnlock()
		return val, nil
	}
	var stale T
	hasStale := c.hasValue
	if hasStale {
		stale = clone(c.value)
	}
	c.mu.RUnlock()

	fresh, err := fetch()
	if err != nil {
		return stale, err
	}

	c.mu.Lock()
	c.value = fresh
	c.fetchedAt = time.Now()
	c.hasValue = true
	c.mu.Unlock()

	return clone(fresh), nil
}

// Update stores a value and resets the TTL timer.
func (c *CachedValue[T]) Update(value T) {
	c.mu.Lock()
	c.value = value
	c.fetchedAt = time.Now()
	c.hasValue = true
	c.mu.Unlock()
}

// Read returns the cached value without fetching. The clone function is
// applied before returning. If no value has been cached, returns the zero
// value of T.
func (c *CachedValue[T]) Read(clone func(T) T) T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasValue {
		var zero T
		return zero
	}
	if clone == nil {
		return c.value
	}
	return clone(c.value)
}
