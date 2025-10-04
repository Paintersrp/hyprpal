package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// defaultTimeout is used when the caller does not provide a context deadline.
	defaultTimeout = 3 * time.Second
	// socketFileName is the filename of the control socket within the runtime dir.
	socketFileName = "control.sock"
)

// Client talks to the running hyprpal daemon over its control socket.
type Client struct {
	socketPath string
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

// New creates a client that connects to the provided socket path. When path is
// empty, the default runtime path is used.
func New(path string) (*Client, error) {
	if path == "" {
		var err error
		path, err = DefaultSocketPath()
		if err != nil {
			return nil, err
		}
	}
	return &Client{socketPath: path}, nil
}

// DefaultSocketPath returns the expected location of the hyprpal control socket.
func DefaultSocketPath() (string, error) {
	if env := os.Getenv("HYPRPAL_CONTROL_SOCKET"); env != "" {
		return env, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", errors.New("XDG_RUNTIME_DIR not set")
	}
	return filepath.Join(runtimeDir, "hyprpal", socketFileName), nil
}

// Mode retrieves the daemon's active mode along with the list of available modes.
func (c *Client) Mode(ctx context.Context) (ModeStatus, error) {
	var status ModeStatus
	if err := c.do(ctx, request{Action: "mode.get"}, &status); err != nil {
		return ModeStatus{}, err
	}
	return status, nil
}

// SetMode instructs the daemon to switch to the provided mode name.
func (c *Client) SetMode(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("mode name cannot be empty")
	}
	payload := request{Action: "mode.set", Params: map[string]any{"name": name}}
	return c.do(ctx, payload, nil)
}

// Reload asks the daemon to reload its configuration.
func (c *Client) Reload(ctx context.Context) error {
	return c.do(ctx, request{Action: "reload"}, nil)
}

// Plan requests the daemon to compute and optionally explain the next plan.
func (c *Client) Plan(ctx context.Context, explain bool) (PlanResult, error) {
	params := map[string]any{"explain": explain}
	var result PlanResult
	if err := c.do(ctx, request{Action: "plan", Params: params}, &result); err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

type request struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

type response struct {
	Status string          `json:"status"`
	Error  string          `json:"error,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

func (c *Client) do(ctx context.Context, req request, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial control socket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Status != "ok" {
		if resp.Error == "" {
			resp.Error = "unknown control error"
		}
		return errors.New(resp.Error)
	}
	if out == nil || resp.Data == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}
