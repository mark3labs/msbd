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
	listen        string
	apiKey        string
	defaultImage  string
	maxSandboxes  int
	createTimeout time.Duration
	logLevel      string
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
		"Bearer token required on every request; empty = unauthenticated ($MSBD_API_KEY)")
	f.StringVar(&o.defaultImage, "default-image", envOr("MSBD_DEFAULT_IMAGE", "microsandbox/python"),
		"OCI image used when create omits image ($MSBD_DEFAULT_IMAGE)")
	f.IntVar(&o.maxSandboxes, "max-sandboxes", envInt("MSBD_MAX_SANDBOXES", 0),
		"Hard cap on concurrent sandboxes; 0 = unlimited ($MSBD_MAX_SANDBOXES)")
	f.DurationVar(&o.createTimeout, "create-timeout",
		time.Duration(envInt("MSBD_CREATE_TIMEOUT_SECS", 300))*time.Second,
		"Sandbox boot deadline, covers cold OCI pulls ($MSBD_CREATE_TIMEOUT_SECS)")
	f.StringVar(&o.logLevel, "log-level", envOr("MSBD_LOG_LEVEL", "info"),
		"Log verbosity: debug, info, warn, error ($MSBD_LOG_LEVEL)")

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
	}

	if o.apiKey == "" {
		log.Warn("api key is empty — server is UNAUTHENTICATED (dev only)")
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
	})

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
	httpSrv := &http.Server{
		Addr:              o.listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write/read timeout: Run can block on long builds. Front with a
		// proxy that has a high read timeout, or none.
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
		log.Info("shutting down — draining in-flight requests")
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		if serr := httpSrv.Shutdown(sctx); serr != nil {
			return fmt.Errorf("graceful shutdown: %w", serr)
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
