package core

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Handles without a local path must remain safe to map. SDK 0.7.x's deprecated
// Path accessor panics in this case; Reference is backend-neutral and safe.
func TestSnapshotFromHandleWithoutLocalPath(t *testing.T) {
	snapshot := snapshotFromHandle(&msb.SnapshotHandle{})
	if snapshot.Path != "" {
		t.Fatalf("reference = %q, want empty", snapshot.Path)
	}
}
