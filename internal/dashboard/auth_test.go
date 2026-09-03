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
	body := `{"loginuser":"` + user + `","loginpass":"` + pass + `","loginnext":"/"}`
	req, _ := http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(body))
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
	resp, err := c.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("login page with no users = %d, want 303 redirect", resp.StatusCode)
	}

	// And the dashboard itself stays open.
	resp2, err := c.Get(ts.URL + "/")
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
	resp, err := c.Get(ts.URL + "/sandboxes")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("page without a session = %d, want 303 to login", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirect went to %q, want the login page", loc)
	}
	// The original destination must survive the detour.
	if !strings.Contains(loc, url.QueryEscape("/sandboxes")) {
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
	if !strings.Contains(okBody, `"/"`) {
		t.Errorf("successful login should redirect, got: %s", okBody)
	}

	// The cookie now unlocks pages.
	resp, err := c.Get(ts.URL + "/sandboxes")
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
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/logout", nil)
	out, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = out.Body.Close()

	after, err := c.Get(ts.URL + "/")
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
	// Scoped to "/" now that the dashboard owns the root of the URL space.
	// Safe because the REST API under /api/v1 reads the Authorization header
	// only and never consults a cookie.
	if got.Path != "/" {
		t.Errorf("cookie path = %q, want /", got.Path)
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

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
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
	for _, path := range []string{"/", "/sandboxes", "/ui/sandboxes/table"} {
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
		{http.MethodPost, "/ui/sandboxes"},
		{http.MethodDelete, "/ui/sandboxes/x"},
		{http.MethodPost, "/ui/sandboxes/x/run"},
		{http.MethodPost, "/ui/volumes"},
		{http.MethodPost, "/ui/images/pull"},
		{http.MethodPost, "/ui/users"},
		{http.MethodPost, "/ui/keys"},
		{http.MethodPost, "/ui/sandboxes/x/terminal-ticket"},
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
	for _, path := range []string{"/settings/users", "/settings/keys"} {
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

	page, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, page)
	_ = page.Body.Close()
	for _, want := range []string{
		`href="/settings/keys"`,
		`href="/settings/users"`,
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

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	_ = resp.Body.Close()
	if strings.Contains(body, "/settings/") {
		t.Error("settings nav shown without a state store")
	}

	r, err := http.Get(ts.URL + "/settings/users")
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
		{http.MethodGet, "/settings/keys"},
		{http.MethodGet, "/settings/users"},
		{http.MethodGet, "/ui/keys/table"},
		{http.MethodPost, "/ui/keys"},
		{http.MethodPost, "/ui/keys/1/revoke"},
		{http.MethodDelete, "/ui/keys/1"},
		{http.MethodGet, "/ui/users/table"},
		{http.MethodPost, "/ui/users"},
		{http.MethodPost, "/ui/users/password"},
		{http.MethodPost, "/ui/users/alice/role"},
		{http.MethodDelete, "/ui/users/alice"},
		{http.MethodPost, "/ui/account/password"},
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

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/keys",
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
	tbl, err := http.Get(ts.URL + "/ui/keys/table")
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
//
// The Sec-Fetch-Site cases are the reason this delegates to the stdlib's
// http.CrossOriginProtection: the hand-rolled Origin-vs-Host check this
// replaced ALLOWED a cross-site mutation that simply omitted Origin.
func TestCrossOriginMutationRefused(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"cross-site Origin", map[string]string{
			"Origin": "https://evil.example",
		}, http.StatusForbidden},
		{"cross-site Sec-Fetch-Site, no Origin", map[string]string{
			"Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden},
		{"same-site Sec-Fetch-Site, no Origin", map[string]string{
			"Sec-Fetch-Site": "same-site",
		}, http.StatusForbidden},
		{"same-origin Sec-Fetch-Site", map[string]string{
			"Sec-Fetch-Site": "same-origin",
		}, http.StatusOK},
		// curl and other non-browser clients send neither header and must keep
		// working: the dashboard endpoints are scripted against.
		{"no browser headers (curl)", nil, http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/keys", strings.NewReader(`{}`))
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("POST /ui/keys %v = %d, want %d", c.headers, resp.StatusCode, c.want)
			}
		})
	}
}

// TestAuthFormsRefuseCrossOrigin covers the two UNAUTHENTICATED state-changing
// endpoints. They take no session guard, so before guardForm they were the only
// mutations on the server with no CSRF defence: login-CSRF signs a victim into
// an attacker's account, logout-CSRF is drive-by sign-out.
//
// The same-origin and no-header rows are the ones that matter for regressions:
// the real login form and any scripted sign-in must keep working.
func TestAuthFormsRefuseCrossOrigin(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	headerCases := []struct {
		name    string
		headers map[string]string
		blocked bool
	}{
		{"cross-site Origin", map[string]string{"Origin": "https://evil.example"}, true},
		{"cross-site Sec-Fetch-Site", map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"same-site Sec-Fetch-Site", map[string]string{"Sec-Fetch-Site": "same-site"}, true},
		{"same-origin Sec-Fetch-Site", map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
		{"no browser headers (curl)", nil, false},
	}

	for _, path := range []string{"/login", "/logout"} {
		for _, c := range headerCases {
			t.Run(path+" "+c.name, func(t *testing.T) {
				body := `{"loginuser":"alice","loginpass":"correct horse","loginnext":"/"}`
				req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				for k, v := range c.headers {
					req.Header.Set(k, v)
				}
				resp, err := client(t).Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()

				if c.blocked {
					if resp.StatusCode != http.StatusForbidden {
						t.Errorf("POST %s %v = %d, want 403", path, c.headers, resp.StatusCode)
					}
					// A refused sign-in must not have handed out a session.
					if hasSessionCookie(resp) {
						t.Errorf("POST %s %v set a session cookie despite being refused",
							path, c.headers)
					}
					return
				}
				if resp.StatusCode == http.StatusForbidden {
					t.Errorf("POST %s %v = 403, want the request to be allowed through",
						path, c.headers)
					return
				}
				// Not merely "not 403": prove the endpoint still does its job
				// through the guard, or this row would pass vacuously against a
				// handler that had been broken some other way.
				if path == "/login" && !hasSessionCookie(resp) {
					t.Errorf("POST /login %v: allowed but no session cookie — sign-in did not complete",
						c.headers)
				}
				if path == "/logout" && resp.StatusCode != http.StatusSeeOther {
					t.Errorf("POST /logout %v = %d, want 303 to the login page",
						c.headers, resp.StatusCode)
				}
			})
		}
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

	resp, err := http.Get(ts.URL + "/assets/css/output.css")
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

	resp, err := http.Get(ts.URL + "/login")
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
		"/assets/css/output.css",
		"/login",
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
	if strings.Contains(body, `href="/sandboxes"`) {
		t.Error("login page should not render the app shell")
	}
}

// TestSafeNext is the open-redirect guard on the post-login bounce.
func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/",
		"/sandboxes":             "/sandboxes",
		"/images?sort=x":         "/images?sort=x",
		"/settings/keys":         "/settings/keys",
		"//evil.example/":        "/",
		"https://evil.example/x": "/",
		"javascript:alert(1)":    "/",
		// The API is JSON behind a bearer token: a dead end for a browser.
		"/api/v1/sandboxes": "/",
		// Never bounce back to the form we just came from.
		"/login?next=/x": "/",
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

// TestBrowserLoginFlowEndToEnd walks the whole sign-in journey with the exact
// header set a real browser sends at each step (Sec-Fetch-Site: none for a
// typed URL, same-origin for the Datastar fetch and the sign-out form POST).
//
// The cross-origin guards are only worth having if the legitimate flow still
// works, and every step here passes through one: guardPage, guardForm,
// guardAPI. A unit test of Check() cannot catch a guard wired onto the wrong
// route.
func TestBrowserLoginFlowEndToEnd(t *testing.T) {
	st := newTestStore(t)
	ts := newTestServerWithStore(Config{Enabled: true}, st)
	defer ts.Close()
	if _, err := st.CreateUser(t.Context(), "alice", "correct horse", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	origin := ts.URL

	// 1. Land on a protected page → redirected to login.
	r1, _ := http.NewRequest("GET", ts.URL+"/sandboxes", nil)
	r1.Header.Set("Sec-Fetch-Site", "none") // typed in the URL bar
	resp1, err := c.Do(r1)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp1.Body.Close()
	t.Logf("GET /sandboxes -> %d  Location=%s", resp1.StatusCode, resp1.Header.Get("Location"))
	if resp1.StatusCode != http.StatusSeeOther {
		t.Errorf("want 303 to login, got %d", resp1.StatusCode)
	}

	// 2. Datastar submits the login form via fetch(): same-origin.
	body := `{"loginuser":"alice","loginpass":"correct horse","loginnext":"/sandboxes"}`
	r2, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(body))
	r2.Header.Set("Content-Type", "application/json")
	r2.Header.Set("Origin", origin)
	r2.Header.Set("Sec-Fetch-Site", "same-origin")
	r2.Header.Set("Sec-Fetch-Mode", "cors")
	resp2, err := c.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	sse := readAll(t, resp2)
	_ = resp2.Body.Close()
	t.Logf("POST /login -> %d  cookie=%v", resp2.StatusCode, hasSessionCookie(resp2))
	if resp2.StatusCode != http.StatusOK || !hasSessionCookie(resp2) {
		t.Fatalf("login failed: %d %s", resp2.StatusCode, sse)
	}
	if !strings.Contains(sse, "/sandboxes") {
		t.Errorf("expected SSE redirect to /sandboxes, got %s", sse)
	}

	// 3. The page now loads with the session cookie.
	r3, _ := http.NewRequest("GET", ts.URL+"/sandboxes", nil)
	r3.Header.Set("Sec-Fetch-Site", "same-origin")
	resp3, err := c.Do(r3)
	if err != nil {
		t.Fatal(err)
	}
	page := readAll(t, resp3)
	_ = resp3.Body.Close()
	t.Logf("GET /sandboxes (signed in) -> %d", resp3.StatusCode)
	if resp3.StatusCode != http.StatusOK || !strings.Contains(page, "Sandboxes · msbd") {
		t.Errorf("signed-in page load failed: %d", resp3.StatusCode)
	}

	// 4. A Datastar SSE fragment fetch works (guardAPI + crossOrigin).
	r4, _ := http.NewRequest("GET", ts.URL+"/ui/sandboxes/table", nil)
	r4.Header.Set("Origin", origin)
	r4.Header.Set("Sec-Fetch-Site", "same-origin")
	resp4, err := c.Do(r4)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp4.Body.Close()
	t.Logf("GET /ui/sandboxes/table -> %d", resp4.StatusCode)
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("SSE fragment = %d, want 200", resp4.StatusCode)
	}

	// 5. Sign out: a plain same-origin form POST.
	r5, _ := http.NewRequest("POST", ts.URL+"/logout", nil)
	r5.Header.Set("Origin", origin)
	r5.Header.Set("Sec-Fetch-Site", "same-origin")
	r5.Header.Set("Sec-Fetch-Mode", "navigate")
	resp5, err := c.Do(r5)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp5.Body.Close()
	t.Logf("POST /logout -> %d  Location=%s", resp5.StatusCode, resp5.Header.Get("Location"))
	if resp5.StatusCode != http.StatusSeeOther {
		t.Errorf("logout = %d, want 303", resp5.StatusCode)
	}
}
