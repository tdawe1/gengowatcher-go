package dedupe

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGate_FirstSeenThenDuplicateThenExpiry(t *testing.T) {
	g := New(30*time.Millisecond, 2)

	if !g.FirstSeen("job:1") {
		t.Fatal("expected first sighting")
	}
	if g.FirstSeen("job:1") {
		t.Fatal("expected duplicate suppression")
	}

	if !waitForFirstSeen(t, g, "job:1", 300*time.Millisecond) {
		t.Fatal("expected key to be accepted after expiry")
	}
}

func TestGate_EvictsOldestWhenCapacityReached(t *testing.T) {
	g := New(time.Hour, 2)

	if !g.FirstSeen("job:1") {
		t.Fatal("expected first key to be first seen")
	}
	if !g.FirstSeen("job:2") {
		t.Fatal("expected second key to be first seen")
	}
	if !g.FirstSeen("job:3") {
		t.Fatal("expected third key to be first seen")
	}

	if !g.FirstSeen("job:1") {
		t.Fatal("expected oldest key to be evicted")
	}
	if g.FirstSeen("job:3") {
		t.Fatal("expected newest key to remain deduplicated")
	}
}

func TestGate_RepeatedExpiryReinsertKeepsOrderBookkeepingBounded(t *testing.T) {
	g := New(20*time.Millisecond, 3)

	if !g.FirstSeen("job:1") {
		t.Fatal("expected initial first-seen")
	}

	for i := 0; i < 20; i++ {
		if g.FirstSeen("job:1") {
			t.Fatal("expected duplicate suppression before expiry")
		}
		if !waitForFirstSeen(t, g, "job:1", 300*time.Millisecond) {
			t.Fatal("expected first-seen after expiry")
		}
	}

	if got := len(g.order); got > g.capacity {
		t.Fatalf("expected order bookkeeping to remain bounded, got %d entries with capacity %d", got, g.capacity)
	}
}

func TestNew_NegativeCapacityIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected New not to panic with negative capacity, got panic: %v", r)
		}
	}()

	g := New(time.Hour, -1)
	if g.capacity != 0 {
		t.Fatalf("expected negative capacity to clamp to 0, got %d", g.capacity)
	}

	if !g.FirstSeen("job:1") {
		t.Fatal("expected first sighting to pass")
	}
	if !g.FirstSeen("job:1") {
		t.Fatal("expected dedupe to be disabled for negative capacity")
	}
}

func TestGate_ConcurrentDuplicateSuppression(t *testing.T) {
	g := New(time.Hour, 128)

	const workers = 64
	start := make(chan struct{})
	var seenCount atomic.Int32
	var wg sync.WaitGroup

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if g.FirstSeen("job:concurrent") {
				seenCount.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := seenCount.Load(); got != 1 {
		t.Fatalf("expected exactly one first-seen under concurrency, got %d", got)
	}
}

func waitForFirstSeen(t *testing.T, g *Gate, key string, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if g.FirstSeen(key) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}

	return false
}
