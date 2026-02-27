package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/tdawe1/gengowatcher-go/internal/dedupe"
	"github.com/tdawe1/gengowatcher-go/internal/reaction"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

type TelemetryEvent struct {
	JobID                string
	Source               gengo.Source
	DetectedAt           time.Time
	DetectedToDispatchMS float64
}

type TelemetrySink interface {
	Write(ctx context.Context, event TelemetryEvent) error
}

const (
	defaultTelemetryMaxInFlight  = 64
	defaultTelemetryWriteTimeout = 2 * time.Second
)

var ErrTelemetryBackpressure = errors.New("telemetry async pool saturated")

type RouterOption func(*Router)

type Router struct {
	gate                  *dedupe.Gate
	executor              *reaction.Executor
	telemetry             TelemetrySink
	telemetryMaxInFlight  int
	telemetryWriteTimeout time.Duration
	telemetrySlots        chan struct{}
	onTelemetryError      func(error)
	onFirstSeenJob        func(gengo.JobEvent)
}

func WithTelemetryMaxInFlight(maxInFlight int) RouterOption {
	return func(r *Router) {
		if r == nil {
			return
		}

		r.telemetryMaxInFlight = maxInFlight
	}
}

func WithTelemetryErrorHook(fn func(error)) RouterOption {
	return func(r *Router) {
		if r == nil {
			return
		}

		r.onTelemetryError = fn
	}
}

func WithTelemetryWriteTimeout(timeout time.Duration) RouterOption {
	return func(r *Router) {
		if r == nil {
			return
		}

		r.telemetryWriteTimeout = timeout
	}
}

// WithOnFirstSeenJob registers a callback for first-seen jobs during Handle.
//
// The callback executes synchronously on the router hot path, so it must be
// non-blocking. Panics are recovered to prevent hook failures from breaking
// reaction dispatch.
func WithOnFirstSeenJob(fn func(gengo.JobEvent)) RouterOption {
	return func(r *Router) {
		if r == nil {
			return
		}

		r.onFirstSeenJob = fn
	}
}

func NewRouter(gate *dedupe.Gate, executor *reaction.Executor, telemetry TelemetrySink, opts ...RouterOption) *Router {
	r := &Router{
		gate:                  gate,
		executor:              executor,
		telemetry:             telemetry,
		telemetryMaxInFlight:  defaultTelemetryMaxInFlight,
		telemetryWriteTimeout: defaultTelemetryWriteTimeout,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	if r.telemetryMaxInFlight <= 0 {
		r.telemetryMaxInFlight = defaultTelemetryMaxInFlight
	}
	if r.telemetryWriteTimeout <= 0 {
		r.telemetryWriteTimeout = defaultTelemetryWriteTimeout
	}

	r.telemetrySlots = make(chan struct{}, r.telemetryMaxInFlight)

	return r
}

func (r *Router) Handle(ctx context.Context, ev gengo.JobEvent) {
	if r == nil {
		return
	}

	if ev.Type != gengo.EventJobFound || ev.Job == nil {
		return
	}

	jobKey := canonicalJobKey(ev.Job)
	if jobKey == "" {
		return
	}

	if !r.firstSeen(jobKey) {
		return
	}

	r.runFirstSeenHook(ev)

	if ctx == nil {
		ctx = context.Background()
	}

	if r.executor != nil {
		r.executor.Dispatch(ctx, ev.Job.URL, ev.Job.Title)
	}

	if r.telemetry != nil {
		r.writeTelemetryAsync(ctx, ev, jobKey)
	}
}

func (r *Router) runFirstSeenHook(ev gengo.JobEvent) {
	if r == nil || r.onFirstSeenJob == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	r.onFirstSeenJob(ev)
}

func (r *Router) firstSeen(jobID string) bool {
	if r.gate == nil {
		return true
	}

	return r.gate.FirstSeen(jobID)
}

func (r *Router) writeTelemetryAsync(parentCtx context.Context, ev gengo.JobEvent, jobKey string) {
	if !r.acquireTelemetrySlot() {
		r.reportTelemetryError(ErrTelemetryBackpressure)
		return
	}

	if parentCtx == nil {
		parentCtx = context.Background()
	}

	jobID := strings.TrimSpace(ev.Job.ID)
	if jobID == "" {
		jobID = jobKey
	}

	telem := TelemetryEvent{
		JobID:                jobID,
		Source:               ev.Source,
		DetectedAt:           ev.Time,
		DetectedToDispatchMS: toMilliseconds(time.Since(ev.Time)),
	}

	go func() {
		defer r.releaseTelemetrySlot()

		writeCtx, cancel := context.WithTimeout(parentCtx, r.telemetryWriteTimeout)
		defer cancel()

		if err := r.telemetry.Write(writeCtx, telem); err != nil {
			r.reportTelemetryError(err)
		}
	}()
}

func (r *Router) acquireTelemetrySlot() bool {
	if r == nil {
		return false
	}

	select {
	case r.telemetrySlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Router) releaseTelemetrySlot() {
	if r == nil {
		return
	}

	select {
	case <-r.telemetrySlots:
	default:
	}
}

func (r *Router) reportTelemetryError(err error) {
	if r == nil || r.onTelemetryError == nil || err == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	r.onTelemetryError(err)
}

func canonicalJobKey(job *gengo.Job) string {
	if job == nil {
		return ""
	}

	if id := strings.TrimSpace(job.ID); id != "" {
		return "id:" + id
	}

	if key := urlDerivedKey(job.URL); key != "" {
		return "url:" + key
	}

	if key := stableFingerprint(job); key != "" {
		return "fp:" + key
	}

	return ""
}

func urlDerivedKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	cleanPath := path.Clean(strings.TrimSpace(u.EscapedPath()))
	if cleanPath == "." {
		cleanPath = ""
	}

	segments := strings.Split(strings.Trim(cleanPath, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if isDigits(segments[i]) {
			return "job:" + segments[i]
		}
	}

	if host == "" && cleanPath == "" {
		return ""
	}

	query := u.Query().Encode()
	if query == "" {
		return host + cleanPath
	}

	return host + cleanPath + "?" + query
}

func stableFingerprint(job *gengo.Job) string {
	title := strings.TrimSpace(strings.ToLower(job.Title))
	language := strings.TrimSpace(strings.ToLower(job.Language))
	currency := strings.TrimSpace(strings.ToUpper(job.Currency))

	reward := strconv.FormatFloat(job.Reward, 'f', 4, 64)
	unitCount := strconv.Itoa(job.UnitCount)
	deadline := "0"
	if !job.Deadline.IsZero() {
		deadline = strconv.FormatInt(job.Deadline.UTC().Unix(), 10)
	}

	hasSignal := title != "" || language != "" || currency != "" || job.Reward > 0 || job.UnitCount > 0 || !job.Deadline.IsZero()
	if !hasSignal {
		return ""
	}

	payload := strings.Join([]string{title, language, currency, reward, unitCount, deadline}, "|")
	digest := sha256.Sum256([]byte(payload))

	return hex.EncodeToString(digest[:16])
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func toMilliseconds(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}

	return float64(d) / float64(time.Millisecond)
}
