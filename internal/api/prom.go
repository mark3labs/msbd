package api

// prom.go — a tiny Prometheus text-exposition endpoint (GET /metrics) with no
// external client dependency. It exports the operational signals ops most want
// for a microVM daemon whose failure mode is host-resource exhaustion:
// sandbox counts by state vs the configured cap, active async jobs, live
// terminals, request totals by status class, and Go runtime basics.

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// reqCounters tracks request outcomes for the /metrics endpoint. Cheap atomics;
// updated from logMW so every routed request is counted.
type reqCounters struct {
	total atomic.Int64
	c2xx  atomic.Int64
	c3xx  atomic.Int64
	c4xx  atomic.Int64
	c5xx  atomic.Int64
}

func (rc *reqCounters) observe(status int) {
	rc.total.Add(1)
	switch {
	case status >= 500:
		rc.c5xx.Add(1)
	case status >= 400:
		rc.c4xx.Add(1)
	case status >= 300:
		rc.c3xx.Add(1)
	default:
		rc.c2xx.Add(1)
	}
}

// termCounter tracks live terminal sessions (incremented/decremented by the
// terminal handler). Exposed at /metrics and used by graceful shutdown.
var (
	activeTerminals atomic.Int64
	globalReqStats  reqCounters
)

func (s *Server) handleProm(w http.ResponseWriter, r *http.Request) {
	var byState map[string]int
	if list, err := s.svc.List(r.Context()); err == nil {
		byState = make(map[string]int, len(list))
		for i := range list {
			byState[list[i].State]++
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var b strings.Builder
	metric(&b, "msbd_up", "gauge", "1 = process is serving", 1)
	metric(&b, "msbd_uptime_seconds", "gauge", "seconds since start", time.Since(s.startedT).Seconds())

	total := 0
	fmt.Fprintln(&b, "# HELP msbd_sandboxes Sandboxes known to the runtime, by state.")
	fmt.Fprintln(&b, "# TYPE msbd_sandboxes gauge")
	for state, n := range byState {
		fmt.Fprintf(&b, "msbd_sandboxes{state=%q} %d\n", state, n)
		total += n
	}
	metric(&b, "msbd_sandboxes_total", "gauge", "Total sandboxes known to the runtime", float64(total))
	metric(&b, "msbd_sandboxes_max", "gauge", "Configured sandbox cap (0 = unlimited)", float64(s.svc.MaxSandboxes()))
	metric(&b, "msbd_jobs_active", "gauge", "Tracked async jobs (running + retained)", float64(s.svc.ActiveJobs()))
	metric(&b, "msbd_terminals_active", "gauge", "Live interactive terminal sessions", float64(activeTerminals.Load()))

	fmt.Fprintln(&b, "# HELP msbd_http_requests_total HTTP requests by status class.")
	fmt.Fprintln(&b, "# TYPE msbd_http_requests_total counter")
	fmt.Fprintf(&b, "msbd_http_requests_total{class=\"2xx\"} %d\n", globalReqStats.c2xx.Load())
	fmt.Fprintf(&b, "msbd_http_requests_total{class=\"3xx\"} %d\n", globalReqStats.c3xx.Load())
	fmt.Fprintf(&b, "msbd_http_requests_total{class=\"4xx\"} %d\n", globalReqStats.c4xx.Load())
	fmt.Fprintf(&b, "msbd_http_requests_total{class=\"5xx\"} %d\n", globalReqStats.c5xx.Load())

	metric(&b, "msbd_goroutines", "gauge", "Current goroutine count", float64(runtime.NumGoroutine()))
	metric(&b, "msbd_memory_heap_bytes", "gauge", "Heap bytes in use", float64(ms.HeapInuse))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func metric(b *strings.Builder, name, typ, help string, val float64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n%s %g\n", name, help, name, typ, name, val)
}
