package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hyprpal/hypr-smartd/internal/ipc"
	"github.com/hyprpal/hypr-smartd/internal/layout"
	"github.com/hyprpal/hypr-smartd/internal/rules"
	"github.com/hyprpal/hypr-smartd/internal/state"
	"github.com/hyprpal/hypr-smartd/internal/util"
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

	mu        sync.Mutex
	debounce  map[string]time.Time
	cooldown  map[string]time.Time
	lastWorld *state.World
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
		hyprctl:    hyprctl,
		logger:     logger,
		modes:      modeMap,
		modeOrder:  order,
		activeMode: active,
		dryRun:     dryRun,
		debounce:   make(map[string]time.Time),
		cooldown:   make(map[string]time.Time),
	}
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
	activeMode := e.activeMode
	mode, ok := e.modes[activeMode]
	e.mu.Unlock()

	if !ok {
		e.logger.Warnf("no active mode selected; skipping apply")
		return nil
	}

	plan := layout.Plan{}
	now := time.Now()
	participating := make(map[string]struct{})
	for _, rule := range mode.Rules {
		key := activeMode + ":" + rule.Name
		e.mu.Lock()
		last := e.debounce[key]
		cooldownUntil := e.cooldown[key]
		e.mu.Unlock()
		if cooldownUntil.After(now) {
			e.logger.Infof("rule %s skipped (cooldown) [mode %s]", rule.Name, activeMode)
			continue
		}
		if !last.IsZero() && now.Sub(last) < rule.Debounce {
			e.logger.Infof("rule %s skipped (debounced) [mode %s]", rule.Name, activeMode)
			continue
		}
		if !rule.When(rules.EvalContext{Mode: e.activeMode, World: world}) {
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
		plan.Merge(rulePlan)
		participating[key] = struct{}{}
		e.mu.Lock()
		e.debounce[key] = now
		e.mu.Unlock()
	}

	if len(plan.Commands) == 0 {
		return nil
	}

	applyCooldowns := func() {
		if len(participating) == 0 {
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		until := now.Add(1 * time.Second)
		for key := range participating {
			e.cooldown[key] = until
		}
	}
	if e.dryRun {
		for _, cmd := range plan.Commands {
			e.logger.Infof("DRY-RUN dispatch: %v", cmd)
		}
		applyCooldowns()
		return nil
	}
	if err := plan.Execute(e.hyprctl); err != nil {
		return err
	}
	applyCooldowns()
	for _, cmd := range plan.Commands {
		e.logger.Infof("dispatched: %v", cmd)
	}
	return nil
}

// LastWorld returns the most recent world snapshot.
func (e *Engine) LastWorld() *state.World {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastWorld
}

func (e *Engine) isInteresting(kind string) bool {
	switch kind {
	case "openwindow", "closewindow", "activewindow", "workspace", "movewindow", "monitorremoved", "monitoradded":
		return true
	default:
		return false
	}
}
