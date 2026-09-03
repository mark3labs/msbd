package dashboard

// auth.go — who is driving the dashboard.
//
// Three modes, resolved per request so the CLI can change things under a
// running server:
//
//	open     no store users and no --dashboard-user/pass → everything allowed
//	         (dev only; the daemon logs a loud warning at boot)
//	basic    no store users but --dashboard-user/pass set → HTTP Basic, exactly
//	         the pre-store behaviour, kept so no existing deployment breaks
//	session  at least one store user exists → login page + server-side session
//	         cookie. This mode WINS over basic: creating a real account is an
//	         explicit upgrade, and silently continuing to honour a stale env
//	         password afterwards would be a surprise.
//
// The session cookie holds an opaque random id, not a signed claim: revocation
// is a DELETE, with no key rotation, no clock skew and no JWT footguns.

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/msbd/internal/store"
)

// sessionCookie is the cookie carrying the dashboard session id. It is scoped
// to /dashboard so it is never sent to the REST API, which authenticates with
// bearer tokens and must not be reachable with an ambient browser credential.
const sessionCookie = "msbd_session"

// userCountTTL bounds how stale the "are there any accounts?" answer can be.
// It only matters in the seconds after `msbd users add` creates the first user.
const userCountTTL = 3 * time.Second

type authMode int

const (
	modeOpen authMode = iota
	modeBasic
	modeSession
)

// identity is the authenticated principal for a request.
type identity struct {
	// Name is the display name; empty in open mode.
	Name string
	// Role is store.RoleAdmin or store.RoleViewer.
	Role string
	// SessionID is set only in session mode (used to sign out).
	SessionID string
	// Mode records how the principal was authenticated.
	Mode authMode
}

// IsAdmin reports whether this principal may mutate state and manage accounts.
// Open and basic modes are unrestricted by construction: they have no notion of
// roles, and inventing one would break the existing single-password setup.
func (i identity) IsAdmin() bool { return i.Role != store.RoleViewer }

// Anonymous reports whether nobody actually signed in (open mode).
func (i identity) Anonymous() bool { return i.Mode == modeOpen }

type ctxKey struct{}

// withIdentity attaches the principal to the request context.
func withIdentity(r *http.Request, id identity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
}

// identityOf returns the principal for a request. A request that never passed
// through a guard reports an unrestricted open-mode identity, which is correct:
// the only such routes are the unauthenticated ones (login, assets).
func identityOf(r *http.Request) identity { return identityFromContext(r.Context()) }

// identityFromContext is the ctx-only form, used by the view-model builders
// that only receive a context.
func identityFromContext(ctx context.Context) identity {
	if id, ok := ctx.Value(ctxKey{}).(identity); ok {
		return id
	}
	return identity{Mode: modeOpen, Role: store.RoleAdmin}
}

// ---------------------------------------------------------------------------
// mode resolution
// ---------------------------------------------------------------------------

// userCounter memoises how many accounts exist, so the mode check on every
// request is a mutex and a comparison rather than a query.
type userCounter struct {
	mu    sync.Mutex
	n     int
	until time.Time
	known bool
}

// count returns the cached account count, refreshing it when stale. On a query
// error it keeps serving the last known value rather than flipping the whole
// dashboard to a different auth mode because of a transient database problem.
func (c *userCounter) count(ctx context.Context, st *store.Store) int {
	if st == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.known && now.Before(c.until) {
		return c.n
	}
	n, err := st.CountUsers(ctx)
	if err != nil {
		if c.known {
			return c.n
		}
		return 0
	}
	c.n, c.known, c.until = n, true, now.Add(userCountTTL)
	return n
}

// invalidate forces the next count() to re-query (after adding/removing a user).
func (c *userCounter) invalidate() {
	c.mu.Lock()
	c.known = false
	c.mu.Unlock()
}

// mode resolves the active authentication mode for this request.
func (h *Handler) mode(ctx context.Context) authMode {
	if h.users.count(ctx, h.store) > 0 {
		return modeSession
	}
	if h.cfg.BasicAuthEnabled() {
		return modeBasic
	}
	return modeOpen
}

// locked reports whether the dashboard must refuse to serve: it has NO auth of
// its own while the REST API DOES require a bearer token. Serving it then would
// hand every visitor full sandbox control, neatly bypassing the token.
//
// This is evaluated per request rather than at boot precisely so the fix works
// without a restart: `msbd users add <name>` creates an account, the mode flips
// to session, and the dashboard unlocks on the next page load.
func (h *Handler) locked(ctx context.Context) bool {
	if h.cfg.AllowInsecure || h.mode(ctx) != modeOpen {
		return false
	}
	return h.cfg.APIKeyConfigured || h.cfg.KeyCache.AuthConfigured(ctx)
}

// ---------------------------------------------------------------------------
// guards
// ---------------------------------------------------------------------------

// authenticate resolves the principal for a request, or reports why it failed.
func (h *Handler) authenticate(r *http.Request) (identity, bool) {
	switch h.mode(r.Context()) {
	case modeOpen:
		return identity{Mode: modeOpen, Role: store.RoleAdmin}, true

	case modeBasic:
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.User)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.Pass)) == 1
		if ok && userOK && passOK {
			return identity{Name: user, Role: store.RoleAdmin, Mode: modeBasic}, true
		}
		return identity{}, false

	default: // modeSession
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			return identity{}, false
		}
		sess, u, err := h.store.LookupSession(r.Context(), c.Value)
		if err != nil {
			return identity{}, false
		}
		return identity{Name: u.Username, Role: u.Role, SessionID: sess.ID, Mode: modeSession}, true
	}
}

// guardPage protects a full HTML page. An unauthenticated request is redirected
// to the login form (remembering where it was headed) in session mode, or
// challenged with Basic auth in basic mode — a 401 body would be a dead end for
// someone who simply has not signed in yet.
func (h *Handler) guardPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.locked(r.Context()) {
			h.lockedPage(w, r)
			return
		}
		id, ok := h.authenticate(r)
		if ok {
			next(w, withIdentity(r, id))
			return
		}
		if h.mode(r.Context()) == modeSession {
			http.Redirect(w, r, "/dashboard/login?next="+url.QueryEscape(r.URL.RequestURI()),
				http.StatusSeeOther)
			return
		}
		h.challengeBasic(w)
	}
}

// guardAPI protects an SSE fragment or action endpoint. Failure is a status
// code, never a redirect: Datastar would follow a 303 and patch the login page
// into whatever element it was updating.
func (h *Handler) guardAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.locked(r.Context()) {
			http.Error(w, "dashboard is locked: configure dashboard auth", http.StatusForbidden)
			return
		}
		id, ok := h.authenticate(r)
		if !ok {
			if h.mode(r.Context()) == modeSession {
				// Tell the browser to leave the stale page rather than sit on a
				// dashboard whose every update silently 401s.
				w.Header().Set("X-Msbd-Login", "/dashboard/login")
				http.Error(w, "session expired", http.StatusUnauthorized)
				return
			}
			h.challengeBasic(w)
			return
		}
		if !h.sameOrigin(r) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next(w, withIdentity(r, id))
	}
}

// guardWrite is guardAPI plus a role check: viewers may look at everything the
// dashboard shows but may not change anything.
func (h *Handler) guardWrite(next http.HandlerFunc) http.HandlerFunc {
	return h.guardAPI(func(w http.ResponseWriter, r *http.Request) {
		if !identityOf(r).IsAdmin() {
			http.Error(w, "read-only account", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// guardAdminPage is guardPage plus a role check, for the Settings section.
func (h *Handler) guardAdminPage(next http.HandlerFunc) http.HandlerFunc {
	return h.guardPage(func(w http.ResponseWriter, r *http.Request) {
		if !identityOf(r).IsAdmin() {
			h.forbiddenPage(w, r)
			return
		}
		next(w, r)
	})
}

func (h *Handler) challengeBasic(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="msbd dashboard", charset="UTF-8"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// sameOrigin is belt-and-braces CSRF defence for cookie-authenticated mutations.
// SameSite=Lax already blocks cross-site POSTs in every current browser; this
// also rejects a request whose Origin was set by something else. A request with
// no Origin header (curl, same-origin GET) is allowed through — requiring one
// would break scripted use of the dashboard endpoints.
func (h *Handler) sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// ---------------------------------------------------------------------------
// session cookies
// ---------------------------------------------------------------------------

// setSessionCookie writes the login cookie for a freshly created session.
func (h *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.ID,
		Path:     "/dashboard",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie expires the login cookie.
func (h *Handler) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/dashboard",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// isHTTPS reports whether the request reached us over TLS, directly or through
// a terminating reverse proxy. Getting this wrong in the safe direction (no
// Secure flag on a plain-HTTP dev server) is required, or the cookie would be
// dropped and login would appear to silently fail.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// safeNext sanitises the ?next= redirect target so the login form can't be used
// as an open redirect. Only absolute paths inside /dashboard are honoured.
func safeNext(raw string) string {
	const fallback = "/dashboard"
	if raw == "" {
		return fallback
	}
	// Reject anything that could be read as scheme-relative ("//evil.example")
	// or absolute-with-host before parsing.
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return fallback
	}
	if u.Path != "/dashboard" && !strings.HasPrefix(u.Path, "/dashboard/") {
		return fallback
	}
	// Never bounce straight back to the login page.
	if strings.HasPrefix(u.Path, "/dashboard/login") {
		return fallback
	}
	return u.RequestURI()
}

// errInvalidLogin is the single message every failed sign-in produces, so the
// form cannot be used to discover which usernames exist.
var errInvalidLogin = errors.New("incorrect username or password")
