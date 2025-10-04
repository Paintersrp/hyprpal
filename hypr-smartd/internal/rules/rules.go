package rules

import (
	"fmt"
	"time"

	"github.com/hyprpal/hypr-smartd/internal/config"
)

// Rule represents a compiled rule ready for evaluation.
type Rule struct {
	Name     string
	When     Predicate
	Actions  []Action
	Debounce time.Duration
}

// Mode aggregates rules under a named mode.
type Mode struct {
	Name  string
	Rules []Rule
}

// BuildModes compiles configuration into executable rule sets.
func BuildModes(cfg *config.Config) ([]Mode, error) {
	modes := make([]Mode, 0, len(cfg.Modes))
	for _, mode := range cfg.Modes {
		compiled := Mode{Name: mode.Name}
		for _, rc := range mode.Rules {
			pred, err := BuildPredicate(rc.When)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rc.Name, err)
			}
			acts, err := BuildActions(rc.Actions)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rc.Name, err)
			}
			debounce := time.Duration(rc.DebounceMs) * time.Millisecond
			if debounce == 0 {
				debounce = 500 * time.Millisecond
			}
			compiled.Rules = append(compiled.Rules, Rule{
				Name:     rc.Name,
				When:     pred,
				Actions:  acts,
				Debounce: debounce,
			})
		}
		modes = append(modes, compiled)
	}
	return modes, nil
}
