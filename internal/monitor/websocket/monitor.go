package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type Monitor struct {
	cfg               config.WebSocketConfig
	dialer            *websocket.Dialer
	readTimeout       time.Duration
	heartbeatInterval time.Duration
	pongWait          time.Duration
	reconnectMinWait  time.Duration
	reconnectMaxWait  time.Duration
}

type socketAuth struct {
	UserID      string `json:"user_id"`
	UserSession string `json:"user_session"`
	UserKey     string `json:"user_key,omitempty"`
}

func New(cfg config.WebSocketConfig) *Monitor {
	return &Monitor{
		cfg:               cfg,
		dialer:            websocket.DefaultDialer,
		readTimeout:       30 * time.Second,
		heartbeatInterval: 10 * time.Second,
		reconnectMinWait:  250 * time.Millisecond,
		reconnectMaxWait:  4 * time.Second,
	}
}

func (m *Monitor) Start(ctx context.Context, events chan<- gengo.JobEvent) error {
	backoff := m.reconnectMinWait
	for {
		err, resetBackoff := m.runConnection(ctx, events)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			if resetBackoff {
				backoff = m.reconnectMinWait
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

func (m *Monitor) runConnection(ctx context.Context, events chan<- gengo.JobEvent) (error, bool) {
	conn, err := m.dialWithFallback(ctx)
	if err != nil {
		return err, false
	}
	defer conn.Close()

	auth := socketAuth{
		UserID:      m.cfg.UserID,
		UserSession: m.cfg.UserSession,
		UserKey:     m.cfg.UserKey,
	}
	if err := conn.WriteJSON(auth); err != nil {
		return err, false
	}

	connected := true
	effectivePongWait := m.effectivePongWait()
	effectiveHeartbeatInterval := m.effectiveHeartbeatInterval()

	if effectivePongWait > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(effectivePongWait)); err != nil {
			return err, connected
		}
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(effectivePongWait))
		})
	}

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	if effectiveHeartbeatInterval > 0 {
		go m.runHeartbeat(heartbeatCtx, conn, effectiveHeartbeatInterval)
	}

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
				return nil, false
			}
			return err, connected
		}

		if effectivePongWait > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(effectivePongWait)); err != nil {
				return err, connected
			}
		}

		event, ok := parseJobPublished(payload)
		if !ok {
			continue
		}

		select {
		case events <- event:
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (m *Monitor) effectivePongWait() time.Duration {
	if m.pongWait > 0 {
		return m.pongWait
	}

	return m.readTimeout
}

func (m *Monitor) effectiveHeartbeatInterval() time.Duration {
	if m.heartbeatInterval <= 0 {
		return 0
	}

	if m.effectivePongWait() <= 0 {
		return 0
	}

	return m.heartbeatInterval
}

func (m *Monitor) runHeartbeat(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(interval)); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func (m *Monitor) dialWithFallback(ctx context.Context) (*websocket.Conn, error) {
	headers := m.authHeaders()
	conn, resp, err := m.dialer.DialContext(ctx, m.cfg.URL, headers)
	if err == nil {
		return conn, nil
	}

	if len(headers) == 0 || !shouldRetryWithoutHeaders(err, resp) {
		return nil, err
	}

	fallbackConn, fallbackResp, fallbackErr := m.dialer.DialContext(ctx, m.cfg.URL, nil)
	if fallbackErr != nil {
		return nil, fmt.Errorf(
			"websocket dial failed with headers then without headers: primary=%s; fallback=%s",
			formatDialError(err, resp),
			formatDialError(fallbackErr, fallbackResp),
		)
	}

	return fallbackConn, nil
}

func shouldRetryWithoutHeaders(err error, resp *http.Response) bool {
	if err == nil || resp == nil {
		return false
	}

	bodyText := ""
	if resp.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if readErr == nil {
			bodyText = strings.ToLower(string(body))
		}
	}

	errText := strings.ToLower(err.Error())
	headerRejected := hasExplicitHeaderCookieRejectionSignal(errText) || hasExplicitHeaderCookieRejectionSignal(bodyText)

	if !headerRejected {
		return false
	}

	return resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized
}

func hasExplicitHeaderCookieRejectionSignal(text string) bool {
	if text == "" {
		return false
	}

	hasHeaderCookie := strings.Contains(text, "header") || strings.Contains(text, "cookie")
	if !hasHeaderCookie {
		return false
	}

	rejectionTokens := []string{"reject", "rejected", "invalid", "missing", "not allowed", "forbidden", "denied", "unsupported"}
	for _, token := range rejectionTokens {
		if strings.Contains(text, token) {
			return true
		}
	}

	return false
}

func formatDialError(err error, resp *http.Response) string {
	if err == nil {
		return "none"
	}

	if resp == nil {
		return err.Error()
	}

	return fmt.Sprintf("%s (status=%d)", err.Error(), resp.StatusCode)
}

func (m *Monitor) authHeaders() http.Header {
	headers := http.Header{}
	cookieRequest := &http.Request{Header: make(http.Header)}
	if m.cfg.UserID != "" {
		cookieRequest.AddCookie(&http.Cookie{Name: "user_id", Value: m.cfg.UserID})
	}
	if m.cfg.UserSession != "" {
		cookieRequest.AddCookie(&http.Cookie{Name: "session", Value: m.cfg.UserSession})
	}
	if cookie := cookieRequest.Header.Get("Cookie"); cookie != "" {
		headers.Set("Cookie", cookie)
	}
	if m.cfg.UserKey != "" {
		headers.Set("X-User-Key", m.cfg.UserKey)
	}
	return headers
}

type socketEnvelope struct {
	Event      string          `json:"event"`
	Type       string          `json:"type"`
	Data       json.RawMessage `json:"data"`
	Collection json.RawMessage `json:"collection"`
}

type socketJob struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Reward     float64 `json:"reward"`
	Rewards    float64 `json:"rewards"`
	SourceLC   string  `json:"lc_src"`
	TargetLC   string  `json:"lc_tgt"`
	SourceLang string  `json:"source_lang"`
	TargetLang string  `json:"target_lang"`
}

type availableCollection struct {
	ID      json.RawMessage `json:"id"`
	Reward  float64         `json:"reward"`
	Rewards float64         `json:"rewards"`
	Source  string          `json:"lc_src"`
	Target  string          `json:"lc_tgt"`
}

func parseJobPublished(payload []byte) (gengo.JobEvent, bool) {
	var envelope socketEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return gengo.JobEvent{}, false
	}

	if envelope.Event == "job_published" {
		var data socketJob
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return gengo.JobEvent{}, false
		}
		if data.ID == "" {
			return gengo.JobEvent{}, false
		}

		reward := data.Reward
		if reward == 0 {
			reward = data.Rewards
		}
		language := buildLanguage(data.SourceLC, data.TargetLC)
		if language == "" {
			language = buildLanguage(data.SourceLang, data.TargetLang)
		}

		return gengo.NewJobEvent(
			gengo.EventJobFound,
			gengo.SourceWebSocket,
			&gengo.Job{
				ID:       data.ID,
				Title:    data.Title,
				URL:      data.URL,
				Source:   gengo.SourceWebSocket,
				Reward:   reward,
				Language: language,
				FoundAt:  time.Now(),
			},
		), true
	}

	if envelope.Type == "available_collection" {
		body := envelope.Collection
		if len(body) == 0 {
			body = envelope.Data
		}
		if len(body) == 0 {
			return gengo.JobEvent{}, false
		}

		var data availableCollection
		if err := json.Unmarshal(body, &data); err != nil {
			return gengo.JobEvent{}, false
		}
		id, ok := parseCollectionID(data.ID)
		if !ok {
			return gengo.JobEvent{}, false
		}

		reward := data.Reward
		if reward == 0 {
			reward = data.Rewards
		}

		return gengo.NewJobEvent(
			gengo.EventJobFound,
			gengo.SourceWebSocket,
			&gengo.Job{
				ID:       id,
				Source:   gengo.SourceWebSocket,
				Reward:   reward,
				Language: buildLanguage(data.Source, data.Target),
				FoundAt:  time.Now(),
			},
		), true
	}

	return gengo.JobEvent{}, false
}

func parseCollectionID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		return asString, asString != ""
	}

	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber.String(), asNumber.String() != ""
	}

	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return strconv.FormatFloat(asFloat, 'f', -1, 64), true
	}

	return "", false
}

func buildLanguage(src string, tgt string) string {
	if src == "" || tgt == "" {
		return ""
	}

	return src + " -> " + tgt
}

func (m *Monitor) Name() string {
	return "websocket"
}

func (m *Monitor) Source() gengo.Source {
	return gengo.SourceWebSocket
}

func (m *Monitor) Enabled() bool {
	return m.cfg.Enabled && m.cfg.URL != "" && m.cfg.UserID != "" && m.cfg.UserSession != ""
}
