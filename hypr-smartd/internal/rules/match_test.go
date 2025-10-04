package rules

import (
	"testing"

	"github.com/hyprpal/hypr-smartd/internal/config"
	"github.com/hyprpal/hypr-smartd/internal/layout"
	"github.com/hyprpal/hypr-smartd/internal/state"
)

func TestAppClassPredicateMatchesActiveClient(t *testing.T) {
	pred, err := BuildPredicate(config.PredicateConfig{AppClass: "Slack"})
	if err != nil {
		t.Fatalf("build predicate: %v", err)
	}
	world := &state.World{
		Clients: []state.Client{{
			Address:     "0xabc",
			Class:       "Slack",
			WorkspaceID: 1,
		}},
		ActiveClientAddress: "0xabc",
		ActiveWorkspaceID:   1,
		Workspaces:          []state.Workspace{{ID: 1, MonitorName: "DP-1"}},
		Monitors:            []state.Monitor{{Name: "DP-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	if !pred(EvalContext{Mode: "Coding", World: world}) {
		t.Fatalf("expected predicate to match active Slack window")
	}
}
