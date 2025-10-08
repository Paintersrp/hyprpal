package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hyprpal/hyprpal/internal/layout"
)

// LintError represents a configuration validation failure with path context.
type LintError struct {
	Path    string
	Message string
}

// Error satisfies the error interface.
func (e LintError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

func newLintError(path, format string, args ...interface{}) LintError {
	return LintError{Path: path, Message: fmt.Sprintf(format, args...)}
}

// Config is the top-level configuration document.
type Config struct {
        ManagedWorkspaces []int                    `yaml:"managedWorkspaces"`
        Modes             []ModeConfig             `yaml:"modes"`
        RedactTitles      bool                     `yaml:"redactTitles"`
        Gaps              Gaps                     `yaml:"gaps"`
        TolerancePx       float64                  `yaml:"tolerancePx"`
        Profiles          MatcherProfiles          `yaml:"profiles"`
        ManualReserved    map[string]layout.Insets `yaml:"manualReserved"`
        Telemetry         TelemetryConfig          `yaml:"telemetry"`
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
                Telemetry         TelemetryConfig          `yaml:"telemetry"`
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
        c.Telemetry = raw.Telemetry

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

// TelemetryConfig gates anonymous telemetry collection.
type TelemetryConfig struct {
        Enabled bool `yaml:"enabled"`
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
	Priority        int             `yaml:"priority"`
	Throttle        *RuleThrottle   `yaml:"throttle"`
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
		Priority        int             `yaml:"priority"`
		Throttle        *RuleThrottle   `yaml:"throttle"`
	}

	var raw rawRule
	if err := value.Decode(&raw); err != nil {
		return err
	}

	r.Name = raw.Name
	r.When = raw.When
	r.Actions = raw.Actions
	r.DebounceMs = raw.DebounceMs
	r.Priority = raw.Priority
	r.Throttle = raw.Throttle

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

// RuleThrottle describes optional rate limiting for rule execution.
type RuleThrottle struct {
	FiringLimit int                  `yaml:"firingLimit"`
	WindowMs    int                  `yaml:"windowMs"`
	Windows     []RuleThrottleWindow `yaml:"windows"`
}

// RuleThrottleWindow defines a single sliding window threshold for a rule.
type RuleThrottleWindow struct {
	FiringLimit int `yaml:"firingLimit"`
	WindowMs    int `yaml:"windowMs"`
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

// Lint returns a slice of validation errors, covering all detected issues.
func (c *Config) Lint() []LintError {
	var errs []LintError
	if len(c.Modes) == 0 {
		errs = append(errs, newLintError("modes", "must define at least one mode"))
	}
	if c.Gaps.Inner < 0 {
		errs = append(errs, newLintError("gaps.inner", "cannot be negative"))
	}
	if c.Gaps.Outer < 0 {
		errs = append(errs, newLintError("gaps.outer", "cannot be negative"))
	}
	if c.TolerancePx < 0 {
		errs = append(errs, newLintError("tolerancePx", "cannot be negative"))
	}
	for name, profile := range c.Profiles {
		if err := profile.Validate(); err != nil {
			errs = append(errs, newLintError(fmt.Sprintf("profiles.%s", name), err.Error()))
		}
	}
	managed := map[int]int{}
	for i, ws := range c.ManagedWorkspaces {
		path := fmt.Sprintf("managedWorkspaces[%d]", i)
		if ws <= 0 {
			errs = append(errs, newLintError(path, "must be positive, got %d", ws))
		}
		if prevIdx, exists := managed[ws]; exists {
			errs = append(errs, newLintError(path, "duplicate workspace ID %d (already defined at managedWorkspaces[%d])", ws, prevIdx))
		} else {
			managed[ws] = i
		}
	}
	modeNames := map[string]int{}
	for i, mode := range c.Modes {
		modePath := fmt.Sprintf("modes[%d]", i)
		if mode.Name == "" {
			errs = append(errs, newLintError(modePath+".name", "cannot be empty"))
		} else if prevIdx, exists := modeNames[mode.Name]; exists {
			errs = append(errs, newLintError(modePath+".name", "duplicate mode name %q (already defined at modes[%d])", mode.Name, prevIdx))
		} else {
			modeNames[mode.Name] = i
		}
		for j, rule := range mode.Rules {
			rulePath := fmt.Sprintf("%s.rules[%d]", modePath, j)
			if rule.Name == "" {
				errs = append(errs, newLintError(rulePath+".name", "cannot be empty"))
			}
			if len(rule.Actions) == 0 {
				errs = append(errs, newLintError(rulePath+".actions", "must define at least one action"))
			}
			if rule.Priority < 0 {
				errs = append(errs, newLintError(rulePath+".priority", "cannot be negative"))
			}
			if rule.Throttle != nil {
				throttlePath := rulePath + ".throttle"
				legacyConfigured := rule.Throttle.FiringLimit != 0 || rule.Throttle.WindowMs != 0
				if legacyConfigured {
					if rule.Throttle.FiringLimit <= 0 {
						errs = append(errs, newLintError(throttlePath+".firingLimit", "must be positive"))
					}
					if rule.Throttle.WindowMs <= 0 {
						errs = append(errs, newLintError(throttlePath+".windowMs", "must be positive"))
					}
				}
				for idx, window := range rule.Throttle.Windows {
					windowPath := fmt.Sprintf("%s.windows[%d]", throttlePath, idx)
					if window.FiringLimit <= 0 {
						errs = append(errs, newLintError(windowPath+".firingLimit", "must be positive"))
					}
					if window.WindowMs <= 0 {
						errs = append(errs, newLintError(windowPath+".windowMs", "must be positive"))
					}
				}
				if !legacyConfigured && len(rule.Throttle.Windows) == 0 {
					errs = append(errs, newLintError(throttlePath, "must define at least one window"))
				}
			}
			errs = append(errs, c.lintMatcherReferences(i, j, rule)...)
		}
	}
	for name, insets := range c.ManualReserved {
		if insets.HasNegative() {
			errs = append(errs, newLintError(fmt.Sprintf("manualReserved.%s", name), "cannot include negative values"))
		}
	}
	return errs
}

func (c *Config) lintMatcherReferences(modeIdx, ruleIdx int, rule RuleConfig) []LintError {
	var errs []LintError
	rulePath := fmt.Sprintf("modes[%d].rules[%d]", modeIdx, ruleIdx)
	for actionIdx, action := range rule.Actions {
		actionPath := fmt.Sprintf("%s.actions[%d]", rulePath, actionIdx)
		if action.Type == "layout.grid" {
			gridCfg, err := action.GridLayout()
			if err != nil {
				errs = append(errs, newLintError(actionPath, err.Error()))
				continue
			}
			for slotIdx, slot := range gridCfg.Slots {
				if slot.Match == nil {
					continue
				}
				matchPath := fmt.Sprintf("%s.params.slots[%d].match", actionPath, slotIdx)
				c.lintMatchProfileRefs(matchPath, slot.Match, &errs)
			}
			continue
		}
		matchVal, ok := action.Params["match"]
		if !ok || matchVal == nil {
			continue
		}
		matchMap, ok := matchVal.(map[string]interface{})
		if !ok {
			errs = append(errs, newLintError(actionPath+".params.match", "must be a mapping"))
			continue
		}
		c.lintMatchProfileRefs(actionPath+".params.match", matchMap, &errs)
	}
	return errs
}

func (c *Config) lintMatchProfileRefs(path string, matchMap map[string]interface{}, errs *[]LintError) {
	comboKeys := 0
	if _, ok := matchMap["allOfProfiles"]; ok {
		comboKeys++
	}
	if _, ok := matchMap["anyOfProfiles"]; ok {
		comboKeys++
	}
	if comboKeys > 1 {
		*errs = append(*errs, newLintError(path, "cannot combine allOfProfiles and anyOfProfiles"))
	}
	if profileVal, ok := matchMap["profile"]; ok {
		profilePath := path + ".profile"
		profileName, ok := stringForLint(profileVal, profilePath, false, errs)
		if ok {
			if _, exists := c.Profiles[profileName]; !exists {
				*errs = append(*errs, newLintError(profilePath, "references unknown profile %q", profileName))
			}
		}
		conflictKeys := make([]string, 0, 2)
		for _, key := range []string{"allOfProfiles", "anyOfProfiles"} {
			if _, exists := matchMap[key]; exists {
				conflictKeys = append(conflictKeys, key)
			}
		}
		for _, key := range []string{"class", "anyClass", "titleRegex"} {
			if _, exists := matchMap[key]; exists {
				conflictKeys = append(conflictKeys, key)
			}
		}
		if len(conflictKeys) > 0 {
			*errs = append(*errs, newLintError(path, "cannot combine profile with %s", strings.Join(conflictKeys, ", ")))
		}
	}
	if namesVal, ok := matchMap["allOfProfiles"]; ok {
		c.lintProfileList(path+".allOfProfiles", namesVal, errs)
	}
	if namesVal, ok := matchMap["anyOfProfiles"]; ok {
		c.lintProfileList(path+".anyOfProfiles", namesVal, errs)
	}
}

func (c *Config) lintProfileList(path string, value interface{}, errs *[]LintError) {
	names, ok := stringSliceForLint(value, path, errs)
	if !ok {
		return
	}
	for i, name := range names {
		if _, exists := c.Profiles[name]; !exists {
			*errs = append(*errs, newLintError(fmt.Sprintf("%s[%d]", path, i), "references unknown profile %q", name))
		}
	}
}

func stringSliceForLint(value interface{}, path string, errs *[]LintError) ([]string, bool) {
	switch list := value.(type) {
	case []string:
		if len(list) == 0 {
			*errs = append(*errs, newLintError(path, "must not be empty"))
			return nil, false
		}
		result := make([]string, len(list))
		copy(result, list)
		valid := true
		for i, item := range result {
			if item == "" {
				*errs = append(*errs, newLintError(fmt.Sprintf("%s[%d]", path, i), "cannot be empty"))
				valid = false
			}
		}
		return result, valid
	case []interface{}:
		if len(list) == 0 {
			*errs = append(*errs, newLintError(path, "must not be empty"))
			return nil, false
		}
		result := make([]string, len(list))
		valid := true
		for i, item := range list {
			str, ok := stringForLint(item, fmt.Sprintf("%s[%d]", path, i), false, errs)
			if !ok {
				valid = false
				continue
			}
			result[i] = str
		}
		return result, valid
	default:
		*errs = append(*errs, newLintError(path, "must be a list of strings"))
		return nil, false
	}
}

func stringForLint(v interface{}, path string, allowEmpty bool, errs *[]LintError) (string, bool) {
	s, ok := v.(string)
	if !ok {
		*errs = append(*errs, newLintError(path, "must be a string"))
		return "", false
	}
	if !allowEmpty && s == "" {
		*errs = append(*errs, newLintError(path, "cannot be empty"))
		return "", false
	}
	return s, true
}

// Load reads and validates a configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LintFile reads a configuration file and returns all validation errors.
func LintFile(path string) ([]LintError, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return cfg.Lint(), nil
}

// Parse decodes raw configuration data and applies default values without validation.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if !c.toleranceSet {
		c.TolerancePx = 2.0
		c.toleranceSet = true
	}
}

// Validate performs basic sanity checks and returns the first error encountered.
func (c *Config) Validate() error {
	errs := c.Lint()
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf(errs[0].Error())
}

// Validate ensures matcher configuration has at least one selection criteria.
func (m MatcherConfig) Validate() error {
	if m.Class == "" && len(m.AnyClass) == 0 && m.TitleRegex == "" {
		return fmt.Errorf("must define class, anyClass, or titleRegex")
	}
	return nil
}
