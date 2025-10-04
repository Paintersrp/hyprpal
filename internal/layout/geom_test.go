package layout

import "testing"

func TestSplitSidecarLeft(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	_, dock := SplitSidecar(monitor, "left", 25, Gaps{})
	if dock.Width != 250 {
		t.Fatalf("expected dock width 250, got %v", dock.Width)
	}
	if dock.X != 0 {
		t.Fatalf("expected dock X to remain 0")
	}
}

func TestSplitSidecarClampsMinimumWidth(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	main, dock := SplitSidecar(monitor, "right", 5, Gaps{})
	if dock.Width != 100 {
		t.Fatalf("expected dock width to clamp to 100, got %v", dock.Width)
	}
	if main.Width != 900 {
		t.Fatalf("expected main width to shrink to 900, got %v", main.Width)
	}
	if dock.X != 900 {
		t.Fatalf("expected dock X to start at 900, got %v", dock.X)
	}
}

func TestSplitSidecarAppliesGaps(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	main, dock := SplitSidecar(monitor, "left", 25, Gaps{Inner: 10, Outer: 20})
	if dock.X != 20 {
		t.Fatalf("expected dock X to respect outer gap, got %v", dock.X)
	}
	if dock.Width != 237.5 {
		t.Fatalf("expected dock width 237.5, got %v", dock.Width)
	}
	if main.X != 267.5 {
		t.Fatalf("expected main X 267.5, got %v", main.X)
	}
	if main.Height != 760 {
		t.Fatalf("expected main height 760, got %v", main.Height)
	}
}

func TestApproximatelyEqualUsesTolerance(t *testing.T) {
	a := Rect{Width: 100}
	b := Rect{Width: 101}
	if !ApproximatelyEqual(a, b, 1) {
		t.Fatalf("expected rects to be approximately equal within tolerance")
	}
	if ApproximatelyEqual(a, b, 0.5) {
		t.Fatalf("expected rects to differ when tolerance is too small")
	}
}
