package syncmap_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/runpod/hsm/syncmap"
)

func TestSyncMap(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Test Set and Get
		m.Store("a", 1)
		if val, ok := m.Load("a"); !ok || val != 1 {
			t.Errorf("expected 1, got %v", val)
		}

		// Test non-existent key
		if _, ok := m.Load("b"); ok {
			t.Error("expected false for non-existent key")
		}

		// Test Delete
		m.Delete("a")
		if _, ok := m.Load("a"); ok {
			t.Error("expected key to be deleted")
		}

		// Test Len
		m.Store("a", 1)
		m.Store("b", 2)
		if m.Len() != 2 {
			t.Errorf("expected length 2, got %d", m.Len())
		}

		// Test Snapshot
		snapshot := m.Snapshot()
		if len(snapshot) != 2 {
			t.Errorf("expected snapshot length 2, got %d", len(snapshot))
		}
		if snapshot["a"] != 1 || snapshot["b"] != 2 {
			t.Error("snapshot values don't match")
		}
	})

	t.Run("filtered snapshot", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Populate the map with test data
		m.Store("a", 1)
		m.Store("b", 2)
		m.Store("c", 3)
		m.Store("d", 4)

		// Test filtering even numbers
		evenFilter := func(key string, value int) bool {
			return value%2 == 0
		}
		evenSnapshot := m.Snapshot(evenFilter)
		if len(evenSnapshot) != 2 {
			t.Errorf("expected 2 even numbers, got %d", len(evenSnapshot))
		}
		if evenSnapshot["b"] != 2 || evenSnapshot["d"] != 4 {
			t.Error("even snapshot values don't match")
		}

		// Test filtering by key prefix
		prefixFilter := func(key string, value int) bool {
			return key[0] == 'a' || key[0] == 'b'
		}
		prefixSnapshot := m.Snapshot(prefixFilter)
		if len(prefixSnapshot) != 2 {
			t.Errorf("expected 2 keys with prefix a or b, got %d", len(prefixSnapshot))
		}
		if prefixSnapshot["a"] != 1 || prefixSnapshot["b"] != 2 {
			t.Error("prefix snapshot values don't match")
		}

		// Test empty filter (should return all items)
		emptySnapshot := m.Snapshot()
		if len(emptySnapshot) != 4 {
			t.Errorf("expected all 4 items, got %d", len(emptySnapshot))
		}

		// Test filter that excludes everything
		excludeAllFilter := func(key string, value int) bool {
			return false
		}
		emptyFilterSnapshot := m.Snapshot(excludeAllFilter)
		if len(emptyFilterSnapshot) != 0 {
			t.Errorf("expected 0 items, got %d", len(emptyFilterSnapshot))
		}
	})

	t.Run("concurrent load or store", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Test LoadOrStore when key doesn't exist
		val, loaded := m.LoadOrStore("x", 10)
		if loaded || val != 10 {
			t.Errorf("expected val=10 and loaded=false, got val=%v, loaded=%v", val, loaded)
		}

		// Test LoadOrStore when key exists
		val, loaded = m.LoadOrStore("x", 20)
		if !loaded || val != 10 {
			t.Errorf("expected val=10 and loaded=true, got val=%v, loaded=%v", val, loaded)
		}
	})

	t.Run("load and delete", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Test LoadAndDelete on non-existent key
		val, loaded := m.LoadAndDelete("x")
		if loaded || val != 0 {
			t.Errorf("expected val=0 and loaded=false for non-existent key, got val=%v, loaded=%v", val, loaded)
		}

		// Test LoadAndDelete on existing key
		m.Store("x", 42)
		val, loaded = m.LoadAndDelete("x")
		if !loaded || val != 42 {
			t.Errorf("expected val=42 and loaded=true, got val=%v, loaded=%v", val, loaded)
		}

		// Verify key was deleted
		if _, ok := m.Load("x"); ok {
			t.Error("expected key to be deleted after LoadAndDelete")
		}
	})

	t.Run("range operations", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}
		testData := map[string]int{
			"a": 1,
			"b": 2,
			"c": 3,
		}

		// Populate the map
		for k, v := range testData {
			m.Store(k, v)
		}

		// Test Range
		visited := make(map[string]int)
		m.Range(func(key string, value int) bool {
			visited[key] = value
			return true
		})

		if len(visited) != len(testData) {
			t.Errorf("expected to visit %d items, visited %d", len(testData), len(visited))
		}
		for k, v := range testData {
			if visited[k] != v {
				t.Errorf("expected visited[%s]=%d, got %d", k, v, visited[k])
			}
		}

		// Test Range with early termination
		count := 0
		m.Range(func(key string, value int) bool {
			count++
			return false // stop after first item
		})
		if count != 1 {
			t.Errorf("expected Range to stop after 1 item, continued for %d items", count)
		}
	})

	t.Run("clear operation", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Populate the map
		m.Store("a", 1)
		m.Store("b", 2)
		m.Store("c", 3)

		// Test Clear
		m.Clear()
		if m.Len() != 0 {
			t.Errorf("expected length 0 after Clear, got %d", m.Len())
		}

		// Verify all keys are gone
		if _, ok := m.Load("a"); ok {
			t.Error("expected key 'a' to be deleted after Clear")
		}
	})

	t.Run("length tracking accuracy", func(t *testing.T) {
		m := syncmap.SyncMap[string, int]{}

		// Test empty map
		if m.Len() != 0 {
			t.Errorf("expected length 0 for empty map, got %d", m.Len())
		}

		// Test length after adding items
		m.Store("a", 1)
		if m.Len() != 1 {
			t.Errorf("expected length 1 after adding one item, got %d", m.Len())
		}

		m.Store("b", 2)
		m.Store("c", 3)
		if m.Len() != 3 {
			t.Errorf("expected length 3 after adding three items, got %d", m.Len())
		}

		// Test length after overwriting an existing key
		m.Store("a", 4) // Overwrite existing key
		if m.Len() != 3 {
			t.Errorf("expected length to remain 3 after overwriting, got %d", m.Len())
		}

		// Test length after deleting items
		m.Delete("a")
		if m.Len() != 2 {
			t.Errorf("expected length 2 after deleting one item, got %d", m.Len())
		}

		// Test length after attempting to delete a non-existent key
		m.Delete("non-existent")
		if m.Len() != 2 {
			t.Errorf("expected length to remain 2 after deleting non-existent key, got %d", m.Len())
		}

		// Test length with LoadOrStore
		val, loaded := m.LoadOrStore("d", 4) // New key
		if !loaded && m.Len() != 3 {
			t.Errorf("expected length 3 after LoadOrStore of new key, got %d", m.Len())
		}

		val, loaded = m.LoadOrStore("d", 5) // Existing key
		if loaded && val != 4 {
			t.Errorf("expected LoadOrStore to return existing value 4, got %v", val)
		}
		if m.Len() != 3 {
			t.Errorf("expected length to remain 3 after LoadOrStore of existing key, got %d", m.Len())
		}

		// Test length with LoadAndDelete
		val, ok := m.LoadAndDelete("c")
		if !ok || val != 3 {
			t.Errorf("expected LoadAndDelete to return 3, got %v (ok: %v)", val, ok)
		}
		if m.Len() != 2 {
			t.Errorf("expected length 2 after LoadAndDelete, got %d", m.Len())
		}

		// Test Clear operation
		m.Clear()
		if m.Len() != 0 {
			t.Errorf("expected length 0 after Clear, got %d", m.Len())
		}

		// Test with multiple operations
		m.Store("x", 1)
		m.Store("y", 2)
		m.Store("z", 3)
		m.Delete("y")
		m.Store("w", 4)
		if m.Len() != 3 {
			t.Errorf("expected length 3 after multiple operations, got %d", m.Len())
		}
	})

}

func BenchmarkMaps(b *testing.B) {
	b.Run("store", func(b *testing.B) {
		b.Run("syncmap", func(b *testing.B) {
			m := syncmap.SyncMap[int, int]{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Store(i, i)
			}
		})

		b.Run("sync.Map", func(b *testing.B) {
			var m sync.Map
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Store(i, i)
			}
		})
	})

	b.Run("load", func(b *testing.B) {
		b.Run("syncmap", func(b *testing.B) {
			m := syncmap.SyncMap[int, int]{}
			for i := 0; i < 1000; i++ {
				m.Store(i, i)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Load(i % 1000)
			}
		})

		b.Run("sync.Map", func(b *testing.B) {
			var m sync.Map
			for i := 0; i < 1000; i++ {
				m.Store(i, i)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Load(i % 1000)
			}
		})
	})

	b.Run("load_or_store", func(b *testing.B) {
		b.Run("syncmap", func(b *testing.B) {
			m := syncmap.SyncMap[int, int]{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.LoadOrStore(i%100, i)
			}
		})

		b.Run("sync.Map", func(b *testing.B) {
			var m sync.Map
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.LoadOrStore(i%100, i)
			}
		})
	})

	b.Run("delete", func(b *testing.B) {
		b.Run("syncmap", func(b *testing.B) {
			m := syncmap.SyncMap[int, int]{}
			for i := 0; i < 1000; i++ {
				m.Store(i, i)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Delete(i % 1000)
			}
		})

		b.Run("sync.Map", func(b *testing.B) {
			var m sync.Map
			for i := 0; i < 1000; i++ {
				m.Store(i, i)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Delete(i % 1000)
			}
		})
	})

	b.Run("concurrent_mixed", func(b *testing.B) {
		for _, goroutines := range []int{1, 4, 8, 16, 32, 64} {
			name := fmt.Sprintf("goroutines_%d", goroutines)

			b.Run(name+"/syncmap", func(b *testing.B) {
				m := syncmap.SyncMap[int, int]{}
				var wg sync.WaitGroup
				b.ResetTimer()

				for g := 0; g < goroutines; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := 0; i < b.N/goroutines; i++ {
							switch i % 4 {
							case 0:
								m.Store(i, i)
							case 1:
								m.Load(i)
							case 2:
								m.LoadOrStore(i, i)
							case 3:
								m.Delete(i)
							}
						}
					}()
				}
				wg.Wait()
			})

			b.Run(name+"/sync.Map", func(b *testing.B) {
				var m sync.Map
				var wg sync.WaitGroup
				b.ResetTimer()

				for g := 0; g < goroutines; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := 0; i < b.N/goroutines; i++ {
							switch i % 4 {
							case 0:
								m.Store(i, i)
							case 1:
								m.Load(i)
							case 2:
								m.LoadOrStore(i, i)
							case 3:
								m.Delete(i)
							}
						}
					}()
				}
				wg.Wait()
			})
		}
	})

	b.Run("range", func(b *testing.B) {
		sizes := []int{10, 100, 1000, 10000}
		for _, size := range sizes {
			name := fmt.Sprintf("size_%d", size)

			b.Run(name+"/syncmap", func(b *testing.B) {
				m := syncmap.SyncMap[int, int]{}
				for i := 0; i < size; i++ {
					m.Store(i, i)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					m.Range(func(key, value int) bool {
						return true
					})
				}
			})

			b.Run(name+"/sync.Map", func(b *testing.B) {
				var m sync.Map
				for i := 0; i < size; i++ {
					m.Store(i, i)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					m.Range(func(key, value any) bool {
						return true
					})
				}
			})
		}
	})
}
