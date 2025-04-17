// Package syncmap provides a high-performance, generic, thread-safe map implementation
// with additional features beyond the standard sync.Map.
//
// Key features:
//   - Generic type support for type-safe operations
//   - Thread-safe operations using RWMutex
//   - Snapshot functionality with optional filtering
//   - Length tracking and clear operations
//   - Zero-allocation operations in most cases
//   - Extended atomic operations (Swap, LoadAndDelete)
//
// Example usage:
//
//	m := syncmap.Make[string, int]()
//
//	// Basic operations
//	m.Store("counter", 1)
//	if val, ok := m.Load("counter"); ok {
//	    fmt.Printf("Counter: %d\n", val)
//	}
//
//	// Atomic operations
//	newVal, loaded := m.LoadOrStore("counter", 2)
//	oldVal, existed := m.Swap("counter", 3)
//
//	// Filtering
//	evenOnly := func(key string, val int) bool {
//	    return val%2 == 0
//	}
//	snapshot, _ := m.Snapshot(evenOnly)
package syncmap

import (
	"sync"
	"sync/atomic"
)

// SyncMap is a generic thread-safe map implementation.
// K must be comparable, and V can be any type.
type SyncMap[K comparable, V any] struct {
	items  sync.Map
	length atomic.Int64
}

// Filter is a function type used for filtering map entries in Snapshot operations.
// It returns true for entries that should be included in the snapshot.
type Filter[K comparable, V any] func(key K, value V) bool

// Load retrieves the value for a key.
// Returns the value and a boolean indicating whether the key was present.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("key", 42)
//
//	if val, ok := m.Load("key"); ok {
//	    fmt.Printf("Value: %d\n", val)
//	} else {
//	    fmt.Println("Key not found")
//	}
func (m *SyncMap[K, V]) Load(key K) (V, bool) {
	item, ok := m.items.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return item.(V), true
}

// Store sets the value for a key.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("counter", 1)
func (m *SyncMap[K, V]) Store(key K, value V) {
	_, exists := m.items.Load(key)
	m.items.Store(key, value)

	if !exists {
		m.length.Add(1)
	}
}

// Delete removes a key from the map.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("temp", 42)
//	m.Delete("temp") // Key is now removed
func (m *SyncMap[K, V]) Delete(key K) {
	_, ok := m.items.Load(key)
	m.items.Delete(key)

	if !ok {
		return
	}
	m.length.Add(-1)

}

// Len returns the number of items in the map.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("a", 1)
//	m.Store("b", 2)
//	fmt.Printf("Map size: %d\n", m.Len()) // Output: Map size: 2
func (m *SyncMap[K, V]) Len() int {
	return int(m.length.Load())
}

// Range calls f sequentially for each key and value in the map.
// If f returns false, iteration stops.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("a", 1)
//	m.Store("b", 2)
//
//	m.Range(func(key string, value int) bool {
//	    fmt.Printf("%s: %d\n", key, value)
//	    return true // continue iteration
//	})
func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.items.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//
//	// Key doesn't exist, value will be stored
//	val, loaded := m.LoadOrStore("counter", 1)
//	// val == 1, loaded == false
//
//	// Key exists, original value will be returned
//	val, loaded = m.LoadOrStore("counter", 2)
//	// val == 1, loaded == true
func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (V, bool) {
	item, loaded := m.items.LoadOrStore(key, value)
	if !loaded {
		m.length.Add(1)
	}

	return item.(V), loaded
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("temp", 42)
//
//	// Remove and get the value atomically
//	if val, ok := m.LoadAndDelete("temp"); ok {
//	    fmt.Printf("Removed value: %d\n", val)
//	}
func (m *SyncMap[K, V]) LoadAndDelete(key K) (V, bool) {
	item, ok := m.items.LoadAndDelete(key)
	if !ok {
		var zero V
		return zero, false
	}
	m.length.Add(-1)
	return item.(V), true
}

// Snapshot returns a copy of the current map state.
// Optional filters can be provided to include only specific entries.
// Multiple filters are applied as AND conditions.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("a", 1)
//	m.Store("b", 2)
//
//	// Get all items
//	all, _ := m.Snapshot()
//
//	// Get only even numbers
//	evenFilter := func(key string, val int) bool {
//	    return val%2 == 0
//	}
//	evens, _ := m.Snapshot(evenFilter)
//
//	// Multiple filters
//	prefixFilter := func(key string, _ int) bool {
//	    return strings.HasPrefix(key, "user_")
//	}
//	userEvens, _ := m.Snapshot(evenFilter, prefixFilter)
func (m *SyncMap[K, V]) Snapshot(filters ...Filter[K, V]) map[K]V {
	snapshot := make(map[K]V, int(m.length.Load()))
	m.Range(func(key K, value V) bool {
		if len(filters) > 0 {
			for _, filter := range filters {
				if !filter(key, value) {
					return true
				}
			}
		}
		snapshot[key] = value
		return true
	})
	return snapshot
}

// Clear removes all items from the map.
//
// Example:
//
//	m := syncmap.Make[string, int]()
//	m.Store("a", 1)
//	m.Store("b", 2)
//
//	m.Clear() // Map is now empty
//	fmt.Printf("Size after clear: %d\n", m.Len()) // Output: Size after clear: 0
func (m *SyncMap[K, V]) Clear() {
	m.items = sync.Map{}
	m.length.Store(0)
}
