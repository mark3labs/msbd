package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/store"
)

// newTestServer builds a dashboard with no state store: the legacy Basic-auth /
// open behaviour, which most of these tests exercise.
func newTestServer(cfg Config) *httptest.Server {
	return newTestServerWithStore(cfg, nil)
}

func newTestServerWithStore(cfg Config, st *store.Store) *httptest.Server {
	svc := core.NewService(core.Opts{DefaultImage: "microsandbox/python"})
	mux := http.NewServeMux()
	New(svc, cfg, st).Mount(mux)
	return httptest.NewServer(mux)
}

// newTestStore opens an in-memory store for the session-auth tests.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.MemoryPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestIndexRenders(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
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
		"/assets/css/output.css",
		"/assets/vendor/datastar.js",
		`href="/sandboxes"`,
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
		"/":          "Overview · msbd",
		"/sandboxes": "Sandboxes · msbd",
		"/volumes":   "Volumes · msbd",
		"/images":    "Images · msbd",
		"/snapshots": "Snapshots · msbd",
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
		"/assets/css/output.css",
		"/assets/favicon.svg",
		"/assets/vendor/datastar.js",
		"/assets/vendor/xterm.js",
		"/assets/js/metric-chart.js",
		"/assets/js/dialog.min.js",
		"/assets/js/tabs.min.js",
		"/assets/js/progress.min.js",
		"/assets/js/copybutton.min.js",
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
	resp, err := http.Get(ts.URL + "/")
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
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
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
	req3, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
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
		{http.MethodGet, "/ui/overview"},
		{http.MethodGet, "/ui/sandboxes/table"},
		{http.MethodGet, "/ui/volumes/table"},
		{http.MethodGet, "/ui/images/table"},
		{http.MethodGet, "/ui/snapshots/table"},
		{http.MethodGet, "/ui/images/inspect?ref=alpine"},
		{http.MethodPost, "/ui/sandboxes/x/jobs/y/cancel"},
		{http.MethodPost, "/ui/sandboxes/x/files"},
		{http.MethodGet, "/ui/sandboxes/x/files/view?path=/etc"},
		{http.MethodPost, "/ui/sandboxes/x/files/mkdir"},
		{http.MethodPost, "/ui/snapshots/x/verify"},
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

func TestConfigBasicAuthEnabled(t *testing.T) {
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
		if got := c.cfg.BasicAuthEnabled(); got != c.want {
			t.Errorf("BasicAuthEnabled(%+v) = %v, want %v", c.cfg, got, c.want)
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

	resp, err := http.Get(ts.URL + "/sandboxes/sbx_does_not_exist")
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
	if !strings.Contains(body, `href="/sandboxes"`) {
		t.Error("error page should keep the nav so the user isn't stranded")
	}
}

// TestImagePruneRouteIsMounted is the regression test for a dead Prune button:
// the view used to POST to an unmounted "prune-preview" route, which 404'd.
func TestImagePruneRouteIsMounted(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/images/prune", nil)
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

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/images/pull?ref=alpine%3A3&force=1", nil)
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

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/images/pull", nil)
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

	h := New(core.NewService(core.Opts{}), Config{}, nil)
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

// TestConcurrentRendersAreRaceFree renders pages and SSE fragments from many
// goroutines at once. It is the end-to-end guard for the data race that broke
// CI: templui components merge Tailwind classes on whatever goroutine is
// serving the request, and the upstream tailwind-merge-go global is not
// goroutine-safe (see internal/dashboard/twmerge). Meaningful under -race.
func TestConcurrentRendersAreRaceFree(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	paths := []string{
		"/",
		"/sandboxes",
		"/volumes",
		"/images",
		"/snapshots",
		"/ui/overview",
		"/ui/sandboxes/table",
		"/ui/volumes/table",
		"/ui/images/table",
		"/ui/snapshots/table",
	}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range paths {
				// Stagger the starting path so goroutines hit different
				// handlers simultaneously instead of marching in lockstep.
				p := paths[(i+g)%len(paths)]
				resp, err := http.Get(ts.URL + p)
				if err != nil {
					t.Errorf("GET %s: %v", p, err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode >= 500 {
					t.Errorf("GET %s = %d", p, resp.StatusCode)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestDashboardNeverShadowsTheAPI is the structural guard for the shared mux.
// The dashboard owns the root of the URL space, so it is one careless
// mux.HandleFunc away from swallowing a REST route. ServeMux.Handler reports
// which pattern (if any) a request matches, so mounting the dashboard ALONE
// and asking it about API paths proves the dashboard claims none of them.
//
// Two invariants: nothing under /api/ (that prefix is the versioned REST API),
// and no bare "/" catch-all — a catch-all matches every unmatched request and
// would flatten ServeMux's 405 Method Not Allowed into a 404 API-wide.
func TestDashboardNeverShadowsTheAPI(t *testing.T) {
	mux := http.NewServeMux()
	New(nil, Config{Enabled: true}, newTestStore(t)).Mount(mux)

	// Every shape the api package registers, plus the unversioned ops routes.
	foreign := [][2]string{
		{http.MethodGet, "/api/v1/version"},
		{http.MethodPost, "/api/v1/sandboxes"},
		{http.MethodGet, "/api/v1/sandboxes"},
		{http.MethodGet, "/api/v1/sandboxes/sbx_1"},
		{http.MethodPut, "/api/v1/sandboxes"}, // wrong method: must still not match
		{http.MethodPost, "/api/v1/sandboxes/sbx_1/exec"},
		{http.MethodGet, "/api/v1/sandboxes/sbx_1/terminal"},
		{http.MethodGet, "/api/v1/volumes"},
		{http.MethodGet, "/api/v1/images"},
		{http.MethodGet, "/api/v1/snapshots"},
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/metrics"},
		{http.MethodGet, "/docs"},
		{http.MethodGet, "/openapi.yaml"},
		// A path the dashboard has no page for must stay unclaimed, or a
		// mistyped API route would render HTML instead of 404ing.
		{http.MethodGet, "/definitely/not/a/route"},
	}
	for _, f := range foreign {
		method, path := f[0], f[1]
		_, pattern := mux.Handler(httptest.NewRequest(method, path, nil))
		if pattern != "" {
			t.Errorf("dashboard claims %s %s via pattern %q — it must not shadow the REST API",
				method, path, pattern)
		}
	}

	// Sanity: the recogniser works, i.e. the dashboard's own pages DO match.
	for _, path := range []string{"/", "/sandboxes", "/settings/keys", "/ui/overview"} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern == "" {
			t.Errorf("dashboard route %s is not registered", path)
		}
	}
}

// assetRefRe finds every local asset URL a rendered page pulls in: the
// stylesheet, the Datastar runtime, and the per-component templui scripts
// (whose base path lives in the vendored utils/templui.go and is regenerated
// from .templui.json).
var assetRefRe = regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)`)

// TestRenderedAssetURLsResolve walks the asset URLs the shell actually emits
// and fetches each one. Moving the dashboard to the root moved /dashboard/assets
// to /assets, and a stale base path is invisible in the browser — a 404 on a
// <script> tag reports nothing, the component is just silently inert.
func TestRenderedAssetURLsResolve(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()

	seen := map[string]bool{}
	for _, m := range assetRefRe.FindAllStringSubmatch(body, -1) {
		seen[m[1]] = true
	}
	// The page must reference the stylesheet, the Datastar runtime AND at
	// least one templui component script, or this test proves nothing.
	for _, want := range []string{"/assets/css/output.css", "/assets/vendor/datastar.js"} {
		if !seen[want] {
			t.Errorf("rendered page does not reference %s", want)
		}
	}
	// templui components ship as *.min.js under componentScriptBasePath. Match
	// on ".min.js" specifically: layout.templ also hardcodes /assets/js/
	// metric-chart.js, so a plain "/assets/js/" prefix count would stay
	// non-zero even with a stale component base path.
	componentJS := 0
	for u := range seen {
		if strings.HasPrefix(u, "/assets/js/") && strings.HasSuffix(u, ".min.js") {
			componentJS++
		}
	}
	if componentJS == 0 {
		t.Error("rendered page references no /assets/js/*.min.js templui script — " +
			"check componentScriptBasePath in internal/dashboard/utils/templui.go " +
			"and jsPublicPath in .templui.json")
	}

	for u := range seen {
		r, err := http.Get(ts.URL + u)
		if err != nil {
			t.Errorf("GET %s: %v", u, err)
			continue
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (asset referenced but not served)", u, r.StatusCode)
		}
	}
}
