package ipc

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSocketDispatcherDispatchBatch(t *testing.T) {
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
	defer listener.Close()

	disp, err := newSocketDispatcher()
	if err != nil {
		t.Fatalf("newSocketDispatcher: %v", err)
	}
	if got := disp.DispatchSocketPath(); got != socketPath {
		t.Fatalf("unexpected socket path: got %q want %q", got, socketPath)
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
	if err := disp.DispatchBatch(commands); err != nil {
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
		"dispatch focuswindow address:test",
		"dispatch movewindowpixel exact 0 0",
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

	if err := disp.Dispatch("focuswindow", "address:solo"); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var singleConnVal net.Conn
	select {
	case err := <-singleErr:
		t.Fatalf("single accept: %v", err)
	case singleConnVal = <-singleConn:
	}
	singleData, err := io.ReadAll(singleConnVal)
	singleConnVal.Close()
	if err != nil {
		t.Fatalf("read single payload: %v", err)
	}
	single := strings.TrimSpace(string(singleData))
	if single != "dispatch focuswindow address:solo" {
		t.Fatalf("unexpected single payload: %q", single)
	}
}

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	original, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if !had {
			os.Unsetenv(key)
			return
		}
		os.Setenv(key, original)
	})
}
