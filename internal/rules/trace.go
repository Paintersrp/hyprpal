package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hyprpal/hyprpal/internal/config"
)

type traceNode interface {
	trace(ctx EvalContext) (bool, *PredicateTrace)
}

type predicateEntry struct {
	fn   Predicate
	node traceNode
}

func compilePredicate(pc config.PredicateConfig) (Predicate, traceNode, error) {
	entries := make([]predicateEntry, 0)

	if len(pc.All) > 0 {
		childPreds := make([]Predicate, 0, len(pc.All))
		childNodes := make([]traceNode, 0, len(pc.All))
		for _, child := range pc.All {
			pred, node, err := compilePredicate(child)
			if err != nil {
				return nil, nil, err
			}
			childPreds = append(childPreds, pred)
			childNodes = append(childNodes, node)
		}
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				for _, p := range childPreds {
					if !p(ctx) {
						return false
					}
				}
				return true
			},
			node: &allNode{children: childNodes},
		})
	}

	if len(pc.Any) > 0 {
		childPreds := make([]Predicate, 0, len(pc.Any))
		childNodes := make([]traceNode, 0, len(pc.Any))
		for _, child := range pc.Any {
			pred, node, err := compilePredicate(child)
			if err != nil {
				return nil, nil, err
			}
			childPreds = append(childPreds, pred)
			childNodes = append(childNodes, node)
		}
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				for _, p := range childPreds {
					if p(ctx) {
						return true
					}
				}
				return false
			},
			node: &anyNode{children: childNodes},
		})
	}

	if pc.Not != nil {
		pred, node, err := compilePredicate(*pc.Not)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, predicateEntry{
			fn:   func(ctx EvalContext) bool { return !pred(ctx) },
			node: &notNode{child: node},
		})
	}

	if pc.Mode != "" {
		expected := strings.ToLower(pc.Mode)
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				return strings.ToLower(ctx.Mode) == expected
			},
			node: &modeNode{expected: expected, raw: pc.Mode},
		})
	}

	if pc.AppClass != "" {
		expected := strings.ToLower(pc.AppClass)
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				if c := ctx.World.ActiveClient(); c != nil {
					return strings.ToLower(c.Class) == expected
				}
				return false
			},
			node: &appClassNode{expected: expected, raw: pc.AppClass},
		})
	}

	if pc.TitleRegex != "" {
		rgx, err := regexp.Compile(pc.TitleRegex)
		if err != nil {
			return nil, nil, fmt.Errorf("compile title regex: %w", err)
		}
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				if c := ctx.World.ActiveClient(); c != nil {
					return rgx.MatchString(c.Title)
				}
				return false
			},
			node: &titleRegexNode{pattern: pc.TitleRegex, regex: rgx},
		})
	}

	if len(pc.AppsPresent) > 0 {
		wanted := make([]string, len(pc.AppsPresent))
		for i, w := range pc.AppsPresent {
			wanted[i] = strings.ToLower(w)
		}
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
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
			},
			node: &appsPresentNode{expected: wanted, raw: append([]string(nil), pc.AppsPresent...)},
		})
	}

	if pc.WorkspaceID != 0 {
		expected := pc.WorkspaceID
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				return ctx.World.ActiveWorkspaceID == expected
			},
			node: &workspaceNode{expected: expected},
		})
	}

	if pc.MonitorName != "" {
		name := strings.ToLower(pc.MonitorName)
		entries = append(entries, predicateEntry{
			fn: func(ctx EvalContext) bool {
				mon, err := ctx.World.MonitorForWorkspace(ctx.World.ActiveWorkspaceID)
				if err != nil {
					return false
				}
				return strings.ToLower(mon.Name) == name
			},
			node: &monitorNode{expected: name, raw: pc.MonitorName},
		})
	}

	if len(entries) == 0 {
		return func(ctx EvalContext) bool { return true }, &rootNode{}, nil
	}

	preds := make([]Predicate, len(entries))
	nodes := make([]traceNode, len(entries))
	for i, entry := range entries {
		preds[i] = entry.fn
		nodes[i] = entry.node
	}

	combined := func(ctx EvalContext) bool {
		for _, p := range preds {
			if !p(ctx) {
				return false
			}
		}
		return true
	}

	return combined, &rootNode{children: nodes}, nil
}

// BuildPredicateWithTrace compiles the predicate and returns a tracer for inspection.
func BuildPredicateWithTrace(pc config.PredicateConfig) (Predicate, *PredicateTracer, error) {
	pred, node, err := compilePredicate(pc)
	if err != nil {
		return nil, nil, err
	}
	return pred, &PredicateTracer{root: node}, nil
}

// PredicateTrace captures predicate evaluation decisions.
type PredicateTrace struct {
	Kind     string            `json:"kind"`
	Result   bool              `json:"result"`
	Details  map[string]any    `json:"details,omitempty"`
	Children []*PredicateTrace `json:"children,omitempty"`
}

// ClonePredicateTrace performs a deep copy of the predicate trace tree.
func ClonePredicateTrace(src *PredicateTrace) *PredicateTrace {
	if src == nil {
		return nil
	}
	clone := &PredicateTrace{
		Kind:   src.Kind,
		Result: src.Result,
	}
	if len(src.Details) > 0 {
		clone.Details = make(map[string]any, len(src.Details))
		for k, v := range src.Details {
			clone.Details[k] = v
		}
	}
	if len(src.Children) > 0 {
		clone.Children = make([]*PredicateTrace, len(src.Children))
		for i, child := range src.Children {
			clone.Children[i] = ClonePredicateTrace(child)
		}
	}
	return clone
}

// PredicateTracer evaluates predicates while capturing branch outcomes.
type PredicateTracer struct {
	root traceNode
}

// Trace executes the predicate and returns the boolean result alongside its trace.
func (t *PredicateTracer) Trace(ctx EvalContext) (bool, *PredicateTrace) {
	if t == nil || t.root == nil {
		return true, &PredicateTrace{Kind: "predicate", Result: true}
	}
	return t.root.trace(ctx)
}

type rootNode struct {
	children []traceNode
}

func (n *rootNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	if len(n.children) == 0 {
		return true, &PredicateTrace{Kind: "predicate", Result: true}
	}
	result := true
	traces := make([]*PredicateTrace, 0, len(n.children))
	for _, child := range n.children {
		childResult, childTrace := child.trace(ctx)
		traces = append(traces, childTrace)
		if !childResult {
			result = false
		}
	}
	return result, &PredicateTrace{Kind: "predicate", Result: result, Children: traces}
}

type allNode struct {
	children []traceNode
}

func (n *allNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	result := true
	traces := make([]*PredicateTrace, 0, len(n.children))
	for _, child := range n.children {
		childResult, childTrace := child.trace(ctx)
		traces = append(traces, childTrace)
		if !childResult {
			result = false
		}
	}
	return result, &PredicateTrace{Kind: "all", Result: result, Children: traces}
}

type anyNode struct {
	children []traceNode
}

func (n *anyNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	result := false
	traces := make([]*PredicateTrace, 0, len(n.children))
	for _, child := range n.children {
		childResult, childTrace := child.trace(ctx)
		traces = append(traces, childTrace)
		if childResult {
			result = true
		}
	}
	if len(n.children) == 0 {
		result = false
	}
	return result, &PredicateTrace{Kind: "any", Result: result, Children: traces}
}

type notNode struct {
	child traceNode
}

func (n *notNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	if n.child == nil {
		return true, &PredicateTrace{Kind: "not", Result: true}
	}
	childResult, childTrace := n.child.trace(ctx)
	result := !childResult
	return result, &PredicateTrace{Kind: "not", Result: result, Children: []*PredicateTrace{childTrace}}
}

type modeNode struct {
	expected string
	raw      string
}

func (n *modeNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	actual := strings.ToLower(ctx.Mode)
	result := actual == n.expected
	details := map[string]any{
		"expected": n.raw,
		"actual":   ctx.Mode,
	}
	return result, &PredicateTrace{Kind: "mode", Result: result, Details: details}
}

type appClassNode struct {
	expected string
	raw      string
}

func (n *appClassNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	details := map[string]any{
		"expected": n.raw,
	}
	result := false
	if c := ctx.World.ActiveClient(); c != nil {
		details["actual"] = c.Class
		result = strings.ToLower(c.Class) == n.expected
	} else {
		details["actual"] = ""
		details["hasClient"] = false
	}
	return result, &PredicateTrace{Kind: "app.class", Result: result, Details: details}
}

type titleRegexNode struct {
	pattern string
	regex   *regexp.Regexp
}

func (n *titleRegexNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	details := map[string]any{
		"pattern": n.pattern,
	}
	result := false
	if c := ctx.World.ActiveClient(); c != nil {
		details["title"] = c.Title
		details["hasClient"] = true
		result = n.regex.MatchString(c.Title)
	} else {
		details["hasClient"] = false
	}
	return result, &PredicateTrace{Kind: "app.titleRegex", Result: result, Details: details}
}

type appsPresentNode struct {
	expected []string
	raw      []string
}

func (n *appsPresentNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	present := map[string]struct{}{}
	actual := make([]string, 0, len(ctx.World.Clients))
	for _, c := range ctx.World.Clients {
		lc := strings.ToLower(c.Class)
		present[lc] = struct{}{}
		actual = append(actual, c.Class)
	}
	result := true
	missing := make([]string, 0)
	for i, expected := range n.expected {
		if _, ok := present[expected]; !ok {
			result = false
			if i < len(n.raw) {
				missing = append(missing, n.raw[i])
			} else {
				missing = append(missing, expected)
			}
		}
	}
	details := map[string]any{
		"expected": n.raw,
	}
	if len(actual) > 0 {
		details["present"] = actual
	}
	if len(missing) > 0 {
		details["missing"] = missing
	}
	return result, &PredicateTrace{Kind: "apps.present", Result: result, Details: details}
}

type workspaceNode struct {
	expected int
}

func (n *workspaceNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	actual := ctx.World.ActiveWorkspaceID
	result := actual == n.expected
	details := map[string]any{
		"expected": n.expected,
		"actual":   actual,
	}
	return result, &PredicateTrace{Kind: "workspace.id", Result: result, Details: details}
}

type monitorNode struct {
	expected string
	raw      string
}

func (n *monitorNode) trace(ctx EvalContext) (bool, *PredicateTrace) {
	details := map[string]any{
		"expected": n.raw,
	}
	result := false
	mon, err := ctx.World.MonitorForWorkspace(ctx.World.ActiveWorkspaceID)
	if err != nil {
		details["error"] = err.Error()
	} else {
		details["actual"] = mon.Name
		result = strings.ToLower(mon.Name) == n.expected
	}
	return result, &PredicateTrace{Kind: "monitor.name", Result: result, Details: details}
}

// SummarizePredicateTrace renders a predicate trace as human-readable lines including captured values.
func SummarizePredicateTrace(trace *PredicateTrace) []string {
	if trace == nil {
		return nil
	}
	lines := make([]string, 0)
	var walk func(prefix string, node *PredicateTrace)
	walk = func(prefix string, node *PredicateTrace) {
		if node == nil {
			return
		}
		detail := formatTraceDetails(node.Details)
		line := fmt.Sprintf("%s%s => %t", prefix, node.Kind, node.Result)
		if detail != "" {
			line = fmt.Sprintf("%s %s", line, detail)
		}
		lines = append(lines, line)
		childPrefix := prefix + "  "
		for _, child := range node.Children {
			walk(childPrefix, child)
		}
	}
	walk("", trace)
	return lines
}

func formatTraceDetails(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, details[key]))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
