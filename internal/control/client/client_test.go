package client

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hyprpal/hyprpal/internal/control"
	"github.com/hyprpal/hyprpal/internal/rules"
)

func startTestServer(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "socket")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handler(conn)
	}()
	return path
}

func TestRulesStatusSuccess(t *testing.T) {
	now := time.Now().UTC().Round(time.Second)
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Action != control.ActionRulesStatus {
			t.Errorf("unexpected action %q", req.Action)
			return
		}
		resp := control.Response{Status: control.StatusOK, Data: control.RulesStatus{Rules: []control.RuleStatus{{
			Mode:             "Coding",
			Rule:             "Dock",
			TotalExecutions:  3,
			RecentExecutions: []time.Time{now},
			Disabled:         true,
			DisabledReason:   "throttle",
			DisabledSince:    now,
			Throttle: &control.RuleThrottle{Windows: []control.RuleThrottleWindow{{
				FiringLimit: 5,
				WindowMs:    2000,
			}}},
		}}}}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	status, err := cli.RulesStatus(context.Background())
	if err != nil {
		t.Fatalf("RulesStatus returned error: %v", err)
	}
	if len(status.Rules) != 1 {
		t.Fatalf("expected one rule status, got %d", len(status.Rules))
	}
	got := status.Rules[0]
	if got.Mode != "Coding" || got.Rule != "Dock" || got.TotalExecutions != 3 {
		t.Fatalf("unexpected rule status: %#v", got)
	}
	if !got.Disabled || got.DisabledReason != "throttle" {
		t.Fatalf("expected disabled rule with reason, got %#v", got)
	}
	if got.Throttle == nil || len(got.Throttle.Windows) != 1 {
		t.Fatalf("expected throttle windows in status: %#v", got.Throttle)
	}
}

func TestRulesStatusError(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Action != control.ActionRulesStatus {
			t.Errorf("unexpected action %q", req.Action)
			return
		}
		resp := control.Response{Status: control.StatusError, Error: "boom"}
		_ = json.NewEncoder(conn).Encode(resp)
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if _, err := cli.RulesStatus(context.Background()); err == nil {
		t.Fatalf("expected error from RulesStatus")
	}
}

func TestMetricsSuccess(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Action != control.ActionMetricsGet {
			t.Errorf("unexpected action %q", req.Action)
			return
		}
		resp := control.Response{Status: control.StatusOK, Data: control.MetricsSnapshot{
			Enabled: true,
			Totals:  control.MetricsTotals{Matched: 2, Applied: 1},
			Rules:   []control.MetricsRule{{Mode: "Mode", Rule: "Rule", Matched: 2, Applied: 1}},
		}}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	snapshot, err := cli.Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics returned error: %v", err)
	}
	if !snapshot.Enabled {
		t.Fatalf("expected telemetry enabled")
	}
	if snapshot.Totals.Matched != 2 || snapshot.Totals.Applied != 1 {
		t.Fatalf("unexpected totals: %#v", snapshot.Totals)
	}
	if len(snapshot.Rules) != 1 {
		t.Fatalf("expected one rule entry, got %d", len(snapshot.Rules))
	}
}

func TestMetricsError(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		resp := control.Response{Status: control.StatusError, Error: "disabled"}
		_ = json.NewEncoder(conn).Encode(resp)
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if _, err := cli.Metrics(context.Background()); err == nil {
		t.Fatalf("expected error from Metrics")
	}
}

func TestEnableRule(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Action != control.ActionRuleEnable {
			t.Errorf("unexpected action %q", req.Action)
			return
		}
		if req.Params["mode"] != "Coding" || req.Params["rule"] != "Dock" {
			t.Errorf("unexpected params: %#v", req.Params)
			return
		}
		resp := control.Response{Status: control.StatusOK}
		_ = json.NewEncoder(conn).Encode(resp)
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := cli.EnableRule(context.Background(), "", "Dock"); err == nil {
		t.Fatalf("expected error for empty mode")
	}
	if err := cli.EnableRule(context.Background(), "Coding", ""); err == nil {
		t.Fatalf("expected error for empty rule")
	}
	if err := cli.EnableRule(context.Background(), "Coding", "Dock"); err != nil {
		t.Fatalf("EnableRule returned error: %v", err)
	}
}

func TestEnableRuleServerError(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		resp := control.Response{Status: control.StatusError, Error: "unknown rule"}
		_ = json.NewEncoder(conn).Encode(resp)
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := cli.EnableRule(context.Background(), "Coding", "Dock"); err == nil {
		t.Fatalf("expected error from EnableRule")
	}
}

func TestPlanIncludesPredicateTrace(t *testing.T) {
	path := startTestServer(t, func(conn net.Conn) {
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Action != control.ActionPlan {
			t.Errorf("unexpected action %q", req.Action)
			return
		}
		resp := control.Response{Status: control.StatusOK, Data: control.PlanResult{Commands: []control.PlanCommand{{
			Dispatch:  []string{"dispatch"},
			Reason:    "Mode:Rule",
			Action:    "layout.test",
			Predicate: &rules.PredicateTrace{Kind: "predicate", Result: true},
		}}}}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	})
	cli, err := New(path)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	result, err := cli.Plan(context.Background(), true)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(result.Commands) != 1 {
		t.Fatalf("expected one command, got %d", len(result.Commands))
	}
	if result.Commands[0].Action != "layout.test" {
		t.Fatalf("expected action in plan result, got %q", result.Commands[0].Action)
	}
	if result.Commands[0].Predicate == nil {
		t.Fatalf("expected predicate trace in plan result")
	}
	if result.Commands[0].Predicate.Kind != "predicate" {
		t.Fatalf("unexpected predicate kind: %#v", result.Commands[0].Predicate)
	}
}
