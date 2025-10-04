package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/control"
	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/ipc"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/util"
)

func main() {
	home, _ := os.UserHomeDir()
	defaultConfig := filepath.Join(home, ".config", "hyprpal", "config.yaml")

	cfgPath := flag.String("config", defaultConfig, "path to YAML config")
	dryRun := flag.Bool("dry-run", false, "do not dispatch commands")
	logLevel := flag.String("log-level", "info", "log level (trace|debug|info|warn|error)")
	startMode := flag.String("mode", "", "initial mode to activate")
	dispatchStrategy := flag.String("dispatch", string(ipc.DispatchStrategySocket), "dispatch strategy (socket|hyprctl)")
	flag.Parse()

	selectedStrategy := ipc.DispatchStrategy(strings.ToLower(*dispatchStrategy))
	switch selectedStrategy {
	case ipc.DispatchStrategySocket, ipc.DispatchStrategyHyprctl:
	default:
		exitErr(fmt.Errorf("unsupported dispatch strategy %q", *dispatchStrategy))
	}

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hypr, strategy, err := ipc.NewEngineClient(logger, selectedStrategy)
	if err != nil {
		exitErr(fmt.Errorf("configure dispatch strategy: %w", err))
	}
	logger.Infof("using %s dispatch strategy", strategy)
	eng := engine.New(hypr, logger, modes, *dryRun, cfg.RedactTitles, layout.Gaps{
		Inner: cfg.Gaps.Inner,
		Outer: cfg.Gaps.Outer,
	}, cfg.PlacementTolerancePx)
	if *startMode != "" {
		if err := eng.SetMode(*startMode); err != nil {
			logger.Warnf("failed to set mode %s: %v", *startMode, err)
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	reload := func(reason string) error {
		logger.Infof("%s, reloading config", reason)
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		modes, err := rules.BuildModes(cfg)
		if err != nil {
			return fmt.Errorf("compile rules: %w", err)
		}
		eng.ReloadModes(modes)
		eng.SetRedactTitles(cfg.RedactTitles)
		eng.SetLayoutParameters(layout.Gaps{
			Inner: cfg.Gaps.Inner,
			Outer: cfg.Gaps.Outer,
		}, cfg.PlacementTolerancePx)
		if err := eng.Reconcile(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("reconcile after reload: %w", err)
		}
		return nil
	}

	ctrlSrv, err := control.NewServer(eng, logger, reload)
	if err != nil {
		exitErr(fmt.Errorf("start control server: %w", err))
	}

	errs := make(chan error, 2)
	go func() {
		errs <- eng.Run(ctx)
	}()
	go func() {
		errs <- ctrlSrv.Serve(ctx)
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
		case reason := <-reloadRequests:
			if err := reload(reason); err != nil {
				logger.Errorf("reload failed: %v", err)
			}
		case sig := <-sigs:
			switch sig {
			case syscall.SIGHUP:
				if err := reload("received SIGHUP"); err != nil {
					logger.Errorf("reload failed: %v", err)
				}
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
