package ipc

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setupSocketDispatcher(t *testing.T) (*socketDispatcher, net.Listener, string) {
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

	disp, err := newSocketDispatcher()
	if err != nil {
		t.Fatalf("newSocketDispatcher: %v", err)
	}
	return disp, listener, socketPath
}

func TestSocketDispatcherDispatchBatch(t *testing.T) {
	disp, listener, socketPath := setupSocketDispatcher(t)
	if got := disp.DispatchSocketPath(); got != socketPath {
		t.Fatalf("unexpected socket path: got %q want %q", got, socketPath)
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
	if err := disp.DispatchBatch(commands); err != nil {
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

	if err := disp.Dispatch("focuswindow", "address:solo"); err != nil {
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

func TestSocketDispatcherDispatchErrors(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		disp, listener, _ := setupSocketDispatcher(t)

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

		err := disp.Dispatch("focuswindow", "address:test")
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
		disp, listener, _ := setupSocketDispatcher(t)

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

		err := disp.DispatchBatch([][]string{
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
