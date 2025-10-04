package ipc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyprpal/hyprpal/internal/util"
)

// Event represents a Hyprland event stream payload.
type Event struct {
	Kind    string
	Payload string
}

// Subscribe connects to the Hyprland event socket and streams events until context cancellation.
func Subscribe(ctx context.Context, logger *util.Logger) (<-chan Event, error) {
	socket, err := eventSocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect event socket: %w", err)
	}
	events := make(chan Event)
	go func() {
		defer close(events)
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, ">>", 2)
			ev := Event{Kind: parts[0]}
			if len(parts) == 2 {
				ev.Payload = parts[1]
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			logger.Warnf("event stream error: %v", err)
		}
	}()
	return events, nil
}

func eventSocketPath() (string, error) {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return "", fmt.Errorf("HYPRLAND_INSTANCE_SIGNATURE not set")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	return filepath.Join(runtimeDir, "hypr", sig, ".socket2.sock"), nil
}
