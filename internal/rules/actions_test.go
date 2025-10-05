package rules

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

func TestBuildSidecarDockRejectsNarrowWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 5,
		"side":         "left",
	}, nil)
	if err == nil {
		t.Fatalf("expected error for widthPercent below minimum")
	}
}

func TestBuildSidecarDockRejectsWideWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 60,
		"side":         "right",
	}, nil)
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

	_, dock := layout.SplitSidecar(world.Monitors[0].Rectangle, action.Side, action.WidthPercent, layout.Gaps{}, layout.Insets{})

	buf := &bytes.Buffer{}
	logger := util.NewLoggerWithWriter(util.LevelInfo, buf)
	ctx := ActionContext{World: world, Logger: logger, RuleName: "sidecar", TolerancePx: 2, Gaps: layout.Gaps{}, MonitorReserved: map[string]layout.Insets{}}

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
		World:             world,
		Logger:            logger,
		RuleName:          "sidecar",
		ManagedWorkspaces: map[int]struct{}{1: {}},
		TolerancePx:       2,
		Gaps:              layout.Gaps{},
		MonitorReserved:   map[string]layout.Insets{},
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

func TestSidecarDockPlanRespectsReservedInsets(t *testing.T) {
	action := &SidecarDockAction{
		WorkspaceID:  3,
		Side:         "left",
		WidthPercent: 25,
		Match:        func(state.Client) bool { return true },
	}

	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xabc",
			WorkspaceID: 3,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 0, Y: 0, Width: 600, Height: 400},
		}},
		Workspaces: []state.Workspace{{ID: 3, MonitorName: "DP-1"}},
		Monitors: []state.Monitor{{
			Name:      "DP-1",
			Rectangle: layout.Rect{X: 0, Y: 0, Width: 1200, Height: 800},
		}},
	}

	reserved := layout.Insets{Left: 40, Top: 10, Bottom: 10}
	ctx := ActionContext{
		World:           world,
		RuleName:        "sidecar",
		TolerancePx:     1,
		Gaps:            layout.Gaps{},
		MonitorReserved: map[string]layout.Insets{"DP-1": reserved},
	}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatalf("expected commands when target must move")
	}
	_, dockRect := layout.SplitSidecar(world.Monitors[0].Rectangle, action.Side, action.WidthPercent, layout.Gaps{}, reserved)
	if layout.ApproximatelyEqual(world.Clients[0].Geometry, dockRect, ctx.TolerancePx) {
		t.Fatalf("expected reserved insets to change target rect")
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
		World:             world,
		Logger:            logger,
		RuleName:          "fullscreen",
		ManagedWorkspaces: map[int]struct{}{1: {}},
		TolerancePx:       2,
		Gaps:              layout.Gaps{},
		MonitorReserved:   map[string]layout.Insets{},
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

func TestGridPlanSkipsMissingClients(t *testing.T) {
	action := &GridLayoutAction{
		WorkspaceID:   1,
		ColumnWeights: []float64{1, 1},
		RowWeights:    []float64{1},
		Slots: []GridSlotAction{{
			Name:    "primary",
			Row:     0,
			Col:     0,
			RowSpan: 1,
			ColSpan: 2,
			Match:   func(c state.Client) bool { return strings.EqualFold(c.Class, "Code") },
		}},
	}
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xaaa",
			WorkspaceID: 1,
			MonitorName: "DP-1",
			Class:       "Chat",
		}},
		Workspaces: []state.Workspace{{ID: 1, MonitorName: "DP-1"}},
		Monitors:   []state.Monitor{{Name: "DP-1", Rectangle: layout.Rect{Width: 1000, Height: 800}}},
	}
	ctx := ActionContext{
		World:           world,
		RuleName:        "grid",
		Gaps:            layout.Gaps{},
		MonitorReserved: map[string]layout.Insets{},
	}
	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) != 0 {
		t.Fatalf("expected no commands when slot has no matching client")
	}
}

func TestGridPlanIgnoresOutOfBoundsSlot(t *testing.T) {
	matched := false
	action := &GridLayoutAction{
		WorkspaceID:   2,
		ColumnWeights: []float64{1, 1},
		RowWeights:    []float64{1, 1},
		Slots: []GridSlotAction{{
			Name:    "invalid",
			Row:     5,
			Col:     0,
			RowSpan: 1,
			ColSpan: 1,
			Match:   func(c state.Client) bool { return true },
		}, {
			Name:    "valid",
			Row:     0,
			Col:     0,
			RowSpan: 1,
			ColSpan: 1,
			Match: func(c state.Client) bool {
				matched = true
				return true
			},
		}},
	}
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xbbb",
			WorkspaceID: 2,
			MonitorName: "DP-2",
			Geometry:    layout.Rect{Width: 100, Height: 100},
		}},
		Workspaces: []state.Workspace{{ID: 2, MonitorName: "DP-2"}},
		Monitors:   []state.Monitor{{Name: "DP-2", Rectangle: layout.Rect{Width: 1200, Height: 800}}},
	}
	ctx := ActionContext{
		World:           world,
		RuleName:        "grid",
		Gaps:            layout.Gaps{},
		MonitorReserved: map[string]layout.Insets{},
	}
	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatalf("expected commands for valid slot")
	}
	if !matched {
		t.Fatalf("expected matcher to be invoked for valid slot")
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
		World:             world,
		Logger:            logger,
		RuleName:          "fullscreen",
		ManagedWorkspaces: map[int]struct{}{1: {}},
		MutateUnmanaged:   true,
		TolerancePx:       2,
		Gaps:              layout.Gaps{},
		MonitorReserved:   map[string]layout.Insets{},
	}

	plan, err := action.Plan(ctx)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatalf("expected commands when unmanaged workspaces allowed")
	}
	if strings.Contains(buf.String(), "unmanaged") {
		t.Fatalf("unexpected unmanaged log when mutateUnmanaged is true: %q", buf.String())
	}
}

func TestParseClientMatcherProfile(t *testing.T) {
	profiles := map[string]config.MatcherConfig{
		"comms": {AnyClass: []string{"Slack", "discord"}},
	}

	matcher, err := parseClientMatcher(map[string]interface{}{"profile": "comms"}, profiles)
	if err != nil {
		t.Fatalf("parseClientMatcher returned error: %v", err)
	}

	if !matcher(state.Client{Class: "Slack"}) {
		t.Fatalf("expected matcher to match profile class")
	}

	if matcher(state.Client{Class: "Firefox"}) {
		t.Fatalf("expected matcher to reject non-profile class")
	}
}
