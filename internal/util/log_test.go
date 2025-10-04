package util

import "testing"

func TestParseLogLevel(t *testing.T) {
	tests := map[string]LogLevel{
		"trace": LevelTrace,
		"TRACE": LevelTrace,
		"debug": LevelDebug,
		"info":  LevelInfo,
		"warn":  LevelWarn,
		"error": LevelError,
	}

	for input, want := range tests {
		if got := ParseLogLevel(input); got != want {
			t.Fatalf("ParseLogLevel(%q) = %v, want %v", input, got, want)
		}
	}

	if got := ParseLogLevel("unknown"); got != LevelInfo {
		t.Fatalf("ParseLogLevel default = %v, want %v", got, LevelInfo)
	}
}
