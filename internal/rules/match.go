package rules

import (
	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/state"
)

// EvalContext contains world snapshot and currently selected mode.
type EvalContext struct {
	Mode  string
	World *state.World
}

// Predicate evaluates the context and returns true or false.
type Predicate func(ctx EvalContext) bool

// BuildPredicate compiles a predicate configuration into an evaluator.
func BuildPredicate(pc config.PredicateConfig) (Predicate, error) {
	pred, _, err := compilePredicate(pc)
	return pred, err
}
