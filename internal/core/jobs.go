package core

// jobs.go — background (async) command execution.
//
// Launch starts an SDK ExecStream and a drain goroutine that accumulates
// stdout/stderr and records the exit code. Poll reads the snapshot. Jobs are
// in-memory only: after an msbd restart a previously-launched job is unknown
// and Poll reports "gone" (the JobGone contract shipagent already handles).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Job lifecycle states (mirror shipagent's sandbox.Job* vocabulary).
const (
	JobRunning = "running"
	JobDone    = "done"
	JobDead    = "dead"
	JobGone    = "gone"
)

type JobStatus struct {
	State    string
	ExitCode int
	Stdout   string
	Stderr   string
}

type job struct {
	mu       sync.Mutex
	state    string
	exitCode int
	stdout   strings.Builder
	stderr   strings.Builder
	handle   *msb.ExecHandle
}

func (j *job) snapshot() *JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return &JobStatus{
		State:    j.state,
		ExitCode: j.exitCode,
		Stdout:   j.stdout.String(),
		Stderr:   j.stderr.String(),
	}
}

type JobRegistry struct {
	mu   sync.RWMutex
	jobs map[string]*job // key "sandboxID\x00jobID"
}

func NewJobRegistry() *JobRegistry {
	return &JobRegistry{jobs: make(map[string]*job)}
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
	// ShellStream: /bin/sh -c <cmd>, streamed.
	h, err := sb.ShellStream(ctx, p.Cmd, execOpts...)
	if err != nil {
		return "", fmt.Errorf("launch: %w", err)
	}
	jobID := newJobID()
	j := &job{state: JobRunning, handle: h}
	r.mu.Lock()
	r.jobs[key(sandboxID, jobID)] = j
	r.mu.Unlock()

	go drain(j)
	return jobID, nil
}

// drain consumes the stream to completion in the background. It uses a detached
// context so the job keeps running after the launching HTTP request returns.
func drain(j *job) {
	defer func() { _ = j.handle.Close() }()
	ctx := context.Background()
	for {
		ev, err := j.handle.Recv(ctx)
		if err != nil {
			j.mu.Lock()
			if j.state == JobRunning {
				j.state = JobDead
			}
			j.mu.Unlock()
			return
		}
		j.mu.Lock()
		switch ev.Kind {
		case msb.ExecEventStdout:
			j.stdout.Write(ev.Data)
		case msb.ExecEventStderr:
			j.stderr.Write(ev.Data)
		case msb.ExecEventExited:
			j.exitCode = ev.ExitCode
			j.state = JobDone
			j.mu.Unlock()
			return
		case msb.ExecEventFailed:
			if ev.Failure != nil {
				j.stderr.WriteString(fmt.Sprintf("%v", ev.Failure))
			}
			j.state = JobDead
			j.mu.Unlock()
			return
		case msb.ExecEventDone:
			if j.state == JobRunning {
				j.state = JobDone
			}
			j.mu.Unlock()
			return
		}
		j.mu.Unlock()
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

func (r *JobRegistry) dropSandbox(sandboxID string) {
	prefix := sandboxID + "\x00"
	r.mu.Lock()
	for k, j := range r.jobs {
		if strings.HasPrefix(k, prefix) {
			_ = j.handle.Kill(context.Background())
			delete(r.jobs, k)
		}
	}
	r.mu.Unlock()
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}
