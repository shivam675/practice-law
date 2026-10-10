package auth

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
)

const refreshCookieName = "mm_refresh"

// refreshCookiePath scopes the cookie to the only endpoints that need it, so
// it is not attached to every API call.
const refreshCookiePath = "/api/v1/auth"

type Handlers struct {
	svc          *Service
	cookieDomain string
	cookieSecure bool
}

func NewHandlers(svc *Service, cookieDomain string, cookieSecure bool) *Handlers {
	return &Handlers{svc: svc, cookieDomain: cookieDomain, cookieSecure: cookieSecure}
}

func (h *Handlers) Routes(r chi.Router) {
	r.Post("/login", h.login)
	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
}

type loginRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Organization string `json:"organization,omitempty"`
}

type sessionResponse struct {
	AccessToken string      `json:"access_token"`
	ExpiresAt   time.Time   `json:"expires_at"`
	User        userPayload `json:"user"`
}

type userPayload struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	FullName       string `json:"full_name"`
	OrganizationID string `json:"organization_id"`
	Organization   string `json:"organization"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		httpx.Fail(w, r, httpx.ErrBadRequest("Email and password are required."))
		return
	}

	tokens, err := h.svc.Login(r.Context(), LoginInput{
		Email:            req.Email,
		Password:         req.Password,
		OrganizationSlug: strings.TrimSpace(req.Organization),
		UserAgent:        r.UserAgent(),
		IP:               clientIP(r),
		RequestID:        httpx.RequestIDFrom(r.Context()),
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.setRefreshCookie(w, tokens.Refresh, tokens.RefreshExpire)
	httpx.JSON(w, r, http.StatusOK, toSessionResponse(tokens))
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		httpx.Fail(w, r, httpx.ErrUnauthorized())
		return
	}

	tokens, err := h.svc.Refresh(r.Context(), cookie.Value, r.UserAgent(),
		clientIP(r), httpx.RequestIDFrom(r.Context()))
	if err != nil {
		h.clearRefreshCookie(w)
		httpx.Fail(w, r, err)
		return
	}

	h.setRefreshCookie(w, tokens.Refresh, tokens.RefreshExpire)
	httpx.JSON(w, r, http.StatusOK, toSessionResponse(tokens))
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			httpx.LoggerFrom(r.Context()).Error("logout", "error", err)
		}
	}
	h.clearRefreshCookie(w)
	httpx.JSON(w, r, http.StatusNoContent, nil)
}

// Me returns the caller's identity and effective permissions, which is what
// the client uses to decide which navigation to render.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	p := MustPrincipal(r.Context())
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"user": userPayload{
			ID:             p.UserID.String(),
			Email:          p.Email,
			FullName:       p.FullName,
			OrganizationID: p.OrganizationID.String(),
		},
		"permissions": p.Permissions(),
	})
}

func toSessionResponse(t Tokens) sessionResponse {
	return sessionResponse{
		AccessToken: t.Access,
		ExpiresAt:   t.AccessExpires,
		User: userPayload{
			ID:             t.User.ID.String(),
			Email:          t.User.Email,
			FullName:       t.User.FullName,
			OrganizationID: t.User.OrganizationID.String(),
			Organization:   t.User.OrganizationSlug,
		},
	}
}

func (h *Handlers) setRefreshCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		Domain:   h.cookieDomain,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handlers) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		Domain:   h.cookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clientIP trusts X-Forwarded-For only for its leftmost entry and only because
// the deployment always sits behind a reverse proxy that rewrites it.
func clientIP(r *http.Request) net.IP {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
