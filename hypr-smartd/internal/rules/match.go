package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hyprpal/hypr-smartd/internal/config"
	"github.com/hyprpal/hypr-smartd/internal/state"
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
	var preds []Predicate
	if len(pc.All) > 0 {
		childPreds := make([]Predicate, 0, len(pc.All))
		for _, child := range pc.All {
			p, err := BuildPredicate(child)
			if err != nil {
				return nil, err
			}
			childPreds = append(childPreds, p)
		}
		preds = append(preds, func(ctx EvalContext) bool {
			for _, p := range childPreds {
				if !p(ctx) {
					return false
				}
			}
			return true
		})
	}
	if len(pc.Any) > 0 {
		childPreds := make([]Predicate, 0, len(pc.Any))
		for _, child := range pc.Any {
			p, err := BuildPredicate(child)
			if err != nil {
				return nil, err
			}
			childPreds = append(childPreds, p)
		}
		preds = append(preds, func(ctx EvalContext) bool {
			for _, p := range childPreds {
				if p(ctx) {
					return true
				}
			}
			return false
		})
	}
	if pc.Not != nil {
		child, err := BuildPredicate(*pc.Not)
		if err != nil {
			return nil, err
		}
		preds = append(preds, func(ctx EvalContext) bool { return !child(ctx) })
	}
	if pc.Mode != "" {
		expected := strings.ToLower(pc.Mode)
		preds = append(preds, func(ctx EvalContext) bool {
			return strings.ToLower(ctx.Mode) == expected
		})
	}
	if pc.AppClass != "" {
		expected := strings.ToLower(pc.AppClass)
		preds = append(preds, func(ctx EvalContext) bool {
			if c := ctx.World.ActiveClient(); c != nil {
				return strings.ToLower(c.Class) == expected
			}
			return false
		})
	}
	if pc.TitleRegex != "" {
		rgx, err := regexp.Compile(pc.TitleRegex)
		if err != nil {
			return nil, fmt.Errorf("compile title regex: %w", err)
		}
		preds = append(preds, func(ctx EvalContext) bool {
			if c := ctx.World.ActiveClient(); c != nil {
				return rgx.MatchString(c.Title)
			}
			return false
		})
	}
	if len(pc.AppsPresent) > 0 {
		wanted := make([]string, len(pc.AppsPresent))
		for i, w := range pc.AppsPresent {
			wanted[i] = strings.ToLower(w)
		}
		preds = append(preds, func(ctx EvalContext) bool {
			present := map[string]struct{}{}
			for _, c := range ctx.World.Clients {
				present[strings.ToLower(c.Class)] = struct{}{}
			}
			for _, w := range wanted {
				if _, ok := present[w]; !ok {
					return false
				}
			}
			return true
		})
	}
	if pc.WorkspaceID != 0 {
		expected := pc.WorkspaceID
		preds = append(preds, func(ctx EvalContext) bool {
			return ctx.World.ActiveWorkspaceID == expected
		})
	}
	if pc.MonitorName != "" {
		name := strings.ToLower(pc.MonitorName)
		preds = append(preds, func(ctx EvalContext) bool {
			mon, err := ctx.World.MonitorForWorkspace(ctx.World.ActiveWorkspaceID)
			if err != nil {
				return false
			}
			return strings.ToLower(mon.Name) == name
		})
	}

	if len(preds) == 0 {
		return func(ctx EvalContext) bool { return true }, nil
	}

	return func(ctx EvalContext) bool {
		for _, p := range preds {
			if !p(ctx) {
				return false
			}
		}
		return true
	}, nil
}
