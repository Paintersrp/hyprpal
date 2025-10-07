package control

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hyprpal/hyprpal/internal/state"
)

const (
	// SocketFileName is the filename of the control socket within the runtime dir.
	SocketFileName = "control.sock"

	// Action names supported by the control protocol.
	ActionModeGet      = "mode.get"
	ActionModeSet      = "mode.set"
	ActionReload       = "reload"
	ActionPlan         = "plan"
	ActionInspect      = "inspect" // legacy alias for inspector.get
	ActionInspectorGet = "inspector.get"
	ActionRulesStatus  = "rules.status"
	ActionRuleEnable   = "rules.enable"

	// Response statuses.
	StatusOK    = "ok"
	StatusError = "error"
)

// Request represents a control API request.
type Request struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// Response represents a control API response.
type Response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   any    `json:"data,omitempty"`
}

// ModeStatus describes the daemon's active mode and the available set.
type ModeStatus struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
}

// PlanCommand represents a single hyprctl dispatch planned by the daemon.
type PlanCommand struct {
	Dispatch []string `json:"dispatch"`
	Reason   string   `json:"reason,omitempty"`
}

// PlanResult captures the commands returned by the daemon when planning.
type PlanResult struct {
	Commands []PlanCommand `json:"commands"`
}

// RuleEvaluation captures a single rule evaluation outcome for the inspector view.
type RuleEvaluation struct {
	Timestamp time.Time  `json:"timestamp"`
	Mode      string     `json:"mode"`
	Rule      string     `json:"rule"`
	Status    string     `json:"status"`
	Commands  [][]string `json:"commands,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// InspectorSnapshot captures the daemon's last reconciled world snapshot, mode state, and rule log.
type InspectorSnapshot struct {
	Mode    ModeStatus       `json:"mode"`
	World   *state.World     `json:"world"`
	History []RuleEvaluation `json:"history,omitempty"`
}

// RuleStatus describes a rule's execution counters and disablement state as exposed over the
// control API.
type RuleStatus struct {
	Mode             string      `json:"mode"`
	Rule             string      `json:"rule"`
	TotalExecutions  int         `json:"totalExecutions"`
	RecentExecutions []time.Time `json:"recentExecutions,omitempty"`
	Disabled         bool        `json:"disabled"`
	DisabledReason   string      `json:"disabledReason,omitempty"`
	DisabledSince    time.Time   `json:"disabledSince,omitempty"`
}

// RulesStatus aggregates the execution state for all loaded rules.
type RulesStatus struct {
	Rules []RuleStatus `json:"rules"`
}

// DefaultSocketPath returns the expected location of the hyprpal control socket.
func DefaultSocketPath() (string, error) {
	if env := os.Getenv("HYPRPAL_CONTROL_SOCKET"); env != "" {
		return env, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	base := runtimeDir
	if base == "" {
		base = os.TempDir()
		if base == "" {
			return "", errors.New("no runtime directory available")
		}
	}
	return filepath.Join(base, "hyprpal", SocketFileName), nil
}
