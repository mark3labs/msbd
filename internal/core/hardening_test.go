package core

import (
	"strings"
	"testing"
	"time"
)

func TestRingBufferCapsAndTracksTotal(t *testing.T) {
	r := newRing(8)
	if _, err := r.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if r.trunc {
		t.Fatal("should not be truncated yet")
	}
	if _, err := r.Write([]byte("defghij")); err != nil { // total 10 bytes into cap 8
		t.Fatal(err)
	}
	if !r.trunc {
		t.Fatal("expected truncation")
	}
	if got := r.String(); len(got) != 8 || got != "cdefghij" {
		t.Fatalf("ring tail = %q, want %q", got, "cdefghij")
	}
	if r.total != 10 {
		t.Fatalf("total = %d, want 10", r.total)
	}
}

func TestRingBufferWriteLargerThanCap(t *testing.T) {
	r := newRing(4)
	if _, err := r.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if r.String() != "6789" {
		t.Fatalf("got %q, want 6789", r.String())
	}
	if !r.trunc || r.total != 10 {
		t.Fatalf("trunc=%v total=%d", r.trunc, r.total)
	}
}

func TestValidateCreateRejectsOutOfRange(t *testing.T) {
	cases := []CreateParams{
		{CPU: 300},
		{CPU: -1},
		{MemoryMB: -5},
		{AutoStopSecs: -1},
		{Ports: []PortMapping{{HostPort: 70000, GuestPort: 80}}},
		{Ports: []PortMapping{{HostPort: -1, GuestPort: 80}}},
		{Secrets: []SecretParam{{EnvVar: "", Value: "x"}}},
		{Mounts: []MountParam{{GuestPath: "", Volume: "v"}}},
	}
	for i, c := range cases {
		if err := validateCreate(c); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		}
	}
	if err := validateCreate(CreateParams{CPU: 2, MemoryMB: 512, Ports: []PortMapping{{HostPort: 8080, GuestPort: 80}}}); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
}

func TestCheckHostPathAllowlist(t *testing.T) {
	// Empty allowlist denies everything.
	deny := &Service{}
	if _, err := deny.checkHostPath("/tmp/x"); err == nil {
		t.Fatal("empty allowlist should deny")
	}

	s := &Service{hostPaths: normalizeHostPaths([]string{"/tmp/msbd"})}
	if _, err := s.checkHostPath("/tmp/msbd/out.tar"); err != nil {
		t.Fatalf("path within allowlist rejected: %v", err)
	}
	// Escape attempts.
	for _, p := range []string{"/etc/passwd", "/tmp/msbd/../secret", "/tmp/msbdX", ""} {
		if _, err := s.checkHostPath(p); err == nil {
			t.Errorf("path %q should be denied", p)
		}
	}
}

func TestTerminalTicketSingleUseAndBinding(t *testing.T) {
	s := &Service{tickets: newTicketRegistry()}
	tok, exp := s.MintTerminalTicket("sbx_a")
	if tok == "" || !exp.After(time.Now()) {
		t.Fatal("bad ticket")
	}
	// Wrong sandbox is rejected and consumes the ticket.
	if s.RedeemTerminalTicket(tok, "sbx_b") {
		t.Fatal("ticket redeemed for wrong sandbox")
	}
	if s.RedeemTerminalTicket(tok, "sbx_a") {
		t.Fatal("ticket should have been consumed by the failed attempt")
	}

	// Correct sandbox works exactly once.
	tok2, _ := s.MintTerminalTicket("sbx_a")
	if !s.RedeemTerminalTicket(tok2, "sbx_a") {
		t.Fatal("valid ticket rejected")
	}
	if s.RedeemTerminalTicket(tok2, "sbx_a") {
		t.Fatal("ticket reused")
	}
	if s.RedeemTerminalTicket("", "sbx_a") || s.RedeemTerminalTicket("nope", "sbx_a") {
		t.Fatal("bogus tickets accepted")
	}
}

func TestNormalizeHostPathsSkipsEmpty(t *testing.T) {
	got := normalizeHostPaths([]string{" ", "", "/tmp/a", "  /tmp/b "})
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, "/tmp/") {
			t.Fatalf("unexpected %q", p)
		}
	}
}
