package core

// tickets.go — short-lived, single-use terminal tickets.
//
// The dashboard terminal page and any browser WS client cannot set an
// Authorization header on a WebSocket handshake, so historically the long-lived
// REST bearer token was forwarded as ?key= (and embedded in the terminal HTML).
// That leaks the full-power token into page source, browser history, and
// reverse-proxy access logs. A ticket is a per-sandbox, single-use, ~60s token
// that the WS auth layer accepts INSTEAD of the bearer key: minting it requires
// the caller to already be authenticated, and redeeming it consumes it.

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// terminalTicketTTL bounds how long a minted ticket is valid before use.
	terminalTicketTTL = 60 * time.Second
)

type ticket struct {
	sandbox string
	expires time.Time
}

type ticketRegistry struct {
	mu sync.Mutex
	m  map[string]ticket
}

func newTicketRegistry() *ticketRegistry {
	return &ticketRegistry{m: make(map[string]ticket)}
}

// MintTerminalTicket issues a single-use ticket bound to sandbox id, valid for
// terminalTicketTTL. Callers must already be authenticated. The returned token
// is opaque; RedeemTerminalTicket consumes it.
func (s *Service) MintTerminalTicket(sandbox string) (string, time.Time) {
	tok := newTicketToken()
	exp := time.Now().Add(terminalTicketTTL)
	s.tickets.mu.Lock()
	s.tickets.m[tok] = ticket{sandbox: sandbox, expires: exp}
	s.tickets.sweepLocked()
	s.tickets.mu.Unlock()
	return tok, exp
}

// RedeemTerminalTicket consumes a ticket, returning true only if it exists, is
// unexpired, and is bound to the given sandbox. It is single-use: a successful
// (or expired) redemption removes it.
func (s *Service) RedeemTerminalTicket(tok, sandbox string) bool {
	if tok == "" {
		return false
	}
	s.tickets.mu.Lock()
	defer s.tickets.mu.Unlock()
	t, ok := s.tickets.m[tok]
	if !ok {
		return false
	}
	delete(s.tickets.m, tok) // single use, even on mismatch
	if time.Now().After(t.expires) {
		return false
	}
	return t.sandbox == sandbox
}

// sweepLocked drops expired tickets. Caller holds mu.
func (r *ticketRegistry) sweepLocked() {
	now := time.Now()
	for k, t := range r.m {
		if now.After(t.expires) {
			delete(r.m, k)
		}
	}
}

func newTicketToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "tkt_" + hex.EncodeToString(b[:])
}
