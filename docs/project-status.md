# Project Status

Last updated: 2026-02-26

## Current Phase

- Phase 2.5 hardening follow-ups plus runtime pipeline wiring are implemented.
- Full test suite is passing (`go test ./...`).
- Work is on `main` with a focused commit series for each task area.

## Completed in Phase 2.5

- Enforced RSS minimum poll interval guardrail (`>=31s`).
- Added WebSocket heartbeat/pong liveness for long idle stability.
- Added shared bounded dedupe gate (TTL/capacity) and integrated it into RSS.
- Added non-blocking reaction executor with async panic/error hooks.
- Added SQLite telemetry sink and rolling latency view.
- Added first-seen router with async best-effort telemetry path.
- Added planning and design docs for this hardening pass.
- Added canonical router dedupe fallback chain: `job.id -> URL-derived key -> stable fingerprint`.
- Added bounded async backpressure controls for reaction dispatch and telemetry writes.
- Added telemetry failure surfacing hooks for sink write errors and telemetry saturation.
- Wired monitors into runtime watcher pipeline (`monitor -> router -> reaction + telemetry`).
- Added jittered WebSocket reconnect backoff.
- Added RSS conditional polling support (`If-None-Match` / `If-Modified-Since`) with `304 Not Modified` handling.

## Review Outcome

- Status: ready for Phase 3 kickoff, with non-blocking telemetry aggregate improvements remaining.
- Security posture: no critical injection/XSS-style findings in this scope.

## Must-Fix Before Phase 3

- All must-fix items are now completed.
- Phase 3 can start once non-blocking recommendations are prioritized.

## Recommended (Non-Blocking)

- Improve telemetry aggregates toward p50/p90/p99 and source asymmetry tracking.

## Update Log

- 2026-02-26: Completed second-pass review follow-ups (telemetry cancellation timeout, bounded watcher shutdown, instance jitter RNG, query-sensitive URL fallback dedupe, RSS 304 validator refresh) and validated merge readiness.
- 2026-02-26: Wired runtime monitor-router-reaction-telemetry flow and implemented recommended reconnect jitter plus RSS conditional polling headers.
- 2026-02-26: Completed pre-Phase 3 must-fix follow-ups (canonical dedupe fallback, bounded async backpressure, telemetry error surfacing) and marked Phase 3 as unblocked.
- 2026-02-24: Created project status baseline; recorded completed Phase 2.5 hardening scope, review outcome, and pre-Phase 3 must-fix items.

### How To Update This File

- Update `Last updated` at the top.
- Add a new bullet under `Update Log` with date and what changed.
- Keep newest entries first.
