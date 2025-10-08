package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/ipc"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type smokeClient struct {
	*ipc.Client
}

func (c *smokeClient) Dispatch(args ...string) error {
	return nil
}

func (c *smokeClient) DispatchBatch(commands [][]string) error {
	return nil
}

func main() {
	home, _ := os.UserHomeDir()
	defaultConfig := filepath.Join(home, ".config", "hyprpal", "config.yaml")

	cfgPath := flag.String("config", defaultConfig, "path to YAML config")
	logLevel := flag.String("log-level", "info", "log level (trace|debug|info|warn|error)")
	mode := flag.String("mode", "", "mode to evaluate (defaults to config's first mode)")
	explain := flag.Bool("explain", true, "include rule reasons with planned commands")
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

	client := &smokeClient{Client: ipc.NewClient()}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	world, err := state.NewWorld(ctx, client)
	if err != nil {
		exitErr(fmt.Errorf("build world: %w", err))
	}

	fmt.Printf("Loaded config from %s\n", *cfgPath)
	fmt.Println("\n=== Configuration ===")
	if err := marshalYAML(cfg); err != nil {
		logger.Warnf("failed to print config: %v", err)
	}

	fmt.Println("\n=== World Snapshot ===")
	if err := marshalJSON(world); err != nil {
		logger.Warnf("failed to print world snapshot: %v", err)
	}

	eng := engine.New(client, logger, modes, true, cfg.RedactTitles, layout.Gaps{
		Inner: cfg.Gaps.Inner,
		Outer: cfg.Gaps.Outer,
	}, cfg.TolerancePx, cfg.ManualReserved)

	if *mode != "" {
		if err := eng.SetMode(*mode); err != nil {
			logger.Warnf("unable to set mode %q: %v", *mode, err)
		}
	}

	fmt.Printf("\nEvaluating mode: %s\n", eng.ActiveMode())
	commands, err := eng.PreviewPlan(ctx, *explain)
	if err != nil {
		exitErr(fmt.Errorf("preview plan: %w", err))
	}

	if len(commands) == 0 {
		fmt.Println("\nNo planned commands for current snapshot.")
	} else {
		fmt.Println("\n=== Planned Commands ===")
		for _, cmd := range commands {
			if *explain {
				action := cmd.Action
				if action == "" {
					action = "(unknown action)"
				}
				fmt.Printf("action: %s\n", action)
			}
			fmt.Printf("dispatch: %s\n", formatCommand(cmd.Dispatch))
			if cmd.Reason != "" {
				fmt.Printf("  reason: %s\n", cmd.Reason)
			}
		}
	}

	checks := eng.RuleCheckHistory()
	if len(checks) > 0 {
		fmt.Println("\n=== Rule Evaluation Checks ===")
		for _, check := range checks {
			status := "skipped"
			if check.Matched {
				status = "matched"
			}
			fmt.Printf("[%s] %s → %s", check.Timestamp.Format(time.RFC3339), check.Rule, status)
			if check.Reason != "" {
				fmt.Printf(" (%s)", check.Reason)
			}
			fmt.Println()
			if check.Predicate != nil {
				if err := marshalJSON(check.Predicate); err != nil {
					logger.Warnf("failed to print predicate trace for %s: %v", check.Rule, err)
				}
			}
		}
	}
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func marshalYAML(v any) error {
	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(v)
}

func marshalJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func formatCommand(parts []string) string {
	return strings.Join(parts, " ")
}
