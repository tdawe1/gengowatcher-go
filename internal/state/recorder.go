package state

import (
	"errors"
	"sync"

	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const (
	defaultRecorderBuffer = 128
	defaultMaxJobs        = 200
)

var ErrRecorderClosed = errors.New("state recorder is closed")

type snapshotStore interface {
	Load() (Snapshot, error)
	Save(Snapshot) error
}

type RecorderDeps struct {
	Buffer  int
	MaxJobs int
	OnError func(error)
}

type Recorder struct {
	store   snapshotStore
	onError func(error)
	maxJobs int
	events  chan gengo.JobEvent

	mu       sync.RWMutex
	snapshot Snapshot
	sendMu   sync.RWMutex

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

func NewRecorder(store snapshotStore, deps RecorderDeps) (*Recorder, error) {
	if store == nil {
		return nil, ErrEmptyPath
	}

	snapshot, err := store.Load()
	if err != nil {
		return nil, err
	}

	buffer := deps.Buffer
	if buffer <= 0 {
		buffer = defaultRecorderBuffer
	}

	maxJobs := deps.MaxJobs
	if maxJobs <= 0 {
		maxJobs = defaultMaxJobs
	}

	r := &Recorder{
		store:    store,
		onError:  deps.OnError,
		maxJobs:  maxJobs,
		events:   make(chan gengo.JobEvent, buffer),
		snapshot: normalizeSnapshot(snapshot),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
	}

	go r.run()

	return r, nil
}

func OpenRecorder(path string, deps RecorderDeps) (*Recorder, error) {
	store, err := New(path)
	if err != nil {
		return nil, err
	}

	return NewRecorder(store, deps)
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return newSnapshot()
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return cloneSnapshot(r.snapshot)
}

func (r *Recorder) Record(ev gengo.JobEvent) bool {
	if r == nil {
		return false
	}

	r.sendMu.RLock()
	defer r.sendMu.RUnlock()

	select {
	case <-r.closed:
		return false
	default:
	}

	select {
	case r.events <- ev:
		return true
	default:
		return false
	}
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}

	r.closeOnce.Do(func() {
		r.sendMu.Lock()
		defer r.sendMu.Unlock()
		close(r.closed)
		close(r.events)
	})

	<-r.done
	return nil
}

func (r *Recorder) run() {
	defer close(r.done)

	for {
		ev, ok := <-r.events
		if !ok {
			return
		}

		snapshot, changed := r.applyBatch(ev)
		if !changed {
			continue
		}

		if err := r.store.Save(snapshot); err != nil {
			r.reportError(err)
		}
	}
}

func (r *Recorder) applyBatch(first gengo.JobEvent) (Snapshot, bool) {
	changed := false

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.applyEventLocked(first) {
		changed = true
	}

	for {
		select {
		case ev, ok := <-r.events:
			if !ok {
				return cloneSnapshot(r.snapshot), changed
			}
			if r.applyEventLocked(ev) {
				changed = true
			}
		default:
			return cloneSnapshot(r.snapshot), changed
		}
	}
}

func (r *Recorder) applyEventLocked(ev gengo.JobEvent) bool {
	if ev.Type != gengo.EventJobFound || ev.Job == nil {
		return false
	}

	job := cloneJob(ev.Job)
	r.snapshot.Jobs = append([]*gengo.Job{job}, r.snapshot.Jobs...)
	if len(r.snapshot.Jobs) > r.maxJobs {
		r.snapshot.Jobs = r.snapshot.Jobs[:r.maxJobs]
	}
	r.snapshot.Stats.Increment(ev.Source)
	r.snapshot.LastUpdated = r.snapshot.Stats.LastUpdated.UTC()

	return true
}

func (r *Recorder) reportError(err error) {
	if r == nil || r.onError == nil || err == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	r.onError(err)
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot = normalizeSnapshot(snapshot)

	clone := Snapshot{
		Version:     snapshot.Version,
		Jobs:        make([]*gengo.Job, 0, len(snapshot.Jobs)),
		LastUpdated: snapshot.LastUpdated,
	}

	if snapshot.Stats != nil {
		cloneStats := *snapshot.Stats
		cloneStats.BySource = make(map[gengo.Source]int, len(snapshot.Stats.BySource))
		for source, count := range snapshot.Stats.BySource {
			cloneStats.BySource[source] = count
		}
		clone.Stats = &cloneStats
	} else {
		clone.Stats = gengo.NewStats()
	}

	for _, job := range snapshot.Jobs {
		clone.Jobs = append(clone.Jobs, cloneJob(job))
	}

	return clone
}

func cloneJob(job *gengo.Job) *gengo.Job {
	if job == nil {
		return nil
	}

	clone := *job
	if job.Payload != nil {
		clone.Payload = append([]byte(nil), job.Payload...)
	}

	return &clone
}
