package rss

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type Monitor struct {
	cfg           config.RSSConfig
	checkInterval time.Duration
	minReward     float64
	stateMu       sync.Mutex
	seen          map[string]struct{}
	primed        bool
	fetchTimeout  time.Duration
	inFlight      atomic.Bool
	pauseFile     string
	pauseSleep    time.Duration
	failureCount  int
	maxBackoff    time.Duration
}

var rewardPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)reward\s*=\s*([0-9]+(?:\.[0-9]+)?)`),
	regexp.MustCompile(`(?i)reward\s*:\s*(?:US\$|\$)?\s*([0-9]+(?:\.[0-9]+)?)`),
}

func New(cfg config.RSSConfig, checkInterval time.Duration, minReward float64) *Monitor {
	pauseSleep, maxBackoff := sanitizeRSSDurations(cfg)

	return &Monitor{
		cfg:           cfg,
		checkInterval: checkInterval,
		minReward:     minReward,
		seen:          make(map[string]struct{}),
		fetchTimeout:  30 * time.Second,
		pauseFile:     cfg.PauseFile,
		pauseSleep:    pauseSleep,
		maxBackoff:    maxBackoff,
	}
}

func sanitizeRSSDurations(cfg config.RSSConfig) (time.Duration, time.Duration) {
	pauseSleep := cfg.PauseSleep
	if pauseSleep < 0 {
		pauseSleep = config.DefaultRSSPauseSleep
	}

	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = config.DefaultRSSMaxBackoff
	}

	return pauseSleep, maxBackoff
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	if m.checkInterval <= 0 {
		return fmt.Errorf("invalid rss check interval: %s", m.checkInterval)
	}

	wait := time.Duration(0)
	for {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
		}

		if m.isPaused() {
			wait = m.pauseDuration()
			continue
		}

		var err error
		if !m.isPrimed() {
			err = m.primeOnce(ctx)
		} else {
			err = m.pollOnce(ctx, events)
		}

		wait = m.nextWait(err == nil)

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			select {
			case events <- gengo.NewErrorEvent(gengo.SourceRSS, err.Error()):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (m *Monitor) isPaused() bool {
	if m.pauseFile == "" {
		return false
	}

	_, err := os.Stat(m.pauseFile)
	return err == nil
}

func (m *Monitor) pauseDuration() time.Duration {
	if m.pauseSleep > 0 {
		return m.pauseSleep
	}

	if m.checkInterval > 0 {
		return m.checkInterval
	}

	return time.Second
}

func (m *Monitor) nextWait(success bool) time.Duration {
	if success {
		m.failureCount = 0
		return m.checkInterval
	}

	m.failureCount++
	maxBackoff := m.maxBackoffDuration()
	wait := m.checkInterval

	for i := 0; i < m.failureCount; i++ {
		if wait >= maxBackoff {
			return maxBackoff
		}
		if wait > maxBackoff/2 {
			return maxBackoff
		}
		wait *= 2
	}
	if wait <= 0 || wait > maxBackoff {
		return maxBackoff
	}

	return wait
}

func (m *Monitor) maxBackoffDuration() time.Duration {
	if m.maxBackoff > 0 {
		return m.maxBackoff
	}

	return config.DefaultRSSMaxBackoff
}

func (m *Monitor) pollOnce(ctx context.Context, events chan<- gengo.JobEvent) error {
	if !m.inFlight.CompareAndSwap(false, true) {
		return nil
	}
	defer m.inFlight.Store(false)

	pollCtx, cancel := m.withFetchTimeout(ctx)
	defer cancel()

	items, err := m.fetchItems(pollCtx)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		return nil
	}

	return m.emitItems(ctx, events, items)
}

func (m *Monitor) primeOnce(ctx context.Context) error {
	primeCtx, cancel := m.withFetchTimeout(ctx)
	defer cancel()

	items, err := m.fetchItems(primeCtx)
	if err != nil {
		return err
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	for _, item := range items {
		if item == nil {
			continue
		}

		if itemKey := dedupeKey(item); itemKey != "" {
			m.seen[itemKey] = struct{}{}
		}
	}

	m.primed = true
	return nil
}

func (m *Monitor) fetchItems(ctx context.Context) ([]*gofeed.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.URL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("rss feed request failed: status %d", resp.StatusCode)
	}

	feed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	return feed.Items, nil
}

func (m *Monitor) withFetchTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := m.fetchTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return context.WithTimeout(ctx, timeout)
}

func (m *Monitor) emitItems(ctx context.Context, events chan<- gengo.JobEvent, items []*gofeed.Item) error {
	for _, item := range items {
		if item == nil {
			continue
		}

		reward := parseReward(item.Description)
		if !m.shouldEmit(dedupeKey(item), reward) {
			continue
		}

		event := gengo.NewJobEvent(
			gengo.EventJobFound,
			gengo.SourceRSS,
			&gengo.Job{ID: item.GUID, Title: item.Title, Source: gengo.SourceRSS, Reward: reward, URL: item.Link, FoundAt: time.Now()},
		)

		select {
		case events <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func parseReward(description string) float64 {
	for _, pattern := range rewardPatterns {
		matches := pattern.FindStringSubmatch(description)
		if len(matches) != 2 {
			continue
		}

		reward, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return reward
		}
	}

	return 0
}

func dedupeKey(item *gofeed.Item) string {
	guid := strings.TrimSpace(item.GUID)
	if guid != "" {
		return "guid:" + guid
	}

	link := strings.TrimSpace(item.Link)
	if link != "" {
		return "link:" + link
	}

	title := strings.TrimSpace(item.Title)
	published := strings.TrimSpace(item.Published)
	if item.PublishedParsed != nil {
		published = item.PublishedParsed.UTC().Format(time.RFC3339Nano)
	}
	if title != "" && published != "" {
		return "title+published:" + title + "|" + published
	}

	if title != "" {
		return "title:" + title
	}

	return ""
}

func (m *Monitor) shouldEmit(itemKey string, reward float64) bool {
	if reward < m.minReward {
		return false
	}

	if itemKey == "" {
		return true
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if _, exists := m.seen[itemKey]; exists {
		return false
	}

	m.seen[itemKey] = struct{}{}
	return true
}

func (m *Monitor) isPrimed() bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.primed
}

func (m *Monitor) Name() string {
	return "rss"
}

func (m *Monitor) Source() gengo.Source {
	return gengo.SourceRSS
}

func (m *Monitor) Enabled() bool {
	return m.cfg.Enabled && m.cfg.URL != ""
}
