package core

import (
	"context"
	"testing"
	"time"
)

// TestPullImageRejectsEmptyReference verifies the SDK-free validation guard:
// an empty (or whitespace-only) reference is rejected before any microVM boots,
// so this test needs no /dev/kvm.
func TestPullImageRejectsEmptyReference(t *testing.T) {
	svc := NewService(Opts{})
	for _, ref := range []string{"", "   ", "\t"} {
		if _, err := svc.PullImage(context.Background(), ref, false); err == nil {
			t.Fatalf("PullImage(%q) = nil error, want validation error", ref)
		}
	}
}

// TestPullTimeoutDefault verifies the standalone pull budget defaults larger
// than the create budget so cold pulls of big images don't get cut short.
func TestPullTimeoutDefault(t *testing.T) {
	svc := NewService(Opts{})
	if svc.pullTO <= svc.createTO {
		t.Fatalf("pullTO=%v should exceed createTO=%v", svc.pullTO, svc.createTO)
	}
	if svc.pullTO != 15*time.Minute {
		t.Fatalf("pullTO=%v, want 15m default", svc.pullTO)
	}
}
