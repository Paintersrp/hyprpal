package util

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
)

type LogLevel int32

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[string]LogLevel{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

// Logger wraps the standard library logger with basic level filtering.
type Logger struct {
	level atomic.Int32
	base  *log.Logger
}

// NewLogger creates a level-aware logger writing to stderr.
func NewLogger(level LogLevel) *Logger {
	return NewLoggerWithWriter(level, os.Stderr)
}

// NewLoggerWithWriter creates a level-aware logger writing to the provided destination.
func NewLoggerWithWriter(level LogLevel, w io.Writer) *Logger {
	l := &Logger{base: log.New(w, "", log.LstdFlags|log.Lmsgprefix)}
	l.level.Store(int32(level))
	return l
}

func (l *Logger) SetLevel(level LogLevel) {
	l.level.Store(int32(level))
}

func (l *Logger) Level() LogLevel {
	return LogLevel(l.level.Load())
}

func (l *Logger) logf(level LogLevel, prefix string, format string, args ...interface{}) {
	if level < LogLevel(l.level.Load()) {
		return
	}
	l.base.Printf("[%s] %s", strings.ToUpper(prefix), fmt.Sprintf(format, args...))
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.logf(LevelDebug, "debug", format, args...)
}
func (l *Logger) Infof(format string, args ...interface{}) {
	l.logf(LevelInfo, "info", format, args...)
}
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.logf(LevelWarn, "warn", format, args...)
}
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.logf(LevelError, "error", format, args...)
}

// ParseLogLevel converts a string into a LogLevel, defaulting to info.
func ParseLogLevel(s string) LogLevel {
	if lvl, ok := levelNames[strings.ToLower(s)]; ok {
		return lvl
	}
	return LevelInfo
}
