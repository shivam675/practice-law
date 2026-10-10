package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/slmlabs/megamoot/apps/api/internal/audit"
	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
)

const (
	loginFailureThreshold = 10
	loginLockDuration     = 15 * time.Minute

	// refreshReuseGrace is how long after a refresh a replay of the same token
	// is treated as the client racing itself rather than as theft.
	//
	// Strict rotation without this is unusable: two browser tabs restoring a
	// session at once, a double-clicked retry, or a React development double
	// mount all replay the same cookie within milliseconds and would log the
	// user out of both tabs. The cost is a narrow window in which a stolen
	// token still works, which is a far smaller risk than a product that signs
	// students out mid-assessment. Replays outside the window still revoke the
	// whole family. See docs/security.md.
	refreshReuseGrace = 10 * time.Second
)

type Service struct {
	store  *Store
	tokens *TokenIssuer
	audit  *audit.Logger
}

func NewService(store *Store, tokens *TokenIssuer, auditLog *audit.Logger) *Service {
	return &Service{store: store, tokens: tokens, audit: auditLog}
}

type LoginInput struct {
	Email            string
	Password         string
	OrganizationSlug string
	UserAgent        string
	IP               net.IP
	RequestID        string
}

type Tokens struct {
	Access        string
	AccessExpires time.Time
	Refresh       string
	RefreshExpire time.Time
	User          User
}

// errInvalidCredentials is deliberately identical for a missing account, a
// wrong password and a disabled account. Anything else is an enumeration
// oracle.
func errInvalidCredentials() *httpx.Error {
	return httpx.Err(http.StatusUnauthorized, "invalid_credentials",
		"Email or password is incorrect.")
}

func (s *Service) Login(ctx context.Context, in LoginInput) (Tokens, error) {
	emailNorm := NormalizeEmail(in.Email)

	users, err := s.store.UsersByEmail(ctx, emailNorm)
	if err != nil {
		return Tokens{}, err
	}

	user, err := s.selectUser(users, in.OrganizationSlug)
	if err != nil {
		// Spend the same time as a real verification so a missing account is
		// not distinguishable by latency.
		_, _ = VerifyPassword(dummyHash, in.Password)
		return Tokens{}, err
	}

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return Tokens{}, httpx.Err(http.StatusTooManyRequests, "account_locked",
			"Too many failed attempts. Try again later.")
	}
	if user.Status != "active" || user.PasswordHash == "" {
		_, _ = VerifyPassword(dummyHash, in.Password)
		return Tokens{}, errInvalidCredentials()
	}

	needsRehash, err := VerifyPassword(user.PasswordHash, in.Password)
	if err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			if ferr := s.store.RecordLoginFailure(ctx, user.ID,
				loginFailureThreshold, loginLockDuration); ferr != nil {
				httpx.LoggerFrom(ctx).Error("record login failure", "error", ferr)
			}
			s.audit.Record(ctx, audit.Entry{
				OrganizationID: &user.OrganizationID,
				ActorUserID:    &user.ID,
				Action:         "auth.login.failed",
				TargetKind:     "user",
				TargetID:       &user.ID,
				RequestID:      in.RequestID,
				IP:             in.IP,
			})
			return Tokens{}, errInvalidCredentials()
		}
		return Tokens{}, fmt.Errorf("verify password: %w", err)
	}

	if needsRehash {
		if newHash, herr := HashPassword(in.Password); herr == nil {
			if uerr := s.store.UpdatePasswordHash(ctx, user.ID, user.OrganizationID, newHash, user.PasswordHash); uerr != nil {
				httpx.LoggerFrom(ctx).Error("rehash password", "error", uerr)
			} else {
				user.PasswordHash = newHash
			}
		}
	}

	if err := s.store.RecordLoginSuccess(ctx, user.ID); err != nil {
		return Tokens{}, err
	}

	tokens, err := s.issue(ctx, user, uuid.New(), nil, in.UserAgent, in.IP)
	if err != nil {
		return Tokens{}, err
	}

	s.audit.Record(ctx, audit.Entry{
		OrganizationID: &user.OrganizationID,
		ActorUserID:    &user.ID,
		Action:         "auth.login",
		TargetKind:     "user",
		TargetID:       &user.ID,
		RequestID:      in.RequestID,
		IP:             in.IP,
	})

	return tokens, nil
}

// selectUser disambiguates when one email exists in several organisations.
func (s *Service) selectUser(users []User, slug string) (User, error) {
	switch {
	case len(users) == 0:
		return User{}, errInvalidCredentials()
	case len(users) == 1 && (slug == "" || strings.EqualFold(users[0].OrganizationSlug, slug)):
		return users[0], nil
	case slug == "":
		return User{}, httpx.Err(http.StatusConflict, "organization_required",
			"This email belongs to more than one organisation. Specify which one.")
	}
	for _, u := range users {
		if strings.EqualFold(u.OrganizationSlug, slug) {
			return u, nil
		}
	}
	return User{}, errInvalidCredentials()
}

// Refresh rotates a refresh token. A token presented twice means it leaked, so
// the entire family is revoked and the caller must log in again.
func (s *Service) Refresh(ctx context.Context, plain, userAgent string, ip net.IP, requestID string) (Tokens, error) {
	if plain == "" {
		return Tokens{}, httpx.ErrUnauthorized()
	}

	rec, err := s.store.RefreshTokenByHash(ctx, HashRefreshToken(plain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Tokens{}, httpx.ErrUnauthorized()
		}
		return Tokens{}, err
	}

	if rec.RevokedAt != nil || rec.ExpiresAt.Before(time.Now()) {
		return Tokens{}, httpx.ErrUnauthorized()
	}

	consumed, err := s.store.ConsumeRefreshToken(ctx, rec.ID)
	if err != nil {
		return Tokens{}, err
	}
	if !consumed {
		raced, werr := s.store.UsedWithin(ctx, rec.ID, refreshReuseGrace)
		if werr != nil {
			httpx.LoggerFrom(ctx).Error("check refresh reuse window", "error", werr)
		}

		if !raced {
			// Replay outside the grace window. Treat the family as compromised.
			if rerr := s.store.RevokeFamily(ctx, rec.FamilyID, "refresh_token_reuse"); rerr != nil {
				httpx.LoggerFrom(ctx).Error("revoke family after reuse", "error", rerr)
			}
			s.audit.Record(ctx, audit.Entry{
				OrganizationID: &rec.OrganizationID,
				ActorUserID:    &rec.UserID,
				Action:         "auth.refresh.reuse_detected",
				TargetKind:     "user",
				TargetID:       &rec.UserID,
				Reason:         "refresh token presented twice outside the grace window",
				RequestID:      requestID,
				IP:             ip,
			})
			return Tokens{}, httpx.Err(http.StatusUnauthorized, "token_reuse",
				"Session invalidated. Sign in again.")
		}

		// Inside the window: issue a fresh token in the same family rather
		// than ending the session. Recorded, because a burst of these is worth
		// looking at even when each one is benign.
		s.audit.Record(ctx, audit.Entry{
			OrganizationID: &rec.OrganizationID,
			ActorUserID:    &rec.UserID,
			Action:         "auth.refresh.raced",
			TargetKind:     "user",
			TargetID:       &rec.UserID,
			Reason:         "concurrent refresh inside the grace window",
			RequestID:      requestID,
			IP:             ip,
		})
	}

	user, err := s.store.UserByID(ctx, rec.UserID)
	if err != nil {
		return Tokens{}, httpx.ErrUnauthorized()
	}
	if user.Status != "active" {
		return Tokens{}, httpx.ErrUnauthorized()
	}

	parent := rec.ID
	return s.issue(ctx, user, rec.FamilyID, &parent, userAgent, ip)
}

func (s *Service) Logout(ctx context.Context, plain string) error {
	if plain == "" {
		return nil
	}
	rec, err := s.store.RefreshTokenByHash(ctx, HashRefreshToken(plain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	return s.store.RevokeFamily(ctx, rec.FamilyID, "logout")
}

func (s *Service) issue(ctx context.Context, user User, familyID uuid.UUID,
	parentID *uuid.UUID, userAgent string, ip net.IP) (Tokens, error) {

	access, accessExp, err := s.tokens.IssueAccess(user.ID, user.OrganizationID, familyID)
	if err != nil {
		return Tokens{}, err
	}

	plain, digest, err := NewRefreshToken()
	if err != nil {
		return Tokens{}, err
	}

	rec := RefreshRecord{
		ID:             uuid.New(),
		UserID:         user.ID,
		OrganizationID: user.OrganizationID,
		FamilyID:       familyID,
		ExpiresAt:      time.Now().Add(s.tokens.RefreshTTL()),
	}
	if err := s.store.InsertRefreshToken(ctx, rec, digest, parentID, userAgent, ip, user.PasswordHash); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Tokens{}, httpx.ErrUnauthorized()
		}
		return Tokens{}, err
	}

	return Tokens{
		Access:        access,
		AccessExpires: accessExp,
		Refresh:       plain,
		RefreshExpire: rec.ExpiresAt,
		User:          user,
	}, nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
