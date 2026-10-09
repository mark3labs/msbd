//go:build integration

package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTerminalLive requires an explicitly provisioned SDK-matching msb/libkrunfw
// pair in MSB_HOME, /dev/kvm, and registry access (or a cached image). Setup is
// intentionally separate: EnsureRuntime does not upgrade an existing runtime.
func TestTerminalLive(t *testing.T) {
	if os.Getenv("MSB_HOME") == "" {
		t.Fatal("set MSB_HOME to an isolated home provisioned with the SDK-matching runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := EnsureRuntime(ctx); err != nil {
		t.Fatalf("runtime setup: %v", err)
	}
	svc := NewService(Opts{})
	defer svc.Close()
	image := os.Getenv("MSBD_TEST_IMAGE")
	if image == "" {
		image = "alpine:3.21"
	}
	sb, err := svc.Create(ctx, CreateParams{Image: image, CPU: 1, MemoryMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := svc.Delete(cleanupCtx, sb.ID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	se, err := svc.OpenTerminal(ctx, sb.ID, TerminalParams{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = se.Close() }()
	var output strings.Builder
	until := func(marker string) {
		t.Helper()
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for !strings.Contains(output.String(), marker) {
			select {
			case b, ok := <-se.Output():
				if !ok {
					t.Fatalf("terminal closed before %q; output: %q", marker, output.String())
				}
				output.Write(b)
			case <-timer.C:
				t.Fatalf("timeout waiting for %q; output: %q", marker, output.String())
			}
		}
	}
	write := func(command string) {
		t.Helper()
		if err := se.Write([]byte(command)); err != nil {
			t.Fatal(err)
		}
	}
	// Disable echo so markers prove guest execution rather than echoed input.
	write("stty -echo; tty; stty size; printf 'READY_%s\\n' PTY\n")
	until("READY_PTY")
	if !strings.Contains(output.String(), "/dev/pts/") || !strings.Contains(output.String(), "24 80") {
		t.Fatalf("not a sized kernel PTY: %q", output.String())
	}
	if err := se.Resize(37, 101); err != nil {
		t.Fatal(err)
	}
	write("stty size; printf 'RESIZED_%s\\n' PTY\n")
	until("RESIZED_PTY")
	if !strings.Contains(output.String(), "37 101") {
		t.Fatalf("resize not applied: %q", output.String())
	}
	write("printf 'INPUT_%s\\n' ROUNDTRIP\n")
	until("INPUT_ROUNDTRIP")
	write("sleep 30\n")
	// Give the shell time to start its foreground job before sending Ctrl-C.
	time.Sleep(time.Second)
	write("\x03")
	write("printf 'INTERRUPTED_%s\\n' PTY\n")
	until("INTERRUPTED_PTY")
	write("exit 7\n")
	wait := make(chan int, 1)
	go func() { wait <- se.Wait() }()
	select {
	case code := <-wait:
		if code != 7 {
			t.Fatalf("exit code = %d, want 7", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for core.exec.exited")
	}
	t.Logf("PTY output: %q", output.String())
}
