package core

// registry.go — the live-handle cache and the central resolve() helper.
//
// A microsandbox VM is owned by the *Sandbox FFI handle. Created detached, the
// VM survives both handle release and an msbd process restart, so the registry
// can transparently re-acquire a live handle by name via the SDK's
// GetSandbox → Connect/StartDetached primitives. resolve() folds reconnect AND
// transparent resume into one call: every exec/run/file path goes through it.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Sandbox states are passed through verbatim from the microsandbox SDK
// (running, stopped, crashed, draining, paused). msbd does not normalize or
// remap them, so Instance.state reflects the runtime's own vocabulary.
const (
	StateRunning = "running"
	StateUnknown = "unknown"
)

// ErrNotFound is returned when a sandbox name has no backing VM.
var ErrNotFound = fmt.Errorf("sandbox not found")

// agentVerifyTimeout bounds the guest-agent readiness probe performed after
// booting a previously-stopped sandbox. A healthy boot brings the agent up well
// within this window; exceeding it means the start silently no-op'd (e.g. a
// halted guest with a stale runtime/heartbeat that the runtime mistakes for a
// live VM). It's a package var so tests can shrink it.
var agentVerifyTimeout = 20 * time.Second

// Registry caches live *Sandbox handles keyed by name (== provider id).
type Registry struct {
	mu       sync.RWMutex
	live     map[string]*msb.Sandbox // name → live FFI handle
	created  map[string]time.Time    // name → first-seen (for uptime)
	workdirs map[string]string       // name → resolved native working dir
	defImage string
}

func NewRegistry(defaultImage string) *Registry {
	return &Registry{
		live:     make(map[string]*msb.Sandbox),
		created:  make(map[string]time.Time),
		workdirs: make(map[string]string),
		defImage: defaultImage,
	}
}

func (r *Registry) cache(name string, sb *msb.Sandbox) {
	r.mu.Lock()
	r.live[name] = sb
	if _, ok := r.created[name]; !ok {
		r.created[name] = time.Now()
	}
	r.mu.Unlock()
}

func (r *Registry) cached(name string) *msb.Sandbox {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.live[name]
}

func (r *Registry) forget(name string) {
	r.mu.Lock()
	delete(r.live, name)
	delete(r.created, name)
	delete(r.workdirs, name)
	r.mu.Unlock()
}

// setWorkdir records a sandbox's resolved native working directory.
func (r *Registry) setWorkdir(name, wd string) {
	if wd == "" {
		return
	}
	r.mu.Lock()
	r.workdirs[name] = wd
	r.mu.Unlock()
}

// workdir returns the cached native working directory for name, or "".
func (r *Registry) workdir(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workdirs[name]
}

func (r *Registry) uptime(name string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.created[name]; ok {
		return time.Since(t).Seconds()
	}
	return 0
}

// resolve returns a live, RUNNING *Sandbox handle for name. It is the single
// choke point for transparent resume + reconnect-after-restart:
//
//  1. cache hit → return it (fast path);
//  2. GetSandbox(name) to confirm the VM exists (ErrNotFound if gone);
//  3. running/draining → Connect; stopped/paused/crashed → boot it back up
//     (detached) AND verify the guest agent actually came up; then cache and
//     return the live handle.
func (r *Registry) resolve(ctx context.Context, name string) (*msb.Sandbox, error) {
	if sb := r.cached(name); sb != nil {
		return sb, nil
	}
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, ErrNotFound
	}
	var sb *msb.Sandbox
	switch h.Status() {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining:
		sb, err = h.Connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, err)
		}
	default: // stopped, paused, crashed → boot it back up, detached
		sb, err = bootAndVerify(ctx, h, name)
		if err != nil {
			return nil, err
		}
	}
	r.cache(name, sb)
	return sb, nil
}

// bootAndVerify boots a stopped/paused/crashed sandbox detached and confirms
// its guest agent actually came up before handing back a live handle.
//
// The microsandbox runtime can report a start as successful while the guest
// never actually boots — most notably when a previous guest was halted (not
// cleanly powered off) and left a stale runtime/heartbeat behind that the
// runtime mistakes for an already-live VM. Left unchecked, StartDetached
// returns a handle for a dead box, we cache it, POST /start answers 204, and
// the first exec/run/terminal/file op fails with an opaque "no agent endpoint
// found". We catch that here instead: if the agent doesn't come up we make one
// recovery attempt (force-kill to clear the stale runtime, then reboot), and
// if it still doesn't, we return a descriptive error and cache nothing — so
// /start reports the real failure rather than a false success.
func bootAndVerify(ctx context.Context, h *msb.SandboxHandle, name string) (*msb.Sandbox, error) {
	sb, err := h.StartDetached(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: start: %w", name, err)
	}
	if verifyAgentUp(ctx, name) == nil {
		return sb, nil
	}
	// Silent no-op: the runtime reported a start but the guest agent never came
	// up. Force-kill to clear any stale runtime/heartbeat state, then boot once
	// more and re-verify.
	_ = sb.Detach(ctx) // release the dead handle without touching the VM
	_ = h.Kill(ctx)    // best-effort: clears stale runtime for a halted guest
	sb, err = h.StartDetached(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: restart after clearing stale runtime: %w", name, err)
	}
	if verr := verifyAgentUp(ctx, name); verr != nil {
		_ = sb.Detach(ctx)
		return nil, fmt.Errorf("resolve %s: guest agent did not come up after start; "+
			"the sandbox is unresumable and must be recreated: %w", name, verr)
	}
	return sb, nil
}

// verifyAgentUp confirms the guest agent for name is reachable — i.e. the VM
// actually booted and can service exec/file/terminal requests — by opening (and
// immediately closing) an agent connection. It returns nil as soon as a
// handshake succeeds, retrying within agentVerifyTimeout to absorb the brief
// window where the socket appears slightly after StartDetached returns, and the
// last error once the deadline passes.
func verifyAgentUp(ctx context.Context, name string) error {
	deadline := time.Now().Add(agentVerifyTimeout)
	var lastErr error
	for {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		client, err := msb.ConnectAgentSandbox(cctx, name)
		cancel()
		if err == nil {
			_ = client.Close()
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Reconcile re-attaches to every pre-existing VM at startup so an msbd restart
// doesn't orphan running sandboxes. Best-effort: a handle that fails to connect
// is left for lazy resolve() on next use.
func (r *Registry) Reconcile(ctx context.Context) (int, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, h := range handles {
		r.mu.Lock()
		if _, ok := r.created[h.Name()]; !ok {
			r.created[h.Name()] = h.CreatedAt()
		}
		r.mu.Unlock()
		n++
	}
	return n, nil
}

// sdkStatus returns the SDK's raw status string, or "unknown" if empty. msbd
// passes the runtime's vocabulary through untouched rather than normalizing it.
func sdkStatus(s msb.SandboxStatus) string {
	if strings.TrimSpace(string(s)) == "" {
		return StateUnknown
	}
	return string(s)
}
