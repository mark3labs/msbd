package core

import (
	"context"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// EnsureRuntime resolves the host runtime, installing the SDK-pinned release
// only when wholly absent. Complete existing pairs are reused without a version
// check; partial installations and invalid explicit paths fail closed.
func EnsureRuntime(ctx context.Context) error {
	_, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{})
	return err
}

// RuntimeVersion reports the loaded FFI version, NOT the resolved msb binary's
// version. SDK 0.7.x may reuse an older host runtime independently of the FFI.
func RuntimeVersion() (string, error) { return msb.RuntimeVersion() }

// SDKVersion reports the SDK version the server is built against.
func SDKVersion() string { return msb.SDKVersion() }
