package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration structure for GengoWatcher.
// It contains all settings for the watcher, monitors, notifications, and state.
type Config struct {
	Watcher       WatcherConfig       `mapstructure:"watcher"`
	WebSocket     WebSocketConfig     `mapstructure:"websocket"`
	RSS           RSSConfig           `mapstructure:"rss"`
	Email         EmailConfig         `mapstructure:"email"`
	Website       WebsiteConfig       `mapstructure:"website"`
	Notifications NotificationsConfig `mapstructure:"notifications"`
	State         StateConfig         `mapstructure:"state"`
}

// WatcherConfig contains general watcher settings.
type WatcherConfig struct {
	// CheckInterval is the polling interval for RSS and Website monitors.
	CheckInterval time.Duration `mapstructure:"check_interval"`
	// MinReward is the minimum reward to trigger notifications.
	MinReward float64 `mapstructure:"min_reward"`
}

// WebSocketConfig contains WebSocket monitor settings.
type WebSocketConfig struct {
	// Enabled determines if the WebSocket monitor runs.
	Enabled bool `mapstructure:"enabled"`
	// URL is the WebSocket endpoint URL.
	URL string `mapstructure:"url"`
	// UserID is the Gengo user ID cookie value (from env var).
	UserID string `mapstructure:"-"`
	// UserSession is the Gengo session cookie value (from env var).
	UserSession string `mapstructure:"-"`
	// UserKey is the Gengo user key from local storage (from env var).
	UserKey string `mapstructure:"-"`
}

// RSSConfig contains RSS feed monitor settings.
type RSSConfig struct {
	// Enabled determines if the RSS monitor runs.
	Enabled bool `mapstructure:"enabled"`
	// URL is the RSS feed URL.
	URL string `mapstructure:"url"`
	// PauseFile pauses RSS polling while this file exists.
	PauseFile string `mapstructure:"pause_file"`
	// PauseSleep is the sleep duration between pause-file checks.
	PauseSleep time.Duration `mapstructure:"pause_sleep"`
	// MaxBackoff is the maximum exponential backoff after RSS failures.
	MaxBackoff time.Duration `mapstructure:"max_backoff"`
}

// EmailConfig contains email monitor settings.
type EmailConfig struct {
	// Enabled determines if the email monitor runs.
	Enabled bool `mapstructure:"enabled"`
	// CredentialsPath is the path to OAuth2 credentials JSON file.
	CredentialsPath string `mapstructure:"credentials_path"`
	// TokenPath is the path to the stored OAuth2 token.
	TokenPath string `mapstructure:"token_path"`
}

// WebsiteConfig contains website scraping monitor settings.
type WebsiteConfig struct {
	// Enabled determines if the website monitor runs.
	Enabled bool `mapstructure:"enabled"`
	// URL is the Gengo jobs page URL.
	URL string `mapstructure:"url"`
	// Headless determines if the browser runs in headless mode.
	Headless bool `mapstructure:"headless"`
}

// NotificationsConfig contains notification settings.
type NotificationsConfig struct {
	// Desktop enables desktop notifications.
	Desktop bool `mapstructure:"desktop"`
	// Sound enables sound alerts.
	Sound bool `mapstructure:"sound"`
	// SoundFile is the path to the notification sound file.
	SoundFile string `mapstructure:"sound_file"`
	// OpenBrowser automatically opens job URLs in browser.
	OpenBrowser bool `mapstructure:"open_browser"`
}

// StateConfig contains state persistence settings.
type StateConfig struct {
	// File is the path to the state JSON file.
	File string `mapstructure:"file"`
}

// Load reads configuration from file and environment variables.
// If configFile is empty, it searches for config.toml in standard locations.
func Load(configFile string) (*Config, error) {
	v := viper.New()

	// Set default values
	setDefaults(v)

	// Configure file reading
	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("toml")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.config/gengowatcher")
		v.AddConfigPath("/etc/gengowatcher")
	}

	// Enable environment variable override
	v.SetEnvPrefix("GENGO")
	v.AutomaticEnv()

	// Map specific env vars
	_ = v.BindEnv("websocket.user_id", "GENGO_USER_ID")
	_ = v.BindEnv("websocket.user_session", "GENGO_USER_SESSION")
	_ = v.BindEnv("websocket.user_key", "GENGO_USER_KEY")
	_ = v.BindEnv("watcher.check_interval", "GENGO_CHECK_INTERVAL")
	_ = v.BindEnv("watcher.min_reward", "GENGO_MIN_REWARD")
	_ = v.BindEnv("state.file", "GENGO_STATE_FILE")
	_ = v.BindEnv("rss.pause_file", "GENGO_RSS_PAUSE_FILE")
	_ = v.BindEnv("rss.pause_sleep", "GENGO_RSS_PAUSE_SLEEP")
	_ = v.BindEnv("rss.max_backoff", "GENGO_RSS_MAX_BACKOFF")

	// Read config file (ignore if not found, use defaults)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found, will use defaults
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Load sensitive values from environment (not in config file)
	cfg.WebSocket.UserID = os.Getenv("GENGO_USER_ID")
	cfg.WebSocket.UserSession = os.Getenv("GENGO_USER_SESSION")
	cfg.WebSocket.UserKey = os.Getenv("GENGO_USER_KEY")

	// Apply defaults for zero values
	applyDefaults(&cfg)

	return &cfg, nil
}

// setDefaults configures Viper with default values.
func setDefaults(v *viper.Viper) {
	v.SetDefault("watcher.check_interval", DefaultCheckInterval)
	v.SetDefault("watcher.min_reward", DefaultMinReward)

	v.SetDefault("websocket.enabled", DefaultEnabled.WebSocket)
	v.SetDefault("websocket.url", DefaultWebSocketURL)

	v.SetDefault("rss.enabled", DefaultEnabled.RSS)
	v.SetDefault("rss.url", DefaultRSSURL)
	v.SetDefault("rss.pause_file", DefaultRSSPauseFile)
	v.SetDefault("rss.pause_sleep", DefaultRSSPauseSleep)
	v.SetDefault("rss.max_backoff", DefaultRSSMaxBackoff)

	v.SetDefault("email.enabled", DefaultEnabled.Email)

	v.SetDefault("website.enabled", DefaultEnabled.Website)

	v.SetDefault("notifications.desktop", DefaultDesktopNotify)
	v.SetDefault("notifications.sound", DefaultSoundNotify)
	v.SetDefault("notifications.sound_file", DefaultSoundFile)
	v.SetDefault("notifications.open_browser", DefaultOpenBrowser)

	v.SetDefault("state.file", DefaultStateFile)
}

// applyDefaults ensures required fields have values.
func applyDefaults(cfg *Config) {
	if cfg.Watcher.CheckInterval == 0 {
		cfg.Watcher.CheckInterval = DefaultCheckInterval
	}
	if cfg.Watcher.MinReward == 0 {
		cfg.Watcher.MinReward = DefaultMinReward
	}
	if cfg.WebSocket.URL == "" {
		cfg.WebSocket.URL = DefaultWebSocketURL
	}
	if cfg.RSS.URL == "" {
		cfg.RSS.URL = DefaultRSSURL
	}
	if cfg.RSS.PauseSleep <= 0 {
		cfg.RSS.PauseSleep = DefaultRSSPauseSleep
	}
	if cfg.RSS.MaxBackoff <= 0 {
		cfg.RSS.MaxBackoff = DefaultRSSMaxBackoff
	}
	if cfg.State.File == "" {
		cfg.State.File = DefaultStateFile
	}
	if cfg.Notifications.SoundFile == "" {
		cfg.Notifications.SoundFile = DefaultSoundFile
	}
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if c.WebSocket.Enabled {
		if c.WebSocket.UserID == "" {
			return fmt.Errorf("websocket enabled but GENGO_USER_ID not set")
		}
		if c.WebSocket.UserSession == "" {
			return fmt.Errorf("websocket enabled but GENGO_USER_SESSION not set")
		}
	}

	if c.Email.Enabled {
		if c.Email.CredentialsPath == "" {
			return fmt.Errorf("email enabled but credentials_path not set")
		}
		if c.Email.TokenPath == "" {
			return fmt.Errorf("email enabled but token_path not set")
		}
	}

	if c.RSS.PauseSleep < 0 {
		return fmt.Errorf("rss.pause_sleep must be >= 0")
	}
	if c.RSS.MaxBackoff <= 0 {
		return fmt.Errorf("rss.max_backoff must be > 0")
	}

	return nil
}

// HasEnabledMonitors returns true if at least one monitor is enabled.
func (c *Config) HasEnabledMonitors() bool {
	return c.WebSocket.Enabled || c.RSS.Enabled || c.Email.Enabled || c.Website.Enabled
}
