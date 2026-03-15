package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestStore_SaveAndLoadRoundTrip(t *testing.T) {
	store := newTestStore(t)

	stats := gengo.NewStats()
	stats.TotalFound = 7
	stats.SessionFound = 2
	stats.BySource[gengo.SourceWebSocket] = 6
	stats.BySource[gengo.SourceRSS] = 1
	stats.LastUpdated = time.Date(2026, time.February, 27, 12, 0, 0, 0, time.UTC)

	snapshot := Snapshot{
		Jobs: []*gengo.Job{
			{
				ID:       "job-42",
				Title:    "JP -> EN",
				Source:   gengo.SourceWebSocket,
				Reward:   12.5,
				Currency: "USD",
				FoundAt:  time.Date(2026, time.February, 27, 12, 0, 0, 0, time.UTC),
				URL:      "https://gengo.com/jobs/42",
			},
		},
		Stats: stats,
	}

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	if loaded.Version != currentVersion {
		t.Fatalf("expected version %d, got %d", currentVersion, loaded.Version)
	}
	if len(loaded.Jobs) != 1 || loaded.Jobs[0] == nil || loaded.Jobs[0].ID != "job-42" {
		t.Fatalf("unexpected loaded jobs: %#v", loaded.Jobs)
	}
	if loaded.Stats == nil {
		t.Fatal("expected loaded stats")
	}
	if loaded.Stats.TotalFound != 7 || loaded.Stats.SessionFound != 2 {
		t.Fatalf("unexpected loaded stats counts: %#v", loaded.Stats)
	}
	if loaded.Stats.BySource[gengo.SourceWebSocket] != 6 || loaded.Stats.BySource[gengo.SourceRSS] != 1 {
		t.Fatalf("unexpected loaded stats by source: %#v", loaded.Stats.BySource)
	}
	if loaded.LastUpdated.IsZero() {
		t.Fatal("expected load to preserve last_updated")
	}
}

func TestStore_LoadMissingFileReturnsDefaultSnapshot(t *testing.T) {
	store := newTestStore(t)

	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load missing snapshot: %v", err)
	}

	if snapshot.Version != currentVersion {
		t.Fatalf("expected default version %d, got %d", currentVersion, snapshot.Version)
	}
	if len(snapshot.Jobs) != 0 {
		t.Fatalf("expected no jobs, got %#v", snapshot.Jobs)
	}
	if snapshot.Stats == nil {
		t.Fatal("expected initialized stats")
	}
	if snapshot.Stats.BySource == nil {
		t.Fatal("expected initialized by_source map")
	}
}

func TestStore_SaveWritesValidJSONAndCleansTempFiles(t *testing.T) {
	store := newTestStore(t)

	if err := store.Save(Snapshot{
		Jobs: []*gengo.Job{{ID: "job-1", Source: gengo.SourceRSS}},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	var decoded Snapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("expected valid state json, got %v", err)
	}
	if decoded.Version != currentVersion {
		t.Fatalf("expected version %d, got %d", currentVersion, decoded.Version)
	}

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), ".*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no leftover temp files, found %v", matches)
	}
}

func TestStore_ConcurrentSaveAndLoadRemainConsistent(t *testing.T) {
	store := newTestStore(t)

	var wg sync.WaitGroup
	for i := range 24 {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			stats := gengo.NewStats()
			stats.TotalFound = i + 1
			stats.SessionFound = i + 1
			stats.BySource[gengo.SourceRSS] = i + 1

			if err := store.Save(Snapshot{
				Jobs: []*gengo.Job{{
					ID:      "job-concurrent",
					Title:   "job",
					Source:  gengo.SourceRSS,
					Reward:  float64(i),
					FoundAt: time.Now().UTC(),
				}},
				Stats: stats,
			}); err != nil {
				t.Errorf("save snapshot: %v", err)
				return
			}

			if _, err := store.Load(); err != nil {
				t.Errorf("load snapshot: %v", err)
			}
		}()
	}
	wg.Wait()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("final load snapshot: %v", err)
	}
	if loaded.Version != currentVersion {
		t.Fatalf("expected final version %d, got %d", currentVersion, loaded.Version)
	}
	if loaded.Stats == nil || loaded.Stats.BySource == nil {
		t.Fatalf("expected initialized stats in final snapshot, got %#v", loaded.Stats)
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read final state file: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("expected final file to contain valid json, got %v", err)
	}
}

func TestNew_EmptyPathReturnsError(t *testing.T) {
	store, err := New("")
	if err == nil {
		t.Fatal("expected empty path error")
	}
	if store != nil {
		t.Fatalf("expected nil store, got %#v", store)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	return store
}
