package core

// terminal.go — interactive terminal sessions.
//
// A Session bridges a client (typically a WebSocket) to an interactive shell
// running on a REAL kernel PTY inside the guest. The backend (see
// terminal_agent.go) drives the microsandbox agent protocol directly, giving
// colors, line editing, working window resize, and job control.
//
// Sessions are in-memory only: after an msbd restart a previously-opened
// session is gone and its WebSocket simply closes (the JobGone analogue).

import (
	"context"
)

// TerminalParams configures an interactive terminal session.
type TerminalParams struct {
	// Cmd is the command line to run. Empty launches an interactive login
	// shell (`/bin/sh -i`); a non-empty value runs via `/bin/sh -c <cmd>`.
	Cmd string
	Cwd string
	Env map[string]string
	// Rows/Cols are the initial PTY dimensions, carried in the exec request
	// and honored by the guest kernel. Default 24x80 when zero.
	Rows uint16
	Cols uint16
	Term string // $TERM value; defaults to "xterm-256color"
}

// Session is one live interactive terminal. It is safe for the output reader
// and the input writer to run on separate goroutines; Write/Resize/Signal are
// individually safe for concurrent use.
//
// The api package depends only on this interface — no microsandbox SDK type
// crosses the boundary, preserving the cgo isolation seam.
type Session interface {
	// Output yields merged stdout+stderr bytes from the guest PTY. It is
	// closed when the session ends (process exit, error, or Close).
	Output() <-chan []byte
	// Write sends bytes to the guest's stdin.
	Write(p []byte) error
	// Resize updates the PTY window dimensions.
	Resize(rows, cols uint16) error
	// Signal sends a Unix signal to the guest process (sig <= 0 means kill).
	Signal(ctx context.Context, sig int) error
	// Close tears the session down, killing the guest process and releasing
	// the agent handle. Safe to call more than once.
	Close() error
	// Wait blocks until the process exits, returning its exit code. Returns
	// the recorded code immediately if the session already ended.
	Wait() int
}

// OpenTerminal starts an interactive PTY terminal in sandbox id. It resolves
// the sandbox through the registry (transparent resume + reconnect) first,
// exactly like Run, so a paused box is booted before we connect to its agent
// relay by name.
func (s *Service) OpenTerminal(ctx context.Context, id string, p TerminalParams) (Session, error) {
	if _, err := s.reg.resolve(ctx, id); err != nil {
		return nil, err
	}
	return openAgentSession(ctx, id, p)
}
