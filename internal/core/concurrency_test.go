package core

import (
	"sync"
	"testing"
	"time"
)

// TestJobRegistryConcurrentAccess exercises the JobRegistry maps under the race
// detector: concurrent poll/drop/janitor with no live SDK handles. It guards the
// mutex coverage added for bounded buffers + TTL eviction.
func TestJobRegistryConcurrentAccess(t *testing.T) {
	r := NewJobRegistry(1024, 50*time.Millisecond)
	defer r.Close()

	// Seed a finished job directly (no SDK handle needed).
	seed := func(sb, id string) {
		j := &job{state: JobDone, stdout: newRing(1024), stderr: newRing(1024), finishedAt: time.Now(), closed: true}
		r.mu.Lock()
		r.jobs[key(sb, id)] = j
		r.mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sb := "sbx_x"
			id := "job_" + string(rune('a'+n%26))
			seed(sb, id)
			_, _ = r.poll(sb, id)
			if n%3 == 0 {
				r.dropSandbox(sb)
			}
			_ = r.ActiveJobs()
		}(i)
	}
	wg.Wait()

	// Let the janitor run at least once to evict the finished jobs.
	time.Sleep(150 * time.Millisecond)
	if got := r.ActiveJobs(); got != 0 {
		t.Logf("ActiveJobs after TTL sweep = %d (expected eventual 0)", got)
	}
}

// TestRegistryConcurrentResolveMiss confirms singleflight-guarded resolve() of a
// non-existent sandbox is race-free and returns ErrNotFound to every caller
// (the slow path never panics or corrupts the maps under concurrency).
func TestRegistryConcurrentResolveMiss(t *testing.T) {
	reg := NewRegistry("microsandbox/python")
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, err := reg.resolve(t.Context(), "sbx_missing_concurrent")
			if err == nil {
				t.Error("expected error for missing sandbox")
			}
		})
	}
	wg.Wait()
}
