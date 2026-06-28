package main

// msbd — Microsandbox REST host.
//
// A standalone, self-hostable HTTP server that wraps the microsandbox Go SDK
// (cgo + libkrun) and exposes the small REST API shipagent's cgo-free
// msbprovider adapter consumes. Run this on a host that has /dev/kvm; shipagent
// then treats microsandbox as just another remote sandbox backend.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/msbd/internal/api"
	"github.com/mark3labs/msbd/internal/core"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func main() {
	cfg := loadConfig()

	log.Printf("msbd starting — SDK %s, listen %s, default image %q",
		core.SDKVersion(), cfg.listen, cfg.defaultImage)

	// 1) Ensure the msb + libkrunfw runtime is present (downloads on first run).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if err := msb.EnsureInstalled(ctx); err != nil {
		cancel()
		log.Fatalf("EnsureInstalled failed: %v", err)
	}
	cancel()
	if v, err := core.RuntimeVersion(); err == nil {
		log.Printf("msb runtime ready — version %s", v)
	}

	svc := core.NewService(core.Opts{
		DefaultImage:  cfg.defaultImage,
		MaxSandboxes:  cfg.maxSandboxes,
		CreateTimeout: cfg.createTimeout,
	})

	// 2) Re-attach to any sandboxes that outlived a previous msbd process.
	rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
	if n, err := svc.Reconcile(rctx); err != nil {
		log.Printf("reconcile (non-fatal): %v", err)
	} else if n > 0 {
		log.Printf("reconciled %d pre-existing sandbox(es)", n)
	}
	rcancel()

	// 3) Serve.
	srv := api.NewServer(svc, cfg.apiKey, readinessProbe).SetPrebaked(cfg.prebaked)
	httpSrv := &http.Server{
		Addr:              cfg.listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write/read timeout: Run can block on long builds. Front with a
		// proxy that has a high read timeout, or none.
	}
	log.Printf("msbd listening on %s", cfg.listen)
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
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

type config struct {
	listen        string
	apiKey        string
	defaultImage  string
	maxSandboxes  int
	createTimeout time.Duration
	prebaked      bool
}

func loadConfig() config {
	c := config{
		listen:        envOr("MSBD_LISTEN", ":8099"),
		apiKey:        os.Getenv("MSBD_API_KEY"),
		defaultImage:  envOr("MSBD_DEFAULT_IMAGE", "microsandbox/python"),
		maxSandboxes:  envInt("MSBD_MAX_SANDBOXES", 0),
		createTimeout: time.Duration(envInt("MSBD_CREATE_TIMEOUT_SECS", 300)) * time.Second,
		prebaked:      envBool("MSBD_PREBAKED"),
	}
	if c.apiKey == "" {
		log.Printf("WARNING: MSBD_API_KEY is empty — server is UNAUTHENTICATED (dev only)")
	}
	return c
}

func envBool(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes"
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
