package api

// terminal.go — interactive terminal over WebSocket.
//
// GET /v1/sandboxes/{id}/terminal upgrades to a WebSocket that bridges a
// browser/CLI terminal emulator to a shell running inside the guest. The wire
// protocol is deliberately small:
//
//   client → server
//     - BINARY frame  : raw stdin bytes (keystrokes)
//     - TEXT frame     : JSON control message, one of
//         {"type":"resize","rows":40,"cols":120}
//         {"type":"signal","signal":2}             // 2 = SIGINT (Ctrl-C)
//
//   server → client
//     - BINARY frame  : raw stdout/stderr bytes (terminal output)
//     - TEXT frame     : JSON event, currently only
//         {"type":"exit","exit_code":0}            // sent just before close
//
// Auth: browsers cannot set Authorization on a WebSocket upgrade, so the bearer
// token may be supplied as ?key=<token> in addition to the standard
// Authorization header. The key is stripped from logs by the request logger
// only seeing the path, not the query — see logMW.
//
// The terminal runs on a REAL kernel PTY in the guest (see
// core/terminal_agent.go): colors, line editing, window resize, and job
// control all work, including full-screen TUIs like vim and top.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"

	"github.com/mark3labs/msbd/internal/core"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same-origin is not meaningful for an API server driven by SDKs/CLIs;
	// auth is enforced via bearer token, so accept any Origin.
	CheckOrigin: func(*http.Request) bool { return true },
}

// terminalControl is a client → server TEXT control message.
type terminalControl struct {
	Type   string `json:"type"`
	Rows   uint16 `json:"rows"`
	Cols   uint16 `json:"cols"`
	Signal int    `json:"signal"`
}

// terminalEvent is a server → client TEXT event message.
type terminalEvent struct {
	Type     string `json:"type"`
	ExitCode int    `json:"exit_code"`
}

const (
	wsWriteTimeout = 10 * time.Second
	wsPongTimeout  = 60 * time.Second
	wsPingInterval = 25 * time.Second
)

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rows := atoiU16(r.URL.Query().Get("rows"))
	cols := atoiU16(r.URL.Query().Get("cols"))
	p := core.TerminalParams{
		Cmd:  r.URL.Query().Get("cmd"),
		Cwd:  r.URL.Query().Get("cwd"),
		Rows: rows,
		Cols: cols,
		Term: r.URL.Query().Get("term"),
	}

	// Open the session BEFORE upgrading so failures surface as clean HTTP
	// status codes (404 for an unknown sandbox) instead of a WebSocket that
	// connects and immediately closes.
	sess, err := s.svc.OpenTerminal(r.Context(), id, p)
	if err != nil {
		notFoundOr(w, err)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		_ = sess.Close()
		return
	}
	log.Info("terminal opened", "sandbox", id)
	defer func() {
		_ = sess.Close()
		_ = conn.Close()
		log.Info("terminal closed", "sandbox", id)
	}()

	bridgeTerminal(conn, sess)
}

// bridgeTerminal splices the WebSocket and the core.Session until either side
// ends. All writes to conn happen on this goroutine (gorilla requires a single
// concurrent writer); the reader runs on its own goroutine and signals through
// readErr/ctx. The guest PTY produces canonical CRLF, so output bytes pass
// through verbatim.
func bridgeTerminal(conn *websocket.Conn, sess core.Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Keepalive: respond to pongs to keep idle terminals alive through proxies.
	_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})

	// Reader goroutine: client → guest. Closes ctx when the socket ends.
	go func() {
		defer cancel()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if err := sess.Write(data); err != nil {
					return
				}
			case websocket.TextMessage:
				handleControl(ctx, sess, data)
			}
		}
	}()

	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	out := sess.Output()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case b, ok := <-out:
			if !ok {
				// Guest process ended — report the exit code, then close.
				writeExit(conn, sess.Wait())
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				return
			}
		}
	}
}

func handleControl(ctx context.Context, sess core.Session, data []byte) {
	var c terminalControl
	if err := json.Unmarshal(data, &c); err != nil {
		return
	}
	switch c.Type {
	case "resize":
		_ = sess.Resize(c.Rows, c.Cols)
	case "signal":
		_ = sess.Signal(ctx, c.Signal)
	}
}

func writeExit(conn *websocket.Conn, code int) {
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if b, err := json.Marshal(terminalEvent{Type: "exit", ExitCode: code}); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, b)
	}
	msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "process exited")
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_ = conn.WriteMessage(websocket.CloseMessage, msg)
}

func atoiU16(s string) uint16 {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 0xffff {
		return 0
	}
	return uint16(n)
}
