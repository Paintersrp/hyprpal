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

// SplitSidecar returns the primary and sidecar rectangles given a monitor rect and desired width percentage.
func SplitSidecar(monitor Rect, side string, widthPercent float64, gaps Gaps) (main Rect, dock Rect) {
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
	usable := monitor
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
