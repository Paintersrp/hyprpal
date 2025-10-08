package metrics

import (
	"sort"
	"sync"
	"time"
)

// Collector aggregates anonymous telemetry counters for rule execution.
type Collector struct {
	mu      sync.RWMutex
	enabled bool
	started time.Time
	rules   map[string]*RuleMetrics
}

// RuleMetrics captures per-rule counters tracked by the collector.
type RuleMetrics struct {
	Mode           string    `json:"mode"`
	Rule           string    `json:"rule"`
	Matched        uint64    `json:"matched"`
	Applied        uint64    `json:"applied"`
	DispatchErrors uint64    `json:"dispatchErrors"`
	LastMatched    time.Time `json:"lastMatched,omitempty"`
	LastApplied    time.Time `json:"lastApplied,omitempty"`
	LastErrored    time.Time `json:"lastErrored,omitempty"`
}

// Totals aggregates counters across all rules in a snapshot.
type Totals struct {
	Matched        uint64 `json:"matched"`
	Applied        uint64 `json:"applied"`
	DispatchErrors uint64 `json:"dispatchErrors"`
}

// Snapshot is the serializable view of the current metrics state.
type Snapshot struct {
	Enabled bool          `json:"enabled"`
	Started time.Time     `json:"started,omitempty"`
	Totals  Totals        `json:"totals"`
	Rules   []RuleMetrics `json:"rules,omitempty"`
}

// NewCollector returns a collector with the provided opt-in state.
func NewCollector(enabled bool) *Collector {
	c := &Collector{}
	c.SetEnabled(enabled)
	return c
}

// Enabled reports whether telemetry collection is currently active.
func (c *Collector) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.enabled
}

// SetEnabled toggles telemetry collection, resetting counters when enabling.
func (c *Collector) SetEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled == enabled {
		return
	}
	c.enabled = enabled
	if !enabled {
		c.rules = nil
		c.started = time.Time{}
		return
	}
	c.started = time.Now()
	c.rules = make(map[string]*RuleMetrics)
}

// RecordMatch increments the matched counter for a rule.
func (c *Collector) RecordMatch(mode, rule string) {
	c.updateRule(mode, rule, func(metrics *RuleMetrics, now time.Time) {
		metrics.Matched++
		metrics.LastMatched = now
	})
}

// RecordApplied increments the applied counter for a rule.
func (c *Collector) RecordApplied(mode, rule string) {
	c.updateRule(mode, rule, func(metrics *RuleMetrics, now time.Time) {
		metrics.Applied++
		metrics.LastApplied = now
	})
}

// RecordDispatchError increments the dispatch error counter for a rule.
func (c *Collector) RecordDispatchError(mode, rule string) {
	c.updateRule(mode, rule, func(metrics *RuleMetrics, now time.Time) {
		metrics.DispatchErrors++
		metrics.LastErrored = now
	})
}

func (c *Collector) updateRule(mode, rule string, mutate func(*RuleMetrics, time.Time)) {
	if c == nil || mutate == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	if c.rules == nil {
		c.rules = make(map[string]*RuleMetrics)
	}
	key := mode + ":" + rule
	metrics, exists := c.rules[key]
	if !exists {
		metrics = &RuleMetrics{Mode: mode, Rule: rule}
		c.rules[key] = metrics
	}
	mutate(metrics, now)
}

// Snapshot returns the current counters for serialization or display.
func (c *Collector) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := Snapshot{Enabled: c.enabled}
	if !c.enabled {
		return snap
	}
	snap.Started = c.started
	if len(c.rules) == 0 {
		return snap
	}
	snap.Rules = make([]RuleMetrics, 0, len(c.rules))
	for _, metrics := range c.rules {
		if metrics == nil {
			continue
		}
		clone := *metrics
		snap.Rules = append(snap.Rules, clone)
		snap.Totals.Matched += clone.Matched
		snap.Totals.Applied += clone.Applied
		snap.Totals.DispatchErrors += clone.DispatchErrors
	}
	sort.Slice(snap.Rules, func(i, j int) bool {
		if snap.Rules[i].Mode == snap.Rules[j].Mode {
			return snap.Rules[i].Rule < snap.Rules[j].Rule
		}
		return snap.Rules[i].Mode < snap.Rules[j].Mode
	})
	return snap
}
