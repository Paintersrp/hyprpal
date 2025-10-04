package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/util"
)

// Server hosts the hyprpal control socket and serves requests.
type Server struct {
	engine     *engine.Engine
	logger     *util.Logger
	reload     func(reason string) error
	socketPath string

	mu       sync.Mutex
	listener net.Listener
}

// NewServer creates a new control server.
func NewServer(eng *engine.Engine, logger *util.Logger, reload func(reason string) error) (*Server, error) {
	path, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	return &Server{
		engine:     eng,
		logger:     logger,
		reload:     reload,
		socketPath: path,
	}, nil
}

// Serve listens on the control socket until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.prepareSocket(); err != nil {
		return err
	}
	s.logger.Infof("control server listening on %s", s.socketPath)
	defer s.cleanup()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if s.listener != nil {
			s.listener.Close()
		}
		s.mu.Unlock()
	}()

	for {
		conn, err := s.accept(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Errorf("control accept error: %v", err)
			continue
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) accept(ctx context.Context) (net.Conn, error) {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener == nil {
		return nil, context.Canceled
	}
	conn, err := listener.Accept()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return conn, nil
}

func (s *Server) prepareSocket() error {
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create control dir: %w", err)
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		listener.Close()
		return fmt.Errorf("chmod control socket: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	return nil
}

func (s *Server) cleanup() {
	s.mu.Lock()
	listener := s.listener
	s.listener = nil
	s.mu.Unlock()
	if listener != nil {
		listener.Close()
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warnf("remove control socket: %v", err)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	var req Request
	if err := dec.Decode(&req); err != nil {
		s.writeError(conn, fmt.Errorf("decode request: %w", err))
		return
	}
	switch req.Action {
	case ActionModeGet:
		s.handleModeGet(conn)
	case ActionModeSet:
		s.handleModeSet(conn, req.Params)
	case ActionReload:
		s.handleReload(conn)
	case ActionPlan:
		s.handlePlan(ctx, conn, req.Params)
	default:
		s.writeError(conn, fmt.Errorf("unknown action %q", req.Action))
	}
}

func (s *Server) handleModeGet(conn net.Conn) {
	status := ModeStatus{
		Active:    s.engine.ActiveMode(),
		Available: s.engine.AvailableModes(),
	}
	s.writeOK(conn, status)
}

func (s *Server) handleModeSet(conn net.Conn, params map[string]any) {
	name, _ := params["name"].(string)
	if name == "" {
		s.writeError(conn, errors.New("missing mode name"))
		return
	}
	if err := s.engine.SetMode(name); err != nil {
		s.writeError(conn, err)
		return
	}
	s.writeOK(conn, nil)
}

func (s *Server) handleReload(conn net.Conn) {
	if s.reload == nil {
		s.writeError(conn, errors.New("reload not supported"))
		return
	}
	if err := s.reload("control request"); err != nil {
		s.writeError(conn, err)
		return
	}
	s.writeOK(conn, nil)
}

func (s *Server) handlePlan(ctx context.Context, conn net.Conn, params map[string]any) {
	explain, _ := params["explain"].(bool)
	commands, err := s.engine.PreviewPlan(ctx, explain)
	if err != nil {
		s.writeError(conn, err)
		return
	}
	result := PlanResult{Commands: make([]PlanCommand, 0, len(commands))}
	for _, cmd := range commands {
		result.Commands = append(result.Commands, PlanCommand{
			Dispatch: cmd.Dispatch,
			Reason:   cmd.Reason,
		})
	}
	s.writeOK(conn, result)
}

func (s *Server) writeOK(conn net.Conn, data any) {
	resp := Response{Status: StatusOK}
	if data != nil {
		resp.Data = data
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func (s *Server) writeError(conn net.Conn, err error) {
	resp := Response{Status: StatusError}
	if err != nil {
		resp.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(resp)
}
