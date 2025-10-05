package config

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hyprpal/hyprpal/internal/layout"
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
		ManualReserved: map[string]layout.Insets{"*": {}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateProfileAllOfCombination(t *testing.T) {
	cfg := Config{
		Profiles: MatcherProfiles{
			"comms": {AnyClass: []string{"Slack", "Discord"}},
			"focus": {TitleRegex: "Focus"},
		},
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.fullscreen",
					Params: map[string]interface{}{
						"match": map[string]interface{}{
							"allOfProfiles": []interface{}{"comms", "focus"},
						},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateProfileAnyOfCombination(t *testing.T) {
	cfg := Config{
		Profiles: MatcherProfiles{
			"chat": {Class: "Slack"},
			"mail": {Class: "Thunderbird"},
		},
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.grid",
					Params: map[string]interface{}{
						"workspace": 1,
						"slots": []interface{}{
							map[string]interface{}{
								"name": "chat",
								"row":  0,
								"col":  0,
								"match": map[string]interface{}{
									"anyOfProfiles": []interface{}{"chat", "mail"},
								},
							},
						},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateAnyOfProfilesEmptyList(t *testing.T) {
	cfg := Config{
		Profiles: MatcherProfiles{"chat": {Class: "Slack"}},
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.fullscreen",
					Params: map[string]interface{}{
						"match": map[string]interface{}{
							"anyOfProfiles": []interface{}{},
						},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for empty anyOfProfiles")
	}
}

func TestValidateGridAllOfProfilesUnknown(t *testing.T) {
	cfg := Config{
		Profiles: MatcherProfiles{"chat": {Class: "Slack"}},
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name: "Rule",
				When: PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{
					Type: "layout.grid",
					Params: map[string]interface{}{
						"workspace": 1,
						"slots": []interface{}{
							map[string]interface{}{
								"name": "chat",
								"row":  0,
								"col":  0,
								"match": map[string]interface{}{
									"allOfProfiles": []interface{}{"chat", "missing"},
								},
							},
						},
					},
				}},
			}},
		}},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for unknown profile in grid allOfProfiles")
	}
}

func TestValidateManualReservedRejectsNegative(t *testing.T) {
	cfg := Config{
		Modes: []ModeConfig{{
			Name: "Test",
			Rules: []RuleConfig{{
				Name:    "Rule",
				When:    PredicateConfig{Mode: "Test"},
				Actions: []ActionConfig{{Type: "layout.fullscreen"}},
			}},
		}},
		ManualReserved: map[string]layout.Insets{
			"DP-1": {Top: -5},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for negative manualReserved values")
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

	t.Run("defaultWhenAbsent", func(t *testing.T) {
		var cfg Config
		if err := yaml.Unmarshal([]byte(`{}`), &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg.applyDefaults()
		if cfg.TolerancePx != 2 {
			t.Fatalf("expected default tolerancePx of 2, got %v", cfg.TolerancePx)
		}
	})

	t.Run("explicitZeroPreserved", func(t *testing.T) {
		data := []byte(`tolerancePx: 0`)
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg.applyDefaults()
		if cfg.TolerancePx != 0 {
			t.Fatalf("expected explicit tolerancePx of 0 to be preserved, got %v", cfg.TolerancePx)
		}
	})
}

func TestRuleConfigUnmarshalMutateAlias(t *testing.T) {
	t.Run("mutateUnmanaged", func(t *testing.T) {
		data := []byte(`
modes:
  - name: Test
    rules:
      - name: Example
        when:
          mode: Test
        mutateUnmanaged: true
        actions:
          - type: layout.fullscreen
`)

		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Modes[0].Rules[0].MutateUnmanaged {
			t.Fatalf("expected mutateUnmanaged to be true")
		}
	})

	t.Run("allowUnmanagedLegacy", func(t *testing.T) {
		data := []byte(`
modes:
  - name: Test
    rules:
      - name: Example
        when:
          mode: Test
        allowUnmanaged: true
        actions:
          - type: layout.fullscreen
`)

		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Modes[0].Rules[0].MutateUnmanaged {
			t.Fatalf("expected legacy allowUnmanaged to populate mutateUnmanaged")
		}
	})
}
