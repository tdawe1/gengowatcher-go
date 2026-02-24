package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestStart_EmitsJobFoundFromJobPublishedMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"job_published","data":{"id":"job-42","title":"Hello"}}`))
		<-time.After(250 * time.Millisecond)
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
		<-time.After(500 * time.Millisecond)
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
