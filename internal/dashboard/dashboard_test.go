package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		"msbd dashboard",
		"/dashboard/assets/css/output.css",
		"/dashboard/assets/vendor/datastar.js",
		"data-init=",
		"/dashboard/api/sandboxes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

func TestStaticAssets(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	for _, path := range []string{
		"/dashboard/assets/css/output.css",
		"/dashboard/assets/vendor/datastar.js",
		"/dashboard/assets/vendor/xterm.js",
		"/dashboard/assets/js/metric-chart.js",
		"/dashboard/assets/js/dialog.min.js",
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
