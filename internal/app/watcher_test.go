package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/internal/pipeline"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestWatcher_StartRoutesMonitorEventsToReactionAndTelemetry(t *testing.T) {
	jobEvent := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		ID:    "job-101",
		Title: "JP -> EN",
		URL:   "https://gengo.com/jobs/101",
	})

	openCalled := make(chan string, 1)
	telemetryCalled := make(chan pipeline.TelemetryEvent, 1)

	w, err := NewWatcher(&config.Config{}, Deps{
		Monitors: []gengo.Monitor{
			stubMonitor{
				name:    "stub",
				source:  gengo.SourceWebSocket,
				enabled: true,
				events:  []gengo.JobEvent{jobEvent},
			},
		},
		Open: func(context.Context, string) error {
			openCalled <- "open"
			return nil
		},
		TelemetrySink: telemetrySinkFunc(func(_ context.Context, event pipeline.TelemetryEvent) error {
			telemetryCalled <- event
			return nil
		}),
		EventBuffer: 4,
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	select {
	case <-openCalled:
	case <-time.After(time.Second):
		t.Fatal("expected reaction open dispatch")
	}

	select {
	case event := <-telemetryCalled:
		if event.JobID != "job-101" {
			t.Fatalf("expected telemetry job id job-101, got %q", event.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected telemetry write")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error on cancel, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected watcher Start to stop after cancel")
	}
}

func TestWatcher_StartReturnsFatalMonitorError(t *testing.T) {
	fatalErr := errors.New("fatal monitor failure")

	w, err := NewWatcher(&config.Config{}, Deps{
		Monitors: []gengo.Monitor{
			stubMonitor{
				name:     "fatal",
				source:   gengo.SourceRSS,
				enabled:  true,
				startErr: fatalErr,
			},
		},
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	err = w.Start(context.Background())
	if err == nil {
		t.Fatal("expected fatal monitor error")
	}
	if !strings.Contains(err.Error(), fatalErr.Error()) {
		t.Fatalf("expected error to include %q, got %q", fatalErr.Error(), err.Error())
	}
}

func TestWatcher_StartReturnsFatalErrorWithoutHangingOnStuckMonitor(t *testing.T) {
	fatalErr := errors.New("fatal monitor failure")
	stuckRelease := make(chan struct{})
	defer close(stuckRelease)

	w, err := NewWatcher(&config.Config{}, Deps{
		Monitors: []gengo.Monitor{
			stubMonitor{
				name:     "fatal",
				source:   gengo.SourceRSS,
				enabled:  true,
				startErr: fatalErr,
			},
			stubMonitor{
				name:         "stuck",
				source:       gengo.SourceWebSocket,
				enabled:      true,
				ignoreCancel: true,
				blockUntil:   stuckRelease,
			},
		},
		ShutdownWaitTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(context.Background())
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected fatal monitor error")
		}
		if !strings.Contains(err.Error(), fatalErr.Error()) {
			t.Fatalf("expected error to include %q, got %q", fatalErr.Error(), err.Error())
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected Start to return quickly even with stuck monitor")
	}
}

type stubMonitor struct {
	name         string
	source       gengo.Source
	enabled      bool
	events       []gengo.JobEvent
	startErr     error
	ignoreCancel bool
	blockUntil   chan struct{}
}

func (m stubMonitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	if m.startErr != nil {
		return m.startErr
	}

	if m.blockUntil != nil {
		if m.ignoreCancel {
			<-m.blockUntil
			return nil
		}

		select {
		case <-m.blockUntil:
			return nil
		case <-ctx.Done():
			return nil
		}
	}

	for _, ev := range m.events {
		select {
		case events <- ev:
		case <-ctx.Done():
			return nil
		}
	}

	<-ctx.Done()
	return nil
}

func (m stubMonitor) Name() string {
	return m.name
}

func (m stubMonitor) Source() gengo.Source {
	return m.source
}

func (m stubMonitor) Enabled() bool {
	return m.enabled
}

type telemetrySinkFunc func(context.Context, pipeline.TelemetryEvent) error

func (f telemetrySinkFunc) Write(ctx context.Context, event pipeline.TelemetryEvent) error {
	return f(ctx, event)
}
