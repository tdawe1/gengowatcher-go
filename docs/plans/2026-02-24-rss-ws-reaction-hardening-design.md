# RSS+WS Reaction Hardening Design

Date: 2026-02-24
Status: Validated design (pre-implementation)

## Goal

Harden the monitor core before Phase 3 so the app behaves as a latency-first job capture system under long idle periods and burst arrivals.

## Confirmed Constraints and Decisions

- Both `WebSocket` and `RSS` are mandatory and complementary.
- Job capture is won/lost in seconds, so post-notification reaction time is the primary optimization target.
- Source behavior is black-box; design must be resilient without relying on undocumented guarantees.
- Dedupe policy is `first-seen wins` across sources.
- Browser strategy is managed warm profile with existing authenticated session/cookies.
- Degraded mode policy is `open anyway + loud warning`.
- Dedupe defaults are `TTL=72h` and `capacity=50k`.
- Telemetry default persistence is SQLite with rolling views.

## Architecture

1. `WSMonitor` (primary low-latency source)
2. `RSSMonitor` (coverage + resilience source)
3. `GlobalDedupeGate` (shared across sources, first-seen wins)
4. `EligibilityGate` (pure in-memory rules)
5. `ReactionExecutor` (immediate open + notify in parallel)
6. `BrowserProfileManager` (warm/authenticated profile lifecycle)
7. `TelemetrySink` (async SQLite writes and rolling metrics)

Hot path principle: receive -> dedupe -> filter -> dispatch actions. No disk/network enrichment in this path.

## Component Design

### WebSocket Monitor

- Add explicit ping/pong liveness with heartbeat interval and pong timeout.
- Use jittered reconnect backoff tuned for fast recovery after true disconnects.
- Keep monitor errors non-fatal and continue reconnect loop.
- Continue header fallback behavior for handshake rejection.

### RSS Monitor

- Enforce hard config guard: poll interval must never go below 31 seconds.
- Keep pause-file and exponential backoff behavior.
- Preserve startup priming and dedupe integration.
- Add optional conditional requests (`If-None-Match`/`If-Modified-Since`) where available.

### Global Dedupe Gate

- Shared bounded cache keyed by canonical job ID.
- Canonical key strategy: `job.id` first, then parsed ID from URL, then stable fallback fingerprint.
- On first sighting: emit immediately and store with expiry.
- On duplicate sighting inside TTL: suppress downstream action.

### Reaction Executor

- Eligible jobs trigger concurrent actions:
  - open URL in managed warm profile
  - desktop notification
  - sound alert
- Return control immediately after dispatching actions.
- Action failures emit high-severity warnings but never stop ingest pipeline.

### Browser Profile Manager

- Manage dedicated browser profile for Gengo session continuity.
- Health probes (profile live, auth reachability) run out-of-band.
- Probe failures switch state to degraded mode but do not block opening jobs.

## Telemetry (Primary Focus)

### Event Envelope

Each processed candidate writes a compact envelope asynchronously:

- `source`
- `job_id`
- `detected_at`
- `dedupe_at`
- `dispatch_at`
- `open_started_at`
- `notify_started_at`
- `degraded_mode`
- `captcha_signal`
- `outcome`

### SQLite Storage

Proposed tables:

- `events_raw`
- `actions_raw`
- `monitor_health`

Proposed rolling views/materialized aggregates:

- `latency_5m` (P50/P90/P99 dispatch and open timings)
- `hourly_health` (`jobs_seen`, `jobs_eligible`, `actions_started`, `opens_failed`, `ws_reconnects`, `rss_errors`)
- `source_asymmetry` (`ws_only`, `rss_only`, `both_seen_suppressed`)

### Alert Thresholds

- `p99(detected_to_dispatch_ms) > 150ms`
- `open_fail_rate > 2%` over 10 minutes
- `degraded_mode_duration > 60s`

These alerts optimize what matters operationally: reaction speed and successful action execution.

## Error Handling Policy

- Monitor-level errors are non-fatal and retried.
- Dedupe/filter errors fail open to preserve capture chance (with warning telemetry).
- Action-level errors are isolated per action; one failure cannot block others.
- Pipeline must remain alive unless context is cancelled.

## Test Strategy

1. Long-idle WS keepalive test (no churn during idle).
2. WS stall/disconnect recovery test with bounded reconnect latency.
3. RSS min-interval validation test (`>=31s` guard).
4. Cross-source duplicate suppression test (first-seen wins).
5. Degraded browser path test (open anyway + warning).
6. Telemetry pipeline backpressure test (hot path never blocks on DB).
7. End-to-end timing assertion test (dispatch latency budget).

## Success Criteria

- Stable WS sessions during long idle periods.
- No duplicate user actions for same job across RSS+WS inside dedupe window.
- Guaranteed RSS compliance with 31-second minimum polling interval.
- Immediate job action dispatch independent of telemetry persistence speed.
- Observable latency and reliability trends via SQLite rolling metrics.
