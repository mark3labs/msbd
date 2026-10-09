package core

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// verifiedSDKVersion is the microsandbox SDK release whose agent wire format
// the constants and CBOR struct mirrors in terminal_agent.go were verified
// against. The SDK pins the embedded FFI, but EnsureRuntime in 0.7.x reuses
// existing host runtime pairs without a version check. Provision a matching
// runtime explicitly when validating a deliberate SDK upgrade.
//
// When this test fails after bumping the SDK: re-verify the protocolVersion,
// message-type strings, and wire* struct fields in terminal_agent.go against
// the new microsandbox protocol crate (crates/protocol/lib), then update this
// constant to match. The terminal is the only feature riding the raw agent
// protocol; nothing else in msbd depends on this wire format.
//
// 0.6.1 → 0.6.7 verification (protocol generation 5 → 6):
//   - crates/protocol/lib/exec.rs is byte-identical between the two tags, so
//     every wire* payload struct in terminal_agent.go is still correct.
//   - message.rs only appends four generation-6 core message types
//     (core.ping/pong/touch/touched), which msbd does not use; all exec message
//     strings and frame flags are unchanged.
//   - sdk/go/agent.go (the transport this backend rides) is byte-identical.
//   - Confirmed end-to-end against a live microVM: connect, stdin/stdout round
//     trip, real PTY device in the guest, core.exec.resize changing `stty size`,
//     Ctrl-C interrupting a command, and a clean core.exec.exited with code 0.
//
// 0.6.7 → 0.6.16 verification (protocol generation 6 → 7):
//   - crates/protocol/lib/exec.rs is byte-identical between the two tags, so
//     every wire* payload struct in terminal_agent.go is still correct.
//   - message.rs only appends ONE generation-7 core message type
//     (core.bootstrap, flags=0, host→guest one-shot boot config), which msbd
//     does not use. Every exec message string, its frame flag, and its
//     min_protocol_version (baseline gen 1) is unchanged, so declaring v=7 in
//     the ExecRequest we send is equivalent to declaring v=6 for peer-gating
//     purposes: exec is available at every generation.
//   - sdk/go/agent.go (the transport this backend rides) is byte-identical.
//
// 0.6.16 → 0.7.7 verification (protocol generation 7 → 9):
//   - Compared tagged v0.6.16 and v0.7.7 source from
//     https://github.com/superradcompany/microsandbox/tree/v0.7.7:
//     crates/protocol/lib/exec.rs and sdk/go/agent.go are byte-identical.
//   - message.rs preserves the envelope (v/t/p, serde_bytes p, skipped id/flags),
//     all exec wire strings, flags and minimum generation. Generation 8 bulk
//     is opt-in for FS/TCP (bulk.rs); Rust sandbox/attach.rs still uses CBOR
//     ExecRequest and exec data/resize/signal, with no bulk offer. Generation 9
//     adds workload/transport-credit and root-disk controls.
//   - Verified 2026-10-09 on /dev/kvm using an isolated MSB_HOME provisioned
//     via InstallRuntime (msb --version = 0.7.7, libkrunfw.so.5.6.1):
//     TestTerminalLive passed with public.ecr.aws/docker/library/alpine:3.21.
//     Confirmed /dev/pts/0, initial 24x80, stdin/output round trip, resize to
//     37x101, Ctrl-C interrupting sleep 30, and core.exec.exited code 7.
const verifiedSDKVersion = "0.7.7"

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
