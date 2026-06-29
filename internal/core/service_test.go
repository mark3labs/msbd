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
	opts, err := buildCreateOptions(CreateParams{DiskGB: 32}, "microsandbox/python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := applyOptions(opts)
	if got, want := cfg.OCIUpperSizeMiB, uint32(32*1024); got != want {
		t.Fatalf("OCIUpperSizeMiB = %d, want %d (32 GiB -> MiB)", got, want)
	}
}

// TestBuildCreateOptionsNoDiskGB confirms that omitting disk_gb leaves the
// overlay size at the SDK/image default (zero, i.e. unset) rather than forcing
// a value.
func TestBuildCreateOptionsNoDiskGB(t *testing.T) {
	opts, err := buildCreateOptions(CreateParams{}, "microsandbox/python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := applyOptions(opts)
	if cfg.OCIUpperSizeMiB != 0 {
		t.Fatalf("OCIUpperSizeMiB = %d, want 0 when disk_gb is unset", cfg.OCIUpperSizeMiB)
	}
}

// TestBuildCreateOptionsDiskGBInvalid rejects negative and out-of-range disk_gb
// instead of silently skipping or wrapping the uint32 MiB conversion.
func TestBuildCreateOptionsDiskGBInvalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		diskGB int
	}{
		{"negative", -1},
		{"overflow", maxDiskGB + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildCreateOptions(CreateParams{DiskGB: tc.diskGB}, "img"); err == nil {
				t.Fatalf("expected error for disk_gb=%d, got nil", tc.diskGB)
			}
		})
	}
}

// TestBuildCreateOptionsDiskGBMax accepts the largest in-range disk_gb and
// converts it without overflowing uint32.
func TestBuildCreateOptionsDiskGBMax(t *testing.T) {
	opts, err := buildCreateOptions(CreateParams{DiskGB: maxDiskGB}, "img")
	if err != nil {
		t.Fatalf("unexpected error at max disk_gb: %v", err)
	}
	cfg := applyOptions(opts)
	if got, want := cfg.OCIUpperSizeMiB, uint32(maxDiskGB)*1024; got != want {
		t.Fatalf("OCIUpperSizeMiB = %d, want %d", got, want)
	}
}

// TestBuildCreateOptionsResources covers the CPU/memory mapping alongside disk
// so the conversion test doesn't silently regress the neighbouring knobs.
func TestBuildCreateOptionsResources(t *testing.T) {
	opts, err := buildCreateOptions(CreateParams{CPU: 2, MemoryMB: 1024, DiskGB: 8}, "img")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := applyOptions(opts)
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
