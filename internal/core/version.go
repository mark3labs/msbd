package core

import msb "github.com/superradcompany/microsandbox/sdk/go"

// RuntimeVersion reports the msb runtime version (for diagnostics).
func RuntimeVersion() (string, error) { return msb.RuntimeVersion() }

// SDKVersion reports the SDK version the server is built against.
func SDKVersion() string { return msb.SDKVersion() }
