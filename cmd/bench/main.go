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
	"text/tabwriter"
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
	WarmupIterations   int                  `json:"warmupIterations"`
	Dispatches         benchDispatchStats   `json:"dispatches"`
	Latency            benchLatencyStats    `json:"latency"`
	IterationDuration  benchLatencyStats    `json:"iterationDuration"`
	Allocations        benchAllocationStats `json:"allocations"`
	TotalDurationMs    float64              `json:"totalDurationMs"`
	EventsPerSecond    float64              `json:"eventsPerSecond"`
}

type benchReport struct {
	Summary     benchSummary     `json:"summary"`
	DurationsMs []float64        `json:"durationsMs"`
	Iterations  []benchIteration `json:"iterations,omitempty"`
}

type benchIteration struct {
	Index      int     `json:"index"`
	DurationMs float64 `json:"durationMs"`
	Dispatches int     `json:"dispatches"`
	Events     int     `json:"events"`
}

type benchEventTrace struct {
	Iteration  int     `json:"iteration"`
	EventIndex int     `json:"eventIndex"`
	Kind       string  `json:"kind"`
	Payload    string  `json:"payload"`
	DurationMs float64 `json:"durationMs"`
	Dispatches int     `json:"dispatches"`
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
	warmup := flag.Int("warmup", 0, "number of warm-up iterations to run before timing")
	cpuProfile := flag.String("cpu-profile", "", "write CPU profile to file")
	memProfile := flag.String("mem-profile", "", "write heap profile to file")
	modeFlag := flag.String("mode", "", "mode to activate before replay")
	logLevel := flag.String("log-level", "warn", "log level (trace|debug|info|warn|error)")
	respectDelays := flag.Bool("respect-delays", false, "sleep for event delays declared in the fixture")
	outputPath := flag.String("output", "-", "write JSON report to file ('-' for stdout)")
	humanSummary := flag.Bool("human", false, "print a tabular summary alongside the JSON output")
	eventTracePath := flag.String("event-trace", "", "write per-event timings to file (JSON array, '-' for stdout)")
	explain := flag.Bool("explain", false, "log matched rule predicate proofs for each event during replay")
	flag.Parse()

	if *iterations <= 0 {
		fmt.Fprintln(os.Stderr, "iterations must be positive")
		os.Exit(1)
	}
	if *warmup < 0 {
		fmt.Fprintln(os.Stderr, "warmup must be zero or positive")
		os.Exit(1)
	}

	logger := util.NewLogger(util.ParseLogLevel(*logLevel))

	traceEnabled := strings.TrimSpace(*eventTracePath) != ""

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

	ctx := context.Background()

	if *warmup > 0 {
		for i := 0; i < *warmup; i++ {
			if _, _, _, _, err := replayIteration(ctx, fixture, cfg, modes, logger, activeMode, *respectDelays, i+1, false, false, *explain); err != nil {
				exitErr(fmt.Errorf("warmup iteration %d: %w", i+1, err))
			}
		}
	}

	runtime.GC()
	var startMem runtime.MemStats
	runtime.ReadMemStats(&startMem)

	eventsPerIteration := len(fixture.Events)
	durations := make([]time.Duration, 0, eventsPerIteration*(*iterations))
	iterationDurations := make([]time.Duration, 0, *iterations)
	iterationDispatches := make([]int, 0, *iterations)
	totalDispatches := 0
	var eventTraces []benchEventTrace
	if traceEnabled {
		eventTraces = make([]benchEventTrace, 0, eventsPerIteration*(*iterations))
	}

	for i := 0; i < *iterations; i++ {
		iterationDuration, dispatchCount, eventDurations, traces, err := replayIteration(ctx, fixture, cfg, modes, logger, activeMode, *respectDelays, i+1, true, traceEnabled, *explain)
		if err != nil {
			exitErr(fmt.Errorf("iteration %d: %w", i+1, err))
		}
		iterationDurations = append(iterationDurations, iterationDuration)
		iterationDispatches = append(iterationDispatches, dispatchCount)
		totalDispatches += dispatchCount
		durations = append(durations, eventDurations...)
		if traceEnabled {
			eventTraces = append(eventTraces, traces...)
		}
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

	report := buildReport(fixture, activeMode, *iterations, *warmup, durations, iterationDurations, iterationDispatches, totalDispatches, startMem, endMem)
	if err := writeReport(report, *outputPath); err != nil {
		exitErr(fmt.Errorf("encode report: %w", err))
	}

	if err := writeEventTrace(eventTraces, *eventTracePath); err != nil {
		exitErr(fmt.Errorf("write event trace: %w", err))
	}

	if *humanSummary {
		if err := printHumanSummary(report.Summary, os.Stdout); err != nil {
			exitErr(fmt.Errorf("print human summary: %w", err))
		}
	}
}

func replayIteration(ctx context.Context, fixture benchFixture, cfg *config.Config, modes []rules.Mode, logger *util.Logger, activeMode string, respectDelays bool, iteration int, capture bool, trace bool, explain bool) (time.Duration, int, []time.Duration, []benchEventTrace, error) {
	iterationStart := time.Now()
	hypr := fixture.newHyprctl()
	eng := engine.New(hypr, logger, modes, false, cfg.RedactTitles, layout.Gaps{
		Inner: cfg.Gaps.Inner,
		Outer: cfg.Gaps.Outer,
	}, cfg.TolerancePx, cfg.ManualReserved)

	if activeMode != "" {
		if err := eng.SetMode(activeMode); err != nil {
			return 0, 0, nil, nil, fmt.Errorf("set mode %q: %w", activeMode, err)
		}
	}

	if err := eng.Reconcile(ctx); err != nil {
		return 0, 0, nil, nil, fmt.Errorf("initial reconcile: %w", err)
	}

	if explain {
		eng.ClearRuleCheckHistory()
	}

	var eventDurations []time.Duration
	if capture {
		eventDurations = make([]time.Duration, 0, len(fixture.Events))
	}
	var traces []benchEventTrace
	if capture && trace {
		traces = make([]benchEventTrace, 0, len(fixture.Events))
	}

	for idx, ev := range fixture.Events {
		if respectDelays && ev.Delay > 0 {
			time.Sleep(ev.Delay)
		}
		beforeDispatches := hypr.Dispatches()
		start := time.Now()
		if err := eng.ApplyEvent(ctx, ev.Event); err != nil {
			return 0, 0, nil, nil, fmt.Errorf("apply %s: %w", ev.Event.Kind, err)
		}
		if explain {
			records := eng.RuleCheckHistory()
			payload := strings.TrimSpace(ev.Event.Payload)
			for _, record := range records {
				if !record.Matched {
					continue
				}
				if record.Predicate == nil {
					if payload != "" {
						logger.Infof("explain iteration %d event %d (%s %s) rule %s matched (predicate proof unavailable)", iteration, idx+1, ev.Event.Kind, payload, record.Rule)
					} else {
						logger.Infof("explain iteration %d event %d (%s) rule %s matched (predicate proof unavailable)", iteration, idx+1, ev.Event.Kind, record.Rule)
					}
					continue
				}
				summary := rules.SummarizePredicateTrace(record.Predicate)
				if payload != "" {
					logger.Infof("explain iteration %d event %d (%s %s) rule %s matched", iteration, idx+1, ev.Event.Kind, payload, record.Rule)
				} else {
					logger.Infof("explain iteration %d event %d (%s) rule %s matched", iteration, idx+1, ev.Event.Kind, record.Rule)
				}
				if len(summary) == 0 {
					logger.Infof("  <no predicate details>")
					continue
				}
				for _, line := range summary {
					logger.Infof("  %s", line)
				}
			}
			eng.ClearRuleCheckHistory()
		}
		elapsed := time.Since(start)
		if capture {
			eventDurations = append(eventDurations, elapsed)
			if trace {
				dispatchDiff := hypr.Dispatches() - beforeDispatches
				traces = append(traces, benchEventTrace{
					Iteration:  iteration,
					EventIndex: idx + 1,
					Kind:       ev.Event.Kind,
					Payload:    ev.Event.Payload,
					DurationMs: toMillis(elapsed),
					Dispatches: dispatchDiff,
				})
			}
		}
	}

	iterationDuration := time.Since(iterationStart)
	dispatchCount := hypr.Dispatches()
	return iterationDuration, dispatchCount, eventDurations, traces, nil
}

func buildReport(fixture benchFixture, mode string, iterations int, warmup int, durations []time.Duration, iterationDurations []time.Duration, iterationDispatches []int, dispatches int, start, end runtime.MemStats) benchReport {
	totalEvents := len(fixture.Events) * iterations
	latencyStats, totalEventDuration := buildLatencyStats(durations)
	iterationStats, _ := buildLatencyStats(iterationDurations)

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

	iterationsData := make([]benchIteration, 0, len(iterationDurations))
	for i, d := range iterationDurations {
		dispatchCount := 0
		if i < len(iterationDispatches) {
			dispatchCount = iterationDispatches[i]
		}
		iterationsData = append(iterationsData, benchIteration{
			Index:      i + 1,
			DurationMs: toMillis(d),
			Dispatches: dispatchCount,
			Events:     len(fixture.Events),
		})
	}

	summary := benchSummary{
		Fixture:            fixture.Name,
		Mode:               mode,
		Iterations:         iterations,
		WarmupIterations:   warmup,
		EventsPerIteration: len(fixture.Events),
		TotalEvents:        totalEvents,
		Dispatches: benchDispatchStats{
			Total:        dispatches,
			PerIteration: safeDivide(dispatches, iterations),
			PerEvent:     safeDivide(dispatches, totalEvents),
		},
		Latency:           latencyStats,
		IterationDuration: iterationStats,
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
		TotalDurationMs: toMillis(totalEventDuration),
		EventsPerSecond: eventsPerSecond(totalEventDuration, totalEvents),
	}

	return benchReport{Summary: summary, DurationsMs: durationsMs, Iterations: iterationsData}
}

func buildLatencyStats(durations []time.Duration) (benchLatencyStats, time.Duration) {
	stats := benchLatencyStats{}
	if len(durations) == 0 {
		return stats, 0
	}
	total := time.Duration(0)
	for _, d := range durations {
		total += d
	}
	mean := time.Duration(0)
	if len(durations) > 0 {
		mean = total / time.Duration(len(durations))
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	stats.Min = toMillis(sorted[0])
	stats.Mean = toMillis(mean)
	stats.Median = toMillis(percentile(sorted, 0.50))
	stats.P95 = toMillis(percentile(sorted, 0.95))
	stats.Max = toMillis(sorted[len(sorted)-1])
	return stats, total
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
		dir := filepath.Dir(outputPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create report dir: %w", err)
			}
		}
		out, err = os.Create(outputPath)
		if err != nil {
			return err
		}
		defer out.Close()
		w = out
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func printHumanSummary(summary benchSummary, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(tw, "Fixture:\t%s\n", summary.Fixture); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Mode:\t%s\n", fallback(summary.Mode, "(default)")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Iterations:\t%d\n", summary.Iterations); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Warmup iterations:\t%d\n", summary.WarmupIterations); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Events/iteration:\t%d\n", summary.EventsPerIteration); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Total events:\t%d\n", summary.TotalEvents); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Dispatches:\t%d (%.2f / iter, %.2f / event)\n", summary.Dispatches.Total, summary.Dispatches.PerIteration, summary.Dispatches.PerEvent); err != nil {
		return err
	}
	latency := summary.Latency
	if _, err := fmt.Fprintf(tw, "Latency (ms):\tmin %.2f | mean %.2f | median %.2f | p95 %.2f | max %.2f\n", latency.Min, latency.Mean, latency.Median, latency.P95, latency.Max); err != nil {
		return err
	}
	iterationLatency := summary.IterationDuration
	if _, err := fmt.Fprintf(tw, "Iteration duration (ms):\tmin %.2f | mean %.2f | median %.2f | p95 %.2f | max %.2f\n", iterationLatency.Min, iterationLatency.Mean, iterationLatency.Median, iterationLatency.P95, iterationLatency.Max); err != nil {
		return err
	}
	allocs := summary.Allocations
	if _, err := fmt.Fprintf(tw, "Allocations:\t%d total (%.2f / event)\n", allocs.Total, allocs.PerEvent); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Bytes allocated:\t%s (%.2f / event)\n", formatBytesUnsigned(allocs.BytesTotal), allocs.BytesPerEvent); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Heap delta:\t%s change, %d objects (%.2f / event)\n", formatBytesSigned(allocs.HeapAllocDelta), allocs.HeapObjectsDelta, allocs.HeapObjectsPerEvent); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Events/sec:\t%.2f\n", summary.EventsPerSecond); err != nil {
		return err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return nil
}

func writeEventTrace(events []benchEventTrace, outputPath string) error {
	path := strings.TrimSpace(outputPath)
	if path == "" {
		return nil
	}

	var (
		w   io.Writer
		out *os.File
		err error
	)

	if path == "-" {
		w = os.Stdout
	} else {
		dir := filepath.Dir(path)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create event trace dir: %w", err)
			}
		}
		out, err = os.Create(path)
		if err != nil {
			return err
		}
		defer out.Close()
		w = out
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(events); err != nil {
		return err
	}
	return nil
}

func formatBytesUnsigned(bytes uint64) string {
	const miB = 1024 * 1024
	if bytes == 0 {
		return "0 B (0.00 MiB)"
	}
	return fmt.Sprintf("%d B (%.2f MiB)", bytes, float64(bytes)/float64(miB))
}

func formatBytesSigned(delta int64) string {
	if delta == 0 {
		return "0 B (0.00 MiB)"
	}
	sign := ""
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	return fmt.Sprintf("%s%s", sign, formatBytesUnsigned(uint64(delta)))
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
