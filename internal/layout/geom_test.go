package layout

import "testing"

func TestSplitSidecarLeft(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	_, dock := SplitSidecar(monitor, "left", 25)
	if dock.Width != 250 {
		t.Fatalf("expected dock width 250, got %v", dock.Width)
	}
	if dock.X != 0 {
		t.Fatalf("expected dock X to remain 0")
	}
}

func TestSplitSidecarClampsMinimumWidth(t *testing.T) {
	monitor := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	main, dock := SplitSidecar(monitor, "right", 5)
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
