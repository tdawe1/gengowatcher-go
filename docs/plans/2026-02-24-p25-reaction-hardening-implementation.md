# P2.5 RSS+WS Reaction Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ship latency-first hardening before Phase 3: stable idle WebSocket behavior, enforced RSS polling limits, cross-source first-seen dedupe primitives, and non-blocking SQLite telemetry.

**Architecture:** Keep monitor contracts unchanged (`gengo.Monitor`) and harden internals first. Add focused, testable building blocks (`dedupe`, `reaction`, `telemetry`) that can be wired by Phase 3 without redesign. Apply strict TDD per task with small commits.

**Tech Stack:** Go 1.24, `github.com/gorilla/websocket`, `database/sql`, `modernc.org/sqlite`, table-driven tests, `httptest`.

---

Use these implementation skills while executing:
- `@superpowers:test-driven-development`
- `@go-testing`
- `@superpowers:verification-before-completion`

### Task 1: Enforce RSS Minimum Poll Interval (31s Guardrail)

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go`

**Step 1: Write the failing test**

```go
func TestValidate_RSSRequiresMinimumPollInterval(t *testing.T) {
	cfg := &Config{
		Watcher: WatcherConfig{CheckInterval: 10 * time.Second},
		RSS: RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss", PauseSleep: 1, MaxBackoff: 1},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for rss poll interval below 31s")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestValidate_RSSRequiresMinimumPollInterval -v`
Expected: FAIL because validation currently allows low intervals.

**Step 3: Write minimal implementation**

```go
const MinRSSPollInterval = 31 * time.Second

func (c *Config) Validate() error {
	// existing checks...
	if c.RSS.Enabled && c.Watcher.CheckInterval < MinRSSPollInterval {
		return fmt.Errorf("watcher.check_interval must be >= %s when rss is enabled", MinRSSPollInterval)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run TestValidate_RSSRequiresMinimumPollInterval -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix: enforce minimum rss poll interval"
```

### Task 2: WebSocket Heartbeat Liveness (Idle-Safe)

**Files:**
- Modify: `internal/monitor/websocket/monitor_test.go`
- Modify: `internal/monitor/websocket/monitor.go`

**Step 1: Write the failing test**

```go
func TestStart_DoesNotEmitTimeoutWhileHeartbeatAlive(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage() // auth
		<-r.Context().Done()
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{Enabled: true, URL: toWebSocketURL(server.URL), UserID: "u", UserSession: "s"})
	m.heartbeatInterval = 20 * time.Millisecond
	m.pongWait = 90 * time.Millisecond

	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Millisecond)
	defer cancel()
	_ = m.Start(ctx, events)

	for {
		select {
		case ev := <-events:
			if ev.Type == gengo.EventError {
				t.Fatalf("unexpected error during healthy idle heartbeat: %#v", ev)
			}
		default:
			return
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_DoesNotEmitTimeoutWhileHeartbeatAlive -v`
Expected: FAIL with timeout error event.

**Step 3: Write minimal implementation**

```go
type Monitor struct {
	// existing fields...
	heartbeatInterval time.Duration
	pongWait          time.Duration
}

func (m *Monitor) runConnection(ctx context.Context, events chan<- gengo.JobEvent) (error, bool) {
	// after connect:
	_ = conn.SetReadDeadline(time.Now().Add(m.pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(m.pongWait))
	})

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go m.runHeartbeat(heartbeatCtx, conn)

	// keep read loop, remove per-iteration fixed deadline reset
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run 'TestStart_DoesNotEmitTimeoutWhileHeartbeatAlive|TestStart_EmitsErrorEventWhenReadTimesOut' -v`
Expected: PASS for both tests.

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "feat: add websocket ping pong liveness heartbeat"
```

### Task 3: Add Shared First-Seen Dedupe Gate (TTL + Capacity)

**Files:**
- Create: `internal/dedupe/gate.go`
- Create: `internal/dedupe/gate_test.go`

**Step 1: Write the failing test**

```go
func TestGate_FirstSeenThenDuplicateThenExpiry(t *testing.T) {
	g := New(50*time.Millisecond, 2)
	if !g.FirstSeen("job:1") { t.Fatal("expected first sighting") }
	if g.FirstSeen("job:1") { t.Fatal("expected duplicate suppression") }
	time.Sleep(60 * time.Millisecond)
	if !g.FirstSeen("job:1") { t.Fatal("expected key to be accepted after expiry") }
}

func TestGate_EvictsOldestWhenCapacityReached(t *testing.T) {
	g := New(time.Hour, 2)
	_ = g.FirstSeen("job:1")
	_ = g.FirstSeen("job:2")
	_ = g.FirstSeen("job:3")
	if !g.FirstSeen("job:1") { t.Fatal("expected oldest key to be evicted") }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/dedupe -run TestGate -v`
Expected: FAIL with missing package/types.

**Step 3: Write minimal implementation**

```go
type Gate struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	entries  map[string]time.Time
	order    []string
}

func (g *Gate) FirstSeen(key string) bool {
	// prune expired, reject duplicates, cap by oldest insertion
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/dedupe -run TestGate -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/dedupe/gate.go internal/dedupe/gate_test.go
git commit -m "feat: add bounded ttl dedupe gate"
```

### Task 4: RSS Uses Bounded Dedupe Gate Instead of Unbounded Map

**Files:**
- Modify: `internal/monitor/rss/monitor.go`
- Modify: `internal/monitor/rss/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestShouldEmit_AllowsPreviouslySeenKeyAfterDedupeTTL(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}, time.Second, 0)
	m.dedupeTTL = 30 * time.Millisecond
	m.dedupeCap = 2
	m.resetDedupeForTest()

	if !m.shouldEmit("guid:job-1", 1.0) { t.Fatal("expected first emit") }
	if m.shouldEmit("guid:job-1", 1.0) { t.Fatal("expected suppression before ttl") }
	time.Sleep(40 * time.Millisecond)
	if !m.shouldEmit("guid:job-1", 1.0) { t.Fatal("expected emit after ttl") }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestShouldEmit_AllowsPreviouslySeenKeyAfterDedupeTTL -v`
Expected: FAIL because current dedupe store never expires.

**Step 3: Write minimal implementation**

```go
type Monitor struct {
	// remove seen map
	dedupe *dedupe.Gate
}

func New(...) *Monitor {
	return &Monitor{ /* ... */, dedupe: dedupe.New(72*time.Hour, 50_000) }
}

func (m *Monitor) shouldEmit(itemKey string, reward float64) bool {
	if reward < m.minReward { return false }
	if itemKey == "" { return true }
	return m.dedupe.FirstSeen(itemKey)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run 'TestShouldEmit_AllowsPreviouslySeenKeyAfterDedupeTTL|TestStart_DeduplicatesAndAppliesMinReward' -v`
Expected: PASS and no regression.

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: switch rss dedupe to bounded ttl gate"
```

### Task 5: Add Reaction Executor (Open + Notify Parallel, Non-Blocking)

**Files:**
- Create: `internal/reaction/executor.go`
- Create: `internal/reaction/executor_test.go`

**Step 1: Write the failing test**

```go
func TestExecutor_DispatchesOpenAndNotifyInParallel(t *testing.T) {
	var openCalled, notifyCalled atomic.Bool
	exec := New(Deps{
		Open: func(context.Context, string) error { openCalled.Store(true); time.Sleep(30 * time.Millisecond); return nil },
		Notify: func(context.Context, string) error { notifyCalled.Store(true); time.Sleep(30 * time.Millisecond); return nil },
	})

	start := time.Now()
	exec.Dispatch(context.Background(), "https://gengo.com/t/jobs/details/1", "job")
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("dispatch should return quickly, took %s", elapsed)
	}

	time.Sleep(50 * time.Millisecond)
	if !openCalled.Load() || !notifyCalled.Load() { t.Fatal("expected both actions") }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/reaction -run TestExecutor_DispatchesOpenAndNotifyInParallel -v`
Expected: FAIL with missing package/types.

**Step 3: Write minimal implementation**

```go
type Deps struct {
	Open   func(context.Context, string) error
	Notify func(context.Context, string) error
}

func (e *Executor) Dispatch(ctx context.Context, url string, title string) {
	go func() { _ = e.deps.Open(ctx, url) }()
	go func() { _ = e.deps.Notify(ctx, title) }()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/reaction -run TestExecutor_DispatchesOpenAndNotifyInParallel -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/reaction/executor.go internal/reaction/executor_test.go
git commit -m "feat: add non blocking reaction executor"
```

### Task 6: Add SQLite Telemetry Sink with Rolling Latency View

**Files:**
- Create: `internal/telemetry/sqlite/sink.go`
- Create: `internal/telemetry/sqlite/sink_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write the failing test**

```go
func TestSink_PersistsAndAggregatesLatency(t *testing.T) {
	sink, err := New("file::memory:?cache=shared")
	if err != nil { t.Fatal(err) }
	defer sink.Close()

	ev := Event{JobID: "42", Source: "websocket", DetectedToDispatchMS: 12.0, DispatchToOpenMS: 40.0}
	if err := sink.Write(context.Background(), ev); err != nil { t.Fatal(err) }

	stats, err := sink.LatencySnapshot(context.Background())
	if err != nil { t.Fatal(err) }
	if stats.Count != 1 { t.Fatalf("expected 1 row, got %d", stats.Count) }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/telemetry/sqlite -run TestSink_PersistsAndAggregatesLatency -v`
Expected: FAIL with missing package/dependency.

**Step 3: Write minimal implementation**

```go
// create schema:
// events_raw(id, job_id, source, detected_at, detected_to_dispatch_ms, dispatch_to_open_ms, degraded_mode, outcome)
// view latency_5m as aggregate count/avg/p50-like approximation for now

func (s *Sink) Write(ctx context.Context, ev Event) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events_raw (...) VALUES (...)`, ...)
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/telemetry/sqlite -run TestSink_PersistsAndAggregatesLatency -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/telemetry/sqlite/sink.go internal/telemetry/sqlite/sink_test.go go.mod go.sum
git commit -m "feat: add sqlite telemetry sink for latency metrics"
```

### Task 7: Add Event Router Primitive (First-Seen Wins + Async Telemetry)

**Files:**
- Create: `internal/pipeline/router.go`
- Create: `internal/pipeline/router_test.go`

**Step 1: Write the failing test**

```go
func TestRouter_FirstSeenWinsAcrossSources(t *testing.T) {
	// same job ID from websocket then rss should trigger exactly one Dispatch
}

func TestRouter_TelemetryFailureDoesNotBlockDispatch(t *testing.T) {
	// telemetry write error should not prevent reaction dispatch
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline -run TestRouter -v`
Expected: FAIL with missing package/types.

**Step 3: Write minimal implementation**

```go
type Router struct {
	dedupe    *dedupe.Gate
	reaction  *reaction.Executor
	telemetry TelemetrySink
}

func (r *Router) Handle(ctx context.Context, ev gengo.JobEvent) {
	// first-seen dedupe, then dispatch reaction immediately,
	// then write telemetry asynchronously (best effort)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline -run TestRouter -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/pipeline/router.go internal/pipeline/router_test.go
git commit -m "feat: add first seen event router with async telemetry"
```

### Task 8: Verification Gate + Roadmap Update

**Files:**
- Modify: `go-rewrite-plan.md`

**Step 1: Add explicit P2.5 hardening progress note**

```markdown
2. **Phase 2.5 (Hardening):** RSS/WS reliability + reaction pipeline + telemetry instrumentation
```

**Step 2: Run targeted hardening suites**

Run: `go test ./internal/config ./internal/monitor/rss ./internal/monitor/websocket ./internal/dedupe ./internal/reaction ./internal/telemetry/sqlite ./internal/pipeline -v`
Expected: PASS.

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS.

**Step 4: Verify working tree state**

Run: `git status --short`
Expected: Only intended files are modified.

**Step 5: Commit**

```bash
git add go-rewrite-plan.md
git commit -m "docs: record p25 rss ws reaction hardening progress"
```
