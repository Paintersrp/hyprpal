package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration document.
type Config struct {
	ManagedWorkspaces []int           `yaml:"managedWorkspaces"`
	Modes             []ModeConfig    `yaml:"modes"`
	RedactTitles      bool            `yaml:"redactTitles"`
	Gaps              Gaps            `yaml:"gaps"`
	TolerancePx       float64         `yaml:"tolerancePx"`
	Profiles          MatcherProfiles `yaml:"profiles"`
}

// UnmarshalYAML handles deprecated fields while decoding configuration files.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig struct {
		ManagedWorkspaces []int           `yaml:"managedWorkspaces"`
		Modes             []ModeConfig    `yaml:"modes"`
		RedactTitles      bool            `yaml:"redactTitles"`
		Gaps              Gaps            `yaml:"gaps"`
		TolerancePx       *float64        `yaml:"tolerancePx"`
		LegacyTolerancePx *float64        `yaml:"placementTolerancePx"`
		Profiles          MatcherProfiles `yaml:"profiles"`
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

	switch {
	case raw.TolerancePx != nil:
		c.TolerancePx = *raw.TolerancePx
	case raw.LegacyTolerancePx != nil:
		c.TolerancePx = *raw.LegacyTolerancePx
	default:
		c.TolerancePx = 0
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
	Name           string          `yaml:"name"`
	When           PredicateConfig `yaml:"when"`
	Actions        []ActionConfig  `yaml:"actions"`
	DebounceMs     int             `yaml:"debounceMs"`
	AllowUnmanaged bool            `yaml:"allowUnmanaged"`
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
	if c.TolerancePx == 0 {
		c.TolerancePx = 2.0
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
	return nil
}

func (c *Config) validateMatcherReferences(mode string, rule RuleConfig) error {
	for _, action := range rule.Actions {
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
		profileName, ok := profileNameRaw.(string)
		if !ok {
			return fmt.Errorf("rule %q in mode %q: match.profile must be a string", rule.Name, mode)
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
