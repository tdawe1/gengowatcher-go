package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/dedupe"
	"github.com/tdawe1/gengowatcher-go/internal/reaction"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type TelemetryEvent struct {
	JobID                string
	Source               gengo.Source
	DetectedAt           time.Time
	DetectedToDispatchMS float64
}

type TelemetrySink interface {
	Write(ctx context.Context, event TelemetryEvent) error
}

type Router struct {
	gate      *dedupe.Gate
	executor  *reaction.Executor
	telemetry TelemetrySink
}

func NewRouter(gate *dedupe.Gate, executor *reaction.Executor, telemetry TelemetrySink) *Router {
	return &Router{
		gate:      gate,
		executor:  executor,
		telemetry: telemetry,
	}
}

func (r *Router) Handle(ctx context.Context, ev gengo.JobEvent) {
	if r == nil {
		return
	}

	if ev.Type != gengo.EventJobFound || ev.Job == nil {
		return
	}

	if strings.TrimSpace(ev.Job.ID) == "" {
		return
	}

	if !r.firstSeen(ev.Job.ID) {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}

	if r.executor != nil {
		r.executor.Dispatch(ctx, ev.Job.URL, ev.Job.Title)
	}

	if r.telemetry != nil {
		r.writeTelemetryAsync(ev)
	}
}

func (r *Router) firstSeen(jobID string) bool {
	if r.gate == nil {
		return true
	}

	return r.gate.FirstSeen(jobID)
}

func (r *Router) writeTelemetryAsync(ev gengo.JobEvent) {
	telem := TelemetryEvent{
		JobID:                ev.Job.ID,
		Source:               ev.Source,
		DetectedAt:           ev.Time,
		DetectedToDispatchMS: toMilliseconds(time.Since(ev.Time)),
	}

	go func() {
		_ = r.telemetry.Write(context.Background(), telem)
	}()
}

func toMilliseconds(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}

	return float64(d) / float64(time.Millisecond)
}
