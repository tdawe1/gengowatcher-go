package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const defaultRollingWindow = 5 * time.Minute

var (
	ErrEmptyDSN        = errors.New("sqlite telemetry sink dsn is required")
	ErrSinkUnavailable = errors.New("sqlite telemetry sink is unavailable")
	ErrSinkClosed      = errors.New("sqlite telemetry sink is closed")
)

type Event struct {
	JobID                string
	Source               string
	DetectedAt           time.Time
	DetectedToDispatchMS float64
	DispatchToOpenMS     float64
	DegradedMode         bool
	Outcome              string
}

type LatencyStats struct {
	Count                   int64
	AvgDetectedToDispatchMS float64
	AvgDispatchToOpenMS     float64
}

type Sink struct {
	db     *sql.DB
	closed atomic.Bool
}

func New(dsn string) (*Sink, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, ErrEmptyDSN
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	sink := &Sink{db: db}
	if err := sink.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return sink, nil
}

func (s *Sink) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	if s.closed.Swap(true) {
		return nil
	}

	return s.db.Close()
}

func (s *Sink) Write(ctx context.Context, ev Event) error {
	if err := s.ensureAvailable(); err != nil {
		return err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	if ev.DetectedAt.IsZero() {
		ev.DetectedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO events_raw (
			job_id,
			source,
			detected_at,
			detected_to_dispatch_ms,
			dispatch_to_open_ms,
			degraded_mode,
			outcome
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.JobID,
		ev.Source,
		ev.DetectedAt.UTC(),
		ev.DetectedToDispatchMS,
		ev.DispatchToOpenMS,
		boolToInt(ev.DegradedMode),
		ev.Outcome,
	)
	if err != nil {
		if errors.Is(err, sql.ErrConnDone) || s.closed.Load() {
			return ErrSinkClosed
		}

		return fmt.Errorf("insert telemetry event: %w", err)
	}

	return nil
}

func (s *Sink) LatencySnapshot(ctx context.Context) (LatencyStats, error) {
	if err := s.ensureAvailable(); err != nil {
		return LatencyStats{}, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	var stats LatencyStats
	err := s.db.QueryRowContext(ctx, `SELECT count, avg_detected_to_dispatch_ms, avg_dispatch_to_open_ms FROM latency_5m`).Scan(
		&stats.Count,
		&stats.AvgDetectedToDispatchMS,
		&stats.AvgDispatchToOpenMS,
	)
	if err != nil {
		if errors.Is(err, sql.ErrConnDone) || s.closed.Load() {
			return LatencyStats{}, ErrSinkClosed
		}

		return LatencyStats{}, fmt.Errorf("query latency snapshot: %w", err)
	}

	return stats, nil
}

func (s *Sink) initSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	schemaSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS events_raw (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			source TEXT NOT NULL,
			detected_at DATETIME NOT NULL,
			detected_to_dispatch_ms REAL NOT NULL,
			dispatch_to_open_ms REAL NOT NULL,
			degraded_mode INTEGER NOT NULL DEFAULT 0,
			outcome TEXT NOT NULL DEFAULT ''
		);

		CREATE VIEW IF NOT EXISTS latency_5m AS
		SELECT
			COUNT(*) AS count,
			COALESCE(AVG(detected_to_dispatch_ms), 0.0) AS avg_detected_to_dispatch_ms,
			COALESCE(AVG(dispatch_to_open_ms), 0.0) AS avg_dispatch_to_open_ms
		FROM events_raw
		WHERE detected_at >= datetime('now', '-%d minutes');

		CREATE INDEX IF NOT EXISTS idx_events_raw_detected_at
		ON events_raw(detected_at);
	`, int(defaultRollingWindow/time.Minute))

	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}

	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}

	return 0
}

func (s *Sink) ensureAvailable() error {
	if s == nil || s.db == nil {
		return ErrSinkUnavailable
	}

	if s.closed.Load() {
		return ErrSinkClosed
	}

	return nil
}
