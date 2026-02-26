package rss

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/internal/dedupe"
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
	firstFeed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>JP -> EN</title><link>https://gengo.com/jobs/1</link></item></channel></rss>`
	secondFeed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>JP -> EN</title><link>https://gengo.com/jobs/1</link></item>
		<item><guid>job-2</guid><title>EN -> JA</title><link>https://gengo.com/jobs/2</link></item>
	</channel></rss>`
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			_, _ = w.Write([]byte(firstFeed))
			return
		}
		_, _ = w.Write([]byte(secondFeed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 40*time.Millisecond, 0)
	events := make(chan gengo.JobEvent)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.ID != "job-2" {
			t.Fatalf("unexpected event: %#v", ev)
		}
		cancel()
	case <-time.After(time.Second):
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
	firstFeed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><description>reward=10.0</description><link>https://gengo.com/jobs/1</link></item>
	</channel></rss>`
	secondFeed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1 duplicate</title><description>reward=10.0</description><link>https://gengo.com/jobs/1</link></item>
		<item><guid>job-2</guid><title>Job 2</title><description>reward=1.0</description><link>https://gengo.com/jobs/2</link></item>
		<item><guid>job-3</guid><title>Job 3</title><description>reward=9.0</description><link>https://gengo.com/jobs/3</link></item>
	</channel></rss>`
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			_, _ = w.Write([]byte(firstFeed))
			return
		}
		_, _ = w.Write([]byte(secondFeed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 40*time.Millisecond, 5.0)
	events := make(chan gengo.JobEvent, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.ID != "job-3" {
			t.Fatalf("unexpected first event: %#v", ev)
		}
	case <-time.After(time.Second):
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

func TestShouldEmit_AllowsPreviouslySeenKeyAfterDedupeTTL(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}, time.Second, 0)
	m.dedupe = dedupe.New(30*time.Millisecond, 2)

	if !m.shouldEmit("guid:job-1", 1.0) {
		t.Fatal("expected first emit")
	}

	if m.shouldEmit("guid:job-1", 1.0) {
		t.Fatal("expected suppression before ttl")
	}

	time.Sleep(40 * time.Millisecond)

	if !m.shouldEmit("guid:job-1", 1.0) {
		t.Fatal("expected emit after ttl")
	}
}

func TestStart_PrimesFeedWithoutEmittingExistingItems(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item>
	</channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 200*time.Millisecond, 0)
	events := make(chan gengo.JobEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		t.Fatalf("expected no event during prime, got %#v", ev)
	case <-time.After(120 * time.Millisecond):
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

func TestStart_PausedByPauseFileSkipsPolling(t *testing.T) {
	tmp := t.TempDir()
	pauseFile := filepath.Join(tmp, "gengowatcher.pause")
	if err := os.WriteFile(pauseFile, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("<rss><channel></channel></rss>"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 30*time.Millisecond, 0)
	m.pauseFile = pauseFile
	m.pauseSleep = 80 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	_ = m.Start(ctx, make(chan gengo.JobEvent, 1))

	if hits.Load() != 0 {
		t.Fatalf("expected 0 polls while paused, got %d", hits.Load())
	}
}

func TestStart_RetriesPrimingBeforeEmittingJobs(t *testing.T) {
	primingFeed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item>
	</channel></rss>`
	pollFeed := `<?xml version="1.0"?><rss><channel>
		<item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item>
		<item><guid>job-2</guid><title>Job 2</title><link>https://gengo.com/jobs/2</link></item>
	</channel></rss>`
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("temporary failure"))
		case 2:
			_, _ = w.Write([]byte(primingFeed))
		default:
			_, _ = w.Write([]byte(pollFeed))
		}
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 40*time.Millisecond, 0)
	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventError {
			t.Fatalf("expected initial prime failure error event, got %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected initial priming error event")
	}

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Job == nil || ev.Job.ID != "job-2" {
			t.Fatalf("expected first job event for new item after priming recovery, got %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected recovered monitor to emit only new item")
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

func TestPollOnce_TimesOutSlowServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte("<rss><channel></channel></rss>"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	m.fetchTimeout = 50 * time.Millisecond

	err := m.pollOnce(context.Background(), make(chan gengo.JobEvent, 1))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestPrimeOnce_TimesOutSlowServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("<rss><channel></channel></rss>"))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)
	m.fetchTimeout = 40 * time.Millisecond

	started := time.Now()
	err := m.primeOnce(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 120*time.Millisecond {
		t.Fatalf("expected timeout before slow response completes, took %s", elapsed)
	}
}

func TestNextWait_BackoffProgressionAndOverflowSafety(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss", MaxBackoff: 1500 * time.Millisecond}, 100*time.Millisecond, 0)

	if got := m.nextWait(false); got != 200*time.Millisecond {
		t.Fatalf("first failure wait = %s, want %s", got, 200*time.Millisecond)
	}
	if got := m.nextWait(false); got != 400*time.Millisecond {
		t.Fatalf("second failure wait = %s, want %s", got, 400*time.Millisecond)
	}
	if got := m.nextWait(false); got != 800*time.Millisecond {
		t.Fatalf("third failure wait = %s, want %s", got, 800*time.Millisecond)
	}
	if got := m.nextWait(false); got != 1500*time.Millisecond {
		t.Fatalf("fourth failure wait = %s, want cap %s", got, 1500*time.Millisecond)
	}
	if got := m.nextWait(false); got != 1500*time.Millisecond {
		t.Fatalf("capped failure wait = %s, want %s", got, 1500*time.Millisecond)
	}
	if got := m.nextWait(true); got != 100*time.Millisecond {
		t.Fatalf("success wait = %s, want %s", got, 100*time.Millisecond)
	}

	m = New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss", MaxBackoff: time.Duration(math.MaxInt64)}, time.Duration(math.MaxInt64/2+1), 0)
	if got := m.nextWait(false); got <= 0 {
		t.Fatalf("overflow-safe wait must stay positive, got %s", got)
	}
}

func TestNew_SanitizesInvalidRSSDurations(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss", PauseSleep: -1 * time.Second, MaxBackoff: -5 * time.Second}, time.Second, 0)

	if m.pauseSleep != config.DefaultRSSPauseSleep {
		t.Fatalf("pauseSleep = %s, want default %s", m.pauseSleep, config.DefaultRSSPauseSleep)
	}
	if m.maxBackoff != config.DefaultRSSMaxBackoff {
		t.Fatalf("maxBackoff = %s, want default %s", m.maxBackoff, config.DefaultRSSMaxBackoff)
	}

	m = New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss", PauseSleep: 3 * time.Second, MaxBackoff: 0}, time.Second, 0)
	if m.pauseSleep != 3*time.Second {
		t.Fatalf("pauseSleep = %s, want %s", m.pauseSleep, 3*time.Second)
	}
	if m.maxBackoff != config.DefaultRSSMaxBackoff {
		t.Fatalf("zero maxBackoff must default to %s, got %s", config.DefaultRSSMaxBackoff, m.maxBackoff)
	}
}

func TestStart_ReturnsErrorForNonPositiveCheckInterval(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}, 0, 0)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Start should return an error, got panic: %v", r)
		}
	}()

	err := m.Start(context.Background(), make(chan gengo.JobEvent, 1))
	if err == nil {
		t.Fatal("expected error for non-positive check interval")
	}
	if !strings.Contains(err.Error(), "check interval") {
		t.Fatalf("expected check interval error, got %q", err.Error())
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
	firstFeed := `<?xml version="1.0"?><rss><channel>
		<item><title>Job without GUID</title><description>reward=10.0</description><link>https://gengo.com/jobs/no-guid</link></item>
	</channel></rss>`
	secondFeed := `<?xml version="1.0"?><rss><channel>
		<item><title>Job without GUID duplicate</title><description>reward=10.0</description><link>https://gengo.com/jobs/no-guid</link></item>
		<item><title>Job without GUID new</title><description>reward=10.0</description><link>https://gengo.com/jobs/no-guid-2</link></item>
	</channel></rss>`
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			_, _ = w.Write([]byte(firstFeed))
			return
		}
		_, _ = w.Write([]byte(secondFeed))
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, 40*time.Millisecond, 5.0)
	events := make(chan gengo.JobEvent, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() { errCh <- m.Start(ctx, events) }()

	select {
	case ev := <-events:
		if ev.Type != gengo.EventJobFound || ev.Source != gengo.SourceRSS || ev.Job == nil || ev.Job.URL != "https://gengo.com/jobs/no-guid-2" {
			t.Fatalf("unexpected first event: %#v", ev)
		}
	case <-time.After(time.Second):
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

func TestFetchItems_UsesConditionalHeadersAndHandlesNotModified(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item></channel></rss>`

	const etag = `W/"feed-v1"`
	const lastModified = "Mon, 26 Feb 2026 10:00:00 GMT"

	var secondReqIfNoneMatch string
	var secondReqIfModifiedSince string
	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", lastModified)
			_, _ = w.Write([]byte(feed))
		case 2:
			secondReqIfNoneMatch = r.Header.Get("If-None-Match")
			secondReqIfModifiedSince = r.Header.Get("If-Modified-Since")
			w.WriteHeader(http.StatusNotModified)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)

	items, err := m.fetchItems(context.Background())
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected first fetch to return 1 item, got %d", len(items))
	}

	items, err = m.fetchItems(context.Background())
	if err != nil {
		t.Fatalf("second fetch should treat 304 as cache hit, got %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items on 304 response, got %d", len(items))
	}

	if secondReqIfNoneMatch != etag {
		t.Fatalf("expected If-None-Match=%q, got %q", etag, secondReqIfNoneMatch)
	}
	if secondReqIfModifiedSince != lastModified {
		t.Fatalf("expected If-Modified-Since=%q, got %q", lastModified, secondReqIfModifiedSince)
	}
}

func TestFetchItems_RefreshesConditionalValidatorsFrom304Response(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><guid>job-1</guid><title>Job 1</title><link>https://gengo.com/jobs/1</link></item></channel></rss>`

	const etagV1 = `W/"feed-v1"`
	const etagV2 = `W/"feed-v2"`
	const lastModifiedV1 = "Mon, 26 Feb 2026 10:00:00 GMT"
	const lastModifiedV2 = "Mon, 26 Feb 2026 10:05:00 GMT"

	var thirdReqIfNoneMatch string
	var thirdReqIfModifiedSince string
	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("ETag", etagV1)
			w.Header().Set("Last-Modified", lastModifiedV1)
			_, _ = w.Write([]byte(feed))
		case 2:
			w.Header().Set("ETag", etagV2)
			w.Header().Set("Last-Modified", lastModifiedV2)
			w.WriteHeader(http.StatusNotModified)
		case 3:
			thirdReqIfNoneMatch = r.Header.Get("If-None-Match")
			thirdReqIfModifiedSince = r.Header.Get("If-Modified-Since")
			w.WriteHeader(http.StatusNotModified)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	m := New(config.RSSConfig{Enabled: true, URL: server.URL}, time.Hour, 0)

	items, err := m.fetchItems(context.Background())
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected first fetch to return 1 item, got %d", len(items))
	}

	items, err = m.fetchItems(context.Background())
	if err != nil {
		t.Fatalf("second fetch should treat 304 as cache hit, got %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items on second fetch, got %d", len(items))
	}

	items, err = m.fetchItems(context.Background())
	if err != nil {
		t.Fatalf("third fetch should treat 304 as cache hit, got %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items on third fetch, got %d", len(items))
	}

	if thirdReqIfNoneMatch != etagV2 {
		t.Fatalf("expected third request If-None-Match=%q, got %q", etagV2, thirdReqIfNoneMatch)
	}
	if thirdReqIfModifiedSince != lastModifiedV2 {
		t.Fatalf("expected third request If-Modified-Since=%q, got %q", lastModifiedV2, thirdReqIfModifiedSince)
	}
}

func TestEmitItems_SkipsNilFeedItem(t *testing.T) {
	m := New(config.RSSConfig{Enabled: true, URL: "https://example.com/jobs.rss"}, time.Hour, 0)
	events := make(chan gengo.JobEvent, 2)

	err := m.emitItems(context.Background(), events, []*gofeed.Item{
		nil,
		{GUID: "job-1", Title: "Job 1", Link: "https://gengo.com/jobs/1"},
	})
	if err != nil {
		t.Fatalf("emitItems failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Job == nil || ev.Job.ID != "job-1" {
			t.Fatalf("unexpected event payload: %#v", ev)
		}
	default:
		t.Fatal("expected one event")
	}

	select {
	case ev := <-events:
		t.Fatalf("expected exactly one event, got extra: %#v", ev)
	default:
	}
}

func TestParseReward_SupportsPythonAndGoFormats(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        float64
	}{
		{name: "python format", description: "Reward: US$ 12.50", want: 12.5},
		{name: "go format", description: "reward=7.25", want: 7.25},
		{name: "missing", description: "no reward here", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseReward(tc.description); got != tc.want {
				t.Fatalf("parseReward(%q) = %v, want %v", tc.description, got, tc.want)
			}
		})
	}
}
