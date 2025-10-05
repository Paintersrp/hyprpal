package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/ipc"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type benchFixture struct {
	Name            string
	Mode            string
	Clients         []state.Client
	Workspaces      []state.Workspace
	Monitors        []state.Monitor
	ActiveWorkspace int
	ActiveClient    string
	Events          []benchEvent
}

type benchEvent struct {
	Event ipc.Event
	Delay time.Duration
}

type benchLatencyStats struct {
	Min    float64 `json:"minMs"`
	Mean   float64 `json:"meanMs"`
	Median float64 `json:"medianMs"`
	P95    float64 `json:"p95Ms"`
	Max    float64 `json:"maxMs"`
}

type benchAllocationStats struct {
	Total               uint64  `json:"totalAllocations"`
	PerEvent            float64 `json:"allocationsPerEvent"`
	BytesTotal          uint64  `json:"bytesTotal"`
	BytesPerEvent       float64 `json:"bytesPerEvent"`
	MiBTotal            float64 `json:"miBTotal"`
	MiBPerEvent         float64 `json:"miBPerEvent"`
	HeapAllocStart      uint64  `json:"heapAllocStartBytes"`
	HeapAllocEnd        uint64  `json:"heapAllocEndBytes"`
	HeapAllocDelta      int64   `json:"heapAllocDeltaBytes"`
	HeapAllocPerEvent   float64 `json:"heapAllocDeltaPerEvent"`
	HeapObjectsStart    uint64  `json:"heapObjectsStart"`
	HeapObjectsEnd      uint64  `json:"heapObjectsEnd"`
	HeapObjectsDelta    int64   `json:"heapObjectsDelta"`
	HeapObjectsPerEvent float64 `json:"heapObjectsPerEvent"`
}

type benchDispatchStats struct {
	Total        int     `json:"total"`
	PerIteration float64 `json:"perIteration"`
	PerEvent     float64 `json:"perEvent"`
}

type benchSummary struct {
	Fixture            string               `json:"fixture"`
	Mode               string               `json:"mode"`
	Iterations         int                  `json:"iterations"`
	EventsPerIteration int                  `json:"eventsPerIteration"`
	TotalEvents        int                  `json:"totalEvents"`
	Dispatches         benchDispatchStats   `json:"dispatches"`
	Latency            benchLatencyStats    `json:"latency"`
	Allocations        benchAllocationStats `json:"allocations"`
	TotalDurationMs    float64              `json:"totalDurationMs"`
	EventsPerSecond    float64              `json:"eventsPerSecond"`
}

type benchReport struct {
	Summary     benchSummary `json:"summary"`
	DurationsMs []float64    `json:"durationsMs"`
}

type benchHyprctl struct {
	mu              sync.Mutex
	clients         []state.Client
	workspaces      []state.Workspace
	monitors        []state.Monitor
	activeWorkspace int
	activeClient    string
	dispatched      [][]string
}

func (b *benchHyprctl) ListClients(context.Context) ([]state.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	clients := make([]state.Client, len(b.clients))
	copy(clients, b.clients)
	return clients, nil
}

func (b *benchHyprctl) ListWorkspaces(context.Context) ([]state.Workspace, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	workspaces := make([]state.Workspace, len(b.workspaces))
	copy(workspaces, b.workspaces)
	return workspaces, nil
}

func (b *benchHyprctl) ListMonitors(context.Context) ([]state.Monitor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	monitors := make([]state.Monitor, len(b.monitors))
	copy(monitors, b.monitors)
	return monitors, nil
}

func (b *benchHyprctl) ActiveWorkspaceID(context.Context) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeWorkspace, nil
}

func (b *benchHyprctl) ActiveClientAddress(context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeClient, nil
}

func (b *benchHyprctl) Dispatch(args ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatched = append(b.dispatched, append([]string(nil), args...))
	return nil
}

func (b *benchHyprctl) DispatchBatch(commands [][]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, cmd := range commands {
		b.dispatched = append(b.dispatched, append([]string(nil), cmd...))
	}
	return nil
}

func (b *benchHyprctl) Dispatches() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.dispatched)
}

func main() {
	defaultConfig := filepath.Join("configs", "example.yaml")
	defaultFixturePath := filepath.Join("fixtures", "coding.json")

	cfgPath := flag.String("config", defaultConfig, "path to YAML config")
	fixturePath := flag.String("fixture", defaultFixturePath, "path to replay fixture (JSON world or event log)")
	iterations := flag.Int("iterations", 10, "number of times to replay the fixture")
	cpuProfile := flag.String("cpu-profile", "", "write CPU profile to file")
	memProfile := flag.String("mem-profile", "", "write heap profile to file")
	modeFlag := flag.String("mode", "", "mode to activate before replay")
	logLevel := flag.String("log-level", "warn", "log level (trace|debug|info|warn|error)")
	respectDelays := flag.Bool("respect-delays", false, "sleep for event delays declared in the fixture")
	outputPath := flag.String("output", "-", "write JSON report to file ('-' for stdout)")
	flag.Parse()

	if *iterations <= 0 {
		fmt.Fprintln(os.Stderr, "iterations must be positive")
		os.Exit(1)
	}

	logger := util.NewLogger(util.ParseLogLevel(*logLevel))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		exitErr(fmt.Errorf("load config: %w", err))
	}

	modes, err := rules.BuildModes(cfg)
	if err != nil {
		exitErr(fmt.Errorf("compile rules: %w", err))
	}

	fixture := defaultFixture()
	if *fixturePath != "" {
		loaded, loadErr := loadFixture(*fixturePath, fixture)
		if loadErr != nil {
			if errors.Is(loadErr, fs.ErrNotExist) && *fixturePath == defaultFixturePath {
				logger.Warnf("fixture %s not found, using built-in synthetic stream", *fixturePath)
			} else {
				exitErr(fmt.Errorf("load fixture: %w", loadErr))
			}
		} else {
			fixture = loaded
		}
	}

	if len(fixture.Events) == 0 {
		exitErr(errors.New("fixture contains no events"))
	}

	activeMode := fixture.Mode
	if *modeFlag != "" {
		activeMode = *modeFlag
	}

	if activeMode == "" && len(modes) > 0 {
		activeMode = modes[0].Name
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			exitErr(fmt.Errorf("create cpu profile: %w", err))
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			exitErr(fmt.Errorf("start cpu profile: %w", err))
		}
		defer pprof.StopCPUProfile()
	}

	runtime.GC()
	var startMem runtime.MemStats
	runtime.ReadMemStats(&startMem)

	durations := make([]time.Duration, 0, len(fixture.Events)*(*iterations))
	totalDispatches := 0

	ctx := context.Background()

	for i := 0; i < *iterations; i++ {
		hypr := fixture.newHyprctl()
		eng := engine.New(hypr, logger, modes, false, cfg.RedactTitles, layout.Gaps{
			Inner: cfg.Gaps.Inner,
			Outer: cfg.Gaps.Outer,
		}, cfg.TolerancePx, cfg.ManualReserved)

		if activeMode != "" {
			if err := eng.SetMode(activeMode); err != nil {
				exitErr(fmt.Errorf("set mode %q: %w", activeMode, err))
			}
		}

		if err := eng.Reconcile(ctx); err != nil {
			exitErr(fmt.Errorf("initial reconcile: %w", err))
		}

		for _, ev := range fixture.Events {
			if *respectDelays && ev.Delay > 0 {
				time.Sleep(ev.Delay)
			}
			start := time.Now()
			if err := eng.ApplyEvent(ctx, ev.Event); err != nil {
				exitErr(fmt.Errorf("apply %s: %w", ev.Event.Kind, err))
			}
			durations = append(durations, time.Since(start))
		}

		totalDispatches += hypr.Dispatches()
	}

	runtime.GC()
	var endMem runtime.MemStats
	runtime.ReadMemStats(&endMem)

	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			exitErr(fmt.Errorf("create mem profile: %w", err))
		}
		defer f.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			exitErr(fmt.Errorf("write heap profile: %w", err))
		}
	}

	report := buildReport(fixture, activeMode, *iterations, durations, totalDispatches, startMem, endMem)
	if err := writeReport(report, *outputPath); err != nil {
		exitErr(fmt.Errorf("encode report: %w", err))
	}
}

func buildReport(fixture benchFixture, mode string, iterations int, durations []time.Duration, dispatches int, start, end runtime.MemStats) benchReport {
	totalEvents := len(fixture.Events) * iterations
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	avg := time.Duration(0)
	if len(durations) > 0 {
		avg = total / time.Duration(len(durations))
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := percentile(sorted, 0.50)
	p95 := percentile(sorted, 0.95)
	min := time.Duration(0)
	max := time.Duration(0)
	if len(sorted) > 0 {
		min = sorted[0]
		max = sorted[len(sorted)-1]
	}

	allocs := end.Mallocs - start.Mallocs
	allocsPerEvent := float64(allocs)
	if totalEvents > 0 {
		allocsPerEvent = float64(allocs) / float64(totalEvents)
	}
	bytesAllocated := end.TotalAlloc - start.TotalAlloc
	bytesPerEvent := float64(bytesAllocated)
	if totalEvents > 0 {
		bytesPerEvent = float64(bytesAllocated) / float64(totalEvents)
	}

	heapAllocDelta := int64(end.HeapAlloc) - int64(start.HeapAlloc)
	heapAllocPerEvent := float64(heapAllocDelta)
	if totalEvents > 0 {
		heapAllocPerEvent = float64(heapAllocDelta) / float64(totalEvents)
	}
	heapObjectsDelta := int64(end.HeapObjects) - int64(start.HeapObjects)
	heapObjectsPerEvent := float64(heapObjectsDelta)
	if totalEvents > 0 {
		heapObjectsPerEvent = float64(heapObjectsDelta) / float64(totalEvents)
	}

	durationsMs := make([]float64, len(durations))
	for i, d := range durations {
		durationsMs[i] = toMillis(d)
	}

	summary := benchSummary{
		Fixture:            fixture.Name,
		Mode:               mode,
		Iterations:         iterations,
		EventsPerIteration: len(fixture.Events),
		TotalEvents:        totalEvents,
		Dispatches: benchDispatchStats{
			Total:        dispatches,
			PerIteration: safeDivide(dispatches, iterations),
			PerEvent:     safeDivide(dispatches, totalEvents),
		},
		Latency: benchLatencyStats{
			Min:    toMillis(min),
			Mean:   toMillis(avg),
			Median: toMillis(p50),
			P95:    toMillis(p95),
			Max:    toMillis(max),
		},
		Allocations: benchAllocationStats{
			Total:               allocs,
			PerEvent:            allocsPerEvent,
			BytesTotal:          bytesAllocated,
			BytesPerEvent:       bytesPerEvent,
			MiBTotal:            float64(bytesAllocated) / (1024 * 1024),
			MiBPerEvent:         bytesPerEvent / (1024 * 1024),
			HeapAllocStart:      start.HeapAlloc,
			HeapAllocEnd:        end.HeapAlloc,
			HeapAllocDelta:      heapAllocDelta,
			HeapAllocPerEvent:   heapAllocPerEvent,
			HeapObjectsStart:    start.HeapObjects,
			HeapObjectsEnd:      end.HeapObjects,
			HeapObjectsDelta:    heapObjectsDelta,
			HeapObjectsPerEvent: heapObjectsPerEvent,
		},
		TotalDurationMs: toMillis(total),
		EventsPerSecond: eventsPerSecond(total, totalEvents),
	}

	return benchReport{Summary: summary, DurationsMs: durationsMs}
}

func safeDivide(total int, count int) float64 {
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func writeReport(report benchReport, outputPath string) error {
	var (
		w   io.Writer
		out *os.File
		err error
	)
	switch strings.TrimSpace(outputPath) {
	case "", "-":
		w = os.Stdout
	default:
		out, err = os.Create(outputPath)
		if err != nil {
			return err
		}
		defer out.Close()
		w = out
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func eventsPerSecond(total time.Duration, events int) float64 {
	if total <= 0 || events == 0 {
		return 0
	}
	seconds := total.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(events) / seconds
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(p*float64(len(sorted)-1) + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func toMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func (f benchFixture) newHyprctl() *benchHyprctl {
	workspaces := make([]state.Workspace, len(f.Workspaces))
	copy(workspaces, f.Workspaces)
	counts := make(map[int]int)
	for _, c := range f.Clients {
		counts[c.WorkspaceID]++
	}
	for i := range workspaces {
		if count, ok := counts[workspaces[i].ID]; ok {
			workspaces[i].Windows = count
		}
	}
	clients := make([]state.Client, len(f.Clients))
	copy(clients, f.Clients)
	monitors := make([]state.Monitor, len(f.Monitors))
	copy(monitors, f.Monitors)
	return &benchHyprctl{
		clients:         clients,
		workspaces:      workspaces,
		monitors:        monitors,
		activeWorkspace: f.ActiveWorkspace,
		activeClient:    f.ActiveClient,
	}
}

func loadFixture(path string, base benchFixture) (benchFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return benchFixture{}, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" || looksLikeJSON(data) {
		var payload struct {
			Name            string            `json:"name"`
			Mode            string            `json:"mode"`
			ActiveWorkspace int               `json:"activeWorkspace"`
			ActiveClient    string            `json:"activeClient"`
			Clients         []state.Client    `json:"clients"`
			Workspaces      []state.Workspace `json:"workspaces"`
			Monitors        []state.Monitor   `json:"monitors"`
			Events          []struct {
				Kind    string `json:"kind"`
				Payload string `json:"payload"`
				Delay   string `json:"delay"`
			} `json:"events"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return benchFixture{}, err
		}
		fixture := benchFixture{
			Name:            fallback(payload.Name, filepath.Base(path)),
			Mode:            payload.Mode,
			Clients:         payload.Clients,
			Workspaces:      payload.Workspaces,
			Monitors:        payload.Monitors,
			ActiveWorkspace: payload.ActiveWorkspace,
			ActiveClient:    payload.ActiveClient,
		}
		if len(fixture.Clients) == 0 {
			fixture.Clients = append([]state.Client(nil), base.Clients...)
		}
		if len(fixture.Workspaces) == 0 {
			fixture.Workspaces = append([]state.Workspace(nil), base.Workspaces...)
		}
		if len(fixture.Monitors) == 0 {
			fixture.Monitors = append([]state.Monitor(nil), base.Monitors...)
		}
		if fixture.Mode == "" {
			fixture.Mode = base.Mode
		}
		if fixture.ActiveWorkspace == 0 {
			fixture.ActiveWorkspace = base.ActiveWorkspace
		}
		if fixture.ActiveClient == "" {
			fixture.ActiveClient = base.ActiveClient
		}
		for _, ev := range payload.Events {
			delay := time.Duration(0)
			if ev.Delay != "" {
				d, err := time.ParseDuration(ev.Delay)
				if err != nil {
					return benchFixture{}, fmt.Errorf("parse delay %q: %w", ev.Delay, err)
				}
				delay = d
			}
			fixture.Events = append(fixture.Events, benchEvent{
				Event: ipc.Event{Kind: strings.TrimSpace(ev.Kind), Payload: strings.TrimSpace(ev.Payload)},
				Delay: delay,
			})
		}
		if len(fixture.Events) == 0 {
			if len(base.Events) == 0 {
				return benchFixture{}, errors.New("fixture contains no events")
			}
			fixture.Events = append([]benchEvent(nil), base.Events...)
		}
		return fixture, nil
	}
	base.Name = fallback(base.Name, filepath.Base(path))
	events, err := parseEventLog(string(data))
	if err != nil {
		return benchFixture{}, err
	}
	base.Events = events
	return base, nil
}

func looksLikeJSON(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "{")
}

func parseEventLog(input string) ([]benchEvent, error) {
	lines := strings.Split(input, "\n")
	events := make([]benchEvent, 0, len(lines))
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(trimmed, ">>", 2)
		if len(parts) == 0 {
			continue
		}
		kind := strings.TrimSpace(parts[0])
		if kind == "" {
			return nil, fmt.Errorf("line %d: missing event kind", idx+1)
		}
		payload := ""
		if len(parts) == 2 {
			payload = strings.TrimSpace(parts[1])
		}
		events = append(events, benchEvent{Event: ipc.Event{Kind: kind, Payload: payload}})
	}
	if len(events) == 0 {
		return nil, errors.New("event log produced no events")
	}
	return events, nil
}

func defaultFixture() benchFixture {
	return benchFixture{
		Name:            "synthetic-coding",
		Mode:            "Coding",
		ActiveWorkspace: 3,
		ActiveClient:    "0xcode",
		Monitors: []state.Monitor{{
			ID:                1,
			Name:              "DP-1",
			Rectangle:         layout.Rect{Width: 2560, Height: 1440},
			ActiveWorkspaceID: 3,
		}},
		Workspaces: []state.Workspace{{
			ID:          3,
			Name:        "3",
			MonitorName: "DP-1",
		}},
		Clients: []state.Client{
			{
				Address:     "0xcode",
				Class:       "code",
				Title:       "IDE",
				WorkspaceID: 3,
				MonitorName: "DP-1",
			},
			{
				Address:     "0xterm",
				Class:       "kitty",
				Title:       "Terminal",
				WorkspaceID: 3,
				MonitorName: "DP-1",
			},
			{
				Address:     "0xref",
				Class:       "firefox",
				Title:       "Docs",
				WorkspaceID: 3,
				MonitorName: "DP-1",
			},
		},
		Events: []benchEvent{
			{Event: ipc.Event{Kind: "windowtitle", Payload: "0xcode, Coding workspace"}},
			{Event: ipc.Event{Kind: "activewindow", Payload: "0xterm"}},
			{Event: ipc.Event{Kind: "openwindow", Payload: "0xslack, 3, Slack, Slack"}},
			{Event: ipc.Event{Kind: "openwindow", Payload: "0xdiscord, 3, discord, Discord"}},
			{Event: ipc.Event{Kind: "movewindow", Payload: "0xslack, 3"}},
			{Event: ipc.Event{Kind: "movewindow", Payload: "0xdiscord, 3"}},
			{Event: ipc.Event{Kind: "activewindow", Payload: "0xcode"}},
		},
	}
}

func fallback(value, def string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return def
}

func exitErr(err error) {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		fmt.Fprintf(os.Stderr, "error: %v\n", pathErr)
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(1)
}
