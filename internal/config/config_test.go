package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProfilesDuplicateDetection(t *testing.T) {
	data := []byte(`
managedWorkspaces: []
modes:
  - name: Test
    rules:
      - name: Example
        when:
          mode: Test
        actions:
          - type: layout.fullscreen
profiles:
  foo:
    class: Slack
  foo:
    class: Discord
`)

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err == nil {
		t.Fatalf("expected duplicate profile error during unmarshal")
	}
}

func TestValidateUnknownProfileReference(t *testing.T) {
	cfg := Config{
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.fullscreen",
					Params: map[string]interface{}{
						"match": map[string]interface{}{"profile": "missing"},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for unknown profile reference")
	}
}

func TestValidateProfileDefinition(t *testing.T) {
	cfg := Config{
		Profiles: MatcherProfiles{
			"comms": {AnyClass: []string{"Slack", "Discord"}},
		},
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.fullscreen",
					Params: map[string]interface{}{
						"match": map[string]interface{}{"profile": "comms"},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestConfigUnmarshalToleranceAliases(t *testing.T) {
	t.Run("tolerancePx", func(t *testing.T) {
		data := []byte(`tolerancePx: 3.5`)
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TolerancePx != 3.5 {
			t.Fatalf("expected TolerancePx to be 3.5, got %v", cfg.TolerancePx)
		}
	})

	t.Run("placementTolerancePx", func(t *testing.T) {
		data := []byte(`placementTolerancePx: 1.25`)
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TolerancePx != 1.25 {
			t.Fatalf("expected legacy placementTolerancePx to populate TolerancePx, got %v", cfg.TolerancePx)
		}
	})
}
