package engine

import (
	"context"
	"encoding/json"
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

// Engine ties together the world model, rules, and IPC.
type Engine struct {
	hyprctl hyprctlClient
	logger  *util.Logger

	modes              map[string]rules.Mode
	modeOrder          []string
	activeMode         string
	dryRun             bool
	redactTitles       bool
	gaps               layout.Gaps
	placementTolerance float64

	mu          sync.Mutex
	debounce    map[string]time.Time
	cooldown    map[string]time.Time
	execHistory map[string][]time.Time
	lastWorld   *state.World
	evalLog     *evaluationLog
	ruleChecks  *ruleCheckHistory
}

const (
	ruleBurstWindow       = 5 * time.Second
	ruleBurstThreshold    = 3
	ruleBurstCooldown     = 5 * time.Second
	ruleCheckHistoryLimit = 256
)

type plannedRule struct {
	Key  string
	Mode string
	Name string
	Plan layout.Plan
}

// PlannedCommand represents a hyprctl dispatch that would be executed for the
// current world snapshot.
type PlannedCommand struct {
	Dispatch []string
	Reason   string
}

// RuleCheckRecord captures predicate evaluation outcomes for a single rule.
type RuleCheckRecord struct {
	Timestamp time.Time             `json:"timestamp"`
	Mode      string                `json:"mode"`
	Rule      string                `json:"rule"`
	Matched   bool                  `json:"matched"`
	Reason    string                `json:"reason,omitempty"`
	Predicate *rules.PredicateTrace `json:"predicate,omitempty"`
}

type ruleCheckHistory struct {
	buf      []RuleCheckRecord
	start    int
	count    int
	capacity int
}

// New creates a new engine instance.
func New(hyprctl hyprctlClient, logger *util.Logger, modes []rules.Mode, dryRun bool, redactTitles bool, gaps layout.Gaps, placementTolerance float64) *Engine {
	modeMap := make(map[string]rules.Mode)
	order := make([]string, 0, len(modes))
	for _, m := range modes {
		modeMap[m.Name] = m
		order = append(order, m.Name)
	}
	active := ""
	if len(order) > 0 {
		active = order[0]
	}
	return &Engine{
		hyprctl:            hyprctl,
		logger:             logger,
		modes:              modeMap,
		modeOrder:          order,
		activeMode:         active,
		dryRun:             dryRun,
		redactTitles:       redactTitles,
		gaps:               gaps,
		placementTolerance: placementTolerance,
		debounce:           make(map[string]time.Time),
		cooldown:           make(map[string]time.Time),
		execHistory:        make(map[string][]time.Time),
		evalLog:            newEvaluationLog(0),
		ruleChecks:         newRuleCheckHistory(0),
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
		modeMap[m.Name] = m
		order = append(order, m.Name)
	}
	e.modes = modeMap
	e.modeOrder = order
	if _, ok := modeMap[e.activeMode]; !ok && len(order) > 0 {
		e.activeMode = order[0]
	}
	e.debounce = make(map[string]time.Time)
	e.cooldown = make(map[string]time.Time)
	e.execHistory = make(map[string][]time.Time)
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
	e.placementTolerance = tolerance
	e.mu.Unlock()
}

func (e *Engine) redactTitlesEnabled() bool {
	e.mu.Lock()
	enabled := e.redactTitles
	e.mu.Unlock()
	return enabled
}

// Run starts the engine loop until context cancellation.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.reconcileAndApply(ctx); err != nil {
		return err
	}
	events, err := ipc.Subscribe(ctx, e.logger)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
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
				if err := e.reconcileAndApply(ctx); err != nil {
					e.logger.Errorf("reconcile failed: %v", err)
				}
			}
		}
	}
}

// Reconcile triggers a manual world refresh and rule evaluation.
func (e *Engine) Reconcile(ctx context.Context) error {
	return e.reconcileAndApply(ctx)
}

// reconcileAndApply refreshes the world model and evaluates rules.
func (e *Engine) reconcileAndApply(ctx context.Context) error {
	world, err := state.NewWorld(ctx, e.hyprctl)
	if err != nil {
		return err
	}
	redact := e.redactTitlesEnabled()
	storedWorld := world
	if redact {
		storedWorld = state.CloneWorld(world)
		redactWorldTitles(storedWorld)
	}
	e.mu.Lock()
	prev := e.lastWorld
	e.lastWorld = storedWorld
	e.mu.Unlock()

	counts := map[string]int{
		"clients":    len(storedWorld.Clients),
		"workspaces": len(storedWorld.Workspaces),
		"monitors":   len(storedWorld.Monitors),
	}
	e.trace("world.reconciled", map[string]any{
		"counts":          counts,
		"activeWorkspace": storedWorld.ActiveWorkspaceID,
		"activeClient":    storedWorld.ActiveClientAddress,
		"delta":           worldDelta(prev, storedWorld),
	})

	now := time.Now()
	plan, rules := e.evaluate(world, now, true)
	e.trace("plan.aggregated", map[string]any{
		"commandCount": len(plan.Commands),
		"commands":     plan.Commands,
	})
	if len(plan.Commands) == 0 {
		return nil
	}

	e.markDebounce(rules, now)

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

// PreviewPlan evaluates the current world and returns the pending plan without
// dispatching commands.
func (e *Engine) PreviewPlan(ctx context.Context, explain bool) ([]PlannedCommand, error) {
	world, err := state.NewWorld(ctx, e.hyprctl)
	if err != nil {
		return nil, err
	}
	storedWorld := world
	if e.redactTitlesEnabled() {
		storedWorld = state.CloneWorld(world)
		redactWorldTitles(storedWorld)
	}
	e.mu.Lock()
	e.lastWorld = storedWorld
	e.mu.Unlock()

	_, rules := e.evaluate(world, time.Now(), false)
	commands := make([]PlannedCommand, 0)
	for _, pr := range rules {
		reason := ""
		if explain {
			reason = fmt.Sprintf("%s:%s", pr.Mode, pr.Name)
		}
		for _, cmd := range pr.Plan.Commands {
			dispatch := append([]string(nil), cmd...)
			commands = append(commands, PlannedCommand{
				Dispatch: dispatch,
				Reason:   reason,
			})
		}
	}
	return commands, nil
}

// LastWorld returns the most recent world snapshot.
func (e *Engine) LastWorld() *state.World {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastWorld
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

func (e *Engine) evaluate(world *state.World, now time.Time, log bool) (layout.Plan, []plannedRule) {
	e.mu.Lock()
	activeMode := e.activeMode
	mode, ok := e.modes[activeMode]
	gaps := e.gaps
	tolerance := e.placementTolerance
	e.mu.Unlock()
	if !ok {
		if log {
			e.logger.Warnf("no active mode selected; skipping apply")
		}
		return layout.Plan{}, nil
	}

	var plan layout.Plan
	planned := make([]plannedRule, 0, len(mode.Rules))
	evalCtx := rules.EvalContext{Mode: activeMode, World: world}
	for _, rule := range mode.Rules {
		key := activeMode + ":" + rule.Name
		e.mu.Lock()
		last := e.debounce[key]
		cooldownUntil := e.cooldown[key]
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
		for _, action := range rule.Actions {
			p, err := action.Plan(rules.ActionContext{
				World:              world,
				Logger:             e.logger,
				RuleName:           rule.Name,
				ManagedWorkspaces:  rule.ManagedWorkspaces,
				AllowUnmanaged:     rule.AllowUnmanaged,
				Gaps:               gaps,
				PlacementTolerance: tolerance,
			})
			if err != nil {
				e.logger.Errorf("rule %s action error: %v", rule.Name, err)
				continue
			}
			rulePlan.Merge(p)
		}
		if len(rulePlan.Commands) == 0 {
			record.Reason = "no-commands"
			e.recordRuleCheck(record)
			continue
		}
		throttled := e.trackExecution(key, rulePlan, now)
		if throttled {
			if log {
				e.logger.Warnf("rule %s temporarily disabled after %d executions in %s [mode %s]", rule.Name, ruleBurstThreshold, ruleBurstWindow, activeMode)
			}
			record.Reason = "throttled"
			e.recordRuleCheck(record)
			continue
		}
		plan.Merge(rulePlan)
		if log {
			e.trace("rule.matched", map[string]any{
				"mode":     activeMode,
				"rule":     rule.Name,
				"commands": rulePlan.Commands,
			})
		}
		planned = append(planned, plannedRule{
			Key:  key,
			Mode: activeMode,
			Name: rule.Name,
			Plan: rulePlan,
		})
		record.Reason = "matched"
		e.recordRuleCheck(record)
	}
	return plan, planned
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

func cloneRuleCheckRecord(record RuleCheckRecord) RuleCheckRecord {
	cloned := RuleCheckRecord{
		Timestamp: record.Timestamp,
		Mode:      record.Mode,
		Rule:      record.Rule,
		Matched:   record.Matched,
		Reason:    record.Reason,
	}
	if record.Predicate != nil {
		cloned.Predicate = rules.ClonePredicateTrace(record.Predicate)
	}
	return cloned
}

func (e *Engine) recordRuleCheck(record RuleCheckRecord) {
	if e.ruleChecks == nil {
		return
	}
	e.mu.Lock()
	e.ruleChecks.add(record)
	e.mu.Unlock()
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

func (e *Engine) trackExecution(ruleKey string, plan layout.Plan, now time.Time) bool {
	sig := executionSignature(ruleKey, plan)
	windowStart := now.Add(-ruleBurstWindow)

	e.mu.Lock()
	history := e.execHistory[sig]
	pruned := history[:0]
	for _, ts := range history {
		if ts.After(windowStart) {
			pruned = append(pruned, ts)
		}
	}
	pruned = append(pruned, now)
	e.execHistory[sig] = pruned
	exceeded := len(pruned) > ruleBurstThreshold
	if exceeded {
		e.cooldown[ruleKey] = now.Add(ruleBurstCooldown)
	}
	e.mu.Unlock()
	return exceeded
}

func executionSignature(ruleKey string, plan layout.Plan) string {
	var b strings.Builder
	b.WriteString(ruleKey)
	b.WriteString("|")
	for i, cmd := range plan.Commands {
		if i > 0 {
			b.WriteString(";")
		}
		b.WriteString(strings.Join(cmd, " "))
	}
	return b.String()
}

func (e *Engine) isInteresting(kind string) bool {
	switch kind {
	case "openwindow", "closewindow", "activewindow", "workspace", "movewindow", "monitorremoved", "monitoradded":
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
