package connector

import (
	"testing"
	"time"
)

func TestDedupeCache_FirstCheck(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 100)

	// First check should return false (not a duplicate)
	if cache.Check("key1") {
		t.Error("First check should not be a duplicate")
	}
}

func TestDedupeCache_SecondCheck(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 100)

	// First check
	cache.Check("key1")

	// Second check should return true (duplicate)
	if !cache.Check("key1") {
		t.Error("Second check should be a duplicate")
	}
}

func TestDedupeCache_DifferentKeys(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 100)

	cache.Check("key1")

	// Different key should not be a duplicate
	if cache.Check("key2") {
		t.Error("Different key should not be a duplicate")
	}
}

func TestDedupeCache_EmptyKey(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 100)

	// Empty key should always return false
	if cache.Check("") {
		t.Error("Empty key should not be a duplicate")
	}
	if cache.Check("") {
		t.Error("Empty key should still not be a duplicate")
	}
}

func TestDedupeCache_TTLExpiry(t *testing.T) {
	// Very short TTL for testing
	cache := NewDedupeCache(10*time.Millisecond, 100)

	cache.Check("key1")

	// Should be duplicate immediately
	if !cache.Check("key1") {
		t.Error("Should be duplicate before TTL expires")
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Should not be duplicate after TTL
	if cache.Check("key1") {
		t.Error("Should not be duplicate after TTL expires")
	}
}

func TestDedupeCache_MaxSize(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 3)

	// Add 3 entries
	cache.Check("key1")
	cache.Check("key2")
	cache.Check("key3")

	if cache.Size() != 3 {
		t.Errorf("Expected size 3, got %d", cache.Size())
	}

	// Add 4th entry, should evict oldest (key1)
	cache.Check("key4")

	if cache.Size() != 3 {
		t.Errorf("Expected size 3 after eviction, got %d", cache.Size())
	}

	// key1 should no longer be a duplicate (was evicted)
	// Note: Checking key1 will re-add it, evicting key2 in the process
	if cache.Check("key1") {
		t.Error("key1 should have been evicted")
	}

	// Now cache contains: key3, key4, key1 (key2 was evicted when key1 was re-added)
	// key3, key4 should still be duplicates
	if !cache.Check("key3") {
		t.Error("key3 should still be a duplicate")
	}
	if !cache.Check("key4") {
		t.Error("key4 should still be a duplicate")
	}
	// key1 should now be a duplicate (was re-added)
	if !cache.Check("key1") {
		t.Error("key1 should now be a duplicate after re-add")
	}
}

func TestDedupeCache_Clear(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 100)

	cache.Check("key1")
	cache.Check("key2")

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", cache.Size())
	}

	// Keys should no longer be duplicates
	if cache.Check("key1") {
		t.Error("key1 should not be a duplicate after clear")
	}
}

func TestDedupeCache_DefaultValues(t *testing.T) {
	// Test with zero/negative values - should use defaults
	cache := NewDedupeCache(0, 0)

	if cache.ttl != DefaultDedupeTTL {
		t.Errorf("Expected default TTL %v, got %v", DefaultDedupeTTL, cache.ttl)
	}
	if cache.maxSize != DefaultDedupeMaxSize {
		t.Errorf("Expected default maxSize %d, got %d", DefaultDedupeMaxSize, cache.maxSize)
	}
}

func TestDedupeCache_Concurrent(t *testing.T) {
	cache := NewDedupeCache(time.Minute, 1000)

	done := make(chan bool)

	// Run concurrent checks
	for i := range 10 {
		go func(id int) {
			for range 100 {
				key := "key" + string(rune('A'+id))
				cache.Check(key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}

	// Should have 10 unique keys
	if cache.Size() != 10 {
		t.Errorf("Expected size 10, got %d", cache.Size())
	}
}
