package ipc

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hyprpal/hyprpal/internal/layout"
)

func setupEngineSocket(t *testing.T) net.Listener {
	t.Helper()

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
	return listener
}

func TestNewEngineClientSocketStrategy(t *testing.T) {
	listener := setupEngineSocket(t)

	client, strategy, err := NewEngineClient(nil, DispatchStrategySocket)
	if err != nil {
		t.Fatalf("NewEngineClient: %v", err)
	}
	if strategy != DispatchStrategySocket {
		t.Fatalf("unexpected strategy: got %s want %s", strategy, DispatchStrategySocket)
	}
	if _, ok := client.dispatcher.(*socketDispatcher); !ok {
		t.Fatalf("expected socket dispatcher, got %T", client.dispatcher)
	}

	batchLines := make(chan []string, 1)
	batchErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			batchErr <- err
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				batchErr <- err
				return
			}
			lines = append(lines, strings.TrimSuffix(line, "\n"))
			if strings.TrimSpace(line) == "commit" {
				break
			}
		}
		if _, err := conn.Write([]byte("ok\n")); err != nil {
			batchErr <- err
			return
		}
		batchLines <- lines
	}()

	commands := [][]string{{"focuswindow", "address:test"}, {"movewindowpixel", "exact", "0", "0"}}
	if err := client.DispatchBatch(commands); err != nil {
		t.Fatalf("DispatchBatch: %v", err)
	}

	var lines []string
	select {
	case err := <-batchErr:
		t.Fatalf("batch handler: %v", err)
	case lines = <-batchLines:
	}
	expected := []string{
		"begin",
		"dispatch focuswindow address:test",
		"dispatch movewindowpixel exact 0 0",
		"commit",
	}
	if !reflect.DeepEqual(lines, expected) {
		t.Fatalf("unexpected payload: %#v", lines)
	}

	singleLine := make(chan string, 1)
	singleErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			singleErr <- err
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			singleErr <- err
			return
		}
		if _, err := conn.Write([]byte("ok\n")); err != nil {
			singleErr <- err
			return
		}
		singleLine <- strings.TrimSpace(line)
	}()

	if err := client.Dispatch("focuswindow", "address:solo"); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	var single string
	select {
	case err := <-singleErr:
		t.Fatalf("single handler: %v", err)
	case single = <-singleLine:
	}
	if single != "dispatch focuswindow address:solo" {
		t.Fatalf("unexpected single payload: %q", single)
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

func TestEngineClientSocketStrategyErrors(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		listener := setupEngineSocket(t)

		client, strategy, err := NewEngineClient(nil, DispatchStrategySocket)
		if err != nil {
			t.Fatalf("NewEngineClient: %v", err)
		}
		if strategy != DispatchStrategySocket {
			t.Fatalf("unexpected strategy: got %s want %s", strategy, DispatchStrategySocket)
		}
		if _, ok := client.dispatcher.(*socketDispatcher); !ok {
			t.Fatalf("expected socket dispatcher, got %T", client.dispatcher)
		}

		serverErr := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			if _, err := reader.ReadString('\n'); err != nil {
				serverErr <- err
				return
			}
			if _, err := conn.Write([]byte("err=bad dispatch\n")); err != nil {
				serverErr <- err
				return
			}
			serverErr <- nil
		}()

		err = client.Dispatch("focuswindow", "address:test")
		if err == nil {
			t.Fatalf("Dispatch succeeded, want error")
		}
		if !strings.Contains(err.Error(), "err=bad dispatch") {
			t.Fatalf("unexpected error: %v", err)
		}
		if serr := <-serverErr; serr != nil {
			t.Fatalf("server error: %v", serr)
		}
	})

	t.Run("batch", func(t *testing.T) {
		listener := setupEngineSocket(t)

		client, strategy, err := NewEngineClient(nil, DispatchStrategySocket)
		if err != nil {
			t.Fatalf("NewEngineClient: %v", err)
		}
		if strategy != DispatchStrategySocket {
			t.Fatalf("unexpected strategy: got %s want %s", strategy, DispatchStrategySocket)
		}

		serverErr := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					serverErr <- err
					return
				}
				if strings.TrimSpace(line) == "commit" {
					break
				}
			}
			if _, err := conn.Write([]byte("err=batch failed\n")); err != nil {
				serverErr <- err
				return
			}
			serverErr <- nil
		}()

		err = client.DispatchBatch([][]string{
			{"focuswindow", "address:test"},
			{"movewindowpixel", "exact", "0", "0"},
		})
		if err == nil {
			t.Fatalf("DispatchBatch succeeded, want error")
		}
		if !strings.Contains(err.Error(), "err=batch failed") {
			t.Fatalf("unexpected error: %v", err)
		}
		if serr := <-serverErr; serr != nil {
			t.Fatalf("server error: %v", serr)
		}
	})
}
