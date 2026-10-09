package utils

import (
	"sync"
	"testing"
)

// Exercise the actual vendor entry point, including its v4 normalization,
// so an upstream utility refresh cannot silently restore the unsafe global.
func TestCNConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				got := CN("p-2! px-(--old)", []any{"p-4!", map[string]bool{"px-(--new)": true}})
				if got != "p-4! px-(--new)" {
					t.Errorf("CN = %q", got)
					return
				}
			}
		})
	}
	wg.Wait()
}
