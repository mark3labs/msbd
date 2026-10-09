package components

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestScripts(t *testing.T) {
	var output bytes.Buffer
	if err := Scripts().Render(templ.WithNonce(context.Background(), "test-nonce"), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`src="` + bundleSrc + `"`, `nonce="test-nonce"`, `<script defer`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output %q missing %q", output.String(), want)
		}
	}
}

// The generated manifest must name a committed local asset: plain go build
// must never depend on upstream routes or a runtime JavaScript build.
func TestBundleIsLocalAndPresent(t *testing.T) {
	const prefix = "/assets/js/"
	if !strings.HasPrefix(bundleSrc, prefix) {
		t.Fatalf("bundle is not served by embedded assets: %q", bundleSrc)
	}
	asset, err := os.ReadFile(filepath.Join("..", "assets", "js", strings.TrimPrefix(bundleSrc, prefix)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"window.templ", "data-templ"} {
		if !bytes.Contains(asset, []byte(want)) {
			t.Errorf("bundle lacks %q", want)
		}
	}
}
