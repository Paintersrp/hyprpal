package engine

import (
	"bytes"
	"context"
	"encoding/json"
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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)
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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)

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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)

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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)

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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)
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
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)
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

func TestTraceLoggingDryRunSequence(t *testing.T) {
	baseWorld := &state.World{
		Clients:             []state.Client{{Address: "addr-old", WorkspaceID: 1, MonitorName: "HDMI-A-1"}},
		Workspaces:          []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		Monitors:            []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
		ActiveWorkspaceID:   1,
		ActiveClientAddress: "addr-old",
	}
	hypr := &fakeHyprctl{
		clients: []state.Client{{Address: "addr-new", WorkspaceID: 2, MonitorName: "HDMI-A-1"}},
		workspaces: []state.Workspace{
			{ID: 1, Name: "1", MonitorName: "HDMI-A-1"},
			{ID: 2, Name: "2", MonitorName: "HDMI-A-1"},
		},
		monitors:        []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
		activeWorkspace: 2,
		activeClient:    "addr-new",
	}
	var plan layout.Plan
	plan.Add("focuswindow", "address:addr-new")
	plan.Add("movewindowpixel", "exact", "0", "0")
	rule := rules.Rule{
		Name:    "match",
		When:    func(rules.EvalContext) bool { return true },
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelTrace, &logs)
	eng := New(hypr, logger, []rules.Mode{mode}, true, false, layout.Gaps{}, 2, nil)
	eng.mu.Lock()
	eng.lastWorld = baseWorld
	eng.mu.Unlock()

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply returned error: %v", err)
	}

	if len(hypr.dispatched) != 0 {
		t.Fatalf("expected no dispatches in dry-run, got %d", len(hypr.dispatched))
	}

	traceLines := extractTraceLines(t, logs.String())
	expectedOrder := []string{"world.reconciled", "rule.matched", "plan.aggregated", "dispatch.result", "dispatch.result"}
	if len(traceLines) != len(expectedOrder) {
		t.Fatalf("expected %d trace lines, got %d: %v", len(expectedOrder), len(traceLines), traceLines)
	}
	for i, want := range expectedOrder {
		if !strings.Contains(traceLines[i], want) {
			t.Fatalf("expected trace line %d to contain %q, got %q", i, want, traceLines[i])
		}
	}

	event, payload := parseTraceLine(t, traceLines[0])
	if event != "world.reconciled" {
		t.Fatalf("expected first event to be world.reconciled, got %s", event)
	}
	delta := payloadValue(t, payload, "delta").(map[string]any)
	if !payloadBool(t, delta, "changed") {
		t.Fatalf("expected delta to indicate change, got %v", delta)
	}
	clientsAdded := payloadStrings(t, delta, "clientsAdded")
	if len(clientsAdded) != 1 || clientsAdded[0] != "addr-new" {
		t.Fatalf("expected clientsAdded to contain addr-new, got %v", clientsAdded)
	}
	clientsRemoved := payloadStrings(t, delta, "clientsRemoved")
	if len(clientsRemoved) != 1 || clientsRemoved[0] != "addr-old" {
		t.Fatalf("expected clientsRemoved to contain addr-old, got %v", clientsRemoved)
	}
	activeWS := payloadValue(t, delta, "activeWorkspace").(map[string]any)
	if intFromAny(t, activeWS["from"]) != 1 || intFromAny(t, activeWS["to"]) != 2 {
		t.Fatalf("unexpected activeWorkspace delta: %v", activeWS)
	}
	activeClient := payloadValue(t, delta, "activeClient").(map[string]any)
	if str, ok := activeClient["from"].(string); !ok || str != "addr-old" {
		t.Fatalf("unexpected activeClient.from: %v", activeClient)
	}
	if str, ok := activeClient["to"].(string); !ok || str != "addr-new" {
		t.Fatalf("unexpected activeClient.to: %v", activeClient)
	}

	_, rulePayload := parseTraceLine(t, traceLines[1])
	if payloadValue(t, rulePayload, "mode").(string) != "Focus" {
		t.Fatalf("expected rule mode Focus, got %v", rulePayload)
	}

	_, planPayload := parseTraceLine(t, traceLines[2])
	if intFromAny(t, planPayload["commandCount"]) != 2 {
		t.Fatalf("expected commandCount 2, got %v", planPayload)
	}

	for i := 3; i < len(traceLines); i++ {
		_, dispatchPayload := parseTraceLine(t, traceLines[i])
		if payloadValue(t, dispatchPayload, "status").(string) != "dry-run" {
			t.Fatalf("expected dry-run status, got %v", dispatchPayload)
		}
	}
}

func TestRedactTitlesToggle(t *testing.T) {
	const secretTitle = "Secret Document"
	hypr := &fakeHyprctl{
		clients: []state.Client{{
			Address:     "addr-1",
			Class:       "App",
			Title:       secretTitle,
			WorkspaceID: 1,
			MonitorName: "HDMI-A-1",
		}},
		workspaces: []state.Workspace{{ID: 1, Name: "1", MonitorName: "HDMI-A-1"}},
		monitors:   []state.Monitor{{ID: 1, Name: "HDMI-A-1", Rectangle: layout.Rect{Width: 1920, Height: 1080}}},
	}
	var plan layout.Plan
	plan.Add("focuswindow", "address:addr-1")
	rule := rules.Rule{
		Name: "title match",
		When: func(ctx rules.EvalContext) bool {
			for _, c := range ctx.World.Clients {
				if c.Title == secretTitle {
					return true
				}
			}
			return false
		},
		Actions: []rules.Action{stubAction{plan: plan}},
	}
	mode := rules.Mode{Name: "Focus", Rules: []rules.Rule{rule}}
	var logs bytes.Buffer
	logger := util.NewLoggerWithWriter(util.LevelInfo, &logs)
	eng := New(hypr, logger, []rules.Mode{mode}, false, false, layout.Gaps{}, 2, nil)

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply returned error: %v", err)
	}
	if len(hypr.dispatched) != 1 {
		t.Fatalf("expected one dispatch before redaction, got %d", len(hypr.dispatched))
	}
	world := eng.LastWorld()
	if world == nil || len(world.Clients) != 1 {
		t.Fatalf("expected last world with one client, got %+v", world)
	}
	if world.Clients[0].Title != secretTitle {
		t.Fatalf("expected stored title %q before redaction, got %q", secretTitle, world.Clients[0].Title)
	}

	key := mode.Name + ":" + rule.Name
	clearCooldown(t, eng, key)
	hypr.dispatched = nil

	eng.SetRedactTitles(true)

	if err := eng.reconcileAndApply(context.Background()); err != nil {
		t.Fatalf("reconcileAndApply with redaction returned error: %v", err)
	}
	if len(hypr.dispatched) != 1 {
		t.Fatalf("expected one dispatch after redaction, got %d", len(hypr.dispatched))
	}
	world = eng.LastWorld()
	if world == nil || len(world.Clients) != 1 {
		t.Fatalf("expected last world with one client after redaction, got %+v", world)
	}
	if world.Clients[0].Title != redactedTitle {
		t.Fatalf("expected redacted title %q, got %q", redactedTitle, world.Clients[0].Title)
	}
	if hypr.clients[0].Title != secretTitle {
		t.Fatalf("expected source client title to remain %q, got %q", secretTitle, hypr.clients[0].Title)
	}
}

func extractTraceLines(t *testing.T, logData string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(logData), "\n")
	var out []string
	for _, line := range lines {
		if strings.Contains(line, "[TRACE]") {
			out = append(out, line)
		}
	}
	return out
}

func parseTraceLine(t *testing.T, line string) (string, map[string]any) {
	t.Helper()
	idx := strings.Index(line, "[TRACE] ")
	if idx == -1 {
		t.Fatalf("line does not contain trace marker: %q", line)
	}
	rest := line[idx+len("[TRACE] "):]
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("unable to split trace line: %q", line)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v (line: %q)", err, line)
	}
	return parts[0], payload
}

func payloadValue(t *testing.T, payload map[string]any, key string) any {
	t.Helper()
	val, ok := payload[key]
	if !ok {
		t.Fatalf("expected key %q in payload %v", key, payload)
	}
	return val
}

func payloadBool(t *testing.T, payload map[string]any, key string) bool {
	t.Helper()
	val := payloadValue(t, payload, key)
	boolVal, ok := val.(bool)
	if !ok {
		t.Fatalf("expected bool for key %q, got %T", key, val)
	}
	return boolVal
}

func payloadStrings(t *testing.T, payload map[string]any, key string) []string {
	t.Helper()
	val := payloadValue(t, payload, key)
	arr, ok := val.([]any)
	if !ok {
		t.Fatalf("expected slice for key %q, got %T", key, val)
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		str, ok := item.(string)
		if !ok {
			t.Fatalf("expected string in array %q, got %T", key, item)
		}
		out = append(out, str)
	}
	return out
}

func intFromAny(t *testing.T, val any) int {
	t.Helper()
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("expected numeric type, got %T", val)
	}
	return 0
}
