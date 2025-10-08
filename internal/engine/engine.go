package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyprpal/hyprpal/internal/ipc"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type hyprctlClient interface {
	state.DataSource
	layout.Dispatcher
}

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct {
	*time.Ticker
}

func (t realTicker) C() <-chan time.Time {
	return t.Ticker.C
}

type subscribeFunc func(ctx context.Context, logger *util.Logger) (<-chan ipc.Event, error)

const defaultPeriodicReconcileInterval = 60 * time.Second

var errMonitorSnapshotRequired = errors.New("monitor geometry requires full snapshot")

// Engine ties together the world model, rules, and IPC.
type Engine struct {
	hyprctl hyprctlClient
	logger  *util.Logger

	modes          map[string]rules.Mode
	modeOrder      []string
	activeMode     string
	dryRun         bool
	redactTitles   bool
	explain        bool
	gaps           layout.Gaps
	tolerancePx    float64
	manualReserved map[string]layout.Insets

	mu         sync.Mutex
	debounce   map[string]time.Time
	cooldown   map[string]time.Time
	ruleState  map[string]*ruleExecutionState
	lastWorld  *state.World
	evalLog    *evaluationLog
	ruleChecks *ruleCheckHistory
	eventSeq   int

	tickerFactory func() ticker
	subscribe     subscribeFunc
}

const (
	ruleCheckHistoryLimit = 256
)

type plannedAction struct {
	Type string
	Plan layout.Plan
}

type plannedRule struct {
	Key       string
	Mode      string
	Name      string
	Priority  int
	Plan      layout.Plan
	Predicate *rules.PredicateTrace
	Actions   []plannedAction
}

type evaluationOrigin struct {
	Kind     string
	Payload  string
	Sequence int
}

// PlannedCommand represents a hyprctl dispatch that would be executed for the
// current world snapshot.
type PlannedCommand struct {
	Dispatch  []string
	Reason    string
	Action    string
	Predicate *rules.PredicateTrace
}

// RuleCheckRecord captures predicate evaluation outcomes for a single rule.
type RuleCheckRecord struct {
	Timestamp     time.Time             `json:"timestamp"`
	Mode          string                `json:"mode"`
	Rule          string                `json:"rule"`
	Matched       bool                  `json:"matched"`
	Reason        string                `json:"reason,omitempty"`
	Predicate     *rules.PredicateTrace `json:"predicate,omitempty"`
	EventKind     string                `json:"eventKind,omitempty"`
	EventPayload  string                `json:"eventPayload,omitempty"`
	EventSequence int                   `json:"eventSequence,omitempty"`
}

type ruleCheckHistory struct {
	buf      []RuleCheckRecord
	start    int
	count    int
	capacity int
}

type ruleExecutionState struct {
	recent         []time.Time
	total          int
	disabled       bool
	disabledSince  time.Time
	disabledReason string
}

func (s *ruleExecutionState) currentReason() string {
	if !s.disabled {
		return ""
	}
	if s.disabledReason != "" {
		return s.disabledReason
	}
	return "disabled"
}

func (s *ruleExecutionState) prune(throttle *rules.RuleThrottle, now time.Time) {
	if throttle == nil || len(s.recent) == 0 {
		return
	}
	maxWindow := throttle.MaxWindow()
	if maxWindow <= 0 {
		s.recent = nil
		return
	}
	cutoff := now.Add(-maxWindow)
	kept := s.recent[:0]
	for _, ts := range s.recent {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	s.recent = kept
}

// RuleStatus describes the current execution state for a compiled rule.
type RuleStatus struct {
	Mode             string
	Rule             string
	TotalExecutions  int
	RecentExecutions []time.Time
	Disabled         bool
	DisabledReason   string
	DisabledSince    time.Time
	Throttle         *rules.RuleThrottle
}

// New creates a new engine instance.
func New(hyprctl hyprctlClient, logger *util.Logger, modes []rules.Mode, dryRun bool, redactTitles bool, gaps layout.Gaps, tolerancePx float64, manualReserved map[string]layout.Insets) *Engine {
	modeMap := make(map[string]rules.Mode)
	order := make([]string, 0, len(modes))
	for _, m := range modes {
		normalized := rules.NormalizeMode(m)
		modeMap[normalized.Name] = normalized
		order = append(order, normalized.Name)
	}
	active := ""
	if len(order) > 0 {
		active = order[0]
	}
	return &Engine{
		hyprctl:        hyprctl,
		logger:         logger,
		modes:          modeMap,
		modeOrder:      order,
		activeMode:     active,
		dryRun:         dryRun,
		redactTitles:   redactTitles,
		explain:        false,
		gaps:           gaps,
		tolerancePx:    tolerancePx,
		manualReserved: cloneInsetsMap(manualReserved),
		debounce:       make(map[string]time.Time),
		cooldown:       make(map[string]time.Time),
		ruleState:      make(map[string]*ruleExecutionState),
		evalLog:        newEvaluationLog(0),
		ruleChecks:     newRuleCheckHistory(0),
		tickerFactory: func() ticker {
			return realTicker{time.NewTicker(defaultPeriodicReconcileInterval)}
		},
		subscribe: ipc.Subscribe,
	}
}

// ActiveMode returns the currently selected mode name.
func (e *Engine) ActiveMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeMode
}

// AvailableModes returns the ordered list of available modes.
func (e *Engine) AvailableModes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	modes := make([]string, len(e.modeOrder))
	copy(modes, e.modeOrder)
	return modes
}

// ReloadModes replaces the mode set while keeping the current selection when possible.
func (e *Engine) ReloadModes(modes []rules.Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	modeMap := make(map[string]rules.Mode)
	order := make([]string, 0, len(modes))
	for _, m := range modes {
		normalized := rules.NormalizeMode(m)
		modeMap[normalized.Name] = normalized
		order = append(order, normalized.Name)
	}
	e.modes = modeMap
	e.modeOrder = order
	if _, ok := modeMap[e.activeMode]; !ok && len(order) > 0 {
		e.activeMode = order[0]
	}
	e.debounce = make(map[string]time.Time)
	e.cooldown = make(map[string]time.Time)
	e.ruleState = make(map[string]*ruleExecutionState)
	if e.evalLog != nil {
		e.evalLog = newEvaluationLog(0)
	}
	if e.ruleChecks != nil {
		e.ruleChecks = newRuleCheckHistory(0)
	}
	e.logger.Infof("reloaded %d modes", len(order))
}

// SetMode selects the active mode if it exists.
func (e *Engine) SetMode(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.modes[name]; !ok {
		return fmt.Errorf("unknown mode %q", name)
	}
	e.activeMode = name
	e.logger.Infof("switched to mode %s", name)
	return nil
}

// SetRedactTitles toggles client title redaction at runtime.
func (e *Engine) SetRedactTitles(enabled bool) {
	e.mu.Lock()
	e.redactTitles = enabled
	e.mu.Unlock()
}

// SetLayoutParameters updates the gap and tolerance configuration.
func (e *Engine) SetLayoutParameters(gaps layout.Gaps, tolerance float64) {
	e.mu.Lock()
	e.gaps = gaps
	e.tolerancePx = tolerance
	e.mu.Unlock()
}

// SetManualReserved replaces the manual monitor insets map.
func (e *Engine) SetManualReserved(manual map[string]layout.Insets) {
	e.mu.Lock()
	e.manualReserved = cloneInsetsMap(manual)
	e.mu.Unlock()
}

// SetExplain toggles detailed rule-match logging after each apply.
func (e *Engine) SetExplain(enabled bool) {
	e.mu.Lock()
	e.explain = enabled
	e.mu.Unlock()
}

func (e *Engine) redactTitlesEnabled() bool {
	e.mu.Lock()
	enabled := e.redactTitles
	e.mu.Unlock()
	return enabled
}

func (e *Engine) explainEnabled() bool {
	e.mu.Lock()
	enabled := e.explain
	e.mu.Unlock()
	return enabled
}

func (e *Engine) flushRuleCheckLogs() {
	if !e.explainEnabled() {
		return
	}
	records := e.RuleCheckHistory()
	if len(records) == 0 {
		return
	}
	defer e.ClearRuleCheckHistory()
	currentSeq := -1
	currentKind := ""
	currentPayload := ""
	loggedHeader := false

	logHeader := func(kind, payload string) {
		if kind != "" {
			if payload != "" {
				e.logger.Infof("explain: event %s %s", kind, payload)
			} else {
				e.logger.Infof("explain: event %s", kind)
			}
		} else {
			e.logger.Infof("explain: evaluation matched (no triggering event)")
		}
	}

	for _, record := range records {
		if record.Reason != "matched" {
			continue
		}
		seq := record.EventSequence
		kind := record.EventKind
		payload := record.EventPayload
		if !loggedHeader || seq != currentSeq || kind != currentKind || payload != currentPayload {
			currentSeq = seq
			currentKind = kind
			currentPayload = payload
			loggedHeader = true
			logHeader(kind, payload)
		}
		timestamp := record.Timestamp.Format(time.RFC3339Nano)
		if record.Predicate == nil {
			e.logger.Infof("  %s:%s matched at %s (predicate trace unavailable)", record.Mode, record.Rule, timestamp)
			continue
		}
		e.logger.Infof("  %s:%s matched at %s", record.Mode, record.Rule, timestamp)
		for _, line := range rules.SummarizePredicateTrace(record.Predicate) {
			e.logger.Infof("    %s", line)
		}
	}
}

// Run starts the engine loop until context cancellation.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.reconcileAndApply(ctx); err != nil {
		return err
	}
	e.flushRuleCheckLogs()
	tick := e.newTicker()
	defer tick.Stop()

	events, err := e.subscribeEvents(ctx)
	if err != nil {
		tick.Stop()
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			e.logger.Infof("periodic reconcile tick")
			if err := e.reconcileAndApply(ctx); err != nil {
				if ctx.Err() != nil {
					e.logger.Debugf("periodic reconcile aborted: %v", err)
				} else {
					e.logger.Errorf("periodic reconcile failed: %v", err)
				}
				continue
			}
			e.flushRuleCheckLogs()
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("event stream closed")
			}
			payload := ev.Payload
			if e.redactTitlesEnabled() && payload != "" {
				payload = redactedTitle
			}
			e.trace("event.received", map[string]any{
				"kind":    ev.Kind,
				"payload": payload,
			})
			if e.isInteresting(ev.Kind) {
				if err := e.applyEvent(ctx, ev); err != nil {
					e.logger.Errorf("incremental apply failed: %v", err)
				} else {
					e.flushRuleCheckLogs()
				}
			}
		}
	}
}

// ApplyEvent processes a single Hyprland event against the cached world state.
// It performs incremental updates when possible and falls back to a full
// reconcile when the event cannot be handled incrementally.
func (e *Engine) ApplyEvent(ctx context.Context, ev ipc.Event) error {
	return e.applyEvent(ctx, ev)
}

func (e *Engine) newTicker() ticker {
	if e.tickerFactory != nil {
		return e.tickerFactory()
	}
	return realTicker{time.NewTicker(defaultPeriodicReconcileInterval)}
}

func (e *Engine) subscribeEvents(ctx context.Context) (<-chan ipc.Event, error) {
	if e.subscribe != nil {
		return e.subscribe(ctx, e.logger)
	}
	return ipc.Subscribe(ctx, e.logger)
}

// Reconcile triggers a manual world refresh and rule evaluation.
func (e *Engine) Reconcile(ctx context.Context) error {
	if err := e.reconcileAndApply(ctx); err != nil {
		return err
	}
	e.flushRuleCheckLogs()
	return nil
}

// reconcileAndApply refreshes the world model and evaluates rules.
func (e *Engine) reconcileAndApply(ctx context.Context) error {
	world, err := state.NewWorld(ctx, e.hyprctl)
	if err != nil {
		return err
	}
	e.mu.Lock()
	prev := e.lastWorld
	e.lastWorld = world
	e.mu.Unlock()

	counts := map[string]int{
		"clients":    len(world.Clients),
		"workspaces": len(world.Workspaces),
		"monitors":   len(world.Monitors),
	}
	e.trace("world.reconciled", map[string]any{
		"counts":          counts,
		"activeWorkspace": world.ActiveWorkspaceID,
		"activeClient":    world.ActiveClientAddress,
		"delta":           worldDelta(prev, world),
	})
	return e.evaluateAndApply(world, time.Now(), true, nil)
}

func (e *Engine) evaluateAndApply(world *state.World, now time.Time, log bool, origin *evaluationOrigin) error {
	if origin != nil && origin.Sequence == 0 {
		origin.Sequence = e.nextEventSequence()
	}
	plan, rules := e.evaluate(world, now, log, origin)
	e.trace("plan.aggregated", map[string]any{
		"commandCount": len(plan.Commands),
		"commands":     plan.Commands,
	})
	if len(plan.Commands) == 0 {
		return nil
	}

	e.markDebounce(rules, now)

	redact := e.redactTitlesEnabled()

	if e.dryRun {
		for _, cmd := range plan.Commands {
			command := loggableCommand(cmd, redact)
			e.trace("dispatch.result", map[string]any{
				"status":  "dry-run",
				"command": command,
			})
		}
		e.applyCooldown(rules, now.Add(1*time.Second))
		e.recordRuleEvaluations(rules, now, RuleEvaluationStatusDryRun, nil)
		return nil
	}
	if err := plan.Execute(e.hyprctl); err != nil {
		for _, cmd := range plan.Commands {
			command := loggableCommand(cmd, redact)
			e.trace("dispatch.result", map[string]any{
				"status":  "error",
				"command": command,
				"error":   err.Error(),
			})
		}
		e.recordRuleEvaluations(rules, now, RuleEvaluationStatusError, err)
		return err
	}
	e.applyCooldown(rules, now.Add(1*time.Second))
	for _, cmd := range plan.Commands {
		command := loggableCommand(cmd, redact)
		e.trace("dispatch.result", map[string]any{
			"status":  "applied",
			"command": command,
		})
		if redact {
			e.logger.Infof("dispatched: %s", redactedTitle)
		} else {
			e.logger.Infof("dispatched: %v", cmd)
		}
	}
	e.recordRuleEvaluations(rules, now, RuleEvaluationStatusApplied, nil)
	return nil
}

func (e *Engine) applyEvent(ctx context.Context, ev ipc.Event) error {
	e.mu.Lock()
	if e.lastWorld == nil {
		e.mu.Unlock()
		return e.reconcileAndApply(ctx)
	}
	prev := state.CloneWorld(e.lastWorld)
	mutated, err := e.mutateWorldLocked(e.lastWorld, ev)
	world := e.lastWorld
	var delta map[string]any
	if mutated {
		delta = worldDelta(prev, world)
	}
	e.mu.Unlock()
	if err != nil {
		if errors.Is(err, errMonitorSnapshotRequired) {
			e.logger.Warnf("incremental update fallback for %s: monitor geometry requires full snapshot", ev.Kind)
		} else {
			e.logger.Warnf("incremental update fallback for %s: %v", ev.Kind, err)
		}
		return e.reconcileAndApply(ctx)
	}
	if !mutated {
		return nil
	}
	e.trace("world.incremental", map[string]any{
		"event": ev.Kind,
		"delta": delta,
	})
	payload := ev.Payload
	if e.redactTitlesEnabled() && payload != "" {
		payload = redactedTitle
	}
	origin := &evaluationOrigin{Kind: ev.Kind, Payload: payload}
	return e.evaluateAndApply(world, time.Now(), true, origin)
}

func (e *Engine) mutateWorldLocked(world *state.World, ev ipc.Event) (bool, error) {
	switch ev.Kind {
	case "openwindow":
		client, workspaceName, err := parseOpenWindowPayload(ev.Payload)
		if err != nil {
			return false, err
		}
		if client.Address == "" {
			return false, fmt.Errorf("openwindow missing address")
		}
		if client.WorkspaceID != 0 {
			ws := world.WorkspaceByID(client.WorkspaceID)
			if ws == nil {
				return false, fmt.Errorf("workspace %d not found", client.WorkspaceID)
			}
			if client.MonitorName == "" {
				client.MonitorName = ws.MonitorName
			}
		} else if workspaceName != "" {
			ws := world.WorkspaceByName(workspaceName)
			if ws == nil {
				return false, fmt.Errorf("workspace %q not found", workspaceName)
			}
			client.WorkspaceID = ws.ID
			if client.MonitorName == "" {
				client.MonitorName = ws.MonitorName
			}
		}
		return world.UpsertClient(client)
	case "closewindow":
		address := strings.TrimSpace(ev.Payload)
		if address == "" {
			return false, fmt.Errorf("closewindow missing address")
		}
		_, err := world.RemoveClient(address)
		if err != nil {
			return false, err
		}
		return true, nil
	case "activewindow":
		address := strings.TrimSpace(ev.Payload)
		if address == "" || strings.EqualFold(address, "0x0") {
			changed := world.SetActiveClient("")
			return changed, nil
		}
		changed := world.SetActiveClient(address)
		return changed, nil
	case "workspace":
		id, monitorName, workspaceName, err := parseWorkspacePayload(ev.Payload)
		if err != nil {
			return false, err
		}
		if id == 0 && workspaceName != "" {
			ws := world.WorkspaceByName(workspaceName)
			if ws == nil {
				return false, fmt.Errorf("workspace %q not found", workspaceName)
			}
			id = ws.ID
		}
		changed, err := world.SetActiveWorkspace(id)
		if err != nil {
			return false, err
		}
		if monitorName != "" {
			bound, bindErr := world.BindWorkspaceToMonitor(id, monitorName)
			if bindErr != nil {
				return false, bindErr
			}
			if bound {
				changed = true
			}
		}
		return changed, nil
	case "movewindow":
		address, workspaceID, workspaceName, err := parseMoveWindowPayload(ev.Payload)
		if err != nil {
			return false, err
		}
		if workspaceID == 0 && workspaceName != "" {
			ws := world.WorkspaceByName(workspaceName)
			if ws == nil {
				return false, fmt.Errorf("workspace %q not found", workspaceName)
			}
			workspaceID = ws.ID
		}
		ws := world.WorkspaceByID(workspaceID)
		if ws == nil {
			return false, fmt.Errorf("workspace %d not found", workspaceID)
		}
		return world.MoveClient(address, workspaceID, ws.MonitorName)
	case "monitoradded":
		_, monitorName, err := parseMonitorPayload(ev.Payload)
		if err != nil {
			return false, err
		}
		if monitorName == "" {
			return false, fmt.Errorf("monitoradded missing name")
		}
		return false, fmt.Errorf("%w: %s", errMonitorSnapshotRequired, monitorName)
	case "monitorremoved":
		_, monitorName, err := parseMonitorPayload(ev.Payload)
		if err != nil {
			return false, err
		}
		if monitorName == "" {
			return false, fmt.Errorf("monitorremoved missing name")
		}
		changed, removeErr := world.RemoveMonitor(monitorName)
		if removeErr != nil {
			return false, removeErr
		}
		return changed, nil
	case "windowtitle":
		address, title, err := parseWindowTitlePayload(ev.Payload)
		if err != nil {
			return false, err
		}
		changed, setErr := world.SetClientTitle(address, title)
		if setErr != nil {
			return false, setErr
		}
		return changed, nil
	default:
		return false, nil
	}
}

func parseOpenWindowPayload(payload string) (state.Client, string, error) {
	parts := splitPayload(payload, 4)
	if len(parts) < 3 {
		return state.Client{}, "", fmt.Errorf("invalid openwindow payload %q", payload)
	}
	if parts[1] == "" {
		return state.Client{}, "", fmt.Errorf("openwindow missing workspace")
	}
	client := state.Client{
		Address: parts[0],
		Class:   parts[2],
	}
	var workspaceName string
	if parts[1] != "" {
		if workspaceID, err := strconv.Atoi(parts[1]); err == nil {
			client.WorkspaceID = workspaceID
		} else {
			workspaceName = parts[1]
		}
	}
	if len(parts) == 4 {
		client.Title = parts[3]
	}
	return client, workspaceName, nil
}

func parseWindowTitlePayload(payload string) (string, string, error) {
	parts := splitPayload(payload, 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid windowtitle payload %q", payload)
	}
	address := parts[0]
	if address == "" {
		return "", "", fmt.Errorf("windowtitle missing address")
	}
	return address, parts[1], nil
}

func parseMoveWindowPayload(payload string) (string, int, string, error) {
	parts := splitPayload(payload, 2)
	if len(parts) != 2 {
		return "", 0, "", fmt.Errorf("invalid movewindow payload %q", payload)
	}
	if parts[1] == "" {
		return "", 0, "", fmt.Errorf("movewindow missing workspace")
	}
	if parts[1] != "" {
		if workspaceID, err := strconv.Atoi(parts[1]); err == nil {
			return parts[0], workspaceID, "", nil
		}
	}
	return parts[0], 0, parts[1], nil
}

func parseWorkspacePayload(payload string) (int, string, string, error) {
	parts := splitPayload(payload, 2)
	if len(parts) == 0 {
		return 0, "", "", fmt.Errorf("invalid workspace payload %q", payload)
	}
	if len(parts) == 1 {
		if parts[0] == "" {
			return 0, "", "", fmt.Errorf("workspace missing identifier")
		}
		if parts[0] != "" {
			if id, err := strconv.Atoi(parts[0]); err == nil {
				return id, "", "", nil
			}
		}
		return 0, "", parts[0], nil
	}
	if parts[1] == "" {
		return 0, parts[0], "", fmt.Errorf("workspace missing identifier")
	}
	if id, err := strconv.Atoi(parts[1]); err == nil {
		return id, parts[0], "", nil
	}
	return 0, parts[0], parts[1], nil
}

func parseMonitorPayload(payload string) (int, string, error) {
	parts := splitPayload(payload, 2)
	if len(parts) == 0 {
		return 0, "", fmt.Errorf("invalid monitor payload %q", payload)
	}
	if len(parts) == 1 {
		return 0, parts[0], nil
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid monitor id %q: %w", parts[0], err)
	}
	return id, parts[1], nil
}

func splitPayload(payload string, maxParts int) []string {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil
	}
	parts := strings.SplitN(trimmed, ",", maxParts)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// PreviewPlan evaluates the current world and returns the pending plan without
// dispatching commands.
func (e *Engine) PreviewPlan(ctx context.Context, explain bool) ([]PlannedCommand, error) {
	world, err := state.NewWorld(ctx, e.hyprctl)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.lastWorld = world
	e.mu.Unlock()

	_, plannedRules := e.evaluate(world, time.Now(), false, nil)
	commands := make([]PlannedCommand, 0)
	for _, pr := range plannedRules {
		reason := ""
		if explain {
			reason = fmt.Sprintf("%s:%s", pr.Mode, pr.Name)
		}
		if len(pr.Actions) == 0 {
			for _, cmd := range pr.Plan.Commands {
				dispatch := append([]string(nil), cmd...)
				commands = append(commands, PlannedCommand{
					Dispatch:  dispatch,
					Reason:    reason,
					Predicate: rules.ClonePredicateTrace(pr.Predicate),
				})
			}
			continue
		}
		for _, action := range pr.Actions {
			actionID := ""
			if explain {
				actionID = action.Type
			}
			for _, cmd := range action.Plan.Commands {
				dispatch := append([]string(nil), cmd...)
				commands = append(commands, PlannedCommand{
					Dispatch:  dispatch,
					Reason:    reason,
					Action:    actionID,
					Predicate: rules.ClonePredicateTrace(pr.Predicate),
				})
			}
		}
	}
	return commands, nil
}

// LastWorld returns the most recent world snapshot.
func (e *Engine) LastWorld() *state.World {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastWorld == nil {
		return nil
	}
	snapshot := state.CloneWorld(e.lastWorld)
	if e.redactTitles {
		redactWorldTitles(snapshot)
	}
	return snapshot
}

// RuleEvaluationHistory returns the recent rule evaluation records.
func (e *Engine) RuleEvaluationHistory() []RuleEvaluation {
	if e.evalLog == nil {
		return nil
	}
	return e.evalLog.snapshot()
}

// RuleCheckHistory returns the buffered rule evaluation checks.
func (e *Engine) RuleCheckHistory() []RuleCheckRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ruleChecks == nil {
		return nil
	}
	return e.ruleChecks.snapshot()
}

// ClearRuleCheckHistory removes any buffered rule evaluation records.
func (e *Engine) ClearRuleCheckHistory() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ruleChecks == nil {
		return
	}
	e.ruleChecks.clear()
}

func (e *Engine) evaluate(world *state.World, now time.Time, log bool, origin *evaluationOrigin) (layout.Plan, []plannedRule) {
	e.mu.Lock()
	activeMode := e.activeMode
	mode, ok := e.modes[activeMode]
	gaps := e.gaps
	tolerance := e.tolerancePx
	e.mu.Unlock()
	if !ok {
		if log {
			e.logger.Warnf("no active mode selected; skipping apply")
		}
		return layout.Plan{}, nil
	}

	var plan layout.Plan
	planned := make([]plannedRule, 0, len(mode.Rules))
	monitorInsets := e.combineMonitorInsets(world.Monitors)
	evalCtx := rules.EvalContext{Mode: activeMode, World: world}

	groups := mode.PriorityGroups
	if len(groups) == 0 && len(mode.Rules) > 0 {
		normalized := rules.NormalizeMode(mode)
		groups = normalized.PriorityGroups
		mode = normalized
		e.mu.Lock()
		if _, exists := e.modes[activeMode]; exists {
			e.modes[activeMode] = normalized
		}
		e.mu.Unlock()
	}

	for _, group := range groups {
		groupMutated := false
		for _, rule := range group.Rules {
			key := activeMode + ":" + rule.Name
			e.mu.Lock()
			last := e.debounce[key]
			cooldownUntil := e.cooldown[key]
			disabledReason := ""
			if state, ok := e.ruleState[key]; ok && state.disabled {
				disabledReason = state.currentReason()
			}
			e.mu.Unlock()
			matched := true
			predicateTrace := &rules.PredicateTrace{Kind: "predicate", Result: true}
			if rule.Tracer != nil {
				matched, predicateTrace = rule.Tracer.Trace(evalCtx)
			} else {
				matched = rule.When(evalCtx)
				predicateTrace = &rules.PredicateTrace{Kind: "predicate", Result: matched}
			}
			record := RuleCheckRecord{
				Timestamp: now,
				Mode:      activeMode,
				Rule:      rule.Name,
				Matched:   matched,
				Predicate: predicateTrace,
			}
			if origin != nil {
				record.EventKind = origin.Kind
				record.EventPayload = origin.Payload
				record.EventSequence = origin.Sequence
			}
			if disabledReason != "" {
				if log {
					e.logger.Infof("rule %s skipped (%s) [mode %s]", rule.Name, disabledReason, activeMode)
				}
				record.Reason = disabledReason
				e.recordRuleCheck(record)
				continue
			}
			if cooldownUntil.After(now) {
				if log {
					e.logger.Infof("rule %s skipped (cooldown) [mode %s]", rule.Name, activeMode)
				}
				record.Reason = "cooldown"
				e.recordRuleCheck(record)
				continue
			}
			if !last.IsZero() && now.Sub(last) < rule.Debounce {
				if log {
					e.logger.Infof("rule %s skipped (debounced) [mode %s]", rule.Name, activeMode)
				}
				record.Reason = "debounced"
				e.recordRuleCheck(record)
				continue
			}
			if !matched {
				record.Reason = "predicate"
				e.recordRuleCheck(record)
				continue
			}
			rulePlan := layout.Plan{}
			plannedActions := make([]plannedAction, 0, len(rule.Actions))
			for _, action := range rule.Actions {
				p, err := action.Impl.Plan(rules.ActionContext{
					World:             world,
					Logger:            e.logger,
					RuleName:          rule.Name,
					ManagedWorkspaces: rule.ManagedWorkspaces,
					MutateUnmanaged:   rule.MutateUnmanaged,
					Gaps:              gaps,
					TolerancePx:       tolerance,
					MonitorReserved:   monitorInsets,
				})
				if err != nil {
					e.logger.Errorf("rule %s action error: %v", rule.Name, err)
					continue
				}
				rulePlan.Merge(p)
				if len(p.Commands) > 0 {
					plannedActions = append(plannedActions, plannedAction{
						Type: action.Type,
						Plan: clonePlan(p),
					})
				}
			}
			if len(rulePlan.Commands) == 0 {
				record.Reason = "no-commands"
				e.recordRuleCheck(record)
				continue
			}
			allowed, reason, disabledNow, window := e.recordRuleExecution(key, now, rule.Throttle)
			if !allowed {
				if reason == "" {
					reason = "disabled"
				}
				if log {
					if disabledNow && rule.Throttle != nil {
						limit, windowDur := throttleWindowSummary(rule.Throttle, window)
						if limit > 0 && windowDur > 0 {
							e.logger.Warnf("rule %s disabled after exceeding throttle limit (%d within %s) [mode %s]", rule.Name, limit, windowDur, activeMode)
						} else {
							e.logger.Warnf("rule %s disabled after exceeding throttle limit [mode %s]", rule.Name, activeMode)
						}
					} else {
						e.logger.Infof("rule %s skipped (%s) [mode %s]", rule.Name, reason, activeMode)
					}
				}
				record.Reason = reason
				e.recordRuleCheck(record)
				continue
			}
			plan.Merge(rulePlan)
			groupMutated = true
			if log {
				e.trace("rule.matched", map[string]any{
					"mode":     activeMode,
					"rule":     rule.Name,
					"priority": rule.Priority,
					"commands": rulePlan.Commands,
				})
			}
			planned = append(planned, plannedRule{
				Key:       key,
				Mode:      activeMode,
				Name:      rule.Name,
				Priority:  rule.Priority,
				Plan:      rulePlan,
				Predicate: rules.ClonePredicateTrace(predicateTrace),
				Actions:   plannedActions,
			})
			record.Reason = "matched"
			e.recordRuleCheck(record)
		}
		if groupMutated {
			if log {
				e.trace("rules.priority-stop", map[string]any{
					"mode":     activeMode,
					"priority": group.Priority,
				})
			}
			break
		}
	}
	return plan, planned
}

func (e *Engine) combineMonitorInsets(monitors []state.Monitor) map[string]layout.Insets {
	result := make(map[string]layout.Insets, len(monitors))
	e.mu.Lock()
	manual := cloneInsetsMap(e.manualReserved)
	e.mu.Unlock()
	wildcard, wildcardOK := manual["*"]
	emptyWildcard, emptyOK := manual[""]
	for _, mon := range monitors {
		insets := mon.Reserved
		if manual != nil {
			if override, ok := manual[mon.Name]; ok {
				insets = override
			} else if wildcardOK {
				insets = wildcard
			} else if emptyOK {
				insets = emptyWildcard
			}
		}
		result[mon.Name] = insets
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func clonePlan(plan layout.Plan) layout.Plan {
	if len(plan.Commands) == 0 {
		return layout.Plan{}
	}
	cloned := make([][]string, len(plan.Commands))
	for i, cmd := range plan.Commands {
		cloned[i] = append([]string(nil), cmd...)
	}
	return layout.Plan{Commands: cloned}
}

func newRuleCheckHistory(limit int) *ruleCheckHistory {
	if limit <= 0 {
		limit = ruleCheckHistoryLimit
	}
	return &ruleCheckHistory{
		buf:      make([]RuleCheckRecord, limit),
		capacity: limit,
	}
}

func (h *ruleCheckHistory) add(record RuleCheckRecord) {
	if h == nil || h.capacity == 0 {
		return
	}
	rec := cloneRuleCheckRecord(record)
	if h.count < h.capacity {
		idx := (h.start + h.count) % h.capacity
		h.buf[idx] = rec
		h.count++
		return
	}
	h.buf[h.start] = rec
	h.start = (h.start + 1) % h.capacity
}

func (h *ruleCheckHistory) snapshot() []RuleCheckRecord {
	if h == nil || h.count == 0 {
		return nil
	}
	out := make([]RuleCheckRecord, h.count)
	for i := 0; i < h.count; i++ {
		idx := (h.start + i) % h.capacity
		out[i] = cloneRuleCheckRecord(h.buf[idx])
	}
	return out
}

func (h *ruleCheckHistory) clear() {
	if h == nil {
		return
	}
	h.start = 0
	h.count = 0
}

func cloneRuleCheckRecord(record RuleCheckRecord) RuleCheckRecord {
	cloned := RuleCheckRecord{
		Timestamp:     record.Timestamp,
		Mode:          record.Mode,
		Rule:          record.Rule,
		Matched:       record.Matched,
		Reason:        record.Reason,
		EventKind:     record.EventKind,
		EventPayload:  record.EventPayload,
		EventSequence: record.EventSequence,
	}
	if record.Predicate != nil {
		cloned.Predicate = rules.ClonePredicateTrace(record.Predicate)
	}
	return cloned
}

func cloneInsetsMap(src map[string]layout.Insets) map[string]layout.Insets {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]layout.Insets, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (e *Engine) recordRuleCheck(record RuleCheckRecord) {
	if e.ruleChecks == nil {
		return
	}
	e.mu.Lock()
	e.ruleChecks.add(record)
	e.mu.Unlock()
}

func (e *Engine) nextEventSequence() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventSeq++
	return e.eventSeq
}

func (e *Engine) getRuleStateLocked(key string) *ruleExecutionState {
	if state, ok := e.ruleState[key]; ok {
		return state
	}
	state := &ruleExecutionState{}
	e.ruleState[key] = state
	return state
}

func (e *Engine) recordRuleExecution(ruleKey string, when time.Time, throttle *rules.RuleThrottle) (bool, string, bool, *rules.RuleThrottleWindow) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.getRuleStateLocked(ruleKey)
	if state.disabled {
		return false, state.currentReason(), false, nil
	}
	if throttle != nil {
		state.prune(throttle, when)
		if window := exceededThrottleWindow(throttle, state.recent, when); window != nil {
			state.disabled = true
			state.disabledSince = when
			reason := throttleDisabledReason(throttle, window)
			state.disabledReason = reason
			return false, reason, true, window
		}
	}
	state.total++
	if throttle != nil {
		state.recent = append(state.recent, when)
	}
	return true, "", false, nil
}

func throttleDisabledReason(throttle *rules.RuleThrottle, window *rules.RuleThrottleWindow) string {
	limit, windowDur := throttleWindowSummary(throttle, window)
	if limit > 0 && windowDur > 0 {
		return fmt.Sprintf("disabled (throttle: %d in %s)", limit, windowDur)
	}
	return "disabled (throttle)"
}

func throttleWindowSummary(throttle *rules.RuleThrottle, window *rules.RuleThrottleWindow) (int, time.Duration) {
	if window != nil && window.FiringLimit > 0 && window.Window > 0 {
		return window.FiringLimit, window.Window
	}
	if throttle != nil {
		for i := range throttle.Windows {
			candidate := throttle.Windows[i]
			if candidate.FiringLimit > 0 && candidate.Window > 0 {
				return candidate.FiringLimit, candidate.Window
			}
		}
	}
	return 0, 0
}

func exceededThrottleWindow(throttle *rules.RuleThrottle, recent []time.Time, now time.Time) *rules.RuleThrottleWindow {
	if throttle == nil || len(throttle.Windows) == 0 {
		return nil
	}
	for i := range throttle.Windows {
		window := throttle.Windows[i]
		if window.FiringLimit <= 0 || window.Window <= 0 {
			continue
		}
		cutoff := now.Add(-window.Window)
		count := 0
		for j := len(recent) - 1; j >= 0; j-- {
			if recent[j].Before(cutoff) {
				break
			}
			count++
		}
		if count >= window.FiringLimit {
			return &throttle.Windows[i]
		}
	}
	return nil
}

// RulesStatus reports the per-rule execution state including throttle counters.
func (e *Engine) RulesStatus() []RuleStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	statuses := make([]RuleStatus, 0)
	for _, modeName := range e.modeOrder {
		mode, ok := e.modes[modeName]
		if !ok {
			continue
		}
		for _, rule := range mode.Rules {
			key := modeName + ":" + rule.Name
			state := e.getRuleStateLocked(key)
			state.prune(rule.Throttle, now)
			status := RuleStatus{
				Mode:            modeName,
				Rule:            rule.Name,
				TotalExecutions: state.total,
				Disabled:        state.disabled,
				DisabledReason:  state.currentReason(),
				DisabledSince:   state.disabledSince,
			}
			if rule.Throttle != nil {
				status.Throttle = rule.Throttle.Clone()
			}
			if rule.Throttle != nil && len(state.recent) > 0 {
				status.RecentExecutions = append([]time.Time(nil), state.recent...)
			}
			statuses = append(statuses, status)
		}
	}
	return statuses
}

// EnableRule clears throttle state and reenables the provided rule.
func (e *Engine) EnableRule(modeName, ruleName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	mode, ok := e.modes[modeName]
	if !ok {
		return fmt.Errorf("unknown mode %q", modeName)
	}
	found := false
	for _, rule := range mode.Rules {
		if rule.Name == ruleName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown rule %q in mode %q", ruleName, modeName)
	}
	key := modeName + ":" + ruleName
	delete(e.debounce, key)
	delete(e.cooldown, key)
	state := e.getRuleStateLocked(key)
	state.recent = nil
	state.total = 0
	state.disabled = false
	state.disabledSince = time.Time{}
	state.disabledReason = ""
	if e.logger != nil {
		e.logger.Infof("rule %s manually re-enabled [mode %s]", ruleName, modeName)
	}
	return nil
}

func (e *Engine) markDebounce(rules []plannedRule, when time.Time) {
	if len(rules) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, pr := range rules {
		e.debounce[pr.Key] = when
	}
}

func (e *Engine) applyCooldown(rules []plannedRule, until time.Time) {
	if len(rules) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, pr := range rules {
		e.cooldown[pr.Key] = until
	}
}

func (e *Engine) recordRuleEvaluations(rules []plannedRule, when time.Time, status RuleEvaluationStatus, err error) {
	if len(rules) == 0 {
		return
	}
	if e.evalLog == nil {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	for _, pr := range rules {
		entry := RuleEvaluation{
			Timestamp: when,
			Mode:      pr.Mode,
			Rule:      pr.Name,
			Status:    status,
			Commands:  cloneCommands(pr.Plan.Commands),
			Error:     message,
		}
		e.evalLog.record(entry)
	}
}

func (e *Engine) isInteresting(kind string) bool {
	switch kind {
	case "openwindow", "closewindow", "activewindow", "workspace", "movewindow", "monitorremoved", "monitoradded", "windowtitle":
		return true
	default:
		return false
	}
}

func (e *Engine) trace(event string, fields map[string]any) {
	if e.logger == nil {
		return
	}
	e.logger.Tracef("%s %s", event, formatTraceFields(fields))
}

const redactedTitle = "[redacted]"

func redactWorldTitles(world *state.World) {
	if world == nil {
		return
	}
	for i := range world.Clients {
		if world.Clients[i].Title != "" {
			world.Clients[i].Title = redactedTitle
		}
	}
}

func loggableCommand(cmd []string, redact bool) any {
	if !redact {
		return cmd
	}
	return redactedTitle
}

func formatTraceFields(fields map[string]any) string {
	if len(fields) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(k))
		b.WriteByte(':')
		val, err := json.Marshal(fields[k])
		if err != nil {
			b.WriteString(strconv.Quote(fmt.Sprintf("<marshal error: %v>", err)))
			continue
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.String()
}

func worldDelta(prev, curr *state.World) map[string]any {
	if curr == nil {
		return map[string]any{"changed": false}
	}
	if prev == nil {
		return map[string]any{
			"initial": true,
			"changed": true,
		}
	}
	delta := make(map[string]any)
	changed := false

	prevClients := make(map[string]struct{}, len(prev.Clients))
	for _, c := range prev.Clients {
		prevClients[c.Address] = struct{}{}
	}
	currClients := make(map[string]struct{}, len(curr.Clients))
	for _, c := range curr.Clients {
		currClients[c.Address] = struct{}{}
	}
	var addedClients, removedClients []string
	for addr := range currClients {
		if _, ok := prevClients[addr]; !ok {
			addedClients = append(addedClients, addr)
		}
	}
	for addr := range prevClients {
		if _, ok := currClients[addr]; !ok {
			removedClients = append(removedClients, addr)
		}
	}
	sort.Strings(addedClients)
	sort.Strings(removedClients)
	if len(addedClients) > 0 {
		delta["clientsAdded"] = addedClients
		changed = true
	}
	if len(removedClients) > 0 {
		delta["clientsRemoved"] = removedClients
		changed = true
	}

	if prev.ActiveWorkspaceID != curr.ActiveWorkspaceID {
		delta["activeWorkspace"] = map[string]int{
			"from": prev.ActiveWorkspaceID,
			"to":   curr.ActiveWorkspaceID,
		}
		changed = true
	}
	if prev.ActiveClientAddress != curr.ActiveClientAddress {
		delta["activeClient"] = map[string]string{
			"from": prev.ActiveClientAddress,
			"to":   curr.ActiveClientAddress,
		}
		changed = true
	}

	prevWorkspaces := make(map[int]struct{}, len(prev.Workspaces))
	for _, ws := range prev.Workspaces {
		prevWorkspaces[ws.ID] = struct{}{}
	}
	currWorkspaces := make(map[int]struct{}, len(curr.Workspaces))
	for _, ws := range curr.Workspaces {
		currWorkspaces[ws.ID] = struct{}{}
	}
	var addedWorkspaces, removedWorkspaces []int
	for id := range currWorkspaces {
		if _, ok := prevWorkspaces[id]; !ok {
			addedWorkspaces = append(addedWorkspaces, id)
		}
	}
	for id := range prevWorkspaces {
		if _, ok := currWorkspaces[id]; !ok {
			removedWorkspaces = append(removedWorkspaces, id)
		}
	}
	sort.Ints(addedWorkspaces)
	sort.Ints(removedWorkspaces)
	if len(addedWorkspaces) > 0 {
		delta["workspacesAdded"] = addedWorkspaces
		changed = true
	}
	if len(removedWorkspaces) > 0 {
		delta["workspacesRemoved"] = removedWorkspaces
		changed = true
	}

	prevMonitors := make(map[string]struct{}, len(prev.Monitors))
	for _, mon := range prev.Monitors {
		prevMonitors[mon.Name] = struct{}{}
	}
	currMonitors := make(map[string]struct{}, len(curr.Monitors))
	for _, mon := range curr.Monitors {
		currMonitors[mon.Name] = struct{}{}
	}
	var addedMonitors, removedMonitors []string
	for name := range currMonitors {
		if _, ok := prevMonitors[name]; !ok {
			addedMonitors = append(addedMonitors, name)
		}
	}
	for name := range prevMonitors {
		if _, ok := currMonitors[name]; !ok {
			removedMonitors = append(removedMonitors, name)
		}
	}
	sort.Strings(addedMonitors)
	sort.Strings(removedMonitors)
	if len(addedMonitors) > 0 {
		delta["monitorsAdded"] = addedMonitors
		changed = true
	}
	if len(removedMonitors) > 0 {
		delta["monitorsRemoved"] = removedMonitors
		changed = true
	}

	delta["changed"] = changed
	return delta
}
