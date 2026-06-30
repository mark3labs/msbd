package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestPTYCmdDefault(t *testing.T) {
	cmd, args := ptyCmd(TerminalParams{})
	if cmd != "/bin/sh" || !slices.Equal(args, []string{"-c", defaultShellScript}) {
		t.Fatalf("default = %q %v, want /bin/sh [-c <defaultShellScript>]", cmd, args)
	}
	// The bootstrap must prefer the image's default shell but still guarantee
	// a /bin/sh fallback as the final exec.
	if !strings.Contains(defaultShellScript, `"$SHELL"`) {
		t.Errorf("defaultShellScript should consult $SHELL: %q", defaultShellScript)
	}
	if !strings.HasSuffix(defaultShellScript, "exec /bin/sh -i") {
		t.Errorf("defaultShellScript must end with the /bin/sh fallback: %q", defaultShellScript)
	}
}

func TestPTYCmdExplicit(t *testing.T) {
	cmd, args := ptyCmd(TerminalParams{Cmd: "  top  "})
	if cmd != "/bin/sh" || !slices.Equal(args, []string{"-c", "top"}) {
		t.Fatalf("explicit = %q %v, want /bin/sh [-c top] (trimmed)", cmd, args)
	}
}

func TestPTYEnvDefaultTerm(t *testing.T) {
	env := ptyEnv(TerminalParams{})
	if !slices.Contains(env, "TERM=xterm-256color") {
		t.Fatalf("env = %v, want TERM=xterm-256color", env)
	}
}

func TestPTYEnvCustomTermAndCallerEnv(t *testing.T) {
	env := ptyEnv(TerminalParams{Term: "screen-256color", Env: map[string]string{"FOO": "bar"}})
	if !slices.Contains(env, "TERM=screen-256color") {
		t.Fatalf("env = %v, want TERM=screen-256color", env)
	}
	if !slices.Contains(env, "FOO=bar") {
		t.Fatalf("env = %v, want FOO=bar", env)
	}
}

// The agent PTY wire format must round-trip through CBOR: encodeFrame wraps a
// payload in the Message envelope, decodeFrame must recover the type and the
// nested payload bytes.
func TestEncodeDecodeFrameRoundTrip(t *testing.T) {
	body, err := encodeFrame(mtExecResize, wireExecResize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("encodeFrame: %v", err)
	}
	typ, payload, err := decodeFrame(body)
	if err != nil {
		t.Fatalf("decodeFrame: %v", err)
	}
	if typ != mtExecResize {
		t.Fatalf("type = %q, want %q", typ, mtExecResize)
	}
	var rz wireExecResize
	if err := cbor.Unmarshal(payload, &rz); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if rz.Rows != 40 || rz.Cols != 120 {
		t.Fatalf("payload = %+v, want {40 120}", rz)
	}
}
