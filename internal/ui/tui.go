package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tdawe1/gengowatcher-go/internal/app"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const tuiEventBuffer = 128

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
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan gengo.JobEvent, tuiEventBuffer)

	watcher, err := newWatcher(cfg, app.Deps{
		OnJobFound: func(ev gengo.JobEvent) {
			select {
			case events <- ev:
			default:
			}
		},
	})
	if err != nil {
		return err
	}

	model := NewModel()
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

	select {
	case werr := <-watcherErr:
		if err == nil && werr != nil {
			return werr
		}
	case <-time.After(2 * time.Second):
		if err == nil {
			return context.DeadlineExceeded
		}
	}

	return err
}
