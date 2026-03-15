package notify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/tdawe1/gengowatcher-go/internal/config"
)

var (
	ErrNoBrowserCommand       = errors.New("no browser open command available")
	ErrNoDesktopNotifyCommand = errors.New("no desktop notification command available")
	ErrNoSoundPlaybackCommand = errors.New("no sound playback command available")
)

type commandSpec struct {
	name string
	args []string
}

type commandRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
}

type osCommandRunner struct{}

func (osCommandRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (osCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

// Service exposes browser-open and desktop-notify helpers that map directly
// onto the existing reaction executor's Open/Notify callbacks.
type Service struct {
	cfg         config.NotificationsConfig
	runner      commandRunner
	browserCmds []commandSpec
	desktopCmds []commandSpec
	soundCmds   []commandSpec
}

func New(cfg config.NotificationsConfig) *Service {
	return &Service{
		cfg:         cfg,
		runner:      osCommandRunner{},
		browserCmds: browserCommands(runtime.GOOS),
		desktopCmds: desktopCommands(runtime.GOOS),
		soundCmds:   soundCommands(runtime.GOOS),
	}
}

func (s *Service) Open(ctx context.Context, url string) error {
	if s == nil || !s.cfg.OpenBrowser {
		return nil
	}

	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}

	spec, err := s.resolveCommand(s.browserCmds, ErrNoBrowserCommand)
	if err != nil {
		return err
	}

	args := append(append([]string{}, spec.args...), url)
	return s.runner.Run(s.contextOrBackground(ctx), spec.name, args...)
}

func (s *Service) Notify(ctx context.Context, title string) error {
	if s == nil {
		return nil
	}

	ctx = s.contextOrBackground(ctx)
	title = strings.TrimSpace(title)

	var errs []error
	if s.cfg.Desktop && title != "" {
		if err := s.runDesktopNotify(ctx, title); err != nil {
			errs = append(errs, err)
		}
	}

	if s.cfg.Sound && strings.TrimSpace(s.cfg.SoundFile) != "" {
		if err := s.runSound(ctx, s.cfg.SoundFile); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (s *Service) runDesktopNotify(ctx context.Context, title string) error {
	spec, err := s.resolveCommand(s.desktopCmds, ErrNoDesktopNotifyCommand)
	if err != nil {
		return err
	}

	args := desktopArgs(spec, title)
	if err := s.runner.Run(ctx, spec.name, args...); err != nil {
		return fmt.Errorf("desktop notify: %w", err)
	}

	return nil
}

func (s *Service) runSound(ctx context.Context, soundFile string) error {
	spec, err := s.resolveCommand(s.soundCmds, ErrNoSoundPlaybackCommand)
	if err != nil {
		return err
	}

	args := soundArgs(spec, soundFile)
	if err := s.runner.Run(ctx, spec.name, args...); err != nil {
		return fmt.Errorf("sound playback: %w", err)
	}

	return nil
}

func (s *Service) resolveCommand(candidates []commandSpec, missingErr error) (commandSpec, error) {
	if s == nil || s.runner == nil {
		return commandSpec{}, errors.New("notify service is not configured")
	}

	for _, candidate := range candidates {
		if candidate.name == "" {
			continue
		}
		if _, err := s.runner.LookPath(candidate.name); err == nil {
			return candidate, nil
		}
	}

	if missingErr != nil {
		return commandSpec{}, missingErr
	}

	return commandSpec{}, errors.New("no commands configured")
}

func (s *Service) contextOrBackground(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}

	return context.Background()
}

func browserCommands(goos string) []commandSpec {
	switch goos {
	case "darwin":
		return []commandSpec{{name: "open"}}
	case "windows":
		return []commandSpec{{name: "rundll32", args: []string{"url.dll,FileProtocolHandler"}}}
	default:
		return []commandSpec{{name: "xdg-open"}}
	}
}

func desktopCommands(goos string) []commandSpec {
	switch goos {
	case "darwin":
		return []commandSpec{{name: "osascript"}}
	case "windows":
		return []commandSpec{{name: "powershell"}}
	default:
		return []commandSpec{{name: "notify-send", args: []string{"GengoWatcher"}}}
	}
}

func soundCommands(goos string) []commandSpec {
	switch goos {
	case "darwin":
		return []commandSpec{{name: "afplay"}}
	case "windows":
		return []commandSpec{{name: "powershell"}}
	default:
		return []commandSpec{
			{name: "paplay"},
			{name: "aplay"},
			{name: "ffplay", args: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
		}
	}
}

func desktopArgs(spec commandSpec, title string) []string {
	switch spec.name {
	case "osascript":
		return []string{"-e", fmt.Sprintf(`display notification "" with title %q`, title)}
	case "powershell":
		return []string{
			"-NoProfile",
			"-Command",
			fmt.Sprintf(`[void][Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');[System.Windows.Forms.MessageBox]::Show(%q,'GengoWatcher')`, title),
		}
	default:
		return append(append([]string{}, spec.args...), title)
	}
}

func soundArgs(spec commandSpec, soundFile string) []string {
	switch spec.name {
	case "powershell":
		return []string{
			"-NoProfile",
			"-Command",
			fmt.Sprintf(`$p = New-Object Media.SoundPlayer %q; $p.PlaySync()`, soundFile),
		}
	default:
		return append(append([]string{}, spec.args...), soundFile)
	}
}
