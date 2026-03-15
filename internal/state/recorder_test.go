package state

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestNewRecorder_LoadsInitialSnapshot(t *testing.T) {
	store := &stubSnapshotStore{
		loadSnapshot: Snapshot{
			Jobs: []*gengo.Job{{ID: "job-1"}},
			Stats: &gengo.Stats{
				TotalFound:   3,
				SessionFound: 1,
				BySource:     map[gengo.Source]int{gengo.SourceRSS: 3},
			},
		},
	}

	recorder, err := NewRecorder(store, RecorderDeps{})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	defer recorder.Close()

	snapshot := recorder.Snapshot()
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "job-1" {
		t.Fatalf("unexpected loaded jobs: %#v", snapshot.Jobs)
	}
	if snapshot.Stats.TotalFound != 3 {
		t.Fatalf("unexpected loaded stats: %#v", snapshot.Stats)
	}
}

func TestRecorder_RecordPersistsJobsAndStats(t *testing.T) {
	store := &stubSnapshotStore{
		loadSnapshot: newSnapshot(),
		saveCalled:   make(chan Snapshot, 1),
	}

	recorder, err := NewRecorder(store, RecorderDeps{MaxJobs: 2})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	defer recorder.Close()

	ok := recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{
		ID:    "job-42",
		Title: "JP -> EN",
		URL:   "https://gengo.com/jobs/42",
	}))
	if !ok {
		t.Fatal("expected record to enqueue event")
	}

	select {
	case saved := <-store.saveCalled:
		if len(saved.Jobs) != 1 || saved.Jobs[0].ID != "job-42" {
			t.Fatalf("unexpected saved jobs: %#v", saved.Jobs)
		}
		if saved.Stats.TotalFound != 1 || saved.Stats.BySource[gengo.SourceWebSocket] != 1 {
			t.Fatalf("unexpected saved stats: %#v", saved.Stats)
		}
	case <-time.After(time.Second):
		t.Fatal("expected persisted snapshot")
	}
}

func TestRecorder_RecordIgnoresNonJobEvents(t *testing.T) {
	store := &stubSnapshotStore{loadSnapshot: newSnapshot()}

	recorder, err := NewRecorder(store, RecorderDeps{})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	defer recorder.Close()

	if !recorder.Record(gengo.NewErrorEvent(gengo.SourceRSS, "boom")) {
		t.Fatal("expected record enqueue for non-job event")
	}

	time.Sleep(25 * time.Millisecond)

	if got := store.saveCount(); got != 0 {
		t.Fatalf("expected no saves for non-job event, got %d", got)
	}
}

func TestRecorder_RecordReturnsFalseWhenBufferFull(t *testing.T) {
	store := &blockingSnapshotStore{
		loadSnapshot: newSnapshot(),
		blockSave:    make(chan struct{}),
	}

	recorder, err := NewRecorder(store, RecorderDeps{Buffer: 2})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	defer func() {
		close(store.blockSave)
		_ = recorder.Close()
	}()

	first := recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-1"}))
	second := recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-2"}))
	third := recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-3"}))

	if !first || !second {
		t.Fatalf("expected first two records to enqueue, got first=%v second=%v", first, second)
	}
	if third {
		t.Fatal("expected third record to be dropped when buffer is full")
	}
}

func TestRecorder_CloseStopsFurtherRecording(t *testing.T) {
	store := &stubSnapshotStore{loadSnapshot: newSnapshot()}

	recorder, err := NewRecorder(store, RecorderDeps{})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	if recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-1"})) {
		t.Fatal("expected record to fail after close")
	}
}

func TestRecorder_ReportsSaveErrors(t *testing.T) {
	saveErr := errors.New("save failed")
	store := &stubSnapshotStore{
		loadSnapshot: newSnapshot(),
		saveErr:      saveErr,
	}
	errCh := make(chan error, 1)

	recorder, err := NewRecorder(store, RecorderDeps{
		OnError: func(err error) {
			errCh <- err
		},
	})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	defer recorder.Close()

	if !recorder.Record(gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: "job-1"})) {
		t.Fatal("expected record to enqueue event")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, saveErr) {
			t.Fatalf("expected save error %v, got %v", saveErr, err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected save error callback")
	}
}

func TestOpenRecorder_UsesStorePath(t *testing.T) {
	path := t.TempDir() + "/state.json"
	recorder, err := OpenRecorder(path, RecorderDeps{})
	if err != nil {
		t.Fatalf("open recorder: %v", err)
	}
	defer recorder.Close()

	if got := recorder.Snapshot().Version; got != currentVersion {
		t.Fatalf("expected default snapshot version %d, got %d", currentVersion, got)
	}
}

type stubSnapshotStore struct {
	loadSnapshot Snapshot
	saveErr      error
	saveCalled   chan Snapshot

	mu        sync.Mutex
	saveCalls int
}

func (s *stubSnapshotStore) Load() (Snapshot, error) {
	return cloneSnapshot(s.loadSnapshot), nil
}

func (s *stubSnapshotStore) Save(snapshot Snapshot) error {
	s.mu.Lock()
	s.saveCalls++
	s.mu.Unlock()

	if s.saveCalled != nil {
		select {
		case s.saveCalled <- cloneSnapshot(snapshot):
		default:
		}
	}

	return s.saveErr
}

func (s *stubSnapshotStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCalls
}

type blockingSnapshotStore struct {
	loadSnapshot Snapshot
	blockSave    chan struct{}
}

func (s *blockingSnapshotStore) Load() (Snapshot, error) {
	return cloneSnapshot(s.loadSnapshot), nil
}

func (s *blockingSnapshotStore) Save(snapshot Snapshot) error {
	<-s.blockSave
	_ = snapshot
	return nil
}
