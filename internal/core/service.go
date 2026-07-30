package core

// service.go — the cgo/FFI-facing business logic. Everything that touches the
// microsandbox SDK lives behind this Service; the api package only speaks DTOs.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Service is the single owner of all microsandbox SDK calls.
type Service struct {
	reg          *Registry
	defaultImage string
	maxSandboxes int
	createTO     time.Duration
	pullTO       time.Duration
	jobs         *JobRegistry
	tickets      *ticketRegistry
	hostPaths    []string // allowlisted host path prefixes (empty = deny all)

	// admit serializes create admission (the capacity check + reservation) so
	// N concurrent creates can't all read n<max and overshoot MaxSandboxes.
	admit    sync.Mutex
	inflight int // creates that passed admission but haven't finished
}

// Opts configures the Service. Zero values fall back to sane defaults.
type Opts struct {
	DefaultImage  string
	MaxSandboxes  int
	CreateTimeout time.Duration
	// PullTimeout bounds a standalone image pull (POST /v1/images/pull). Pulls
	// boot a throwaway microVM and a cold fetch of a large image can outlast the
	// create timeout, so it gets its own, larger budget.
	PullTimeout time.Duration
	// JobMaxBytes caps each async job's stdout/stderr ring buffer. 0 uses a
	// built-in default. Older output is dropped once the cap is hit.
	JobMaxBytes int
	// JobTTL is how long a finished job's output is retained before eviction.
	// 0 uses a built-in default.
	JobTTL time.Duration
	// HostPaths is the allowlist of host path prefixes that the host-transfer
	// endpoints (files/copy-from-host, files/copy-to-host, snapshot
	// export/import) may touch. Empty means those endpoints are DENIED.
	HostPaths []string
}

func NewService(o Opts) *Service {
	if o.DefaultImage == "" {
		o.DefaultImage = "microsandbox/python" // safe public default
	}
	if o.CreateTimeout <= 0 {
		o.CreateTimeout = 5 * time.Minute // cold image pull headroom
	}
	if o.PullTimeout <= 0 {
		o.PullTimeout = 15 * time.Minute // standalone pulls can be large/cold
	}
	return &Service{
		reg:          NewRegistry(o.DefaultImage),
		defaultImage: o.DefaultImage,
		maxSandboxes: o.MaxSandboxes,
		createTO:     o.CreateTimeout,
		pullTO:       o.PullTimeout,
		jobs:         NewJobRegistry(o.JobMaxBytes, o.JobTTL),
		tickets:      newTicketRegistry(),
		hostPaths:    normalizeHostPaths(o.HostPaths),
	}
}

func (s *Service) DefaultImage() string { return s.defaultImage }

// MaxSandboxes returns the configured concurrent-sandbox cap (0 = unlimited).
func (s *Service) MaxSandboxes() int { return s.maxSandboxes }

// Reconcile re-attaches to pre-existing VMs at boot.
func (s *Service) Reconcile(ctx context.Context) (int, error) { return s.reg.Reconcile(ctx) }

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// maxDiskGB bounds CreateParams.DiskGB so the GiB->MiB conversion stays within
// the uint32 the SDK's managed root-disk size accepts (math.MaxUint32 / 1024).
const maxDiskGB = 4194303

// CreateParams is the provider-neutral create input.
type CreateParams struct {
	Image         string
	CPU           float64
	MemoryMB      int
	DiskGB        int // writable overlay upper size, in GiB (OCI images)
	AutoStopSecs  int
	Env           map[string]string
	Labels        map[string]string
	Workdir       string
	User          string
	Hostname      string
	NetworkPolicy string        // none | public-only | allow-all | non-local
	Ports         []PortMapping // host:guest port forwards
	Secrets       []SecretParam // injected env-var secrets
	Mounts        []MountParam  // named-volume mounts by guest path
}

// PortMapping forwards a host port to a guest port.
type PortMapping struct {
	HostPort  int
	GuestPort int
	Protocol  string // "tcp" (default) | "udp"
}

// SecretParam injects a secret value as a guest environment variable. The value
// never crosses the FFI into the guest as a literal — the runtime handles it.
type SecretParam struct {
	EnvVar string
	Value  string
}

// MountParam mounts a named persistent volume at a guest path.
type MountParam struct {
	GuestPath string
	Volume    string
	Readonly  bool
}

// Instance is the provider-neutral resource shape.
type Instance struct {
	ID            string
	Image         string
	State         string
	Workdir       string
	UptimeSeconds float64
	Labels        map[string]string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// InspectResult is the full config + metadata for one sandbox.
type InspectResult struct {
	Instance
	ConfigJSON string
}

// validateCreate rejects out-of-range numeric inputs before they are silently
// truncated by the uint conversions in buildCreateOptions (e.g. cpu:300 -> 44
// as a uint8, host_port:-1 -> 65535 as a uint16). Returns ErrInvalidParams.
func validateCreate(p CreateParams) error {
	if p.CPU < 0 || p.CPU > 255 {
		return fmt.Errorf("%w: cpu %g out of range (0-255)", ErrInvalidParams, p.CPU)
	}
	if p.MemoryMB < 0 {
		return fmt.Errorf("%w: memory_mb %d must be non-negative", ErrInvalidParams, p.MemoryMB)
	}
	if p.AutoStopSecs < 0 {
		return fmt.Errorf("%w: auto_stop_secs %d must be non-negative", ErrInvalidParams, p.AutoStopSecs)
	}
	for _, pm := range p.Ports {
		if pm.HostPort < 0 || pm.HostPort > 65535 {
			return fmt.Errorf("%w: host_port %d out of range (1-65535)", ErrInvalidParams, pm.HostPort)
		}
		if pm.GuestPort < 0 || pm.GuestPort > 65535 {
			return fmt.Errorf("%w: guest_port %d out of range (1-65535)", ErrInvalidParams, pm.GuestPort)
		}
	}
	for _, se := range p.Secrets {
		if strings.TrimSpace(se.EnvVar) == "" {
			return fmt.Errorf("%w: secret env_var must not be empty", ErrInvalidParams)
		}
	}
	for _, m := range p.Mounts {
		if strings.TrimSpace(m.GuestPath) == "" {
			return fmt.Errorf("%w: mount guest_path must not be empty", ErrInvalidParams)
		}
		if strings.TrimSpace(m.Volume) == "" {
			return fmt.Errorf("%w: mount volume must not be empty", ErrInvalidParams)
		}
	}
	return nil
}

// cleanupSandbox best-effort removes a possibly-partially-created VM on a fresh,
// short-lived context so a failed/aborted Create doesn't orphan a detached box.
func cleanupSandbox(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if h, err := msb.GetSandbox(ctx, name); err == nil {
		_ = h.Stop(ctx)
	}
	_ = msb.RemoveSandbox(ctx, name)
}

func (s *Service) Create(ctx context.Context, p CreateParams) (*Instance, error) {
	if err := validateCreate(p); err != nil {
		return nil, err
	}
	// Admission control: serialize the capacity check + slot reservation so
	// concurrent creates can't all observe n<max and overshoot. A count()
	// error is treated as "deny" rather than silently skipping the cap.
	if s.maxSandboxes > 0 {
		s.admit.Lock()
		n, cerr := s.count(ctx)
		if cerr != nil {
			s.admit.Unlock()
			return nil, fmt.Errorf("%w: cannot verify capacity: %v", ErrCapacity, cerr)
		}
		if n+s.inflight >= s.maxSandboxes {
			s.admit.Unlock()
			return nil, fmt.Errorf("%w: %d/%d sandboxes", ErrCapacity, n+s.inflight, s.maxSandboxes)
		}
		s.inflight++
		s.admit.Unlock()
		defer func() {
			s.admit.Lock()
			s.inflight--
			s.admit.Unlock()
		}()
	}
	image := strings.TrimSpace(p.Image)
	if image == "" {
		image = s.defaultImage
	}
	name := newName()
	workdir := strings.TrimSpace(p.Workdir)

	opts, err := buildCreateOptions(p, image)
	if err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, s.createTO)
	defer cancel()

	sb, err := msb.CreateSandbox(cctx, name, opts...)
	if err != nil {
		// The runtime may have partially materialized the VM before failing.
		// Best-effort cleanup on a fresh context so we don't orphan a detached
		// box that still counts against capacity.
		cleanupSandbox(name)
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	s.reg.cache(name, sb)

	// Resolve the box's REAL working directory.
	//
	//   1. Caller pinned a workdir: ensure it exists in the guest (mkdir -p),
	//      then use it — the dir gets created if the image didn't ship it,
	//      rather than the SDK refusing to boot.
	//   2. No workdir pinned: trust the image's own WORKDIR by asking the guest
	//      with `pwd`.
	//
	// Best-effort: fall back to defaultWorkdir on any error.
	resolved := workdir
	if resolved != "" {
		quoted := shellQuote(resolved)
		if _, perr := runShell(cctx, sb, ExecParams{Cmd: "mkdir -p " + quoted}); perr != nil {
			// Don't fail Create on mkdir error — fall back to the image's WORKDIR.
			resolved = ""
		}
	}
	if resolved == "" {
		if out, perr := runShell(cctx, sb, ExecParams{Cmd: "pwd"}); perr == nil {
			if wd := strings.TrimSpace(out.Stdout); strings.HasPrefix(wd, "/") {
				resolved = wd
			}
		}
	}
	resolved = defaultWorkdir(resolved)
	s.reg.setWorkdir(name, resolved)

	return &Instance{
		ID:      name,
		Image:   image,
		State:   StateRunning,
		Workdir: resolved,
		Labels:  p.Labels,
	}, nil
}

// buildCreateOptions translates the provider-neutral CreateParams into the
// microsandbox SDK option slice. It is deterministic and free of side effects
// (no SDK calls beyond constructing options), which keeps the CreateParams ->
// SandboxOption mapping unit-testable without booting a microVM. It returns an
// error for inputs that can't be represented safely (e.g. an out-of-range
// disk size) rather than silently dropping or wrapping them.
func buildCreateOptions(p CreateParams, image string) ([]msb.SandboxOption, error) {
	opts := []msb.SandboxOption{
		msb.WithImage(image),
		msb.WithDetached(), // survive msbd restart
	}
	if p.MemoryMB > 0 {
		opts = append(opts, msb.WithMemory(uint32(p.MemoryMB)))
	}
	if p.CPU > 0 {
		opts = append(opts, msb.WithCPUs(uint8(p.CPU)))
	}
	if p.DiskGB != 0 {
		// API field is GiB; SDK option takes MiB (uint32). Reject negatives and
		// values that would overflow the MiB conversion instead of wrapping.
		if p.DiskGB < 0 {
			return nil, fmt.Errorf("invalid disk_gb: %d (must be non-negative)", p.DiskGB)
		}
		if p.DiskGB > maxDiskGB {
			return nil, fmt.Errorf("invalid disk_gb: %d (exceeds maximum of %d GiB)", p.DiskGB, maxDiskGB)
		}
		// SDK 0.6.7 replaced WithOCIUpperSize with the explicit root-disk
		// factory (the old option is now a deprecated alias). "Managed" is the
		// writable overlay on top of an OCI image, which is what disk_gb has
		// always meant here.
		opts = append(opts, msb.WithRootDisk(msb.RootDisk.Managed(uint32(p.DiskGB)*1024)))
	}
	if len(p.Env) > 0 {
		opts = append(opts, msb.WithEnv(p.Env))
	}
	if len(p.Labels) > 0 {
		opts = append(opts, msb.WithLabels(p.Labels))
	}
	if u := strings.TrimSpace(p.User); u != "" {
		opts = append(opts, msb.WithUser(u))
	}
	if hn := strings.TrimSpace(p.Hostname); hn != "" {
		opts = append(opts, msb.WithHostname(hn))
	}
	if net := networkConfig(p.NetworkPolicy); net != nil {
		opts = append(opts, msb.WithNetwork(net))
	}
	if len(p.Ports) > 0 {
		bindings := make([]msb.PortBinding, 0, len(p.Ports))
		for _, pm := range p.Ports {
			proto := msb.PortProtocolTCP
			if strings.EqualFold(pm.Protocol, "udp") {
				proto = msb.PortProtocolUDP
			}
			bindings = append(bindings, msb.PortBinding{
				HostPort:  uint16(pm.HostPort),
				GuestPort: uint16(pm.GuestPort),
				Protocol:  proto,
			})
		}
		opts = append(opts, msb.WithPortBindings(bindings...))
	}
	if len(p.Secrets) > 0 {
		secrets := make([]msb.SecretEntry, 0, len(p.Secrets))
		for _, se := range p.Secrets {
			secrets = append(secrets, msb.SecretEntry{EnvVar: se.EnvVar, Value: se.Value})
		}
		opts = append(opts, msb.WithSecrets(secrets...))
	}
	if len(p.Mounts) > 0 {
		mounts := make(map[string]msb.MountConfig, len(p.Mounts))
		for _, mp := range p.Mounts {
			mounts[mp.GuestPath] = msb.Mount.Named(mp.Volume, msb.MountOptions{Readonly: mp.Readonly})
		}
		opts = append(opts, msb.WithMounts(mounts))
	}
	// Deliberately NOT passing WithWorkdir(p.Workdir): the SDK validates that
	// the path already exists in the image's rootfs at boot, and refuses with
	// "invalid config: workdir does not exist in guest" when it doesn't — a
	// common case when callers pass an opinionated default like /workspace
	// against an arbitrary OCI image. We mkdir+chdir below instead, which is
	// strictly looser and matches Docker's behavior. The image's own WORKDIR
	// still applies for the initial pwd when the caller didn't pin one.
	if p.AutoStopSecs > 0 {
		opts = append(opts, msb.WithIdleTimeout(time.Duration(p.AutoStopSecs)*time.Second))
	}
	return opts, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Instance, error) {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.instanceFromHandle(id, h), nil
}

// Inspect returns the full SDK config JSON plus normalized metadata.
func (s *Service) Inspect(ctx context.Context, id string) (*InspectResult, error) {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return &InspectResult{
		Instance:   *s.instanceFromHandle(id, h),
		ConfigJSON: h.ConfigJSON(),
	}, nil
}

func (s *Service) List(ctx context.Context) ([]Instance, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	out := make([]Instance, 0, len(handles))
	for _, h := range handles {
		out = append(out, *s.instanceFromHandle(h.Name(), h))
	}
	return out, nil
}

func (s *Service) count(ctx context.Context) (int, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return 0, err
	}
	return len(handles), nil
}

func (s *Service) Stop(ctx context.Context, id string) error {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	if err := h.Stop(ctx); err != nil {
		return fmt.Errorf("stop %s: %w", id, err)
	}
	s.reg.forget(id) // drop the (now invalid) live handle; resolve() re-acquires
	return nil
}

// Start is an idempotent ensure-running: resolve() boots a stopped box, verifies
// its guest agent actually came up, and caches a live handle. It returns an
// error (rather than a false-positive success) if the box reports a start but
// the guest never boots — e.g. a halted guest with stale runtime state.
func (s *Service) Start(ctx context.Context, id string) error {
	_, err := s.reg.resolve(ctx, id)
	return err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	// RemoveSandbox refuses a running box, so stop it first (best-effort).
	if h, err := msb.GetSandbox(ctx, id); err == nil {
		switch h.Status() {
		case msb.SandboxStatusRunning, msb.SandboxStatusDraining, msb.SandboxStatusPaused:
			_ = h.Stop(ctx)
		}
	}
	s.jobs.dropSandbox(id)
	s.reg.forget(id)
	if err := msb.RemoveSandbox(ctx, id); err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Exec / Run
// ---------------------------------------------------------------------------

type ExecParams struct {
	Cmd     string
	Cwd     string
	Env     map[string]string
	Timeout time.Duration
	Stdin   bool // open a stdin pipe (jobs only)
}

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Exec is one short round-trip. Does NOT ensure-running (boot-time helpers).
func (s *Service) Exec(ctx context.Context, id string, p ExecParams) (*ExecResult, error) {
	sb := s.reg.cached(id)
	if sb == nil {
		var err error
		sb, err = s.reg.resolve(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	return runShell(ctx, sb, p)
}

// Run is long-safe and ensures the box is running first (transparent resume).
func (s *Service) Run(ctx context.Context, id string, p ExecParams) (*ExecResult, error) {
	return withSandbox(ctx, s, id, func(sb *msb.Sandbox) (*ExecResult, error) {
		return runShell(ctx, sb, p)
	})
}

// buildExecOptions translates the shared exec knobs (cwd/env/timeout) into SDK
// options. It is the single source of truth for that mapping, shared by the
// synchronous exec path (runShell) and the async job path (launch, which
// appends its own stdin-pipe option on top).
func buildExecOptions(p ExecParams) []msb.ExecOption {
	var execOpts []msb.ExecOption
	if strings.TrimSpace(p.Cwd) != "" {
		execOpts = append(execOpts, msb.WithExecCwd(p.Cwd))
	}
	if len(p.Env) > 0 {
		execOpts = append(execOpts, msb.WithExecEnv(p.Env))
	}
	if p.Timeout > 0 {
		execOpts = append(execOpts, msb.WithExecTimeout(p.Timeout))
	}
	return execOpts
}

func runShell(ctx context.Context, sb *msb.Sandbox, p ExecParams) (*ExecResult, error) {
	out, err := sb.Shell(ctx, p.Cmd, buildExecOptions(p)...)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return &ExecResult{ExitCode: out.ExitCode(), Stdout: out.Stdout(), Stderr: out.Stderr()}, nil
}

// ---------------------------------------------------------------------------
// File IO (native FS)
// ---------------------------------------------------------------------------

func (s *Service) ReadFile(ctx context.Context, id, path, cwd string) ([]byte, error) {
	return withSandbox(ctx, s, id, func(sb *msb.Sandbox) ([]byte, error) {
		b, err := sb.FS().Read(ctx, resolvePath(path, cwd))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return b, nil
	})
}

func (s *Service) WriteFile(ctx context.Context, id, path, cwd string, content []byte) error {
	_, err := withSandbox(ctx, s, id, func(sb *msb.Sandbox) (struct{}, error) {
		dest := resolvePath(path, cwd)
		if dir := parentDir(dest); dir != "" {
			_ = sb.FS().Mkdir(ctx, dir) // best-effort; Write reports the real error
		}
		if err := sb.FS().Write(ctx, dest, content); err != nil {
			return struct{}{}, fmt.Errorf("write %s: %w", path, err)
		}
		return struct{}{}, nil
	})
	return err
}

// withSandbox resolves the sandbox, runs fn, and — if fn fails AND the VM is
// found to be down afterward (idle auto-stop, external stop, or crash after the
// handle was cached) — invalidates the stale cached handle and retries once
// against a freshly resolved (re-booted) handle. The liveness re-check keeps a
// genuine in-guest failure from being masked or the command double-run.
func withSandbox[T any](ctx context.Context, s *Service, id string, fn func(*msb.Sandbox) (T, error)) (T, error) {
	var zero T
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return zero, err
	}
	out, err := fn(sb)
	if err == nil {
		return out, nil
	}
	// Only retry if the box is actually down now — otherwise the error is real.
	h, gerr := msb.GetSandbox(ctx, id)
	if gerr != nil {
		return zero, err
	}
	switch h.Status() {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining:
		return zero, err
	}
	s.reg.forget(id)
	sb2, rerr := s.reg.resolve(ctx, id)
	if rerr != nil {
		return zero, err
	}
	return fn(sb2)
}

// ---------------------------------------------------------------------------
// Async jobs
// ---------------------------------------------------------------------------

func (s *Service) Launch(ctx context.Context, id string, p ExecParams) (string, error) {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return "", err
	}
	return s.jobs.launch(ctx, id, sb, p)
}

func (s *Service) Poll(id, job string) (*JobStatus, error) {
	return s.jobs.poll(id, job)
}

// WriteJobStdin writes bytes to a running job's stdin pipe.
func (s *Service) WriteJobStdin(id, job string, data []byte) error {
	return s.jobs.writeStdin(id, job, data)
}

// CloseJobStdin closes a running job's stdin pipe (EOF).
func (s *Service) CloseJobStdin(id, job string) error {
	return s.jobs.closeStdin(id, job)
}

// SignalJob sends a signal to a running job (sig <= 0 means kill).
// CancelJob terminates a running job promptly. Prefer this over SignalJob for
// a user-facing "stop this command" action: it unblocks the drain goroutine
// first, so it returns immediately instead of waiting on an agent ack that the
// parked Recv would swallow, and it marks the job JobKilled so the caller can
// tell a cancellation apart from a clean exit 0.
func (s *Service) CancelJob(id, job string) error {
	return s.jobs.cancelJob(id, job)
}

func (s *Service) SignalJob(ctx context.Context, id, job string, sig int) error {
	return s.jobs.signal(ctx, id, job, sig)
}

// ActiveJobs reports the number of tracked async jobs (for diagnostics/metrics).
func (s *Service) ActiveJobs() int { return s.jobs.ActiveJobs() }

// Close releases background resources (the job janitor). Safe to call once at
// shutdown; in-flight jobs and terminals are in-memory and simply end.
func (s *Service) Close() { s.jobs.Close() }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) instanceFromHandle(id string, h *msb.SandboxHandle) *Instance {
	inst := &Instance{
		ID:            id,
		State:         sdkStatus(h.Status()),
		UptimeSeconds: s.reg.uptime(id),
	}
	inst.CreatedAt = h.CreatedAt()
	inst.UpdatedAt = h.UpdatedAt()
	if cfg, err := h.Config(); err == nil && cfg != nil {
		inst.Image = cfg.Image
		inst.Workdir = cfg.Workdir
		inst.Labels = cfg.Labels
	}
	// Prefer the workdir we resolved at create (the image's real WORKDIR);
	// the stored config only carries an explicitly-pinned workdir.
	if wd := s.reg.workdir(id); wd != "" {
		inst.Workdir = wd
	}
	inst.Workdir = defaultWorkdir(inst.Workdir)
	return inst
}

func newName() string { return randID("sbx_", 8) }

// randID returns prefix followed by n cryptographically-random bytes rendered as
// hex. It's the shared minter behind sandbox/job/snapshot ids.
func randID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func defaultWorkdir(wd string) string {
	if strings.TrimSpace(wd) != "" {
		return wd
	}
	return "/"
}

func resolvePath(path, cwd string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "/") {
		return path
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "/"
	}
	if !strings.HasSuffix(cwd, "/") {
		cwd += "/"
	}
	return cwd + path
}

// networkConfig maps msbd's stable policy-preset name onto an SDK
// NetworkConfig, or nil for the empty/default case.
//
// SDK 0.6.7 removed the flat NetworkPolicyPreset enum in favour of composable
// deny-by-default *profiles* (public / private / host). The preset names below
// are msbd's WIRE CONTRACT (they appear in openapi.yaml and every generated
// client), so they are kept verbatim and translated here. Mapping, matching the
// semantics the old presets documented:
//
//	none        → deny everything (unchanged factory)
//	public-only → public internet only; RFC-1918 private ranges blocked
//	allow-all   → permit everything (unchanged factory)
//	non-local   → public internet + private/LAN egress, still no loopback,
//	              link-local or metadata — i.e. every profile except host
func networkConfig(policy string) *msb.NetworkConfig {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "none":
		return msb.NetworkPolicy.None()
	case "public-only", "public_only":
		return msb.NetworkPolicy.FromProfiles(msb.NetworkProfilePublic)
	case "allow-all", "allow_all":
		return msb.NetworkPolicy.AllowAll()
	case "non-local", "non_local":
		return msb.NetworkPolicy.FromProfiles(msb.NetworkProfilePublic, msb.NetworkProfilePrivate)
	default:
		return nil
	}
}

func parentDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// shellQuote wraps s in single quotes for safe inclusion in a /bin/sh command
// line. Embedded single quotes are escaped via the standard `'\”` dance.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// normalizeHostPaths cleans + absolutizes the configured host-path allowlist.
func normalizeHostPaths(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	return out
}

// checkHostPath verifies that a host path targeted by a copy/export/import
// endpoint is within one of the configured allowlist prefixes. An empty
// allowlist DENIES every host path (secure default). Symlinks in the existing
// portion are resolved to block escapes. Returns the cleaned absolute path on
// success, or ErrForbidden.
func (s *Service) checkHostPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("%w: empty host path", ErrForbidden)
	}
	if len(s.hostPaths) == 0 {
		return "", fmt.Errorf("%w: host-path transfers are disabled (set --host-paths / MSBD_HOST_PATHS)", ErrForbidden)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrForbidden, err)
	}
	abs = filepath.Clean(abs)
	// Resolve symlinks on the existing portion to prevent a symlinked path from
	// escaping the allowlist; fall back to the lexical path for not-yet-existing
	// write destinations.
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	for _, root := range s.hostPaths {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%w: host path %q is not within the allowlist", ErrForbidden, p)
}
