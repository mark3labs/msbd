package dashboard

// handlers_auth.go — the login form, sign-out, and self-service password change.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
	"github.com/mark3labs/msbd/internal/store"
)

// pageLogin renders the sign-in form. It is one of the two unauthenticated
// pages, so it must not touch anything a guard protects.
//
// When the dashboard is not in session mode there is nothing to sign into, and
// when the visitor already has a valid session there is nothing to do — both
// bounce to the dashboard rather than showing a form that cannot help.
func (h *Handler) pageLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if h.mode(r.Context()) != modeSession {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if _, ok := h.authenticate(r); ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Credentials pages must not sit in a shared cache or the browser's
	// back-forward cache after sign-out.
	w.Header().Set("Cache-Control", "no-store")
	_ = views.LoginPage(next, h.cfg.Version).Render(r.Context(), w)
}

type loginSignals struct {
	Username string `json:"loginuser"`
	Password string `json:"loginpass"`
	Next     string `json:"loginnext"`
}

// doLogin verifies credentials and starts a session.
//
// It answers over SSE (the form is a Datastar POST), so success is a
// client-side redirect rather than a 303: the browser must run the navigation
// itself for the newly-set cookie to be attached to the next request.
func (h *Handler) doLogin(w http.ResponseWriter, r *http.Request) {
	if h.mode(r.Context()) != modeSession {
		http.Error(w, "login is not enabled", http.StatusNotFound)
		return
	}
	sig := &loginSignals{}
	_ = datastar.ReadSignals(r, sig)

	username := strings.TrimSpace(sig.Username)
	next := safeNext(sig.Next)

	// The cookie must be set BEFORE the SSE body starts, so authenticate and
	// create the session first and only then open the event stream.
	u, err := h.store.Authenticate(r.Context(), username, sig.Password)
	if err != nil {
		// Every failure renders identically: a wrong password and an unknown
		// account must be indistinguishable.
		sse := datastar.NewSSE(w, r)
		_ = sse.PatchElementTempl(views.LoginError(errInvalidLogin.Error()))
		return
	}
	sess, err := h.store.CreateSession(r.Context(), u.ID, h.cfg.SessionTTL,
		r.UserAgent(), clientIP(r))
	if err != nil {
		sse := datastar.NewSSE(w, r)
		_ = sse.PatchElementTempl(views.LoginError("could not start a session: " + cleanErr(err)))
		return
	}
	h.setSessionCookie(w, r, sess)

	sse := datastar.NewSSE(w, r)
	_ = sse.Redirect(next)
}

// doLogout ends the current session. It is a plain form POST (not Datastar) so
// it works even if the page's JS is wedged, which is exactly when someone most
// wants to sign out.
func (h *Handler) doLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = h.store.DeleteSession(r.Context(), c.Value)
	}
	h.clearSessionCookie(w, r)
	http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
}

type changePasswordSignals struct {
	Current string `json:"curpass"`
	New     string `json:"newpass1"`
	Confirm string `json:"newpass2"`
}

// accountPassword is the self-service password change, available to any
// signed-in account including viewers.
//
// It re-checks the CURRENT password even though the caller is already
// authenticated: without that, an unattended browser is enough to take over the
// account permanently.
func (h *Handler) accountPassword(w http.ResponseWriter, r *http.Request) {
	sig := &changePasswordSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	id := identityOf(r)
	if id.Mode != modeSession {
		_ = sse.PatchElementTempl(views.InlineError("change-password-error",
			"Not available", "This deployment does not use stored accounts."))
		return
	}
	if sig.New != sig.Confirm {
		_ = sse.PatchElementTempl(views.InlineError("change-password-error",
			"Passwords do not match", "Retype the new password."))
		return
	}
	if _, err := h.store.Authenticate(r.Context(), id.Name, sig.Current); err != nil {
		_ = sse.PatchElementTempl(views.InlineError("change-password-error",
			"Incorrect password", "The current password is wrong."))
		return
	}
	if err := h.store.SetPassword(r.Context(), id.Name, sig.New); err != nil {
		_ = sse.PatchElementTempl(views.InlineError("change-password-error",
			"Could not change password", cleanErr(err)))
		return
	}

	// SetPassword deliberately invalidates every session for the user — which
	// includes this one. Mint a fresh session so the person who just rotated
	// their own password is not thrown back to the login form.
	if sess, err := h.store.CreateSession(r.Context(), h.userID(r, id.Name),
		h.cfg.SessionTTL, r.UserAgent(), clientIP(r)); err == nil {
		h.setSessionCookie(w, r, sess)
	}

	_ = sse.PatchElementTempl(views.ClearInline("change-password-error"))
	closeDialog(sse, "change-password")
	notify(sse, toast.VariantSuccess, "Password changed", "Your other sessions were signed out.")
}

// userID resolves a username to its id, returning 0 when it cannot (in which
// case the caller simply skips re-issuing the cookie and the user signs in
// again — annoying, never wrong).
func (h *Handler) userID(r *http.Request, username string) int64 {
	u, err := h.store.GetUser(r.Context(), username)
	if err != nil {
		return 0
	}
	return u.ID
}

// forbiddenPage renders the read-only refusal for a page a viewer may not see.
func (h *Handler) forbiddenPage(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionOverview, "Not permitted")
	m.Username = identityOf(r).Name
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = views.Page(m, views.InlineError("page-error", "Administrator access required",
		"This account is read-only. Ask an admin to change your role.")).Render(r.Context(), w)
}

// lockedPage explains the dashboard's safety refusal (see Handler.locked) and
// how to clear it, instead of leaving the operator with an inscrutable 403.
func (h *Handler) lockedPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = views.LockedPage(h.cfg.Version).Render(r.Context(), w)
}

// clientIP extracts the caller address for the session audit fields, honouring
// a single X-Forwarded-For hop.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

// storeErrTitle maps a store error to a short, human title for a toast.
func storeErrTitle(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "Not found"
	case errors.Is(err, store.ErrExists):
		return "Already exists"
	case errors.Is(err, store.ErrLastAdmin):
		return "Last admin"
	default:
		return "Failed"
	}
}
