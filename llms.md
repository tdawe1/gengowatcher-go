# llms.md

This file is the quick-start context for LLM agents working in `gengowatcher-go`.

## What This Project Is

- Go rewrite of GengoWatcher focused on monitor parity and composable API.
- Public contract lives in `pkg/gengo`.
- Current implementation focus is low-latency RSS + WebSocket ingestion hardening.

## Current Status (Important)

- Phase 2.5 hardening plus reliability follow-ups are implemented.
- Tests are currently green with `go test ./...`.
- Source-of-truth status is tracked in `docs/project-status.md`.

## Core Contracts

- `pkg/gengo/monitor.go`
  - `Monitor.Start(ctx, events)` blocks until cancellation/fatal error.
  - Non-fatal monitor issues should emit `EventError` and continue.
- `pkg/gengo/models.go`
  - `JobEvent` and `Job` are the canonical event payload types.
  - `EventJobFound` is the primary hot-path event.

## Implemented Runtime Components

- `internal/config`
  - TOML + env loading and validation.
  - Includes RSS minimum interval guardrail (`>=31s` when RSS enabled).
- `internal/monitor/websocket`
  - Connect/auth, parse job events, reconnect loop.
  - Heartbeat + pong deadline support for idle stability.
- `internal/monitor/rss`
  - Polling, startup priming, reward filtering, backoff.
  - Uses shared bounded dedupe gate.
- `internal/dedupe`
  - TTL + capacity gate for first-seen suppression.
- `internal/reaction`
  - Non-blocking action dispatch (`open`, `notify`) with async panic/error hooks.
- `internal/telemetry/sqlite`
  - SQLite sink for event telemetry and rolling latency view.
- `internal/pipeline`
  - Router: canonical first-seen keying (`id -> url -> fingerprint`) + reaction dispatch + bounded telemetry write.

## Known Gaps / Next Priority

Must-fix reliability items are complete. Next priorities:

1. Start Phase 3 TUI implementation (jobs/stats/logs baseline).
2. Improve telemetry aggregates toward p50/p90/p99 and source asymmetry visibility.
3. Keep runtime cancellation and backpressure guarantees intact while integrating Phase 3 UI paths.

See `docs/project-status.md` for latest callouts.

## How To Validate Changes

- Full suite:

```bash
go test ./...
```

- Focused packages:

```bash
go test ./internal/config ./internal/monitor/rss ./internal/monitor/websocket ./internal/dedupe ./internal/reaction ./internal/telemetry/sqlite ./internal/pipeline -v
```

## Editing Guidance for Agents

- Preserve `pkg/gengo` compatibility unless explicitly changing public API.
- Keep monitor hot-path work non-blocking and cancellation-aware.
- Prefer small, test-backed commits per subsystem.
- Update `docs/project-status.md` when architecture or risk posture changes.
