package layout

import (
	"math"
	"strings"
)

// Rect represents a floating window geometry in logical pixels.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// Gaps represents outer and inner gaps applied during layout calculations.
type Gaps struct {
	Inner float64
	Outer float64
}

// Insets describes reserved space around a rectangle.
type Insets struct {
	Top    float64
	Bottom float64
	Left   float64
	Right  float64
}

// InsetsOverride allows overriding individual inset values.
type InsetsOverride struct {
	Top    *float64
	Bottom *float64
	Left   *float64
	Right  *float64
}

// Apply returns a copy of base with any override values applied.
func (o InsetsOverride) Apply(base Insets) Insets {
	out := base
	if o.Top != nil {
		out.Top = *o.Top
	}
	if o.Bottom != nil {
		out.Bottom = *o.Bottom
	}
	if o.Left != nil {
		out.Left = *o.Left
	}
	if o.Right != nil {
		out.Right = *o.Right
	}
	return out
}

// Clone returns a deep copy of the override to avoid sharing pointers.
func (o InsetsOverride) Clone() InsetsOverride {
	clone := InsetsOverride{}
	if o.Top != nil {
		v := *o.Top
		clone.Top = &v
	}
	if o.Bottom != nil {
		v := *o.Bottom
		clone.Bottom = &v
	}
	if o.Left != nil {
		v := *o.Left
		clone.Left = &v
	}
	if o.Right != nil {
		v := *o.Right
		clone.Right = &v
	}
	return clone
}

func applyInsets(rect Rect, insets Insets) Rect {
	out := rect
	out.X += insets.Left
	out.Y += insets.Top
	out.Width -= insets.Left + insets.Right
	out.Height -= insets.Top + insets.Bottom
	if out.Width < 0 {
		out.Width = 0
	}
	if out.Height < 0 {
		out.Height = 0
	}
	return out
}

// SplitSidecar returns the primary and sidecar rectangles given a monitor rect and desired width percentage.
func SplitSidecar(monitor Rect, reserved Insets, side string, widthPercent float64, gaps Gaps) (main Rect, dock Rect) {
	const (
		defaultWidth = 25
		minWidth     = 10
		maxWidth     = 50
	)
	side = strings.ToLower(side)
	if widthPercent <= 0 {
		widthPercent = defaultWidth
	}
	if widthPercent < minWidth {
		widthPercent = minWidth
	}
	if widthPercent > maxWidth {
		widthPercent = maxWidth
	}
	usable := applyInsets(monitor, reserved)
	usable.X += gaps.Outer
	usable.Y += gaps.Outer
	usable.Width -= gaps.Outer * 2
	usable.Height -= gaps.Outer * 2
	if usable.Width < 0 {
		usable.Width = 0
	}
	if usable.Height < 0 {
		usable.Height = 0
	}

	horizontalSpan := usable.Width - gaps.Inner
	if horizontalSpan < 0 {
		horizontalSpan = 0
	}

	dockWidth := horizontalSpan * widthPercent / 100
	main = usable
	dock = usable
	main.Width = horizontalSpan - dockWidth
	if main.Width < 0 {
		main.Width = 0
	}
	if side == "right" {
		dock.X = usable.X + main.Width + gaps.Inner
	} else {
		dock.Width = dockWidth
		main.X = usable.X + dockWidth + gaps.Inner
	}
	dock.Width = dockWidth
	dock.Y = usable.Y
	dock.Height = usable.Height
	main.Y = usable.Y
	main.Height = usable.Height
	return main, dock
}

// ApproximatelyEqual reports whether two rects are almost equal.
func ApproximatelyEqual(a, b Rect, tolerance float64) bool {
	return math.Abs(a.X-b.X) <= tolerance && math.Abs(a.Y-b.Y) <= tolerance &&
		math.Abs(a.Width-b.Width) <= tolerance && math.Abs(a.Height-b.Height) <= tolerance
}
