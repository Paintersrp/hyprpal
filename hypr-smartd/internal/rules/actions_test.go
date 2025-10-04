package rules

import "testing"

func TestBuildSidecarDockRejectsNarrowWidth(t *testing.T) {
	_, err := buildSidecarDock(map[string]interface{}{
		"workspace":    1,
		"widthPercent": 5,
	})
	if err == nil {
		t.Fatalf("expected error for widthPercent below minimum")
	}
}
