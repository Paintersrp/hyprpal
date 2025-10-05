package layout

import "testing"

func TestSplitSidecarLeft(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	_, dock := SplitSidecar(monitor, "left", 25, Gaps{}, Insets{})
	if dock.Width != 250 {
		t.Fatalf("expected dock width 250, got %v", dock.Width)
	}
	if dock.X != 0 {
		t.Fatalf("expected dock X to remain 0")
	}
}

func TestSplitSidecarClampsMinimumWidth(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	main, dock := SplitSidecar(monitor, "right", 5, Gaps{}, Insets{})
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
	main, dock := SplitSidecar(monitor, "left", 25, Gaps{Inner: 10, Outer: 20}, Insets{})
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
	monitor := Rect{X: 0, Y: 0, Width: 1200, Height: 900}
	reserved := Insets{Top: 10, Right: 30, Bottom: 20, Left: 40}
	main, dock := SplitSidecar(monitor, "right", 25, Gaps{Inner: 10, Outer: 10}, reserved)
	if dock.X <= 0 {
		t.Fatalf("expected dock X to shift due to reserved and gaps, got %v", dock.X)
	}
	if dock.X != 1200-30-10-dock.Width {
		t.Fatalf("expected dock to align with reserved right edge, got X=%v width=%v", dock.X, dock.Width)
	}
	if main.X != reserved.Left+10 {
		t.Fatalf("expected main X to include left reserved and outer gap, got %v", main.X)
	}
	expectedHeight := 900 - reserved.Top - reserved.Bottom - 20
	if dock.Height != expectedHeight {
		t.Fatalf("expected dock height %v, got %v", expectedHeight, dock.Height)
	}
}

func TestInsetsFromSlice(t *testing.T) {
	got := InsetsFromSlice([]float64{1, 2, 3, 4})
	if got.Top != 1 || got.Right != 2 || got.Bottom != 3 || got.Left != 4 {
		t.Fatalf("unexpected insets from slice: %+v", got)
	}
}

func TestShrinkRectClampsToZero(t *testing.T) {
	rect := Rect{Width: 10, Height: 10}
	shrunk := Insets{Top: 6, Bottom: 6}.ShrinkRect(rect)
	if shrunk.Height != 0 {
		t.Fatalf("expected height to clamp to zero, got %v", shrunk.Height)
	}
	if shrunk.Width != 10 {
		t.Fatalf("expected width to remain when left/right zero, got %v", shrunk.Width)
	}
}
