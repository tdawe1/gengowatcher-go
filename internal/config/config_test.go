package config

import "testing"

func TestValidate_WebSocketUserKeyOptional(t *testing.T) {
	cfg := &Config{
		RSS: RSSConfig{
			PauseSleep: 1,
			MaxBackoff: 1,
		},
		WebSocket: WebSocketConfig{
			Enabled:     true,
			UserID:      "user-id",
			UserSession: "session",
			UserKey:     "",
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config without user key, got %v", err)
	}
}

func TestValidate_WebSocketRequiresIDAndSession(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "missing user id",
			cfg: Config{
				RSS:       RSSConfig{PauseSleep: 1, MaxBackoff: 1},
				WebSocket: WebSocketConfig{Enabled: true, UserSession: "session"},
			},
		},
		{
			name: "missing user session",
			cfg: Config{
				RSS:       RSSConfig{PauseSleep: 1, MaxBackoff: 1},
				WebSocket: WebSocketConfig{Enabled: true, UserID: "user-id"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
