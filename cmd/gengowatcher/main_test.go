package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tdawe1/gengowatcher-go/internal/app"
	"github.com/tdawe1/gengowatcher-go/internal/config"
)

func TestRun_ReturnsConfigErrorForInvalidFile(t *testing.T) {
	err := run([]string{"-c", "does-not-exist.toml"})
	if err == nil {
		t.Fatal("expected config load error")
	}
}

func TestRun_ReturnsErrNoEnabledMonitorsWhenAllDisabled(t *testing.T) {
	configFile := writeConfigFile(t, "all-disabled.toml", `[websocket]
enabled = false

[rss]
enabled = false

[email]
enabled = false

[website]
enabled = false
`)

	err := run([]string{"-c", configFile})
	if !errors.Is(err, app.ErrNoEnabledMonitors) {
		t.Fatalf("expected ErrNoEnabledMonitors, got %v", err)
	}
}

func TestRun_PropagatesUIRunError(t *testing.T) {
	configFile := writeConfigFile(t, "rss-enabled.toml", `[watcher]
check_interval = "31s"

[websocket]
enabled = false

[rss]
enabled = true
url = "https://example.com/rss"
pause_sleep = "1s"
max_backoff = "1m"

[email]
enabled = false

[website]
enabled = false
`)

	expectedErr := errors.New("ui run failed")
	originalRunUI := runUI
	runUI = func(_ context.Context, _ *config.Config) error {
		return expectedErr
	}
	t.Cleanup(func() {
		runUI = originalRunUI
	})

	err := run([]string{"-c", configFile})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected propagated ui error, got %v", err)
	}
}

func writeConfigFile(t *testing.T, name string, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}
