package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type testHyprctl struct{}

func (testHyprctl) ListClients(context.Context) ([]state.Client, error)       { return nil, nil }
func (testHyprctl) ListWorkspaces(context.Context) ([]state.Workspace, error) { return nil, nil }
func (testHyprctl) ListMonitors(context.Context) ([]state.Monitor, error)     { return nil, nil }
func (testHyprctl) ActiveWorkspaceID(context.Context) (int, error)            { return 0, nil }
func (testHyprctl) ActiveClientAddress(context.Context) (string, error)       { return "", nil }
func (testHyprctl) Dispatch(...string) error                                  { return nil }

func TestReloadLogsDiffOnFailureAndKeepsPreviousConfig(t *testing.T) {
	t.Helper()

	initial := strings.TrimPrefix(`
managedWorkspaces:
  - 1
modes:
  - name: Focus
    rules:
      - name: Dock
        when:
          mode: Focus
        actions:
          - type: layout.sidecarDock
            params:
              workspace: 1
              side: left
              widthPercent: 25
              match:
                anyClass: [Slack]
`, "\n")
	bad := strings.TrimPrefix(`
managedWorkspaces:
  - 1
modes:
  - name: Focus
    rules:
      - name: Dock
        when:
          mode: Focus
        actions:
          - type: layout.sidecarDock
            params:
              workspace: 1
              side: left
              widthPercent: 5
              match:
                anyClass: [Slack]
`, "\n")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := config.Parse([]byte(initial))
	if err != nil {
		t.Fatalf("parse initial config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate initial config: %v", err)
	}
	modes, err := rules.BuildModes(cfg)
	if err != nil {
		t.Fatalf("build modes: %v", err)
	}

	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	eng := engine.New(testHyprctl{}, logger, modes, false, cfg.RedactTitles, layout.Gaps{
		Inner: cfg.Gaps.Inner,
		Outer: cfg.Gaps.Outer,
	}, cfg.TolerancePx, cfg.ManualReserved)

	reloader := newConfigReloader(path, logger, eng, cfg, []byte(initial))

	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	err = reloader.Reload(context.Background(), "test reason")
	if err == nil {
		t.Fatalf("expected reload error, got nil")
	}
	if !strings.Contains(err.Error(), "widthPercent") {
		t.Fatalf("expected widthPercent error, got %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "config change rejected; diff vs last valid config") {
		t.Fatalf("expected diff log, got %s", logOutput)
	}
	if strings.Contains(logOutput, "reloaded") {
		t.Fatalf("engine should not reload modes on failure: %s", logOutput)
	}

	if err := eng.Reconcile(context.Background()); err != nil {
		t.Fatalf("engine reconcile after failed reload: %v", err)
	}
	modesAfter := eng.AvailableModes()
	if len(modesAfter) != 1 || modesAfter[0] != "Focus" {
		t.Fatalf("unexpected modes after failed reload: %v", modesAfter)
	}
}
