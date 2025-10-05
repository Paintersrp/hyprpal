package main

import (
	"math"
	"runtime"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	cases := []struct {
		name     string
		values   []time.Duration
		p        float64
		expected time.Duration
	}{
		{
			name:     "empty",
			values:   nil,
			p:        0.5,
			expected: 0,
		},
		{
			name:     "lower bound",
			values:   []time.Duration{time.Millisecond, 2 * time.Millisecond},
			p:        -0.1,
			expected: time.Millisecond,
		},
		{
			name:     "upper bound",
			values:   []time.Duration{time.Millisecond, 2 * time.Millisecond},
			p:        1.2,
			expected: 2 * time.Millisecond,
		},
		{
			name:     "median",
			values:   []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
			p:        0.5,
			expected: 2 * time.Millisecond,
		},
		{
			name:     "p95",
			values:   []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond, 5 * time.Millisecond},
			p:        0.95,
			expected: 5 * time.Millisecond,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.values, tc.p); got != tc.expected {
				t.Fatalf("percentile(%s, %f) = %s, want %s", tc.name, tc.p, got, tc.expected)
			}
		})
	}
}

func TestEventsPerSecond(t *testing.T) {
	cases := []struct {
		name     string
		total    time.Duration
		events   int
		expected float64
	}{
		{name: "zero duration", total: 0, events: 10, expected: 0},
		{name: "zero events", total: time.Second, events: 0, expected: 0},
		{name: "positive", total: 10 * time.Millisecond, events: 4, expected: 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eventsPerSecond(tc.total, tc.events)
			if math.Abs(got-tc.expected) > 1e-9 {
				t.Fatalf("eventsPerSecond(%s) = %f, want %f", tc.name, got, tc.expected)
			}
		})
	}
}

func TestSafeDivide(t *testing.T) {
	cases := []struct {
		total    int
		count    int
		expected float64
	}{
		{total: 10, count: 2, expected: 5},
		{total: 0, count: 10, expected: 0},
		{total: 10, count: 0, expected: 0},
	}

	for _, tc := range cases {
		got := safeDivide(tc.total, tc.count)
		if math.Abs(got-tc.expected) > 1e-9 {
			t.Fatalf("safeDivide(%d, %d) = %f, want %f", tc.total, tc.count, got, tc.expected)
		}
	}
}

func TestBuildReport(t *testing.T) {
	fixture := benchFixture{
		Name:   "test",
		Events: []benchEvent{{}, {}},
	}
	durations := []time.Duration{
		time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
	}
	start := runtime.MemStats{Mallocs: 1000, TotalAlloc: 4096}
	end := runtime.MemStats{Mallocs: 1500, TotalAlloc: 8192}

	summary := buildReport(fixture, "Coding", 2, durations, 8, start, end).Summary

	if summary.TotalEvents != 4 {
		t.Fatalf("TotalEvents = %d, want 4", summary.TotalEvents)
	}
	if summary.Dispatches.Total != 8 {
		t.Fatalf("Dispatches.Total = %d, want 8", summary.Dispatches.Total)
	}
	if math.Abs(summary.Allocations.PerEvent-125) > 1e-9 {
		t.Fatalf("Allocations.PerEvent = %f, want 125", summary.Allocations.PerEvent)
	}
	if math.Abs(summary.Allocations.BytesPerEvent-1024) > 1e-9 {
		t.Fatalf("Allocations.BytesPerEvent = %f, want 1024", summary.Allocations.BytesPerEvent)
	}
	if math.Abs(summary.EventsPerSecond-400) > 1e-6 {
		t.Fatalf("EventsPerSecond = %f, want 400", summary.EventsPerSecond)
	}
}

func TestParseEventLog(t *testing.T) {
	input := `
# comment
openwindow>>0xabc,3,Class,Title

activewindow>>0xabc
movewindow>>0xabc,3
# trailing comment
`
	events, err := parseEventLog(input)
	if err != nil {
		t.Fatalf("parseEventLog returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Event.Kind != "openwindow" || events[0].Event.Payload != "0xabc,3,Class,Title" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Event.Kind != "activewindow" || events[1].Event.Payload != "0xabc" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Event.Kind != "movewindow" || events[2].Event.Payload != "0xabc,3" {
		t.Fatalf("unexpected third event: %+v", events[2])
	}
}
