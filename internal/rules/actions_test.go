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

func TestParseClientMatcherAllOfProfiles(t *testing.T) {
	profiles := map[string]config.MatcherConfig{
		"comms": {AnyClass: []string{"Slack", "Discord"}},
		"focus": {TitleRegex: "Focus"},
	}

	matcher, err := parseClientMatcher(map[string]interface{}{
		"allOfProfiles": []interface{}{"comms", "focus"},
	}, profiles)
	if err != nil {
		t.Fatalf("expected matcher, got error: %v", err)
	}

	client := state.Client{Class: "Slack", Title: "Deep Focus"}
	if !matcher(client) {
		t.Fatalf("expected matcher to require all profiles")
	}

	other := state.Client{Class: "Slack", Title: "Random"}
	if matcher(other) {
		t.Fatalf("expected matcher to reject when any profile fails")
	}
}

func TestParseClientMatcherAnyOfProfiles(t *testing.T) {
	profiles := map[string]config.MatcherConfig{
		"term":   {Class: "foot"},
		"editor": {TitleRegex: "Code"},
	}

	matcher, err := parseClientMatcher(map[string]interface{}{
		"anyOfProfiles": []interface{}{"term", "editor"},
	}, profiles)
	if err != nil {
		t.Fatalf("expected matcher, got error: %v", err)
	}

	if !matcher(state.Client{Class: "foot"}) {
		t.Fatalf("expected matcher to match first profile")
	}
	if !matcher(state.Client{Class: "kitty", Title: "VS Code"}) {
		t.Fatalf("expected matcher to match second profile")
	}
	if matcher(state.Client{Class: "kitty", Title: "Notes"}) {
		t.Fatalf("expected matcher to reject when no profiles match")
	}
}

func TestParseClientMatcherProfileRegression(t *testing.T) {
	profiles := map[string]config.MatcherConfig{
		"comms": {AnyClass: []string{"Slack"}},
	}

	matcher, err := parseClientMatcher(map[string]interface{}{"profile": "comms"}, profiles)
	if err != nil {
		t.Fatalf("expected matcher, got error: %v", err)
	}
	if !matcher(state.Client{Class: "Slack"}) {
		t.Fatalf("expected matcher to use single profile")
	}
}

func TestParseClientMatcherAllOfProfilesErrors(t *testing.T) {
	profiles := map[string]config.MatcherConfig{}

	if _, err := parseClientMatcher(map[string]interface{}{"allOfProfiles": []interface{}{}}, profiles); err == nil {
		t.Fatalf("expected error for empty allOfProfiles")
	}

	if _, err := parseClientMatcher(map[string]interface{}{"allOfProfiles": []interface{}{"missing"}}, profiles); err == nil {
		t.Fatalf("expected error for unknown profile in allOfProfiles")
	}
}

func TestParseClientMatcherAnyOfProfilesErrors(t *testing.T) {
	profiles := map[string]config.MatcherConfig{}

	if _, err := parseClientMatcher(map[string]interface{}{"anyOfProfiles": []interface{}{}}, profiles); err == nil {
		t.Fatalf("expected error for empty anyOfProfiles")
	}

	if _, err := parseClientMatcher(map[string]interface{}{"anyOfProfiles": []interface{}{"missing"}}, profiles); err == nil {
		t.Fatalf("expected error for unknown profile in anyOfProfiles")
	}
}

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

func TestBuildSidecarDockRejectsInvalidFocusAfter(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":  1,
		"side":       "left",
		"focusAfter": "later",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "focusAfter") {
		t.Fatalf("expected focusAfter validation error, got %v", err)
	}
}

func TestSidecarDockPlanIdempotentLogs(t *testing.T) {
	action := &SidecarDockAction{
		WorkspaceID:  1,
		Side:         "right",
		WidthPercent: 25,
		Match:        func(state.Client) bool { return true },
		FocusAfter:   "sidecar",
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
		FocusAfter:   "sidecar",
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
		FocusAfter:   "sidecar",
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

func TestSidecarDockPlanFocusPolicies(t *testing.T) {
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xside",
			WorkspaceID: 1,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 0, Y: 0, Width: 500, Height: 500},
		}, {
			Address:     "0xhost",
			WorkspaceID: 1,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 500, Y: 0, Width: 1100, Height: 500},
			Focused:     true,
		}},
		Workspaces:          []state.Workspace{{ID: 1, MonitorName: "DP-1"}},
		Monitors:            []state.Monitor{{Name: "DP-1", Rectangle: layout.Rect{X: 0, Y: 0, Width: 1600, Height: 900}}},
		ActiveClientAddress: "0xhost",
	}
	ctx := ActionContext{
		World:           world,
		RuleName:        "sidecar",
		TolerancePx:     1,
		Gaps:            layout.Gaps{},
		MonitorReserved: map[string]layout.Insets{},
	}

	mkAction := func(focus string) *SidecarDockAction {
		return &SidecarDockAction{
			WorkspaceID:  1,
			Side:         "right",
			WidthPercent: 25,
			Match: func(c state.Client) bool {
				return c.Address == "0xside"
			},
			FocusAfter: focus,
		}
	}

	t.Run("sidecar", func(t *testing.T) {
		plan, err := mkAction("sidecar").Plan(ctx)
		if err != nil {
			t.Fatalf("plan failed: %v", err)
		}
		focuses := focusTargets(plan)
		if len(focuses) != 1 || focuses[0] != "address:0xside" {
			t.Fatalf("expected a single focus on sidecar, got %v", focuses)
		}
	})

	t.Run("host", func(t *testing.T) {
		plan, err := mkAction("host").Plan(ctx)
		if err != nil {
			t.Fatalf("plan failed: %v", err)
		}
		focuses := focusTargets(plan)
		if len(focuses) != 2 {
			t.Fatalf("expected two focus commands, got %v", focuses)
		}
		if focuses[0] != "address:0xside" || focuses[1] != "address:0xhost" {
			t.Fatalf("expected focus sequence sidecar→host, got %v", focuses)
		}
		if last := plan.Commands[len(plan.Commands)-1]; !equalCommand(last, []string{"focuswindow", "address:0xhost"}) {
			t.Fatalf("expected final focus command to target host, got %v", last)
		}
	})

	t.Run("none", func(t *testing.T) {
		plan, err := mkAction("none").Plan(ctx)
		if err != nil {
			t.Fatalf("plan failed: %v", err)
		}
		if len(plan.Commands) < 4 {
			t.Fatalf("expected move/resize commands, got %d", len(plan.Commands))
		}
		focuses := focusTargets(plan)
		if len(focuses) != 1 || focuses[0] != "address:0xside" {
			t.Fatalf("expected only initial focus on sidecar, got %v", focuses)
		}
		if last := plan.Commands[len(plan.Commands)-1]; last[0] == "focuswindow" {
			t.Fatalf("expected final command to be non-focus, got %v", last)
		}
	})
}

func TestSidecarDockPlanSkipsFocusWhenTargetAlreadyActive(t *testing.T) {
	monitorRect := layout.Rect{X: 0, Y: 0, Width: 1600, Height: 900}
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xside",
			WorkspaceID: 1,
			MonitorName: "DP-1",
			Geometry:    layout.Rect{X: 200, Y: 150, Width: 600, Height: 500},
			Focused:     true,
		}, {
			Address:     "0xhost",
			WorkspaceID: 1,
			MonitorName: "DP-1",
		}},
		Workspaces:          []state.Workspace{{ID: 1, MonitorName: "DP-1"}},
		Monitors:            []state.Monitor{{Name: "DP-1", Rectangle: monitorRect}},
		ActiveClientAddress: "0xside",
	}

	ctx := ActionContext{
		World:           world,
		RuleName:        "sidecar",
		TolerancePx:     1,
		Gaps:            layout.Gaps{},
		MonitorReserved: map[string]layout.Insets{},
	}

	mkAction := func(focus string) *SidecarDockAction {
		return &SidecarDockAction{
			WorkspaceID:  1,
			Side:         "right",
			WidthPercent: 25,
			Match: func(c state.Client) bool {
				return c.Address == "0xside"
			},
			FocusAfter: focus,
		}
	}

	for _, focus := range []string{"sidecar", "host", "none"} {
		t.Run(focus, func(t *testing.T) {
			action := mkAction(focus)
			plan, err := action.Plan(ctx)
			if err != nil {
				t.Fatalf("plan failed: %v", err)
			}
			if len(plan.Commands) < 3 {
				t.Fatalf("expected plan to include placement commands, got %d", len(plan.Commands))
			}
			if focuses := focusTargets(plan); len(focuses) != 0 {
				t.Fatalf("expected no focus commands when target already active, got %v", focuses)
			}

			secondPlan, err := action.Plan(ctx)
			if err != nil {
				t.Fatalf("second plan failed: %v", err)
			}
			if focuses := focusTargets(secondPlan); len(focuses) != 0 {
				t.Fatalf("expected no focus commands on repeated plan, got %v", focuses)
			}
		})
	}
}

func equalCommand(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func focusTargets(plan layout.Plan) []string {
	var targets []string
	for _, cmd := range plan.Commands {
		if len(cmd) >= 2 && cmd[0] == "focuswindow" {
			targets = append(targets, cmd[1])
		}
	}
	return targets
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
