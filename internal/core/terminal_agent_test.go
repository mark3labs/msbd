package core

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// verifiedSDKVersion is the microsandbox SDK release whose agent wire format
// the constants and CBOR struct mirrors in terminal_agent.go were verified
// against. The SDK pins the embedded FFI and the downloaded msb runtime to this
// exact version, so the protocol cannot drift at runtime — but a deliberate SDK
// bump can change the wire format.
//
// When this test fails after bumping the SDK: re-verify the protocolVersion,
// message-type strings, and wire* struct fields in terminal_agent.go against
// the new microsandbox protocol crate (crates/protocol/lib), then update this
// constant to match. The terminal is the only feature riding the raw agent
// protocol; nothing else in msbd depends on this wire format.
const verifiedSDKVersion = "0.6.1"

func TestPinnedSDKVersion(t *testing.T) {
	if got := msb.SDKVersion(); got != verifiedSDKVersion {
		t.Fatalf(`microsandbox SDK is %q but the agent terminal wire format was `+
			`verified against %q.

The interactive terminal (internal/core/terminal_agent.go) hand-encodes the
microsandbox agent protocol, whose schema is pinned to the SDK version. Re-verify
the protocol constants and wire* structs against the new SDK's protocol crate,
confirm the terminal still works end-to-end, then bump verifiedSDKVersion.`,
			got, verifiedSDKVersion)
	}
}
