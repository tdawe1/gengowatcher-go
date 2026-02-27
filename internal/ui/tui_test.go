package ui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tdawe1/gengowatcher-go/internal/app"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestWaitForJobEvent_ConsumesWatcherEvents(t *testing.T) {
	events := make(chan gengo.JobEvent, 1)
	events <- gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-2", Title: "EN -> JA"})

	cmd := waitForJobEvent(events)
	msg := cmd()

	if _, ok := msg.(jobFoundMsg); !ok {
		t.Fatalf("expected jobFoundMsg, got %T", msg)
	}
}

type stubWatcher struct {
	started chan struct{}
}

func (s *stubWatcher) Start(ctx context.Context) error {
	select {
	case s.started <- struct{}{}:
	default:
	}

	<-ctx.Done()
	return nil
}

type stubProgram struct {
	run  func() (tea.Model, error)
	quit func()
	send func(tea.Msg)
}

func (s stubProgram) Run() (tea.Model, error) {
	return s.run()
}

func (s stubProgram) Quit() {
	if s.quit != nil {
		s.quit()
	}
}

func (s stubProgram) Send(msg tea.Msg) {
	if s.send != nil {
		s.send(msg)
	}
}

func TestRun_StartsWatcherAndBridgesEvents(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := &stubWatcher{started: make(chan struct{}, 1)}
	var onJobFound func(gengo.JobEvent)

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		onJobFound = deps.OnJobFound
		return watcher, nil
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		m, ok := model.(Model)
		if !ok {
			t.Fatalf("expected Run to use NewModel directly, got %T", model)
		}
		if m.events == nil {
			t.Fatal("expected model events channel to be wired")
		}

		return stubProgram{run: func() (tea.Model, error) {
			if onJobFound == nil {
				t.Fatal("expected OnJobFound callback to be set")
			}

			ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-2", Title: "EN -> JA"})
			onJobFound(ev)

			cmd := m.Init()
			if cmd == nil {
				t.Fatal("expected init command to wait for job events")
			}

			msg := cmd()
			forwarded, ok := msg.(jobFoundMsg)
			if !ok {
				t.Fatalf("expected jobFoundMsg from init cmd, got %T", msg)
			}
			if forwarded.Event.Job == nil || forwarded.Event.Job.ID != "job-2" {
				t.Fatalf("unexpected forwarded event payload: %#v", forwarded)
			}

			cancel()
			return model, nil
		}, send: nil}
	}

	err := Run(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	select {
	case <-watcher.started:
	case <-time.After(time.Second):
		t.Fatal("expected watcher Start to be invoked")
	}
}

func TestRun_ContextCancelQuitsProgram(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := &stubWatcher{started: make(chan struct{}, 1)}
	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		return watcher, nil
	}

	quitCalled := make(chan struct{}, 1)
	runReleased := make(chan struct{}, 1)

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{
			run: func() (tea.Model, error) {
				<-runReleased
				return model, nil
			},
			quit: func() {
				select {
				case quitCalled <- struct{}{}:
				default:
				}
				select {
				case runReleased <- struct{}{}:
				default:
				}
			},
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, &config.Config{})
	}()

	select {
	case <-watcher.started:
	case <-time.After(time.Second):
		t.Fatal("expected watcher Start to be invoked")
	}

	cancel()

	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("expected program quit to be called on context cancel")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after context cancel")
	}
}

func TestRun_PropagatesWatcherFatalError(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
	})

	fatalErr := errors.New("watcher exploded")
	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		return watcherRunnerFunc(func(context.Context) error {
			return fatalErr
		}), nil
	}

	runReleased := make(chan struct{}, 1)
	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{
			run: func() (tea.Model, error) {
				<-runReleased
				return model, nil
			},
			quit: func() {
				select {
				case runReleased <- struct{}{}:
				default:
				}
			},
		}
	}

	err := Run(context.Background(), &config.Config{})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("expected watcher fatal error, got %v", err)
	}
}

func TestRun_ReportsDroppedEventsWhenBufferSaturated(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevReportDrop := reportDroppedEvents
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		reportDroppedEvents = prevReportDrop
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := &stubWatcher{started: make(chan struct{}, 1)}
	var onJobFound func(gengo.JobEvent)

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		onJobFound = deps.OnJobFound
		return watcher, nil
	}

	var reportedDrops atomic.Uint64
	var reportCalls atomic.Int32
	reportDroppedEvents = func(count uint64) {
		reportCalls.Add(1)
		reportedDrops.Add(count)
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			if onJobFound == nil {
				t.Fatal("expected OnJobFound callback to be set")
			}

			ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-drop", Title: "drop"})
			for i := range tuiEventBuffer + 64 {
				onJobFound(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: ev.Job.ID, Title: ev.Job.Title, Reward: float64(i)}))
			}

			cancel()
			return model, nil
		}}
	}

	err := Run(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	expectedDrops := 64
	if reportedDrops.Load() == 0 {
		t.Fatal("expected dropped event reports when buffer saturates")
	}
	if calls := reportCalls.Load(); calls >= int32(expectedDrops) {
		t.Fatalf("expected aggregated drop reporting, got calls=%d drops=%d", calls, expectedDrops)
	}
}

func TestRun_ReportsWatcherJoinTimeout(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevJoinTimeout := watcherJoinTimeout
	prevReportJoinTimeout := reportWatcherJoinTimeout
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		watcherJoinTimeout = prevJoinTimeout
		reportWatcherJoinTimeout = prevReportJoinTimeout
	})

	watcherJoinTimeout = 20 * time.Millisecond
	releaseWatcher := make(chan struct{})
	defer close(releaseWatcher)

	var timeoutReports atomic.Int32
	reportWatcherJoinTimeout = func(timeout time.Duration) {
		if timeout != watcherJoinTimeout {
			t.Fatalf("expected timeout report to use watcherJoinTimeout, got %s", timeout)
		}
		timeoutReports.Add(1)
	}

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		return watcherRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			<-releaseWatcher
			return nil
		}), nil
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			return model, nil
		}}
	}

	err := Run(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error on join timeout, got %v", err)
	}
	if timeoutReports.Load() != 1 {
		t.Fatalf("expected one timeout report, got %d", timeoutReports.Load())
	}
}

func TestRun_AggregatesDropReportsUnderBurst(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevReportDrop := reportDroppedEvents
	prevDropInterval := droppedEventReportInterval
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		reportDroppedEvents = prevReportDrop
		droppedEventReportInterval = prevDropInterval
	})

	droppedEventReportInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := &stubWatcher{started: make(chan struct{}, 1)}
	var onJobFound func(gengo.JobEvent)

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		onJobFound = deps.OnJobFound
		return watcher, nil
	}

	var reportCalls atomic.Int32
	var reportedDrops atomic.Uint64
	reportDroppedEvents = func(count uint64) {
		reportCalls.Add(1)
		reportedDrops.Add(count)
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			if onJobFound == nil {
				t.Fatal("expected OnJobFound callback to be set")
			}

			totalEvents := tuiEventBuffer + 64
			for i := range totalEvents {
				onJobFound(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-burst", Title: "burst", Reward: float64(i)}))
			}

			cancel()

			return model, nil
		}}
	}

	err := Run(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	totalEvents := tuiEventBuffer + 64
	expectedDrops := totalEvents - tuiEventBuffer
	if got := int(reportedDrops.Load()); got != expectedDrops {
		t.Fatalf("expected aggregated drop reports to equal drops, got=%d expected=%d", got, expectedDrops)
	}
	if calls := reportCalls.Load(); calls >= int32(expectedDrops) {
		t.Fatalf("expected aggregated reporting, got per-drop behavior with %d calls", calls)
	}
}

func TestRun_ReportsDropsThatHappenAfterCancel(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevReportDrop := reportDroppedEvents
	prevDropInterval := droppedEventReportInterval
	prevJoinTimeout := watcherJoinTimeout
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		reportDroppedEvents = prevReportDrop
		droppedEventReportInterval = prevDropInterval
		watcherJoinTimeout = prevJoinTimeout
	})

	droppedEventReportInterval = time.Hour
	watcherJoinTimeout = 250 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var onJobFound func(gengo.JobEvent)
	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		onJobFound = deps.OnJobFound
		return watcherRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(25 * time.Millisecond)
			for i := range tuiEventBuffer + 32 {
				onJobFound(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-late-drop", Title: "late drop", Reward: float64(i)}))
			}
			return nil
		}), nil
	}

	var reportedDrops atomic.Uint64
	var reportCalls atomic.Int32
	reportDroppedEvents = func(count uint64) {
		reportCalls.Add(1)
		reportedDrops.Add(count)
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			return model, nil
		}}
	}

	err := Run(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if reportedDrops.Load() == 0 {
		t.Fatal("expected drop reports for post-cancel drops")
	}
}

func TestRun_ReportsDropsThatHappenAfterJoinTimeout(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevReportDrop := reportDroppedEvents
	prevDropInterval := droppedEventReportInterval
	prevJoinTimeout := watcherJoinTimeout
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		reportDroppedEvents = prevReportDrop
		droppedEventReportInterval = prevDropInterval
		watcherJoinTimeout = prevJoinTimeout
	})

	droppedEventReportInterval = time.Hour
	watcherJoinTimeout = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var onJobFound func(gengo.JobEvent)
	dropsEmitted := make(chan struct{})

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		onJobFound = deps.OnJobFound
		return watcherRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(40 * time.Millisecond)
			for i := range tuiEventBuffer + 32 {
				onJobFound(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-timeout-drop", Title: "timeout drop", Reward: float64(i)}))
			}
			close(dropsEmitted)
			return nil
		}), nil
	}

	var reportedDrops atomic.Uint64
	var reportCalls atomic.Int32
	reportDroppedEvents = func(count uint64) {
		reportCalls.Add(1)
		reportedDrops.Add(count)
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			return model, nil
		}}
	}

	err := Run(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	select {
	case <-dropsEmitted:
	case <-time.After(time.Second):
		t.Fatal("expected watcher to emit post-timeout drops")
	}

	if reportedDrops.Load() == 0 {
		t.Fatal("expected drop reports for post-timeout drops")
	}
	if calls := reportCalls.Load(); calls >= int32(reportedDrops.Load()) {
		t.Fatalf("expected aggregated post-timeout reporting, got calls=%d drops=%d", calls, reportedDrops.Load())
	}
}

func TestRun_PropagatesWatcherErrorDuringPostTimeoutDrain(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevJoinTimeout := watcherJoinTimeout
	prevDrainWindow := watcherPostTimeoutDrainWindow
	prevReportJoinTimeout := reportWatcherJoinTimeout
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		watcherJoinTimeout = prevJoinTimeout
		watcherPostTimeoutDrainWindow = prevDrainWindow
		reportWatcherJoinTimeout = prevReportJoinTimeout
	})

	watcherJoinTimeout = 20 * time.Millisecond
	watcherPostTimeoutDrainWindow = 100 * time.Millisecond

	lateWatcherErr := errors.New("late watcher fatal")
	timeoutReported := make(chan struct{}, 1)
	reportWatcherJoinTimeout = func(time.Duration) {
		select {
		case timeoutReported <- struct{}{}:
		default:
		}
	}

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		return watcherRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			<-timeoutReported
			return lateWatcherErr
		}), nil
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			return model, nil
		}}
	}

	err := Run(context.Background(), &config.Config{})
	if !errors.Is(err, lateWatcherErr) {
		t.Fatalf("expected late watcher error during drain, got %v", err)
	}
}

func TestRun_DoesNotReturnDeadlineExceededOnCleanQuit(t *testing.T) {
	prevNewWatcher := newWatcher
	prevNewTeaProgram := newTeaProgram
	prevJoinTimeout := watcherJoinTimeout
	t.Cleanup(func() {
		newWatcher = prevNewWatcher
		newTeaProgram = prevNewTeaProgram
		watcherJoinTimeout = prevJoinTimeout
	})

	watcherJoinTimeout = 20 * time.Millisecond

	newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
		return watcherRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(60 * time.Millisecond)
			return nil
		}), nil
	}

	newTeaProgram = func(model tea.Model) teaProgramRunner {
		return stubProgram{run: func() (tea.Model, error) {
			return model, nil
		}}
	}

	err := Run(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("expected nil error on clean quit, got %v", err)
	}
}

type watcherRunnerFunc func(context.Context) error

func (f watcherRunnerFunc) Start(ctx context.Context) error {
	return f(ctx)
}
