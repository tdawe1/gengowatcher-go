# GengoWatcher Go Rewrite Design

**Date:** 2026-02-19
**Author:** Design brainstorming session
**Status:** Approved for implementation

## Overview

Rewrite GengoWatcher from Python to Go for integration as a submodule of `translation-app`. The goal is full feature parity with the Python version while providing a clean public API for external consumption.

**Scope:**

- All four job sources: WebSocket, RSS, Email, Website scraping
- Bubbletea TUI with real-time updates
- Desktop notifications with sound and browser opening
- TOML configuration with environment variable override
- JSON state persistence with file locking
- No auto-acceptance or CAPTCHA solving (deferred)

**Out of scope:**

- Gengo API integration (read-only monitoring tool)
- Auto-acceptance with CAPTCHA solving
- Web API server (CLI-focused)

---

## Module Structure

```
gengowatcher-go/
├── go.mod                          # Module: github.com/tdawe1/gengowatcher-go
├── cmd/
│   └── gengowatcher/
│       └── main.go                 # CLI entry point (cobra commands)
├── internal/
│   ├── app/
│   │   └── watcher.go              # GengoWatcher - central hub
│   ├── config/
│   │   ├── config.go               # TOML config + env override
│   │   └── defaults.go             # Default values
│   ├── state/
│   │   ├── state.go                # JSON persistence + file locking
│   │   └── models.go               # Job, Stats, History types
│   ├── ui/
│   │   ├── tui.go                  # Bubbletea program entry
│   │   ├── components/
│   │   │   ├── jobs_table.go       # Job listings DataTable
│   │   │   ├── stats_panel.go      # Stats dashboard
│   │   │   ├── command_input.go    # Command input with history
│   │   │   ├── log_viewer.go       # Filterable log display
│   │   │   └── layout.go           # Main layout container
│   │   └── styles.go               # Lipglass styling constants
│   ├── monitor/
│   │   ├── monitor.go              # Monitor interface
│   │   ├── websocket.go            # Gengo live dashboard WebSocket
│   │   ├── rss.go                  # RSS feed polling
│   │   ├── email.go                # Gmail OAuth monitoring
│   │   └── website.go              # Rod browser scraping
│   ├── notify/
│   │   ├── desktop.go              # Desktop notifications
│   │   └── sound.go                # Sound alert playback
│   └── api/
│       └── server.go               # Optional: standalone web API
├── pkg/
│   └── gengo/                      # Public API for submodule consumers
│       ├── client.go               # Gengo API client
│       ├── models.go               # Shared job/event types
│       └── watcher.go              # Watcher interface for embedding
└── tests/
    ├── integration/
    └── fixtures/
```

**Key points:**

- `cmd/` - CLI entry point for standalone binary
- `pkg/gengo/` - Public API for submodule integration
- `internal/` - CLI-only concerns (TUI, config files, state)

---

## Core Types

### Shared Types (`pkg/gengo/models.go`)

```go
type Job struct {
    ID          string            `json:"id"`
    Title       string            `json:"title"`
    Source      string            `json:"source"` // "websocket", "rss", "email", "website"
    Reward      float64           `json:"reward"`
    Currency    string            `json:"currency"`
    Language    string            `json:"language"` // "en → ja"
    UnitCount   int               `json:"unit_count"`
    Deadline    time.Time         `json:"deadline"`
    FoundAt     time.Time         `json:"found_at"`
    Payload     json.RawMessage   `json:"payload"` // Raw source data
}

type JobEvent struct {
    Type    EventType   `json:"type"`
    Job     *Job        `json:"job,omitempty"`
    Source  string      `json:"source"`
    Error   string      `json:"error,omitempty"`
    Time    time.Time   `json:"time"`
}

type EventType string

const (
    EventJobFound   EventType = "job_found"
    EventJobExpired EventType = "job_expired"
    EventError      EventType = "error"
)
```

### Monitor Interface (`pkg/gengo/monitor.go`)

```go
type Monitor interface {
    // Start begins monitoring. Jobs are sent to the events channel.
    Start(ctx context.Context, events chan<- JobEvent) error
    // Name returns the monitor identifier for logging
    Name() string
    // Enabled returns whether this monitor is configured and should run
    Enabled() bool
}
```

---

## Monitors

### WebSocket Monitor

- Connects to `wss://live-dashboard.gengo.com`
- Authenticates with cookies (user_id, session) + user_key via local storage
- Listens for job_published events
- Handles reconnect with exponential backoff

### RSS Monitor

- Polls RSS feed at configured interval
- Uses feedparser library
- Deduplicates via LRU cache (seen job IDs)
- Filters by minimum reward

### Email Monitor

- Gmail API with OAuth2
- Refresh token from config/env
- Watches for Gengo job notification emails
- Parses job links from email body

### Website Monitor

- Uses `rod` (github.com/go-rod/rod) for browser automation
- Headless Chrome/Chromium
- Polls Gengo jobs page at interval
- Scrapes job listings

---

## TUI Architecture (Bubbletea)

```go
type Model struct {
    // State
    jobs        []*Job
    stats       *Stats
    logs        []LogEntry
    commandHist []string

    // UI State
    currentTab  Tab // Jobs, Stats, Logs
    tableCursor int
    inputBuffer string

    // Components
    jobsTable   jobsTableModel
    statsPanel  statsPanelModel
    logViewer   logViewerModel
    commandInput commandInputModel

    // Channels (bridged to Bubbletea via tea.Cmd)
    jobEvents   chan JobEvent
    logMsgs     chan LogEntry

    // Dependencies
    watcher     *Watcher
    config      *Config
}
```

**Three tabs:**

1. Jobs table - DataTable with recent jobs
2. Stats dashboard - Metrics and charts
3. Log viewer - Filterable log display

**Command input** at bottom with history (supports commands like `check`, `pause`, `resume`)

**Status bar** showing monitor status and pause/resume state

---

## Configuration

TOML-based with environment variable override.

**Example `config.toml`:**

```toml
[watcher]
check_interval = "60s"
min_reward = 5.0

[websocket]
enabled = true
url = "wss://live-dashboard.gengo.com"
# user_id, session, user_key from env vars

[rss]
enabled = true
url = "https://gengo.com/t/jobs.rss"

[email]
enabled = false

[website]
enabled = false

[notifications]
desktop = true
sound = true
sound_file = "assets/notification.wav"
open_browser = true

[state]
file = "state.json"
```

**Environment variables:**

- `GENGO_USER_ID` - WebSocket user ID cookie
- `GENGO_USER_SESSION` - WebSocket session cookie
- `GENGO_USER_KEY` - WebSocket user key from local storage
- `GENGO_CHECK_INTERVAL` - Override check interval
- `GENGO_MIN_REWARD` - Override minimum reward filter

Sensitive fields marked `toml:"-"` read from environment only.

---

## State Persistence

JSON-based with file locking for concurrent safety.

**State schema:**

```json
{
  "version": 1,
  "jobs": [
    {
      "id": "12345",
      "title": "Translation Job",
      "source": "websocket",
      "reward": 8.5,
      "found_at": "2026-02-19T10:30:00Z"
    }
  ],
  "stats": {
    "total_found": 142,
    "session_found": 12,
    "by_source": { "websocket": 138, "rss": 4 }
  },
  "last_updated": "2026-02-19T10:30:00Z"
}
```

- File locking via `flock()` ensures safe concurrent access
- Atomic writes (temp file + rename) prevent corruption
- Thread-safe operations with `sync.RWMutex`

---

## Notifications

```go
type Notifier struct {
    desktopEnabled bool
    soundEnabled   bool
    soundFile      string
    browserCmd     string // e.g., "xdg-open", "open"
}
```

On job discovery:

1. Desktop notification (libnotify)
2. Sound playback
3. Open job URL in browser

User manually accepts job in opened browser - no API interaction.

---

## CLI (Cobra)

```bash
gengowatcher                    # Launch TUI
gengowatcher -c custom.toml     # Custom config
gengowatcher configure          # Interactive setup
gengowatcher check              # One-shot check, JSON output
gengowatcher --version
```

**Commands:**

- Default: Launch TUI
- `configure` - Interactive configuration setup
- `check` - One-shot RSS check, outputs JSON to stdout

---

## Submodule Integration

### In `translation-app/go.mod`:

```go
module github.com/tdawe1/translation-app

require (
    github.com/tdawe1/gengowatcher-go v0.0.0
)

replace github.com/tdawe1/gengowatcher-go => ./gengowatcher-go
```

### Usage in translation-app:

```go
import "github.com/tdawe1/gengowatcher-go/pkg/gengo"

wsMonitor := websocket.New(config.WebSocket)
rssMonitor := rss.New(config.RSS)

events := make(chan gengo.JobEvent, 100)
go wsMonitor.Start(ctx, events)
go rssMonitor.Start(ctx, events)

for event := range events {
    // Handle job event - publish to Redis, notify users, etc.
}
```

**Integration points:**

- `pkg/gengo` provides public API
- `Monitor` interface for extensibility
- `Job` and `JobEvent` shared types
- No CLI concerns leak to consumers

---

## Dependencies

| Component      | Library                              |
| -------------- | ------------------------------------ |
| CLI            | `github.com/spf13/cobra`             |
| TUI            | `github.com/charmbracelet/bubbletea` |
| Styling        | `github.com/charmbracelet/lipglass`  |
| Config         | `github.com/spf13/viper`             |
| RSS            | `github.com/mmcdole/gofeed`          |
| Email          | `github.com/google/gmail-go`         |
| Browser        | `github.com/go-rod/rod`              |
| Desktop notify | `github.com/gen2brain/beeep`         |

---

## Implementation Phases

1. **Phase 1 (Completed):** Core types, monitor interface, config system
2. **Phase 2 (In Progress):** WebSocket + RSS monitors (RSS/WS parity patch robustness is complete)
3. **Phase 3:** TUI with jobs table and basic stats
4. **Phase 4:** State persistence, notifications
5. **Phase 5:** Email + Website monitors
6. **Phase 6:** CLI commands, polish
7. **Phase 7:** Submodule integration with translation-app
