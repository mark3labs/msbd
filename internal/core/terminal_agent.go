package core

// terminal_agent.go — REAL kernel-PTY terminal sessions over the raw agent
// protocol.
//
// The Go SDK exposes no tty/rows/cols/resize on its exec path, and its SSH
// Attach is bound to a local controlling TTY — neither works in a daemon. But
// the SDK DOES expose the low-level agent transport (ConnectAgentSandbox +
// AgentClient.Stream/Send/Next), which is exactly what the SDK's own Attach is
// built on. This file replicates Attach's PTY mechanism, sourcing stdin from
// the caller (a WebSocket) instead of the process terminal:
//
//	stream(core.exec.request{tty:true,rows,cols}) -> opens a PTY session
//	recv core.exec.stdout / .stderr               -> terminal output
//	send core.exec.stdin{data}                    -> keystrokes
//	send core.exec.resize{rows,cols}              -> window resize (REAL)
//	send core.exec.signal{signal}                 -> Ctrl-C etc.
//	recv core.exec.exited{code}                   -> session end
//
// WIRE FORMAT (reverse-engineered from microsandbox protocol v5; NOT part of
// the Go SDK's public API — see crates/protocol/lib in upstream):
//
//	frame body = CBOR(Message{ v:5, t:"<wire string>", p:<CBOR(payload)> })
//	             id + flags travel in the binary frame header, passed
//	             separately to Stream/Send, NOT inside the CBOR.
//
// This couples msbd to an undocumented schema; if microsandbox bumps the
// protocol, this backend breaks while the pipe backend keeps working. Treat it
// as the opt-in, higher-fidelity path.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/fxamacker/cbor/v2"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Protocol constants, verified against crates/protocol/lib/message.rs.
//
// These mirror the WIRE FORMAT of the agent protocol that the SDK's embedded
// FFI speaks. That FFI is byte-pinned by go.sum and the downloaded msb runtime
// is version-checked against the SDK version (see sdk/go/setup.go), so this
// format CANNOT drift at runtime — it is fixed for a given SDK version. The
// only way it changes is a deliberate SDK bump in go.mod, which trips the
// guard in TestPinnedSDKVersion (terminal_agent_test.go): re-verify these
// constants against the new microsandbox protocol crate when that test fails.
const (
	protocolVersion uint8 = 5

	flagTerminal     uint8 = 0b0000_0001 // last frame for a correlation id
	flagSessionStart uint8 = 0b0000_0010 // first frame of a session

	mtExecRequest = "core.exec.request"
	mtExecStarted = "core.exec.started"
	mtExecStdin   = "core.exec.stdin"
	mtExecStdout  = "core.exec.stdout"
	mtExecStderr  = "core.exec.stderr"
	mtExecExited  = "core.exec.exited"
	mtExecFailed  = "core.exec.failed"
	mtExecResize  = "core.exec.resize"
	mtExecSignal  = "core.exec.signal"
)

// wireMessage mirrors protocol::message::Message. id/flags are skipped (frame
// header), matching the Rust #[serde(skip)].
type wireMessage struct {
	V uint8  `cbor:"v"`
	T string `cbor:"t"`
	P []byte `cbor:"p"`
}

// wireExecRequest mirrors protocol::exec::ExecRequest. Optional fields use
// omitempty so the guest's serde(default) fills them; cmd is required.
type wireExecRequest struct {
	Cmd  string   `cbor:"cmd"`
	Args []string `cbor:"args,omitempty"`
	Env  []string `cbor:"env,omitempty"`
	Cwd  string   `cbor:"cwd,omitempty"`
	User string   `cbor:"user,omitempty"`
	TTY  bool     `cbor:"tty"`
	Rows uint16   `cbor:"rows"`
	Cols uint16   `cbor:"cols"`
}

type wireExecData struct {
	Data []byte `cbor:"data"` // serde_bytes -> CBOR byte string
}

type wireExecResize struct {
	Rows uint16 `cbor:"rows"`
	Cols uint16 `cbor:"cols"`
}

type wireExecSignal struct {
	Signal int32 `cbor:"signal"`
}

type wireExecExited struct {
	Code int32 `cbor:"code"`
}

// encodeFrame builds a frame body: CBOR(Message{v, t, CBOR(payload)}).
func encodeFrame(t string, payload any) ([]byte, error) {
	p, err := cbor.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", t, err)
	}
	body, err := cbor.Marshal(wireMessage{V: protocolVersion, T: t, P: p})
	if err != nil {
		return nil, fmt.Errorf("marshal %s envelope: %w", t, err)
	}
	return body, nil
}

// decodeFrame parses a frame body back into its message type and raw payload.
func decodeFrame(body []byte) (string, []byte, error) {
	var m wireMessage
	if err := cbor.Unmarshal(body, &m); err != nil {
		return "", nil, err
	}
	return m.T, m.P, nil
}

// agentSession is the kernel-PTY Session implementation.
type agentSession struct {
	client *msb.AgentClient
	stream *msb.AgentStream
	id     uint32
	out    chan []byte

	mu       sync.Mutex
	closed   bool
	exitCode int
	done     chan struct{}
	quit     chan struct{}
}

// openAgentSession opens a real PTY in sandbox name. The sandbox must already
// be running (callers go through resolve first).
func openAgentSession(ctx context.Context, name string, p TerminalParams) (Session, error) {
	cmd, args := ptyCmd(p)
	rows, cols := p.Rows, p.Cols
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}

	req := wireExecRequest{
		Cmd:  cmd,
		Args: args,
		Env:  ptyEnv(p),
		Cwd:  strings.TrimSpace(p.Cwd),
		TTY:  true,
		Rows: rows,
		Cols: cols,
	}
	body, err := encodeFrame(mtExecRequest, req)
	if err != nil {
		return nil, err
	}

	client, err := msb.ConnectAgentSandbox(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("connect agent: %w", err)
	}
	stream, err := client.Stream(ctx, flagSessionStart, body)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("open pty stream: %w", err)
	}

	se := &agentSession{
		client:   client,
		stream:   stream,
		id:       stream.ID(),
		out:      make(chan []byte, 64),
		exitCode: -1,
		done:     make(chan struct{}),
		quit:     make(chan struct{}),
	}
	go se.pump()
	return se, nil
}

// ptyCmd returns the program + args for the PTY's foreground process. An empty
// Cmd launches an interactive login shell; a non-empty Cmd runs via sh -c.
func ptyCmd(p TerminalParams) (string, []string) {
	if cmd := strings.TrimSpace(p.Cmd); cmd != "" {
		return "/bin/sh", []string{"-c", cmd}
	}
	return "/bin/sh", []string{"-i"}
}

// ptyEnv builds the env var list (KEY=VALUE) for the session. With a real PTY,
// rows/cols are carried in the request, so only TERM (plus caller env) matters.
func ptyEnv(p TerminalParams) []string {
	term := strings.TrimSpace(p.Term)
	if term == "" {
		term = "xterm-256color"
	}
	env := make([]string, 0, len(p.Env)+1)
	env = append(env, "TERM="+term)
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// pump reads PTY output frames and merges stdout/stderr into the output
// channel, recording the exit code. Detached context: outlives the HTTP request.
func (se *agentSession) pump() {
	defer close(se.out)
	defer close(se.done)
	defer func() {
		_ = se.stream.Close(context.Background())
		_ = se.client.Close()
	}()

	ctx := context.Background()
	for {
		frame, err := se.stream.Next(ctx)
		if err != nil || frame == nil { // err or EOF
			return
		}
		t, payload, derr := decodeFrame(frame.Body)
		if derr != nil {
			continue // skip undecodable frames rather than tearing down
		}
		switch t {
		case mtExecStdout, mtExecStderr:
			var d wireExecData
			if cbor.Unmarshal(payload, &d) == nil && len(d.Data) > 0 {
				b := make([]byte, len(d.Data))
				copy(b, d.Data)
				select {
				case se.out <- b:
				case <-se.quit:
					return
				}
			}
		case mtExecExited:
			var e wireExecExited
			if cbor.Unmarshal(payload, &e) == nil {
				se.setExit(int(e.Code))
			}
			return
		case mtExecFailed:
			se.setExit(-1)
			return
		}
	}
}

func (se *agentSession) Output() <-chan []byte { return se.out }

func (se *agentSession) Write(p []byte) error {
	se.mu.Lock()
	closed := se.closed
	se.mu.Unlock()
	if closed {
		return fmt.Errorf("terminal closed")
	}
	body, err := encodeFrame(mtExecStdin, wireExecData{Data: p})
	if err != nil {
		return err
	}
	if err := se.client.Send(context.Background(), se.id, 0, body); err != nil {
		return fmt.Errorf("write pty stdin: %w", err)
	}
	return nil
}

// Resize sends a real PTY window-size change to the guest.
func (se *agentSession) Resize(rows, cols uint16) error {
	se.mu.Lock()
	closed := se.closed
	se.mu.Unlock()
	if closed {
		return fmt.Errorf("terminal closed")
	}
	body, err := encodeFrame(mtExecResize, wireExecResize{Rows: rows, Cols: cols})
	if err != nil {
		return err
	}
	return se.client.Send(context.Background(), se.id, 0, body)
}

func (se *agentSession) Signal(ctx context.Context, sig int) error {
	se.mu.Lock()
	closed := se.closed
	se.mu.Unlock()
	if closed {
		return fmt.Errorf("terminal closed")
	}
	if sig <= 0 {
		sig = 9 // SIGKILL
	}
	body, err := encodeFrame(mtExecSignal, wireExecSignal{Signal: int32(sig)})
	if err != nil {
		return err
	}
	return se.client.Send(ctx, se.id, 0, body)
}

func (se *agentSession) Close() error {
	se.mu.Lock()
	if se.closed {
		se.mu.Unlock()
		return nil
	}
	se.closed = true
	se.mu.Unlock()

	close(se.quit)
	// Best-effort SIGHUP so the guest shell tears down; pump() then observes
	// the stream end and releases the client/stream handles.
	if body, err := encodeFrame(mtExecSignal, wireExecSignal{Signal: 1}); err == nil {
		_ = se.client.Send(context.Background(), se.id, 0, body)
	}
	return nil
}

func (se *agentSession) Wait() int {
	<-se.done
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.exitCode
}

func (se *agentSession) setExit(code int) {
	se.mu.Lock()
	se.exitCode = code
	se.mu.Unlock()
}
