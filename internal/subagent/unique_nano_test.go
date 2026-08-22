package subagent

import (
	"sync"
	"testing"
	"time"
)

// uniqueNano feeds agent IDs, which key the orchestrator's agent table: two
// equal stamps would make the second spawn silently replace the first.
func TestUniqueNanoIsStrictlyIncreasingEvenWhenTheClockIsNot(t *testing.T) {
	// Push the last stamp a full second into the future so every wall-clock
	// reading during the test is <= it and the bump branch has to fire.
	future := time.Now().UnixNano() + int64(time.Second)
	lastAgentNano.Store(future)

	prev := future
	for i := 0; i < 1000; i++ {
		got := uniqueNano()
		if got <= prev {
			t.Fatalf("call %d: uniqueNano() = %d, want > %d", i, got, prev)
		}
		prev = got
	}
}

func TestUniqueNanoIsDistinctUnderContention(t *testing.T) {
	const goroutines, perGoroutine = 32, 200

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make(map[int64]struct{}, goroutines*perGoroutine)
		dups int
	)
	for g := 0; g < goroutines; g++ {
		wg.Go(func() {
			local := make([]int64, 0, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				local = append(local, uniqueNano())
			}
			mu.Lock()
			defer mu.Unlock()
			for _, n := range local {
				if _, dup := seen[n]; dup {
					dups++
				}
				seen[n] = struct{}{}
			}
		})
	}
	wg.Wait()

	if dups != 0 {
		t.Errorf("%d duplicate stamps across %d concurrent calls, want 0", dups, goroutines*perGoroutine)
	}
}
