package dashboard

// auth_test.go — the session-auth surface: login, guards, roles, and the
// interaction between the three auth modes.

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/msbd/internal/store"
)

// client returns an HTTP client that keeps cookies and does NOT auto-follow
// redirects, so tests can assert on the 303 itself.
func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// login performs the Datastar sign-in POST and returns the response.
func login(t *testing.T, c *http.Client, base, user, pass string) *http.Response {
	t.Helper()
	body := `{"loginuser":"` + user + `","loginpass":"` + pass + `","loginnext":"/dashboard"}`
	req, _ := http.NewRequest(http.MethodPost, base+"/dashboard/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestNoUsersMeansNoLoginPage — with an empty store the dashboard keeps its
// previous behaviour (open or Basic); a login form would be a dead end because
// there is no account to type in.
func TestNoUsersMeansNoLoginPage(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	c := client(t)
	resp, err := c.Get(ts.URL + "/dashboard/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("login page with no users = %d, want 303 redirect", resp.StatusCode)
	}

	// And the dashboard itself stays open.
	resp2, err := c.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("dashboard with an empty store = %d, want 200", resp2.StatusCode)
	}
}

// TestCreatingAUserEnablesLogin is the mode switch: the first account turns the
// whole dashboard from open to session-authenticated, with no restart.
func TestCreatingAUserEnablesLogin(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	c := client(t)
	resp, err := c.Get(ts.URL + "/dashboard/sandboxes")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("page without a session = %d, want 303 to login", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/dashboard/login") {
		t.Fatalf("redirect went to %q, want the login page", loc)
	}
	// The original destination must survive the detour.
	if !strings.Contains(loc, url.QueryEscape("/dashboard/sandboxes")) {
		t.Errorf("redirect %q lost the requested page", loc)
	}
}

func TestLoginFlow(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	c := client(t)

	// Wrong password: no cookie, and a message that reveals nothing.
	bad := login(t, c, ts.URL, "alice", "nope")
	badBody := readAll(t, bad)
	_ = bad.Body.Close()
	if strings.Contains(badBody, "datastar-patch-elements") && !strings.Contains(badBody, "incorrect username or password") {
		t.Errorf("failed login should render the generic error, got: %s", badBody)
	}
	if hasSessionCookie(bad) {
		t.Error("failed login set a session cookie")
	}

	// Right password: cookie + redirect instruction.
	ok := login(t, c, ts.URL, "alice", "correct horse")
	okBody := readAll(t, ok)
	_ = ok.Body.Close()
	if !hasSessionCookie(ok) {
		t.Fatal("successful login did not set a session cookie")
	}
	if !strings.Contains(okBody, "/dashboard") {
		t.Errorf("successful login should redirect, got: %s", okBody)
	}

	// The cookie now unlocks pages.
	resp, err := c.Get(ts.URL + "/dashboard/sandboxes")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated page = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "alice") {
		t.Error("shell should show who is signed in")
	}

	// Sign out invalidates it.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/logout", nil)
	out, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = out.Body.Close()

	after, err := c.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	_ = after.Body.Close()
	if after.StatusCode != http.StatusSeeOther {
		t.Errorf("after sign-out = %d, want 303 to login", after.StatusCode)
	}
}

// TestSessionCookieIsHardened pins the flags that make the cookie useless to an
// attacker: unreadable from JS and not sent on cross-site navigation.
func TestSessionCookieIsHardened(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	resp := login(t, client(t), ts.URL, "alice", "correct horse")
	defer func() { _ = resp.Body.Close() }()

	var got *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			got = ck
		}
	}
	if got == nil {
		t.Fatal("no session cookie")
	}
	if !got.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", got.SameSite)
	}
	// Scoped to /dashboard so it is never sent to the bearer-authenticated API.
	if got.Path != "/dashboard" {
		t.Errorf("cookie path = %q, want /dashboard", got.Path)
	}
}

// TestSessionModeBeatsBasicAuth — once a real account exists, a stale
// --dashboard-pass must not remain a second way in.
func TestSessionModeBeatsBasicAuth(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true, User: "admin", Pass: "legacy"}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/dashboard", nil)
	req.SetBasicAuth("admin", "legacy")
	resp, err := client(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("legacy Basic credentials still work after an account was created")
	}
}

// TestViewerCannotMutate is the role boundary. A viewer sees the dashboard but
// every state-changing endpoint refuses them server-side — hiding the buttons
// is a courtesy, this is the enforcement.
func TestViewerCannotMutate(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "root", "rootrootroot", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(t.Context(), "view", "viewviewview", store.RoleViewer); err != nil {
		t.Fatal(err)
	}

	c := client(t)
	resp := login(t, c, ts.URL, "view", "viewviewview")
	_ = resp.Body.Close()

	// Reads are allowed.
	for _, path := range []string{"/dashboard", "/dashboard/sandboxes", "/dashboard/api/sandboxes/table"} {
		r, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("viewer GET %s = %d, want 200", path, r.StatusCode)
		}
	}

	// Writes are not.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/dashboard/api/sandboxes"},
		{http.MethodDelete, "/dashboard/api/sandboxes/x"},
		{http.MethodPost, "/dashboard/api/sandboxes/x/run"},
		{http.MethodPost, "/dashboard/api/volumes"},
		{http.MethodPost, "/dashboard/api/images/pull"},
		{http.MethodPost, "/dashboard/api/users"},
		{http.MethodPost, "/dashboard/api/keys"},
		{http.MethodPost, "/dashboard/api/sandboxes/x/terminal-ticket"},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d, want 403", tc.method, tc.path, r.StatusCode)
		}
	}

	// Settings pages are admin-only and must explain themselves, not 404.
	for _, path := range []string{"/dashboard/settings/users", "/dashboard/settings/keys"} {
		r, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, r)
		_ = r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Errorf("viewer GET %s = %d, want 403", path, r.StatusCode)
		}
		if !strings.Contains(body, "Administrator access required") {
			t.Errorf("GET %s should explain the refusal", path)
		}
	}
}

// TestAdminSeesSettingsNav — the Settings section only exists when there is a
// store to manage and the account may manage it.
func TestAdminSeesSettingsNav(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	resp := login(t, c, ts.URL, "alice", "correct horse")
	_ = resp.Body.Close()

	page, err := c.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, page)
	_ = page.Body.Close()
	for _, want := range []string{
		`href="/dashboard/settings/keys"`,
		`href="/dashboard/settings/users"`,
		"Sign out",
		"Change password",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin shell missing %q", want)
		}
	}
}

// TestNoStoreHidesSettings — a dashboard without a store must not advertise
// (or mount) routes it cannot serve.
func TestNoStoreHidesSettings(t *testing.T) {
	ts := newTestServer(Config{Enabled: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if strings.Contains(body, "/dashboard/settings/") {
		t.Error("settings nav shown without a state store")
	}

	r, err := http.Get(ts.URL + "/dashboard/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("settings route without a store = %d, want 404", r.StatusCode)
	}
}

// TestSettingsRoutesAreMounted keeps the admin surface wired up.
func TestSettingsRoutesAreMounted(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/dashboard/settings/keys"},
		{http.MethodGet, "/dashboard/settings/users"},
		{http.MethodGet, "/dashboard/api/keys/table"},
		{http.MethodPost, "/dashboard/api/keys"},
		{http.MethodPost, "/dashboard/api/keys/1/revoke"},
		{http.MethodDelete, "/dashboard/api/keys/1"},
		{http.MethodGet, "/dashboard/api/users/table"},
		{http.MethodPost, "/dashboard/api/users"},
		{http.MethodPost, "/dashboard/api/users/password"},
		{http.MethodPost, "/dashboard/api/users/alice/role"},
		{http.MethodDelete, "/dashboard/api/users/alice"},
		{http.MethodPost, "/dashboard/api/account/password"},
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

// TestKeyCreateShowsTokenOnce covers the one path where a secret reaches the
// browser: the token must appear in the reveal dialog and nowhere else.
func TestKeyCreateShowsTokenOnce(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/api/keys",
		strings.NewReader(`{"keyname":"ci","keyexpires":"30d"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()

	if !strings.Contains(body, store.TokenPrefix) {
		t.Fatalf("create-key response did not reveal a token: %s", body)
	}
	if !strings.Contains(body, "new-key") {
		t.Error("the reveal dialog was not patched in")
	}

	// It is a real, working key.
	keys, err := st.ListAPIKeys(t.Context())
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	if keys[0].ExpiresAt.IsZero() {
		t.Error("--expires 30d was ignored")
	}

	// The table refresh must NOT contain the token again.
	tbl, err := http.Get(ts.URL + "/dashboard/api/keys/table")
	if err != nil {
		t.Fatal(err)
	}
	tblBody := readAll(t, tbl)
	_ = tbl.Body.Close()
	if strings.Contains(tblBody, store.TokenPrefix) && strings.Contains(tblBody, keys[0].Prefix) {
		// The prefix IS shown (that is the point); the full token must not be.
		if len(tblBody) > 0 && strings.Contains(tblBody, "…") == false {
			t.Error("key table appears to render the full token")
		}
	}
}

// TestCrossOriginMutationRefused is the CSRF backstop behind SameSite=Lax.
func TestCrossOriginMutationRefused(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/dashboard/api/keys", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
}

// TestAssetsAreUnauthenticated — the login page needs its stylesheet before
// anyone has signed in.
func TestAssetsAreUnauthenticated(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true, User: "admin", Pass: "secret"}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/dashboard/assets/css/output.css")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("stylesheet = %d, want 200 (the login page needs it)", resp.StatusCode)
	}
}

// TestLoginPageRenders checks the standalone document does not depend on the
// guarded shell.
func TestLoginPageRenders(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true, Version: "test"}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/dashboard/login")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login page = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		"Sign in · msbd",
		"/dashboard/assets/css/output.css",
		"/dashboard/login",
		"no-store",
	} {
		if want == "no-store" {
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Error("login page must not be cached")
			}
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}
	// The nav would be a wall of links that all bounce back here.
	if strings.Contains(body, `href="/dashboard/sandboxes"`) {
		t.Error("login page should not render the app shell")
	}
}

// TestSafeNext is the open-redirect guard on the post-login bounce.
func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "/dashboard",
		"/dashboard/sandboxes":     "/dashboard/sandboxes",
		"/dashboard/images?sort=x": "/dashboard/images?sort=x",
		"//evil.example/":          "/dashboard",
		"https://evil.example/x":   "/dashboard",
		"/etc/passwd":              "/dashboard",
		"/dashboardevil":           "/dashboard",
		"/dashboard/login?next=/x": "/dashboard",
		"javascript:alert(1)":      "/dashboard",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func hasSessionCookie(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return true
		}
	}
	return false
}
