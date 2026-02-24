# Phase 2 WebSocket + RSS Monitors Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement Phase 2 by shipping RSS and WebSocket monitors that emit `gengo.JobEvent` reliably with filtering, deduplication, and reconnect behavior.

**Architecture:** Add two monitor implementations under `internal/monitor/rss` and `internal/monitor/websocket` that satisfy `pkg/gengo.Monitor`. Build behavior with strict TDD: start from failing tests, implement minimum code to pass, then commit each small behavior. Keep parsing and transport concerns separated so later watcher integration (Phase 3) can consume monitor events without refactors.

**Tech Stack:** Go 1.24, `github.com/mmcdole/gofeed` (RSS parsing), `github.com/gorilla/websocket` (WebSocket client), standard library testing (`testing`, `httptest`, `context`, `time`).

---

Use these implementation skills while executing:
- `@superpowers:test-driven-development`
- `@go-testing`
- `@superpowers:verification-before-completion`

### Task 1: RSS Monitor Contract (constructor + interface behavior)

**Files:**
- Create: `internal/monitor/rss/monitor.go`
- Test: `internal/monitor/rss/monitor_test.go`
- Modify: `go.mod`

**Step 1: Write the failing test**

```go
package rss

import (
    "testing"
    "time"

    "github.com/tdawe1/gengowatcher-go/internal/config"
    "github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestNewMonitor_ImplementsMonitorContract(t *testing.T) {
    cfg := config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}
    m := New(cfg, 30*time.Second, 5.0)

    var monitor gengo.Monitor = m
    if monitor.Name() != "rss" {
        t.Fatalf("expected name rss, got %s", monitor.Name())
    }
    if monitor.Source() != gengo.SourceRSS {
        t.Fatalf("expected source rss, got %s", monitor.Source())
    }
    if !monitor.Enabled() {
        t.Fatalf("expected monitor enabled")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestNewMonitor_ImplementsMonitorContract -v`
Expected: FAIL with `undefined: New`

**Step 3: Write minimal implementation**

```go
type Monitor struct {
    cfg           config.RSSConfig
    checkInterval time.Duration
    minReward     float64
}

func New(cfg config.RSSConfig, checkInterval time.Duration, minReward float64) *Monitor {
    return &Monitor{cfg: cfg, checkInterval: checkInterval, minReward: minReward}
}

func (m *Monitor) Name() string        { return "rss" }
func (m *Monitor) Source() gengo.Source { return gengo.SourceRSS }
func (m *Monitor) Enabled() bool       { return m.cfg.Enabled && m.cfg.URL != "" }
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestNewMonitor_ImplementsMonitorContract -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go go.mod go.sum
git commit -m "feat: add rss monitor contract implementation"
```

### Task 2: RSS Monitor Polling + Event Emission

**Files:**
- Modify: `internal/monitor/rss/monitor.go`
- Modify: `internal/monitor/rss/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestStart_EmitsJobFoundEventForFeedItem(t *testing.T) {
    feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>JP -> EN</title><link>https://gengo.com/jobs/1</link></item></channel></rss>`
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        _, _ = w.Write([]byte(feed))
    }))
    defer server.Close()

    m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 10*time.Millisecond, 0)
    events := make(chan gengo.JobEvent, 1)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go func() { _ = m.Start(ctx, events) }()

    select {
    case ev := <-events:
        if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.ID != "job-1" {
            t.Fatalf("unexpected event: %#v", ev)
        }
    case <-time.After(500 * time.Millisecond):
        t.Fatal("expected job event")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestStart_EmitsJobFoundEventForFeedItem -v`
Expected: FAIL with `Monitor.Start undefined`

**Step 3: Write minimal implementation**

```go
func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
    ticker := time.NewTicker(m.checkInterval)
    defer ticker.Stop()

    for {
        if err := m.pollOnce(ctx, events); err != nil {
            events <- gengo.NewErrorEvent(gengo.SourceRSS, err.Error())
        }

        select {
        case <-ctx.Done():
            return nil
        case <-ticker.C:
        }
    }
}

func (m *Monitor) pollOnce(ctx context.Context, events chan<- gengo.JobEvent) error {
    // fetch RSS, parse with gofeed, map first item to gengo.Job and emit EventJobFound
    return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestStart_EmitsJobFoundEventForFeedItem -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: emit rss job events from feed polling"
```

### Task 3: RSS Deduplication + Min Reward Filter

**Files:**
- Modify: `internal/monitor/rss/monitor.go`
- Modify: `internal/monitor/rss/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestStart_DeduplicatesAndAppliesMinReward(t *testing.T) {
    // feed contains duplicate guid job-1 and one low-reward item (reward=1.0)
    // minReward is 5.0, so only one event should be emitted
    // assert exactly one EventJobFound with job-1
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestStart_DeduplicatesAndAppliesMinReward -v`
Expected: FAIL because dedupe/filter not implemented

**Step 3: Write minimal implementation**

```go
type Monitor struct {
    // ...existing fields...
    seen map[string]struct{}
}

func (m *Monitor) shouldEmit(jobID string, reward float64) bool {
    if reward < m.minReward {
        return false
    }
    if _, exists := m.seen[jobID]; exists {
        return false
    }
    m.seen[jobID] = struct{}{}
    return true
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestStart_DeduplicatesAndAppliesMinReward -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: add rss dedupe and minimum reward filtering"
```

### Task 4: WebSocket Monitor Contract (constructor + metadata)

**Files:**
- Create: `internal/monitor/websocket/monitor.go`
- Test: `internal/monitor/websocket/monitor_test.go`
- Modify: `go.mod`

**Step 1: Write the failing test**

```go
package websocket

import (
    "testing"

    "github.com/tdawe1/gengowatcher-go/internal/config"
    "github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestNewMonitor_ImplementsMonitorContract(t *testing.T) {
    m := New(config.WebSocketConfig{Enabled: true, URL: "ws://example"})

    var monitor gengo.Monitor = m
    if monitor.Name() != "websocket" {
        t.Fatalf("expected websocket, got %s", monitor.Name())
    }
    if monitor.Source() != gengo.SourceWebSocket {
        t.Fatalf("expected websocket source")
    }
    if !monitor.Enabled() {
        t.Fatalf("expected monitor enabled")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestNewMonitor_ImplementsMonitorContract -v`
Expected: FAIL with `undefined: New`

**Step 3: Write minimal implementation**

```go
type Monitor struct {
    cfg config.WebSocketConfig
}

func New(cfg config.WebSocketConfig) *Monitor { return &Monitor{cfg: cfg} }
func (m *Monitor) Name() string               { return "websocket" }
func (m *Monitor) Source() gengo.Source       { return gengo.SourceWebSocket }
func (m *Monitor) Enabled() bool {
    return m.cfg.Enabled && m.cfg.URL != "" && m.cfg.UserID != "" && m.cfg.UserSession != "" && m.cfg.UserKey != ""
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run TestNewMonitor_ImplementsMonitorContract -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go go.mod go.sum
git commit -m "feat: add websocket monitor contract implementation"
```

### Task 5: WebSocket Event Parsing + Job Emission

**Files:**
- Modify: `internal/monitor/websocket/monitor.go`
- Modify: `internal/monitor/websocket/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestStart_EmitsJobFoundFromJobPublishedMessage(t *testing.T) {
    // start local websocket test server
    // write JSON message: {"event":"job_published","data":{"id":"job-42","title":"Hello"}}
    // run Start in goroutine and assert first event is EventJobFound with job id job-42
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_EmitsJobFoundFromJobPublishedMessage -v`
Expected: FAIL because Start/read loop parser not implemented

**Step 3: Write minimal implementation**

```go
func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
    conn, _, err := websocket.DefaultDialer.DialContext(ctx, m.cfg.URL, m.authHeaders())
    if err != nil {
        return err
    }
    defer conn.Close()

    for {
        _, msg, err := conn.ReadMessage()
        if err != nil {
            return err
        }
        if ev, ok := parseJobPublished(msg); ok {
            events <- ev
        }
    }
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run TestStart_EmitsJobFoundFromJobPublishedMessage -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "feat: parse websocket job_published messages into events"
```

### Task 6: WebSocket Reconnect + Non-Fatal Error Events

**Files:**
- Modify: `internal/monitor/websocket/monitor.go`
- Modify: `internal/monitor/websocket/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestStart_ReconnectsAndEmitsErrorEventOnDisconnect(t *testing.T) {
    // server closes first connection immediately, accepts second connection and emits one job message
    // assert first emitted event is EventError (non-fatal), then EventJobFound
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_ReconnectsAndEmitsErrorEventOnDisconnect -v`
Expected: FAIL because reconnect loop is missing

**Step 3: Write minimal implementation**

```go
func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
    backoff := 250 * time.Millisecond
    for {
        err := m.runConnection(ctx, events)
        if err == nil || ctx.Err() != nil {
            return nil
        }

        events <- gengo.NewErrorEvent(gengo.SourceWebSocket, err.Error())

        select {
        case <-ctx.Done():
            return nil
        case <-time.After(backoff):
            if backoff < 4*time.Second {
                backoff *= 2
            }
        }
    }
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run TestStart_ReconnectsAndEmitsErrorEventOnDisconnect -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "feat: add websocket reconnect loop with error event emission"
```

### Task 7: Phase 2 Verification Gate

**Files:**
- Modify: `go-rewrite-plan.md`

**Step 1: Write the failing test**

```go
// No new test file. This is a verification gate task.
```

**Step 2: Run test to verify current state**

Run: `go test ./...`
Expected: PASS for all monitor and existing config/model tests

**Step 3: Write minimal implementation**

```markdown
Update `go-rewrite-plan.md` implementation phases to show:
- Phase 1: Completed
- Phase 2: In Progress (WebSocket + RSS monitors)
```

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add go-rewrite-plan.md
git commit -m "docs: mark rewrite roadmap as entering phase 2"
```
