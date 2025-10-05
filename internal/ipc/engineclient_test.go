package ipc

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hyprpal/hyprpal/internal/layout"
)

func TestNewEngineClientSocketStrategy(t *testing.T) {
	runtimeDir := t.TempDir()
	sig := "instance"
	setEnv(t, "XDG_RUNTIME_DIR", runtimeDir)
	setEnv(t, "HYPRLAND_INSTANCE_SIGNATURE", sig)

	socketPath := filepath.Join(runtimeDir, "hypr", sig, ".socket.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		listener.Close()
	})

	client, strategy, err := NewEngineClient(nil, DispatchStrategySocket)
	if err != nil {
		t.Fatalf("NewEngineClient: %v", err)
	}
	if strategy != DispatchStrategySocket {
		t.Fatalf("unexpected strategy: got %s want %s", strategy, DispatchStrategySocket)
	}

	batchConn := make(chan net.Conn, 1)
	batchErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			batchErr <- err
			return
		}
		batchConn <- conn
	}()

	commands := [][]string{{"focuswindow", "address:test"}, {"movewindowpixel", "exact", "0", "0"}}
	if err := client.DispatchBatch(commands); err != nil {
		t.Fatalf("DispatchBatch: %v", err)
	}

	var conn net.Conn
	select {
	case err := <-batchErr:
		t.Fatalf("batch accept: %v", err)
	case conn = <-batchConn:
	}
	data, err := io.ReadAll(conn)
	conn.Close()
	if err != nil {
		t.Fatalf("read batch payload: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	expected := []string{
		"begin",
		"dispatch focuswindow address:test",
		"dispatch movewindowpixel exact 0 0",
		"commit",
	}
	if !reflect.DeepEqual(lines, expected) {
		t.Fatalf("unexpected payload: %#v", lines)
	}

	singleConn := make(chan net.Conn, 1)
	singleErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			singleErr <- err
			return
		}
		singleConn <- conn
	}()

	if err := client.Dispatch("focuswindow", "address:solo"); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	var single net.Conn
	select {
	case err := <-singleErr:
		t.Fatalf("single accept: %v", err)
	case single = <-singleConn:
	}
	payload, err := io.ReadAll(single)
	single.Close()
	if err != nil {
		t.Fatalf("read single payload: %v", err)
	}
	if got := strings.TrimSpace(string(payload)); got != "dispatch focuswindow address:solo" {
		t.Fatalf("unexpected single payload: %q", got)
	}
}

func TestNewEngineClientHyprctlStrategy(t *testing.T) {
	client, strategy, err := NewEngineClient(nil, DispatchStrategyHyprctl)
	if err != nil {
		t.Fatalf("NewEngineClient: %v", err)
	}
	if strategy != DispatchStrategyHyprctl {
		t.Fatalf("unexpected strategy: got %s want %s", strategy, DispatchStrategyHyprctl)
	}
	if err := client.DispatchBatch([][]string{{"noop"}}); !errors.Is(err, layout.ErrBatchUnsupported) {
		t.Fatalf("expected batch unsupported, got %v", err)
	}
}

func TestNewEngineClientSocketFallback(t *testing.T) {
	setEnv(t, "HYPRLAND_INSTANCE_SIGNATURE", "")
	setEnv(t, "XDG_RUNTIME_DIR", "")

	client, strategy, err := NewEngineClient(nil, DispatchStrategySocket)
	if err != nil {
		t.Fatalf("NewEngineClient: %v", err)
	}
	if strategy != DispatchStrategyHyprctl {
		t.Fatalf("expected hyprctl fallback, got %s", strategy)
	}
	if err := client.DispatchBatch([][]string{{"noop"}}); !errors.Is(err, layout.ErrBatchUnsupported) {
		t.Fatalf("expected batch unsupported, got %v", err)
	}
}
