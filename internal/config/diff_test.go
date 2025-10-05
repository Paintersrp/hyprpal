package config

import (
	"strings"
	"testing"
)

func TestDiffSerialized(t *testing.T) {
	oldData := []byte("a: 1\nb: 2\n")
	newData := []byte("a: 1\nb: 3\n")

	diff := DiffSerialized(oldData, newData)
	if diff == "" {
		t.Fatalf("expected diff, got empty string")
	}
	if !strings.Contains(diff, "b: 2") {
		t.Fatalf("expected diff to contain original line, got %s", diff)
	}
	if !strings.Contains(diff, "b: 3") {
		t.Fatalf("expected diff to contain updated line, got %s", diff)
	}
}
