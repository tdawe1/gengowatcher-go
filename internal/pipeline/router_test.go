package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/dedupe"
	"github.com/tdawe1/gengowatcher-go/internal/reaction"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestRouter_FirstSeenWinsAcrossSources(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 2)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	router := NewRouter(gate, exec, nil)

	job := &gengo.Job{ID: "job-42", Title: "JP -> EN", URL: "https://gengo.com/jobs/42"}
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, job))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, job))

	select {
	case <-openCalls:
	case <-time.After(1 * time.Second):
		t.Fatal("expected one reaction dispatch")
	}

	select {
	case <-openCalls:
		t.Fatal("expected dedupe to suppress second dispatch")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestRouter_TelemetryFailureDoesNotBlockDispatch(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalled := make(chan struct{}, 1)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			select {
			case openCalled <- struct{}{}:
			default:
			}
			return nil
		},
	})

	telemetryCalled := make(chan struct{}, 1)
	releaseTelemetry := make(chan struct{})
	router := NewRouter(gate, exec, telemetrySinkFunc(func(context.Context, TelemetryEvent) error {
		select {
		case telemetryCalled <- struct{}{}:
		default:
		}

		<-releaseTelemetry
		return errors.New("telemetry write failed")
	}))

	handleDone := make(chan struct{})
	go func() {
		router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
			ID:    "job-7",
			Title: "Fast path",
			URL:   "https://gengo.com/jobs/7",
		}))
		close(handleDone)
	}()

	select {
	case <-openCalled:
	case <-time.After(1 * time.Second):
		t.Fatal("expected reaction dispatch despite telemetry failure")
	}

	select {
	case <-telemetryCalled:
	case <-time.After(1 * time.Second):
		t.Fatal("expected telemetry write attempt")
	}

	select {
	case <-handleDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handle should return without waiting for telemetry")
	}

	close(releaseTelemetry)
}

func TestRouter_IgnoresNonJobFoundOrNilJobEvents(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 1)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	telemetryCalls := make(chan struct{}, 1)
	router := NewRouter(gate, exec, telemetrySinkFunc(func(context.Context, TelemetryEvent) error {
		telemetryCalls <- struct{}{}
		return nil
	}))

	router.Handle(context.Background(), gengo.NewErrorEvent(gengo.SourceRSS, "boom"))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, nil))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "", Title: "missing id"}))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobExpired, gengo.SourceWebSocket, &gengo.Job{ID: "job-expired"}))

	select {
	case <-openCalls:
		t.Fatal("expected no reaction dispatches")
	case <-time.After(250 * time.Millisecond):
	}

	select {
	case <-telemetryCalls:
		t.Fatal("expected no telemetry writes")
	case <-time.After(250 * time.Millisecond):
	}
}

type telemetrySinkFunc func(context.Context, TelemetryEvent) error

func (f telemetrySinkFunc) Write(ctx context.Context, event TelemetryEvent) error {
	return f(ctx, event)
}
