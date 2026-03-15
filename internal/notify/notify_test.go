package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tdawe1/gengowatcher-go/internal/config"
)

type runCall struct {
	name string
	args []string
}

type stubRunner struct {
	available map[string]bool
	runErrs   map[string]error
	calls     []runCall
}

func (r *stubRunner) LookPath(name string) (string, error) {
	if r.available[name] {
		return "/usr/bin/" + name, nil
	}

	return "", errors.New("missing")
}

func (r *stubRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, runCall{name: name, args: append([]string{}, args...)})
	if err := r.runErrs[name]; err != nil {
		return err
	}

	return nil
}

func TestService_OpenUsesFirstAvailableBrowserCommand(t *testing.T) {
	runner := &stubRunner{
		available: map[string]bool{"xdg-open": true},
	}
	svc := New(config.NotificationsConfig{OpenBrowser: true})
	svc.runner = runner
	svc.browserCmds = []commandSpec{
		{name: "missing-open"},
		{name: "xdg-open"},
	}

	err := svc.Open(context.Background(), "https://gengo.com/jobs/42")
	if err != nil {
		t.Fatalf("open returned error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected one command run, got %d", len(runner.calls))
	}
	if runner.calls[0].name != "xdg-open" {
		t.Fatalf("expected xdg-open, got %q", runner.calls[0].name)
	}
	if got := runner.calls[0].args[len(runner.calls[0].args)-1]; got != "https://gengo.com/jobs/42" {
		t.Fatalf("expected url arg, got %q", got)
	}
}

func TestService_OpenDisabledDoesNothing(t *testing.T) {
	runner := &stubRunner{available: map[string]bool{"xdg-open": true}}
	svc := New(config.NotificationsConfig{OpenBrowser: false})
	svc.runner = runner

	if err := svc.Open(context.Background(), "https://gengo.com/jobs/42"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no command runs, got %d", len(runner.calls))
	}
}

func TestService_OpenReturnsErrNoBrowserCommand(t *testing.T) {
	svc := New(config.NotificationsConfig{OpenBrowser: true})
	svc.runner = &stubRunner{}
	svc.browserCmds = []commandSpec{{name: "missing-open"}}

	err := svc.Open(context.Background(), "https://gengo.com/jobs/42")
	if !errors.Is(err, ErrNoBrowserCommand) {
		t.Fatalf("expected ErrNoBrowserCommand, got %v", err)
	}
}

func TestService_NotifyRunsDesktopAndSound(t *testing.T) {
	runner := &stubRunner{
		available: map[string]bool{
			"notify-send": true,
			"paplay":      true,
		},
	}
	svc := New(config.NotificationsConfig{
		Desktop:   true,
		Sound:     true,
		SoundFile: "assets/notification.wav",
	})
	svc.runner = runner
	svc.desktopCmds = []commandSpec{{name: "notify-send", args: []string{"GengoWatcher"}}}
	svc.soundCmds = []commandSpec{{name: "paplay"}}

	err := svc.Notify(context.Background(), "JP -> EN")
	if err != nil {
		t.Fatalf("notify returned error: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected two command runs, got %d", len(runner.calls))
	}
	if runner.calls[0].name != "notify-send" {
		t.Fatalf("expected notify-send first, got %q", runner.calls[0].name)
	}
	if got := runner.calls[0].args[len(runner.calls[0].args)-1]; got != "JP -> EN" {
		t.Fatalf("expected title arg, got %q", got)
	}
	if runner.calls[1].name != "paplay" {
		t.Fatalf("expected paplay second, got %q", runner.calls[1].name)
	}
	if got := runner.calls[1].args[len(runner.calls[1].args)-1]; got != "assets/notification.wav" {
		t.Fatalf("expected sound file arg, got %q", got)
	}
}

func TestService_NotifyAggregatesDesktopAndSoundErrors(t *testing.T) {
	runner := &stubRunner{
		available: map[string]bool{
			"notify-send": true,
			"paplay":      true,
		},
		runErrs: map[string]error{
			"notify-send": errors.New("notify failed"),
			"paplay":      errors.New("sound failed"),
		},
	}
	svc := New(config.NotificationsConfig{
		Desktop:   true,
		Sound:     true,
		SoundFile: "assets/notification.wav",
	})
	svc.runner = runner
	svc.desktopCmds = []commandSpec{{name: "notify-send", args: []string{"GengoWatcher"}}}
	svc.soundCmds = []commandSpec{{name: "paplay"}}

	err := svc.Notify(context.Background(), "JP -> EN")
	if err == nil {
		t.Fatal("expected joined error")
	}
	if !strings.Contains(err.Error(), "desktop notify: notify failed") {
		t.Fatalf("expected desktop error in %q", err.Error())
	}
	if !strings.Contains(err.Error(), "sound playback: sound failed") {
		t.Fatalf("expected sound error in %q", err.Error())
	}
}

func TestService_NotifySkipsDisabledAndEmptyInputs(t *testing.T) {
	runner := &stubRunner{
		available: map[string]bool{
			"notify-send": true,
			"paplay":      true,
		},
	}
	svc := New(config.NotificationsConfig{
		Desktop:   true,
		Sound:     true,
		SoundFile: "",
	})
	svc.runner = runner
	svc.desktopCmds = []commandSpec{{name: "notify-send", args: []string{"GengoWatcher"}}}
	svc.soundCmds = []commandSpec{{name: "paplay"}}

	if err := svc.Notify(context.Background(), "   "); err != nil {
		t.Fatalf("expected nil error for empty title/no sound file, got %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no commands, got %d", len(runner.calls))
	}
}
