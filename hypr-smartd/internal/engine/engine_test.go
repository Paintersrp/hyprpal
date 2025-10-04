package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hyprpal/hypr-smartd/internal/layout"
	"github.com/hyprpal/hypr-smartd/internal/rules"
	"github.com/hyprpal/hypr-smartd/internal/state"
	"github.com/hyprpal/hypr-smartd/internal/util"
)

type fakeHyprctl struct {
	clients         []state.Client
	workspaces      []state.Workspace
	monitors        []state.Monitor
	activeWorkspace int
	activeClient    string
	dispatched      [][]string
}

func (f *fakeHyprctl) ListClients(context.Context) ([]state.Client, error) {
	return append([]state.Client(nil), f.clients...), nil
}

func (f *fakeHyprctl) ListWorkspaces(context.Context) ([]state.Workspace, error) {
	return append([]state.Workspace(nil), f.workspaces...), nil
}

func (f *fakeHyprctl) ListMonitors(context.Context) ([]state.Monitor, error) {
	return append([]state.Monitor(nil), f.monitors...), nil
}

func (f *fakeHyprctl) ActiveWorkspaceID(context.Context) (int, error) {
	return f.activeWorkspace, nil
}

func (f *fakeHyprctl) ActiveClientAddress(context.Context) (string, error) {
	return f.activeClient, nil
}

func (f *fakeHyprctl) Dispatch(args ...string) error {
	copyArgs := append([]string(nil), args...)
	f.dispatched = append(f.dispatched, copyArgs)
	return nil
}

func TestReconcileAndApplyLogsDebounceSkip(t *testing.T) {
	hypr := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	mode := rules.Mode{
		Name: "Focus",
		Rules: []rules.Rule{{
			Name:     "noop",
			When:     func(rules.EvalContext) bool { return true },
			Actions:  []rules.Action{rules.NoopAction{}},
			Debounce: time.Second,
		}},
	}
	eng := New(hypr, logger, []rules.Mode{mode}, false)
	key := mode.Name + ":" + mode.Rules[0].Name
	eng.debounce[key] = time.Now()

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply returned error: %v", err)
	}
	got := logs.String()
	expected := "rule noop skipped (debounced) [mode Focus]"
	if !strings.Contains(got, expected) {
		t.Fatalf("expected log %q, got %q", expected, got)
	}
	if len(hypr.dispatched) != 0 {
		t.Fatalf("expected no dispatches, got %d", len(hypr.dispatched))
	}
}
