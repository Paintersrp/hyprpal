package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlclient "github.com/hyprpal/hyprpal/internal/control/client"
)

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestRunCheckSuccess(t *testing.T) {
	cfg := `managedWorkspaces: [1]
modes:
  - name: Test
    rules:
      - name: Example
        when:
          mode: Test
        actions:
          - type: layout.fullscreen
`
	path := writeTempConfig(t, cfg)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCheck([]string{"--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Configuration OK" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunCheckFailure(t *testing.T) {
	cfg := `managedWorkspaces: [0, 0]
manualReserved:
  DP-1:
    top: -1
modes:
  - name: ""
    rules:
      - name: ""
        actions: []
`
	path := writeTempConfig(t, cfg)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCheck([]string{"--config", path}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error from runCheck")
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("expected no stdout, got %q", stdout.String())
	}
	output := stderr.String()
	if !strings.Contains(output, "Configuration has") {
		t.Fatalf("expected aggregated error output, got %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%s: managedWorkspaces[0]: must be positive, got 0", path)) {
		t.Fatalf("missing managed workspace error: %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%s: manualReserved.DP-1: cannot include negative values", path)) {
		t.Fatalf("missing manualReserved error: %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%s: modes[0].rules[0].actions: must define at least one action", path)) {
		t.Fatalf("missing rule actions error: %q", output)
	}
}

type fakeRulesClient struct {
	status    controlclient.RulesStatus
	statusErr error
	enableErr error
	lastMode  string
	lastRule  string
}

func (f *fakeRulesClient) RulesStatus(context.Context) (controlclient.RulesStatus, error) {
	if f.statusErr != nil {
		return controlclient.RulesStatus{}, f.statusErr
	}
	return f.status, nil
}

type fakeMetricsClient struct {
	snapshot controlclient.MetricsSnapshot
	err      error
	calls    int
}

func (f *fakeMetricsClient) Metrics(context.Context) (controlclient.MetricsSnapshot, error) {
	f.calls++
	if f.err != nil {
		return controlclient.MetricsSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func (f *fakeRulesClient) EnableRule(_ context.Context, mode, rule string) error {
	f.lastMode = mode
	f.lastRule = rule
	return f.enableErr
}

func TestRunRulesStatus(t *testing.T) {
	now := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	client := &fakeRulesClient{status: controlclient.RulesStatus{Rules: []controlclient.RuleStatus{
		{Mode: "Coding", Rule: "Dock", TotalExecutions: 3, Throttle: &controlclient.RuleThrottle{Windows: []controlclient.RuleThrottleWindow{{
			FiringLimit: 3,
			WindowMs:    2000,
		}}}},
		{Mode: "Coding", Rule: "Throttle", TotalExecutions: 5, Disabled: true, DisabledReason: "disabled (throttle: 5 in 2s)", DisabledSince: now},
	}}}
	var buf bytes.Buffer
	if err := runRules(context.Background(), client, []string{"status"}, &buf); err != nil {
		t.Fatalf("runRules returned error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Rule counters:") {
		t.Fatalf("expected counters header in output: %q", output)
	}
	if !strings.Contains(output, "[Coding] Dock: total=3 (throttle: 3 in 2s)") {
		t.Fatalf("missing dock counter: %q", output)
	}
	if !strings.Contains(output, "Disabled rules:") {
		t.Fatalf("expected disabled section: %q", output)
	}
	if !strings.Contains(output, now.Format(time.RFC3339)) {
		t.Fatalf("expected disabled timestamp in output: %q", output)
	}
}

func TestRunRulesStatusError(t *testing.T) {
	client := &fakeRulesClient{statusErr: fmt.Errorf("boom")}
	var buf bytes.Buffer
	if err := runRules(context.Background(), client, []string{"status"}, &buf); err == nil {
		t.Fatalf("expected error from runRules")
	}
}

func TestRunRulesEnable(t *testing.T) {
	client := &fakeRulesClient{}
	var buf bytes.Buffer
	if err := runRules(context.Background(), client, []string{"enable", "Coding", "Dock"}, &buf); err != nil {
		t.Fatalf("runRules enable returned error: %v", err)
	}
	if client.lastMode != "Coding" || client.lastRule != "Dock" {
		t.Fatalf("unexpected mode/rule captured: mode=%q rule=%q", client.lastMode, client.lastRule)
	}
	if !strings.Contains(buf.String(), "Rule Dock re-enabled in mode Coding") {
		t.Fatalf("missing enable confirmation: %q", buf.String())
	}
}

func TestRunRulesEnableErrors(t *testing.T) {
	client := &fakeRulesClient{enableErr: fmt.Errorf("boom")}
	var buf bytes.Buffer
	if err := runRules(context.Background(), client, []string{"enable", "Coding", "Dock"}, &buf); err == nil {
		t.Fatalf("expected enable error")
	}
	if err := runRules(context.Background(), client, []string{"enable", "Coding"}, &buf); err == nil {
		t.Fatalf("expected argument error")
	}
	if err := runRules(context.Background(), client, nil, &buf); err == nil {
		t.Fatalf("expected missing subcommand error")
	}
	if err := runRules(context.Background(), client, []string{"unknown"}, &buf); err == nil {
		t.Fatalf("expected unknown subcommand error")
	}
}

func TestRunMetricsEnabled(t *testing.T) {
	now := time.Date(2024, time.March, 10, 1, 2, 3, 0, time.UTC)
	client := &fakeMetricsClient{snapshot: controlclient.MetricsSnapshot{
		Enabled: true,
		Started: now,
		Totals:  controlclient.MetricsTotals{Matched: 3, Applied: 2, DispatchErrors: 1},
		Rules: []controlclient.MetricsRule{{
			Mode:           "Coding",
			Rule:           "Dock",
			Matched:        2,
			Applied:        1,
			DispatchErrors: 1,
		}},
	}}
	var buf bytes.Buffer
	if err := runMetrics(context.Background(), client, &buf); err != nil {
		t.Fatalf("runMetrics returned error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Telemetry enabled") {
		t.Fatalf("expected enabled message, got %q", output)
	}
	if !strings.Contains(output, now.Format(time.RFC3339)) {
		t.Fatalf("expected started timestamp in output: %q", output)
	}
	if !strings.Contains(output, "Totals: matched=3 applied=2 dispatchErrors=1") {
		t.Fatalf("expected totals in output: %q", output)
	}
	if !strings.Contains(output, "[Coding] Dock: matched=2 applied=1 dispatchErrors=1") {
		t.Fatalf("expected per-rule counters: %q", output)
	}
	if client.calls != 1 {
		t.Fatalf("expected single Metrics call, got %d", client.calls)
	}
}

func TestRunMetricsDisabled(t *testing.T) {
	client := &fakeMetricsClient{snapshot: controlclient.MetricsSnapshot{Enabled: false}}
	var buf bytes.Buffer
	if err := runMetrics(context.Background(), client, &buf); err != nil {
		t.Fatalf("runMetrics returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "Telemetry disabled") {
		t.Fatalf("expected disabled message, got %q", buf.String())
	}
}

func TestRunMetricsError(t *testing.T) {
	client := &fakeMetricsClient{err: fmt.Errorf("boom")}
	if err := runMetrics(context.Background(), client, &bytes.Buffer{}); err == nil {
		t.Fatalf("expected metrics error")
	}
}
