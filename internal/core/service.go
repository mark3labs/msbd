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
	jobs         *JobRegistry
}

// Opts configures the Service. Zero values fall back to sane defaults.
type Opts struct {
	DefaultImage  string
	MaxSandboxes  int
	CreateTimeout time.Duration
}

func NewService(o Opts) *Service {
	if o.DefaultImage == "" {
		o.DefaultImage = "microsandbox/python" // safe public default
	}
	if o.CreateTimeout <= 0 {
		o.CreateTimeout = 5 * time.Minute // cold image pull headroom
	}
	return &Service{
		reg:          NewRegistry(o.DefaultImage),
		defaultImage: o.DefaultImage,
		maxSandboxes: o.MaxSandboxes,
		createTO:     o.CreateTimeout,
		jobs:         NewJobRegistry(),
	}
}

func (s *Service) DefaultImage() string { return s.defaultImage }

// Reconcile re-attaches to pre-existing VMs at boot.
func (s *Service) Reconcile(ctx context.Context) (int, error) { return s.reg.Reconcile(ctx) }

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// CreateParams is the provider-neutral create input.
type CreateParams struct {
	Image        string
	CPU          float64
	MemoryMB     int
	AutoStopSecs int
	Env          map[string]string
	Labels       map[string]string
	Workdir      string
}

// Instance is the provider-neutral resource shape.
type Instance struct {
	ID            string
	Image         string
	State         string
	Workdir       string
	UptimeSeconds float64
	CostUSD       float64
	Labels        map[string]string
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
	if len(p.Env) > 0 {
		opts = append(opts, msb.WithEnv(p.Env))
	}
	if len(p.Labels) > 0 {
		opts = append(opts, msb.WithLabels(p.Labels))
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
	//      then use it. This is what callers like shipagent expect when they
	//      pass /workspace — the dir gets created if the image didn't ship it,
	//      rather than the SDK refusing to boot.
	//   2. No workdir pinned: trust the image's own WORKDIR by asking the guest
	//      with `pwd` (matches the Daytona adapter's GetWorkingDir).
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

func (s *Service) Get(ctx context.Context, id string) (*Instance, error) {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.instanceFromHandle(id, h), nil
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

// Start is an idempotent ensure-running: resolve() boots a stopped box and
// caches a live handle.
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) instanceFromHandle(id string, h *msb.SandboxHandle) *Instance {
	inst := &Instance{
		ID:            id,
		State:         mapStatus(h.Status()),
		UptimeSeconds: s.reg.uptime(id),
	}
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

func parentDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// shellQuote wraps s in single quotes for safe inclusion in a /bin/sh command
// line. Embedded single quotes are escaped via the standard `'\''` dance.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
