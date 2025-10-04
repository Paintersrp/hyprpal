package layout

import "math"

// Rect represents a floating window geometry in logical pixels.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// SplitSidecar returns the primary and sidecar rectangles given a monitor rect and desired width percentage.
func SplitSidecar(monitor Rect, side string, widthPercent float64) (main Rect, dock Rect) {
	if widthPercent <= 0 {
		widthPercent = 25
	}
	if widthPercent > 50 {
		widthPercent = 50
	}
	dockWidth := monitor.Width * widthPercent / 100
	main = monitor
	dock = monitor
	if side == "right" {
		dock.X = monitor.X + monitor.Width - dockWidth
		main.Width = monitor.Width - dockWidth
	} else {
		dock.Width = dockWidth
		main.X = monitor.X + dockWidth
		main.Width = monitor.Width - dockWidth
	}
	dock.Width = dockWidth
	return main, dock
}

// ApproximatelyEqual reports whether two rects are almost equal.
func ApproximatelyEqual(a, b Rect) bool {
	const eps = 1.0
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps &&
		math.Abs(a.Width-b.Width) < eps && math.Abs(a.Height-b.Height) < eps
}
