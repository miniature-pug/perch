package safe

import (
	"sync"
	"testing"
)

// TestRecover_PanicDoesNotPropagate proves that a goroutine guarded by
// defer Recover survives a panic in its body rather than crashing the process.
func TestRecover_PanicDoesNotPropagate(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	reached := false
	go func() {
		defer wg.Done()
		defer func() {
			// If Recover let the panic through, this deferred check would itself
			// be running during an unwinding panic and recover() here would be
			// non-nil. Assert Recover already swallowed it.
			if r := recover(); r != nil {
				t.Errorf("panic propagated past safe.Recover: %v", r)
			}
		}()
		defer Recover("test-goroutine")
		reached = true
		panic("boom")
	}()

	wg.Wait()
	if !reached {
		t.Fatal("goroutine body did not run")
	}
}

// TestRecover_NoPanicIsNoop verifies Recover is inert on the happy path.
func TestRecover_NoPanicIsNoop(t *testing.T) {
	func() {
		defer Recover("test-goroutine")
	}()
}
