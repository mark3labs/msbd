package dashboard

import (
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/msbd/internal/dashboard/views"
	"github.com/mark3labs/msbd/internal/store"
)

func TestWithIdentityViewPermissions(t *testing.T) {
	for _, id := range []identity{
		{Name: "viewer", Role: store.RoleViewer, Mode: modeSession},
		{Name: "admin", Role: store.RoleAdmin, Mode: modeSession},
		{Name: "basic", Role: store.RoleAdmin, Mode: modeBasic},
		{Role: store.RoleAdmin, Mode: modeOpen},
	} {
		t.Run(id.Name, func(t *testing.T) {
			original := httptest.NewRequest("GET", "/sandboxes", nil)
			r := withIdentity(original, id)
			if identityOf(r) != id {
				t.Fatal("view permission integration lost the authenticated identity")
			}
			if views.CanAdmin(r.Context()) != id.IsAdmin() {
				t.Fatal("view permission does not match request identity")
			}
			if !views.CanAdmin(original.Context()) {
				t.Fatal("request permission leaked to the parent context")
			}
		})
	}
}
