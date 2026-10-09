package core

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Assert the tagged Rust Message envelope and serde_bytes payload shape rather
// than only round-tripping through our own structs (which could share a bug).
func TestTerminalWireEnvelope(t *testing.T) {
	body, err := encodeFrame(mtExecRequest, wireExecRequest{
		Cmd: "/bin/sh", TTY: true, Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := cbor.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 3 || envelope["v"] != uint64(9) || envelope["t"] != "core.exec.request" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	payload, ok := envelope["p"].([]byte)
	if !ok {
		t.Fatalf("payload must be a CBOR byte string: %#v", envelope["p"])
	}
	var request map[string]any
	if err := cbor.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	if request["cmd"] != "/bin/sh" || request["tty"] != true || request["rows"] != uint64(24) || request["cols"] != uint64(80) {
		t.Fatalf("unexpected exec request: %#v", request)
	}
	for _, key := range []string{"args", "env", "cwd", "user", "rlimits"} {
		if _, exists := request[key]; exists {
			t.Fatalf("optional field %q should use the Rust serde default", key)
		}
	}
	if flagSessionStart != msb.FlagSessionStart || flagTerminal != msb.FlagTerminal {
		t.Fatal("frame flags disagree with SDK transport")
	}
}

func TestTerminalWireData(t *testing.T) {
	input := []byte{0, 3, 255, '\n'}
	body, err := encodeFrame(mtExecStdin, wireExecData{Data: input})
	if err != nil {
		t.Fatal(err)
	}
	kind, payload, err := decodeFrame(body)
	if err != nil || kind != "core.exec.stdin" {
		t.Fatalf("decode: kind=%q err=%v", kind, err)
	}
	var data map[string][]byte
	if err := cbor.Unmarshal(payload, &data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data["data"], input) {
		t.Fatalf("stdin bytes = %v, want %v", data["data"], input)
	}
}
