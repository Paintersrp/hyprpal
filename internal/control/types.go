package control

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	// SocketFileName is the filename of the control socket within the runtime dir.
	SocketFileName = "control.sock"

	// Action names supported by the control protocol.
	ActionModeGet = "mode.get"
	ActionModeSet = "mode.set"
	ActionReload  = "reload"
	ActionPlan    = "plan"

	// Response statuses.
	StatusOK    = "ok"
	StatusError = "error"
)

// Request represents a control API request.
type Request struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// Response represents a control API response.
type Response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   any    `json:"data,omitempty"`
}

// ModeStatus describes the daemon's active mode and the available set.
type ModeStatus struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
}

// PlanCommand represents a single hyprctl dispatch planned by the daemon.
type PlanCommand struct {
	Dispatch []string `json:"dispatch"`
	Reason   string   `json:"reason,omitempty"`
}

// PlanResult captures the commands returned by the daemon when planning.
type PlanResult struct {
	Commands []PlanCommand `json:"commands"`
}

// DefaultSocketPath returns the expected location of the hyprpal control socket.
func DefaultSocketPath() (string, error) {
	if env := os.Getenv("HYPRPAL_CONTROL_SOCKET"); env != "" {
		return env, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	base := runtimeDir
	if base == "" {
		base = os.TempDir()
		if base == "" {
			return "", errors.New("no runtime directory available")
		}
	}
	return filepath.Join(base, "hyprpal", SocketFileName), nil
}
