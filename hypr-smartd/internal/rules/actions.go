package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

// ActionContext is passed to action planners.
type ActionContext struct {
	World    *state.World
	Logger   *util.Logger
	RuleName string
}

// Action produces layout operations for a rule.
type Action interface {
	Plan(ctx ActionContext) (layout.Plan, error)
}

// BuildActions compiles config actions.
func BuildActions(cfgs []config.ActionConfig) ([]Action, error) {
	actions := make([]Action, 0, len(cfgs))
	for _, ac := range cfgs {
		switch ac.Type {
		case "layout.sidecarDock":
			action, err := buildSidecarDock(ac.Params)
			if err != nil {
				return nil, fmt.Errorf("sidecarDock: %w", err)
			}
			actions = append(actions, action)
		case "layout.fullscreen":
			action, err := buildFullscreen(ac.Params)
			if err != nil {
				return nil, fmt.Errorf("fullscreen: %w", err)
			}
			actions = append(actions, action)
		case "layout.ensureWorkspace", "client.pinToWorkspace":
			// Not implemented in v0.1. Keep as no-op for compatibility.
			actions = append(actions, NoopAction{})
		default:
			return nil, fmt.Errorf("unsupported action type %q", ac.Type)
		}
	}
	return actions, nil
}

// NoopAction is a placeholder for yet-to-be-implemented actions.
type NoopAction struct{}

// Plan implements Action.
func (NoopAction) Plan(ActionContext) (layout.Plan, error) { return layout.Plan{}, nil }

type SidecarDockAction struct {
	WorkspaceID  int
	Side         string
	WidthPercent float64
	Match        clientMatcher
}

type clientMatcher func(c state.Client) bool

type FullscreenAction struct {
	Target string
	Match  clientMatcher
}

func buildSidecarDock(params map[string]interface{}) (Action, error) {
	workspace, err := intFrom(params, "workspace")
	if err != nil {
		return nil, err
	}
	side, _ := stringFrom(params, "side")
	side = strings.ToLower(side)
	if side == "" {
		side = "right"
	}
	if side != "left" && side != "right" {
		return nil, fmt.Errorf("side must be left or right, got %q", side)
	}
	width, err := floatFrom(params, "widthPercent", 25)
	if err != nil {
		return nil, err
	}
	if width < 10 {
		return nil, fmt.Errorf("widthPercent must be at least 10, got %v", width)
	}
	if width > 50 {
		return nil, fmt.Errorf("widthPercent must be at most 50, got %v", width)
	}
	matcher, err := parseClientMatcher(params["match"])
	if err != nil {
		return nil, err
	}
	return &SidecarDockAction{WorkspaceID: workspace, Side: side, WidthPercent: width, Match: matcher}, nil
}

func buildFullscreen(params map[string]interface{}) (Action, error) {
	target, _ := stringFrom(params, "target")
	if target == "" {
		target = "active"
	}
	matcher, err := parseClientMatcher(params["match"])
	if err != nil {
		return nil, err
	}
	return &FullscreenAction{Target: target, Match: matcher}, nil
}

func parseClientMatcher(v interface{}) (clientMatcher, error) {
	if v == nil {
		return func(state.Client) bool { return true }, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("match must be a mapping")
	}
	if cls, ok := m["class"]; ok {
		s, err := assertString(cls)
		if err != nil {
			return nil, err
		}
		expected := strings.ToLower(s)
		return func(c state.Client) bool { return strings.ToLower(c.Class) == expected }, nil
	}
	if any, ok := m["anyClass"]; ok {
		list, ok := any.([]interface{})
		if !ok {
			return nil, fmt.Errorf("anyClass must be a list")
		}
		set := map[string]struct{}{}
		for _, item := range list {
			str, err := assertString(item)
			if err != nil {
				return nil, err
			}
			set[strings.ToLower(str)] = struct{}{}
		}
		return func(c state.Client) bool {
			_, ok := set[strings.ToLower(c.Class)]
			return ok
		}, nil
	}
	if rgxVal, ok := m["titleRegex"]; ok {
		str, err := assertString(rgxVal)
		if err != nil {
			return nil, err
		}
		re, err := regexp.Compile(str)
		if err != nil {
			return nil, fmt.Errorf("compile match.titleRegex: %w", err)
		}
		return func(c state.Client) bool { return re.MatchString(c.Title) }, nil
	}
	return nil, fmt.Errorf("match requires class, anyClass, or titleRegex")
}

// Plan implements Action for SidecarDockAction.
func (a *SidecarDockAction) Plan(ctx ActionContext) (layout.Plan, error) {
	var target *state.Client
	for i := range ctx.World.Clients {
		c := ctx.World.Clients[i]
		if c.WorkspaceID == a.WorkspaceID && a.Match(c) {
			target = &ctx.World.Clients[i]
			break
		}
	}
	if target == nil {
		return layout.Plan{}, nil
	}
	monitor, err := ctx.World.MonitorForWorkspace(a.WorkspaceID)
	if err != nil {
		return layout.Plan{}, err
	}
	_, dock := layout.SplitSidecar(monitor.Rectangle, a.Side, a.WidthPercent)
	if layout.ApproximatelyEqual(target.Geometry, dock) {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (idempotent)", ctx.RuleName)
		}
		return layout.Plan{}, nil
	}
	plan := layout.FloatAndPlace(target.Address, dock)
	return plan, nil
}

// Plan implements Action for FullscreenAction.
func (a *FullscreenAction) Plan(ctx ActionContext) (layout.Plan, error) {
	var client *state.Client
	switch strings.ToLower(a.Target) {
	case "active":
		client = ctx.World.ActiveClient()
	case "match":
		for i := range ctx.World.Clients {
			c := ctx.World.Clients[i]
			if a.Match(c) {
				client = &ctx.World.Clients[i]
				break
			}
		}
	default:
		return layout.Plan{}, fmt.Errorf("unknown fullscreen target %q", a.Target)
	}
	if client == nil {
		return layout.Plan{}, nil
	}
	if client.FullscreenMode != 0 {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (idempotent)", ctx.RuleName)
		}
		return layout.Plan{}, nil
	}
	plan := layout.Fullscreen(client.Address, true)
	return plan, nil
}

func intFrom(m map[string]interface{}, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func floatFrom(m map[string]interface{}, key string, def float64) (float64, error) {
	v, ok := m[key]
	if !ok {
		return def, nil
	}
	switch t := v.(type) {
	case float64:
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func stringFrom(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", nil
	}
	return assertString(v)
}

func assertString(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	default:
		return "", fmt.Errorf("expected string, got %T", v)
	}
}
