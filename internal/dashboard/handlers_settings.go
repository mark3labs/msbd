package dashboard

// handlers_settings.go — the Settings section: REST API keys and dashboard
// accounts. Same shape as every other section: a page handler renders the full
// document, the /dashboard/api/* handlers patch the table fragment over SSE.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
	"github.com/mark3labs/msbd/internal/store"
)

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

func (h *Handler) pageKeys(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionKeys, "API keys")
	s := parseSort(r, "created")
	rows, err := h.keyRows(r.Context(), s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list API keys", err)
		return
	}
	h.render(w, r, views.SectionKeys, "API keys", "", views.KeysPage(rows, s))
}

func (h *Handler) keyTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "created")
	rows, err := h.keyRows(r.Context(), s)
	if notifyErr(sse, "List API keys", err) {
		return
	}
	_ = sse.PatchElementTempl(views.KeyTable(rows, s))
}

func (h *Handler) keyRows(ctx context.Context, s views.TableSort) ([]views.KeyRow, error) {
	keys, err := h.store.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.KeyRow, 0, len(keys))
	for i := range keys {
		k := &keys[i]
		rows = append(rows, views.KeyRow{
			ID:         strconv.FormatInt(k.ID, 10),
			Name:       k.Name,
			Prefix:     k.Prefix,
			Status:     k.Status(),
			CreatedBy:  k.CreatedBy,
			CreatedAt:  k.CreatedAt,
			LastUsedAt: k.LastUsedAt,
			ExpiresAt:  k.ExpiresAt,
			Active:     k.Active(),
		})
	}
	sortRows(rows, s, func(a, b views.KeyRow) bool {
		switch s.Col {
		case "name":
			return a.Name < b.Name
		case "used":
			return a.LastUsedAt.Before(b.LastUsedAt)
		default:
			return a.CreatedAt.Before(b.CreatedAt)
		}
	})
	return rows, nil
}

type createKeySignals struct {
	Name    string `json:"keyname"`
	Expires string `json:"keyexpires"`
}

func (h *Handler) keyCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createKeySignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.Name)
	if name == "" {
		_ = sse.PatchElementTempl(views.InlineError("create-key-error", "Name required",
			"Give the key a name so you can recognise it later."))
		return
	}
	ttl, err := parseKeyExpiry(sig.Expires)
	if failInline(sse, "create-key-error", "Create key", err) {
		return
	}

	key, raw, err := h.store.CreateAPIKey(r.Context(), name, ttl, identityOf(r).Name)
	if failInline(sse, "create-key-error", "Create key", err) {
		return
	}
	h.invalidateKeys()

	closeDialog(sse, "create-key")
	// Reveal the token before refreshing the table: this is the one and only
	// time it exists outside the database's hash.
	_ = sse.PatchElementTempl(views.NewKeyDialog(key.Name, raw))
	_ = sse.ExecuteScript("document.getElementById('new-key')?.showModal()")
	h.reRenderKeys(r, sse)
}

func (h *Handler) keyRevoke(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	k, err := h.store.RevokeAPIKey(r.Context(), r.PathValue("id"))
	if notifyErr(sse, storeErrTitle(err)+": revoke key", err) {
		return
	}
	h.invalidateKeys()
	notify(sse, toast.VariantSuccess, "Key revoked", k.Name+" no longer authenticates.")
	h.reRenderKeys(r, sse)
}

func (h *Handler) keyDelete(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	k, err := h.store.DeleteAPIKey(r.Context(), r.PathValue("id"))
	if notifyErr(sse, storeErrTitle(err)+": delete key", err) {
		return
	}
	h.invalidateKeys()
	notify(sse, toast.VariantSuccess, "Key deleted", k.Name)
	h.reRenderKeys(r, sse)
}

func (h *Handler) reRenderKeys(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "created")
	rows, err := h.keyRows(r.Context(), s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.KeyTable(rows, s))
}

// invalidateKeys drops the REST layer's token cache so a key created or revoked
// here takes effect on the next API request instead of after the cache TTL.
func (h *Handler) invalidateKeys() { h.cfg.KeyCache.Invalidate() }

// parseKeyExpiry accepts the day-suffixed values the dialog offers ("30d") plus
// any Go duration, mirroring `msbd keys create --expires`. Empty = never.
func parseKeyExpiry(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(rest)
		if err != nil || days <= 0 {
			return 0, errBadExpiry
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errBadExpiry
	}
	return d, nil
}

var errBadExpiry = errInvalidExpiry{}

type errInvalidExpiry struct{}

func (errInvalidExpiry) Error() string { return "invalid expiry (want e.g. 30d, 12h)" }

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func (h *Handler) pageUsers(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionUsers, "Users")
	s := parseSort(r, "username")
	rows, err := h.userRows(r, s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list users", err)
		return
	}
	h.render(w, r, views.SectionUsers, "Users", "", views.UsersPage(rows, s))
}

func (h *Handler) userTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "username")
	rows, err := h.userRows(r, s)
	if notifyErr(sse, "List users", err) {
		return
	}
	_ = sse.PatchElementTempl(views.UserTable(rows, s))
}

func (h *Handler) userRows(r *http.Request, s views.TableSort) ([]views.UserRow, error) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		return nil, err
	}
	me := identityOf(r).Name

	// The store refuses to delete or demote the last admin. Compute that here
	// too so the UI hides the buttons instead of offering a click that only
	// produces an error toast.
	admins := 0
	for i := range users {
		if users[i].Role == store.RoleAdmin {
			admins++
		}
	}

	rows := make([]views.UserRow, 0, len(users))
	for i := range users {
		u := &users[i]
		rows = append(rows, views.UserRow{
			Username:    u.Username,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt,
			LastLoginAt: u.LastLoginAt,
			Self:        strings.EqualFold(u.Username, me),
			Protected:   u.Role == store.RoleAdmin && admins == 1,
		})
	}
	sortRows(rows, s, func(a, b views.UserRow) bool {
		switch s.Col {
		case "role":
			return a.Role < b.Role
		case "created":
			return a.CreatedAt.Before(b.CreatedAt)
		case "login":
			return a.LastLoginAt.Before(b.LastLoginAt)
		default:
			return strings.ToLower(a.Username) < strings.ToLower(b.Username)
		}
	})
	return rows, nil
}

type createUserSignals struct {
	Username string `json:"newuser"`
	Password string `json:"newpass"`
	Role     string `json:"newrole"`
}

func (h *Handler) userCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createUserSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.Username)
	if name == "" {
		_ = sse.PatchElementTempl(views.InlineError("create-user-error", "Username required",
			"Pick a username for the new account."))
		return
	}
	u, err := h.store.CreateUser(r.Context(), name, sig.Password, sig.Role)
	if failInline(sse, "create-user-error", "Create user", err) {
		return
	}
	h.users.invalidate()

	closeDialog(sse, "create-user")
	notify(sse, toast.VariantSuccess, "User created", u.Username+" ("+u.Role+")")
	h.reRenderUsers(r, sse)
}

type setPasswordSignals struct {
	Username string `json:"pwuser"`
	Password string `json:"pwvalue"`
}

// userPassword is the admin reset. The target arrives in the signals rather
// than the path because the dialog is shared across rows.
func (h *Handler) userPassword(w http.ResponseWriter, r *http.Request) {
	sig := &setPasswordSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.Username)
	if name == "" {
		_ = sse.PatchElementTempl(views.InlineError("set-password-error", "No user selected",
			"Close the dialog and pick a user again."))
		return
	}
	if err := h.store.SetPassword(r.Context(), name, sig.Password); err != nil {
		_ = sse.PatchElementTempl(views.InlineError("set-password-error",
			storeErrTitle(err)+": set password", cleanErr(err)))
		return
	}
	_ = sse.PatchElementTempl(views.ClearInline("set-password-error"))
	closeDialog(sse, "set-password")
	notify(sse, toast.VariantSuccess, "Password set", name+" was signed out everywhere.")
	h.reRenderUsers(r, sse)
}

func (h *Handler) userRole(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	name := r.PathValue("name")
	role := r.URL.Query().Get("role")
	if err := h.store.SetRole(r.Context(), name, role); err != nil {
		notify(sse, toast.VariantError, storeErrTitle(err)+": change role", cleanErr(err))
		return
	}
	notify(sse, toast.VariantSuccess, "Role updated", name+" is now "+role)
	h.reRenderUsers(r, sse)
}

func (h *Handler) userDelete(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	name := r.PathValue("name")

	// Deleting yourself would drop your own session mid-request and strand the
	// page on a dead cookie. The table already hides the button; this is the
	// server-side half of the same rule.
	if strings.EqualFold(name, identityOf(r).Name) {
		notify(sse, toast.VariantError, "Cannot delete yourself",
			"Ask another admin to remove this account.")
		return
	}
	if err := h.store.DeleteUser(r.Context(), name); err != nil {
		notify(sse, toast.VariantError, storeErrTitle(err)+": delete user", cleanErr(err))
		return
	}
	h.users.invalidate()
	notify(sse, toast.VariantSuccess, "User deleted", name)
	h.reRenderUsers(r, sse)
}

func (h *Handler) reRenderUsers(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "username")
	rows, err := h.userRows(r, s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.UserTable(rows, s))
}
