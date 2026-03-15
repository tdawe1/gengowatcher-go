package ui

import (
	"context"
	"log"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tdawe1/gengowatcher-go/internal/app"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/internal/notify"
	"github.com/tdawe1/gengowatcher-go/internal/state"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const tuiEventBuffer = 128

var watcherJoinTimeout = 2 * time.Second
var watcherPostTimeoutDrainWindow = 100 * time.Millisecond

var droppedEventReportInterval = 250 * time.Millisecond

var reportDroppedEvents = func(count uint64) {
	log.Printf("ui events dropped: count=%d", count)
}

var reportWatcherJoinTimeout = func(timeout time.Duration) {
	log.Printf("watcher shutdown exceeded join timeout: timeout=%s", timeout)
}

var reportStateError = func(err error) {
	log.Printf("state persistence error: %v", err)
}

var reportStateDrop = func() {
	log.Printf("state recorder queue saturated; dropping job event")
}

type watcherRunner interface {
	Start(context.Context) error
}

type teaProgramRunner interface {
	Run() (tea.Model, error)
	Quit()
	Send(tea.Msg)
}

var newWatcher = func(cfg *config.Config, deps app.Deps) (watcherRunner, error) {
	return app.NewWatcher(cfg, deps)
}

var newTeaProgram = func(model tea.Model) teaProgramRunner {
	return tea.NewProgram(model)
}

func waitForJobEvent(events <-chan gengo.JobEvent) tea.Cmd {
	return waitForJobEventWithDone(nil, events)
}

func waitForJobEventWithDone(done <-chan struct{}, events <-chan gengo.JobEvent) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-done:
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}

			if ev.Type == gengo.EventJobFound && ev.Job != nil {
				return jobFoundMsg{Event: ev}
			}

			return nil
		}
	}
}

func Run(ctx context.Context, cfg *config.Config) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan gengo.JobEvent, tuiEventBuffer)
	var droppedEvents atomic.Uint64
	notifier := notify.New(cfg.Notifications)
	var recorder *state.Recorder
	model := NewModel()

	if path := strings.TrimSpace(cfg.State.File); path != "" {
		var err error
		recorder, err = state.OpenRecorder(path, state.RecorderDeps{
			OnError: reportStateError,
			MaxJobs: maxJobs,
		})
		if err != nil {
			return err
		}
		defer func() {
			_ = recorder.Close()
		}()
		model = NewModelFromSnapshot(recorder.Snapshot())
	}

	watcher, err := newWatcher(cfg, app.Deps{
		Open:   notifier.Open,
		Notify: notifier.Notify,
		OnJobFound: func(ev gengo.JobEvent) {
			if recorder != nil && !recorder.Record(ev) {
				reportStateDrop()
			}
			select {
			case events <- ev:
			default:
				droppedEvents.Add(1)
			}
		},
	})
	if err != nil {
		return err
	}

	interval := droppedEventReportInterval
	if interval <= 0 {
		interval = time.Second
	}

	dropReporterDone := make(chan struct{})
	stopDropReporter := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(dropReporterDone)

		reportDrops := func() {
			if dropped := droppedEvents.Swap(0); dropped > 0 {
				reportDroppedEvents(dropped)
			}
		}

		for {
			select {
			case <-stopDropReporter:
				reportDrops()
				return
			case <-ticker.C:
				reportDrops()
			}
		}
	}()

	model.events = events
	model.done = runCtx.Done()
	program := newTeaProgram(model)
	watcherErr := make(chan error, 1)

	go func() {
		werr := watcher.Start(runCtx)
		if werr != nil {
			cancel()
			program.Quit()
		}
		watcherErr <- werr
	}()

	go func() {
		<-runCtx.Done()
		program.Quit()
	}()

	_, err = program.Run()
	cancel()

	joinTimer := time.NewTimer(watcherJoinTimeout)
	defer joinTimer.Stop()

	select {
	case werr := <-watcherErr:
		if err == nil && werr != nil {
			close(stopDropReporter)
			<-dropReporterDone
			return werr
		}
	case <-joinTimer.C:
		reportWatcherJoinTimeout(watcherJoinTimeout)

		drainWindow := watcherPostTimeoutDrainWindow
		if drainWindow > 0 {
			drainTimer := time.NewTimer(drainWindow)
			select {
			case werr := <-watcherErr:
				if err == nil && werr != nil {
					close(stopDropReporter)
					<-dropReporterDone
					return werr
				}
			case <-drainTimer.C:
			}
			drainTimer.Stop()
		}
	}

	close(stopDropReporter)
	<-dropReporterDone

	return err
}
