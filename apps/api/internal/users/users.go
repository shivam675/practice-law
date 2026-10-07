// Package users exposes organisation-scoped user administration.
//
// Every query is filtered by the caller's organization_id. There is no code
// path that reads a user from another tenant.
package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
)

type User struct {
	ID        uuid.UUID  `json:"id"`
	Email     string     `json:"email"`
	FullName  string     `json:"full_name"`
	Status    string     `json:"status"`
	Roles     []string   `json:"roles"`
	LastLogin *time.Time `json:"last_login_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type Handlers struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
}

func NewHandlers(pool *pgxpool.Pool, auditLog *audit.Logger) *Handlers {
	return &Handlers{pool: pool, audit: auditLog}
}

func (h *Handlers) Roles(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	rows, err := h.pool.Query(r.Context(), `SELECT key, name FROM roles WHERE organization_id = $1 ORDER BY name`, p.OrganizationID)
	if err != nil {
		httpx.Fail(w, r, fmt.Errorf("list roles: %w", err))
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			httpx.Fail(w, r, err)
			return
		}
		out = append(out, map[string]string{"key": key, "name": name})
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"roles": out})
}

// List returns organisation users. Mount behind authz.Require("user.view").
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	limit := clampInt(queryInt(r, "limit", 50), 1, 200)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	rows, err := h.pool.Query(r.Context(), `
		SELECT u.id, u.email, u.full_name, u.status, u.last_login_at, u.created_at,
		       coalesce(array_agg(ro.key) FILTER (WHERE ro.key IS NOT NULL), '{}')
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles ro ON ro.id = ur.role_id
		WHERE u.organization_id = $1
		  AND u.status <> 'deleted'
		  AND ($2 = '' OR u.full_name ILIKE '%' || $2 || '%' OR u.email ILIKE '%' || $2 || '%')
		GROUP BY u.id
		ORDER BY u.created_at DESC
		LIMIT $3 OFFSET $4`,
		p.OrganizationID, search, limit, offset)
	if err != nil {
		httpx.Fail(w, r, fmt.Errorf("list users: %w", err))
		return
	}
	defer rows.Close()

	out := make([]User, 0, limit)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.Status,
			&u.LastLogin, &u.CreatedAt, &u.Roles); err != nil {
			httpx.Fail(w, r, fmt.Errorf("scan user: %w", err))
			return
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, fmt.Errorf("read users: %w", err))
		return
	}

	httpx.JSON(w, r, http.StatusOK, map[string]any{"users": out, "limit": limit, "offset": offset})
}

type createRequest struct {
	Email    string   `json:"email"`
	FullName string   `json:"full_name"`
	Password string   `json:"password,omitempty"`
	Roles    []string `json:"roles"`
}

// Create adds a user. Mount behind authz.Require("user.create").
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	req.FullName = strings.TrimSpace(req.FullName)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		httpx.Fail(w, r, httpx.ErrBadRequest("A valid email address is required."))
		return
	}
	if req.FullName == "" {
		httpx.Fail(w, r, httpx.ErrBadRequest("Full name is required."))
		return
	}
	if len(req.Roles) == 0 {
		httpx.Fail(w, r, httpx.ErrBadRequest("At least one role is required."))
		return
	}
	if !p.Can("user.assign_role") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}

	// An invited user has no password until they set one. A seeded password is
	// allowed so a pilot can be set up without a mail server.
	var passwordHash any
	status := "invited"
	if req.Password != "" {
		if len(req.Password) < 10 {
			httpx.Fail(w, r, httpx.ErrBadRequest("Password must be at least 10 characters."))
			return
		}
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			httpx.Fail(w, r, fmt.Errorf("hash password: %w", err))
			return
		}
		passwordHash = hash
		status = "active"
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, fmt.Errorf("begin create user: %w", err))
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()

	var userID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO users (organization_id, email, email_normalized, full_name,
		                   password_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		p.OrganizationID, req.Email, auth.NormalizeEmail(req.Email),
		req.FullName, passwordHash, status).Scan(&userID)
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Fail(w, r, httpx.ErrConflict("A user with that email already exists."))
			return
		}
		httpx.Fail(w, r, fmt.Errorf("create user: %w", err))
		return
	}

	if err := assignRoles(r.Context(), tx, p.OrganizationID, userID, p.UserID, req.Roles); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, fmt.Errorf("commit create user: %w", err))
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID,
		ActorUserID:    &p.UserID,
		Action:         "user.create",
		TargetKind:     "user",
		TargetID:       &userID,
		After:          map[string]any{"email": req.Email, "roles": req.Roles, "status": status},
		RequestID:      httpx.RequestIDFrom(r.Context()),
	})

	httpx.JSON(w, r, http.StatusCreated, map[string]any{
		"id": userID, "email": req.Email, "full_name": req.FullName, "status": status,
	})
}

type setRolesRequest struct {
	Roles []string `json:"roles"`
}

// SetRoles replaces a user's roles. Mount behind
// authz.Require("user.assign_role").
func (h *Handlers) SetRoles(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid user id."))
		return
	}

	var req setRolesRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(req.Roles) == 0 {
		httpx.Fail(w, r, httpx.ErrBadRequest("At least one role is required."))
		return
	}

	// Removing your own last administrative role locks you out of the
	// organisation, so it is refused rather than silently applied.
	if userID == p.UserID {
		httpx.Fail(w, r, httpx.ErrBadRequest("You cannot change your own roles."))
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, fmt.Errorf("begin set roles: %w", err))
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()

	var exists bool
	if err := tx.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND organization_id = $2
		               AND status <> 'deleted')`,
		userID, p.OrganizationID).Scan(&exists); err != nil {
		httpx.Fail(w, r, fmt.Errorf("verify user: %w", err))
		return
	}
	if !exists {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}

	if _, err := tx.Exec(r.Context(), `DELETE FROM user_roles ur USING roles ro WHERE ur.role_id=ro.id AND ur.user_id=$1 AND ro.organization_id=$2`, userID, p.OrganizationID); err != nil {
		httpx.Fail(w, r, fmt.Errorf("clear roles: %w", err))
		return
	}
	if err := assignRoles(r.Context(), tx, p.OrganizationID, userID, p.UserID, req.Roles); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, fmt.Errorf("commit set roles: %w", err))
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID,
		ActorUserID:    &p.UserID,
		Action:         "user.assign_role",
		TargetKind:     "user",
		TargetID:       &userID,
		After:          map[string]any{"roles": req.Roles},
		RequestID:      httpx.RequestIDFrom(r.Context()),
	})

	httpx.JSON(w, r, http.StatusOK, map[string]any{"id": userID, "roles": req.Roles})
}

// assignRoles resolves role keys within the caller's organisation only, so a
// role id from another tenant cannot be granted.
func assignRoles(ctx context.Context, tx pgx.Tx, orgID, userID, grantedBy uuid.UUID, keys []string) error {
	// Validate every key before granting any. A partial match must not silently
	// drop a requested role.
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM roles WHERE organization_id = $1 AND key = ANY($2::text[])`, orgID, keys).Scan(&count); err != nil {
		return fmt.Errorf("validate roles: %w", err)
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	if count != len(wanted) {
		return httpx.ErrBadRequest("One or more roles do not exist in this organisation.")
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by)
		SELECT $1, r.id, $2
		FROM roles r
		WHERE r.organization_id = $3 AND r.key = ANY($4::text[])
		ON CONFLICT DO NOTHING`,
		userID, grantedBy, orgID, keys)
	if err != nil {
		return fmt.Errorf("assign roles: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrBadRequest("None of the supplied roles exist in this organisation.")
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
