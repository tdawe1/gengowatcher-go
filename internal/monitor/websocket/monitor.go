package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type Monitor struct {
	cfg              config.WebSocketConfig
	dialer           *websocket.Dialer
	reconnectMinWait time.Duration
	reconnectMaxWait time.Duration
}

func New(cfg config.WebSocketConfig) *Monitor {
	return &Monitor{
		cfg:              cfg,
		dialer:           websocket.DefaultDialer,
		reconnectMinWait: 250 * time.Millisecond,
		reconnectMaxWait: 4 * time.Second,
	}
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	backoff := m.reconnectMinWait
	for {
		err := m.runConnection(ctx, events)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			select {
			case events <- gengo.NewErrorEvent(gengo.SourceWebSocket, err.Error()):
			case <-ctx.Done():
				return nil
			}

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}

			if backoff < m.reconnectMaxWait {
				backoff *= 2
				if backoff > m.reconnectMaxWait {
					backoff = m.reconnectMaxWait
				}
			}
			continue
		}

		if ctx.Err() != nil {
			return nil
		}
		backoff = m.reconnectMinWait
	}
}

func (m *Monitor) runConnection(ctx context.Context, events chan<- gengo.JobEvent) error {
	conn, _, err := m.dialer.DialContext(ctx, m.cfg.URL, m.authHeaders())
	if err != nil {
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		event, ok := parseJobPublished(payload)
		if !ok {
			continue
		}

		select {
		case events <- event:
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *Monitor) authHeaders() http.Header {
	headers := http.Header{}
	cookies := []string{}
	if m.cfg.UserID != "" {
		cookies = append(cookies, "user_id="+m.cfg.UserID)
	}
	if m.cfg.UserSession != "" {
		cookies = append(cookies, "session="+m.cfg.UserSession)
	}
	if len(cookies) > 0 {
		headers.Set("Cookie", strings.Join(cookies, "; "))
	}
	if m.cfg.UserKey != "" {
		headers.Set("X-User-Key", m.cfg.UserKey)
	}
	return headers
}

type socketEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type socketJob struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func parseJobPublished(payload []byte) (gengo.JobEvent, bool) {
	var envelope socketEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return gengo.JobEvent{}, false
	}
	if envelope.Event != "job_published" {
		return gengo.JobEvent{}, false
	}

	var data socketJob
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return gengo.JobEvent{}, false
	}
	if data.ID == "" {
		return gengo.JobEvent{}, false
	}

	return gengo.NewJobEvent(
		gengo.EventJobFound,
		gengo.SourceWebSocket,
		&gengo.Job{ID: data.ID, Title: data.Title, URL: data.URL, Source: gengo.SourceWebSocket, FoundAt: time.Now()},
	), true
}

func (m *Monitor) Name() string {
	return "websocket"
}

func (m *Monitor) Source() gengo.Source {
	return gengo.SourceWebSocket
}

func (m *Monitor) Enabled() bool {
	return m.cfg.Enabled && m.cfg.URL != "" && m.cfg.UserID != "" && m.cfg.UserSession != "" && m.cfg.UserKey != ""
}
