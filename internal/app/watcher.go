package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/internal/dedupe"
	rssmonitor "github.com/tdawe1/gengowatcher-go/internal/monitor/rss"
	wsmonitor "github.com/tdawe1/gengowatcher-go/internal/monitor/websocket"
	"github.com/tdawe1/gengowatcher-go/internal/pipeline"
	"github.com/tdawe1/gengowatcher-go/internal/reaction"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const (
	defaultEventBuffer         = 128
	defaultRouterDedupeTTL     = 72 * time.Hour
	defaultRouterDedupeCap     = 50_000
	defaultTelemetryInFlight   = 64
	defaultShutdownWaitTimeout = 2 * time.Second
)

var ErrNoEnabledMonitors = errors.New("no enabled monitors configured")
var ErrShutdownTimeout = errors.New("watcher shutdown wait timed out")

type Deps struct {
	Monitors []gengo.Monitor

	Open   func(context.Context, string) error
	Notify func(context.Context, string) error

	TelemetrySink     pipeline.TelemetrySink
	OnTelemetryError  func(error)
	OnReactionError   func(action string, err error)
	OnReactionPanic   func(action string, recovered any)
	ReactionInFlight  int
	TelemetryInFlight int

	EventBuffer         int
	RouterDedupeTTL     time.Duration
	RouterDedupeCap     int
	ShutdownWaitTimeout time.Duration
}

type Watcher struct {
	monitors            []gengo.Monitor
	router              *pipeline.Router
	eventBuffer         int
	shutdownWaitTimeout time.Duration
}

func NewWatcher(cfg *config.Config, deps Deps) (*Watcher, error) {
	if cfg == nil {
		cfg = &config.Config{}
	}

	monitors := deps.Monitors
	if len(monitors) == 0 {
		monitors = []gengo.Monitor{
			wsmonitor.New(cfg.WebSocket),
			rssmonitor.New(cfg.RSS, cfg.Watcher.CheckInterval, cfg.Watcher.MinReward),
		}
	}

	reactionExec := reaction.New(reaction.Deps{
		Open:         deps.Open,
		Notify:       deps.Notify,
		MaxInFlight:  deps.ReactionInFlight,
		OnAsyncError: deps.OnReactionError,
		OnAsyncPanic: deps.OnReactionPanic,
	})

	dedupeTTL := deps.RouterDedupeTTL
	if dedupeTTL <= 0 {
		dedupeTTL = defaultRouterDedupeTTL
	}

	dedupeCap := deps.RouterDedupeCap
	if dedupeCap <= 0 {
		dedupeCap = defaultRouterDedupeCap
	}

	telemetryInFlight := deps.TelemetryInFlight
	if telemetryInFlight <= 0 {
		telemetryInFlight = defaultTelemetryInFlight
	}

	router := pipeline.NewRouter(
		dedupe.New(dedupeTTL, dedupeCap),
		reactionExec,
		deps.TelemetrySink,
		pipeline.WithTelemetryMaxInFlight(telemetryInFlight),
		pipeline.WithTelemetryErrorHook(deps.OnTelemetryError),
	)

	eventBuffer := deps.EventBuffer
	if eventBuffer <= 0 {
		eventBuffer = defaultEventBuffer
	}

	shutdownWaitTimeout := deps.ShutdownWaitTimeout
	if shutdownWaitTimeout <= 0 {
		shutdownWaitTimeout = defaultShutdownWaitTimeout
	}

	return &Watcher{
		monitors:            monitors,
		router:              router,
		eventBuffer:         eventBuffer,
		shutdownWaitTimeout: shutdownWaitTimeout,
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	if w == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	enabled := make([]gengo.Monitor, 0, len(w.monitors))
	for _, mon := range w.monitors {
		if mon == nil || !mon.Enabled() {
			continue
		}
		enabled = append(enabled, mon)
	}

	if len(enabled) == 0 {
		return ErrNoEnabledMonitors
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan gengo.JobEvent, w.eventBuffer)
	errCh := make(chan error, len(enabled))

	var wg sync.WaitGroup
	for _, mon := range enabled {
		monitor := mon
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := monitor.Start(runCtx, events); err != nil && runCtx.Err() == nil {
				select {
				case errCh <- fmt.Errorf("monitor %s failed: %w", monitor.Name(), err):
				default:
				}
			}
		}()
	}

	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()

	for {
		select {
		case <-runCtx.Done():
			return w.finishShutdown(cancel, stopped, nil)
		case err := <-errCh:
			if err != nil {
				return w.finishShutdown(cancel, stopped, err)
			}
		case ev := <-events:
			w.router.Handle(runCtx, ev)
		}
	}
}

func (w *Watcher) finishShutdown(cancel context.CancelFunc, stopped <-chan struct{}, baseErr error) error {
	if cancel != nil {
		cancel()
	}

	timeout := w.shutdownWaitTimeout
	if timeout <= 0 {
		timeout = defaultShutdownWaitTimeout
	}

	select {
	case <-stopped:
		return baseErr
	case <-time.After(timeout):
		if baseErr != nil {
			return fmt.Errorf("%w: %w", baseErr, ErrShutdownTimeout)
		}
		return ErrShutdownTimeout
	}
}
