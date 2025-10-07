package control

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

type fakeHyprctl struct {
	mu              sync.Mutex
	listClientCalls int
}

func (f *fakeHyprctl) ListClients(ctx context.Context) ([]state.Client, error) {
	f.mu.Lock()
	f.listClientCalls++
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeHyprctl) ListWorkspaces(ctx context.Context) ([]state.Workspace, error) {
	return nil, nil
}

func (f *fakeHyprctl) ListMonitors(ctx context.Context) ([]state.Monitor, error) {
	return nil, nil
}

func (f *fakeHyprctl) ActiveWorkspaceID(ctx context.Context) (int, error) {
	return 0, nil
}

func (f *fakeHyprctl) ActiveClientAddress(ctx context.Context) (string, error) {
	return "", nil
}

func (f *fakeHyprctl) Dispatch(args ...string) error { return nil }

func (f *fakeHyprctl) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listClientCalls
}

func TestHandleModeSetTriggersReconcile(t *testing.T) {
	hyprctl := &fakeHyprctl{}
	logger := util.NewLoggerWithWriter(util.LevelError, io.Discard)
	modes := []rules.Mode{{Name: "initial"}, {Name: "target"}}
	eng := engine.New(hyprctl, logger, modes, false, false, layout.Gaps{}, 0, nil)
	srv, err := NewServer(eng, logger, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		enc := json.NewEncoder(clientConn)
		req := Request{Action: ActionModeSet, Params: map[string]any{"name": "target"}}
		if err := enc.Encode(req); err != nil {
			t.Errorf("encode request: %v", err)
			return
		}
		dec := json.NewDecoder(clientConn)
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Errorf("decode response: %v", err)
			return
		}
		if resp.Status != StatusOK {
			t.Errorf("expected ok status, got %s (error=%s)", resp.Status, resp.Error)
		}
	}()

	srv.handle(context.Background(), serverConn)
	wg.Wait()

	if calls := hyprctl.calls(); calls != 1 {
		t.Fatalf("expected reconcile to query world once, got %d", calls)
	}
}

func TestHandleRulesStatus(t *testing.T) {
	hyprctl := &fakeHyprctl{}
	logger := util.NewLoggerWithWriter(util.LevelError, io.Discard)
	modes := []rules.Mode{{
		Name: "Mode",
		Rules: []rules.Rule{{
			Name: "Rule",
			Throttle: &rules.RuleThrottle{Windows: []rules.RuleThrottleWindow{{
				FiringLimit: 3,
				Window:      2 * time.Second,
			}}},
		}},
	}}
	eng := engine.New(hyprctl, logger, modes, false, false, layout.Gaps{}, 0, nil)
	srv, err := NewServer(eng, logger, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := json.NewEncoder(clientConn)
		if err := enc.Encode(Request{Action: ActionRulesStatus}); err != nil {
			t.Errorf("encode request: %v", err)
			return
		}
		dec := json.NewDecoder(clientConn)
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Errorf("decode response: %v", err)
			return
		}
		if resp.Status != StatusOK {
			t.Errorf("expected ok status, got %s (error=%s)", resp.Status, resp.Error)
			return
		}
		data, err := json.Marshal(resp.Data)
		if err != nil {
			t.Errorf("marshal payload: %v", err)
			return
		}
		var status RulesStatus
		if err := json.Unmarshal(data, &status); err != nil {
			t.Errorf("unmarshal payload: %v", err)
			return
		}
		if len(status.Rules) != 1 {
			t.Errorf("expected one rule status, got %d", len(status.Rules))
			return
		}
		if status.Rules[0].Mode != "Mode" || status.Rules[0].Rule != "Rule" {
			t.Errorf("unexpected rule entry: %#v", status.Rules[0])
			return
		}
		if status.Rules[0].Throttle == nil || len(status.Rules[0].Throttle.Windows) != 1 {
			t.Errorf("expected throttle windows in response: %#v", status.Rules[0].Throttle)
		}
	}()

	srv.handle(context.Background(), serverConn)
	<-done
}

func TestHandleRuleEnableValidation(t *testing.T) {
	hyprctl := &fakeHyprctl{}
	logger := util.NewLoggerWithWriter(util.LevelError, io.Discard)
	modes := []rules.Mode{{Name: "Mode", Rules: []rules.Rule{{Name: "Rule"}}}}
	eng := engine.New(hyprctl, logger, modes, false, false, layout.Gaps{}, 0, nil)
	srv, err := NewServer(eng, logger, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := json.NewEncoder(clientConn)
		req := Request{Action: ActionRuleEnable, Params: map[string]any{"mode": ""}}
		if err := enc.Encode(req); err != nil {
			t.Errorf("encode request: %v", err)
			return
		}
		dec := json.NewDecoder(clientConn)
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Errorf("decode response: %v", err)
			return
		}
		if resp.Status != StatusError {
			t.Errorf("expected error status, got %s", resp.Status)
		}
	}()

	srv.handle(context.Background(), serverConn)
	<-done
}

func TestHandleRuleEnableEngineError(t *testing.T) {
	hyprctl := &fakeHyprctl{}
	logger := util.NewLoggerWithWriter(util.LevelError, io.Discard)
	modes := []rules.Mode{{Name: "Mode", Rules: []rules.Rule{{Name: "Rule"}}}}
	eng := engine.New(hyprctl, logger, modes, false, false, layout.Gaps{}, 0, nil)
	srv, err := NewServer(eng, logger, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := json.NewEncoder(clientConn)
		req := Request{Action: ActionRuleEnable, Params: map[string]any{"mode": "Mode", "rule": "Missing"}}
		if err := enc.Encode(req); err != nil {
			t.Errorf("encode request: %v", err)
			return
		}
		dec := json.NewDecoder(clientConn)
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Errorf("decode response: %v", err)
			return
		}
		if resp.Status != StatusError {
			t.Errorf("expected error status, got %s", resp.Status)
			return
		}
		if resp.Error == "" {
			t.Errorf("expected error message for engine failure")
		}
	}()

	srv.handle(context.Background(), serverConn)
	<-done
}
