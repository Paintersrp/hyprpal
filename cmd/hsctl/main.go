package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hyprpal/hyprpal/internal/control/client"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	fs := flag.NewFlagSet("hsctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	socket := fs.String("socket", "", "path to hyprpal control socket")
	timeout := fs.Duration("timeout", 3*time.Second, "control request timeout")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	args := fs.Args()
	if len(args) == 0 {
		fs.Usage()
		return fmt.Errorf("missing subcommand")
	}

	cli, err := client.New(*socket)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	switch args[0] {
	case "mode":
		return runMode(ctx, cli, args[1:])
	case "reload":
		return runReload(ctx, cli)
	case "plan":
		return runPlan(ctx, cli, args[1:])
	default:
		fs.Usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runMode(ctx context.Context, cli *client.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("mode requires a subcommand (get|set)")
	}
	switch args[0] {
	case "get":
		status, err := cli.Mode(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Active mode: %s\n", status.Active)
		if len(status.Available) > 0 {
			fmt.Printf("Available modes: %s\n", strings.Join(status.Available, ", "))
		}
		return nil
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("mode set requires a mode name")
		}
		if err := cli.SetMode(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("Switched to mode %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown mode subcommand %q", args[0])
	}
}

func runReload(ctx context.Context, cli *client.Client) error {
	if err := cli.Reload(ctx); err != nil {
		return err
	}
	fmt.Println("Reload requested")
	return nil
}

func runPlan(ctx context.Context, cli *client.Client, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	explain := fs.Bool("explain", false, "include rule explanations in the response")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	result, err := cli.Plan(ctx, *explain)
	if err != nil {
		return err
	}
	if len(result.Commands) == 0 {
		fmt.Println("No pending actions")
		return nil
	}
	for _, cmd := range result.Commands {
		fmt.Printf("dispatch: %s\n", strings.Join(cmd.Dispatch, " "))
		if cmd.Reason != "" {
			fmt.Printf("  reason: %s\n", cmd.Reason)
		}
	}
	return nil
}
