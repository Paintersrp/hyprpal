package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hyprpal/hypr-smartd/internal/config"
	"github.com/hyprpal/hypr-smartd/internal/engine"
	"github.com/hyprpal/hypr-smartd/internal/ipc"
	"github.com/hyprpal/hypr-smartd/internal/rules"
	"github.com/hyprpal/hypr-smartd/internal/util"
)

func main() {
	home, _ := os.UserHomeDir()
	defaultConfig := filepath.Join(home, ".config", "hypr-smartd", "config.yaml")

	cfgPath := flag.String("config", defaultConfig, "path to YAML config")
	dryRun := flag.Bool("dry-run", false, "do not dispatch commands")
	logLevel := flag.String("log-level", "info", "log level (debug|info|warn|error)")
	startMode := flag.String("mode", "", "initial mode to activate")
	flag.Parse()

	logger := util.NewLogger(util.ParseLogLevel(*logLevel))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		exitErr(fmt.Errorf("load config: %w", err))
	}
	modes, err := rules.BuildModes(cfg)
	if err != nil {
		exitErr(fmt.Errorf("compile rules: %w", err))
	}

	hypr := ipc.NewClient()
	eng := engine.New(hypr, logger, modes, *dryRun)
	if *startMode != "" {
		if err := eng.SetMode(*startMode); err != nil {
			logger.Warnf("failed to set mode %s: %v", *startMode, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	errs := make(chan error, 1)
	go func() {
		errs <- eng.Run(ctx)
	}()

	for {
		select {
		case err := <-errs:
			if err != nil && err != context.Canceled {
				logger.Errorf("engine exited: %v", err)
				os.Exit(1)
			}
			logger.Infof("engine stopped")
			return
		case sig := <-sigs:
			switch sig {
			case syscall.SIGHUP:
				logger.Infof("received SIGHUP, reloading config")
				cfg, err := config.Load(*cfgPath)
				if err != nil {
					logger.Errorf("reload failed: %v", err)
					continue
				}
				modes, err := rules.BuildModes(cfg)
				if err != nil {
					logger.Errorf("compile failed: %v", err)
					continue
				}
				eng.ReloadModes(modes)
				if err := eng.Reconcile(ctx); err != nil {
					logger.Errorf("reconcile after reload failed: %v", err)
				}
			case os.Interrupt, syscall.SIGTERM:
				logger.Infof("received %s, shutting down", sig)
				cancel()
			}
		}
	}
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
