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
