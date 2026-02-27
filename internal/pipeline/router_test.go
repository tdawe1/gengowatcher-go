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

func TestRouter_FirstSeenHookPanicDoesNotBreakDispatch(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 1)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	router := NewRouter(
		gate,
		exec,
		nil,
		WithOnFirstSeenJob(func(gengo.JobEvent) {
			panic("hook exploded")
		}),
	)

	panicValue := make(chan any, 1)
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicValue <- recovered
			}
		}()

		router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
			ID:    "job-panic",
			Title: "panic-safe dispatch",
			URL:   "https://gengo.com/jobs/panic",
		}))
	}()

	select {
	case recovered := <-panicValue:
		t.Fatalf("expected first-seen hook panic to be recovered by router, got %v", recovered)
	default:
	}

	select {
	case <-openCalls:
	case <-time.After(1 * time.Second):
		t.Fatal("expected reaction dispatch even when first-seen hook panics")
	}
}

func TestRouter_FirstSeenFallsBackToURLDerivedKeyWhenIDMissing(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 2)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	router := NewRouter(gate, exec, nil)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		Title: "First URL sighting",
		URL:   "https://gengo.com/t/jobs/details/42?from=ws",
	}))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{
		Title: "Second URL sighting",
		URL:   "https://gengo.com/jobs/42?from=rss",
	}))

	select {
	case <-openCalls:
	case <-time.After(1 * time.Second):
		t.Fatal("expected one reaction dispatch")
	}

	select {
	case <-openCalls:
		t.Fatal("expected dedupe to suppress duplicate via url-derived key")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestRouter_URLFallbackIncludesQueryWhenPathHasNoNumericID(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 2)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	router := NewRouter(gate, exec, nil)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		Title: "Query based 1",
		URL:   "https://gengo.com/jobs?job_id=101",
	}))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{
		Title: "Query based 2",
		URL:   "https://gengo.com/jobs?job_id=202",
	}))

	for i := 0; i < 2; i++ {
		select {
		case <-openCalls:
		case <-time.After(1 * time.Second):
			t.Fatalf("expected two distinct reaction dispatches, observed %d", i)
		}
	}
}

func TestRouter_FirstSeenFallsBackToFingerprintWhenIDAndURLMissing(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	openCalls := make(chan struct{}, 2)
	exec := reaction.New(reaction.Deps{
		Open: func(context.Context, string) error {
			openCalls <- struct{}{}
			return nil
		},
	})

	router := NewRouter(gate, exec, nil)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		Title:     "EN -> JA product listing",
		Language:  "en -> ja",
		Reward:    7.25,
		UnitCount: 180,
	}))
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{
		Title:     "EN -> JA product listing",
		Language:  "en -> ja",
		Reward:    7.25,
		UnitCount: 180,
	}))

	select {
	case <-openCalls:
	case <-time.After(1 * time.Second):
		t.Fatal("expected one reaction dispatch")
	}

	select {
	case <-openCalls:
		t.Fatal("expected dedupe to suppress duplicate via fingerprint key")
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
	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "", Title: ""}))
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

func TestRouter_TelemetryFailureInvokesErrorHook(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	errorEvents := make(chan error, 1)
	router := NewRouter(
		gate,
		nil,
		telemetrySinkFunc(func(context.Context, TelemetryEvent) error {
			return errors.New("write exploded")
		}),
		WithTelemetryErrorHook(func(err error) {
			errorEvents <- err
		}),
	)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{ID: "job-88", URL: "https://gengo.com/jobs/88"}))

	select {
	case err := <-errorEvents:
		if err == nil {
			t.Fatal("expected telemetry error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected telemetry error hook callback")
	}
}

func TestRouter_TelemetryBackpressureReportsDrop(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	telemetryStarted := make(chan struct{}, 1)
	releaseTelemetry := make(chan struct{})
	errorEvents := make(chan error, 2)

	router := NewRouter(
		gate,
		nil,
		telemetrySinkFunc(func(context.Context, TelemetryEvent) error {
			select {
			case telemetryStarted <- struct{}{}:
			default:
			}

			<-releaseTelemetry
			return nil
		}),
		WithTelemetryMaxInFlight(1),
		WithTelemetryErrorHook(func(err error) {
			errorEvents <- err
		}),
	)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		ID:    "job-1",
		URL:   "https://gengo.com/jobs/1",
		Title: "first",
	}))

	select {
	case <-telemetryStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected first telemetry write to start")
	}

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{
		ID:    "job-2",
		URL:   "https://gengo.com/jobs/2",
		Title: "second",
	}))

	select {
	case err := <-errorEvents:
		if !errors.Is(err, ErrTelemetryBackpressure) {
			t.Fatalf("expected backpressure error, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected telemetry backpressure callback")
	}

	close(releaseTelemetry)
}

func TestRouter_TelemetryWriteUsesBoundedContext(t *testing.T) {
	gate := dedupe.New(time.Hour, 32)

	errorEvents := make(chan error, 1)
	deadlineSet := make(chan bool, 1)

	router := NewRouter(
		gate,
		nil,
		telemetrySinkFunc(func(ctx context.Context, _ TelemetryEvent) error {
			_, hasDeadline := ctx.Deadline()
			deadlineSet <- hasDeadline

			<-ctx.Done()
			return ctx.Err()
		}),
		WithTelemetryWriteTimeout(25*time.Millisecond),
		WithTelemetryErrorHook(func(err error) {
			errorEvents <- err
		}),
	)

	router.Handle(context.Background(), gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		ID:    "job-ctx-timeout",
		URL:   "https://gengo.com/jobs/ctx-timeout",
		Title: "ctx timeout",
	}))

	select {
	case hasDeadline := <-deadlineSet:
		if !hasDeadline {
			t.Fatal("expected telemetry write context to include deadline")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected telemetry sink call")
	}

	select {
	case err := <-errorEvents:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context deadline exceeded, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected telemetry timeout error callback")
	}
}

type telemetrySinkFunc func(context.Context, TelemetryEvent) error

func (f telemetrySinkFunc) Write(ctx context.Context, event TelemetryEvent) error {
	return f(ctx, event)
}
