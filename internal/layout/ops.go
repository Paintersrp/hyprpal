package layout

import (
	"fmt"
	"strings"
)

// Dispatcher executes hyprctl dispatch commands.
type Dispatcher interface {
	Dispatch(args ...string) error
}

// Plan is a collection of sequential hyprctl dispatch commands.
type Plan struct {
	Commands [][]string
}

// Add appends a dispatch invocation.
func (p *Plan) Add(args ...string) {
	p.Commands = append(p.Commands, args)
}

// Merge merges other plan into this one.
func (p *Plan) Merge(other Plan) {
	p.Commands = append(p.Commands, other.Commands...)
}

// FloatAndPlace ensures the client is floating and resized/moved to rect.
func FloatAndPlace(address string, rect Rect) Plan {
	var p Plan
	addr := fmt.Sprintf("address:%s", address)
	p.Add("setfloatingaddress", addr, "1")
	p.Add("focuswindow", addr)
	p.Add("movewindowpixel", "exact", fmt.Sprintf("%d", int(rect.X)), fmt.Sprintf("%d", int(rect.Y)))
	p.Add("resizewindowpixel", "exact", fmt.Sprintf("%d", int(rect.Width)), fmt.Sprintf("%d", int(rect.Height)))
	return p
}

// Focus focuses the provided client address.
func Focus(address string) Plan {
	var p Plan
	p.Add("focuswindow", fmt.Sprintf("address:%s", address))
	return p
}

// Fullscreen toggles fullscreen state for a target.
func Fullscreen(address string, enable bool) Plan {
	var p Plan
	val := "0"
	if enable {
		val = "1"
	}
	p.Add("fullscreen", fmt.Sprintf("address:%s", address), val)
	return p
}

// Execute applies the plan sequentially using dispatcher.
func (p Plan) Execute(d Dispatcher) error {
	for _, cmd := range p.Commands {
		if err := d.Dispatch(cmd...); err != nil {
			return fmt.Errorf("dispatch %s: %w", strings.Join(cmd, " "), err)
		}
	}
	return nil
}
