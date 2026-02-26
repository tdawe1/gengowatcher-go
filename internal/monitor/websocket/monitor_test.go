package websocket

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func toWebSocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func TestNewMonitor_ImplementsMonitorContract(t *testing.T) {
	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         "ws://example",
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})

	var monitor gengo.Monitor = m
	if monitor.Name() != "websocket" {
		t.Fatalf("expected websocket, got %s", monitor.Name())
	}
	if monitor.Source() != gengo.SourceWebSocket {
		t.Fatalf("expected websocket source")
	}
	if !monitor.Enabled() {
		t.Fatalf("expected monitor enabled")
	}
}

func TestStart_SendsAuthPayloadAfterConnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	authSeen := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err == nil {
			authSeen <- msg
		}
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "u1",
		UserSession: "s1",
	})
	if !m.Enabled() {
		t.Fatal("expected monitor enabled without user_key")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Start(ctx, make(chan gengo.JobEvent, 1))
	}()

	select {
	case raw := <-authSeen:
		if !bytes.Contains(raw, []byte(`"user_id":"u1"`)) {
			t.Fatalf("unexpected auth payload: %s", string(raw))
		}
		if !bytes.Contains(raw, []byte(`"user_session":"s1"`)) {
			t.Fatalf("unexpected auth payload: %s", string(raw))
		}
		if bytes.Contains(raw, []byte(`"user_key"`)) {
			t.Fatalf("expected auth payload without user_key, got: %s", string(raw))
		}
	case <-time.After(time.Second):
		t.Fatal("expected auth payload")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to stop after cancel")
	}
}

func TestAuthHeaders_UsesSafeCookieFormatting(t *testing.T) {
	m := New(config.WebSocketConfig{
		UserID:      "u1 s1",
		UserSession: "s1",
	})

	cookie := m.authHeaders().Get("Cookie")
	if cookie == "" {
		t.Fatal("expected cookie header")
	}
	if strings.Contains(cookie, "user_id=u1 s1") {
		t.Fatalf("expected user_id cookie to be safely quoted, got %q", cookie)
	}
	if !strings.Contains(cookie, `user_id="u1 s1"`) {
		t.Fatalf("expected sanitized user_id cookie, got %q", cookie)
	}
	if !strings.Contains(cookie, "session=s1") {
		t.Fatalf("expected session cookie, got %q", cookie)
	}
}

func TestEnabled_NegativeCases(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.WebSocketConfig
	}{
		{
			name: "disabled config",
			cfg: config.WebSocketConfig{
				Enabled:     false,
				URL:         "ws://example",
				UserID:      "user-id",
				UserSession: "session",
			},
		},
		{
			name: "missing url",
			cfg: config.WebSocketConfig{
				Enabled:     true,
				UserID:      "user-id",
				UserSession: "session",
			},
		},
		{
			name: "missing user id",
			cfg: config.WebSocketConfig{
				Enabled:     true,
				URL:         "ws://example",
				UserSession: "session",
			},
		},
		{
			name: "missing user session",
			cfg: config.WebSocketConfig{
				Enabled: true,
				URL:     "ws://example",
				UserID:  "user-id",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if New(tc.cfg).Enabled() {
				t.Fatalf("expected monitor to be disabled for case %q", tc.name)
			}
		})
	}
}

func TestStart_EmitsJobFoundFromJobPublishedMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"job_published","data":{"id":"job-42","title":"Hello"}}`))
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})

	events := make(chan gengo.JobEvent, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = m.Start(ctx, events)
	}()

	select {
	case event := <-events:
		if event.Type != gengo.EventJobFound {
			t.Fatalf("expected event type %q, got %q", gengo.EventJobFound, event.Type)
		}
		if event.Source != gengo.SourceWebSocket {
			t.Fatalf("expected source %q, got %q", gengo.SourceWebSocket, event.Source)
		}
		if event.Job == nil {
			t.Fatal("expected non-nil job")
		}
		if event.Job.ID != "job-42" {
			t.Fatalf("expected job ID job-42, got %q", event.Job.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected job event from websocket message")
	}
}

func TestStart_ReconnectsAndEmitsErrorEventOnDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connections atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		count := connections.Add(1)
		if count == 1 {
			_ = conn.Close()
			return
		}

		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"job_published","data":{"id":"job-42","title":"Hello"}}`))
		_ = conn.Close()
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})

	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() {
		errCh <- m.Start(ctx, events)
	}()

	select {
	case event := <-events:
		if event.Type != gengo.EventError {
			t.Fatalf("expected first event type %q, got %q", gengo.EventError, event.Type)
		}
		if event.Source != gengo.SourceWebSocket {
			t.Fatalf("expected source %q, got %q", gengo.SourceWebSocket, event.Source)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected websocket error event")
	}

	select {
	case event := <-events:
		if event.Type != gengo.EventJobFound {
			t.Fatalf("expected second event type %q, got %q", gengo.EventJobFound, event.Type)
		}
		if event.Job == nil || event.Job.ID != "job-42" {
			t.Fatalf("expected job-42 event, got %#v", event)
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket reconnect job event")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected Start to stop after cancel")
	}
}

func TestStart_RetriesWithoutHeadersWhenHandshakeRejectsHeaders(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var sawHeaderRejected atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			sawHeaderRejected.CompareAndSwap(0, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("extra headers not allowed"))
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, _, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"job_published","data":{"id":"fallback-job"}}`))
		<-time.After(120 * time.Millisecond)
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "u1",
		UserSession: "s1",
		UserKey:     "k1",
	})

	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		_ = m.Start(ctx, events)
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	for {
		select {
		case ev := <-events:
			if ev.Type == gengo.EventJobFound && ev.Job != nil && ev.Job.ID == "fallback-job" {
				if sawHeaderRejected.Load() == 0 {
					t.Fatal("expected initial handshake rejection when headers are present")
				}
				return
			}
		case <-deadline.C:
			t.Fatal("expected fallback reconnect job event")
		}
	}
}

func TestShouldRetryWithoutHeaders_ClassifierNegatives(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "generic unauthorized without header or cookie signal",
			statusCode: http.StatusUnauthorized,
			body:       "unauthorized",
			want:       false,
		},
		{
			name:       "generic forbidden without header or cookie signal",
			statusCode: http.StatusForbidden,
			body:       "forbidden",
			want:       false,
		},
		{
			name:       "mentions cookie without explicit rejection signal",
			statusCode: http.StatusUnauthorized,
			body:       "cookie preferences are unavailable",
			want:       false,
		},
		{
			name:       "explicit cookie rejection signal",
			statusCode: http.StatusUnauthorized,
			body:       "invalid cookie provided",
			want:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			got := shouldRetryWithoutHeaders(&websocket.HandshakeError{}, resp)
			if got != tc.want {
				t.Fatalf("expected shouldRetryWithoutHeaders=%v, got %v", tc.want, got)
			}
		})
	}
}

func TestDialWithFallback_IncludesPrimaryAndFallbackErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("headers not allowed"))
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "u1",
		UserSession: "s1",
		UserKey:     "k1",
	})

	_, err := m.dialWithFallback(context.Background())
	if err == nil {
		t.Fatal("expected dialWithFallback to fail")
	}

	errText := err.Error()
	if !strings.Contains(errText, "primary=") {
		t.Fatalf("expected primary dial error context, got %q", errText)
	}
	if !strings.Contains(errText, "fallback=") {
		t.Fatalf("expected fallback dial error context, got %q", errText)
	}
	if !strings.Contains(errText, "status=400") {
		t.Fatalf("expected primary status in error context, got %q", errText)
	}
	if !strings.Contains(errText, "status=503") {
		t.Fatalf("expected fallback status in error context, got %q", errText)
	}
}

func TestStart_ResetsReconnectBackoffAfterSuccessfulLifecycle(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)

		switch attempt {
		case 1, 2, 4:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporarily unavailable"))
			return
		case 3:
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			_, _, _ = conn.ReadMessage()
			_ = conn.Close()
			return
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "u1",
		UserSession: "s1",
	})
	m.reconnectMinWait = 50 * time.Millisecond
	m.reconnectMaxWait = 400 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan gengo.JobEvent, 16)
	errCh := make(chan error, 1)

	go func() {
		errCh <- m.Start(ctx, events)
	}()

	errorTimes := make([]time.Time, 0, 4)
	deadline := time.After(2 * time.Second)
	for len(errorTimes) < 4 {
		select {
		case ev := <-events:
			if ev.Type == gengo.EventError {
				errorTimes = append(errorTimes, time.Now())
			}
		case <-deadline:
			t.Fatal("timed out waiting for reconnect errors")
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to stop after cancel")
	}

	deltaAfterSuccessfulLifecycle := errorTimes[3].Sub(errorTimes[2])
	if deltaAfterSuccessfulLifecycle > 140*time.Millisecond {
		t.Fatalf("expected reconnect after successful lifecycle to use min backoff, got %v", deltaAfterSuccessfulLifecycle)
	}
}

func TestStart_ParsesAvailableCollectionPayload(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"available_collection","collection":{"id":"42","rewards":19.5,"lc_src":"en","lc_tgt":"ja"}}`))
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})

	events := make(chan gengo.JobEvent, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = m.Start(ctx, events)
	}()

	select {
	case event := <-events:
		if event.Type != gengo.EventJobFound {
			t.Fatalf("expected event type %q, got %q", gengo.EventJobFound, event.Type)
		}
		if event.Source != gengo.SourceWebSocket {
			t.Fatalf("expected source %q, got %q", gengo.SourceWebSocket, event.Source)
		}
		if event.Job == nil {
			t.Fatal("expected non-nil job")
		}
		if event.Job.ID != "42" {
			t.Fatalf("expected job ID 42, got %q", event.Job.ID)
		}
		if event.Job.Reward != 19.5 {
			t.Fatalf("expected reward 19.5, got %v", event.Job.Reward)
		}
		if event.Job.Language != "en -> ja" {
			t.Fatalf("expected language en -> ja, got %q", event.Job.Language)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected available_collection job event")
	}
}

func TestParseJobPublished_AvailableCollectionEnvelopeVariants(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantID  string
	}{
		{
			name:    "collection field with string id",
			payload: `{"type":"available_collection","collection":{"id":"42","reward":19.5,"lc_src":"en","lc_tgt":"ja"}}`,
			wantID:  "42",
		},
		{
			name:    "collection field with numeric id",
			payload: `{"type":"available_collection","collection":{"id":42,"rewards":19.5,"lc_src":"en","lc_tgt":"ja"}}`,
			wantID:  "42",
		},
		{
			name:    "data field with numeric id",
			payload: `{"type":"available_collection","data":{"id":7,"reward":10,"lc_src":"en","lc_tgt":"fr"}}`,
			wantID:  "7",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := parseJobPublished([]byte(tc.payload))
			if !ok {
				t.Fatal("expected payload to parse")
			}
			if event.Type != gengo.EventJobFound {
				t.Fatalf("expected event type %q, got %q", gengo.EventJobFound, event.Type)
			}
			if event.Job == nil {
				t.Fatal("expected non-nil job")
			}
			if event.Job.ID != tc.wantID {
				t.Fatalf("expected job ID %q, got %q", tc.wantID, event.Job.ID)
			}
		})
	}
}

func TestParseJobPublished_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "malformed json",
			payload: `{"event":"job_published",`,
		},
		{
			name:    "unknown event",
			payload: `{"event":"unknown","data":{"id":"42"}}`,
		},
		{
			name:    "available collection missing id",
			payload: `{"type":"available_collection","collection":{"rewards":19.5}}`,
		},
		{
			name:    "available collection non scalar id",
			payload: `{"type":"available_collection","collection":{"id":{"nested":1}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parseJobPublished([]byte(tc.payload)); ok {
				t.Fatal("expected payload to be ignored")
			}
		})
	}
}

func TestStart_EmitsErrorEventWhenReadTimesOut(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		<-r.Context().Done()
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})
	m.readTimeout = 150 * time.Millisecond
	m.heartbeatInterval = 30 * time.Millisecond
	m.pongWait = 150 * time.Millisecond
	m.reconnectMinWait = 10 * time.Millisecond
	m.reconnectMaxWait = 20 * time.Millisecond

	events := make(chan gengo.JobEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)

	go func() {
		errCh <- m.Start(ctx, events)
	}()

	select {
	case event := <-events:
		if event.Type != gengo.EventError {
			t.Fatalf("expected event type %q, got %q", gengo.EventError, event.Type)
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket timeout error event")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil start error, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected Start to stop after cancel")
	}
}

func TestStart_DoesNotEmitTimeoutWhileHeartbeatAlive(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	m := New(config.WebSocketConfig{
		Enabled:     true,
		URL:         toWebSocketURL(server.URL),
		UserID:      "user-id",
		UserSession: "session",
		UserKey:     "key",
	})
	m.readTimeout = 150 * time.Millisecond
	m.heartbeatInterval = 30 * time.Millisecond
	m.pongWait = 150 * time.Millisecond
	m.reconnectMinWait = 10 * time.Millisecond
	m.reconnectMaxWait = 20 * time.Millisecond

	events := make(chan gengo.JobEvent, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Start(ctx, events)
	}()

	for {
		select {
		case ev := <-events:
			if ev.Type == gengo.EventError {
				t.Fatalf("unexpected error during healthy idle heartbeat: %#v", ev)
			}
		case err := <-errCh:
			if err != nil {
				t.Fatalf("expected nil start error, got %v", err)
			}
			return
		case <-time.After(600 * time.Millisecond):
			t.Fatal("timeout waiting for monitor to stop")
		}
	}
}

func TestApplyReconnectJitter_UsesConfiguredFraction(t *testing.T) {
	m := New(config.WebSocketConfig{Enabled: true, URL: "wss://example.com"})
	m.reconnectJitterFraction = 0.20

	m.randFloat64 = func() float64 { return 0.0 }
	if got := m.applyReconnectJitter(100 * time.Millisecond); got != 80*time.Millisecond {
		t.Fatalf("expected min jittered backoff 80ms, got %s", got)
	}

	m.randFloat64 = func() float64 { return 1.0 }
	if got := m.applyReconnectJitter(100 * time.Millisecond); got != 120*time.Millisecond {
		t.Fatalf("expected max jittered backoff 120ms, got %s", got)
	}
}

func TestApplyReconnectJitter_ReturnsBaseBackoffWhenDisabled(t *testing.T) {
	m := New(config.WebSocketConfig{Enabled: true, URL: "wss://example.com"})
	m.reconnectJitterFraction = 0

	m.randFloat64 = func() float64 { return 0.95 }
	if got := m.applyReconnectJitter(250 * time.Millisecond); got != 250*time.Millisecond {
		t.Fatalf("expected base backoff when jitter disabled, got %s", got)
	}
}

func TestNew_UsesInstanceRandomSourceForReconnectJitter(t *testing.T) {
	m := New(config.WebSocketConfig{Enabled: true, URL: "wss://example.com"})
	if m.randFloat64 == nil {
		t.Fatal("expected randFloat64 to be initialized")
	}

	if reflect.ValueOf(m.randFloat64).Pointer() == reflect.ValueOf(rand.Float64).Pointer() {
		t.Fatal("expected monitor to use instance RNG instead of global rand.Float64")
	}
}
