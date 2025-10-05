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
	World             *state.World
	Logger            *util.Logger
	RuleName          string
	ManagedWorkspaces map[int]struct{}
	MutateUnmanaged   bool
	Gaps              layout.Gaps
	TolerancePx       float64
	MonitorReserved   map[string]layout.Insets
}

func (ctx ActionContext) workspaceAllowed(id int) bool {
	if ctx.MutateUnmanaged {
		return true
	}
	if len(ctx.ManagedWorkspaces) == 0 {
		return true
	}
	_, ok := ctx.ManagedWorkspaces[id]
	return ok
}

// Action produces layout operations for a rule.
type Action interface {
	Plan(ctx ActionContext) (layout.Plan, error)
}

// BuildActions compiles config actions.
func BuildActions(cfgs []config.ActionConfig, profiles map[string]config.MatcherConfig) ([]Action, error) {
	actions := make([]Action, 0, len(cfgs))
	for _, ac := range cfgs {
		switch ac.Type {
		case "layout.sidecarDock":
			action, err := buildSidecarDock(ac.Params, profiles)
			if err != nil {
				return nil, fmt.Errorf("sidecarDock: %w", err)
			}
			actions = append(actions, action)
		case "layout.fullscreen":
			action, err := buildFullscreen(ac.Params, profiles)
			if err != nil {
				return nil, fmt.Errorf("fullscreen: %w", err)
			}
			actions = append(actions, action)
		case "layout.grid":
			action, err := buildGrid(ac, profiles)
			if err != nil {
				return nil, fmt.Errorf("grid: %w", err)
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
	FocusAfter   string
}

type clientMatcher func(c state.Client) bool

type FullscreenAction struct {
	Target string
	Match  clientMatcher
}

type GridLayoutAction struct {
	WorkspaceID   int
	ColumnWeights []float64
	RowWeights    []float64
	Slots         []GridSlotAction
}

type GridSlotAction struct {
	Name    string
	Row     int
	Col     int
	RowSpan int
	ColSpan int
	Match   clientMatcher
}

func buildSidecarDock(params map[string]interface{}, profiles map[string]config.MatcherConfig) (Action, error) {
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
	focus, _ := stringFrom(params, "focusAfter")
	focus = strings.ToLower(focus)
	if focus == "" {
		focus = "sidecar"
	}
	switch focus {
	case "sidecar", "host", "none":
	default:
		return nil, fmt.Errorf("focusAfter must be host, sidecar, or none, got %q", focus)
	}

	matcher, err := parseClientMatcher(params["match"], profiles)
	if err != nil {
		return nil, err
	}
	return &SidecarDockAction{
		WorkspaceID:  workspace,
		Side:         side,
		WidthPercent: width,
		Match:        matcher,
		FocusAfter:   focus,
	}, nil
}

func buildFullscreen(params map[string]interface{}, profiles map[string]config.MatcherConfig) (Action, error) {
	target, _ := stringFrom(params, "target")
	if target == "" {
		target = "active"
	}
	matcher, err := parseClientMatcher(params["match"], profiles)
	if err != nil {
		return nil, err
	}
	return &FullscreenAction{Target: target, Match: matcher}, nil
}

func buildGrid(ac config.ActionConfig, profiles map[string]config.MatcherConfig) (Action, error) {
	cfg, err := ac.GridLayout()
	if err != nil {
		return nil, err
	}
	slots := make([]GridSlotAction, 0, len(cfg.Slots))
	for _, slotCfg := range cfg.Slots {
		matcher, err := parseClientMatcher(slotCfg.Match, profiles)
		if err != nil {
			return nil, fmt.Errorf("slot %q: %w", slotCfg.Name, err)
		}
		slots = append(slots, GridSlotAction{
			Name:    slotCfg.Name,
			Row:     slotCfg.Row,
			Col:     slotCfg.Col,
			RowSpan: slotCfg.Span.Rows,
			ColSpan: slotCfg.Span.Cols,
			Match:   matcher,
		})
	}
	action := &GridLayoutAction{
		WorkspaceID:   cfg.Workspace,
		ColumnWeights: append([]float64(nil), cfg.ColumnWeights...),
		RowWeights:    append([]float64(nil), cfg.RowWeights...),
		Slots:         slots,
	}
	return action, nil
}

func parseClientMatcher(v interface{}, profiles map[string]config.MatcherConfig) (clientMatcher, error) {
	if v == nil {
		return func(state.Client) bool { return true }, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("match must be a mapping")
	}
	comboKeys := 0
	if _, ok := m["allOfProfiles"]; ok {
		comboKeys++
	}
	if _, ok := m["anyOfProfiles"]; ok {
		comboKeys++
	}
	if comboKeys > 1 {
		return nil, fmt.Errorf("match cannot combine allOfProfiles and anyOfProfiles")
	}
	if profileName, ok := m["profile"]; ok {
		conflictKeys := make([]string, 0, 5)
		for _, key := range []string{"allOfProfiles", "anyOfProfiles", "class", "anyClass", "titleRegex"} {
			if _, exists := m[key]; exists {
				conflictKeys = append(conflictKeys, key)
			}
		}
		if len(conflictKeys) > 0 {
			return nil, fmt.Errorf("match.profile cannot be combined with %s", strings.Join(conflictKeys, ", "))
		}
		name, err := assertString(profileName)
		if err != nil {
			return nil, err
		}
		profile, exists := profiles[name]
		if !exists {
			return nil, fmt.Errorf("unknown match profile %q", name)
		}
		return matcherFromConfig(profile)
	}
	if namesVal, ok := m["allOfProfiles"]; ok {
		names, err := stringSlice(namesVal)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("allOfProfiles must not be empty")
		}
		matchers, err := matchersFromProfiles(names, profiles)
		if err != nil {
			return nil, err
		}
		return func(c state.Client) bool {
			for _, matcher := range matchers {
				if !matcher(c) {
					return false
				}
			}
			return true
		}, nil
	}
	if namesVal, ok := m["anyOfProfiles"]; ok {
		names, err := stringSlice(namesVal)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("anyOfProfiles must not be empty")
		}
		matchers, err := matchersFromProfiles(names, profiles)
		if err != nil {
			return nil, err
		}
		return func(c state.Client) bool {
			for _, matcher := range matchers {
				if matcher(c) {
					return true
				}
			}
			return false
		}, nil
	}
	if _, ok := m["class"]; ok {
		cfg, err := matcherConfigFromMap(m)
		if err != nil {
			return nil, err
		}
		return matcherFromConfig(cfg)
	}
	if _, ok := m["anyClass"]; ok {
		cfg, err := matcherConfigFromMap(m)
		if err != nil {
			return nil, err
		}
		return matcherFromConfig(cfg)
	}
	if _, ok := m["titleRegex"]; ok {
		cfg, err := matcherConfigFromMap(m)
		if err != nil {
			return nil, err
		}
		return matcherFromConfig(cfg)
	}
	return nil, fmt.Errorf("match requires class, anyClass, or titleRegex")
}

func matchersFromProfiles(names []string, profiles map[string]config.MatcherConfig) ([]clientMatcher, error) {
	matchers := make([]clientMatcher, 0, len(names))
	for _, name := range names {
		profile, exists := profiles[name]
		if !exists {
			return nil, fmt.Errorf("unknown match profile %q", name)
		}
		matcher, err := matcherFromConfig(profile)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, matcher)
	}
	return matchers, nil
}

func stringSlice(v interface{}) ([]string, error) {
	if strList, ok := v.([]string); ok {
		return append([]string(nil), strList...), nil
	}
	list, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("profile list must be an array of strings")
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		str, err := assertString(item)
		if err != nil {
			return nil, err
		}
		result = append(result, str)
	}
	return result, nil
}

func matcherConfigFromMap(m map[string]interface{}) (config.MatcherConfig, error) {
	var cfg config.MatcherConfig
	if cls, ok := m["class"]; ok {
		s, err := assertString(cls)
		if err != nil {
			return cfg, err
		}
		cfg.Class = s
	}
	if any, ok := m["anyClass"]; ok {
		list, ok := any.([]interface{})
		if !ok {
			return cfg, fmt.Errorf("anyClass must be a list")
		}
		cfg.AnyClass = make([]string, 0, len(list))
		for _, item := range list {
			str, err := assertString(item)
			if err != nil {
				return cfg, err
			}
			cfg.AnyClass = append(cfg.AnyClass, str)
		}
	}
	if rgxVal, ok := m["titleRegex"]; ok {
		str, err := assertString(rgxVal)
		if err != nil {
			return cfg, err
		}
		cfg.TitleRegex = str
	}
	return cfg, nil
}

func matcherFromConfig(cfg config.MatcherConfig) (clientMatcher, error) {
	if cfg.Class != "" {
		expected := strings.ToLower(cfg.Class)
		return func(c state.Client) bool { return strings.ToLower(c.Class) == expected }, nil
	}
	if len(cfg.AnyClass) > 0 {
		set := map[string]struct{}{}
		for _, item := range cfg.AnyClass {
			set[strings.ToLower(item)] = struct{}{}
		}
		return func(c state.Client) bool {
			_, ok := set[strings.ToLower(c.Class)]
			return ok
		}, nil
	}
	if cfg.TitleRegex != "" {
		re, err := regexp.Compile(cfg.TitleRegex)
		if err != nil {
			return nil, fmt.Errorf("compile match.titleRegex: %w", err)
		}
		return func(c state.Client) bool { return re.MatchString(c.Title) }, nil
	}
	return nil, fmt.Errorf("match requires class, anyClass, or titleRegex")
}

// Plan implements Action for SidecarDockAction.
func (a *SidecarDockAction) Plan(ctx ActionContext) (layout.Plan, error) {
	if !ctx.workspaceAllowed(a.WorkspaceID) {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (workspace %d unmanaged)", ctx.RuleName, a.WorkspaceID)
		}
		return layout.Plan{}, nil
	}
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
	reserved := layout.Insets{}
	if ctx.MonitorReserved != nil {
		if insets, ok := ctx.MonitorReserved[monitor.Name]; ok {
			reserved = insets
		}
	}
	_, dock := layout.SplitSidecar(monitor.Rectangle, a.Side, a.WidthPercent, ctx.Gaps, reserved)
	if layout.ApproximatelyEqual(target.Geometry, dock, ctx.TolerancePx) {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (idempotent)", ctx.RuleName)
		}
		return layout.Plan{}, nil
	}
	var plan layout.Plan
	currentFocus := ctx.World.ActiveClientAddress
	addr := fmt.Sprintf("address:%s", target.Address)
	plan.Add("setfloatingaddress", addr, "1")
	if target.Address != currentFocus {
		plan.Add("focuswindow", addr)
		currentFocus = target.Address
	}
	plan.Add("movewindowpixel", "exact", fmt.Sprintf("%d", int(dock.X)), fmt.Sprintf("%d", int(dock.Y)))
	plan.Add("resizewindowpixel", "exact", fmt.Sprintf("%d", int(dock.Width)), fmt.Sprintf("%d", int(dock.Height)))

	hostAddress := ctx.World.ActiveClientAddress
	if hostAddress == target.Address {
		hostAddress = ""
	}

	switch a.FocusAfter {
	case "host":
		if hostAddress != "" && hostAddress != currentFocus {
			plan.Merge(layout.Focus(hostAddress))
			currentFocus = hostAddress
		}
	case "sidecar":
		if target.Address != currentFocus {
			plan.Merge(layout.Focus(target.Address))
			currentFocus = target.Address
		}
	case "none":
		// leave focus as-is
	}

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
	if !ctx.workspaceAllowed(client.WorkspaceID) {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (workspace %d unmanaged)", ctx.RuleName, client.WorkspaceID)
		}
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

// Plan implements Action for GridLayoutAction.
func (a *GridLayoutAction) Plan(ctx ActionContext) (layout.Plan, error) {
	if !ctx.workspaceAllowed(a.WorkspaceID) {
		if ctx.Logger != nil {
			ctx.Logger.Infof("rule %s skipped (workspace %d unmanaged)", ctx.RuleName, a.WorkspaceID)
		}
		return layout.Plan{}, nil
	}
	monitor, err := ctx.World.MonitorForWorkspace(a.WorkspaceID)
	if err != nil {
		return layout.Plan{}, err
	}
	reserved := layout.Insets{}
	if ctx.MonitorReserved != nil {
		if insets, ok := ctx.MonitorReserved[monitor.Name]; ok {
			reserved = insets
		}
	}
	slotSpecs := make([]layout.GridSlotSpec, 0, len(a.Slots))
	for _, slot := range a.Slots {
		slotSpecs = append(slotSpecs, layout.GridSlotSpec{
			Name:    slot.Name,
			Row:     slot.Row,
			Col:     slot.Col,
			RowSpan: slot.RowSpan,
			ColSpan: slot.ColSpan,
		})
	}
	rects, err := layout.GridRects(monitor.Rectangle, ctx.Gaps, reserved, a.ColumnWeights, a.RowWeights, slotSpecs)
	if err != nil {
		return layout.Plan{}, err
	}
	used := make(map[string]struct{})
	var plan layout.Plan
	for _, slot := range a.Slots {
		rect, ok := rects[slot.Name]
		if !ok {
			continue
		}
		var target *state.Client
		for i := range ctx.World.Clients {
			c := &ctx.World.Clients[i]
			if c.WorkspaceID != a.WorkspaceID {
				continue
			}
			if _, taken := used[c.Address]; taken {
				continue
			}
			if slot.Match(*c) {
				target = c
				break
			}
		}
		if target == nil {
			continue
		}
		used[target.Address] = struct{}{}
		if layout.ApproximatelyEqual(target.Geometry, rect, ctx.TolerancePx) {
			continue
		}
		plan.Merge(layout.FloatAndPlace(target.Address, rect))
	}
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
