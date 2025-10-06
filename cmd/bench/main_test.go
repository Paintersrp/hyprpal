package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestPrintHumanSummary(t *testing.T) {
	summary := benchSummary{
		Fixture:            "test",
		Mode:               "Coding",
		Iterations:         2,
		WarmupIterations:   1,
		EventsPerIteration: 3,
		TotalEvents:        6,
		Dispatches: benchDispatchStats{
			Total:        12,
			PerIteration: 6,
			PerEvent:     2,
		},
		Latency: benchLatencyStats{
			Min:    1.0,
			Mean:   2.0,
			Median: 1.5,
			P95:    3.5,
			Max:    4.0,
		},
		IterationDuration: benchLatencyStats{
			Min:    10.0,
			Mean:   12.5,
			Median: 15.0,
			P95:    18.0,
			Max:    20.0,
		},
		Allocations: benchAllocationStats{
			Total:               120,
			PerEvent:            20,
			BytesTotal:          4096,
			BytesPerEvent:       512,
			HeapAllocDelta:      1024,
			HeapObjectsDelta:    12,
			HeapObjectsPerEvent: 2,
		},
		EventsPerSecond: 300,
	}

	var buf bytes.Buffer
	if err := printHumanSummary(summary, &buf); err != nil {
		t.Fatalf("printHumanSummary returned error: %v", err)
	}

	output := buf.String()
	checks := []string{
		"Fixture:                  test",
		"Mode:                     Coding",
		"Dispatches:               12 (6.00 / iter, 2.00 / event)",
		"Warmup iterations:        1",
		"Latency (ms):             min 1.00 | mean 2.00 | median 1.50 | p95 3.50 | max 4.00",
		"Iteration duration (ms):  min 10.00 | mean 12.50 | median 15.00 | p95 18.00 | max 20.00",
		"Allocations:              120 total (20.00 / event)",
		"Heap delta:               1024 B (0.00 MiB) change, 12 objects (2.00 / event)",
	}
	for _, c := range checks {
		if !strings.Contains(output, c) {
			t.Fatalf("expected summary to contain %q, got:\n%s", c, output)
		}
	}
}

func TestBuildReportIncludesWarmup(t *testing.T) {
	report := buildReport(
		benchFixture{Name: "fixture", Events: []benchEvent{{}}},
		"Coding",
		2,
		3,
		[]time.Duration{time.Millisecond},
		[]time.Duration{2 * time.Millisecond},
		[]int{4},
		4,
		runtime.MemStats{},
		runtime.MemStats{},
	)

	if report.Summary.WarmupIterations != 3 {
		t.Fatalf("expected warmup to be 3, got %d", report.Summary.WarmupIterations)
	}
}

func TestWriteEventTrace(t *testing.T) {
	if err := writeEventTrace(nil, ""); err != nil {
		t.Fatalf("writeEventTrace with empty path returned error: %v", err)
	}

	traces := []benchEventTrace{{
		Iteration:  1,
		EventIndex: 2,
		Kind:       "openwindow",
		Payload:    "0x123, payload",
		DurationMs: 1.25,
		Dispatches: 3,
	}}

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")
	if err := writeEventTrace(traces, path); err != nil {
		t.Fatalf("writeEventTrace returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}

	var decoded []benchEventTrace
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}

	if len(decoded) != len(traces) {
		t.Fatalf("decoded length = %d, want %d", len(decoded), len(traces))
	}
	if decoded[0] != traces[0] {
		t.Fatalf("decoded trace mismatch: %+v vs %+v", decoded[0], traces[0])
	}
}

func TestWriteReportCreatesDirAndSkipsHTMLEscape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "report.json")
	report := benchReport{
		Summary: benchSummary{Fixture: "<fixture>", Mode: "Coding"},
	}

	if err := writeReport(report, path); err != nil {
		t.Fatalf("writeReport returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	contents := string(raw)
	if !strings.Contains(contents, "\"fixture\": \"<fixture>\"") {
		t.Fatalf("expected raw angle brackets in output, got:\n%s", contents)
	}
}

func TestLoadFixtureJSONMergesBase(t *testing.T) {
	base := defaultFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	data := `{
  "name": "custom",
  "mode": "Coding",
  "activeWorkspace": 9,
  "activeClient": "0xcustom",
  "events": [
    {"kind": "noop", "payload": "ignored"},
    {"kind": "delayed", "payload": "payload", "delay": "15ms"}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fixture, err := loadFixture(path, base)
	if err != nil {
		t.Fatalf("loadFixture returned error: %v", err)
	}

	if fixture.Name != "custom" {
		t.Fatalf("Name = %q, want custom", fixture.Name)
	}
	if fixture.Mode != "Coding" {
		t.Fatalf("Mode = %q, want Coding", fixture.Mode)
	}
	if fixture.ActiveWorkspace != 9 {
		t.Fatalf("ActiveWorkspace = %d, want 9", fixture.ActiveWorkspace)
	}
	if fixture.ActiveClient != "0xcustom" {
		t.Fatalf("ActiveClient = %q, want 0xcustom", fixture.ActiveClient)
	}
	if got, want := len(fixture.Clients), len(base.Clients); got != want {
		t.Fatalf("Clients length = %d, want %d (base copied)", got, want)
	}
	if len(fixture.Events) != 2 {
		t.Fatalf("Events length = %d, want 2", len(fixture.Events))
	}
	if fixture.Events[1].Delay != 15*time.Millisecond {
		t.Fatalf("second event delay = %s, want 15ms", fixture.Events[1].Delay)
	}
}

func TestLoadFixtureEventLog(t *testing.T) {
	base := defaultFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.log")
	log := "# comment\nopenwindow>>0xabc, 3, app, Title\nactivewindow>>0xabc\n"
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	fixture, err := loadFixture(path, base)
	if err != nil {
		t.Fatalf("loadFixture returned error: %v", err)
	}

	if fixture.Name != base.Name {
		t.Fatalf("Name = %q, want %q", fixture.Name, base.Name)
	}
	if len(fixture.Events) != 2 {
		t.Fatalf("Events length = %d, want 2", len(fixture.Events))
	}
	if fixture.Events[0].Event.Kind != "openwindow" {
		t.Fatalf("first event kind = %q, want openwindow", fixture.Events[0].Event.Kind)
	}
	if fixture.Events[1].Event.Payload != "0xabc" {
		t.Fatalf("second event payload = %q, want 0xabc", fixture.Events[1].Event.Payload)
	}
	if len(fixture.Clients) != len(base.Clients) {
		t.Fatalf("Clients length = %d, want %d (base copied)", len(fixture.Clients), len(base.Clients))
	}
}

func TestLoadFixtureInvalidDelay(t *testing.T) {
	base := defaultFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	data := `{"events":[{"kind":"noop","delay":"totally-not-a-duration"}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := loadFixture(path, base)
	if err == nil {
		t.Fatal("expected error for invalid delay, got nil")
	}
	if !strings.Contains(err.Error(), "parse delay") {
		t.Fatalf("unexpected error for invalid delay: %v", err)
	}
}

func TestFormatBytesSigned(t *testing.T) {
	if got := formatBytesSigned(0); got != "0 B (0.00 MiB)" {
		t.Fatalf("formatBytesSigned(0) = %q", got)
	}
	if got := formatBytesSigned(1024); got != "1024 B (0.00 MiB)" {
		t.Fatalf("formatBytesSigned(1024) = %q", got)
	}
	if got := formatBytesSigned(-2048); got != "-2048 B (0.00 MiB)" {
		t.Fatalf("formatBytesSigned(-2048) = %q", got)
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
	start := runtime.MemStats{Mallocs: 1000, TotalAlloc: 4096, HeapAlloc: 2048, HeapObjects: 200}
	end := runtime.MemStats{Mallocs: 1500, TotalAlloc: 8192, HeapAlloc: 3072, HeapObjects: 260}
	iterationDurations := []time.Duration{10 * time.Millisecond, 12 * time.Millisecond}
	iterationDispatches := []int{5, 3}

	report := buildReport(fixture, "Coding", 2, 0, durations, iterationDurations, iterationDispatches, 8, start, end)
	summary := report.Summary

	if summary.TotalEvents != 4 {
		t.Fatalf("TotalEvents = %d, want 4", summary.TotalEvents)
	}
	if summary.Dispatches.Total != 8 {
		t.Fatalf("Dispatches.Total = %d, want 8", summary.Dispatches.Total)
	}
	if math.Abs(summary.Dispatches.PerEvent-2) > 1e-9 {
		t.Fatalf("Dispatches.PerEvent = %f, want 2", summary.Dispatches.PerEvent)
	}
	if math.Abs(summary.Allocations.PerEvent-125) > 1e-9 {
		t.Fatalf("Allocations.PerEvent = %f, want 125", summary.Allocations.PerEvent)
	}
	if summary.WarmupIterations != 0 {
		t.Fatalf("WarmupIterations = %d, want 0", summary.WarmupIterations)
	}
	if math.Abs(summary.Allocations.BytesPerEvent-1024) > 1e-9 {
		t.Fatalf("Allocations.BytesPerEvent = %f, want 1024", summary.Allocations.BytesPerEvent)
	}
	if math.Abs(summary.EventsPerSecond-400) > 1e-6 {
		t.Fatalf("EventsPerSecond = %f, want 400", summary.EventsPerSecond)
	}
	if summary.Allocations.HeapAllocDelta != 1024 {
		t.Fatalf("Allocations.HeapAllocDelta = %d, want 1024", summary.Allocations.HeapAllocDelta)
	}
	if math.Abs(summary.Allocations.HeapAllocPerEvent-256) > 1e-9 {
		t.Fatalf("Allocations.HeapAllocPerEvent = %f, want 256", summary.Allocations.HeapAllocPerEvent)
	}
	if summary.Allocations.HeapObjectsDelta != 60 {
		t.Fatalf("Allocations.HeapObjectsDelta = %d, want 60", summary.Allocations.HeapObjectsDelta)
	}
	if math.Abs(summary.Allocations.HeapObjectsPerEvent-15) > 1e-9 {
		t.Fatalf("Allocations.HeapObjectsPerEvent = %f, want 15", summary.Allocations.HeapObjectsPerEvent)
	}
	if math.Abs(summary.IterationDuration.Mean-11) > 1e-9 {
		t.Fatalf("IterationDuration.Mean = %f, want 11", summary.IterationDuration.Mean)
	}
	if summary.IterationDuration.Min != 10 {
		t.Fatalf("IterationDuration.Min = %f, want 10", summary.IterationDuration.Min)
	}
	if summary.IterationDuration.Max != 12 {
		t.Fatalf("IterationDuration.Max = %f, want 12", summary.IterationDuration.Max)
	}
	if len(report.Iterations) != 2 {
		t.Fatalf("expected 2 iteration entries, got %d", len(report.Iterations))
	}
	iter := report.Iterations[0]
	if iter.Index != 1 || iter.Dispatches != 5 || iter.Events != len(fixture.Events) {
		t.Fatalf("unexpected first iteration summary: %+v", iter)
	}
	if math.Abs(iter.DurationMs-10) > 1e-9 {
		t.Fatalf("expected first iteration duration 10ms, got %f", iter.DurationMs)
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

func TestLoadFixtureJSONFallbacksToBase(t *testing.T) {
	base := defaultFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	payload := `{
  "mode": "Coding"
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fixture, err := loadFixture(path, base)
	if err != nil {
		t.Fatalf("loadFixture returned error: %v", err)
	}
	if fixture.Mode != "Coding" {
		t.Fatalf("expected mode Coding, got %q", fixture.Mode)
	}
	if len(fixture.Events) != len(base.Events) {
		t.Fatalf("expected %d events, got %d", len(base.Events), len(fixture.Events))
	}
	if len(fixture.Clients) != len(base.Clients) {
		t.Fatalf("expected %d clients, got %d", len(base.Clients), len(fixture.Clients))
	}
	if fixture.ActiveWorkspace != base.ActiveWorkspace {
		t.Fatalf("expected active workspace %d, got %d", base.ActiveWorkspace, fixture.ActiveWorkspace)
	}
}

func TestLoadFixtureJSONFallsBackToBaseMode(t *testing.T) {
	base := defaultFixture()
	base.Mode = "Coding"
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	payload := `{
  "name": "custom",
  "events": [
    {"kind": "activewindow", "payload": "0xabc"}
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fixture, err := loadFixture(path, base)
	if err != nil {
		t.Fatalf("loadFixture returned error: %v", err)
	}
	if fixture.Mode != base.Mode {
		t.Fatalf("expected mode fallback to %q, got %q", base.Mode, fixture.Mode)
	}
	if fixture.Name != "custom" {
		t.Fatalf("expected name custom, got %q", fixture.Name)
	}
	if len(fixture.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(fixture.Events))
	}
}

func TestLoadFixtureJSONParsesDelay(t *testing.T) {
	base := defaultFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	payload := `{
  "events": [
    {"kind": " activewindow ", "payload": "0xabc", "delay": "15ms"}
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fixture, err := loadFixture(path, base)
	if err != nil {
		t.Fatalf("loadFixture returned error: %v", err)
	}
	if len(fixture.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(fixture.Events))
	}
	ev := fixture.Events[0]
	if ev.Event.Kind != "activewindow" {
		t.Fatalf("expected kind activewindow, got %q", ev.Event.Kind)
	}
	if ev.Delay != 15*time.Millisecond {
		t.Fatalf("expected delay 15ms, got %s", ev.Delay)
	}
}
