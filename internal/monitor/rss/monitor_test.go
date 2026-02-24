package rss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestNewMonitor_ImplementsMonitorContract(t *testing.T) {
	cfg := config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}
	m := New(cfg, 30*time.Second, 5.0)

	var monitor gengo.Monitor = m
	if monitor.Name() != "rss" {
		t.Fatalf("expected name rss, got %s", monitor.Name())
	}
	if monitor.Source() != gengo.SourceRSS {
		t.Fatalf("expected source rss, got %s", monitor.Source())
	}
	if !monitor.Enabled() {
		t.Fatalf("expected monitor enabled")
	}
}

func TestStart_EmitsJobFoundEventForFeedItem(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>JP -> EN</title><link>https://gengo.com/jobs/1</link></item></channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	events := make(chan gengo.JobEvent)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.ID != "job-1" {
			t.Fatalf("unexpected event: %#v", ev)
		}
		cancel()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected job event")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Start goroutine to exit after cancel")
	}
}

func TestStart_DeduplicatesAndAppliesMinReward(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><description>reward=10.0</description><link>https://gengo.com/jobs/1</link></item>
		<item><guid>job-1</guid><title>Job 1 duplicate</title><description>reward=10.0</description><link>https://gengo.com/jobs/1</link></item>
		<item><guid>job-2</guid><title>Job 2</title><description>reward=1.0</description><link>https://gengo.com/jobs/2</link></item>
	</channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 5.0)
	events := make(chan gengo.JobEvent, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.ID != "job-1" {
			t.Fatalf("unexpected first event: %#v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected one job event")
	}

	select {
	case ev := <-events:
		t.Fatalf("expected exactly one event, got extra: %#v", ev)
	case <-time.After(150 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Start goroutine to exit after cancel")
	}
}

func TestPollOnce_ReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	err := m.pollOnce(context.Background(), make(chan gengo.JobEvent, 1))
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected status code in error, got %q", err.Error())
	}
}

func TestPollOnce_EmitsJobFoundEventForEachFeedItem(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item><item><guid>job-2</guid><title>Job 2</title><link>https://gengo.com/jobs/2</link></item></channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	events := make(chan gengo.JobEvent, 2)

	err := m.pollOnce(context.Background(), events)
	if err != nil {
		t.Fatalf("pollOnce failed: %v", err)
	}

	var first gengo.JobEvent
	select {
	case first = <-events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected first event")
	}

	var second gengo.JobEvent
	select {
	case second = <-events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected second event")
	}

	if first.Job == nil || second.Job == nil {
		t.Fatalf("expected job payloads, got first=%#v second=%#v", first, second)
	}
	if first.Job.ID != "job-1" || second.Job.ID != "job-2" {
		t.Fatalf("expected events for all feed items in order, got %q then %q", first.Job.ID, second.Job.ID)
	}
}

func TestStart_DeduplicatesWithoutGUIDUsingFallbackKey(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel>
		<item><title>Job without GUID</title><description>reward=10.0</description><link>https://gengo.com/jobs/no-guid</link></item>
		<item><title>Job without GUID duplicate</title><description>reward=10.0</description><link>https://gengo.com/jobs/no-guid</link></item>
	</channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 5.0)
	events := make(chan gengo.JobEvent, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.URL != "https://gengo.com/jobs/no-guid" {
			t.Fatalf("unexpected first event: %#v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected one job event")
	}

	select {
	case ev := <-events:
		t.Fatalf("expected exactly one event, got extra: %#v", ev)
	case <-time.After(150 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Start goroutine to exit after cancel")
	}
}

func TestPollOnce_ReturnsContextErrorWhenCanceledDuringEmit(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item><item><guid>job-2</guid><title>Job 2</title><link>https://gengo.com/jobs/2</link></item></channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	events := make(chan gengo.JobEvent)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.pollOnce(ctx, events)
	}()

	select {
	case <-events:
		cancel()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected first event")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected pollOnce to return after cancel")
	}
}
