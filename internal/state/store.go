package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const currentVersion = 1

var ErrEmptyPath = errors.New("state file path is required")

type Snapshot struct {
	Version     int          `json:"version"`
	Jobs        []*gengo.Job `json:"jobs"`
	Stats       *gengo.Stats `json:"stats"`
	LastUpdated time.Time    `json:"last_updated"`
}

type Store struct {
	path     string
	lockPath string
	mu       sync.RWMutex
}

func New(path string) (*Store, error) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "" || cleanPath == "." {
		return nil, ErrEmptyPath
	}

	return &Store{
		path:     cleanPath,
		lockPath: cleanPath + ".lock",
	}, nil
}

func (s *Store) Load() (Snapshot, error) {
	if s == nil {
		return Snapshot{}, ErrEmptyPath
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	} else if err != nil {
		return Snapshot{}, fmt.Errorf("stat state file: %w", err)
	}

	lockFile, err := s.openLock(syscall.LOCK_SH)
	if err != nil {
		return Snapshot{}, err
	}
	defer closeLock(lockFile)

	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newSnapshot(), nil
		}
		return Snapshot{}, fmt.Errorf("open state file: %w", err)
	}
	defer file.Close()

	var snapshot Snapshot
	if err := json.NewDecoder(file).Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode state file: %w", err)
	}

	return normalizeSnapshot(snapshot), nil
}

func (s *Store) Save(snapshot Snapshot) error {
	if s == nil {
		return ErrEmptyPath
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	lockFile, err := s.openLock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer closeLock(lockFile)

	snapshot = normalizeSnapshot(snapshot)
	snapshot.LastUpdated = time.Now().UTC()
	if snapshot.Stats.LastUpdated.IsZero() {
		snapshot.Stats.LastUpdated = snapshot.LastUpdated
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state snapshot: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	base := filepath.Base(s.path)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}

	tmpName := tmp.Name()
	cleanupTmp := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanupTmp()
		return fmt.Errorf("chmod temp state file: %w", err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		cleanupTmp()
		return fmt.Errorf("replace state file: %w", err)
	}

	if err := syncDir(dir); err != nil {
		return err
	}

	return nil
}

func newSnapshot() Snapshot {
	return Snapshot{
		Version: currentVersion,
		Jobs:    []*gengo.Job{},
		Stats:   gengo.NewStats(),
	}
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	if snapshot.Version <= 0 {
		snapshot.Version = currentVersion
	}
	if snapshot.Jobs == nil {
		snapshot.Jobs = []*gengo.Job{}
	}
	if snapshot.Stats == nil {
		snapshot.Stats = gengo.NewStats()
	}
	if snapshot.Stats.BySource == nil {
		snapshot.Stats.BySource = map[gengo.Source]int{}
	}
	if snapshot.LastUpdated.IsZero() && !snapshot.Stats.LastUpdated.IsZero() {
		snapshot.LastUpdated = snapshot.Stats.LastUpdated.UTC()
	}

	return snapshot
}

func (s *Store) openLock(mode int) (*os.File, error) {
	lockFile, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock file: %w", err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), mode); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock state file: %w", err)
	}

	return lockFile, nil
}

func closeLock(lockFile *os.File) {
	if lockFile == nil {
		return
	}

	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open state directory: %w", err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("sync state directory: %w", err)
	}

	return nil
}
