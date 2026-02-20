package config

import "time"

// Default configuration values for GengoWatcher.
// These are used when values are not specified in config file or env vars.

const (
	// Watcher defaults
	DefaultCheckInterval = 60 * time.Second
	DefaultMinReward     = 5.0

	// WebSocket defaults
	DefaultWebSocketEnabled = false
	DefaultWebSocketURL     = "wss://live-dashboard.gengo.com"

	// RSS defaults
	DefaultRSSEnabled = true
	DefaultRSSURL     = "https://gengo.com/t/jobs.rss"

	// Email defaults
	DefaultEmailEnabled = false

	// Website defaults
	DefaultWebsiteEnabled = false

	// Notification defaults
	DefaultDesktopNotify = true
	DefaultSoundNotify   = true
	DefaultOpenBrowser   = true
	DefaultSoundFile     = "assets/notification.wav"

	// State defaults
	DefaultStateFile = "state.json"
)

// DefaultEnabled indicates which monitors are enabled by default.
var DefaultEnabled = struct {
	WebSocket bool
	RSS       bool
	Email     bool
	Website   bool
}{
	WebSocket: DefaultWebSocketEnabled,
	RSS:       DefaultRSSEnabled,
	Email:     DefaultEmailEnabled,
	Website:   DefaultWebsiteEnabled,
}

