package core

// service.go — the cgo/FFI-facing business logic. Everything that touches the
// microsandbox SDK lives behind this Service; the api package only speaks DTOs.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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
		jobs:         NewJobRegistry(),
	}
}

func (s *Service) DefaultImage() string { return s.defaultImage }

// Reconcile re-attaches to pre-existing VMs at boot.
func (s *Service) Reconcile(ctx context.Context) (int, error) { return s.reg.Reconcile(ctx) }

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// maxDiskGB bounds CreateParams.DiskGB so the GiB->MiB conversion stays within
// the uint32 the SDK's WithOCIUpperSize option accepts (math.MaxUint32 / 1024).
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

func (s *Service) Create(ctx context.Context, p CreateParams) (*Instance, error) {
	if s.maxSandboxes > 0 {
		if n, err := s.count(ctx); err == nil && n >= s.maxSandboxes {
			return nil, fmt.Errorf("host at capacity: %d/%d sandboxes", n, s.maxSandboxes)
		}
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
		opts = append(opts, msb.WithOCIUpperSize(uint32(p.DiskGB)*1024))
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
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	return runShell(ctx, sb, p)
}

func runShell(ctx context.Context, sb *msb.Sandbox, p ExecParams) (*ExecResult, error) {
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
	out, err := sb.Shell(ctx, p.Cmd, execOpts...)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return &ExecResult{ExitCode: out.ExitCode(), Stdout: out.Stdout(), Stderr: out.Stderr()}, nil
}

// ---------------------------------------------------------------------------
// File IO (native FS)
// ---------------------------------------------------------------------------

func (s *Service) ReadFile(ctx context.Context, id, path, cwd string) ([]byte, error) {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	b, err := sb.FS().Read(ctx, resolvePath(path, cwd))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

func (s *Service) WriteFile(ctx context.Context, id, path, cwd string, content []byte) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	dest := resolvePath(path, cwd)
	if dir := parentDir(dest); dir != "" {
		_ = sb.FS().Mkdir(ctx, dir) // best-effort; Write reports the real error
	}
	if err := sb.FS().Write(ctx, dest, content); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
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
func (s *Service) SignalJob(ctx context.Context, id, job string, sig int) error {
	return s.jobs.signal(ctx, id, job, sig)
}

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

func newName() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sbx_" + hex.EncodeToString(b[:])
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

// networkConfig maps a policy preset name to an SDK NetworkConfig, or nil for
// the empty/default case.
func networkConfig(policy string) *msb.NetworkConfig {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "none":
		return &msb.NetworkConfig{Policy: msb.NetworkPolicyPresetNone}
	case "public-only", "public_only":
		return &msb.NetworkConfig{Policy: msb.NetworkPolicyPresetPublicOnly}
	case "allow-all", "allow_all":
		return &msb.NetworkConfig{Policy: msb.NetworkPolicyPresetAllowAll}
	case "non-local", "non_local":
		return &msb.NetworkConfig{Policy: msb.NetworkPolicyPresetNonLocal}
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
