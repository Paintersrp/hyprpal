package rules

import "testing"

func TestBuildSidecarDockRejectsNarrowWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 5,
		"side":         "left",
	})
	if err == nil {
		t.Fatalf("expected error for widthPercent below minimum")
	}
}

func TestBuildSidecarDockRejectsWideWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 60,
		"side":         "right",
	})
	if err == nil {
		t.Fatalf("expected error for widthPercent above maximum")
	}
}
