# RSS+WebSocket Parity Patch Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Achieve Python-equivalent behavior for RSS and WebSocket monitors in Go, including startup priming, robust reconnect/auth behavior, pause/backoff controls, and protocol compatibility.

**Architecture:** Keep the existing `gengo.Monitor` contract and extend monitor internals for parity behavior rather than introducing a new orchestration layer. Add focused, deterministic unit/integration tests per behavior, then implement minimal code to satisfy each test. Preserve existing public types and keep changes constrained to `internal/config`, `internal/monitor/rss`, and `internal/monitor/websocket`.

**Tech Stack:** Go 1.24, `testing`, `httptest`, `net/http`, `gorilla/websocket`, `mmcdole/gofeed`, table-driven tests.

---

### Task 1: RSS Reward Parsing Parity

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/monitor/rss/monitor_test.go`
- Modify: `internal/monitor/rss/monitor.go`

**Step 1: Write the failing test**

```go
func TestParseReward_SupportsPythonAndGoFormats(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        float64
	}{
		{name: "python format", description: "Reward: US$ 12.50", want: 12.5},
		{name: "go format", description: "reward=7.25", want: 7.25},
		{name: "missing", description: "no reward here", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseReward(tc.description); got != tc.want {
				t.Fatalf("parseReward(%q) = %v, want %v", tc.description, got, tc.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestParseReward_SupportsPythonAndGoFormats -v`
Expected: FAIL on `Reward: US$ 12.50` case.

**Step 3: Write minimal implementation**

```go
var rewardPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)reward\s*=\s*([0-9]+(?:\.[0-9]+)?)`),
	regexp.MustCompile(`(?i)reward\s*:\s*(?:US\$|\$)?\s*([0-9]+(?:\.[0-9]+)?)`),
}

func parseReward(description string) float64 {
	for _, pattern := range rewardPatterns {
		matches := pattern.FindStringSubmatch(description)
		if len(matches) != 2 {
			continue
		}
		reward, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return reward
		}
	}
	return 0
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestParseReward_SupportsPythonAndGoFormats -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "fix: support python reward parsing format in rss monitor"
```

### Task 2: RSS Startup Priming (No Initial Burst)

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/monitor/rss/monitor_test.go`
- Modify: `internal/monitor/rss/monitor.go`

**Step 1: Write the failing test**

```go
func TestStart_PrimesFeedWithoutEmittingExistingItems(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item>
	</channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 200*time.Millisecond, 0)
	events := make(chan gengo.JobEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = m.Start(ctx, events) }()

	select {
	case ev := <-events:
		t.Fatalf("expected no event during prime, got %#v", ev)
	case <-time.After(120 * time.Millisecond):
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestStart_PrimesFeedWithoutEmittingExistingItems -v`
Expected: FAIL because current first poll emits the job.

**Step 3: Write minimal implementation**

```go
type Monitor struct {
	// existing fields...
	primed bool
}

func (m *Monitor) primeOnce(ctx context.Context) error {
	items, err := m.fetchItems(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if key := dedupeKey(item); key != "" {
			m.seen[key] = struct{}{}
		}
	}
	m.primed = true
	return nil
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	if !m.primed {
		if err := m.primeOnce(ctx); err != nil && ctx.Err() == nil {
			select {
			case events <- gengo.NewErrorEvent(gengo.SourceRSS, err.Error()):
			case <-ctx.Done():
				return nil
			}
		}
	}
	// continue with normal loop
	return m.loop(ctx, events)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestStart_PrimesFeedWithoutEmittingExistingItems -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: prime rss feed before emitting events"
```

### Task 3: RSS Timeout and In-Flight Guard

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/monitor/rss/monitor_test.go`
- Modify: `internal/monitor/rss/monitor.go`

**Step 1: Write the failing test**

```go
func TestPollOnce_TimesOutSlowServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte("<rss><channel></channel></rss>"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	m.fetchTimeout = 50 * time.Millisecond

	err := m.pollOnce(context.Background(), make(chan gengo.JobEvent, 1))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestPollOnce_TimesOutSlowServer -v`
Expected: FAIL because current code waits for slow server.

**Step 3: Write minimal implementation**

```go
type Monitor struct {
	// existing fields...
	fetchTimeout time.Duration
	inFlight     atomic.Bool
}

func New(cfg config.RSSConfig, checkInterval time.Duration, minReward float64) *Monitor {
	return &Monitor{
		cfg: cfg,
		checkInterval: checkInterval,
		minReward: minReward,
		seen: make(map[string]struct{}),
		fetchTimeout: 30 * time.Second,
	}
}

func (m *Monitor) pollOnce(ctx context.Context, events chan<- gengo.JobEvent) error {
	if !m.inFlight.CompareAndSwap(false, true) {
		return nil
	}
	defer m.inFlight.Store(false)

	pollCtx, cancel := context.WithTimeout(ctx, m.fetchTimeout)
	defer cancel()

	// use pollCtx in http request
	// existing logic unchanged otherwise
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run TestPollOnce_TimesOutSlowServer -v`
Expected: PASS with timeout-driven failure.

**Step 5: Commit**

```bash
git add internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: add rss fetch timeout and in-flight guard"
```

### Task 4: RSS Backoff and Pause-File Parity

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/monitor/rss/monitor.go`
- Modify: `internal/monitor/rss/monitor_test.go`

**Step 1: Write the failing test**

```go
func TestStart_PausedByPauseFileSkipsPolling(t *testing.T) {
	tmp := t.TempDir()
	pauseFile := filepath.Join(tmp, "gengowatcher.pause")
	if err := os.WriteFile(pauseFile, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("<rss><channel></channel></rss>"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 30*time.Millisecond, 0)
	m.pauseFile = pauseFile
	m.pauseSleep = 80 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	_ = m.Start(ctx, make(chan gengo.JobEvent, 1))

	if hits.Load() != 0 {
		t.Fatalf("expected 0 polls while paused, got %d", hits.Load())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/rss -run TestStart_PausedByPauseFileSkipsPolling -v`
Expected: FAIL because current code does not inspect pause file.

**Step 3: Write minimal implementation**

```go
type Monitor struct {
	// existing fields...
	pauseFile string
	pauseSleep time.Duration
	failureCount int
	maxBackoff time.Duration
}

func (m *Monitor) isPaused() bool {
	if m.pauseFile == "" {
		return false
	}
	_, err := os.Stat(m.pauseFile)
	return err == nil
}

func (m *Monitor) nextWait(success bool) time.Duration {
	if success {
		m.failureCount = 0
		return m.checkInterval
	}
	m.failureCount++
	wait := m.checkInterval * time.Duration(1<<min(m.failureCount, 10))
	if wait > m.maxBackoff {
		wait = m.maxBackoff
	}
	return wait
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	wait := time.Duration(0)
	for {
		if m.isPaused() {
			wait = m.pauseSleep
		} else {
			err := m.pollOnce(ctx, events)
			wait = m.nextWait(err == nil)
			if err != nil && ctx.Err() == nil {
				select {
				case events <- gengo.NewErrorEvent(gengo.SourceRSS, err.Error()):
				case <-ctx.Done():
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/rss -run 'TestStart_PausedByPauseFileSkipsPolling|TestStart_DeduplicatesAndAppliesMinReward' -v`
Expected: PASS for pause behavior and no regression in existing flow.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/defaults.go internal/monitor/rss/monitor.go internal/monitor/rss/monitor_test.go
git commit -m "feat: add rss pause-file and exponential backoff behavior"
```

### Task 5: WebSocket Event Protocol Parity (`available_collection`)

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/monitor/websocket/monitor_test.go`
- Modify: `internal/monitor/websocket/monitor.go`

**Step 1: Write the failing test**

```go
func TestStart_ParsesAvailableCollectionPayload(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, _, _ = conn.ReadMessage() // auth payload
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"available_collection","collection":{"id":"42","rewards":19.5,"lc_src":"en","lc_tgt":"ja"}}`))
		<-time.After(120 * time.Millisecond)
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{Enabled: true, URL: toWebSocketURL(server.URL), UserID: "u1", UserSession: "s1"})
	events := make(chan gengo.JobEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Job == nil || ev.Job.ID != "42" || ev.Job.Reward != 19.5 {
			t.Fatalf("unexpected event: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected available_collection event")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_ParsesAvailableCollectionPayload -v`
Expected: FAIL because current parser only handles `event=job_published`.

**Step 3: Write minimal implementation**

```go
type socketEnvelope struct {
	Event string `json:"event"`
	Type  string `json:"type"`
	Data  json.RawMessage `json:"data"`
	Collection json.RawMessage `json:"collection"`
}

type availableCollection struct {
	ID string `json:"id"`
	Rewards float64 `json:"rewards"`
	SourceLang string `json:"lc_src"`
	TargetLang string `json:"lc_tgt"`
}

func parseJobPublished(payload []byte) (gengo.JobEvent, bool) {
	// parse both:
	// 1) {"event":"job_published","data":{...}}
	// 2) {"type":"available_collection","collection":{...}}
	// build gengo.Job with Reward and Language
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run TestStart_ParsesAvailableCollectionPayload -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "feat: support available_collection websocket event parsing"
```

### Task 6: WebSocket Auth Payload + Optional UserKey

**Skills:** @test-driven-development @go-testing

**Files:**
- Modify: `internal/monitor/websocket/monitor_test.go`
- Modify: `internal/monitor/websocket/monitor.go`

**Step 1: Write the failing test**

```go
func TestStart_SendsAuthPayloadAfterConnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	authSeen := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err == nil {
			authSeen <- msg
		}
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{Enabled: true, URL: toWebSocketURL(server.URL), UserID: "u1", UserSession: "s1"})
	if !m.Enabled() {
		t.Fatal("expected monitor enabled without user_key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() { _ = m.Start(ctx, make(chan gengo.JobEvent, 1)) }()

	select {
	case raw := <-authSeen:
		if !bytes.Contains(raw, []byte(`"user_id":"u1"`)) {
			t.Fatalf("unexpected auth payload: %s", string(raw))
		}
	case <-time.After(time.Second):
		t.Fatal("expected auth payload")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_SendsAuthPayloadAfterConnect -v`
Expected: FAIL because current code does not write auth frame and `Enabled()` requires `UserKey`.

**Step 3: Write minimal implementation**

```go
type socketAuth struct {
	UserID      string `json:"user_id"`
	UserSession string `json:"user_session"`
	UserKey     string `json:"user_key,omitempty"`
}

func (m *Monitor) runConnection(ctx context.Context, events chan<- gengo.JobEvent) error {
	conn, _, err := m.dialer.DialContext(ctx, m.cfg.URL, m.authHeaders())
	if err != nil {
		return err
	}
	defer conn.Close()

	auth := socketAuth{UserID: m.cfg.UserID, UserSession: m.cfg.UserSession, UserKey: m.cfg.UserKey}
	if err := conn.WriteJSON(auth); err != nil {
		return err
	}
	// existing read loop
}

func (m *Monitor) Enabled() bool {
	return m.cfg.Enabled && m.cfg.URL != "" && m.cfg.UserID != "" && m.cfg.UserSession != ""
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/websocket -run TestStart_SendsAuthPayloadAfterConnect -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "feat: send websocket auth payload and make user key optional"
```

### Task 7: WebSocket Header-Fallback Retry + Regression Sweep

**Skills:** @test-driven-development @go-testing @verification-before-completion

**Files:**
- Modify: `internal/monitor/websocket/monitor_test.go`
- Modify: `internal/monitor/websocket/monitor.go`

**Step 1: Write the failing test**

```go
func TestStart_RetriesWithoutHeadersWhenHandshakeRejectsHeaders(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var first int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.CompareAndSwapInt32(&first, 0, 1) {
			if r.Header.Get("Cookie") != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("extra headers not allowed"))
				return
			}
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"job_published","data":{"id":"fallback-job"}}`))
		<-time.After(120 * time.Millisecond)
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{Enabled: true, URL: toWebSocketURL(server.URL), UserID: "u1", UserSession: "s1", UserKey: "k1"})
	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() { _ = m.Start(ctx, events) }()

	for {
		select {
		case ev := <-events:
			if ev.Type == gengo.EventJobFound && ev.Job != nil && ev.Job.ID == "fallback-job" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("expected fallback reconnect job event")
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/websocket -run TestStart_RetriesWithoutHeadersWhenHandshakeRejectsHeaders -v`
Expected: FAIL because current code does not retry without headers.

**Step 3: Write minimal implementation**

```go
func (m *Monitor) dialWithFallback(ctx context.Context) (*websocket.Conn, error) {
	conn, _, err := m.dialer.DialContext(ctx, m.cfg.URL, m.authHeaders())
	if err == nil {
		return conn, nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "extra header") || strings.Contains(msg, "header") {
		return m.dialer.DialContext(ctx, m.cfg.URL, nil)
	}
	return nil, err
}
```

**Step 4: Run regression tests to verify all parity behavior**

Run: `go test ./internal/monitor/rss ./internal/monitor/websocket -v`
Expected: PASS for all RSS/WS tests (new + existing).

**Step 5: Commit**

```bash
git add internal/monitor/websocket/monitor.go internal/monitor/websocket/monitor_test.go
git commit -m "fix: add websocket handshake fallback for header rejection"
```

### Task 8: Final Verification Gate

**Skills:** @verification-before-completion

**Files:**
- Modify: `go-rewrite-plan.md`

**Step 1: Add explicit parity progress note**

```markdown
2. **Phase 2 (In Progress):** WebSocket + RSS monitors (parity patch for RSS/WS robustness complete)
```

**Step 2: Run targeted monitor suite**

Run: `go test ./internal/monitor/rss ./internal/monitor/websocket -v`
Expected: PASS.

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS.

**Step 4: Verify clean working tree before handoff**

Run: `git status --short`
Expected: Only intended files changed, no accidental edits.

**Step 5: Commit**

```bash
git add go-rewrite-plan.md
git commit -m "docs: record rss and websocket parity patch progress"
```
