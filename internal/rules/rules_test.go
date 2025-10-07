package rules

import (
	"testing"
	"time"

	"github.com/hyprpal/hyprpal/internal/config"
)

func TestBuildModesPropagatesThrottle(t *testing.T) {
	cfg := &config.Config{
		Modes: []config.ModeConfig{{
			Name: "Test",
			Rules: []config.RuleConfig{{
				Name:     "Limited",
				Actions:  []config.ActionConfig{{Type: "layout.fullscreen"}},
				Throttle: &config.RuleThrottle{FiringLimit: 5, WindowMs: 2000},
			}},
		}},
	}

	modes, err := BuildModes(cfg)
	if err != nil {
		t.Fatalf("BuildModes returned error: %v", err)
	}
	if len(modes) != 1 || len(modes[0].Rules) != 1 {
		t.Fatalf("unexpected modes length: %+v", modes)
	}
	throttle := modes[0].Rules[0].Throttle
	if throttle == nil {
		t.Fatalf("expected throttle to be set")
	}
	if len(throttle.Windows) != 1 {
		t.Fatalf("expected one throttle window, got %d", len(throttle.Windows))
	}
	if throttle.Windows[0].FiringLimit != 5 {
		t.Fatalf("unexpected firing limit: %d", throttle.Windows[0].FiringLimit)
	}
	if throttle.Windows[0].Window != 2*time.Second {
		t.Fatalf("unexpected throttle window: %v", throttle.Windows[0].Window)
	}
}
