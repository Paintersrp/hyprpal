package engine

import (
	"context"
	"fmt"
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

	modes      map[string]rules.Mode
	modeOrder  []string
	activeMode string
	dryRun     bool

	mu          sync.Mutex
	debounce    map[string]time.Time
	cooldown    map[string]time.Time
	execHistory map[string][]time.Time
	lastWorld   *state.World
}

const (
	ruleBurstWindow    = 5 * time.Second
	ruleBurstThreshold = 3
	ruleBurstCooldown  = 5 * time.Second
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

// New creates a new engine instance.
func New(hyprctl hyprctlClient, logger *util.Logger, modes []rules.Mode, dryRun bool) *Engine {
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
		hyprctl:     hyprctl,
		logger:      logger,
		modes:       modeMap,
		modeOrder:   order,
		activeMode:  active,
		dryRun:      dryRun,
		debounce:    make(map[string]time.Time),
		cooldown:    make(map[string]time.Time),
		execHistory: make(map[string][]time.Time),
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
			e.logger.Debugf("event %s %s", ev.Kind, ev.Payload)
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
	e.mu.Lock()
	e.lastWorld = world
	e.mu.Unlock()

	now := time.Now()
	plan, rules := e.evaluate(world, now, true)
	if len(plan.Commands) == 0 {
		return nil
	}

	e.markDebounce(rules, now)

	if e.dryRun {
		for _, cmd := range plan.Commands {
			e.logger.Infof("DRY-RUN dispatch: %v", cmd)
		}
		e.applyCooldown(rules, now.Add(1*time.Second))
		return nil
	}
	if err := plan.Execute(e.hyprctl); err != nil {
		return err
	}
	e.applyCooldown(rules, now.Add(1*time.Second))
	for _, cmd := range plan.Commands {
		e.logger.Infof("dispatched: %v", cmd)
	}
	return nil
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

func (e *Engine) evaluate(world *state.World, now time.Time, log bool) (layout.Plan, []plannedRule) {
	e.mu.Lock()
	activeMode := e.activeMode
	mode, ok := e.modes[activeMode]
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
		if cooldownUntil.After(now) {
			if log {
				e.logger.Infof("rule %s skipped (cooldown) [mode %s]", rule.Name, activeMode)
			}
			continue
		}
		if !last.IsZero() && now.Sub(last) < rule.Debounce {
			if log {
				e.logger.Infof("rule %s skipped (debounced) [mode %s]", rule.Name, activeMode)
			}
			continue
		}
		if !rule.When(evalCtx) {
			continue
		}
		rulePlan := layout.Plan{}
		for _, action := range rule.Actions {
			p, err := action.Plan(rules.ActionContext{
				World:    world,
				Logger:   e.logger,
				RuleName: rule.Name,
			})
			if err != nil {
				e.logger.Errorf("rule %s action error: %v", rule.Name, err)
				continue
			}
			rulePlan.Merge(p)
		}
		if len(rulePlan.Commands) == 0 {
			continue
		}
		throttled := e.trackExecution(key, rulePlan, now)
		if throttled {
			if log {
				e.logger.Warnf("rule %s temporarily disabled after %d executions in %s [mode %s]", rule.Name, ruleBurstThreshold, ruleBurstWindow, activeMode)
			}
			continue
		}
		plan.Merge(rulePlan)
		planned = append(planned, plannedRule{
			Key:  key,
			Mode: activeMode,
			Name: rule.Name,
			Plan: rulePlan,
		})
	}
	return plan, planned
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
