package rules

import (
	"fmt"
	"sort"
	"time"

	"github.com/hyprpal/hyprpal/internal/config"
)

// Rule represents a compiled rule ready for evaluation.
type Rule struct {
	Name              string
	When              Predicate
	Tracer            *PredicateTracer
	Actions           []Action
	Debounce          time.Duration
	MutateUnmanaged   bool
	ManagedWorkspaces map[int]struct{}
	Priority          int
}

// Mode aggregates rules under a named mode.
type Mode struct {
	Name           string
	Rules          []Rule
	PriorityGroups []PriorityGroup
}

// PriorityGroup holds rules that share the same priority level.
type PriorityGroup struct {
	Priority int
	Rules    []Rule
}

// BuildModes compiles configuration into executable rule sets.
func BuildModes(cfg *config.Config) ([]Mode, error) {
	modes := make([]Mode, 0, len(cfg.Modes))
	managed := map[int]struct{}{}
	for _, ws := range cfg.ManagedWorkspaces {
		managed[ws] = struct{}{}
	}
	for _, mode := range cfg.Modes {
		compiled := Mode{Name: mode.Name}
		for _, rc := range mode.Rules {
			pred, tracer, err := BuildPredicateWithTrace(rc.When)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rc.Name, err)
			}
			acts, err := BuildActions(rc.Actions, cfg.Profiles)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rc.Name, err)
			}
			debounce := time.Duration(rc.DebounceMs) * time.Millisecond
			if debounce == 0 {
				debounce = 500 * time.Millisecond
			}
			compiled.Rules = append(compiled.Rules, Rule{
				Name:              rc.Name,
				When:              pred,
				Tracer:            tracer,
				Actions:           acts,
				Debounce:          debounce,
				MutateUnmanaged:   rc.MutateUnmanaged,
				ManagedWorkspaces: managed,
				Priority:          rc.Priority,
			})
		}
		modes = append(modes, NormalizeMode(compiled))
	}
	return modes, nil
}

// NormalizeMode sorts rules by priority (descending) while preserving the
// original order for rules with the same priority, and builds the grouped view.
func NormalizeMode(mode Mode) Mode {
	if len(mode.Rules) == 0 {
		mode.PriorityGroups = nil
		return mode
	}

	sort.SliceStable(mode.Rules, func(i, j int) bool {
		if mode.Rules[i].Priority == mode.Rules[j].Priority {
			return false
		}
		return mode.Rules[i].Priority > mode.Rules[j].Priority
	})

	groups := make([]PriorityGroup, 0)
	for _, rule := range mode.Rules {
		if len(groups) == 0 || groups[len(groups)-1].Priority != rule.Priority {
			groups = append(groups, PriorityGroup{Priority: rule.Priority})
		}
		idx := len(groups) - 1
		groups[idx].Rules = append(groups[idx].Rules, rule)
	}
	mode.PriorityGroups = groups
	return mode
}
