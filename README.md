# gengowatcher-go

Go rewrite of GengoWatcher with a clean monitor API and parity-focused RSS/WebSocket behavior.

## Status

- Phase 1 complete: core public types and monitor contract.
- Phase 2 complete: RSS and WebSocket monitors implemented with hardening/reliability follow-ups.
- Phase 3 in progress: TUI baseline (Jobs + Stats) and CLI launch path.
- Command-input, log-viewer, and state persistence are intentionally deferred until post-baseline phase work.
- Remaining phases (planned): full TUI scope, state persistence, notifications, Email monitor, Website monitor.

See `go-rewrite-plan.md` for full design and roadmap.

## What Is Implemented Today

- Public API types in `pkg/gengo` (`Job`, `JobEvent`, `Monitor`).
- Config loader in `internal/config` (defaults + env override + validation).
- RSS monitor in `internal/monitor/rss` with:
  - startup priming (no initial burst),
  - deduplication and min-reward filtering,
  - timeout and in-flight guard,
  - pause-file support,
  - exponential backoff and error events.
- WebSocket monitor in `internal/monitor/websocket` with:
  - post-connect auth payload,
  - `job_published` and `available_collection` parsing,
  - reconnect loop with bounded backoff,
  - handshake fallback when custom headers are rejected,
  - read timeout protection and error events.
- Runtime watcher pipeline in `internal/app` + `internal/pipeline` with:
  - canonical first-seen routing,
  - non-blocking reaction dispatch,
  - bounded telemetry writes with error hooks.
- Bubble Tea baseline UI in `internal/ui` with:
  - Jobs and Stats tabs,
  - live first-seen job updates from watcher runtime.
- CLI entrypoint in `cmd/gengowatcher`:
  - config loading/validation,
  - monitor enablement guard,
  - signal-aware TUI startup.

## Configuration (Current)

Important env vars currently used by implemented monitors:

- `GENGO_USER_ID`
- `GENGO_USER_SESSION`
- `GENGO_USER_KEY` (optional)
- `GENGO_RSS_PAUSE_FILE`
- `GENGO_RSS_PAUSE_SLEEP`
- `GENGO_RSS_MAX_BACKOFF`

## Development

Run all tests:

```bash
go test ./...
```

Run monitor-focused tests:

```bash
go test ./internal/monitor/rss ./internal/monitor/websocket -v
```
