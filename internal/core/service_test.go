package core

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// applyOptions folds a slice of SandboxOption into a fresh SandboxConfig so
// tests can assert what buildCreateOptions actually wires through to the SDK.
func applyOptions(opts []msb.SandboxOption) msb.SandboxConfig {
	var cfg msb.SandboxConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// TestBuildCreateOptionsDiskGB is the regression test for issue #1:
// resources.disk_gb must be honored and converted from GiB to MiB before being
// handed to the SDK's WithOCIUpperSize option.
func TestBuildCreateOptionsDiskGB(t *testing.T) {
	cfg := applyOptions(buildCreateOptions(CreateParams{DiskGB: 32}, "microsandbox/python"))
	if got, want := cfg.OCIUpperSizeMiB, uint32(32*1024); got != want {
		t.Fatalf("OCIUpperSizeMiB = %d, want %d (32 GiB -> MiB)", got, want)
	}
}

// TestBuildCreateOptionsNoDiskGB confirms that omitting disk_gb leaves the
// overlay size at the SDK/image default (zero, i.e. unset) rather than forcing
// a value.
func TestBuildCreateOptionsNoDiskGB(t *testing.T) {
	cfg := applyOptions(buildCreateOptions(CreateParams{}, "microsandbox/python"))
	if cfg.OCIUpperSizeMiB != 0 {
		t.Fatalf("OCIUpperSizeMiB = %d, want 0 when disk_gb is unset", cfg.OCIUpperSizeMiB)
	}
}

// TestBuildCreateOptionsResources covers the CPU/memory mapping alongside disk
// so the conversion test doesn't silently regress the neighbouring knobs.
func TestBuildCreateOptionsResources(t *testing.T) {
	cfg := applyOptions(buildCreateOptions(CreateParams{CPU: 2, MemoryMB: 1024, DiskGB: 8}, "img"))
	if cfg.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2", cfg.CPUs)
	}
	if cfg.MemoryMiB != 1024 {
		t.Errorf("MemoryMiB = %d, want 1024", cfg.MemoryMiB)
	}
	if cfg.OCIUpperSizeMiB != 8*1024 {
		t.Errorf("OCIUpperSizeMiB = %d, want %d", cfg.OCIUpperSizeMiB, 8*1024)
	}
	if cfg.Image != "img" {
		t.Errorf("Image = %q, want %q", cfg.Image, "img")
	}
}
