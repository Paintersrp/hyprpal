package layout

import "testing"

func TestGridRectsNormalizesWeights(t *testing.T) {
	monitor := Rect{Width: 900, Height: 600}
	slots := []GridSlotSpec{
		{Name: "left", Row: 0, Col: 0},
		{Name: "right", Row: 0, Col: 1},
	}
	rects, err := GridRects(monitor, Gaps{}, Insets{}, []float64{2, 1}, []float64{1}, slots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	left := rects["left"]
	right := rects["right"]
	if left.Width <= right.Width {
		t.Fatalf("expected left column to be wider than right: left=%v right=%v", left.Width, right.Width)
	}
	totalWidth := left.Width + right.Width
	if diff := totalWidth - monitor.Width; diff > 0.01 || diff < -0.01 {
		t.Fatalf("expected columns to fill monitor width, diff=%v", diff)
	}
}

func TestGridRectsClampsSpanAndSkipsOverlap(t *testing.T) {
	monitor := Rect{Width: 800, Height: 800}
	slots := []GridSlotSpec{
		{Name: "primary", Row: 0, Col: 0, RowSpan: 5, ColSpan: 5},
		{Name: "secondary", Row: 0, Col: 0},
	}
	rects, err := GridRects(monitor, Gaps{Inner: 10}, Insets{}, []float64{1, 1}, []float64{1, 1}, slots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	primary, ok := rects["primary"]
	if !ok {
		t.Fatalf("expected primary rect to be returned")
	}
	if primary.Width <= 0 || primary.Height <= 0 {
		t.Fatalf("expected primary rect to have positive size, got %+v", primary)
	}
	if _, ok := rects["secondary"]; ok {
		t.Fatalf("expected overlapping secondary slot to be skipped")
	}
}
