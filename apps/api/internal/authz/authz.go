// Package authz turns permissions into route guards.
//
// Nothing here or anywhere else checks a role name. Roles are collections of
// permissions; the application only ever asks whether a permission is held.
package authz

import (
	"net/http"

	"github.com/slmlabs/megamoot/apps/api/internal/auth"
	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
)

// Require blocks the request unless the principal holds the permission.
func Require(permission string) func(http.Handler) http.Handler {
	return RequireAny(permission)
}

// RequireAny blocks the request unless the principal holds at least one of the
// permissions. Used where a broad and a self-scoped permission both grant
// access; the handler still narrows the query to the caller's own rows.
func RequireAny(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := auth.PrincipalFrom(r.Context())
			if !ok {
				httpx.Fail(w, r, httpx.ErrUnauthorized())
				return
			}
			if !p.CanAny(permissions...) {
				httpx.LoggerFrom(r.Context()).Warn("permission denied",
					"user_id", p.UserID, "required", permissions)
				httpx.Fail(w, r, httpx.ErrForbidden())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
