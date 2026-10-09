package views

import "context"

type adminContextKey struct{}

// WithAdmin carries dashboard mutation permission into pages and SSE fragments.
// This is presentation only; dashboard guards remain the authorization boundary.
func WithAdmin(ctx context.Context, admin bool) context.Context {
	return context.WithValue(ctx, adminContextKey{}, admin)
}

// CanAdmin defaults to true for standalone components and legacy open/basic
// rendering. Authenticated dashboard requests always set an explicit permission.
func CanAdmin(ctx context.Context) bool {
	admin, ok := ctx.Value(adminContextKey{}).(bool)
	return !ok || admin
}
