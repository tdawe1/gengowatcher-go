package dedupe

import (
	"sync"
	"time"
)

type Gate struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	entries  map[string]time.Time
	order    []string
}

func New(ttl time.Duration, capacity int) *Gate {
	if capacity < 0 {
		capacity = 0
	}

	return &Gate{
		ttl:      ttl,
		capacity: capacity,
		entries:  make(map[string]time.Time, capacity),
	}
}

func (g *Gate) FirstSeen(key string) bool {
	if key == "" || g.ttl <= 0 || g.capacity <= 0 {
		return true
	}

	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	g.pruneExpired(now)

	if expiry, ok := g.entries[key]; ok && expiry.After(now) {
		return false
	}

	g.entries[key] = now.Add(g.ttl)
	g.order = append(g.order, key)

	for len(g.entries) > g.capacity {
		g.evictOldest()
	}

	return true
}

func (g *Gate) pruneExpired(now time.Time) {
	for key, expiry := range g.entries {
		if !expiry.After(now) {
			delete(g.entries, key)
		}
	}

	g.compactOrder()
}

func (g *Gate) compactOrder() {
	if len(g.order) == 0 {
		return
	}

	compacted := make([]string, 0, len(g.entries))
	seen := make(map[string]struct{}, len(g.entries))

	for _, key := range g.order {
		if _, exists := g.entries[key]; !exists {
			continue
		}
		if _, duplicated := seen[key]; duplicated {
			continue
		}

		seen[key] = struct{}{}
		compacted = append(compacted, key)
	}

	g.order = compacted
}

func (g *Gate) evictOldest() {
	for len(g.order) > 0 {
		oldest := g.order[0]
		g.order = g.order[1:]

		if _, exists := g.entries[oldest]; exists {
			delete(g.entries, oldest)
			return
		}
	}
}
