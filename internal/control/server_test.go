package control

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"

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
