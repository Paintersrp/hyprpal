package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hyprpal/hyprpal/internal/control/client"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/state"
)

const (
	defaultRefresh = 500 * time.Millisecond
	titleWidth     = 48
)

// Renderer periodically polls the daemon and renders a textual dashboard.
type Renderer struct {
	Client  *client.Client
	Writer  io.Writer
	Refresh time.Duration
}

// New returns a renderer configured with sensible defaults.
func New(cli *client.Client, w io.Writer) *Renderer {
	return &Renderer{Client: cli, Writer: w, Refresh: defaultRefresh}
}

// Run starts the render loop until the context is cancelled.
func (r *Renderer) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Writer == nil {
		r.Writer = os.Stdout
	}
	if r.Client == nil {
		return fmt.Errorf("tui renderer requires a control client")
	}

	refresh := r.Refresh
	if refresh <= 0 {
		refresh = defaultRefresh
	}

	ticker := time.NewTicker(refresh)
	defer ticker.Stop()

	fmt.Fprint(r.Writer, "\033[?25l")
	defer fmt.Fprint(r.Writer, "\033[?25h")

	r.render(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.render(ctx)
		}
	}
}

func (r *Renderer) render(ctx context.Context) {
	snapshot, err := r.Client.Inspect(ctx)

	var buf bytes.Buffer
	buf.WriteString("\033[H\033[2J")
	buf.WriteString("hyprpal inspector — Ctrl+C to exit\n")
	buf.WriteString(time.Now().Format(time.RFC1123))
	buf.WriteString("\n\n")

	if err != nil {
		buf.WriteString(fmt.Sprintf("error: %v\n", err))
		fmt.Fprint(r.Writer, buf.String())
		return
	}

	buf.WriteString(formatMode(snapshot.Mode))
	buf.WriteByte('\n')
	if snapshot.World == nil {
		buf.WriteString("Waiting for daemon to publish world snapshot...\n")
		fmt.Fprint(r.Writer, buf.String())
		return
	}

	buf.WriteString(renderMonitors(snapshot.World))
	buf.WriteString(renderWorkspaces(snapshot.World))
	buf.WriteString(renderClients(snapshot.World))
	fmt.Fprint(r.Writer, buf.String())
}

func formatMode(status client.ModeStatus) string {
	active := status.Active
	if active == "" {
		active = "(none)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Active mode: %s\n", active))
	if len(status.Available) > 0 {
		b.WriteString("Available: ")
		b.WriteString(strings.Join(status.Available, ", "))
		b.WriteByte('\n')
	}
	return b.String()
}

func renderMonitors(world *state.World) string {
	var b strings.Builder
	b.WriteString("Monitors:\n")
	monitors := append([]state.Monitor(nil), world.Monitors...)
	if len(monitors) == 0 {
		b.WriteString("  (none)\n\n")
		return b.String()
	}
	sort.Slice(monitors, func(i, j int) bool {
		return monitors[i].ID < monitors[j].ID
	})
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tGeometry\tActive WS\tFocused WS")
	for _, mon := range monitors {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\n", mon.ID, mon.Name, formatRect(mon.Rectangle), mon.ActiveWorkspaceID, mon.FocusedWorkspaceID)
	}
	tw.Flush()
	b.WriteByte('\n')
	return b.String()
}

func renderWorkspaces(world *state.World) string {
	var b strings.Builder
	b.WriteString("Workspaces:\n")
	workspaces := append([]state.Workspace(nil), world.Workspaces...)
	if len(workspaces) == 0 {
		b.WriteString("  (none)\n\n")
		return b.String()
	}
	sort.Slice(workspaces, func(i, j int) bool {
		return workspaces[i].ID < workspaces[j].ID
	})
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tMonitor\tWindows")
	for _, ws := range workspaces {
		id := fmt.Sprintf("%d", ws.ID)
		if ws.ID == world.ActiveWorkspaceID {
			id += "*"
		}
		name := ws.Name
		if name == "" {
			name = "(unnamed)"
		}
		monitor := ws.MonitorName
		if monitor == "" {
			monitor = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", id, name, monitor, ws.Windows)
	}
	tw.Flush()
	b.WriteByte('\n')
	return b.String()
}

func renderClients(world *state.World) string {
	var b strings.Builder
	b.WriteString("Clients:\n")
	clients := append([]state.Client(nil), world.Clients...)
	if len(clients) == 0 {
		b.WriteString("  (none)\n\n")
		return b.String()
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].WorkspaceID == clients[j].WorkspaceID {
			if clients[i].MonitorName == clients[j].MonitorName {
				return clients[i].Class < clients[j].Class
			}
			return clients[i].MonitorName < clients[j].MonitorName
		}
		return clients[i].WorkspaceID < clients[j].WorkspaceID
	})
	workspaceNames := make(map[int]string, len(world.Workspaces))
	for _, ws := range world.Workspaces {
		workspaceNames[ws.ID] = ws.Name
	}
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "Addr\tClass\tTitle\tWorkspace\tMonitor\tState")
	for _, cl := range clients {
		address := cl.Address
		if address == "" {
			address = "-"
		}
		if cl.Address == world.ActiveClientAddress {
			address = "*" + address
		}
		className := cl.Class
		if className == "" {
			className = "(unknown)"
		}
		title := cl.Title
		if title == "" {
			title = "(untitled)"
		}
		title = truncate(title, titleWidth)
		workspaceLabel := fmt.Sprintf("%d", cl.WorkspaceID)
		if name := workspaceNames[cl.WorkspaceID]; name != "" {
			workspaceLabel = fmt.Sprintf("%s (%s)", workspaceLabel, name)
		}
		if cl.WorkspaceID == world.ActiveWorkspaceID {
			workspaceLabel += "*"
		}
		monitor := cl.MonitorName
		if monitor == "" {
			monitor = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", address, className, title, workspaceLabel, monitor, clientState(cl, world.ActiveClientAddress))
	}
	tw.Flush()
	b.WriteByte('\n')
	return b.String()
}

func formatRect(rect layout.Rect) string {
	return fmt.Sprintf("%.0fx%.0f @ %.0f,%.0f", rect.Width, rect.Height, rect.X, rect.Y)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

func clientState(cl state.Client, active string) string {
	var parts []string
	if cl.Address == active {
		parts = append(parts, "active")
	} else if cl.Focused {
		parts = append(parts, "focused")
	}
	if cl.Floating {
		parts = append(parts, "floating")
	}
	if cl.FullscreenMode > 0 {
		parts = append(parts, "fullscreen")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
