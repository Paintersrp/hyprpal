package layout

import "testing"

func TestSplitSidecarLeft(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	_, dock := SplitSidecar(monitor, Insets{}, "left", 25, Gaps{})
	if dock.Width != 250 {
		t.Fatalf("expected dock width 250, got %v", dock.Width)
	}
	if dock.X != 0 {
		t.Fatalf("expected dock X to remain 0")
	}
}

func TestSplitSidecarClampsMinimumWidth(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	main, dock := SplitSidecar(monitor, Insets{}, "right", 5, Gaps{})
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
	main, dock := SplitSidecar(monitor, Insets{}, "left", 25, Gaps{Inner: 10, Outer: 20})
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

func TestSplitSidecarRespectsReservedInsets(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	reserved := Insets{Top: 40, Bottom: 20, Left: 30, Right: 0}
	main, dock := SplitSidecar(monitor, reserved, "left", 25, Gaps{})
	if dock.X != 30 {
		t.Fatalf("expected dock to start after left reserved inset, got %v", dock.X)
	}
	if dock.Y != 40 {
		t.Fatalf("expected dock to start after top reserved inset, got %v", dock.Y)
	}
	if dock.Height != 1020 {
		t.Fatalf("expected dock height to shrink by reserved insets, got %v", dock.Height)
	}
	if main.X <= dock.X {
		t.Fatalf("expected main section to start after dock and inner gap")
	}
}

func TestInsetsOverrideApplyOverridesValues(t *testing.T) {
	base := Insets{Top: 10, Bottom: 20, Left: 5, Right: 5}
	override := InsetsOverride{Top: floatPtr(30), Right: floatPtr(12)}
	out := override.Apply(base)
	if out.Top != 30 {
		t.Fatalf("expected top override to apply, got %v", out.Top)
	}
	if out.Bottom != 20 {
		t.Fatalf("expected bottom to remain base value, got %v", out.Bottom)
	}
	if out.Right != 12 {
		t.Fatalf("expected right override to apply, got %v", out.Right)
	}
	if out.Left != 5 {
		t.Fatalf("expected left to remain unchanged, got %v", out.Left)
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
