package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	OrganizationSlug string
	Email            string
	FullName         string
	PasswordHash     string
	Status           string
	LockedUntil      *time.Time
	FailedLoginCount int
}

type RefreshRecord struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	FamilyID       uuid.UUID
	ExpiresAt      time.Time
	UsedAt         *time.Time
	RevokedAt      *time.Time
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const userColumns = `
	u.id, u.organization_id, o.slug, u.email, u.full_name,
	coalesce(u.password_hash, ''), u.status, u.locked_until, u.failed_login_count`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrganizationID, &u.OrganizationSlug, &u.Email,
		&u.FullName, &u.PasswordHash, &u.Status, &u.LockedUntil, &u.FailedLoginCount)
	return u, err
}

// UsersByEmail returns every active-tenant user with this email. Email is
// unique per organisation, not globally, so one person may hold accounts at
// two institutions; the caller disambiguates by organisation slug.
func (s *Store) UsersByEmail(ctx context.Context, emailNormalized string) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+userColumns+`
		FROM users u
		JOIN organizations o ON o.id = u.organization_id
		WHERE u.email_normalized = $1
		  AND u.status <> 'deleted'
		  AND o.status = 'active'`, emailNormalized)
	if err != nil {
		return nil, fmt.Errorf("query users by email: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users u
		JOIN organizations o ON o.id = u.organization_id
		WHERE u.id = $1 AND u.status <> 'deleted'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user by id: %w", err)
	}
	return u, nil
}

// RecordLoginFailure increments the counter and locks the account once it
// crosses the threshold. Lockout is per account, and rate limiting per IP sits
// in front of it.
func (s *Store) RecordLoginFailure(ctx context.Context, userID uuid.UUID, threshold int, lockFor time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		SET failed_login_count = failed_login_count + 1,
		    locked_until = CASE WHEN failed_login_count + 1 >= $2
		                        THEN now() + $3::interval ELSE locked_until END
		WHERE id = $1`, userID, threshold, lockFor.String())
	if err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return nil
}

func (s *Store) RecordLoginSuccess(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		SET failed_login_count = 0, locked_until = NULL, last_login_at = now()
		WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("record login success: %w", err)
	}
	return nil
}

func (s *Store) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	return nil
}

func (s *Store) InsertRefreshToken(ctx context.Context, rec RefreshRecord, digest []byte,
	parentID *uuid.UUID, userAgent string, ip net.IP) error {

	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens
			(id, user_id, organization_id, family_id, token_hash, parent_id,
			 user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		rec.ID, rec.UserID, rec.OrganizationID, rec.FamilyID, digest, parentID,
		truncate(userAgent, 512), ipOrNil(ip), rec.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *Store) RefreshTokenByHash(ctx context.Context, digest []byte) (RefreshRecord, error) {
	var r RefreshRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, organization_id, family_id, expires_at, used_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, digest).
		Scan(&r.ID, &r.UserID, &r.OrganizationID, &r.FamilyID, &r.ExpiresAt, &r.UsedAt, &r.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshRecord{}, ErrNotFound
	}
	if err != nil {
		return RefreshRecord{}, fmt.Errorf("query refresh token: %w", err)
	}
	return r, nil
}

// ConsumeRefreshToken marks a token used exactly once. A second caller gets
// false, which is the signal that the token was replayed.
func (s *Store) ConsumeRefreshToken(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET used_at = now()
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()`, id)
	if err != nil {
		return false, fmt.Errorf("consume refresh token: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RevokeFamily kills every token descended from one login. Called on logout
// and, more importantly, on detected replay.
func (s *Store) RevokeFamily(ctx context.Context, familyID uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = now(), revoked_reason = $2
		WHERE family_id = $1 AND revoked_at IS NULL`, familyID, reason)
	if err != nil {
		return fmt.Errorf("revoke token family: %w", err)
	}
	return nil
}

func (s *Store) FamilyRevoked(ctx context.Context, familyID uuid.UUID) (bool, error) {
	var revoked bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM refresh_tokens
			WHERE family_id = $1 AND revoked_at IS NOT NULL
		)`, familyID).Scan(&revoked)
	if err != nil {
		return false, fmt.Errorf("check family revocation: %w", err)
	}
	return revoked, nil
}

// PermissionsForUser resolves the effective permission set. Roles are never
// checked by name anywhere in the application.
func (s *Store) PermissionsForUser(ctx context.Context, userID uuid.UUID) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("query permissions: %w", err)
	}
	defer rows.Close()

	perms := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		perms[key] = struct{}{}
	}
	return perms, rows.Err()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func ipOrNil(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}
