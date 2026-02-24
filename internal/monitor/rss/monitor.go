package rss

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type Monitor struct {
	cfg           config.RSSConfig
	checkInterval time.Duration
	minReward     float64
	seen          map[string]struct{}
}

var rewardPattern = regexp.MustCompile(`(?i)reward\s*=\s*([0-9]+(?:\.[0-9]+)?)`)

func New(cfg config.RSSConfig, checkInterval time.Duration, minReward float64) *Monitor {
	return &Monitor{cfg: cfg, checkInterval: checkInterval, minReward: minReward, seen: make(map[string]struct{})}
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		err := m.pollOnce(ctx, events)
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

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (m *Monitor) pollOnce(ctx context.Context, events chan<- gengo.JobEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.URL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("rss feed request failed: status %d", resp.StatusCode)
	}

	feed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		return err
	}

	if len(feed.Items) == 0 {
		return nil
	}

	for _, item := range feed.Items {
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
	matches := rewardPattern.FindStringSubmatch(description)
	if len(matches) != 2 {
		return 0
	}

	reward, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}

	return reward
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

	if _, exists := m.seen[itemKey]; exists {
		return false
	}

	m.seen[itemKey] = struct{}{}
	return true
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
