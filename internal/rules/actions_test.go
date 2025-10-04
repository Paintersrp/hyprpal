package rules

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

func TestBuildSidecarDockRejectsNarrowWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 5,
		"side":         "left",
	})
	if err == nil {
		t.Fatalf("expected error for widthPercent below minimum")
	}
}

func TestBuildSidecarDockRejectsWideWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 60,
		"side":         "right",
	})
	if err == nil {
		t.Fatalf("expected error for widthPercent above maximum")
	}
}

func TestSidecarDockPlanIdempotentLogs(t *testing.T) {
	action := &SidecarDockAction{
		WorkspaceID:  1,
		Side:         "right",
		WidthPercent: 25,
		Match:        func(state.Client) bool { return true },
	}

	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xabc",
			WorkspaceID: 1,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 0, Y: 0, Width: 800, Height: 600},
		}},
		Workspaces: []state.Workspace{{ID: 1, MonitorName: "DP-1"}},
		Monitors: []state.Monitor{{
			Name:      "DP-1",
			Rectangle: layout.Rect{X: 0, Y: 0, Width: 1600, Height: 900},
		}},
	}

	_, dock := layout.SplitSidecar(world.Monitors[0].Rectangle, action.Side, action.WidthPercent, layout.Gaps{})

	buf := &bytes.Buffer{}
	logger := util.NewLoggerWithWriter(util.LevelInfo, buf)
	ctx := ActionContext{World: world, Logger: logger, RuleName: "sidecar", PlacementTolerance: 2, Gaps: layout.Gaps{}}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatalf("expected commands on first plan when geometry differs")
	}

	world.Clients[0].Geometry = dock

	secondPlan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("second plan failed: %v", err)
	}
	if len(secondPlan.Commands) != 0 {
		t.Fatalf("expected no commands when geometry already matches")
	}

	if !strings.Contains(buf.String(), "rule sidecar skipped (idempotent)") {
		t.Fatalf("expected idempotent skip log, got %q", buf.String())
	}
}

func TestSidecarDockPlanSkipsOnUnmanagedWorkspace(t *testing.T) {
	action := &SidecarDockAction{
		WorkspaceID:  5,
		Side:         "right",
		WidthPercent: 25,
		Match:        func(state.Client) bool { return true },
	}

	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xabc",
			WorkspaceID: 5,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 0, Y: 0, Width: 800, Height: 600},
		}},
		Workspaces: []state.Workspace{{ID: 5, MonitorName: "DP-1"}},
		Monitors: []state.Monitor{{
			Name:      "DP-1",
			Rectangle: layout.Rect{X: 0, Y: 0, Width: 1600, Height: 900},
		}},
	}

	buf := &bytes.Buffer{}
	logger := util.NewLoggerWithWriter(util.LevelInfo, buf)
	ctx := ActionContext{
		World:              world,
		Logger:             logger,
		RuleName:           "sidecar",
		ManagedWorkspaces:  map[int]struct{}{1: {}},
		PlacementTolerance: 2,
		Gaps:               layout.Gaps{},
	}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) != 0 {
		t.Fatalf("expected no commands when workspace unmanaged, got %d", len(plan.Commands))
	}
	if !strings.Contains(buf.String(), "workspace 5 unmanaged") {
		t.Fatalf("expected unmanaged workspace log, got %q", buf.String())
	}
}

func TestFullscreenPlanSkipsOnUnmanagedWorkspace(t *testing.T) {
	action := &FullscreenAction{Target: "active", Match: func(state.Client) bool { return true }}
	world := &state.World{
		Clients: []state.Client{{
			Address:        "0xabc",
			WorkspaceID:    7,
			MonitorName:    "DP-1",
			FullscreenMode: 0,
			Focused:        true,
		}},
		Workspaces:          []state.Workspace{{ID: 7, MonitorName: "DP-1"}},
		Monitors:            []state.Monitor{{Name: "DP-1", Rectangle: layout.Rect{Width: 1600, Height: 900}}},
		ActiveClientAddress: "0xabc",
	}
	buf := &bytes.Buffer{}
	logger := util.NewLoggerWithWriter(util.LevelInfo, buf)
	ctx := ActionContext{
		World:              world,
		Logger:             logger,
		RuleName:           "fullscreen",
		ManagedWorkspaces:  map[int]struct{}{1: {}},
		PlacementTolerance: 2,
		Gaps:               layout.Gaps{},
	}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) != 0 {
		t.Fatalf("expected no commands when workspace unmanaged, got %d", len(plan.Commands))
	}
	if !strings.Contains(buf.String(), "workspace 7 unmanaged") {
		t.Fatalf("expected unmanaged workspace log, got %q", buf.String())
	}
}

func TestFullscreenPlanAllowsWhenOptedIn(t *testing.T) {
	action := &FullscreenAction{Target: "active", Match: func(state.Client) bool { return true }}
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xabc",
			WorkspaceID: 7,
			MonitorName: "DP-1",
		}},
		Workspaces:          []state.Workspace{{ID: 7, MonitorName: "DP-1"}},
		Monitors:            []state.Monitor{{Name: "DP-1", Rectangle: layout.Rect{Width: 1600, Height: 900}}},
		ActiveClientAddress: "0xabc",
	}
	buf := &bytes.Buffer{}
	logger := util.NewLoggerWithWriter(util.LevelInfo, buf)
	ctx := ActionContext{
		World:              world,
		Logger:             logger,
		RuleName:           "fullscreen",
		ManagedWorkspaces:  map[int]struct{}{1: {}},
		AllowUnmanaged:     true,
		PlacementTolerance: 2,
		Gaps:               layout.Gaps{},
	}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatalf("expected commands when unmanaged workspaces allowed")
	}
	if strings.Contains(buf.String(), "unmanaged") {
		t.Fatalf("unexpected unmanaged log when allowUnmanaged is true: %q", buf.String())
	}
}
