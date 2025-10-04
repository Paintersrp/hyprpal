package rules

import (
	"testing"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/state"
)

func worldFixture(t *testing.T, activeIndex int, clients ...state.Client) *state.World {
	t.Helper()
	const (
		workspaceID = 1
		monitorName = "DP-1"
	)
	clones := make([]state.Client, len(clients))
	copy(clones, clients)
	for i := range clones {
		if clones[i].WorkspaceID == 0 {
			clones[i].WorkspaceID = workspaceID
		}
		if clones[i].MonitorName == "" {
			clones[i].MonitorName = monitorName
		}
	}
	world := &state.World{
		Clients:             clones,
		Workspaces:          []state.Workspace{{ID: workspaceID, MonitorName: monitorName}},
		Monitors:            []state.Monitor{{Name: monitorName}},
		ActiveWorkspaceID:   workspaceID,
		ActiveClientAddress: "",
	}
	if activeIndex >= 0 {
		if activeIndex >= len(clones) {
			t.Fatalf("active index %d out of range", activeIndex)
		}
		world.ActiveClientAddress = clones[activeIndex].Address
	}
	return world
}

func TestAppClassPredicateMatchesActiveClient(t *testing.T) {
	pred, err := BuildPredicate(config.PredicateConfig{AppClass: "Slack"})
	if err != nil {
		t.Fatalf("build predicate: %v", err)
	}
	world := worldFixture(t, 0, state.Client{
		Address: "0xabc",
		Class:   "Slack",
	})
	if !pred(EvalContext{Mode: "Coding", World: world}) {
		t.Fatalf("expected predicate to match active Slack window")
	}
}

func TestPredicateLogicalCombinators(t *testing.T) {
	world := worldFixture(t, 0, state.Client{
		Address: "0x111",
		Class:   "Slack",
		Title:   "Daily Standup",
	})
	cfg := config.PredicateConfig{
		All: []config.PredicateConfig{
			{
				Any: []config.PredicateConfig{{Mode: "Coding"}, {AppClass: "Nonexistent"}},
			},
			{Not: &config.PredicateConfig{Mode: "Gaming"}},
		},
		AppClass: "Slack",
	}
	pred, err := BuildPredicate(cfg)
	if err != nil {
		t.Fatalf("build predicate: %v", err)
	}
	if !pred(EvalContext{Mode: "Coding", World: world}) {
		t.Fatalf("expected combined predicate to succeed")
	}
	if pred(EvalContext{Mode: "Gaming", World: world}) {
		t.Fatalf("expected predicate to fail when mode is Gaming")
	}
}

func TestPredicateClassAndTitleRegex(t *testing.T) {
	matchingWorld := worldFixture(t, 0, state.Client{
		Address: "0x222",
		Class:   "Slack",
		Title:   "Morning Standup",
	})
	cfg := config.PredicateConfig{AppClass: "slack", TitleRegex: "Standup$"}
	pred, err := BuildPredicate(cfg)
	if err != nil {
		t.Fatalf("build predicate: %v", err)
	}
	if !pred(EvalContext{Mode: "Coding", World: matchingWorld}) {
		t.Fatalf("expected predicate to match case-insensitive class and title regex")
	}
	nonMatchingWorld := worldFixture(t, 0, state.Client{
		Address: "0x333",
		Class:   "Slack",
		Title:   "Weekly Sync",
	})
	if pred(EvalContext{Mode: "Coding", World: nonMatchingWorld}) {
		t.Fatalf("expected predicate to fail when title regex does not match")
	}
}

func TestAppsPresentRequiresAllClasses(t *testing.T) {
	pred, err := BuildPredicate(config.PredicateConfig{AppsPresent: []string{"Slack", "Discord"}})
	if err != nil {
		t.Fatalf("build predicate: %v", err)
	}
	world := worldFixture(t, 0,
		state.Client{Address: "0x444", Class: "Slack"},
		state.Client{Address: "0x555", Class: "Discord"},
		state.Client{Address: "0x666", Class: "Firefox"},
	)
	if !pred(EvalContext{Mode: "Coding", World: world}) {
		t.Fatalf("expected apps.present predicate to succeed when all classes exist")
	}
	missingWorld := worldFixture(t, 0,
		state.Client{Address: "0x777", Class: "Slack"},
		state.Client{Address: "0x888", Class: "Firefox"},
	)
	if pred(EvalContext{Mode: "Coding", World: missingWorld}) {
		t.Fatalf("expected apps.present predicate to fail when a class is missing")
	}
}

func TestPredicateTracerProducesTrace(t *testing.T) {
	cfg := config.PredicateConfig{
		All: []config.PredicateConfig{
			{Mode: "Coding"},
			{Any: []config.PredicateConfig{{AppClass: "Slack"}, {AppClass: "Discord"}}},
		},
		MonitorName: "DP-1",
	}
	pred, tracer, err := BuildPredicateWithTrace(cfg)
	if err != nil {
		t.Fatalf("build predicate with trace: %v", err)
	}
	world := worldFixture(t, 0, state.Client{Address: "0x999", Class: "Slack", Title: "Daily Standup"})
	ctx := EvalContext{Mode: "Coding", World: world}
	if !pred(ctx) {
		t.Fatalf("expected predicate to match composite conditions")
	}
	matched, trace := tracer.Trace(ctx)
	if !matched {
		t.Fatalf("expected tracer match result to be true")
	}
	if trace == nil {
		t.Fatalf("expected trace tree")
	}
	if trace.Kind != "predicate" {
		t.Fatalf("expected root trace kind 'predicate', got %q", trace.Kind)
	}
	if node := findTraceByKind(trace, "any"); node == nil || !node.Result {
		t.Fatalf("expected 'any' node to succeed")
	}
	if node := findTraceByKind(trace, "mode"); node == nil || !node.Result {
		t.Fatalf("expected mode node to succeed")
	}

	ctxNoMatch := EvalContext{Mode: "Gaming", World: world}
	if pred(ctxNoMatch) {
		t.Fatalf("expected predicate to fail when mode mismatches")
	}
	matched, trace = tracer.Trace(ctxNoMatch)
	if matched {
		t.Fatalf("expected tracer match result to be false")
	}
	if node := findTraceByKind(trace, "mode"); node == nil || node.Result {
		t.Fatalf("expected mode node to report failure")
	}
}

func findTraceByKind(trace *PredicateTrace, kind string) *PredicateTrace {
	if trace == nil {
		return nil
	}
	if trace.Kind == kind {
		return trace
	}
	for _, child := range trace.Children {
		if found := findTraceByKind(child, kind); found != nil {
			return found
		}
	}
	return nil
}
