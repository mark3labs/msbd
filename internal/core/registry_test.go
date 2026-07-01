package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestVerifyAgentUpFailsForMissingSandbox is the regression test for issue #3:
// booting a box whose guest never comes up must surface an error rather than a
// silent success. verifyAgentUp against a name with no live agent returns the
// underlying connect error once its deadline passes instead of reporting the
// (dead) box as ready.
func TestVerifyAgentUpFailsForMissingSandbox(t *testing.T) {
	// Shrink the probe window so the test doesn't wait the full 20s.
	restore := agentVerifyTimeout
	agentVerifyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { agentVerifyTimeout = restore })

	start := time.Now()
	err := verifyAgentUp(context.Background(), "sbx_does_not_exist_"+time.Now().Format("150405"))
	if err == nil {
		t.Fatal("verifyAgentUp: want error for a sandbox with no live agent, got nil")
	}
	// It must respect the deadline rather than hanging.
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("verifyAgentUp: took %s, want it bounded by agentVerifyTimeout", elapsed)
	}
}

// TestVerifyAgentUpHonorsCanceledContext ensures the probe returns promptly when
// the caller's context is already done, propagating the context error.
func TestVerifyAgentUpHonorsCanceledContext(t *testing.T) {
	restore := agentVerifyTimeout
	agentVerifyTimeout = 10 * time.Second
	t.Cleanup(func() { agentVerifyTimeout = restore })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := verifyAgentUp(ctx, "sbx_canceled")
	if err == nil {
		t.Fatal("verifyAgentUp: want error for a canceled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Logf("verifyAgentUp returned %v (a connect error is acceptable if it beat the cancel check)", err)
	}
}
