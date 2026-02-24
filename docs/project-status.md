# Project Status

Last updated: 2026-02-24

## Current Phase

- Phase 2.5 hardening is implemented and committed.
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

## Review Outcome

- Status: conditionally ready, with key reliability follow-ups identified before deep Phase 3 work.
- Security posture: no critical injection/XSS-style findings in this scope.

## Must-Fix Before Phase 3

1. Implement canonical dedupe key fallback in router:
   - current: ID-only
   - required: `job.id -> URL-derived key -> stable fingerprint`
2. Add bounded async execution/backpressure for reaction + telemetry paths:
   - avoid unbounded goroutine fan-out under burst traffic
3. Surface telemetry sink write failures:
   - current behavior can silently drop errors
   - required: hook/log/metric for operator visibility

## Recommended (Non-Blocking)

- Add jitter to WebSocket reconnect backoff.
- Add RSS conditional polling headers (`If-None-Match` / `If-Modified-Since`).
- Improve telemetry aggregates toward p50/p90/p99 and source asymmetry tracking.

## Update Log

- 2026-02-24: Created project status baseline; recorded completed Phase 2.5 hardening scope, review outcome, and pre-Phase 3 must-fix items.

### How To Update This File

- Update `Last updated` at the top.
- Add a new bullet under `Update Log` with date and what changed.
- Keep newest entries first.
