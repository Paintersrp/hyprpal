package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestRunCheckSuccess(t *testing.T) {
	cfg := `managedWorkspaces: [1]
modes:
  - name: Test
    rules:
      - name: Example
        when:
          mode: Test
        actions:
          - type: layout.fullscreen
`
	path := writeTempConfig(t, cfg)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCheck([]string{"--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Configuration OK" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunCheckFailure(t *testing.T) {
	cfg := `managedWorkspaces: [0, 0]
manualReserved:
  DP-1:
    top: -1
modes:
  - name: ""
    rules:
      - name: ""
        actions: []
`
	path := writeTempConfig(t, cfg)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCheck([]string{"--config", path}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error from runCheck")
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("expected no stdout, got %q", stdout.String())
	}
	output := stderr.String()
	if !strings.Contains(output, "Configuration has") {
		t.Fatalf("expected aggregated error output, got %q", output)
	}
	if !strings.Contains(output, "managedWorkspaces[0]: must be positive, got 0") {
		t.Fatalf("missing managed workspace error: %q", output)
	}
	if !strings.Contains(output, "manualReserved.DP-1: cannot include negative values") {
		t.Fatalf("missing manualReserved error: %q", output)
	}
	if !strings.Contains(output, "modes[0].rules[0].actions: must define at least one action") {
		t.Fatalf("missing rule actions error: %q", output)
	}
}
