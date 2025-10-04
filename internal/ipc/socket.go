package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyprpal/hyprpal/internal/layout"
)

type socketDispatcher struct {
	path string
}

func newSocketDispatcher() (*socketDispatcher, error) {
	path, err := dispatchSocketPath()
	if err != nil {
		return nil, err
	}
	return &socketDispatcher{path: path}, nil
}

func (d *socketDispatcher) Dispatch(args ...string) error {
	if len(args) == 0 {
		return nil
	}
	return d.DispatchBatch([][]string{args})
}

func (d *socketDispatcher) DispatchBatch(commands [][]string) error {
	if len(commands) == 0 {
		return nil
	}
	conn, err := net.Dial("unix", d.path)
	if err != nil {
		return fmt.Errorf("connect dispatch socket: %w", err)
	}
	defer conn.Close()

	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		if len(cmd) == 0 {
			continue
		}
		parts := append([]string{"dispatch"}, cmd...)
		lines = append(lines, strings.Join(parts, " "))
	}
	if len(lines) == 0 {
		return nil
	}
	payload := strings.Join(lines, "\n") + "\n"
	if _, err := conn.Write([]byte(payload)); err != nil {
		return fmt.Errorf("write dispatch payload: %w", err)
	}
	return nil
}

func (d *socketDispatcher) DispatchSocketPath() string {
	return d.path
}

func dispatchSocketPath() (string, error) {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return "", fmt.Errorf("HYPRLAND_INSTANCE_SIGNATURE not set")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	return filepath.Join(runtimeDir, "hypr", sig, ".socket.sock"), nil
}

var _ layout.BatchDispatcher = (*socketDispatcher)(nil)
