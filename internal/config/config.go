package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration document.
type Config struct {
	ManagedWorkspaces []int        `yaml:"managedWorkspaces"`
	Modes             []ModeConfig `yaml:"modes"`
	RedactTitles      bool         `yaml:"redactTitles"`
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate performs basic sanity checks.
func (c *Config) Validate() error {
	if len(c.Modes) == 0 {
		return fmt.Errorf("config must define at least one mode")
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
		}
	}
	return nil
}
