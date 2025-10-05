package state

import (
	"context"
	"errors"
	"fmt"

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
	Reserved           layout.Insets
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

// UpsertClient inserts or replaces a client in the world and keeps workspace counts in sync.
func (w *World) UpsertClient(client Client) (bool, error) {
	if w == nil {
		return false, errors.New("world is nil")
	}
	if client.WorkspaceID != 0 && w.WorkspaceByID(client.WorkspaceID) == nil {
		return false, fmt.Errorf("workspace %d not found", client.WorkspaceID)
	}
	idx := -1
	for i := range w.Clients {
		if w.Clients[i].Address == client.Address {
			idx = i
			break
		}
	}
	if idx == -1 {
		w.Clients = append(w.Clients, client)
		if client.WorkspaceID != 0 {
			w.incrementWorkspaceWindows(client.WorkspaceID)
			if w.Clients[len(w.Clients)-1].MonitorName == "" {
				if ws := w.WorkspaceByID(client.WorkspaceID); ws != nil {
					w.Clients[len(w.Clients)-1].MonitorName = ws.MonitorName
				}
			}
		}
		w.RefreshFocus()
		return true, nil
	}
	prev := w.Clients[idx]
	w.Clients[idx] = client
	if prev.WorkspaceID != client.WorkspaceID {
		if prev.WorkspaceID != 0 {
			w.decrementWorkspaceWindows(prev.WorkspaceID)
		}
		if client.WorkspaceID != 0 {
			w.incrementWorkspaceWindows(client.WorkspaceID)
			if w.Clients[idx].MonitorName == "" {
				if ws := w.WorkspaceByID(client.WorkspaceID); ws != nil {
					w.Clients[idx].MonitorName = ws.MonitorName
				}
			}
		}
	}
	w.RefreshFocus()
	changed := prev != client
	return changed, nil
}

// RemoveClient removes the client with address from the world.
func (w *World) RemoveClient(address string) (Client, error) {
	if w == nil {
		return Client{}, errors.New("world is nil")
	}
	for i := range w.Clients {
		if w.Clients[i].Address == address {
			removed := w.Clients[i]
			if removed.WorkspaceID != 0 {
				w.decrementWorkspaceWindows(removed.WorkspaceID)
			}
			w.Clients = append(w.Clients[:i], w.Clients[i+1:]...)
			if w.ActiveClientAddress == address {
				w.ActiveClientAddress = ""
			}
			w.RefreshFocus()
			return removed, nil
		}
	}
	return Client{}, fmt.Errorf("client %s not found", address)
}

// MoveClient changes the workspace binding for a client.
func (w *World) MoveClient(address string, workspaceID int, monitorName string) (bool, error) {
	if w == nil {
		return false, errors.New("world is nil")
	}
	if workspaceID != 0 && w.WorkspaceByID(workspaceID) == nil {
		return false, fmt.Errorf("workspace %d not found", workspaceID)
	}
	for i := range w.Clients {
		if w.Clients[i].Address == address {
			prev := w.Clients[i]
			if prev.WorkspaceID == workspaceID {
				monitorTarget := monitorName
				if monitorTarget == "" {
					if ws := w.WorkspaceByID(workspaceID); ws != nil {
						monitorTarget = ws.MonitorName
					}
				}
				if monitorTarget == prev.MonitorName {
					return false, nil
				}
			}
			if prev.WorkspaceID != 0 {
				w.decrementWorkspaceWindows(prev.WorkspaceID)
			}
			if workspaceID != 0 {
				w.incrementWorkspaceWindows(workspaceID)
			}
			w.Clients[i].WorkspaceID = workspaceID
			if monitorName != "" {
				w.Clients[i].MonitorName = monitorName
			} else if ws := w.WorkspaceByID(workspaceID); ws != nil {
				w.Clients[i].MonitorName = ws.MonitorName
			}
			w.RefreshFocus()
			return true, nil
		}
	}
	return false, fmt.Errorf("client %s not found", address)
}

// SetActiveClient updates the active client address and client focus flags.
func (w *World) SetActiveClient(address string) bool {
	if w == nil {
		return false
	}
	if w.ActiveClientAddress == address {
		w.RefreshFocus()
		return false
	}
	w.ActiveClientAddress = address
	w.RefreshFocus()
	return true
}

// RefreshFocus recalculates the Focused flag for all clients.
func (w *World) RefreshFocus() {
	if w == nil {
		return
	}
	active := w.ActiveClientAddress
	for i := range w.Clients {
		w.Clients[i].Focused = active != "" && w.Clients[i].Address == active
	}
}

// SetActiveWorkspace updates the active workspace and monitor bindings.
func (w *World) SetActiveWorkspace(id int) (bool, error) {
	if w == nil {
		return false, errors.New("world is nil")
	}
	if id != 0 && w.WorkspaceByID(id) == nil {
		return false, fmt.Errorf("workspace %d not found", id)
	}
	if w.ActiveWorkspaceID == id {
		return false, nil
	}
	w.ActiveWorkspaceID = id
	if id != 0 {
		if ws := w.WorkspaceByID(id); ws != nil {
			w.updateMonitorWorkspaceBinding(ws.MonitorName, id)
		}
	}
	return true, nil
}

// BindWorkspaceToMonitor updates the monitor assignment for a workspace.
func (w *World) BindWorkspaceToMonitor(id int, monitorName string) (bool, error) {
	if w == nil {
		return false, errors.New("world is nil")
	}
	ws := w.WorkspaceByID(id)
	if ws == nil {
		return false, fmt.Errorf("workspace %d not found", id)
	}
	if ws.MonitorName == monitorName {
		return false, nil
	}
	ws.MonitorName = monitorName
	w.updateMonitorWorkspaceBinding(monitorName, id)
	return true, nil
}

// UpsertMonitor inserts or updates a monitor description.
func (w *World) UpsertMonitor(mon Monitor) bool {
	if w == nil {
		return false
	}
	for i := range w.Monitors {
		if w.Monitors[i].Name == mon.Name {
			if w.Monitors[i] == mon {
				return false
			}
			w.Monitors[i] = mon
			return true
		}
	}
	w.Monitors = append(w.Monitors, mon)
	return true
}

// RemoveMonitor deletes a monitor by name and clears workspace bindings.
func (w *World) RemoveMonitor(name string) (bool, error) {
	if w == nil {
		return false, errors.New("world is nil")
	}
	for i := range w.Monitors {
		if w.Monitors[i].Name == name {
			w.Monitors = append(w.Monitors[:i], w.Monitors[i+1:]...)
			for j := range w.Workspaces {
				if w.Workspaces[j].MonitorName == name {
					w.Workspaces[j].MonitorName = ""
				}
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("monitor %s not found", name)
}

func (w *World) updateMonitorWorkspaceBinding(name string, workspaceID int) {
	if name == "" {
		return
	}
	for i := range w.Monitors {
		if w.Monitors[i].Name == name {
			w.Monitors[i].ActiveWorkspaceID = workspaceID
			w.Monitors[i].FocusedWorkspaceID = workspaceID
		}
	}
}

func (w *World) incrementWorkspaceWindows(id int) {
	if id == 0 {
		return
	}
	if ws := w.WorkspaceByID(id); ws != nil {
		ws.Windows++
	}
}

func (w *World) decrementWorkspaceWindows(id int) {
	if id == 0 {
		return
	}
	if ws := w.WorkspaceByID(id); ws != nil {
		if ws.Windows > 0 {
			ws.Windows--
		}
	}
}
