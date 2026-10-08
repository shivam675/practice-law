package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"
	"unicode/utf8"
)

func ValidateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < 10 || len(password) > 1024 {
		return httpx.ErrBadRequest("Password must contain at least 10 characters and be at most 1024 bytes.")
	}
	return nil
}
func canResetTarget(p Principal, platform bool) bool {
	return p.Can("user.edit") && (!platform || p.Can("platform.organization.manage"))
}
func validResetToken(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == 32
}
func invalidReset() error {
	return httpx.ErrBadRequest("This reset link is invalid or expired. Ask an administrator for a new link.")
}

func (h *Handlers) IssuePasswordReset(w http.ResponseWriter, r *http.Request) {
	p := MustPrincipal(r.Context())
	if !p.Can("user.edit") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid user id."))
		return
	}
	token, expires, err := h.svc.issuePasswordReset(r.Context(), p, userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.svc.audit.Record(r.Context(), audit.Entry{OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID, Action: "auth.password_reset.issue", TargetKind: "user", TargetID: &userID, RequestID: httpx.RequestIDFrom(r.Context())})
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"reset_path": "/reset-password#token=" + token, "expires_at": expires})
}
func (s *Service) issuePasswordReset(ctx context.Context, p Principal, userID uuid.UUID) (string, time.Time, error) {
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var platform bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id=ur.role_id WHERE ur.user_id=u.id AND ro.organization_id IS NULL) FROM users u WHERE u.id=$1 AND u.organization_id=$2 AND u.status IN ('active','invited') FOR UPDATE OF u`, userID, p.OrganizationID).Scan(&platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, httpx.ErrNotFound()
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if !canResetTarget(p, platform) {
		return "", time.Time{}, httpx.ErrForbidden()
	}
	token, digest, err := NewRefreshToken()
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE user_id=$1 AND organization_id=$2 AND used_at IS NULL`, userID, p.OrganizationID); err != nil {
		return "", time.Time{}, err
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `INSERT INTO password_reset_tokens(organization_id,user_id,issued_by,token_hash,expires_at) VALUES($1,$2,$3,$4,now()+interval '15 minutes') RETURNING expires_at`, p.OrganizationID, userID, p.UserID, digest).Scan(&expires)
	if err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}
func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := ValidateNewPassword(in.Password); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if !validResetToken(in.Token) {
		httpx.Fail(w, r, invalidReset())
		return
	}
	orgID, userID, err := h.svc.resetPassword(r.Context(), in.Token, in.Password)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.svc.audit.Record(r.Context(), audit.Entry{OrganizationID: &orgID, ActorUserID: &userID, Action: "auth.password_reset.complete", TargetKind: "user", TargetID: &userID, RequestID: httpx.RequestIDFrom(r.Context())})
	h.clearRefreshCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]bool{"reset": true})
}
func (s *Service) resetPassword(ctx context.Context, token, password string) (uuid.UUID, uuid.UUID, error) {
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var orgID, userID uuid.UUID
	// Resolve the opaque capability first, then lock the user before tokens to match issuance lock order.
	err = tx.QueryRow(ctx, `SELECT organization_id,user_id FROM password_reset_tokens WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now()`, HashRefreshToken(token)).Scan(&orgID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, invalidReset()
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$1 AND u.organization_id=$2 AND u.status IN ('active','invited') AND o.status='active' FOR UPDATE OF u`, userID, orgID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, invalidReset()
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	// A revoked administrator or newly promoted platform target invalidates an old delegated link.
	tag, err := tx.Exec(ctx, `UPDATE password_reset_tokens t SET used_at=now() WHERE t.token_hash=$1 AND t.organization_id=$2 AND t.user_id=$3 AND t.used_at IS NULL AND t.expires_at>now()
 AND EXISTS(SELECT 1 FROM users issuer JOIN user_roles ur ON ur.user_id=issuer.id JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE issuer.id=t.issued_by AND issuer.organization_id=t.organization_id AND issuer.status='active' AND rp.permission_key='user.edit')
 AND (NOT EXISTS(SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id=ur.role_id WHERE ur.user_id=t.user_id AND ro.organization_id IS NULL)
 OR EXISTS(SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE ur.user_id=t.issued_by AND rp.permission_key='platform.organization.manage'))`, HashRefreshToken(token), orgID, userID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if tag.RowsAffected() != 1 {
		return uuid.Nil, uuid.Nil, invalidReset()
	}
	hash, err := HashPassword(password)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("hash reset password: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$3,status='active',failed_login_count=0,locked_until=NULL WHERE id=$1 AND organization_id=$2`, userID, orgID, hash); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now(),revoked_reason='password_reset' WHERE user_id=$1 AND organization_id=$2 AND revoked_at IS NULL`, userID, orgID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE user_id=$1 AND organization_id=$2 AND used_at IS NULL`, userID, orgID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return orgID, userID, nil
}
