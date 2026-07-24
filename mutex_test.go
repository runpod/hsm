package hsm

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMutexNoDoubleClose reproduces the unlock signal double-close race:
// contending tryLock/unlock (like concurrent Dispatch) plus wait() readers.
// Panics on the buggy code within a few hundred ms; clean with the fix.
func TestMutexNoDoubleClose(t *testing.T) {
	var m mutex
	m.lock() // initialise the signal
	m.unlock()

	const workers = 32
	var wg sync.WaitGroup
	var stop atomic.Bool
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if m.tryLock() {
					go m.unlock() // release off the acquiring goroutine, like process
				}
				_ = m.wait()
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	if m.tryLock() { // drain a possible last holder
		m.unlock()
	}
}
