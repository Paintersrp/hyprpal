package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
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
	cfgFullPath, err := filepath.Abs(*cfgPath)
	if err != nil {
		exitErr(fmt.Errorf("resolve config path: %w", err))
	}
	cfgFullPath = filepath.Clean(cfgFullPath)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		exitErr(fmt.Errorf("watch config: %w", err))
	}
	defer watcher.Close()
	cfgDir := filepath.Dir(cfgFullPath)
	if err := watcher.Add(cfgDir); err != nil {
		exitErr(fmt.Errorf("watch config dir: %w", err))
	}
	if err := watcher.Add(cfgFullPath); err != nil {
		logger.Debugf("unable to watch config file directly: %v", err)
	}
	reloadRequests := make(chan string, 1)
	go watchConfig(logger, watcher, cfgFullPath, reloadRequests)

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
	reload := func(reason string) {
		logger.Infof("%s, reloading config", reason)
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			logger.Errorf("reload failed: %v", err)
			return
		}
		modes, err := rules.BuildModes(cfg)
		if err != nil {
			logger.Errorf("compile failed: %v", err)
			return
		}
		eng.ReloadModes(modes)
		if err := eng.Reconcile(ctx); err != nil {
			logger.Errorf("reconcile after reload failed: %v", err)
		}
	}

	for {
		select {
		case err := <-errs:
			if err != nil && err != context.Canceled {
				logger.Errorf("engine exited: %v", err)
				os.Exit(1)
			}
			logger.Infof("engine stopped")
			return
		case reason := <-reloadRequests:
			reload(reason)
		case sig := <-sigs:
			switch sig {
			case syscall.SIGHUP:
				reload("received SIGHUP")
			case os.Interrupt, syscall.SIGTERM:
				logger.Infof("received %s, shutting down", sig)
				cancel()
			}
		}
	}
}

func watchConfig(logger *util.Logger, watcher *fsnotify.Watcher, target string, reloadRequests chan<- string) {
	const debounceWindow = 250 * time.Millisecond
	var (
		timer   *time.Timer
		timerCh <-chan time.Time
	)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != target {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(debounceWindow)
				timerCh = timer.C
			} else {
				if !timer.Stop() {
					<-timerCh
				}
				timer.Reset(debounceWindow)
			}
		case <-timerCh:
			timer = nil
			timerCh = nil
			select {
			case reloadRequests <- "config file updated":
			default:
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.Warnf("config watcher error: %v", err)
		}
	}
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
