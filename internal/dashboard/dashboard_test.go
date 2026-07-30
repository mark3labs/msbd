package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
)

func newTestServer(cfg Config) *httptest.Server {
	svc := core.NewService(core.Opts{DefaultImage: "microsandbox/python"})
	mux := http.NewServeMux()
	New(svc, cfg).Mount(mux)
	return httptest.NewServer(mux)
}

func TestIndexRenders(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readAll(t, resp)
	for _, want := range []string{
		"Overview · msbd",
		"/dashboard/assets/css/output.css",
		"/dashboard/assets/vendor/datastar.js",
		`href="/dashboard/sandboxes"`,
		`aria-current="page"`,
		"msbd-theme", // theme boot script
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

// TestSectionPagesAreRealURLs is the regression test for the SPA-only
// navigation that used to make every section unbookmarkable.
func TestSectionPagesAreRealURLs(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	cases := map[string]string{
		"/dashboard":           "Overview · msbd",
		"/dashboard/sandboxes": "Sandboxes · msbd",
		"/dashboard/volumes":   "Volumes · msbd",
		"/dashboard/images":    "Images · msbd",
		"/dashboard/snapshots": "Snapshots · msbd",
	}
	for path, wantTitle := range cases {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, resp)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, wantTitle) {
			t.Errorf("GET %s: missing title %q", path, wantTitle)
		}
		if !strings.Contains(body, "<!doctype html>") && !strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("GET %s: not a full document", path)
		}
	}
}

func TestStaticAssets(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	for _, path := range []string{
		"/dashboard/assets/css/output.css",
		"/dashboard/assets/favicon.svg",
		"/dashboard/assets/vendor/datastar.js",
		"/dashboard/assets/vendor/xterm.js",
		"/dashboard/assets/js/metric-chart.js",
		"/dashboard/assets/js/dialog.min.js",
		"/dashboard/assets/js/tabs.min.js",
		"/dashboard/assets/js/progress.min.js",
		"/dashboard/assets/js/copybutton.min.js",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestBasicAuth(t *testing.T) {
	ts := newTestServer(Config{Enabled: true, User: "admin", Pass: "secret"})
	defer ts.Close()

	// No credentials → 401.
	resp, err := http.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", resp.StatusCode)
	}
	if h := resp.Header.Get("WWW-Authenticate"); !strings.Contains(h, "Basic") {
		t.Errorf("missing WWW-Authenticate challenge, got %q", h)
	}

	// Correct credentials → 200.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/dashboard", nil)
	req.SetBasicAuth("admin", "secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", resp2.StatusCode)
	}

	// Wrong password → 401.
	req3, _ := http.NewRequest(http.MethodGet, ts.URL+"/dashboard", nil)
	req3.SetBasicAuth("admin", "nope")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-pass status = %d, want 401", resp3.StatusCode)
	}
}

// TestNewRoutesAreMounted keeps the expanded action surface wired up.
func TestNewRoutesAreMounted(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	// A mounted route must not 404. (Most will fail at the SDK layer here,
	// which surfaces as a 200 SSE stream carrying an error toast.)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/dashboard/api/overview"},
		{http.MethodGet, "/dashboard/api/sandboxes/table"},
		{http.MethodGet, "/dashboard/api/volumes/table"},
		{http.MethodGet, "/dashboard/api/images/table"},
		{http.MethodGet, "/dashboard/api/snapshots/table"},
		{http.MethodGet, "/dashboard/api/images/inspect?ref=alpine"},
		{http.MethodPost, "/dashboard/api/sandboxes/x/jobs/y/cancel"},
		{http.MethodPost, "/dashboard/api/sandboxes/x/files"},
		{http.MethodGet, "/dashboard/api/sandboxes/x/files/view?path=/etc"},
		{http.MethodPost, "/dashboard/api/sandboxes/x/files/mkdir"},
		{http.MethodPost, "/dashboard/api/snapshots/x/verify"},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, route not mounted", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestConfigAuthEnabled(t *testing.T) {
	cases := []struct {
		cfg  Config
		want bool
	}{
		{Config{}, false},
		{Config{User: "a"}, true},
		{Config{Pass: "b"}, true},
		{Config{User: "a", Pass: "b"}, true},
	}
	for _, c := range cases {
		if got := c.cfg.AuthEnabled(); got != c.want {
			t.Errorf("AuthEnabled(%+v) = %v, want %v", c.cfg, got, c.want)
		}
	}
}

func TestParsePorts(t *testing.T) {
	ok, err := parsePorts("8080:80\n5432:5432/udp\n\n# comment")
	if err != nil {
		t.Fatalf("parsePorts: %v", err)
	}
	if len(ok) != 2 {
		t.Fatalf("got %d mappings, want 2", len(ok))
	}
	if ok[0].HostPort != 8080 || ok[0].GuestPort != 80 || ok[0].Protocol != "tcp" {
		t.Errorf("unexpected first mapping: %+v", ok[0])
	}
	if ok[1].Protocol != "udp" {
		t.Errorf("protocol not parsed: %+v", ok[1])
	}
	for _, bad := range []string{"8080", "abc:80", "8080:99999", "8080:80/sctp"} {
		if _, err := parsePorts(bad); err == nil {
			t.Errorf("parsePorts(%q) should have failed", bad)
		}
	}
}

func TestParseMounts(t *testing.T) {
	got, err := parseMounts("data:/data\ncache:/var/cache:ro")
	if err != nil {
		t.Fatalf("parseMounts: %v", err)
	}
	if len(got) != 2 || got[0].Volume != "data" || got[0].GuestPath != "/data" {
		t.Fatalf("unexpected mounts: %+v", got)
	}
	if !got[1].Readonly {
		t.Error("':ro' suffix should mark the mount read-only")
	}
	for _, bad := range []string{"data", "data:relative/path"} {
		if _, err := parseMounts(bad); err == nil {
			t.Errorf("parseMounts(%q) should have failed", bad)
		}
	}
}

func TestParseSecrets(t *testing.T) {
	got := parseSecrets("TOKEN=abc\n\nOTHER=def")
	if len(got) != 2 {
		t.Fatalf("got %d secrets, want 2", len(got))
	}
}

func TestPruneSummaryReportsNumbers(t *testing.T) {
	bytes := uint64(1024 * 1024)
	got := pruneSummary(&core.ImagePruneReport{
		ImageRefsRemoved: 2, LayersRemoved: 1, BytesReclaimed: &bytes,
	})
	for _, want := range []string{"2 image refs", "1 layer", "1.0 MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("prune summary %q missing %q", got, want)
		}
	}
	if got := pruneSummary(nil); !strings.Contains(got, "Nothing") {
		t.Errorf("nil report = %q", got)
	}
}

func TestCrumbsFor(t *testing.T) {
	got := crumbsFor("/a/b")
	if len(got) != 3 || got[0].Path != "/" || got[2].Path != "/a/b" {
		t.Fatalf("crumbsFor(/a/b) = %+v", got)
	}
	if len(crumbsFor("/")) != 1 {
		t.Error("root should produce a single crumb")
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename("a\"b\nc"); got != "abc" {
		t.Errorf("safeFilename = %q", got)
	}
	if got := safeFilename(""); got != "download" {
		t.Errorf("empty name = %q", got)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

// TestUnknownSandboxIs404 — an unknown sandbox page must report 404, not 500,
// and must still render the shell so the user can navigate away.
func TestUnknownSandboxIs404(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/dashboard/sandboxes/sbx_does_not_exist")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(body, "not available") {
		t.Error("error page should explain what went wrong")
	}
	if !strings.Contains(body, `href="/dashboard/sandboxes"`) {
		t.Error("error page should keep the nav so the user isn't stranded")
	}
}

// TestImagePruneRouteIsMounted is the regression test for a dead Prune button:
// the view used to POST to an unmounted "prune-preview" route, which 404'd.
func TestImagePruneRouteIsMounted(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/api/images/prune", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("prune route is not mounted")
	}
}

// TestImagePullAcceptsRowAction covers the one-click Re-pull entry point, where
// the reference arrives as a query param rather than a Datastar signal.
func TestImagePullAcceptsRowAction(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/api/images/pull?ref=alpine%3A3&force=1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE stream)", resp.StatusCode)
	}
	// Either progress or an error toast — but never the "reference required"
	// complaint, which would mean the query param was ignored.
	if strings.Contains(body, "Reference required") {
		t.Error("row action reference was ignored")
	}
}

// TestImagePullRequiresReference keeps the empty-input path friendly.
func TestImagePullRequiresReference(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/api/images/pull", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if !strings.Contains(body, "Reference required") {
		t.Error("empty pull should report an inline error, not silently do nothing")
	}
}

// TestPullErrorHint — registry errors are the most common pull failure and the
// raw SDK text is unactionable, so every recognised class must get guidance.
func TestPullErrorHint(t *testing.T) {
	cases := map[string]string{
		// Docker Hub's answer for typo / private / rate-limited alike.
		"pull alpine:3.19: image error: registry error: Not authorized: url https://index.docker.io/...": "rate limited",
		"manifest unknown":                        "does not exist",
		"context deadline exceeded":               "time budget",
		"dial tcp: lookup registry: no such host": "unreachable",
		"write /var/lib: no space left on device": "Prune",
	}
	for errMsg, want := range cases {
		got := pullErrorHint(errMsg)
		if got == "" {
			t.Errorf("no hint for %q", errMsg)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("hint for %q = %q, want it to mention %q", errMsg, got, want)
		}
	}
	if pullErrorHint("something totally unexpected") != "" {
		t.Error("unknown errors should not get a bogus hint")
	}
}

// TestNotifyPullOutcome covers the three things a user actually wants to know
// after a pull. The middle case is the whole reason force re-pull exists: for a
// moving tag, "did this fetch anything new?" is only answerable by comparing
// the manifest digest before and after.
func TestNotifyPullOutcome(t *testing.T) {
	size := int64(5 << 20)
	img := func(digest string) *core.Image {
		return &core.Image{Reference: "alpine:latest", ManifestDigest: digest, SizeBytes: &size}
	}

	cases := []struct {
		name        string
		img         *core.Image
		priorDigest string
		wasCached   bool
		want        string
	}{
		{"fresh pull", img("sha256:aaa"), "", false, "Image pulled"},
		{"digest moved", img("sha256:bbb"), "sha256:aaa", true, "Image updated"},
		{"unchanged", img("sha256:aaa"), "sha256:aaa", true, "Already up to date"},
	}

	h := New(core.NewService(core.Opts{}), Config{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			sse := datastar.NewSSE(rec, req)
			h.notifyPullOutcome(sse, c.img, c.priorDigest, c.wasCached, 3*time.Second)

			body := rec.Body.String()
			if !strings.Contains(body, c.want) {
				t.Errorf("outcome = %q, want it to say %q", firstLine(body), c.want)
			}
			if c.name == "digest moved" && !strings.Contains(body, "sha256:aaa") {
				t.Error("an updated image should show the digest it moved from")
			}
		})
	}
}

func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if strings.Contains(l, "Image") || strings.Contains(l, "up to date") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(s)
}
