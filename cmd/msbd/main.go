package main

// msbd — Microsandbox REST host.
//
// A standalone, self-hostable HTTP server that wraps the microsandbox Go SDK
// (cgo + libkrun) and exposes a small REST API for managing fast, local
// microVMs. Run it on a host that has /dev/kvm; clients then treat microsandbox
// as a remote sandbox backend with no cgo on their side.
//
// The CLI is built on cobra and styled with charmbracelet/fang.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/mark3labs/msbd/internal/api"
	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard"

	rootmsbd "github.com/mark3labs/msbd"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Build metadata, injected at link time via -ldflags "-X main.version=...".
// Defaults apply to plain `go build` / `go run` (no ldflags). GoReleaser and
// the Nix flake both override version; commit/date are release-only.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithVersion(version),
		fang.WithCommit(commit),
		// Cancel the command context on Ctrl-C / SIGTERM so the server drains
		// in-flight requests instead of dropping them.
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	); err != nil {
		os.Exit(1)
	}
}

// newRootCmd builds the msbd command tree. The root runs `serve` when invoked
// with no subcommand, so `msbd` (and the Docker/systemd entrypoints) keep
// booting the server exactly as before.
func newRootCmd() *cobra.Command {
	serve := newServeCmd()

	root := &cobra.Command{
		Use:   "msbd",
		Short: "Microsandbox REST host — drive fast local microVMs over HTTP",
		Long: `msbd is a self-hostable HTTP server that wraps the microsandbox Go SDK
(cgo + libkrun) and exposes a REST API for managing fast, local microVMs.

Run it on a host with /dev/kvm; clients then treat microsandbox as a remote
sandbox backend with no cgo on their side.

  • Interactive API docs   http://<listen>/docs
  • OpenAPI spec           http://<listen>/openapi.yaml
  • Health · readiness     /healthz · /readyz`,
		Version: version,
		// fang renders styled usage/errors; let it own the output.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          serve.RunE,
	}
	// Mirror serve's flags onto the root so `msbd --listen ...` works without
	// the explicit subcommand (shared flag values — parsed by whichever runs).
	root.Flags().AddFlagSet(serve.Flags())
	root.AddCommand(serve)
	return root
}

// serveOptions holds the resolved server configuration. Flag defaults are
// seeded from MSBD_* env vars, so an explicit flag overrides the env, which
// overrides the built-in default.
type serveOptions struct {
	listen            string
	apiKey            string
	apiKeyFile        string
	defaultImage      string
	maxSandboxes      int
	createTimeout     time.Duration
	pullTimeout       time.Duration
	jobMaxBytes       int
	jobTTL            time.Duration
	shutdownTimeout   time.Duration
	hostPaths         []string
	logLevel          string
	dashboard         bool
	dashboardUser     string
	dashboardPass     string
	dashboardInsecure bool
}

func newServeCmd() *cobra.Command {
	o := &serveOptions{}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		Long: `Start the msbd HTTP server.

Downloads the msb + libkrunfw runtime on first run, re-attaches to any
sandboxes that outlived a previous process, then serves the REST API until
interrupted (Ctrl-C / SIGTERM trigger a graceful drain).`,
		Example: `  msbd serve --listen :8099 --api-key $TOKEN
  MSBD_DEFAULT_IMAGE=microsandbox/python msbd serve`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), o)
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.listen, "listen", envOr("MSBD_LISTEN", ":8099"),
		"HTTP listen address ($MSBD_LISTEN)")
	f.StringVar(&o.apiKey, "api-key", os.Getenv("MSBD_API_KEY"),
		"Bearer token(s), comma-separated for rotation; empty = unauthenticated ($MSBD_API_KEY)")
	f.StringVar(&o.apiKeyFile, "api-key-file", os.Getenv("MSBD_API_KEY_FILE"),
		"Read the bearer token from this file instead of --api-key ($MSBD_API_KEY_FILE)")
	f.StringVar(&o.defaultImage, "default-image", envOr("MSBD_DEFAULT_IMAGE", "microsandbox/python"),
		"OCI image used when create omits image ($MSBD_DEFAULT_IMAGE)")
	f.IntVar(&o.maxSandboxes, "max-sandboxes", envInt("MSBD_MAX_SANDBOXES", 0),
		"Hard cap on concurrent sandboxes; 0 = unlimited ($MSBD_MAX_SANDBOXES)")
	f.DurationVar(&o.createTimeout, "create-timeout",
		time.Duration(envInt("MSBD_CREATE_TIMEOUT_SECS", 300))*time.Second,
		"Sandbox boot deadline, covers cold OCI pulls ($MSBD_CREATE_TIMEOUT_SECS)")
	f.DurationVar(&o.pullTimeout, "pull-timeout",
		time.Duration(envInt("MSBD_PULL_TIMEOUT_SECS", 900))*time.Second,
		"Standalone image-pull deadline (POST /v1/images/pull); larger than create ($MSBD_PULL_TIMEOUT_SECS)")
	f.IntVar(&o.jobMaxBytes, "job-max-bytes", envInt("MSBD_JOB_MAX_BYTES", 0),
		"Per-stream cap on async job stdout/stderr ring buffers; 0 = built-in default (1 MiB) ($MSBD_JOB_MAX_BYTES)")
	f.DurationVar(&o.jobTTL, "job-ttl",
		time.Duration(envInt("MSBD_JOB_TTL_SECS", 0))*time.Second,
		"Retention for a finished job's output before eviction; 0 = built-in default (15m) ($MSBD_JOB_TTL_SECS)")
	f.DurationVar(&o.shutdownTimeout, "shutdown-timeout",
		time.Duration(envInt("MSBD_SHUTDOWN_TIMEOUT_SECS", 60))*time.Second,
		"Graceful-drain deadline on SIGTERM/Ctrl-C ($MSBD_SHUTDOWN_TIMEOUT_SECS)")
	f.StringSliceVar(&o.hostPaths, "host-paths", envList("MSBD_HOST_PATHS"),
		"Allowlisted host path prefixes for copy/export/import; empty = host transfers DENIED ($MSBD_HOST_PATHS, comma-separated)")
	f.StringVar(&o.logLevel, "log-level", envOr("MSBD_LOG_LEVEL", "info"),
		"Log verbosity: debug, info, warn, error ($MSBD_LOG_LEVEL)")
	f.BoolVar(&o.dashboard, "dashboard", envBool("MSBD_DASHBOARD", true),
		"Serve the web dashboard at /dashboard ($MSBD_DASHBOARD)")
	f.StringVar(&o.dashboardUser, "dashboard-user", os.Getenv("MSBD_DASHBOARD_USER"),
		"Dashboard HTTP Basic auth username; with --dashboard-pass enables auth ($MSBD_DASHBOARD_USER)")
	f.StringVar(&o.dashboardPass, "dashboard-pass", os.Getenv("MSBD_DASHBOARD_PASS"),
		"Dashboard HTTP Basic auth password; with --dashboard-user enables auth ($MSBD_DASHBOARD_PASS)")
	f.BoolVar(&o.dashboardInsecure, "dashboard-allow-insecure", envBool("MSBD_DASHBOARD_ALLOW_INSECURE", false),
		"Allow the dashboard to run WITHOUT auth even when an API key is set (unsafe) ($MSBD_DASHBOARD_ALLOW_INSECURE)")

	return cmd
}

// runServe boots the runtime, reconciles existing sandboxes, and serves until
// ctx is cancelled (signal) or the listener fails. It returns an error rather
// than calling Fatal so fang can render it in the styled error format.
func runServe(ctx context.Context, o *serveOptions) error {
	// Configure the charmbracelet default logger: timestamps on, level from
	// --log-level. Color is auto-detected from the TTY.
	log.SetReportTimestamp(true)
	log.SetTimeFormat("2006/01/02 15:04:05")
	if lvl, err := log.ParseLevel(o.logLevel); err == nil {
		log.SetLevel(lvl)
	} else {
		return fmt.Errorf("invalid --log-level %q (want debug|info|warn|error)", o.logLevel)
	}

	// Config validation: fail fast rather than silently running mis-configured.
	if o.maxSandboxes < 0 {
		return fmt.Errorf("invalid --max-sandboxes %d (must be >= 0)", o.maxSandboxes)
	}
	if o.createTimeout <= 0 {
		return fmt.Errorf("invalid --create-timeout %s (must be > 0)", o.createTimeout)
	}
	if o.pullTimeout <= 0 {
		return fmt.Errorf("invalid --pull-timeout %s (must be > 0)", o.pullTimeout)
	}
	if o.jobMaxBytes < 0 {
		return fmt.Errorf("invalid --job-max-bytes %d (must be >= 0)", o.jobMaxBytes)
	}
	if o.jobTTL < 0 {
		return fmt.Errorf("invalid --job-ttl %s (must be >= 0)", o.jobTTL)
	}
	if o.shutdownTimeout <= 0 {
		return fmt.Errorf("invalid --shutdown-timeout %s (must be > 0)", o.shutdownTimeout)
	}

	// --api-key-file wins over --api-key so secrets can live in a file (Docker/
	// K8s secrets) instead of the process environment.
	if strings.TrimSpace(o.apiKeyFile) != "" {
		b, err := os.ReadFile(o.apiKeyFile)
		if err != nil {
			return fmt.Errorf("read --api-key-file: %w", err)
		}
		o.apiKey = strings.TrimSpace(string(b))
	}

	if o.apiKey == "" {
		log.Warn("api key is empty — server is UNAUTHENTICATED (dev only)")
	}
	if len(o.hostPaths) == 0 {
		log.Info("host-path transfers disabled (no --host-paths / MSBD_HOST_PATHS)")
	} else {
		log.Info("host-path transfers allowlisted", "prefixes", o.hostPaths)
	}

	log.Info("starting msbd",
		"version", version, "commit", commit, "built", date,
		"sdk", core.SDKVersion(), "listen", o.listen, "default_image", o.defaultImage)

	// 1) Ensure the msb + libkrunfw runtime is present (downloads on first run).
	ictx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err := msb.EnsureInstalled(ictx)
	cancel()
	if err != nil {
		return fmt.Errorf("ensure runtime installed: %w", err)
	}
	if v, rerr := core.RuntimeVersion(); rerr == nil {
		log.Info("msb runtime ready", "version", v)
	}

	svc := core.NewService(core.Opts{
		DefaultImage:  o.defaultImage,
		MaxSandboxes:  o.maxSandboxes,
		CreateTimeout: o.createTimeout,
		PullTimeout:   o.pullTimeout,
		JobMaxBytes:   o.jobMaxBytes,
		JobTTL:        o.jobTTL,
		HostPaths:     o.hostPaths,
	})
	defer svc.Close()

	// 2) Re-attach to any sandboxes that outlived a previous msbd process.
	rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
	if n, rerr := svc.Reconcile(rctx); rerr != nil {
		log.Warn("reconcile failed (non-fatal)", "err", rerr)
	} else if n > 0 {
		log.Info("reconciled pre-existing sandboxes", "count", n)
	}
	rcancel()

	// 3) Serve.
	srv := api.NewServer(svc, o.apiKey, readinessProbe).
		SetOpenAPI(rootmsbd.OpenAPISpec)
	if o.dashboard {
		dcfg := dashboard.Config{
			Enabled: true,
			User:    o.dashboardUser,
			Pass:    o.dashboardPass,
			Version: version,
		}
		switch {
		case dcfg.AuthEnabled():
			srv.SetDashboard(dashboard.New(svc, dcfg))
			log.Info("dashboard enabled", "path", "/dashboard", "auth", "basic")
		case o.apiKey == "":
			// Fully open deployment (no API key either) — dev mode.
			srv.SetDashboard(dashboard.New(svc, dcfg))
			log.Warn("dashboard enabled WITHOUT auth (no API key set — dev only)", "path", "/dashboard")
		case o.dashboardInsecure:
			srv.SetDashboard(dashboard.New(svc, dcfg))
			log.Warn("dashboard enabled WITHOUT auth while an API key IS set — --dashboard-allow-insecure overrides the safety refusal", "path", "/dashboard")
		default:
			// API key set but dashboard has no auth: refuse, since the dashboard
			// grants full sandbox control and would bypass the API bearer token.
			log.Error("dashboard NOT mounted: an API key is set but no dashboard auth is configured — set --dashboard-user/--dashboard-pass, or --dashboard-allow-insecure to override")
		}
	}
	httpSrv := &http.Server{
		Addr:              o.listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// IdleTimeout only applies BETWEEN requests, so it's safe alongside long
		// /run and the WebSocket terminal. No Read/Write timeout: Run can block on
		// long builds — body-size limits (MaxBytesReader) guard against slowloris.
		IdleTimeout: 120 * time.Second,
	}

	// Serve in the background so we can wait on either a listener error or a
	// shutdown signal (ctx cancellation from fang's WithNotifySignal).
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", o.listen)
		if serr := httpSrv.ListenAndServe(); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			errCh <- serr
			return
		}
		errCh <- nil
	}()

	select {
	case serr := <-errCh:
		if serr != nil {
			return fmt.Errorf("server error: %w", serr)
		}
		return nil
	case <-ctx.Done():
		log.Info("shutting down — draining in-flight requests", "timeout", o.shutdownTimeout)
		sctx, scancel := context.WithTimeout(context.Background(), o.shutdownTimeout)
		defer scancel()
		if serr := httpSrv.Shutdown(sctx); serr != nil {
			// A drain-deadline overrun (e.g. a long /run or a live terminal still
			// open) is expected operationally — warn and exit 0 so `systemctl
			// restart` / `docker stop` don't report a spurious failure.
			log.Warn("graceful shutdown deadline exceeded; forcing exit", "err", serr)
		}
		log.Info("stopped")
		return nil
	}
}

// readinessProbe reports whether the host can actually boot microVMs: the FFI
// must be loadable (proved by RuntimeVersion succeeding) and /dev/kvm must be
// openable read/write — the same access libkrun needs.
func readinessProbe() error {
	if _, err := core.RuntimeVersion(); err != nil {
		return fmt.Errorf("runtime not ready: %w", err)
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("/dev/kvm not accessible: %w", err)
	}
	_ = f.Close()
	return nil
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// envList parses a comma-separated env var into a trimmed, non-empty slice.
func envList(k string) []string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return nil
	}
	out := make([]string, 0)
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
