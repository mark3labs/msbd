package core

// jobs_cancel_test.go — regression tests for job cancellation.
//
// Background: a running job's drain goroutine is parked in ExecHandle.Recv on
// the same SDK stream that Signal/Kill use. Calling Kill "live" therefore waits
// on an ack the parked Recv has already swallowed, which used to hang the
// calling HTTP request for the entire lifetime of the command. cancelJob fixes
// that by cancelling the job context first (unblocking Recv) and only then
// killing under handleMu — the sequence dropSandbox already used.

import (
	"context"
	"testing"
	"time"
)

// newFakeRunningJob seeds a running job with no SDK handle. cancelJob must cope
// with a nil handle (it only touches it when non-nil and unclosed), which lets
// us assert the locking/state machine without booting a microVM.
func newFakeRunningJob(r *JobRegistry, sb, id string) *job {
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		state:  JobRunning,
		stdout: newRing(1024),
		stderr: newRing(1024),
		cancel: cancel,
	}
	// Park a stand-in "drain" on the job context so we can prove cancelJob
	// releases it rather than waiting for the command to finish.
	go func() { <-ctx.Done() }()
	r.mu.Lock()
	r.jobs[key(sb, id)] = j
	r.mu.Unlock()
	return j
}

// TestCancelJobIsPromptAndMarksKilled is the regression test for the hang: the
// call must return immediately, not block for the command's duration.
func TestCancelJobIsPromptAndMarksKilled(t *testing.T) {
	r := NewJobRegistry(1024, time.Minute)
	defer r.Close()
	newFakeRunningJob(r, "sbx_1", "job_1")

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- r.cancelJob("sbx_1", "job_1") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelJob: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelJob blocked — the Recv/Kill deadlock has regressed")
	}
	if el := time.Since(start); el > time.Second {
		t.Errorf("cancelJob took %s, want ~instant", el)
	}

	st, err := r.poll("sbx_1", "job_1")
	if err != nil {
		t.Fatal(err)
	}
	// A killed process reports exit 0, so the state is the only signal that
	// distinguishes "user cancelled" from "succeeded".
	if st.State != JobKilled {
		t.Errorf("state = %q, want %q", st.State, JobKilled)
	}
}

// TestCancelJobUnblocksDrain proves the job context is cancelled, which is what
// releases a Recv parked on a live stream.
func TestCancelJobUnblocksDrain(t *testing.T) {
	r := NewJobRegistry(1024, time.Minute)
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	j := &job{state: JobRunning, stdout: newRing(1024), stderr: newRing(1024), cancel: cancel}
	r.mu.Lock()
	r.jobs[key("sbx_1", "job_1")] = j
	r.mu.Unlock()

	unblocked := make(chan struct{})
	go func() { <-ctx.Done(); close(unblocked) }()

	if err := r.cancelJob("sbx_1", "job_1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-unblocked:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelJob did not cancel the job context, so drain stays parked in Recv")
	}
}

// TestCancelJobIsIdempotent — the UI can fire Cancel twice, and the janitor may
// race it; neither should error or clobber a recorded exit state.
func TestCancelJobIsIdempotent(t *testing.T) {
	r := NewJobRegistry(1024, time.Minute)
	defer r.Close()
	newFakeRunningJob(r, "sbx_1", "job_1")

	if err := r.cancelJob("sbx_1", "job_1"); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if err := r.cancelJob("sbx_1", "job_1"); err != nil {
		t.Fatalf("second cancel: %v", err)
	}

	// A job that already finished normally must keep its state.
	r.mu.Lock()
	r.jobs[key("sbx_1", "job_2")] = &job{state: JobDone, exitCode: 3, stdout: newRing(16), stderr: newRing(16)}
	r.mu.Unlock()
	if err := r.cancelJob("sbx_1", "job_2"); err != nil {
		t.Fatalf("cancel finished job: %v", err)
	}
	st, _ := r.poll("sbx_1", "job_2")
	if st.State != JobDone || st.ExitCode != 3 {
		t.Errorf("finished job clobbered: state=%q exit=%d", st.State, st.ExitCode)
	}
}

func TestCancelJobUnknown(t *testing.T) {
	r := NewJobRegistry(1024, time.Minute)
	defer r.Close()
	if err := r.cancelJob("sbx_nope", "job_nope"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestSignalKillDelegatesToCancel pins the REST contract: signal<=0 means
// "terminate", and must take the non-blocking path.
func TestSignalKillDelegatesToCancel(t *testing.T) {
	r := NewJobRegistry(1024, time.Minute)
	defer r.Close()
	newFakeRunningJob(r, "sbx_1", "job_1")

	done := make(chan error, 1)
	go func() { done <- r.signal(context.Background(), "sbx_1", "job_1", 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("signal(0): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal(0) blocked — it should delegate to cancelJob")
	}
	st, _ := r.poll("sbx_1", "job_1")
	if st.State != JobKilled {
		t.Errorf("state = %q, want %q", st.State, JobKilled)
	}
}
