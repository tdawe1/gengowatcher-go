package reaction

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutor_DispatchesOpenAndNotifyInParallel(t *testing.T) {
	var openCalled atomic.Bool
	var notifyCalled atomic.Bool

	openStarted := make(chan struct{})
	notifyStarted := make(chan struct{})
	release := make(chan struct{})

	exec := New(Deps{
		Open: func(context.Context, string) error {
			openCalled.Store(true)
			close(openStarted)
			<-release
			return nil
		},
		Notify: func(context.Context, string) error {
			notifyCalled.Store(true)
			close(notifyStarted)
			<-release
			return nil
		},
	})

	start := time.Now()
	exec.Dispatch(context.Background(), "https://gengo.com/t/jobs/details/1", "job")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("dispatch should return quickly, took %s", elapsed)
	}

	select {
	case <-openStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected open action to start")
	}

	select {
	case <-notifyStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected notify action to start")
	}

	close(release)

	if !openCalled.Load() || !notifyCalled.Load() {
		t.Fatal("expected both actions")
	}
}

func TestExecutor_DispatchNilDependenciesDoesNotPanic(t *testing.T) {
	exec := New(Deps{})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected dispatch with nil dependencies not to panic, got %v", r)
		}
	}()

	exec.Dispatch(context.Background(), "https://gengo.com/t/jobs/details/1", "job")
}

func TestExecutor_DispatchContainsPanicsAndReportsThem(t *testing.T) {
	panicEvents := make(chan string, 2)

	exec := New(Deps{
		Open: func(context.Context, string) error {
			panic("open boom")
		},
		Notify: func(context.Context, string) error {
			panic("notify boom")
		},
		OnAsyncPanic: func(action string, recovered any) {
			panicEvents <- action
		},
	})

	exec.Dispatch(context.Background(), "https://gengo.com/t/jobs/details/1", "job")

	seen := map[string]bool{}
	deadline := time.After(200 * time.Millisecond)
	for len(seen) < 2 {
		select {
		case action := <-panicEvents:
			seen[action] = true
		case <-deadline:
			t.Fatalf("expected panic callbacks for open and notify, got %#v", seen)
		}
	}
}

func TestExecutor_DispatchReportsAsyncErrors(t *testing.T) {
	openErr := errors.New("open failed")
	notifyErr := errors.New("notify failed")

	errorEvents := make(chan string, 2)
	exec := New(Deps{
		Open: func(context.Context, string) error {
			return openErr
		},
		Notify: func(context.Context, string) error {
			return notifyErr
		},
		OnAsyncError: func(action string, err error) {
			if err == nil {
				return
			}
			errorEvents <- action + ":" + err.Error()
		},
	})

	exec.Dispatch(context.Background(), "https://gengo.com/t/jobs/details/1", "job")

	seen := map[string]bool{}
	deadline := time.After(200 * time.Millisecond)
	for len(seen) < 2 {
		select {
		case msg := <-errorEvents:
			seen[msg] = true
		case <-deadline:
			t.Fatalf("expected error callbacks for open and notify, got %#v", seen)
		}
	}

	if !seen["open:"+openErr.Error()] {
		t.Fatalf("missing open error callback: %#v", seen)
	}
	if !seen["notify:"+notifyErr.Error()] {
		t.Fatalf("missing notify error callback: %#v", seen)
	}
}
