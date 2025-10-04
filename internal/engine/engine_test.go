package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type fakeHyprctl struct {
	clients         []state.Client
	workspaces      []state.Workspace
	monitors        []state.Monitor
	activeWorkspace int
	activeClient    string
	dispatched      [][]string
}

type batchHyprctl struct {
	*fakeHyprctl
	batchCalls    int
	dispatchCalls int
}

type stubAction struct {
	plan layout.Plan
}

func (s stubAction) Plan(rules.ActionContext) (layout.Plan, error) {
	return s.plan, nil
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

func (b *batchHyprctl) Dispatch(args ...string) error {
	b.dispatchCalls++
	return b.fakeHyprctl.Dispatch(args...)
}

func (b *batchHyprctl) DispatchBatch(commands [][]string) error {
	b.batchCalls++
	for _, cmd := range commands {
		if err := b.fakeHyprctl.Dispatch(cmd...); err != nil {
			return err
		}
	}
	return nil
}

func clearCooldown(t *testing.T, eng *Engine, key string) {
	t.Helper()
	eng.mu.Lock()
	delete(eng.cooldown, key)
	eng.mu.Unlock()
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

func TestReconcileSkipsRulesDuringCooldown(t *testing.T) {
	hypr := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	var actPlan layout.Plan
	actPlan.Add("focuswindow", "address:fake")
	rule := rules.Rule{
		Name:    "cool",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: actPlan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	eng := New(hypr, logger, []rules.Mode{mode}, false)

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("first reconcileAndApply returned error: %v", err)
	}
	if len(hypr.dispatched) != 1 {
		t.Fatalf("expected one dispatch, got %d", len(hypr.dispatched))
	}

	logs.Reset()
	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("second reconcileAndApply returned error: %v", err)
	}
	expected := "rule cool skipped (cooldown) [mode Focus]"
	if !strings.Contains(logs.String(), expected) {
		t.Fatalf("expected log %q, got %q", expected, logs.String())
	}
	if len(hypr.dispatched) != 1 {
		t.Fatalf("expected no additional dispatches, got %d", len(hypr.dispatched))
	}
}

func TestReconcileUsesBatchDispatcherWhenAvailable(t *testing.T) {
	base := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	hypr := &batchHyprctl{fakeHyprctl: base}
	var plan layout.Plan
	plan.Add("focuswindow", "address:fake")
	plan.Add("movewindowpixel", "exact", "0", "0")
	rule := rules.Rule{
		Name:    "batch",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	eng := New(hypr, logger, []rules.Mode{mode}, false)

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply returned error: %v", err)
	}
	if hypr.batchCalls != 1 {
		t.Fatalf("expected batch dispatch to be used once, got %d", hypr.batchCalls)
	}
	if hypr.dispatchCalls != 0 {
		t.Fatalf("expected no individual dispatch calls, got %d", hypr.dispatchCalls)
	}
	if len(base.dispatched) != 2 {
		t.Fatalf("expected two dispatched commands, got %d", len(base.dispatched))
	}
}

func TestRuleExecutionTrackingAllowsNormalFlow(t *testing.T) {
	hypr := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	var plan layout.Plan
	plan.Add("focuswindow", "address:client")
	rule := rules.Rule{
		Name:    "burst",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	eng := New(hypr, logger, []rules.Mode{mode}, false)

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply returned error: %v", err)
	}
	if len(hypr.dispatched) != 1 {
		t.Fatalf("expected one dispatch, got %d", len(hypr.dispatched))
	}
	if strings.Contains(logs.String(), "temporarily disabled") {
		t.Fatalf("unexpected temporary disable log: %s", logs.String())
	}
}

func TestRuleExecutionTrackingBelowThreshold(t *testing.T) {
	hypr := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	var plan layout.Plan
	plan.Add("focuswindow", "address:client")
	rule := rules.Rule{
		Name:    "burst",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	eng := New(hypr, logger, []rules.Mode{mode}, false)
	key := mode.Name + ":" + rule.Name

	for i := 0; i < ruleBurstThreshold; i++ {
		if err := eng.reconcileAndApply(context.Background()); err != nil {
			t.Fatalf("reconcileAndApply call %d returned error: %v", i+1, err)
		}
		clearCooldown(t, eng, key)
	}

	if len(hypr.dispatched) != ruleBurstThreshold {
		t.Fatalf("expected %d dispatches, got %d", ruleBurstThreshold, len(hypr.dispatched))
	}
	if strings.Contains(logs.String(), "temporarily disabled") {
		t.Fatalf("unexpected temporary disable log: %s", logs.String())
	}
}

func TestRuleExecutionTrackingExceedsThreshold(t *testing.T) {
	hypr := &fakeHyprctl{
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	var plan layout.Plan
	plan.Add("focuswindow", "address:client")
	rule := rules.Rule{
		Name:    "burst",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	eng := New(hypr, logger, []rules.Mode{mode}, false)
	key := mode.Name + ":" + rule.Name

	for i := 0; i < ruleBurstThreshold; i++ {
		if err := eng.reconcileAndApply(context.Background()); err != nil {
			t.Fatalf("reconcileAndApply call %d returned error: %v", i+1, err)
		}
		clearCooldown(t, eng, key)
	}

	logs.Reset()
	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("final reconcileAndApply returned error: %v", err)
	}
	if !strings.Contains(logs.String(), "temporarily disabled") {
		t.Fatalf("expected temporary disable log, got %s", logs.String())
	}
	if len(hypr.dispatched) != ruleBurstThreshold {
		t.Fatalf("expected %d dispatches after throttling, got %d", ruleBurstThreshold, len(hypr.dispatched))
	}
	eng.mu.Lock()
	cooldownUntil := eng.cooldown[key]
	eng.mu.Unlock()
	if cooldownUntil.Before(time.Now()) {
		t.Fatalf("expected cooldown to be in the future, got %v", cooldownUntil)
	}
}
