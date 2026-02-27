package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tdawe1/gengowatcher-go/internal/app"
	"github.com/tdawe1/gengowatcher-go/internal/config"
	"github.com/tdawe1/gengowatcher-go/internal/ui"
)

var runUI = ui.Run

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gengowatcher", flag.ContinueOnError)
	configFile := fs.String("c", "", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		return err
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	if !cfg.HasEnabledMonitors() {
		return app.ErrNoEnabledMonitors
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return runUI(ctx, cfg)
}
