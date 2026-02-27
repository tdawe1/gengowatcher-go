# Phase 3 TUI Baseline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Start Phase 3 by shipping a runnable Bubble Tea TUI baseline with live job ingestion and basic stats, wired to the existing monitor-router-reaction-telemetry runtime.

**Architecture:** Keep the current runtime (`internal/app`, `internal/pipeline`) as the source of truth for first-seen routing and reliability controls. Add a thin event hook from router to watcher, then bridge watcher events into Bubble Tea messages. Implement only Jobs + Stats baseline (no full command palette/log filtering yet) to avoid overbuilding.

**Tech Stack:** Go 1.24, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss`, existing monitors (`internal/monitor/rss`, `internal/monitor/websocket`), table-driven tests.

---

Use these implementation skills while executing:
- `@superpowers:test-driven-development`
- `@go-testing`
- `@superpowers:verification-before-completion`

### Task 1: Add Router First-Seen Event Hook for UI Consumers

**Files:**
- Modify: `internal/pipeline/router.go`
- Modify: `internal/pipeline/router_test.go`

**Step 1: Write the failing test**

```go
func TestRouter_FirstSeenHookRunsOnceForDuplicates(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)
	hookCalls := make(chan gengo.JobEvent, 2)

	router := NewRouter(
		gate,
		nil,
		nil,
		WithOnFirstSeenJob(func(ev gengo.JobEvent) {
			hookCalls <- ev
		}),
	)

	job := &gengo.Job{ID: "job-42", URL: "https://gengo.com/jobs/42"}
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, job))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, job))

	select {
	case <-hookCalls:
	case <-time.After(time.Second):
		t.Fatal("expected first-seen hook call")
	}

	select {
	case <-hookCalls:
		t.Fatal("expected duplicate suppression for first-seen hook")
	case <-time.After(250 * time.Millisecond):
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline -run TestRouter_FirstSeenHookRunsOnceForDuplicates -v`
Expected: FAIL with missing `WithOnFirstSeenJob` / hook wiring.

**Step 3: Write minimal implementation**

```go
type Router struct {
	// existing fields...
	onFirstSeenJob func(gengo.JobEvent)
}

func WithOnFirstSeenJob(fn func(gengo.JobEvent)) RouterOption {
	return func(r *Router) {
		r.onFirstSeenJob = fn
	}
}

func (r *Router) Handle(ctx context.Context, ev gengo.JobEvent) {
	// existing first-seen checks...
	if r.onFirstSeenJob != nil {
		r.onFirstSeenJob(ev)
	}
	// existing dispatch + telemetry...
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline -run 'TestRouter_FirstSeenHookRunsOnceForDuplicates|TestRouter_FirstSeenWinsAcrossSources' -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/pipeline/router.go internal/pipeline/router_test.go
git commit -m "feat: expose router first-seen hook for ui"
```

### Task 2: Expose Watcher Job Callback Driven by Router First-Seen Hook

**Files:**
- Modify: `internal/app/watcher.go`
- Modify: `internal/app/watcher_test.go`

**Step 1: Write the failing test**

```go
func TestWatcher_StartCallsOnJobFoundForFirstSeenEvents(t *testing.T) {
	jobA := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{ID: "job-1", URL: "https://gengo.com/jobs/1"})
	jobADupe := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-1", URL: "https://gengo.com/jobs/1"})

	jobCalls := make(chan gengo.JobEvent, 2)
	w, _ := NewWatcher(&config.Config{}, Deps{
		Monitors: []gengo.Monitor{stubMonitor{name: "m", source: gengo.SourceWebSocket, enabled: true, events: []gengo.JobEvent{jobA, jobADupe}}},
		OnJobFound: func(ev gengo.JobEvent) {
			jobCalls <- ev
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Start(ctx) }()

	select {
	case <-jobCalls:
	case <-time.After(time.Second):
		t.Fatal("expected first-seen callback")
	}

	select {
	case <-jobCalls:
		t.Fatal("expected duplicate suppression for watcher callback")
	case <-time.After(250 * time.Millisecond):
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestWatcher_StartCallsOnJobFoundForFirstSeenEvents -v`
Expected: FAIL with missing `OnJobFound` support.

**Step 3: Write minimal implementation**

```go
type Deps struct {
	// existing fields...
	OnJobFound func(gengo.JobEvent)
}

router := pipeline.NewRouter(
	...,
	pipeline.WithOnFirstSeenJob(deps.OnJobFound),
)
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run 'TestWatcher_StartCallsOnJobFoundForFirstSeenEvents|TestWatcher_StartRoutesMonitorEventsToReactionAndTelemetry' -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/app/watcher.go internal/app/watcher_test.go
git commit -m "feat: stream first-seen watcher events to ui hook"
```

### Task 3: Implement Bubble Tea Model for Jobs + Stats Tabs

**Files:**
- Create: `internal/ui/model.go`
- Create: `internal/ui/model_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write the failing test**

```go
func TestModel_UpdateJobFoundPrependsAndUpdatesStats(t *testing.T) {
	m := NewModel()
	ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{ID: "job-1", Title: "JP -> EN", Reward: 12.5})

	updated, _ := m.Update(jobFoundMsg{Event: ev})
	next := updated.(Model)

	if len(next.jobs) != 1 || next.jobs[0].ID != "job-1" {
		t.Fatalf("expected prepended job, got %#v", next.jobs)
	}
	if next.stats.TotalFound != 1 || next.stats.BySource[gengo.SourceWebSocket] != 1 {
		t.Fatalf("unexpected stats: %#v", next.stats)
	}
}

func TestModel_TabNavigation(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	next := updated.(Model)
	if next.currentTab != tabStats {
		t.Fatalf("expected stats tab, got %v", next.currentTab)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/ui -run 'TestModel_UpdateJobFoundPrependsAndUpdatesStats|TestModel_TabNavigation' -v`
Expected: FAIL because model/package do not exist.

**Step 3: Write minimal implementation**

```go
type tab int

const (
	tabJobs tab = iota
	tabStats
)

type jobFoundMsg struct { Event gengo.JobEvent }

type Model struct {
	jobs       []*gengo.Job
	stats      *gengo.Stats
	currentTab tab
	quitting   bool
}

func NewModel() Model { return Model{stats: gengo.NewStats(), currentTab: tabJobs} }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobFoundMsg:
		m.jobs = append([]*gengo.Job{msg.Event.Job}, m.jobs...)
		if len(m.jobs) > 200 { m.jobs = m.jobs[:200] }
		m.stats.Increment(msg.Event.Source)
		return m, nil
	case tea.KeyMsg:
		// q/ctrl+c quits; 1->jobs, 2->stats
	}
	return m, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/ui -run TestModel -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go go.mod go.sum
git commit -m "feat: add bubbletea model for jobs and stats baseline"
```

### Task 4: Add TUI Runner that Bridges Watcher Events into Bubble Tea

**Files:**
- Create: `internal/ui/tui.go`
- Create: `internal/ui/tui_test.go`
- Modify: `internal/app/watcher.go` (only if export helper needed)

**Step 1: Write the failing test**

```go
func TestRunModel_ConsumesWatcherEvents(t *testing.T) {
	events := make(chan gengo.JobEvent, 1)
	events <- gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-2", Title: "EN -> JA"})

	cmd := waitForJobEvent(events)
	msg := cmd()

	if _, ok := msg.(jobFoundMsg); !ok {
		t.Fatalf("expected jobFoundMsg, got %T", msg)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/ui -run TestRunModel_ConsumesWatcherEvents -v`
Expected: FAIL with missing event bridge helper.

**Step 3: Write minimal implementation**

```go
func waitForJobEvent(events <-chan gengo.JobEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		if ev.Type == gengo.EventJobFound && ev.Job != nil {
			return jobFoundMsg{Event: ev}
		}
		return nil
	}
}

func Run(ctx context.Context, cfg *config.Config) error {
	eventCh := make(chan gengo.JobEvent, 128)
	watcher, err := app.NewWatcher(cfg, app.Deps{
		OnJobFound: func(ev gengo.JobEvent) {
			select {
			case eventCh <- ev:
			default:
			}
		},
	})
	if err != nil { return err }

	go func() { _ = watcher.Start(ctx) }()

	m := NewModel()
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/ui -run 'TestRunModel_ConsumesWatcherEvents|TestModel_UpdateJobFoundPrependsAndUpdatesStats' -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ui/tui.go internal/ui/tui_test.go
git commit -m "feat: bridge watcher first-seen events into tui runtime"
```

### Task 5: Add CLI Entrypoint for Launching TUI

**Files:**
- Create: `cmd/gengowatcher/main.go`
- Create: `cmd/gengowatcher/main_test.go`

**Step 1: Write the failing test**

```go
func TestRun_ReturnsConfigErrorForInvalidFile(t *testing.T) {
	err := run([]string{"-c", "does-not-exist.toml"})
	if err == nil {
		t.Fatal("expected config load error")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/gengowatcher -run TestRun_ReturnsConfigErrorForInvalidFile -v`
Expected: FAIL because command entrypoint does not exist.

**Step 3: Write minimal implementation**

```go
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gengowatcher", flag.ContinueOnError)
	configFile := fs.String("c", "", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !cfg.HasEnabledMonitors() {
		return app.ErrNoEnabledMonitors
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return ui.Run(ctx, cfg)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/gengowatcher -run TestRun_ReturnsConfigErrorForInvalidFile -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/gengowatcher/main.go cmd/gengowatcher/main_test.go
git commit -m "feat: add cli entrypoint for phase 3 tui"
```

### Task 6: Phase 3 Baseline Verification + Status Updates

**Files:**
- Modify: `go-rewrite-plan.md`
- Modify: `docs/project-status.md`
- Modify: `llms.md`
- Modify: `README.md`

**Step 1: Update roadmap/status docs**

```markdown
- Mark Phase 3 as in progress.
- Record TUI baseline scope (Jobs + Stats tabs, live first-seen feed).
- Keep explicit note that command-input/log-viewer/state persistence stay for subsequent phase work.
```

**Step 2: Run focused test suites**

Run: `go test ./internal/ui ./internal/app ./internal/pipeline ./internal/monitor/rss ./internal/monitor/websocket ./internal/reaction -v`
Expected: PASS.

**Step 3: Run CLI package tests**

Run: `go test ./cmd/gengowatcher -v`
Expected: PASS.

**Step 4: Run full suite**

Run: `go test ./...`
Expected: PASS.

**Step 5: Verify working tree and commit**

```bash
git status --short
git add go-rewrite-plan.md docs/project-status.md llms.md README.md
git commit -m "docs: mark phase 3 tui baseline as started"
```
