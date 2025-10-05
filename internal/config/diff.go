package config

import (
	"strings"

	"github.com/google/go-cmp/cmp"
)

// DiffSerialized returns a unified diff between two serialized configuration payloads.
func DiffSerialized(previous, current []byte) string {
	prevLines := splitLines(previous)
	currLines := splitLines(current)
	if diff := cmp.Diff(prevLines, currLines); diff != "" {
		return diff
	}
	return ""
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}
