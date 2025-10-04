package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/state"
	"github.com/hyprpal/hyprpal/internal/util"
)

// Client wraps hyprctl shell-outs.
type Client struct {
	Binary string
}

// NewClient returns a hyprctl client using the binary on PATH.
func NewClient() *Client {
	return &Client{Binary: "hyprctl"}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("hyprctl %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (c *Client) queryJSON(ctx context.Context, topic string) ([]byte, error) {
	return c.run(ctx, "-j", topic)
}

// ListClients returns all clients.
func (c *Client) ListClients(ctx context.Context) ([]state.Client, error) {
	data, err := c.queryJSON(ctx, "clients")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Address   string `json:"address"`
		Class     string `json:"class"`
		Title     string `json:"title"`
		Workspace struct {
			ID int `json:"id"`
		} `json:"workspace"`
		Monitor        any       `json:"monitor"`
		Floating       bool      `json:"floating"`
		At             []float64 `json:"at"`
		Size           []float64 `json:"size"`
		Focused        bool      `json:"focused"`
		FocusHistoryID int       `json:"focusHistoryID"`
		FullscreenMode int       `json:"fullscreenMode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode clients: %w", err)
	}
	clients := make([]state.Client, 0, len(raw))
	for _, cl := range raw {
		var monitorName string
		switch v := cl.Monitor.(type) {
		case string:
			monitorName = v
		case float64:
			monitorName = fmt.Sprintf("%d", int(v))
		}
		rect := layout.Rect{}
		if len(cl.At) == 2 {
			rect.X = cl.At[0]
			rect.Y = cl.At[1]
		}
		if len(cl.Size) == 2 {
			rect.Width = cl.Size[0]
			rect.Height = cl.Size[1]
		}
		focused := cl.Focused
		if cl.FocusHistoryID == 0 {
			focused = true
		}
		clients = append(clients, state.Client{
			Address:        cl.Address,
			Class:          cl.Class,
			Title:          cl.Title,
			WorkspaceID:    cl.Workspace.ID,
			MonitorName:    monitorName,
			Floating:       cl.Floating,
			Geometry:       rect,
			Focused:        focused,
			FullscreenMode: cl.FullscreenMode,
		})
	}
	return clients, nil
}

// ListWorkspaces returns workspaces.
func (c *Client) ListWorkspaces(ctx context.Context) ([]state.Workspace, error) {
	data, err := c.queryJSON(ctx, "workspaces")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		MonitorName string `json:"monitor"`
		Windows     int    `json:"windows"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode workspaces: %w", err)
	}
	workspaces := make([]state.Workspace, 0, len(raw))
	for _, ws := range raw {
		workspaces = append(workspaces, state.Workspace{
			ID:          ws.ID,
			Name:        ws.Name,
			MonitorName: ws.MonitorName,
			Windows:     ws.Windows,
		})
	}
	return workspaces, nil
}

// ListMonitors returns monitor snapshots.
func (c *Client) ListMonitors(ctx context.Context) ([]state.Monitor, error) {
	data, err := c.queryJSON(ctx, "monitors")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID              int     `json:"id"`
		Name            string  `json:"name"`
		X               float64 `json:"x"`
		Y               float64 `json:"y"`
		Width           float64 `json:"width"`
		Height          float64 `json:"height"`
		ActiveWorkspace struct {
			ID int `json:"id"`
		} `json:"activeWorkspace"`
		FocusedWorkspace struct {
			ID int `json:"id"`
		} `json:"focusedWorkspace"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode monitors: %w", err)
	}
	monitors := make([]state.Monitor, 0, len(raw))
	for _, m := range raw {
		monitors = append(monitors, state.Monitor{
			ID:                 m.ID,
			Name:               m.Name,
			Rectangle:          layout.Rect{X: m.X, Y: m.Y, Width: m.Width, Height: m.Height},
			ActiveWorkspaceID:  m.ActiveWorkspace.ID,
			FocusedWorkspaceID: m.FocusedWorkspace.ID,
		})
	}
	return monitors, nil
}

// ActiveWorkspaceID returns currently focused workspace id.
func (c *Client) ActiveWorkspaceID(ctx context.Context) (int, error) {
	data, err := c.queryJSON(ctx, "activeworkspace")
	if err != nil {
		return 0, err
	}
	var payload struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, fmt.Errorf("decode activeworkspace: %w", err)
	}
	return payload.ID, nil
}

// ActiveClientAddress returns active client address.
func (c *Client) ActiveClientAddress(ctx context.Context) (string, error) {
	data, err := c.queryJSON(ctx, "activewindow")
	if err != nil {
		return "", err
	}
	var payload struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode activewindow: %w", err)
	}
	return payload.Address, nil
}

// Dispatch invokes `hyprctl dispatch`.
func (c *Client) Dispatch(args ...string) error {
	ctx := context.Background()
	dispatchArgs := append([]string{"dispatch"}, args...)
	_, err := c.run(ctx, dispatchArgs...)
	return err
}

var _ state.DataSource = (*Client)(nil)
var _ layout.Dispatcher = (*Client)(nil)

// DispatchStrategy describes how dispatch commands are issued to Hyprland.
type DispatchStrategy string

const (
	// DispatchStrategySocket uses the Hyprland command socket directly.
	DispatchStrategySocket DispatchStrategy = "socket"
	// DispatchStrategyHyprctl shells out to the hyprctl binary.
	DispatchStrategyHyprctl DispatchStrategy = "hyprctl"
)

// EngineClient exposes a dispatcher strategy backed by the hyprctl data source.
type EngineClient struct {
	*Client
	dispatcher layout.Dispatcher
}

// Dispatch forwards dispatch requests to the active dispatcher.
func (c *EngineClient) Dispatch(args ...string) error {
	if c.dispatcher != nil {
		return c.dispatcher.Dispatch(args...)
	}
	return c.Client.Dispatch(args...)
}

// DispatchBatch attempts to batch dispatches when supported by the active dispatcher.
func (c *EngineClient) DispatchBatch(commands [][]string) error {
	if c.dispatcher == nil {
		return layout.ErrBatchUnsupported
	}
	if batcher, ok := c.dispatcher.(layout.BatchDispatcher); ok {
		return batcher.DispatchBatch(commands)
	}
	return layout.ErrBatchUnsupported
}

// NewEngineClient returns a client suitable for the engine using the requested strategy when possible.
func NewEngineClient(logger *util.Logger, requested DispatchStrategy) (*EngineClient, DispatchStrategy, error) {
	base := NewClient()
	switch requested {
	case DispatchStrategySocket:
		disp, err := newSocketDispatcher()
		if err != nil {
			if logger != nil {
				logger.Warnf("falling back to hyprctl dispatch: %v", err)
			}
			return &EngineClient{Client: base}, DispatchStrategyHyprctl, nil
		}
		if logger != nil {
			logger.Debugf("using socket dispatch at %s", disp.DispatchSocketPath())
		}
		return &EngineClient{Client: base, dispatcher: disp}, DispatchStrategySocket, nil
	case DispatchStrategyHyprctl:
		return &EngineClient{Client: base}, DispatchStrategyHyprctl, nil
	default:
		return nil, "", fmt.Errorf("unknown dispatch strategy %q", requested)
	}
}

var _ layout.Dispatcher = (*EngineClient)(nil)
var _ layout.BatchDispatcher = (*EngineClient)(nil)
