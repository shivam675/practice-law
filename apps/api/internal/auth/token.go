package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims carries only identity. Permissions are resolved per request from the
// database so that revoking a role takes effect immediately rather than at the
// next token refresh.
type Claims struct {
	jwt.RegisteredClaims
	OrganizationID string `json:"org"`
	FamilyID       string `json:"fam"`
}

type TokenIssuer struct {
	key        []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenIssuer(key []byte, issuer string, accessTTL, refreshTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{key: key, issuer: issuer, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

func (t *TokenIssuer) AccessTTL() time.Duration  { return t.accessTTL }
func (t *TokenIssuer) RefreshTTL() time.Duration { return t.refreshTTL }

// IssueAccess returns a short-lived signed JWT. The client keeps it in memory,
// never in localStorage.
func (t *TokenIssuer) IssueAccess(userID, orgID, familyID uuid.UUID) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(t.accessTTL)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    t.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expires),
			ID:        uuid.NewString(),
		},
		OrganizationID: orgID.String(),
		FamilyID:       familyID.String(),
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}

func (t *TokenIssuer) ParseAccess(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(tok *jwt.Token) (any, error) {
			if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %v", tok.Header["alg"])
			}
			return t.key, nil
		},
		jwt.WithIssuer(t.issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, err
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

// NewRefreshToken returns an opaque token and the sha256 digest stored in the
// database. The plaintext exists only in the cookie; a database dump does not
// hand an attacker working sessions.
func NewRefreshToken() (plain string, digest []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plain))
	return plain, sum[:], nil
}

func HashRefreshToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
