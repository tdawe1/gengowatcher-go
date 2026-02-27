# ARCHITECTURE

## Overview

`gengowatcher-go` is a monitor-first system for discovering Gengo jobs and emitting normalized events through a stable public API. The current implementation prioritizes low-latency ingestion from WebSocket and RSS with resilience controls for long idle periods and burst traffic.

Primary goals:

- Keep event contracts clean for downstream consumers.
- Isolate source-specific behavior inside monitor implementations.
- Improve reliability and observability without blocking the detection hot path.

## Top-Level Structure

- `pkg/gengo`
  - Public, stable contracts (`Job`, `JobEvent`, `Monitor`).
- `internal/config`
  - Config loading, defaults, env overrides, validation.
- `internal/app`
  - Runtime watcher orchestration and monitor fan-in.
- `internal/monitor/websocket`
  - WebSocket ingestion and reconnect behavior.
- `internal/monitor/rss`
  - RSS polling and item parsing.
- `internal/dedupe`
  - Shared bounded first-seen gate.
- `internal/reaction`
  - Non-blocking action dispatch primitives.
- `internal/pipeline`
  - First-seen routing + telemetry handoff.
- `internal/telemetry/sqlite`
  - Local telemetry persistence and rolling latency snapshot.
- `internal/ui`
  - Bubble Tea model and runtime bridge from watcher events to UI messages.

## Data Model and Contracts

Defined in `pkg/gengo/models.go`:

- `Job`: normalized job payload (`ID`, `Title`, `Reward`, `Source`, `URL`, `FoundAt`, etc.).
- `JobEvent`: transport envelope for monitor output.
- `EventType`: includes `job_found` and `error` paths used by monitors.

Defined in `pkg/gengo/monitor.go`:

- `Monitor` interface:
  - `Start(ctx, events)`
  - `Name()`
  - `Source()`
  - `Enabled()`

This separation allows monitor internals to evolve while keeping consumer code stable.

## Ingestion Architecture

### WebSocket Path

`internal/monitor/websocket/monitor.go`:

- Connects to configured WS endpoint.
- Sends auth payload after connection.
- Parses `job_published` and `available_collection` messages into `EventJobFound`.
- Uses reconnect with bounded exponential backoff.
- Adds heartbeat (`Ping`) + pong deadline extension to survive long idle periods.
- Emits `EventError` on recoverable failures and retries.

### RSS Path

`internal/monitor/rss/monitor.go`:

- Polls configured RSS URL at watcher interval.
- Enforces startup priming to avoid initial duplicate burst.
- Parses rewards and applies min-reward filtering.
- Uses pause-file support and exponential failure backoff.
- Uses shared bounded dedupe gate for repeated item suppression.

Config guardrail in `internal/config/config.go` requires RSS polling interval >= 31s when RSS is enabled.

## Hardening Components (Phase 2.5)

### Shared Dedupe Gate

`internal/dedupe/gate.go`:

- Thread-safe first-seen gate.
- TTL-based expiration.
- Capacity-based deterministic eviction.
- Used by RSS monitor and router-level first-seen checks.

### Reaction Executor

`internal/reaction/executor.go`:

- Dispatches open + notify actions asynchronously.
- Contains panic inside worker goroutines.
- Exposes optional async error/panic hooks for observability.

### Router

`internal/pipeline/router.go`:

- Accepts normalized `JobEvent`.
- Ignores non-`job_found` and invalid payloads.
- Applies first-seen suppression before action dispatch.
- Dispatches reactions immediately.
- Writes telemetry asynchronously (best effort).

### Telemetry Sink

`internal/telemetry/sqlite/sink.go`:

- SQLite-backed event sink.
- Stores telemetry events in `events_raw`.
- Provides `latency_5m` rolling aggregate view.
- Includes sink lifecycle/error handling (empty DSN, unavailable/closed sink states).

### TUI Runtime Bridge

`internal/ui/tui.go`:

- Uses a bounded event channel between watcher callbacks and Bubble Tea message handling.
- Treats enqueue overflow as a counted drop (non-blocking hot path).
- Aggregates dropped-event reporting on interval with final flush during shutdown.
- Emits explicit observability signal when watcher join exceeds timeout.
- Applies bounded post-timeout drain window to capture late drops and preserve late watcher fatal error propagation when available.

## Current Risk Notes

Documented operational follow-ups:

1. Percentile telemetry views (p50/p90/p99) and source-asymmetry visibility are still pending.
2. Full command-input/log-viewer UX remains deferred until after Phase 3 baseline stabilizes.
3. State persistence and notification delivery remain deferred to post-baseline phases.

See `docs/project-status.md` for active priorities.

## Verification Strategy

Use package-focused tests for architecture-critical paths:

```bash
go test ./internal/config ./internal/monitor/rss ./internal/monitor/websocket ./internal/dedupe ./internal/reaction ./internal/telemetry/sqlite ./internal/pipeline -v
```

Run full regression before merge:

```bash
go test ./...
```
