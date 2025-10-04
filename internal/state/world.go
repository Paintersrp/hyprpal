package state

import (
	"context"
	"errors"

	"github.com/hyprpal/hyprpal/internal/layout"
)

// Client describes a Hyprland client window.
type Client struct {
	Address        string
	Class          string
	Title          string
	WorkspaceID    int
	MonitorName    string
	Floating       bool
	Geometry       layout.Rect
	Focused        bool
	FullscreenMode int
}

// Workspace describes a Hyprland workspace.
type Workspace struct {
	ID          int
	Name        string
	MonitorName string
	Windows     int
}

// Monitor describes a monitor and its logical size.
type Monitor struct {
	ID                 int
	Name               string
	Rectangle          layout.Rect
	ActiveWorkspaceID  int
	FocusedWorkspaceID int
}

// World represents the current snapshot of Hyprland.
type World struct {
	Clients             []Client
	Workspaces          []Workspace
	Monitors            []Monitor
	ActiveWorkspaceID   int
	ActiveClientAddress string
}

// DataSource abstracts queries required to build the world snapshot.
type DataSource interface {
	ListClients(ctx context.Context) ([]Client, error)
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	ListMonitors(ctx context.Context) ([]Monitor, error)
	ActiveWorkspaceID(ctx context.Context) (int, error)
	ActiveClientAddress(ctx context.Context) (string, error)
}

// NewWorld creates a world snapshot using the provided data source.
func NewWorld(ctx context.Context, src DataSource) (*World, error) {
	clients, err := src.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	workspaces, err := src.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	monitors, err := src.ListMonitors(ctx)
	if err != nil {
		return nil, err
	}
	activeWS, err := src.ActiveWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	activeClient, err := src.ActiveClientAddress(ctx)
	if err != nil {
		return nil, err
	}
	world := &World{
		Clients:             clients,
		Workspaces:          workspaces,
		Monitors:            monitors,
		ActiveWorkspaceID:   activeWS,
		ActiveClientAddress: activeClient,
	}
	workspaceMonitor := make(map[int]string)
	for _, ws := range workspaces {
		workspaceMonitor[ws.ID] = ws.MonitorName
	}
	for i := range world.Clients {
		c := &world.Clients[i]
		if c.MonitorName == "" {
			if name, ok := workspaceMonitor[c.WorkspaceID]; ok {
				c.MonitorName = name
			}
		}
	}
	return world, nil
}

// FindClient returns the client with address, or nil.
func (w *World) FindClient(address string) *Client {
	for i := range w.Clients {
		if w.Clients[i].Address == address {
			return &w.Clients[i]
		}
	}
	return nil
}

// ActiveClient returns the active client if present.
func (w *World) ActiveClient() *Client {
	if w.ActiveClientAddress == "" {
		return nil
	}
	return w.FindClient(w.ActiveClientAddress)
}

// MonitorByName finds a monitor by name.
func (w *World) MonitorByName(name string) *Monitor {
	for i := range w.Monitors {
		if w.Monitors[i].Name == name {
			return &w.Monitors[i]
		}
	}
	return nil
}

// WorkspaceByID finds workspace by ID.
func (w *World) WorkspaceByID(id int) *Workspace {
	for i := range w.Workspaces {
		if w.Workspaces[i].ID == id {
			return &w.Workspaces[i]
		}
	}
	return nil
}

// MonitorForWorkspace resolves the monitor owning the workspace ID.
func (w *World) MonitorForWorkspace(id int) (*Monitor, error) {
	ws := w.WorkspaceByID(id)
	if ws == nil {
		return nil, errors.New("workspace not found")
	}
	mon := w.MonitorByName(ws.MonitorName)
	if mon == nil {
		return nil, errors.New("monitor not found for workspace")
	}
	return mon, nil
}

// CloneWorld returns a deep copy of the provided world snapshot.
func CloneWorld(src *World) *World {
	if src == nil {
		return nil
	}
	copyWorld := *src
	if len(src.Clients) > 0 {
		copyWorld.Clients = append([]Client(nil), src.Clients...)
	}
	if len(src.Workspaces) > 0 {
		copyWorld.Workspaces = append([]Workspace(nil), src.Workspaces...)
	}
	if len(src.Monitors) > 0 {
		copyWorld.Monitors = append([]Monitor(nil), src.Monitors...)
	}
	return &copyWorld
}
