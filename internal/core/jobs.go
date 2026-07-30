package core

// jobs.go — background (async) command execution.
//
// Launch starts an SDK ExecStream and a drain goroutine that accumulates
// stdout/stderr into BOUNDED ring buffers and records the exit code. Poll reads
// the snapshot. Jobs are in-memory only: after an msbd restart a previously-
// launched job is unknown and Poll reports "gone" (the JobGone contract).
//
// Resource safety:
//   - stdout/stderr are capped ring buffers (a runaway `yes` can't OOM the
//     daemon); the overflow is reported via JobStatus.Truncated + *_Bytes.
//   - finished jobs are evicted by a TTL janitor so the map + FFI handles don't
//     grow without bound.
//   - all *ExecHandle calls are serialized behind handleMu (the SDK handle is
//     NOT goroutine-safe), and a per-job context lets Close/drop cancel a Recv
//     that's blocked on a wedged guest agent.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Job lifecycle states.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobDead    = "dead"
	JobGone    = "gone"
	// JobKilled marks a job the caller explicitly cancelled. The runtime reports
	// a killed process as exit 0, so without this a cancelled command is
	// indistinguishable from a successful one.
	JobKilled = "killed"
)

const (
	defaultJobMaxBytes = 1 << 20 // 1 MiB per stream
	defaultJobTTL      = 15 * time.Minute
	jobSweepInterval   = time.Minute
	jobKillTimeout     = 5 * time.Second
	// jobSignalTimeout bounds a live Signal/Kill on a running job so a blocked
	// agent ack can't pin an HTTP request open for the whole command.
	jobSignalTimeout = 5 * time.Second
)

type JobStatus struct {
	State       string
	ExitCode    int
	Stdout      string
	Stderr      string
	Truncated   bool  // true if either stream overflowed its ring buffer
	StdoutBytes int64 // total bytes ever produced on stdout (pre-truncation)
	StderrBytes int64 // total bytes ever produced on stderr
}

// ringBuffer is a fixed-capacity byte buffer that keeps only the most recent
// `cap` bytes, tracking the total written and whether truncation occurred.
type ringBuffer struct {
	buf   []byte
	cap   int
	total int64
	trunc bool
}

func newRing(capBytes int) *ringBuffer {
	if capBytes <= 0 {
		capBytes = defaultJobMaxBytes
	}
	return &ringBuffer{cap: capBytes}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	n := len(p)
	r.total += int64(n)
	if n >= r.cap {
		// New data alone exceeds capacity: keep only its tail.
		r.buf = append(r.buf[:0], p[n-r.cap:]...)
		r.trunc = true
		return n, nil
	}
	if len(r.buf)+n > r.cap {
		drop := len(r.buf) + n - r.cap
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.trunc = true
	}
	r.buf = append(r.buf, p...)
	return n, nil
}

func (r *ringBuffer) String() string { return string(r.buf) }

type job struct {
	mu         sync.Mutex
	state      string
	exitCode   int
	stdout     *ringBuffer
	stderr     *ringBuffer
	finishedAt time.Time

	// handleMu serializes all *ExecHandle calls (Recv in drain vs Kill/Signal
	// from HTTP handlers vs Close). The SDK handle is not goroutine-safe.
	// Recv blocks, so drain does NOT hold handleMu across it; instead Close/
	// drop cancel `cancel` to unblock Recv, then Kill under the lock.
	handleMu sync.Mutex
	handle   *msb.ExecHandle
	stdin    *msb.ExecSink
	closed   bool // handle has been Close()d by drain
	cancel   context.CancelFunc
}

func (j *job) snapshot() *JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return &JobStatus{
		State:       j.state,
		ExitCode:    j.exitCode,
		Stdout:      j.stdout.String(),
		Stderr:      j.stderr.String(),
		Truncated:   j.stdout.trunc || j.stderr.trunc,
		StdoutBytes: j.stdout.total,
		StderrBytes: j.stderr.total,
	}
}

func (j *job) markFinished(state string) {
	j.mu.Lock()
	if j.state == JobRunning {
		j.state = state
	}
	j.finishedAt = time.Now()
	j.mu.Unlock()
}

type JobRegistry struct {
	mu       sync.RWMutex
	jobs     map[string]*job // key "sandboxID\x00jobID"
	maxBytes int
	ttl      time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

func NewJobRegistry(maxBytes int, ttl time.Duration) *JobRegistry {
	if maxBytes <= 0 {
		maxBytes = defaultJobMaxBytes
	}
	if ttl <= 0 {
		ttl = defaultJobTTL
	}
	r := &JobRegistry{
		jobs:     make(map[string]*job),
		maxBytes: maxBytes,
		ttl:      ttl,
		stop:     make(chan struct{}),
	}
	go r.janitor()
	return r
}

// Close stops the janitor goroutine (used on graceful shutdown).
func (r *JobRegistry) Close() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// janitor periodically evicts finished jobs older than the TTL, releasing their
// ring buffers and dropping the map entry (Poll then returns JobGone).
func (r *JobRegistry) janitor() {
	t := time.NewTicker(jobSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			now := time.Now()
			r.mu.Lock()
			for k, j := range r.jobs {
				j.mu.Lock()
				expired := !j.finishedAt.IsZero() && now.Sub(j.finishedAt) > r.ttl
				j.mu.Unlock()
				if expired {
					delete(r.jobs, k)
				}
			}
			r.mu.Unlock()
		}
	}
}

// ActiveJobs reports how many jobs are currently tracked (running + retained).
func (r *JobRegistry) ActiveJobs() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.jobs)
}

func key(sandboxID, jobID string) string { return sandboxID + "\x00" + jobID }

func (r *JobRegistry) launch(ctx context.Context, sandboxID string, sb *msb.Sandbox, p ExecParams) (string, error) {
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
	if p.Stdin {
		execOpts = append(execOpts, msb.WithExecStdinPipe())
	}
	// ShellStream: /bin/sh -c <cmd>, streamed.
	h, err := sb.ShellStream(ctx, p.Cmd, execOpts...)
	if err != nil {
		return "", fmt.Errorf("launch: %w", err)
	}
	jobID := newJobID()
	dctx, cancel := context.WithCancel(context.Background())
	j := &job{
		state:  JobRunning,
		handle: h,
		stdout: newRing(r.maxBytes),
		stderr: newRing(r.maxBytes),
		cancel: cancel,
	}
	if p.Stdin {
		j.stdin = h.TakeStdin()
	}
	r.mu.Lock()
	r.jobs[key(sandboxID, jobID)] = j
	r.mu.Unlock()

	go drain(dctx, j)
	return jobID, nil
}

// drain consumes the stream to completion in the background. It uses a detached,
// cancellable context so the job keeps running after the launching HTTP request
// returns, but Close/drop can still unblock a Recv wedged on a hung guest.
func drain(ctx context.Context, j *job) {
	defer func() {
		j.handleMu.Lock()
		j.closed = true
		_ = j.handle.Close()
		j.handleMu.Unlock()
	}()
	for {
		ev, err := j.handle.Recv(ctx)
		if err != nil {
			j.markFinished(JobDead)
			return
		}
		j.mu.Lock()
		switch ev.Kind {
		case msb.ExecEventStdout:
			_, _ = j.stdout.Write(ev.Data)
			j.mu.Unlock()
		case msb.ExecEventStderr:
			_, _ = j.stderr.Write(ev.Data)
			j.mu.Unlock()
		case msb.ExecEventExited:
			j.exitCode = ev.ExitCode
			j.state = JobDone
			j.finishedAt = time.Now()
			j.mu.Unlock()
			return
		case msb.ExecEventFailed:
			if ev.Failure != nil {
				_, _ = fmt.Fprintf(j.stderr, "%v", ev.Failure)
			}
			j.state = JobDead
			j.finishedAt = time.Now()
			j.mu.Unlock()
			return
		case msb.ExecEventDone:
			if j.state == JobRunning {
				j.state = JobDone
			}
			j.finishedAt = time.Now()
			j.mu.Unlock()
			return
		default:
			j.mu.Unlock()
		}
	}
}

func (r *JobRegistry) poll(sandboxID, jobID string) (*JobStatus, error) {
	r.mu.RLock()
	j := r.jobs[key(sandboxID, jobID)]
	r.mu.RUnlock()
	if j == nil {
		return &JobStatus{State: JobGone}, nil
	}
	return j.snapshot(), nil
}

// writeStdin writes bytes to a running job's stdin pipe. Returns ErrNotFound if
// the job is unknown, or an error if the job was not launched with a stdin pipe.
func (r *JobRegistry) writeStdin(sandboxID, jobID string, data []byte) error {
	r.mu.RLock()
	j := r.jobs[key(sandboxID, jobID)]
	r.mu.RUnlock()
	if j == nil {
		return ErrNotFound
	}
	if j.stdin == nil {
		return fmt.Errorf("job %s has no stdin pipe (launch with stdin=true)", jobID)
	}
	if _, err := j.stdin.Write(data); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

// closeStdin closes a running job's stdin pipe (signals EOF to the process).
func (r *JobRegistry) closeStdin(sandboxID, jobID string) error {
	r.mu.RLock()
	j := r.jobs[key(sandboxID, jobID)]
	r.mu.RUnlock()
	if j == nil {
		return ErrNotFound
	}
	if j.stdin == nil {
		return fmt.Errorf("job %s has no stdin pipe", jobID)
	}
	if err := j.stdin.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	}
	return nil
}

// signal sends a signal to a running job's process. A negative or zero signal
// number is treated as a kill request. Serialized against drain's handle use.
//
// IMPORTANT: while a job is running, drain is parked in handle.Recv on the same
// SDK stream. The SDK's Signal/Kill wait for an agent ack that the parked Recv
// consumes, so calling them "live" blocks until the command finishes on its own
// — which wedges the calling HTTP request for the lifetime of the command. We
// therefore bound the call: on timeout the caller gets a clear error instead of
// a hung connection. Callers who want to *terminate* a job should use
// cancelJob, which unblocks Recv first (the pattern dropSandbox already uses).
func (r *JobRegistry) signal(ctx context.Context, sandboxID, jobID string, sig int) error {
	// A kill request is a termination, so take the path that actually works.
	if sig <= 0 {
		return r.cancelJob(sandboxID, jobID)
	}
	r.mu.RLock()
	j := r.jobs[key(sandboxID, jobID)]
	r.mu.RUnlock()
	if j == nil {
		return ErrNotFound
	}
	j.handleMu.Lock()
	defer j.handleMu.Unlock()
	if j.closed {
		return fmt.Errorf("job %s already finished", jobID)
	}
	sctx, cancel := context.WithTimeout(ctx, jobSignalTimeout)
	defer cancel()
	if err := j.handle.Signal(sctx, sig); err != nil {
		return fmt.Errorf("signal job %s: %w", jobID, err)
	}
	return nil
}

// cancelJob terminates a running job deterministically and promptly.
//
// It follows the same sequence dropSandbox uses, and for the same reason: the
// drain goroutine is blocked in handle.Recv, so we cancel the job context FIRST
// to unblock it, and only then take handleMu and Kill. Doing it the other way
// round deadlocks against the parked Recv.
//
// The job is marked JobKilled so callers can distinguish "the user stopped it"
// from "it exited 0" — the runtime reports a killed process as a plain exit 0.
func (r *JobRegistry) cancelJob(sandboxID, jobID string) error {
	r.mu.RLock()
	j := r.jobs[key(sandboxID, jobID)]
	r.mu.RUnlock()
	if j == nil {
		return ErrNotFound
	}

	j.mu.Lock()
	already := j.state != JobRunning
	j.mu.Unlock()
	if already {
		return nil // idempotent: cancelling a finished job is a no-op
	}

	// Unblock drain's Recv so the handle is free for a Kill.
	if j.cancel != nil {
		j.cancel()
	}

	j.handleMu.Lock()
	if !j.closed && j.handle != nil {
		kctx, cancel := context.WithTimeout(context.Background(), jobKillTimeout)
		_ = j.handle.Kill(kctx)
		cancel()
	}
	j.handleMu.Unlock()

	j.mu.Lock()
	j.state = JobKilled
	j.finishedAt = time.Now()
	j.mu.Unlock()
	return nil
}

func (r *JobRegistry) dropSandbox(sandboxID string) {
	prefix := sandboxID + "\x00"
	// Collect + remove under the map lock, then act on the handles OUTSIDE it
	// so a blocking/wedged Kill can't freeze the whole jobs API.
	r.mu.Lock()
	victims := make([]*job, 0)
	for k, j := range r.jobs {
		if strings.HasPrefix(k, prefix) {
			victims = append(victims, j)
			delete(r.jobs, k)
		}
	}
	r.mu.Unlock()
	for _, j := range victims {
		if j.cancel != nil {
			j.cancel() // unblock a Recv hung on a wedged guest
		}
		j.handleMu.Lock()
		if !j.closed && j.handle != nil {
			kctx, cancel := context.WithTimeout(context.Background(), jobKillTimeout)
			_ = j.handle.Kill(kctx)
			cancel()
		}
		j.handleMu.Unlock()
	}
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}
