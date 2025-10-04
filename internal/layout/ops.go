package layout

import (
	"errors"
	"fmt"
	"strings"
)

// Dispatcher executes hyprctl dispatch commands.
type Dispatcher interface {
	Dispatch(args ...string) error
}

// BatchDispatcher allows issuing multiple dispatch commands in a single operation.
type BatchDispatcher interface {
	Dispatch(args ...string) error
	DispatchBatch(commands [][]string) error
}

// ErrBatchUnsupported signals that the dispatcher cannot perform batch dispatches.
var ErrBatchUnsupported = errors.New("batch dispatch unsupported")

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
	if batcher, ok := d.(BatchDispatcher); ok {
		if err := batcher.DispatchBatch(p.Commands); err == nil {
			return nil
		} else if !errors.Is(err, ErrBatchUnsupported) {
			if len(p.Commands) > 0 {
				return fmt.Errorf("batch dispatch failed: %w", err)
			}
			return err
		}
	}
	for _, cmd := range p.Commands {
		if err := d.Dispatch(cmd...); err != nil {
			return fmt.Errorf("dispatch %s: %w", strings.Join(cmd, " "), err)
		}
	}
	return nil
}
