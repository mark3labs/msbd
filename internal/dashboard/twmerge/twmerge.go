// Package twmerge provides a goroutine-safe Tailwind class merger.
//
// Every templui component funnels its class strings through utils.TwMerge,
// which historically called the upstream package-level
// tailwind-merge-go/pkg/twmerge.Merge. That global is NOT safe for concurrent
// use, and templ components render on whatever goroutine is serving the
// request, so two simultaneous dashboard requests raced. `go test -race`
// caught it in two distinct places:
//
//  1. Lazy init. CreateTwMerge returns a closure over `config`, `cache` and
//     `fnToCall`; the first call runs an `init` shim that assigns all three and
//     then swaps `fnToCall` to the real merger. Concurrent first calls
//     read/write those captured vars unsynchronized.
//
//  2. The default LRU cache (pkg/lru). It has two mutexes but they don't cover
//     the same data: Set calls remove() under cacheMutex while Get calls
//     insertRight() under listMutex, so the intrusive linked list's prev/next
//     pointers are mutated concurrently. That's the race CI reported.
//
// Both are upstream bugs we can't fix from here, so we sidestep them: build ONE
// private merger instance, hand it a cache that actually locks, and force the
// lazy init to completion inside a sync.Once so the captured variables are
// written exactly once with a happens-before edge to every later call.
//
// The merge itself is pure — MakeSplitModifiers / MakeGetClassGroupId /
// MakeMergeClassList only read the config — so once init has run, concurrent
// calls touch shared state only through the cache below.
package twmerge

import (
	"sync"

	upstream "github.com/Oudwins/tailwind-merge-go/pkg/twmerge"
)

// maxCacheEntries bounds the memoization map. It matches the upstream default
// (MakeDefaultConfig sets MaxCacheSize: 1000) and is far above the number of
// distinct class strings a server-rendered dashboard actually produces.
const maxCacheEntries = 1000

// syncCache is a concurrency-safe implementation of twmerge's cache.ICache.
//
// Rather than reimplement an LRU (the upstream one is what broke), it uses a
// plain map under an RWMutex and, on overflow, flushes wholesale. Class strings
// come from a fixed set of templates, so the working set is small and stable:
// the cap is a safety valve against unbounded growth, not a hot path. A flush
// costs one re-merge per live class string, which is microseconds of pure
// string work.
type syncCache struct {
	mu sync.RWMutex
	m  map[string]string
}

func (c *syncCache) Get(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[key]
}

func (c *syncCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxCacheEntries {
		clear(c.m)
	}
	c.m[key] = value
}

// merger builds the single shared merge function. sync.OnceValue gives us both
// "exactly once" and the memory barrier that makes the closure's captured
// config/cache/fnToCall safe to read from every goroutine afterwards.
var merger = sync.OnceValue(func() upstream.TwMergeFn {
	fn := upstream.CreateTwMerge(nil, &syncCache{m: make(map[string]string, maxCacheEntries)})
	// Run the lazy `init` shim to completion HERE, while we're still inside the
	// Once. After this call fnToCall points at the real merger and is never
	// written again. An empty class list short-circuits before the cache, so
	// this warm-up costs one config build and nothing else.
	_ = fn("")
	return fn
})

// Merge combines Tailwind classes and resolves conflicts. Safe for concurrent
// use by multiple goroutines.
func Merge(classes ...string) string {
	return merger()(classes...)
}
