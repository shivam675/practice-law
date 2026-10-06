package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/intelimek/megamoot/apps/api/internal/httpx"
)

type ctxKey int

const ctxKeyPrincipal ctxKey = iota

// Principal is the authenticated caller for one request. Permissions are
// resolved per request rather than carried in the token, so revoking a role
// takes effect immediately.
type Principal struct {
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	FamilyID       uuid.UUID
	Email          string
	FullName       string
	permissions    map[string]struct{}
}

func (p Principal) Can(permission string) bool {
	_, ok := p.permissions[permission]
	return ok
}

// CanAny reports whether the principal holds at least one of the permissions.
// Used where a broad and a scoped permission both grant access, such as
// submission.view and submission.view_own.
func (p Principal) CanAny(permissions ...string) bool {
	for _, perm := range permissions {
		if p.Can(perm) {
			return true
		}
	}
	return false
}

func (p Principal) Permissions() []string {
	out := make([]string, 0, len(p.permissions))
	for k := range p.permissions {
		out = append(out, k)
	}
	return out
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKeyPrincipal).(Principal)
	return p, ok
}

// MustPrincipal panics if no principal is present. Only call it inside routes
// mounted behind Authenticate.
func MustPrincipal(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("auth: no principal in context; route is missing Authenticate middleware")
	}
	return p
}

// Authenticate validates the bearer token, checks the token family has not
// been revoked, and loads the effective permission set.
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}

		claims, err := s.tokens.ParseAccess(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}
		orgID, err := uuid.Parse(claims.OrganizationID)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}
		familyID, err := uuid.Parse(claims.FamilyID)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}

		// An access token outlives a logout by up to its TTL unless the family
		// is checked. This is one indexed query; correctness beats saving it.
		revoked, err := s.store.FamilyRevoked(r.Context(), familyID)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		if revoked {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}

		user, err := s.store.UserByID(r.Context(), userID)
		if err != nil || user.Status != "active" || user.OrganizationID != orgID {
			httpx.Fail(w, r, httpx.ErrUnauthorized())
			return
		}

		perms, err := s.store.PermissionsForUser(r.Context(), userID)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}

		principal := Principal{
			UserID:         user.ID,
			OrganizationID: user.OrganizationID,
			FamilyID:       familyID,
			Email:          user.Email,
			FullName:       user.FullName,
			permissions:    perms,
		}

		ctx := context.WithValue(r.Context(), ctxKeyPrincipal, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
