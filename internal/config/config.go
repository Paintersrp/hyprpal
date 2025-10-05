package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/hyprpal/hyprpal/internal/layout"
)

// Config is the top-level configuration document.
type Config struct {
	ManagedWorkspaces []int                    `yaml:"managedWorkspaces"`
	Modes             []ModeConfig             `yaml:"modes"`
	RedactTitles      bool                     `yaml:"redactTitles"`
	Gaps              Gaps                     `yaml:"gaps"`
	TolerancePx       float64                  `yaml:"tolerancePx"`
	Profiles          MatcherProfiles          `yaml:"profiles"`
	ManualReserved    map[string]layout.Insets `yaml:"manualReserved"`
	toleranceSet      bool                     `yaml:"-"`
}

// UnmarshalYAML handles deprecated fields while decoding configuration files.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig struct {
		ManagedWorkspaces []int                    `yaml:"managedWorkspaces"`
		Modes             []ModeConfig             `yaml:"modes"`
		RedactTitles      bool                     `yaml:"redactTitles"`
		Gaps              Gaps                     `yaml:"gaps"`
		TolerancePx       *float64                 `yaml:"tolerancePx"`
		LegacyTolerancePx *float64                 `yaml:"placementTolerancePx"`
		Profiles          MatcherProfiles          `yaml:"profiles"`
		ManualReserved    map[string]layout.Insets `yaml:"manualReserved"`
	}

	var raw rawConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}

	c.ManagedWorkspaces = raw.ManagedWorkspaces
	c.Modes = raw.Modes
	c.RedactTitles = raw.RedactTitles
	c.Gaps = raw.Gaps
	c.Profiles = raw.Profiles
	c.ManualReserved = raw.ManualReserved

	switch {
	case raw.TolerancePx != nil:
		c.TolerancePx = *raw.TolerancePx
		c.toleranceSet = true
	case raw.LegacyTolerancePx != nil:
		c.TolerancePx = *raw.LegacyTolerancePx
		c.toleranceSet = true
	default:
		c.TolerancePx = 0
		c.toleranceSet = false
	}

	return nil
}

// MatcherProfiles defines reusable client matcher templates by name.
type MatcherProfiles map[string]MatcherConfig

// UnmarshalYAML ensures profile names are unique and values are parsed correctly.
func (p *MatcherProfiles) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		*p = nil
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("profiles must be a mapping")
	}
	result := make(map[string]MatcherConfig, len(value.Content)/2)
	seen := map[string]struct{}{}
	for i := 0; i < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("profile name must be a string")
		}
		name := keyNode.Value
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate profile %q", name)
		}
		seen[name] = struct{}{}
		var cfg MatcherConfig
		if err := valNode.Decode(&cfg); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
		result[name] = cfg
	}
	*p = result
	return nil
}

// MatcherConfig describes a reusable client matcher.
type MatcherConfig struct {
	Class      string   `yaml:"class"`
	AnyClass   []string `yaml:"anyClass"`
	TitleRegex string   `yaml:"titleRegex"`
}

// Gaps describes inner and outer gaps applied during layout planning.
type Gaps struct {
	Inner float64 `yaml:"inner"`
	Outer float64 `yaml:"outer"`
}

// ModeConfig represents a named mode with a set of rules.
type ModeConfig struct {
	Name  string       `yaml:"name"`
	Rules []RuleConfig `yaml:"rules"`
}

// RuleConfig represents a declarative rule with predicates and actions.
type RuleConfig struct {
	Name            string          `yaml:"name"`
	When            PredicateConfig `yaml:"when"`
	Actions         []ActionConfig  `yaml:"actions"`
	DebounceMs      int             `yaml:"debounceMs"`
	MutateUnmanaged bool            `yaml:"mutateUnmanaged"`
}

// UnmarshalYAML keeps backwards compatibility with the deprecated allowUnmanaged flag.
func (r *RuleConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawRule struct {
		Name            string          `yaml:"name"`
		When            PredicateConfig `yaml:"when"`
		Actions         []ActionConfig  `yaml:"actions"`
		DebounceMs      int             `yaml:"debounceMs"`
		MutateUnmanaged *bool           `yaml:"mutateUnmanaged"`
		AllowUnmanaged  *bool           `yaml:"allowUnmanaged"`
	}

	var raw rawRule
	if err := value.Decode(&raw); err != nil {
		return err
	}

	r.Name = raw.Name
	r.When = raw.When
	r.Actions = raw.Actions
	r.DebounceMs = raw.DebounceMs

	switch {
	case raw.MutateUnmanaged != nil:
		r.MutateUnmanaged = *raw.MutateUnmanaged
	case raw.AllowUnmanaged != nil:
		r.MutateUnmanaged = *raw.AllowUnmanaged
	default:
		r.MutateUnmanaged = false
	}

	return nil
}

// PredicateConfig implements the simple predicate tree language.
type PredicateConfig struct {
	Any         []PredicateConfig `yaml:"any"`
	All         []PredicateConfig `yaml:"all"`
	Not         *PredicateConfig  `yaml:"not"`
	Mode        string            `yaml:"mode"`
	AppClass    string            `yaml:"app.class"`
	TitleRegex  string            `yaml:"app.titleRegex"`
	AppsPresent []string          `yaml:"apps.present"`
	WorkspaceID int               `yaml:"workspace.id"`
	MonitorName string            `yaml:"monitor.name"`
}

// ActionConfig describes a single action invocation.
type ActionConfig struct {
	Type   string                 `yaml:"type"`
	Params map[string]interface{} `yaml:"params"`
}

// GridActionConfig represents the typed configuration for a layout.grid action.
type GridActionConfig struct {
	Workspace     int
	ColumnWeights []float64
	RowWeights    []float64
	Slots         []GridSlotConfig
}

// GridSlotConfig defines a single named slot within a grid action.
type GridSlotConfig struct {
	Name  string
	Row   int
	Col   int
	Span  GridSpanConfig
	Match map[string]interface{}
}

// GridSpanConfig controls how many rows/columns a slot occupies.
type GridSpanConfig struct {
	Rows int
	Cols int
}

// GridLayout parses the params for a layout.grid action into a strongly typed structure.
func (a ActionConfig) GridLayout() (*GridActionConfig, error) {
	if a.Type != "layout.grid" {
		return nil, fmt.Errorf("action type %q is not layout.grid", a.Type)
	}
	workspaceVal, ok := a.Params["workspace"]
	if !ok {
		return nil, fmt.Errorf("layout.grid requires workspace")
	}
	workspace, err := intFromInterface(workspaceVal, "workspace")
	if err != nil {
		return nil, err
	}
	if workspace <= 0 {
		return nil, fmt.Errorf("workspace must be positive, got %d", workspace)
	}

	cfg := &GridActionConfig{Workspace: workspace}

	if weights, ok := a.Params["colWeights"]; ok {
		vals, err := floatSliceFromInterface(weights, "colWeights")
		if err != nil {
			return nil, err
		}
		for i, w := range vals {
			if w <= 0 {
				return nil, fmt.Errorf("colWeights[%d] must be positive, got %v", i, w)
			}
		}
		cfg.ColumnWeights = vals
	}
	if weights, ok := a.Params["rowWeights"]; ok {
		vals, err := floatSliceFromInterface(weights, "rowWeights")
		if err != nil {
			return nil, err
		}
		for i, w := range vals {
			if w <= 0 {
				return nil, fmt.Errorf("rowWeights[%d] must be positive, got %v", i, w)
			}
		}
		cfg.RowWeights = vals
	}

	rawSlots, ok := a.Params["slots"]
	if !ok {
		return nil, fmt.Errorf("layout.grid requires slots")
	}
	list, ok := rawSlots.([]interface{})
	if !ok {
		return nil, fmt.Errorf("slots must be a list")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("slots must contain at least one entry")
	}
	cfg.Slots = make([]GridSlotConfig, 0, len(list))
	seenNames := map[string]struct{}{}
	for i, item := range list {
		slotMap, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("slots[%d] must be a mapping", i)
		}
		nameVal, ok := slotMap["name"]
		if !ok {
			return nil, fmt.Errorf("slots[%d] missing name", i)
		}
		name, err := stringFromInterfaceValue(nameVal, fmt.Sprintf("slots[%d].name", i))
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, fmt.Errorf("slots[%d] name cannot be empty", i)
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("duplicate slot name %q", name)
		}
		seenNames[name] = struct{}{}

		rowVal, ok := slotMap["row"]
		if !ok {
			return nil, fmt.Errorf("slots[%q] missing row", name)
		}
		row, err := intFromInterface(rowVal, fmt.Sprintf("slots[%q].row", name))
		if err != nil {
			return nil, err
		}
		if row < 0 {
			return nil, fmt.Errorf("slots[%q].row cannot be negative", name)
		}

		colVal, ok := slotMap["col"]
		if !ok {
			return nil, fmt.Errorf("slots[%q] missing col", name)
		}
		col, err := intFromInterface(colVal, fmt.Sprintf("slots[%q].col", name))
		if err != nil {
			return nil, err
		}
		if col < 0 {
			return nil, fmt.Errorf("slots[%q].col cannot be negative", name)
		}

		slotCfg := GridSlotConfig{Name: name, Row: row, Col: col}

		if spanVal, ok := slotMap["span"]; ok && spanVal != nil {
			spanMap, ok := spanVal.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("slots[%q].span must be a mapping", name)
			}
			if rowsVal, ok := spanMap["rows"]; ok {
				spanRows, err := intFromInterface(rowsVal, fmt.Sprintf("slots[%q].span.rows", name))
				if err != nil {
					return nil, err
				}
				if spanRows <= 0 {
					return nil, fmt.Errorf("slots[%q].span.rows must be positive", name)
				}
				slotCfg.Span.Rows = spanRows
			}
			if colsVal, ok := spanMap["cols"]; ok {
				spanCols, err := intFromInterface(colsVal, fmt.Sprintf("slots[%q].span.cols", name))
				if err != nil {
					return nil, err
				}
				if spanCols <= 0 {
					return nil, fmt.Errorf("slots[%q].span.cols must be positive", name)
				}
				slotCfg.Span.Cols = spanCols
			}
		}
		if slotCfg.Span.Rows == 0 {
			slotCfg.Span.Rows = 1
		}
		if slotCfg.Span.Cols == 0 {
			slotCfg.Span.Cols = 1
		}

		if matchVal, ok := slotMap["match"]; ok && matchVal != nil {
			matchMap, ok := matchVal.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("slots[%q].match must be a mapping", name)
			}
			slotCfg.Match = matchMap
		}

		cfg.Slots = append(cfg.Slots, slotCfg)
	}

	return cfg, nil
}

func intFromInterface(v interface{}, field string) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	default:
		return 0, fmt.Errorf("%s must be a number", field)
	}
}

func floatSliceFromInterface(v interface{}, field string) ([]float64, error) {
	switch list := v.(type) {
	case []float64:
		return append([]float64(nil), list...), nil
	case []int:
		result := make([]float64, len(list))
		for i, item := range list {
			result[i] = float64(item)
		}
		return result, nil
	case []int64:
		result := make([]float64, len(list))
		for i, item := range list {
			result[i] = float64(item)
		}
		return result, nil
	case []interface{}:
		result := make([]float64, len(list))
		for i, item := range list {
			switch t := item.(type) {
			case float64:
				result[i] = t
			case int:
				result[i] = float64(t)
			case int64:
				result[i] = float64(t)
			default:
				return nil, fmt.Errorf("%s[%d] must be a number", field, i)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be a list", field)
	}
}

func stringFromInterfaceValue(v interface{}, field string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	if s == "" {
		return "", fmt.Errorf("%s cannot be empty", field)
	}
	return s, nil
}

// Load reads and validates a configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if !c.toleranceSet {
		c.TolerancePx = 2.0
		c.toleranceSet = true
	}
}

// Validate performs basic sanity checks.
func (c *Config) Validate() error {
	if len(c.Modes) == 0 {
		return fmt.Errorf("config must define at least one mode")
	}
	if c.Gaps.Inner < 0 {
		return fmt.Errorf("gaps.inner cannot be negative")
	}
	if c.Gaps.Outer < 0 {
		return fmt.Errorf("gaps.outer cannot be negative")
	}
	if c.TolerancePx < 0 {
		return fmt.Errorf("tolerancePx cannot be negative")
	}
	for name, profile := range c.Profiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	managed := map[int]struct{}{}
	for _, ws := range c.ManagedWorkspaces {
		if ws <= 0 {
			return fmt.Errorf("managed workspace IDs must be positive, got %d", ws)
		}
		if _, exists := managed[ws]; exists {
			return fmt.Errorf("duplicate managed workspace %d", ws)
		}
		managed[ws] = struct{}{}
	}
	names := map[string]struct{}{}
	for _, m := range c.Modes {
		if m.Name == "" {
			return fmt.Errorf("mode name cannot be empty")
		}
		if _, exists := names[m.Name]; exists {
			return fmt.Errorf("duplicate mode name %q", m.Name)
		}
		names[m.Name] = struct{}{}
		for _, r := range m.Rules {
			if r.Name == "" {
				return fmt.Errorf("rule name cannot be empty (mode %q)", m.Name)
			}
			if len(r.Actions) == 0 {
				return fmt.Errorf("rule %q in mode %q must define actions", r.Name, m.Name)
			}
			if err := c.validateMatcherReferences(m.Name, r); err != nil {
				return err
			}
		}
	}
	for name, insets := range c.ManualReserved {
		if insets.HasNegative() {
			return fmt.Errorf("manualReserved entry %q cannot include negative values", name)
		}
	}
	return nil
}

func (c *Config) validateMatcherReferences(mode string, rule RuleConfig) error {
	for _, action := range rule.Actions {
		if action.Type == "layout.grid" {
			gridCfg, err := action.GridLayout()
			if err != nil {
				return fmt.Errorf("rule %q in mode %q: %w", rule.Name, mode, err)
			}
			for _, slot := range gridCfg.Slots {
				if slot.Match == nil {
					continue
				}
				profileNameRaw, ok := slot.Match["profile"]
				if !ok {
					continue
				}
				profileName, err := stringFromInterfaceValue(profileNameRaw, fmt.Sprintf("rule %s in mode %s slot %s match.profile", rule.Name, mode, slot.Name))
				if err != nil {
					return err
				}
				if _, exists := c.Profiles[profileName]; !exists {
					return fmt.Errorf("rule %q in mode %q slot %q references unknown profile %q", rule.Name, mode, slot.Name, profileName)
				}
			}
			continue
		}
		matchVal, ok := action.Params["match"]
		if !ok || matchVal == nil {
			continue
		}
		matchMap, ok := matchVal.(map[string]interface{})
		if !ok {
			return fmt.Errorf("rule %q in mode %q: match must be a mapping", rule.Name, mode)
		}
		profileNameRaw, ok := matchMap["profile"]
		if !ok {
			continue
		}
		profileName, err := stringFromInterfaceValue(profileNameRaw, fmt.Sprintf("rule %s in mode %s match.profile", rule.Name, mode))
		if err != nil {
			return err
		}
		if _, exists := c.Profiles[profileName]; !exists {
			return fmt.Errorf("rule %q in mode %q references unknown profile %q", rule.Name, mode, profileName)
		}
	}
	return nil
}

// Validate ensures matcher configuration has at least one selection criteria.
func (m MatcherConfig) Validate() error {
	if m.Class == "" && len(m.AnyClass) == 0 && m.TitleRegex == "" {
		return fmt.Errorf("must define class, anyClass, or titleRegex")
	}
	return nil
}
