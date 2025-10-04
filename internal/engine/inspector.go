package engine

import (
	"sync"
	"time"
)

type RuleEvaluationStatus string

const (
	RuleEvaluationStatusApplied RuleEvaluationStatus = "applied"
	RuleEvaluationStatusDryRun  RuleEvaluationStatus = "dry-run"
	RuleEvaluationStatusError   RuleEvaluationStatus = "error"

	inspectorHistoryLimit = 128
)

type RuleEvaluation struct {
	Timestamp time.Time            `json:"timestamp"`
	Mode      string               `json:"mode"`
	Rule      string               `json:"rule"`
	Status    RuleEvaluationStatus `json:"status"`
	Commands  [][]string           `json:"commands,omitempty"`
	Error     string               `json:"error,omitempty"`
}

type evaluationLog struct {
	mu      sync.Mutex
	entries []RuleEvaluation
	limit   int
}

func newEvaluationLog(limit int) *evaluationLog {
	if limit <= 0 {
		limit = inspectorHistoryLimit
	}
	return &evaluationLog{limit: limit}
}

func (l *evaluationLog) record(entry RuleEvaluation) {
	if l == nil {
		return
	}
	cloned := cloneCommands(entry.Commands)
	entry.Commands = cloned
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit > 0 && len(l.entries) == l.limit {
		copy(l.entries, l.entries[1:])
		l.entries = l.entries[:l.limit-1]
	}
	l.entries = append(l.entries, entry)
}

func (l *evaluationLog) snapshot() []RuleEvaluation {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return nil
	}
	out := make([]RuleEvaluation, len(l.entries))
	for i, entry := range l.entries {
		out[i] = RuleEvaluation{
			Timestamp: entry.Timestamp,
			Mode:      entry.Mode,
			Rule:      entry.Rule,
			Status:    entry.Status,
			Commands:  cloneCommands(entry.Commands),
			Error:     entry.Error,
		}
	}
	return out
}

func cloneCommands(src [][]string) [][]string {
	if len(src) == 0 {
		return nil
	}
	out := make([][]string, len(src))
	for i, cmd := range src {
		out[i] = append([]string(nil), cmd...)
	}
	return out
}
