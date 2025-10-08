package metrics

import (
	"testing"
	"time"
)

func TestCollectorRecordsCounters(t *testing.T) {
	c := NewCollector(true)
	c.RecordMatch("Coding", "Dock")
	c.RecordApplied("Coding", "Dock")
	c.RecordDispatchError("Coding", "Dock")
	snap := c.Snapshot()
	if !snap.Enabled {
		t.Fatalf("expected snapshot to be enabled")
	}
	if snap.Totals.Matched != 1 || snap.Totals.Applied != 1 || snap.Totals.DispatchErrors != 1 {
		t.Fatalf("unexpected totals: %#v", snap.Totals)
	}
	if len(snap.Rules) != 1 {
		t.Fatalf("expected one rule in snapshot, got %d", len(snap.Rules))
	}
	rule := snap.Rules[0]
	if rule.Mode != "Coding" || rule.Rule != "Dock" {
		t.Fatalf("unexpected rule key: %#v", rule)
	}
	if rule.Matched != 1 || rule.Applied != 1 || rule.DispatchErrors != 1 {
		t.Fatalf("unexpected rule counters: %#v", rule)
	}
	if rule.LastMatched.IsZero() || rule.LastApplied.IsZero() || rule.LastErrored.IsZero() {
		t.Fatalf("expected timestamps to be recorded: %#v", rule)
	}
}

func TestCollectorToggle(t *testing.T) {
	c := NewCollector(false)
	c.RecordMatch("Coding", "Dock")
	if snap := c.Snapshot(); snap.Enabled || len(snap.Rules) != 0 {
		t.Fatalf("expected disabled snapshot: %#v", snap)
	}
	c.SetEnabled(true)
	c.RecordMatch("Coding", "Dock")
	c.RecordApplied("Coding", "Dock")
	snap := c.Snapshot()
	if !snap.Enabled || snap.Totals.Matched != 1 || snap.Totals.Applied != 1 {
		t.Fatalf("unexpected enabled snapshot: %#v", snap)
	}
	c.SetEnabled(false)
	snap = c.Snapshot()
	if snap.Enabled {
		t.Fatalf("expected disabled after toggle")
	}
	if !snap.Started.IsZero() {
		t.Fatalf("expected started timestamp reset, got %v", snap.Started)
	}
	time.Sleep(10 * time.Millisecond)
	c.SetEnabled(true)
	c.RecordMatch("Coding", "Dock")
	snap = c.Snapshot()
	if snap.Totals.Matched != 1 {
		t.Fatalf("expected counters to reset after re-enable: %#v", snap)
	}
}
