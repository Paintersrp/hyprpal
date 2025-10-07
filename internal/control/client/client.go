package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/hyprpal/hyprpal/internal/control"
)

const (
	// defaultTimeout is used when the caller does not provide a context deadline.
	defaultTimeout = 3 * time.Second
)

// Client talks to the running hyprpal daemon over its control socket.
type Client struct {
	socketPath string
}

type (
	// ModeStatus describes the daemon's active mode and the available set.
	ModeStatus = control.ModeStatus
	// PlanCommand represents a single hyprctl dispatch planned by the daemon.
	PlanCommand = control.PlanCommand
	// PlanResult captures the commands returned by the daemon when planning.
	PlanResult = control.PlanResult
	// RuleEvaluation mirrors the inspector rule log entry returned by the daemon.
	RuleEvaluation = control.RuleEvaluation
	// InspectorState captures the daemon's inspector payload.
	InspectorState = control.InspectorSnapshot
	// RuleStatus mirrors the rule counter payload returned by the daemon.
	RuleStatus = control.RuleStatus
	// RuleThrottle mirrors the throttle window payload returned by the daemon.
	RuleThrottle = control.RuleThrottle
	// RuleThrottleWindow mirrors a single throttle window configuration.
	RuleThrottleWindow = control.RuleThrottleWindow
	// RulesStatus aggregates rule execution state for all modes.
	RulesStatus = control.RulesStatus
)

// New creates a client that connects to the provided socket path. When path is
// empty, the default runtime path is used.
func New(path string) (*Client, error) {
	if path == "" {
		var err error
		path, err = control.DefaultSocketPath()
		if err != nil {
			return nil, err
		}
	}
	return &Client{socketPath: path}, nil
}

// Mode retrieves the daemon's active mode along with the list of available modes.
func (c *Client) Mode(ctx context.Context) (ModeStatus, error) {
	var status ModeStatus
	if err := c.do(ctx, control.Request{Action: control.ActionModeGet}, &status); err != nil {
		return ModeStatus{}, err
	}
	return status, nil
}

// SetMode instructs the daemon to switch to the provided mode name.
func (c *Client) SetMode(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("mode name cannot be empty")
	}
	payload := control.Request{Action: control.ActionModeSet, Params: map[string]any{"name": name}}
	return c.do(ctx, payload, nil)
}

// Reload asks the daemon to reload its configuration.
func (c *Client) Reload(ctx context.Context) error {
	return c.do(ctx, control.Request{Action: control.ActionReload}, nil)
}

// Plan requests the daemon to compute and optionally explain the next plan.
func (c *Client) Plan(ctx context.Context, explain bool) (PlanResult, error) {
	params := map[string]any{"explain": explain}
	var result PlanResult
	if err := c.do(ctx, control.Request{Action: control.ActionPlan, Params: params}, &result); err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

// Inspect retrieves the daemon's most recent world snapshot, mode information, and rule log.
func (c *Client) Inspect(ctx context.Context) (InspectorState, error) {
	return c.InspectorGet(ctx)
}

// InspectorGet retrieves the inspector payload via the control socket.
func (c *Client) InspectorGet(ctx context.Context) (InspectorState, error) {
	var snapshot InspectorState
	if err := c.do(ctx, control.Request{Action: control.ActionInspectorGet}, &snapshot); err != nil {
		return InspectorState{}, err
	}
	return snapshot, nil
}

// RulesStatus retrieves the daemon's per-rule counters and disablement state.
func (c *Client) RulesStatus(ctx context.Context) (RulesStatus, error) {
	var status RulesStatus
	if err := c.do(ctx, control.Request{Action: control.ActionRulesStatus}, &status); err != nil {
		return RulesStatus{}, err
	}
	return status, nil
}

// EnableRule clears throttle state and reenables the specified rule within a mode.
func (c *Client) EnableRule(ctx context.Context, mode, rule string) error {
	if mode == "" {
		return errors.New("mode cannot be empty")
	}
	if rule == "" {
		return errors.New("rule cannot be empty")
	}
	params := map[string]any{"mode": mode, "rule": rule}
	return c.do(ctx, control.Request{Action: control.ActionRuleEnable, Params: params}, nil)
}

func (c *Client) do(ctx context.Context, req control.Request, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial control socket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	var resp control.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Status != control.StatusOK {
		if resp.Error == "" {
			resp.Error = "unknown control error"
		}
		return errors.New(resp.Error)
	}
	if out == nil || resp.Data == nil {
		return nil
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}
