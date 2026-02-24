package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSink_PersistsAndAggregatesLatency(t *testing.T) {
	sink, err := New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	defer sink.Close()

	ev := Event{
		JobID:                "42",
		Source:               "websocket",
		DetectedAt:           time.Now().UTC(),
		DetectedToDispatchMS: 12.0,
		DispatchToOpenMS:     40.0,
	}

	if err := sink.Write(context.Background(), ev); err != nil {
		t.Fatalf("write event: %v", err)
	}

	stats, err := sink.LatencySnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if stats.Count != 1 {
		t.Fatalf("expected 1 row, got %d", stats.Count)
	}

	if stats.AvgDetectedToDispatchMS != 12.0 {
		t.Fatalf("expected avg detected->dispatch 12.0, got %f", stats.AvgDetectedToDispatchMS)
	}

	if stats.AvgDispatchToOpenMS != 40.0 {
		t.Fatalf("expected avg dispatch->open 40.0, got %f", stats.AvgDispatchToOpenMS)
	}
}

func TestNew_EmptyDSNReturnsError(t *testing.T) {
	sink, err := New("")
	if err == nil {
		t.Fatal("expected error for empty dsn")
	}
	if sink != nil {
		t.Fatal("expected nil sink for empty dsn")
	}
}

func TestSink_LatencySnapshotExcludesOldEvents(t *testing.T) {
	sink, err := New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	defer sink.Close()

	oldEvent := Event{
		JobID:                "old",
		Source:               "rss",
		DetectedAt:           time.Now().UTC().Add(-10 * time.Minute),
		DetectedToDispatchMS: 100.0,
		DispatchToOpenMS:     100.0,
	}
	if err := sink.Write(context.Background(), oldEvent); err != nil {
		t.Fatalf("write old event: %v", err)
	}

	recentEvent := Event{
		JobID:                "new",
		Source:               "websocket",
		DetectedAt:           time.Now().UTC(),
		DetectedToDispatchMS: 10.0,
		DispatchToOpenMS:     20.0,
	}
	if err := sink.Write(context.Background(), recentEvent); err != nil {
		t.Fatalf("write recent event: %v", err)
	}

	stats, err := sink.LatencySnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if stats.Count != 1 {
		t.Fatalf("expected 1 recent row, got %d", stats.Count)
	}
	if stats.AvgDetectedToDispatchMS != 10.0 {
		t.Fatalf("expected avg detected->dispatch 10.0, got %f", stats.AvgDetectedToDispatchMS)
	}
	if stats.AvgDispatchToOpenMS != 20.0 {
		t.Fatalf("expected avg dispatch->open 20.0, got %f", stats.AvgDispatchToOpenMS)
	}
}

func TestSink_WriteAfterCloseReturnsError(t *testing.T) {
	sink, err := New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("close sink: %v", err)
	}

	err = sink.Write(context.Background(), Event{JobID: "42", Source: "websocket"})
	if err == nil {
		t.Fatal("expected write after close to return error")
	}
}

func TestSink_NilContextOperationsAreSafe(t *testing.T) {
	sink, err := New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	defer sink.Close()

	err = sink.Write(nil, Event{JobID: "42", Source: "websocket"})
	if err != nil {
		t.Fatalf("write with nil context: %v", err)
	}

	stats, err := sink.LatencySnapshot(nil)
	if err != nil {
		t.Fatalf("snapshot with nil context: %v", err)
	}
	if stats.Count != 1 {
		t.Fatalf("expected 1 row, got %d", stats.Count)
	}
}

func TestSink_NilReceiverOperationsDoNotPanic(t *testing.T) {
	var sink *Sink

	if err := sink.Write(context.Background(), Event{}); err == nil {
		t.Fatal("expected write on nil sink to return error")
	}

	_, err := sink.LatencySnapshot(context.Background())
	if err == nil {
		t.Fatal("expected snapshot on nil sink to return error")
	}

	if !errors.Is(err, ErrSinkUnavailable) {
		t.Fatalf("expected ErrSinkUnavailable, got %v", err)
	}
}
