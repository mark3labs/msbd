package twmerge

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fields returns the class list as a set. The upstream merger emits surviving
// classes in Go map-iteration order, so the ORDER of the result is not stable
// between calls (only cache hits make it look stable). Order is irrelevant for
// CSS — specificity comes from the stylesheet, not the attribute — so every
// assertion here compares sets.
func fields(s string) map[string]bool {
	set := map[string]bool{}
	for f := range strings.FieldsSeq(s) {
		set[f] = true
	}
	return set
}

func sameClasses(a, b string) bool {
	x, y := fields(a), fields(b)
	if len(x) != len(y) {
		return false
	}
	for k := range x {
		if !y[k] {
			return false
		}
	}
	return true
}

func TestMergeResolvesConflicts(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"bg-red-500 hover:bg-blue-500", "bg-green-500"}, "bg-green-500 hover:bg-blue-500"},
		{[]string{"p-4", "p-8"}, "p-8"},
		{[]string{"text-sm font-bold", "text-lg"}, "font-bold text-lg"},
		{[]string{"custom-class p-2", "p-6"}, "custom-class p-6"},
		{[]string{"", ""}, ""},
		{nil, ""},
	} {
		if got := Merge(tc.in...); !sameClasses(got, tc.want) {
			t.Errorf("Merge(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMergeConcurrent is the regression test for the data race that broke CI.
//
// Upstream's global twmerge.Merge is not goroutine-safe: its lazy init writes
// captured config/cache/fnToCall vars unsynchronized, and its default LRU cache
// mutates one linked list under two different mutexes. templ renders on
// per-request goroutines, so two concurrent dashboard requests tripped it.
// Under `-race` this test fails loudly if TwMerge ever points back at an
// unsynchronized merger.
func TestMergeConcurrent(t *testing.T) {
	const goroutines, iterations = 16, 300

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range iterations {
				// Mix repeated (cache-hit) and unique (cache-miss + evicting)
				// inputs so Get, Set and the overflow flush all run hot.
				Merge("bg-red-500 p-4", "bg-blue-500")
				Merge(fmt.Sprintf("p-%d text-sm", i%12), fmt.Sprintf("p-%d", (i+g)%12))
			}
		}(g)
	}
	wg.Wait()
}

// TestMergeCacheOverflowKeepsCorrectResults exercises the wholesale flush in
// syncCache.Set: results must stay correct after the cache is cleared.
func TestMergeCacheOverflowKeepsCorrectResults(t *testing.T) {
	const probe = "p-4"
	want := Merge(probe, "p-8")

	// Push well past maxCacheEntries with unique inputs to force flushes.
	for i := range maxCacheEntries * 3 {
		Merge(fmt.Sprintf("m-%d", i), fmt.Sprintf("m-%d", i+1))
	}

	if got := Merge(probe, "p-8"); !sameClasses(got, want) {
		t.Errorf("after cache overflow Merge(%q,\"p-8\") = %q, want %q", probe, got, want)
	}
}

func TestSyncCacheBounded(t *testing.T) {
	c := &syncCache{m: make(map[string]string)}
	for i := range maxCacheEntries * 2 {
		c.Set(fmt.Sprintf("k%d", i), "v")
	}
	if len(c.m) > maxCacheEntries {
		t.Errorf("cache grew to %d entries, want <= %d", len(c.m), maxCacheEntries)
	}
	if got := c.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty", got)
	}
}
